package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/acquire/fs"
	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/graph"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

// editFixture indexes calc.go with Add (lines 3-5) and Multiply (lines 7-9)
// and returns a server over it with editing allowed.
func editFixture(t *testing.T, refresher Refresher) (*Server, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	codeFile := filepath.Join(dir, "calc.go")
	if err := os.WriteFile(codeFile, []byte(editInitialContent), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Migrate(ctx, db.DB, time.Now().Unix); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	reg := registry.New(db)
	run, err := reg.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}
	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	f := model.File{Path: "calc.go", Language: "go", ContentHash: fs.HashBytes([]byte(editInitialContent)), ParseStatus: "ok"}
	fileID, err := w.AddFile(ctx, &f)
	if err != nil {
		t.Fatalf("add file: %v", err)
	}
	for _, sym := range []model.Symbol{
		{FileID: fileID, Name: "Add", Kind: model.KindFunction, FQN: "calc:Add",
			Range:      model.Range{Start: model.Position{Line: 3}, End: model.Position{Line: 5}},
			Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode},
		{FileID: fileID, Name: "Multiply", Kind: model.KindFunction, FQN: "calc:Multiply",
			Range:      model.Range{Start: model.Position{Line: 7}, End: model.Position{Line: 9}},
			Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode},
	} {
		if _, err := w.AddSymbol(ctx, &sym); err != nil {
			t.Fatalf("add symbol %s: %v", sym.Name, err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	_ = w.Close()
	if err := reg.CommitRun(ctx, run.ID); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	cfg := config.Defaults()
	cfg.RepoRoot = dir
	cfg.ProjectName = "calc"
	cfg.MCPProfile = config.MCPStandard
	cfg.AllowEdits = true

	srv, err := New(Deps{Registry: reg, Graph: graph.New(db), Config: cfg, Refresher: refresher})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv, codeFile
}

const editInitialContent = `package calc

func Add(a, b int) int {
	return a + b
}

func Multiply(a, b int) int {
	return a * b
}
`

// countingRefresher records Invalidate calls; Refresh does nothing.
type countingRefresher struct{ invalidated int }

func (c *countingRefresher) Refresh(context.Context) RefreshOutcome { return RefreshOutcome{} }
func (c *countingRefresher) Invalidate()                            { c.invalidated++ }

func TestEditingTools(t *testing.T) {
	ctx := context.Background()

	// 1-2. replace_symbol_body: a dry run writes nothing, a commit writes.
	ref := &countingRefresher{}
	srv, codeFile := editFixture(t, ref)
	_, resp, err := srv.replaceSymbolBody(ctx, nil, replaceSymbolBodyArgs{
		Name: "Add", NewBody: "\t// new logic\n\treturn a + b + 0", DryRun: true,
	})
	if err != nil || resp == nil {
		t.Fatalf("replace_symbol_body dry_run failed: %v", err)
	}
	if !strings.Contains(resp.Summary, "Replaced implementation body") {
		t.Errorf("unexpected summary: %s", resp.Summary)
	}
	if curr, _ := os.ReadFile(codeFile); string(curr) != editInitialContent {
		t.Errorf("dry_run modified file unexpectedly")
	}
	if ref.invalidated != 0 {
		t.Errorf("a dry run invalidated the index %d time(s)", ref.invalidated)
	}

	res, _, err := srv.replaceSymbolBody(ctx, nil, replaceSymbolBodyArgs{Name: "Add", NewBody: "\treturn a + b + 10"})
	if err != nil || (res != nil && res.IsError) {
		t.Fatalf("replace_symbol_body commit failed: %v %s", err, resultText(res))
	}
	curr, _ := os.ReadFile(codeFile)
	if !strings.Contains(string(curr), "return a + b + 10") {
		t.Errorf("expected modified file to contain new return statement:\n%s", curr)
	}
	if ref.invalidated != 1 {
		t.Errorf("a committed edit invalidated the index %d time(s), want 1", ref.invalidated)
	}

	// 3. A second write to the same file before it is re-indexed must be
	// refused: the index still holds the pre-edit line ranges.
	res, _, err = srv.insertAfterSymbol(ctx, nil, insertAfterSymbolArgs{
		Name: "Multiply", Code: "func Subtract(a, b int) int {\n\treturn a - b\n}",
	})
	if err != nil {
		t.Fatalf("insert_after_symbol: %v", err)
	}
	if res == nil || !res.IsError || !strings.Contains(resultText(res), "re-indexed") {
		t.Fatalf("an edit against a stale index was not refused with retry guidance: %s", resultText(res))
	}
	if after, _ := os.ReadFile(codeFile); string(after) != string(curr) {
		t.Error("the refused edit changed the file")
	}

	// 4. insert_after_symbol on a fresh index.
	srv, codeFile = editFixture(t, nil)
	res, _, err = srv.insertAfterSymbol(ctx, nil, insertAfterSymbolArgs{
		Name: "Multiply", Code: "func Subtract(a, b int) int {\n\treturn a - b\n}",
	})
	if err != nil || (res != nil && res.IsError) {
		t.Fatalf("insert_after_symbol failed: %v %s", err, resultText(res))
	}
	if curr, _ := os.ReadFile(codeFile); !strings.Contains(string(curr), "func Subtract") {
		t.Errorf("expected inserted Subtract function in file:\n%s", curr)
	}

	// 5. safe_delete_symbol on a fresh index: nothing depends on Multiply.
	srv, codeFile = editFixture(t, nil)
	res, _, err = srv.safeDeleteSymbol(ctx, nil, safeDeleteSymbolArgs{Name: "Multiply"})
	if err != nil || (res != nil && res.IsError) {
		t.Fatalf("safe_delete_symbol failed: %v %s", err, resultText(res))
	}
	if curr, _ := os.ReadFile(codeFile); strings.Contains(string(curr), "func Multiply") {
		t.Errorf("expected Multiply to be deleted from file:\n%s", curr)
	}
}

// A scope's role narrows the server's edit permission and never widens it. A
// viewer scope on a server started with edits allowed answers a write with a
// refusal that names the role; an editor scope writes.
func TestScopedRoleDecidesWhetherEditsWrite(t *testing.T) {
	ctx := context.Background()

	srv, codeFile := editFixture(t, &countingRefresher{})
	viewer, err := srv.ScopedAs(config.MCPFull, nil, Permissions{Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := viewer.replaceSymbolBody(ctx, nil, replaceSymbolBodyArgs{Name: "Add", NewBody: "\treturn a + b + 10"})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.IsError || !strings.Contains(resultText(res), "its role is viewer") {
		t.Fatalf("viewer write was not refused by role: %s", resultText(res))
	}
	if curr, _ := os.ReadFile(codeFile); string(curr) != editInitialContent {
		t.Fatal("a viewer scope wrote to disk")
	}

	editor, err := srv.ScopedAs(config.MCPFull, nil, Permissions{AllowEdits: true, Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	res, _, err = editor.replaceSymbolBody(ctx, nil, replaceSymbolBodyArgs{Name: "Add", NewBody: "\treturn a + b + 10"})
	if err != nil || (res != nil && res.IsError) {
		t.Fatalf("editor write failed: %v %s", err, resultText(res))
	}
	if curr, _ := os.ReadFile(codeFile); !strings.Contains(string(curr), "return a + b + 10") {
		t.Fatal("an editor scope did not write")
	}
}

// A role cannot grant what the server withheld: an editor scope on a server
// without --allow-edits is refused with the server's own reason.
func TestScopedRoleCannotWidenEdits(t *testing.T) {
	srv, _ := editFixture(t, &countingRefresher{})
	srv.deps.Config.AllowEdits = false
	editor, err := srv.ScopedAs(config.MCPFull, nil, Permissions{AllowEdits: true, Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	res, _, _ := editor.replaceSymbolBody(context.Background(), nil, replaceSymbolBodyArgs{Name: "Add", NewBody: "\treturn 0"})
	if res == nil || !res.IsError || !strings.Contains(resultText(res), "--allow-edits") {
		t.Fatalf("editor scope wrote on a server without edits: %s", resultText(res))
	}
}
