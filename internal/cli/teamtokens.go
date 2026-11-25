package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/config"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
)

// ---------------------------------------------------------------- tokens file
//
// ONE INDEX, SEVERAL NAMED TOKENS, EACH WITH ITS OWN SCOPE.
//
// `graphlume serve --http` has one token: whoever holds it reads everything
// the server's profile exposes. A team sharing an index needs to hand out
// access a person or a bot at a time, give a CI job fewer tools than a
// reviewer, take one away without disturbing the rest, and read afterwards who
// asked what. That is a tokens file.
//
// The file is JSON Lines: one entry per line, so adding a token appends a line
// and never rewrites the ones already there.
//
//	{"name":"ci","sha256":"<64 hex>","profile":"minimal","tools":["callers"],"expires":"2027-01-31"}
//
// It holds the SHA-256 of each token and never a token. Tokens are 256 random
// bits (`graphlume serve create-token` makes them), so an unsalted hash is not
// guessable, and a copy of the file lets nobody in.
//
// The file is read ONCE, when the server starts. Adding, changing or revoking
// a token takes a restart. A server that re-read it on a timer would act on a
// half-written file, and one that re-read it on a signal has no signal on
// Windows; "restart to apply" is the behaviour that is the same everywhere and
// can be stated in one sentence.
//
// A file that cannot be read exactly stops the server. The alternative, to
// skip the line that does not parse, turns a typo in an expiry date into a
// token that never expires.

// tokensFileMaxBytes bounds the tokens file. An entry is about 150 bytes, so
// this is room for several thousand tokens; a larger file is not a tokens file.
const tokensFileMaxBytes = 1 << 20

// teamTokenPrefix marks a token made by `serve create-token`, so a secret
// scanner and a person reading a log can both tell what leaked.
const teamTokenPrefix = "rcx_"

// maxPresentedTokenBytes bounds what is hashed per request. A real token is 68
// bytes; nothing longer can match, so nothing longer is worth a hash.
const maxPresentedTokenBytes = 512

// tokenNamePattern keeps a name safe to print in a terminal, a log line and an
// audit record: no spaces, no control characters, nothing to escape.
var tokenNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// tokenLine is one entry as written in the file.
type tokenLine struct {
	Name    string   `json:"name"`
	SHA256  string   `json:"sha256"`
	Profile string   `json:"profile"`
	Tools   []string `json:"tools,omitempty"`
	Expires string   `json:"expires,omitempty"`
	Role    string   `json:"role,omitempty"`
}

// Token roles. A role decides what a token may do beyond calling its tools;
// the profile and tool list decide which tools it may call.
//
//   - viewer: read only. Editing tools answer with a preview and never write,
//     even on a server started with --allow-edits. The default, so a token
//     written before roles existed, or without thought, cannot change code.
//   - editor: editing tools write when the server allows edits.
//   - admin: editor, and the projects action lists every project registered
//     on the server's machine instead of only the one being served.
const (
	roleViewer = "viewer"
	roleEditor = "editor"
	roleAdmin  = "admin"
)

func validRole(r string) bool { return r == roleViewer || r == roleEditor || r == roleAdmin }

// tokenEntry is one entry, checked.
type tokenEntry struct {
	Name    string
	Hash    [sha256.Size]byte
	Profile config.MCPProfile
	// Tools is the allow-list within the profile, sorted. Empty means the
	// whole profile.
	Tools []string
	// Expires is the first instant the token is no longer accepted. Zero
	// means it does not expire.
	Expires time.Time
	// Role is viewer, editor or admin; viewer when the line names none.
	Role string
}

// permissions is what the token's role allows beyond its tools.
func (e *tokenEntry) permissions() devmcp.Permissions {
	return devmcp.Permissions{
		AllowEdits:      e.Role == roleEditor || e.Role == roleAdmin,
		ListAllProjects: e.Role == roleAdmin,
		Role:            e.Role,
	}
}

