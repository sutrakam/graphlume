package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// decode reads the structured answer of a tool call.
func decode(t *testing.T, res *sdk.CallToolResult) Response {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// seedSecretFiles: one setting name defined three times, in an environment
// template, in a key file and in an ordinary settings file, plus the code that
// reads it. Every value is synthetic.
func seedSecretFiles(ctx context.Context, reg *registry.Registry, runID int64) {
	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		panic(err)
	}
	defer w.Close()
	add := func(path, lang, text string, kind model.EntityKind, name string, line int) {
		f := model.File{Path: path, Language: lang, ContentHash: "h" + path, ParseStatus: "ok"}
		id, _ := w.AddFile(ctx, &f)
		sym := model.Symbol{
			FileID: id, Kind: kind, Name: name, FQN: path + ":" + name, Exported: true,
			Range:      model.Range{Start: model.Position{Line: line}, End: model.Position{Line: line + 2}},
			Confidence: model.ConfidenceExact, Source: model.SourceCompiler,
		}
		if _, err := w.AddSymbol(ctx, &sym); err != nil {
			panic(err)
		}
		_ = w.IndexFileText(ctx, id, path, text)
	}
	add("deploy/.env.example", "dotenv", "LEDGER_DATABASE_PASSWORD=synthetic_pw_42\n", model.KindConfiguration, "LEDGER_DATABASE_PASSWORD", 1)
	add("deploy/ledger.key", "", "ledger database password key material\n", model.KindConfiguration, "LEDGER_DATABASE_PASSWORD", 1)
	add("deploy/settings.yml", "yaml", "ledger_database_password: ${LEDGER_DATABASE_PASSWORD}\n", model.KindConfiguration, "ledger_database_password", 1)
	add("ledger/rotate.go", "go", "package ledger\n\nfunc RotateLedgerDatabasePassword() error {\n\treturn nil\n}\n", model.KindFunction, "RotateLedgerDatabasePassword", 3)
	_ = w.Flush(ctx)
}

// TestContextNeverOffersSecretBearingFiles is the regression for a context
// pack that listed items from a tracked environment template: its variable
// names and path, in the pack, the preview and the download.
func TestContextNeverOffersSecretBearingFiles(t *testing.T) {
	sess := newGraphTestServer(t, seedSecretFiles)
	res := callTool(t, sess, "context", map[string]any{"task": "rotate the ledger database password", "limit": 50})
	if res.IsError {
		t.Fatalf("context failed:\n%s", resultText(res))
	}
	r := decode(t, res)
	text := resultText(res)
	var paths []string
	for _, it := range r.Items {
		paths = append(paths, it.Path)
		if strings.Contains(it.Path, ".env") || strings.HasSuffix(it.Path, ".key") {
			t.Errorf("a secret-bearing file was offered: %s", it.Path)
		}
	}
	for _, leak := range []string{".env.example", "ledger.key", "synthetic_pw_42"} {
		if strings.Contains(text, leak) {
			t.Errorf("the answer names %q:\n%s", leak, text)
		}
	}
	// The control: the same name in ordinary files is still offered, so the
	// filter removed the secret files and not the whole topic.
	if !strings.Contains(strings.Join(paths, " "), "ledger/rotate.go") {
		t.Errorf("the code that reads the setting is missing; got %v", paths)
	}
	// Relevance is the ranking the pack is in, and it is exposed.
	for i := 1; i < len(r.Items); i++ {
		if r.Items[i].Relevance > r.Items[i-1].Relevance {
			t.Errorf("items are not in relevance order at %d: %v > %v", i, r.Items[i].Relevance, r.Items[i-1].Relevance)
		}
	}
	if len(r.Items) > 0 && r.Items[0].Relevance <= 0 {
		t.Errorf("the top item carries no relevance score")
	}
	if r.Page == nil || r.Page.Total != len(r.Items) {
		t.Errorf("a paged context answer must state its total; page = %+v", r.Page)
	}
}

