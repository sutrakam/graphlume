package registry

import (
	"context"
	"reflect"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// goFixture is a tiny Go repository staged through the real writer: two
// modules, a package directory with a subdirectory, a test file, a method and
// an unexported function, and an importer file whose calls are part resolved,
// part not.
type goFixture struct {
	r     *Registry
	runID int64
	files map[string]int64
}

func newGoFixture(t *testing.T) goFixture {
	t.Helper()
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "auto", "test")
	if err != nil {
		t.Fatal(err)
	}
	w, err := r.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx := goFixture{r: r, runID: run.ID, files: map[string]int64{}}

	type sym struct {
		name, fqn string
		kind      model.EntityKind
		line      int
		exported  bool
	}
	add := func(path, lang, content string, syms []sym) int64 {
		id, err := w.AddFile(ctx, &model.File{Path: path, Language: lang, ContentHash: "h-" + path, ParseStatus: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if err := w.IndexFileText(ctx, id, path, content); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range syms {
			if _, err := w.AddSymbol(ctx, &model.Symbol{
				Kind: s.kind, Name: s.name, FQN: s.fqn, FileID: id,
				Range:    model.Range{Start: model.Position{Line: s.line}, End: model.Position{Line: s.line + 1}},
				Exported: s.exported, Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
			}); err != nil {
				t.Fatal(err)
			}
		}
		fx.files[path] = id
		return id
	}

	add("go.mod", "gomod", "module example.com/lib\n\ngo 1.22\n", nil)
	add("tools/go.mod", "gomod", "module example.com/lib/tools\n", nil)
	add("README.md", "markdown", "module docs", nil)
	add("lib.go", "go", "// Package lib.\n//go:build !js\n\n/* a block\ncomment */\npackage lib\n\nfunc Do() {}\n", []sym{
		{"Do", "lib.go:Do", model.KindFunction, 8, true},
		{"helper", "lib.go:helper", model.KindFunction, 10, false},
		{"Client", "lib.go:Client", model.KindClass, 12, true},
		{"Send", "lib.go:Client.Send", model.KindMethod, 14, true},
		{"close", "lib.go:Client.close", model.KindMethod, 16, false},
	})
	add("lib_test.go", "go", "package lib\n\nfunc TestHelper() {}\n", []sym{
		{"Do", "lib_test.go:Do", model.KindFunction, 3, true},
		{"Send", "lib_test.go:Fake.Send", model.KindMethod, 5, true},
	})
	add("sub/sub.go", "go", "package subpkg\n\nfunc Do() {}\n", []sym{
		{"Do", "sub/sub.go:Do", model.KindFunction, 3, true},
		{"Send", "sub/sub.go:T.Send", model.KindMethod, 5, true},
	})
	add("sub/deeper/d.go", "go", "package deeper\n\nfunc Do() {}\n", []sym{
		{"Do", "sub/deeper/d.go:Do", model.KindFunction, 3, true},
	})

	app := add("cmd/app/main.go", "go", "package main\n", []sym{
		{"run", "cmd/app/main.go:run", model.KindFunction, 5, false},
	})
	for _, imp := range []Import{
		{Path: "example.com/lib", Line: 3},
		{Path: "example.com/lib/sub", Alias: "s", Line: 4},
		{Path: "example.com/library", Line: 5},
		{Path: "fmt", Line: 6},
	} {
		if err := w.AddImport(ctx, app, imp); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []Reference{
		{Name: "Do", Kind: "reference.call", Receiver: "lib", ReceiverKind: model.ReceiverName, Line: 6, Col: 2},
		{Name: "Do", Kind: "reference.call", Receiver: "s", ReceiverKind: model.ReceiverName, Line: 7, Col: 2},
		{Name: "Println", Kind: "reference.call", Receiver: "fmt", ReceiverKind: model.ReceiverName, Line: 8, Col: 2},
		{Name: "run", Kind: "reference.call", ReceiverKind: model.ReceiverBare, Line: 9, Col: 2},
		{Name: "Client", Kind: "reference.type", Receiver: "lib", ReceiverKind: model.ReceiverName, Line: 10, Col: 2},
	} {
		if err := w.AddReference(ctx, app, ref); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// The bare call to run is answered inside the repository; the rest stay
	// unbound.
	var runRef int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT id FROM symbol_references WHERE name = 'run'`).Scan(&runRef); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkResolutions(ctx, []RefOutcome{{RefID: runRef, Resolution: ResolutionResolved}}); err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestGoModFilesReadsEveryModuleFromTheIndex(t *testing.T) {
	fx := newGoFixture(t)
	files, err := fx.r.GoModFiles(context.Background(), fx.runID)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if want := []string{"go.mod", "tools/go.mod"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("go.mod paths = %v, want %v", paths, want)
	}
	if files[0].Content != "module example.com/lib\n\ngo 1.22\n" {
		t.Fatalf("go.mod content = %q", files[0].Content)
	}
}

// Only unbound calls, only from files importing the module or one of its
// packages, and never through an import that merely shares a prefix.
func TestGoImportedCallsPairsUnboundCallsWithModuleImports(t *testing.T) {
	fx := newGoFixture(t)
	ctx := context.Background()
	calls, truncated, err := fx.r.GoImportedCalls(ctx, fx.runID, []string{"example.com/lib"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("truncated with the default limit")
	}
	type row struct {
		line             int
		name, recv, path string
	}
	var got []row
	for _, c := range calls {
		if c.FilePath != "cmd/app/main.go" || c.FileID != fx.files["cmd/app/main.go"] {
			t.Fatalf("call from %s (file %d)", c.FilePath, c.FileID)
		}
		got = append(got, row{c.Line, c.Name, c.Receiver, c.ImportPath})
	}
	// Each unbound call appears once per provider import of its file:
	// example.com/lib and example.com/lib/sub, never example.com/library or
	// fmt. The resolved bare call and the type reference are absent.
	want := []row{
		{6, "Do", "lib", "example.com/lib"}, {6, "Do", "lib", "example.com/lib/sub"},
		{7, "Do", "s", "example.com/lib"}, {7, "Do", "s", "example.com/lib/sub"},
		{8, "Println", "fmt", "example.com/lib"}, {8, "Println", "fmt", "example.com/lib/sub"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls =\n%v\nwant\n%v", got, want)
	}
	if calls[2].ImportAlias != "" || calls[3].ImportAlias != "s" {
		t.Fatalf("aliases = %q, %q", calls[2].ImportAlias, calls[3].ImportAlias)
	}

	cut, truncated, err := fx.r.GoImportedCalls(ctx, fx.runID, []string{"example.com/lib"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(cut) != 4 {
		t.Fatalf("limit 4: %d rows, truncated=%v", len(cut), truncated)
	}

	none, _, err := fx.r.GoImportedCalls(ctx, fx.runID, []string{"example.com/li"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("a module path that is only a prefix of an import matched %d rows", len(none))
	}
}

func TestGoPackageSymbolsIsOneDirectoryOfExportedPackageMembers(t *testing.T) {
	fx := newGoFixture(t)
	ctx := context.Background()
	names := func(hits []SymbolHit) []string {
		var out []string
		for _, h := range hits {
			out = append(out, h.FilePath+":"+h.Name)
		}
		return out
	}

	root, err := fx.r.GoPackageSymbols(ctx, fx.runID, "", []string{"Do", "helper", "Client", "Send"})
	if err != nil {
		t.Fatal(err)
	}
	// Not the test file's Do, not the unexported helper, not the method Send,
	// and nothing from the subdirectories.
	if got, want := names(root), []string{"lib.go:Client", "lib.go:Do"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root package = %v, want %v", got, want)
	}

	sub, err := fx.r.GoPackageSymbols(ctx, fx.runID, "sub", []string{"Do"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(sub), []string{"sub/sub.go:Do"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sub package = %v, want %v", got, want)
	}
}

func TestGoPackageNameReadsTheClausePastCommentsAndConstraints(t *testing.T) {
	fx := newGoFixture(t)
	ctx := context.Background()
	for dir, want := range map[string]string{"": "lib", "sub": "subpkg", "missing": ""} {
		got, err := fx.r.GoPackageName(ctx, fx.runID, dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("package name in %q = %q, want %q", dir, got, want)
		}
	}
}

// Methods are read per package directory, as declarations are: the root's
// own, never a test file's or a subdirectory's, and never an unexported one,
// which no other package can call.
func TestGoPackageMethodsIsOneDirectoryOfExportedMethods(t *testing.T) {
	fx := newGoFixture(t)
	ctx := context.Background()
	hits, err := fx.r.GoPackageMethods(ctx, fx.runID, "", []string{"Send", "close", "Do"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.FQN)
	}
	if want := []string{"lib.go:Client.Send"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root methods = %v, want %v", got, want)
	}
	sub, err := fx.r.GoPackageMethods(ctx, fx.runID, "sub", []string{"Send"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 1 || sub[0].FQN != "sub/sub.go:T.Send" {
		t.Fatalf("sub methods = %+v", sub)
	}
}

// A package's text is its own non-test Go files, in path order.
func TestGoPackageFilesReadsOnePackagesText(t *testing.T) {
	fx := newGoFixture(t)
	ctx := context.Background()
	for dir, want := range map[string][]string{
		"": {"lib.go"}, "sub": {"sub/sub.go"}, "sub/deeper": {"sub/deeper/d.go"}, "missing": nil,
	} {
		files, err := fx.r.GoPackageFiles(ctx, fx.runID, dir)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range files {
			got = append(got, f.Path)
			if f.Content == "" {
				t.Errorf("%s has no text", f.Path)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("files in %q = %v, want %v", dir, got, want)
		}
	}
}