// scopeKey identifies the entry's scope, so entries with the same profile and
// allow-list share one tool server.
func (e *tokenEntry) scopeKey() string {
	return e.Role + "\x00" + string(e.Profile) + "\x00" + strings.Join(e.Tools, "\x00")
}

// describeScope is the scope in words, for start-up output.
func (e *tokenEntry) describeScope() string {
	s := "role " + e.Role + ", profile " + string(e.Profile)
	if len(e.Tools) > 0 {
		s += ", only " + strings.Join(e.Tools, ", ")
	}
	if e.Expires.IsZero() {
		return s + ", no expiry"
	}
	return s + ", expires " + e.Expires.UTC().Format(time.RFC3339)
}

// tokenSet is the tokens a server accepts.
type tokenSet struct {
	entries []tokenEntry
	now     func() time.Time
}

// lookup returns the entry a presented token belongs to, or nil.
//
// Nil covers every failure alike: no such token, an expired one, an empty or
// oversized string. The caller answers all of them with the same 401, because
// "that token exists but has expired" tells someone holding a stolen token
// that it was real.
//
// The presented token is hashed and the hash compared with every entry in
// constant time, without stopping at a match, so the time taken does not
// depend on which entry matched or on how much of a hash was right.
func (ts *tokenSet) lookup(presented string) *tokenEntry {
	if ts == nil || presented == "" || len(presented) > maxPresentedTokenBytes {
		return nil
	}
	sum := sha256.Sum256([]byte(presented))
	found := -1
	for i := range ts.entries {
		if subtle.ConstantTimeCompare(sum[:], ts.entries[i].Hash[:]) == 1 {
			found = i
		}
	}
	if found < 0 {
		return nil
	}
	e := &ts.entries[found]
	if !e.Expires.IsZero() && !ts.now().Before(e.Expires) {
		return nil
	}
	return e
}

// loadTokensFile reads and checks a tokens file. warnings are for the operator
// and never stop the server; an error always does.
func loadTokensFile(path string) (ts *tokenSet, warnings []string, err error) {
	// Stat before Open: opening a named pipe would block until something
	// wrote to it, and a server that hangs at start is a worse answer than one
	// that says the file is not a file.
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("tokens file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("tokens file %s is not a regular file", path)
	}
	f, err := os.Open(path) //nolint:gosec // G304: the path is the operator's own --tokens-file value
	if err != nil {
		return nil, nil, fmt.Errorf("tokens file: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, tokensFileMaxBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("tokens file %s: %w", path, err)
	}
	if len(data) > tokensFileMaxBytes {
		return nil, nil, fmt.Errorf("tokens file %s is over %d bytes", path, tokensFileMaxBytes)
	}

	entries, err := parseTokens(data)
	if err != nil {
		return nil, nil, fmt.Errorf("tokens file %s: %w", path, err)
	}
	ts = &tokenSet{entries: entries, now: time.Now}

	if w := tokensFileModeWarning(path, fi.Mode()); w != "" {
		warnings = append(warnings, w)
	}
	now := ts.now()
	for i := range entries {
		if e := &entries[i]; !e.Expires.IsZero() && !now.Before(e.Expires) {
			warnings = append(warnings, fmt.Sprintf("token %q expired on %s and will be refused",
				e.Name, e.Expires.UTC().Format(time.RFC3339)))
		}
	}
	return ts, warnings, nil
}

// tokensFileModeWarning reports a tokens file other accounts on the machine
// can read or change. It is a warning and not a refusal because the file holds
// hashes, not tokens, and because the operator may have arranged group access
// on purpose; it is said every start so that it is a decision and not an
// accident.
//
// Windows has no permission bits to read this from, so nothing is checked
// there.
func tokensFileModeWarning(path string, mode os.FileMode) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	perm := mode.Perm()
	switch {
	case perm&0o022 != 0:
		return fmt.Sprintf("tokens file %s is writable by other accounts (mode %04o): anyone who can write it can add a token for themselves. Run: chmod 600 %s",
			path, perm, path)
	case perm&0o044 != 0:
		return fmt.Sprintf("tokens file %s is readable by other accounts (mode %04o): it holds hashes, not tokens, but it names who has access. Run: chmod 600 %s",
			path, perm, path)
	}
	return ""
}