// TestBoundNeverExcerptsSecretBearingFiles: the central barrier every tool's
// answer passes, so definition, outline, explore and search cannot serve an
// environment template's lines either.
func TestBoundNeverExcerptsSecretBearingFiles(t *testing.T) {
	r := &Response{Items: []Item{
		{Kind: "configuration", Path: "svc/.env.production.template", Excerpt: "API_TOKEN=tok_synthetic_123\nDEBUG=1"},
		{Kind: "function", Path: "svc/app.py", Excerpt: `key = os.getenv("API_TOKEN", "tok_synthetic_456")`},
		{Kind: "file_outline", Path: ".env", Excerpt: "[REDACTED:sensitive-file] .env suppressed in full: 2 lines, 20 bytes."},
	}}
	r.Bound(DefaultLimits())
	if r.Items[0].Excerpt != "" {
		t.Errorf("an environment template was excerpted: %q", r.Items[0].Excerpt)
	}
	if strings.Contains(r.Items[1].Excerpt, "tok_synthetic_456") {
		t.Errorf("a credential default beside its name was served: %q", r.Items[1].Excerpt)
	}
	if !strings.HasPrefix(r.Items[2].Excerpt, "[REDACTED:sensitive-file]") {
		t.Errorf("the suppression notice itself must survive: %q", r.Items[2].Excerpt)
	}
}

// TestRenameRefusesReservedWords is the regression for a rename to a reserved
// word that came back as proven, safe edits.
func TestRenameRefusesReservedWords(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "callers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "callers", "provenCaller.java"),
		[]byte("class provenCaller {\n  void run() { new Login().authenticate(); }\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess := newGraphTestServerAt(t, root, seedMixedConfidence)
	for _, bad := range []string{"class", "while", "two words", "9lives"} {
		res := callTool(t, sess, "rename", map[string]any{"name": "Login", "path": "auth/Login.java", "new_name": bad})
		if !res.IsError {
			t.Errorf("rename to %q was accepted:\n%s", bad, resultText(res))
		}
	}
	res := callTool(t, sess, "rename", map[string]any{"name": "Login", "path": "auth/Login.java", "new_name": "SignIn"})
	if res.IsError {
		t.Fatalf("a valid name was refused:\n%s", resultText(res))
	}
	r := decode(t, res)
	if r.RenamePlan == nil || r.RenamePlan.Definition == nil {
		t.Fatalf("the plan must name the definition: %+v", r.RenamePlan)
	}
	// Each site carries its line, so a reviewer sees the identifier in place
	// even when the enclosing function is too long for an excerpt.
	found := false
	for _, e := range append(r.RenamePlan.Apply, r.RenamePlan.Review...) {
		if e.Path == "callers/provenCaller.java" && e.Line == 2 {
			found = true
			if !strings.Contains(e.Text, "new Login()") {
				t.Errorf("site text = %q, want the source line", e.Text)
			}
		}
	}
	if !found {
		t.Errorf("the proven site is missing from the plan: %+v", r.RenamePlan)
	}
}

func TestCheckNewName(t *testing.T) {
	cases := []struct {
		lang, name string
		ok         bool
	}{
		{"go", "range", false}, {"go", "Ranger", true}, {"go", "func", false}, {"go", "string", true},
		{"python", "lambda", false}, {"python", "None", false}, {"python", "lambda_", true},
		{"typescript", "delete", false}, {"typescript", "$store", true}, {"go", "$store", false},
		{"java", "synchronized", false}, {"c_sharp", "namespace", false}, {"rust", "fn", false},
		{"ruby", "valid?", true}, {"", "a b", false}, {"", "1st", false}, {"kotlin", "fun", false},
	}
	for _, c := range cases {
		if got := checkNewName(c.lang, c.name) == ""; got != c.ok {
			t.Errorf("checkNewName(%q, %q) ok = %v, want %v (%s)", c.lang, c.name, got, c.ok, checkNewName(c.lang, c.name))
		}
	}
}

func TestChangesRejectsAnUnknownKind(t *testing.T) {
	fs := newFullServer(t, seedMixedConfidence)
	res := callTool(t, fs.sess, "changes", map[string]any{"base": "HEAD~1", "kind": "everything"})
	if !res.IsError || !strings.Contains(resultText(res), "changed, file, reached or test") {
		t.Errorf("an unknown kind must be refused with the valid ones named:\n%s", resultText(res))
	}
}
