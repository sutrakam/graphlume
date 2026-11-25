package cli

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// mustToken returns a fresh random token. Tests never hold a literal one.
func mustToken(t *testing.T) string {
	t.Helper()
	tok, err := newTeamToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// entryLine renders one tokens-file line for a token.
func entryLine(t *testing.T, name, token, profile, expires string, tools ...string) string {
	t.Helper()
	b, err := json.Marshal(tokenLine{Name: name, SHA256: hashToken(token), Profile: profile, Tools: tools, Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeTokensFile(t *testing.T, mode os.FileMode, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "team-tokens.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is cut by the umask; the tests below depend on it.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokensFileAcceptsWhatCreateTokenWrites(t *testing.T) {
	ci, reviewer := mustToken(t), mustToken(t)
	path := writeTokensFile(t, 0o600,
		"# who may read the index",
		"",
		entryLine(t, "ci", ci, "minimal", "", "callers", "search", "callers"),
		"  "+entryLine(t, "reviewer", reviewer, "full", "2099-12-31")+"\r",
	)
	ts, warnings, err := loadTokensFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("a private, current file produced warnings: %v", warnings)
	}
	if len(ts.entries) != 2 {
		t.Fatalf("read %d entries, want 2", len(ts.entries))
	}

	got := ts.lookup(ci)
	if got == nil || got.Name != "ci" || got.Profile != "minimal" {
		t.Fatalf("lookup(ci) = %+v", got)
	}
	if strings.Join(got.Tools, ",") != "callers,search" {
		t.Errorf("tools = %v, want them sorted and without the duplicate", got.Tools)
	}
	if got := ts.lookup(reviewer); got == nil || got.Name != "reviewer" || len(got.Tools) != 0 {
		t.Errorf("lookup(reviewer) = %+v", got)
	}
	want := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	if at := ts.lookup(reviewer).Expires; !at.Equal(want) {
		t.Errorf("a date expiry ends at %v, want the end of that day, %v", at, want)
	}
}

func TestTokensFileWithABOMIsRead(t *testing.T) {
	tok := mustToken(t)
	entries, err := parseTokens([]byte("\xef\xbb\xbf" + entryLine(t, "ci", tok, "minimal", "") + "\r\n"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("parse = %v, %v", entries, err)
	}
}

// A file that cannot be read exactly must stop the server. Each of these is a
// way a tokens file goes wrong in practice, and each must be an error and not
// a line quietly skipped.
func TestMalformedTokensFilesAreRefused(t *testing.T) {
	tok := mustToken(t)
	good := entryLine(t, "ci", tok, "minimal", "")
	hash := hashToken(tok)

	cases := map[string]string{
		"empty file":              "",
		"only comments":           "# nothing yet\n\n",
		"not json":                "ci " + hash + " minimal",
		"a json array":            "[" + good + "]",
		"two entries on one line": good + " " + entryLine(t, "other", mustToken(t), "minimal", ""),
		"truncated line":          good[:len(good)-5],
		"unknown field":           `{"name":"ci","sha256":"` + hash + `","profile":"minimal","admin":true}`,
		"the token, not its hash": `{"name":"ci","sha256":"` + tok + `","profile":"minimal"}`,
		"a token field":           `{"name":"ci","token":"` + tok + `","profile":"minimal"}`,
		"short hash":              `{"name":"ci","sha256":"` + hash[:40] + `","profile":"minimal"}`,
		"no hash":                 `{"name":"ci","profile":"minimal"}`,
		"no name":                 `{"sha256":"` + hash + `","profile":"minimal"}`,
		"name with a space":       `{"name":"ci bot","sha256":"` + hash + `","profile":"minimal"}`,
		"name with a newline":     `{"name":"ci\nadmin","sha256":"` + hash + `","profile":"minimal"}`,
		"no profile":              `{"name":"ci","sha256":"` + hash + `"}`,
		"unknown profile":         `{"name":"ci","sha256":"` + hash + `","profile":"admin"}`,
		"profile in capitals":     `{"name":"ci","sha256":"` + hash + `","profile":"FULL"}`,
		"empty tools":             `{"name":"ci","sha256":"` + hash + `","profile":"minimal","tools":[]}`,
		"misspelt tool":           `{"name":"ci","sha256":"` + hash + `","profile":"minimal","tools":["find_calers"]}`,
		"tool beyond the profile": `{"name":"ci","sha256":"` + hash + `","profile":"minimal","tools":["hotspots"]}`,
		"tools is a string":       `{"name":"ci","sha256":"` + hash + `","profile":"minimal","tools":"callers"}`,
		"bad expiry":              `{"name":"ci","sha256":"` + hash + `","profile":"minimal","expires":"next week"}`,
		"expiry is a number":      `{"name":"ci","sha256":"` + hash + `","profile":"minimal","expires":1893456000}`,
		"impossible date":         `{"name":"ci","sha256":"` + hash + `","profile":"minimal","expires":"2027-02-30"}`,
		"duplicate name":          good + "\n" + entryLine(t, "ci", mustToken(t), "full", ""),
		"one token, two scopes":   good + "\n" + entryLine(t, "ci-wide", tok, "full", ""),
		"one bad line among good": good + "\n" + `{"name":"x"` + "\n" + entryLine(t, "ok", mustToken(t), "minimal", ""),
	}
	for name, content := range cases {
		entries, err := parseTokens([]byte(content))
		if err == nil {
			t.Errorf("%s: accepted, as %d entries", name, len(entries))
			continue
		}
		// The message goes to a terminal and often a log. If someone pasted a
		// token where a hash belongs, it must not be copied there.
		if strings.Contains(err.Error(), tok) {
			t.Errorf("%s: the error quotes the token: %v", name, err)
		}
	}
}

func TestMissingOrOddTokensFilesAreRefused(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := loadTokensFile(filepath.Join(dir, "absent.jsonl")); err == nil {
		t.Error("a missing tokens file was accepted")
	}
	if _, _, err := loadTokensFile(dir); err == nil {
		t.Error("a directory was accepted as a tokens file")
	}
	big := filepath.Join(dir, "big.jsonl")
	if err := os.WriteFile(big, []byte(strings.Repeat("#\n", tokensFileMaxBytes/2+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadTokensFile(big); err == nil {
		t.Error("an oversized tokens file was accepted")
	}
}

func TestLookupRefusesExpiredUnknownAndOddTokens(t *testing.T) {
	live, expiring := mustToken(t), mustToken(t)
	entries, err := parseTokens([]byte(
		entryLine(t, "live", live, "minimal", "") + "\n" +
			entryLine(t, "expiring", expiring, "full", "2027-01-31T17:00:00Z") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(2027, 1, 31, 17, 0, 0, 0, time.UTC)
	now := deadline.Add(-time.Second)
	ts := &tokenSet{entries: entries, now: func() time.Time { return now }}

	if got := ts.lookup(expiring); got == nil || got.Name != "expiring" {
		t.Fatalf("one second before the deadline: %+v", got)
	}
	now = deadline
	if ts.lookup(expiring) != nil {
		t.Error("the token was accepted at its expiry instant")
	}
	now = deadline.Add(48 * time.Hour)
	if ts.lookup(expiring) != nil {
		t.Error("the token was accepted after it expired")
	}
	if got := ts.lookup(live); got == nil || got.Name != "live" {
		t.Errorf("a token without an expiry stopped working: %+v", got)
	}

	for name, presented := range map[string]string{
		"empty":               "",
		"unknown":             mustToken(t),
		"the hash itself":     hashToken(live),
		"upper case":          strings.ToUpper(live),
		"with a trailing nul": live + "\x00",
		"a prefix":            live[:len(live)-1],
		"oversized":           strings.Repeat("a", maxPresentedTokenBytes+1),
	} {
		if got := ts.lookup(presented); got != nil {
			t.Errorf("%s: matched %q", name, got.Name)
		}
	}
	var none *tokenSet
	if none.lookup(live) != nil {
		t.Error("a nil token set matched")
	}
}

func TestTokensFileWarnsWhenOthersCanReadOrWriteIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits to check")
	}
	line := entryLine(t, "ci", mustToken(t), "minimal", "")
	for mode, want := range map[os.FileMode]string{
		0o600: "",
		0o400: "",
		0o640: "readable by other accounts",
		0o644: "readable by other accounts",
		0o604: "readable by other accounts",
		0o660: "writable by other accounts",
		0o666: "writable by other accounts",
		0o602: "writable by other accounts",
	} {
		path := writeTokensFile(t, mode, line)
		_, warnings, err := loadTokensFile(path)
		if err != nil {
			t.Fatalf("%04o: %v", mode, err)
		}
		got := strings.Join(warnings, "\n")
		if want == "" && got != "" {
			t.Errorf("%04o: unexpected warning: %s", mode, got)
		}
		if want != "" && !strings.Contains(got, want) {
			t.Errorf("%04o: warnings = %q, want one saying %q", mode, got, want)
		}
	}
}

func TestTokensFileWarnsAboutEntriesAlreadyExpired(t *testing.T) {
	path := writeTokensFile(t, 0o600,
		entryLine(t, "old", mustToken(t), "minimal", "2001-01-01"),
		entryLine(t, "current", mustToken(t), "minimal", "2099-01-01"))
	_, warnings, err := loadTokensFile(path)
	if err != nil {
		t.Fatalf("an expired entry must not stop the server: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, `"old"`) || strings.Contains(joined, `"current"`) {
		t.Errorf("warnings = %q, want one naming old and none naming current", joined)
	}
}

// create-token is the only code that ever writes a token out. It must show it
// once, store only the hash, and write a line the server accepts.
func TestCreateTokenPrintsTheSecretOnceAndStoresOnlyItsHash(t *testing.T) {
	root := isolate(t)
	path := filepath.Join(t.TempDir(), "team-tokens.jsonl")

	out, err := run(t, root, "--json", "serve", "create-token",
		"--name", "ci", "--profile", "standard", "--tool", "callers", "--tool", "impact",
		"--expires", "2099-06-30", "--tokens-file", path)
	if err != nil {
		t.Fatalf("create-token: %v\n%s", err, out)
	}
	var made struct {
		Name     string    `json:"name"`
		Token    string    `json:"token"`
		Entry    tokenLine `json:"entry"`
		Appended bool      `json:"appended"`
	}
	if err := json.Unmarshal([]byte(out), &made); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !strings.HasPrefix(made.Token, teamTokenPrefix) || len(made.Token) != len(teamTokenPrefix)+64 {
		t.Fatalf("token has the wrong shape (%d characters)", len(made.Token))
	}
	if !made.Appended || made.Entry.SHA256 != hashToken(made.Token) {
		t.Errorf("entry = %+v", made.Entry)
	}

	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), made.Token) {
		t.Fatal("the tokens file holds the token itself")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("tokens file created with mode %04o, want 0600", fi.Mode().Perm())
		}
	}

	// A second token, in the human-readable form.
	text, err := run(t, root, "serve", "create-token", "--name", "reviewer", "--profile", "full", "--tokens-file", path)
	if err != nil {
		t.Fatalf("second create-token: %v\n%s", err, text)
	}
	if strings.Count(text, teamTokenPrefix) != 1 {
		t.Errorf("the token should appear exactly once in the output:\n%s", text)
	}
	if !strings.Contains(text, "only time it is shown") || !strings.Contains(text, "restart") {
		t.Errorf("the output should say the token is shown once and that a restart loads it:\n%s", text)
	}

	ts, warnings, err := loadTokensFile(path)
	if err != nil {
		t.Fatalf("the server refuses the file create-token wrote: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings on a file create-token made: %v", warnings)
	}
	got := ts.lookup(made.Token)
	if got == nil || got.Name != "ci" || got.Profile != "standard" || strings.Join(got.Tools, ",") != "callers,impact" {
		t.Errorf("lookup of the created token = %+v", got)
	}
	if len(ts.entries) != 2 {
		t.Errorf("file has %d entries, want 2", len(ts.entries))
	}
}

func TestCreateTokenWithoutAFilePrintsTheEntry(t *testing.T) {
	root := isolate(t)
	out, err := run(t, root, "serve", "create-token", "--name", "bot")
	if err != nil {
		t.Fatalf("create-token: %v\n%s", err, out)
	}
	var token, line string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, teamTokenPrefix):
			token = l
		case strings.HasPrefix(l, "{"):
			line = l
		}
	}
	if token == "" || line == "" {
		t.Fatalf("output lacks the token or the entry:\n%s", out)
	}
	entries, err := parseTokens([]byte(line))
	if err != nil {
		t.Fatalf("the printed entry is not a valid tokens-file line: %v", err)
	}
	if entries[0].Name != "bot" || entries[0].Profile != "minimal" || hashToken(token) != hex.EncodeToString(entries[0].Hash[:]) {
		t.Errorf("entry = %+v", entries[0])
	}
}

func TestCreateTokenRefusesWhatTheServerWouldRefuse(t *testing.T) {
	root := isolate(t)
	path := filepath.Join(t.TempDir(), "team-tokens.jsonl")
	if out, err := run(t, root, "serve", "create-token", "--name", "ci", "--tokens-file", path); err != nil {
		t.Fatalf("create-token: %v\n%s", err, out)
	}
	before, _ := os.ReadFile(path)

	for name, args := range map[string][]string{
		"duplicate name":        {"--name", "ci", "--tokens-file", path},
		"no name":               {"--tokens-file", path},
		"bad name":              {"--name", "CI Bot", "--tokens-file", path},
		"unknown profile":       {"--name", "x", "--profile", "admin", "--tokens-file", path},
		"tool beyond profile":   {"--name", "x", "--profile", "minimal", "--tool", "hotspots", "--tokens-file", path},
		"misspelt tool":         {"--name", "x", "--tool", "find_calers", "--tokens-file", path},
		"expiry in the past":    {"--name", "x", "--expires", "2001-01-01", "--tokens-file", path},
		"unreadable expiry":     {"--name", "x", "--expires", "soon", "--tokens-file", path},
		"an argument, not flag": {"--name", "x", "extra", "--tokens-file", path},
	} {
		out, err := run(t, root, append([]string{"serve", "create-token"}, args...)...)
		if err == nil {
			t.Errorf("%s: accepted\n%s", name, out)
		}
		if strings.Contains(out, teamTokenPrefix) {
			t.Errorf("%s: a token was printed for an entry that was not stored:\n%s", name, out)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("a refused create-token changed the file:\n%s", after)
	}

	// A file the server would refuse is not appended to.
	broken := writeTokensFile(t, 0o600, `{"name":"half"`)
	if out, err := run(t, root, "serve", "create-token", "--name", "x", "--tokens-file", broken); err == nil {
		t.Errorf("appended to a malformed file:\n%s", out)
	}
	// A file holding only a comment is a fine place for the first token, and
	// one without a final newline must not end up with two entries on a line.
	fresh := filepath.Join(t.TempDir(), "fresh.jsonl")
	if err := os.WriteFile(fresh, []byte("# team tokens"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if out, err := run(t, root, "serve", "create-token", "--name", name, "--tokens-file", fresh); err != nil {
			t.Fatalf("create-token %s: %v\n%s", name, err, out)
		}
	}
	if ts, _, err := loadTokensFile(fresh); err != nil || len(ts.entries) != 2 {
		t.Errorf("after two appends: %v, %v", ts, err)
	}
}

// create-token gives the same warning the server does, while the operator is
// still looking at the file.
func TestCreateTokenWarnsAboutAnExposedTokensFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits to check")
	}
	root := isolate(t)
	path := writeTokensFile(t, 0o644, "# shared with the ops group")
	out, err := run(t, root, "serve", "create-token", "--name", "ci", "--tokens-file", path)
	if err != nil {
		t.Fatalf("create-token: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "readable by other accounts") {
		t.Errorf("no warning about a world-readable tokens file:\n%s", out)
	}
	// The file's mode is the operator's decision; the command does not change it.
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("create-token changed the file's mode to %04o", fi.Mode().Perm())
	}
}

// A line without a role is a viewer: a token written before roles existed, or
// without thought, must not gain write access. An unknown role is refused.
func TestTokenRoles(t *testing.T) {
	tok := mustToken(t)
	for _, tc := range []struct{ role, want string }{{"", roleViewer}, {"viewer", roleViewer}, {"editor", roleEditor}, {"admin", roleAdmin}} {
		e, err := checkTokenLine(tokenLine{Name: "ci", SHA256: hashToken(tok), Profile: "minimal", Role: tc.role})
		if err != nil || e.Role != tc.want {
			t.Fatalf("role %q: got %q, %v", tc.role, e.Role, err)
		}
	}
	if _, err := checkTokenLine(tokenLine{Name: "ci", SHA256: hashToken(tok), Profile: "minimal", Role: "root"}); err == nil {
		t.Fatal("unknown role accepted")
	}
	viewer := tokenEntry{Role: roleViewer}
	editor := tokenEntry{Role: roleEditor}
	admin := tokenEntry{Role: roleAdmin}
	if viewer.permissions().AllowEdits || !editor.permissions().AllowEdits || editor.permissions().ListAllProjects || !admin.permissions().ListAllProjects {
		t.Fatal("role permissions are wrong")
	}
	if viewer.scopeKey() == editor.scopeKey() {
		t.Fatal("two roles with one profile share a tool server")
	}
}