// parseTokens checks every line of a tokens file. Blank lines and lines
// starting with # are skipped; anything else must be a complete, valid entry.
//
// Errors name the line and the field and never quote the line: an operator who
// pasted a token where its hash belongs must not have it copied into a log.
func parseTokens(data []byte) ([]tokenEntry, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // a BOM from a Windows editor

	var out []tokenEntry
	names := map[string]int{}
	hashes := map[[sha256.Size]byte]int{}
	for i, raw := range bytes.Split(data, []byte("\n")) {
		n := i + 1
		line := bytes.TrimSpace(raw)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		e, err := parseTokenLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if first, dup := names[e.Name]; dup {
			return nil, fmt.Errorf("line %d: the name %q is already used on line %d", n, e.Name, first)
		}
		if first, dup := hashes[e.Hash]; dup {
			return nil, fmt.Errorf("line %d: the same token is already listed on line %d; one token cannot have two scopes", n, first)
		}
		names[e.Name], hashes[e.Hash] = n, n
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, errors.New("it lists no tokens, so nobody could use the server; add one with 'graphlume serve create-token'")
	}
	return out, nil
}

func parseTokenLine(line []byte) (tokenEntry, error) {
	var raw tokenLine
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		// The decoder's own message is passed on only when it names a field,
		// which is safe; a syntax error could quote the line.
		if msg := err.Error(); strings.Contains(msg, "unknown field") {
			return tokenEntry{}, fmt.Errorf("%s; the fields are name, sha256, profile, tools and expires",
				strings.TrimPrefix(msg, "json: "))
		}
		return tokenEntry{}, errors.New("not a JSON object with the fields name, sha256, profile, tools and expires")
	}
	if dec.More() {
		return tokenEntry{}, errors.New("more than one JSON value on the line; write one entry per line")
	}
	return checkTokenLine(raw)
}

// checkTokenLine validates one entry. It is shared by the file reader and by
// `serve create-token`, so the command cannot write a line the server refuses.
func checkTokenLine(raw tokenLine) (tokenEntry, error) {
	e := tokenEntry{Name: raw.Name, Profile: config.MCPProfile(raw.Profile)}

	if !tokenNamePattern.MatchString(raw.Name) {
		return e, errors.New("name must be 1 to 63 characters: lower-case letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	sum, err := hex.DecodeString(raw.SHA256)
	if err != nil || len(sum) != sha256.Size {
		return e, fmt.Errorf("token %q: sha256 must be the 64 hexadecimal characters of the token's SHA-256, never the token itself", raw.Name)
	}
	copy(e.Hash[:], sum)

	if !e.Profile.Valid() {
		return e, fmt.Errorf("token %q: profile must be minimal, standard or full", raw.Name)
	}
	if raw.Tools != nil {
		if len(raw.Tools) == 0 {
			return e, fmt.Errorf("token %q: tools is empty, which would allow nothing; leave it out to allow the whole profile", raw.Name)
		}
		onProfile := map[string]bool{}
		for _, t := range devmcp.ToolsOf(e.Profile) {
			onProfile[t] = true
		}
		seen := map[string]bool{}
		for _, t := range raw.Tools {
			if !devmcp.ValidToolName(t) {
				// Not echoed: whatever it is, it is not a tool name.
				return e, fmt.Errorf("token %q: tools holds a value that is not a tool name; names look like callers", raw.Name)
			}
			if !onProfile[t] {
				return e, fmt.Errorf("token %q: %q is not a tool on the %s profile; 'graphlume tool list --profile %s' lists them",
					raw.Name, t, e.Profile, e.Profile)
			}
			if !seen[t] {
				seen[t] = true
				e.Tools = append(e.Tools, t)
			}
		}
		sort.Strings(e.Tools)
	}
	e.Role = raw.Role
	if e.Role == "" {
		e.Role = roleViewer
	}
	if !validRole(e.Role) {
		return e, fmt.Errorf("token %q: role must be viewer, editor or admin", raw.Name)
	}
	if raw.Expires != "" {
		at, err := parseTokenExpiry(raw.Expires)
		if err != nil {
			return e, fmt.Errorf("token %q: %w", raw.Name, err)
		}
		e.Expires = at
	}
	return e, nil
}

// parseTokenExpiry reads an expiry as a date or as a timestamp.
//
// A date means the token works THROUGH that day in UTC and stops at the next
// midnight: "expires 2027-01-31" read as "dead on the morning of the 31st"
// would surprise everyone who has ever read a date on a card. A timestamp is
// the exact instant.
func parseTokenExpiry(s string) (time.Time, error) {
	if day, err := time.Parse("2006-01-02", s); err == nil {
		return day.AddDate(0, 0, 1), nil
	}
	if at, err := time.Parse(time.RFC3339, s); err == nil {
		return at.UTC(), nil
	}
	return time.Time{}, errors.New("expires must be a date such as 2027-01-31 (valid through that day, UTC) or an RFC 3339 time such as 2027-01-31T17:00:00Z")
}

// hashToken is the stored form of a token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newTeamToken returns a fresh token: 256 random bits behind a fixed prefix.
func newTeamToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return teamTokenPrefix + hex.EncodeToString(b), nil
}

// appendTokenLine adds one entry to a tokens file, creating it readable only
// by the user. Lines already in the file are never rewritten.
//
// The existing file is checked first. Appending to a file the server would
// refuse hides the real problem behind a new one, and appending a name that is
// already there makes the file unreadable for the next start.
func appendTokenLine(path string, raw tokenLine) error {
	var existing []byte
	switch fi, err := os.Stat(path); {
	case errors.Is(err, os.ErrNotExist):
		// The first token: the file is created below.
	case err != nil:
		return fmt.Errorf("tokens file: %w", err)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("tokens file %s is not a regular file", path)
	case fi.Size() > tokensFileMaxBytes:
		return fmt.Errorf("tokens file %s is over %d bytes", path, tokensFileMaxBytes)
	default:
		if existing, err = os.ReadFile(path); err != nil { //nolint:gosec // G304: the path is the operator's own --tokens-file value
			return fmt.Errorf("tokens file: %w", err)
		}
	}
	// A file holding only comments is a fine place for the first token.
	if !isNoTokens(existing) {
		entries, err := parseTokens(existing)
		if err != nil {
			return fmt.Errorf("tokens file %s: %w; fix it before adding to it", path, err)
		}
		for _, e := range entries {
			if e.Name == raw.Name {
				return fmt.Errorf("tokens file %s already has a token named %q; choose another name, or remove that line first", path, raw.Name)
			}
		}
	}

	line, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode token entry: %w", err)
	}
	var b bytes.Buffer
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.Write(line)
	b.WriteByte('\n')

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // G304: the path is the operator's own --tokens-file value
	if err != nil {
		return fmt.Errorf("tokens file: %w", err)
	}
	if _, err := f.Write(b.Bytes()); err != nil {
		_ = f.Close()
		return fmt.Errorf("write tokens file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write tokens file %s: %w", path, err)
	}
	return nil
}

// isNoTokens reports whether a file holds only comments and blank lines, which
// is a valid file to append the first token to.
func isNoTokens(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	for _, raw := range bytes.Split(data, []byte("\n")) {
		if line := bytes.TrimSpace(raw); len(line) > 0 && line[0] != '#' {
			return false
		}
	}
	return true
}
