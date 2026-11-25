package registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// seedImportsAndRefs stages two files through the writer, the way the indexer
// does, and returns the run. Going through AddImport and AddReference rather
// than raw INSERTs keeps the reads under test honest about what the write path
// produces.
func seedImportsAndRefs(t *testing.T, r *Registry, extraRefs int) int64 {
	t.Helper()
	ctx := context.Background()
	run, err := r.BeginRun(ctx, "auto", "supplychain")
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}
	w, err := r.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	addFile := func(path, lang string) int64 {
		id, err := w.AddFile(ctx, &model.File{Path: path, Language: lang, ContentHash: "h-" + path, ParseStatus: "ok"})
		if err != nil {
			t.Fatalf("add file %s: %v", path, err)
		}
		return id
	}
	goFile := addFile("cmd/main.go", "go")
	jsFile := addFile("web/app.js", "javascript")

	for _, imp := range []struct {
		file int64
		Import
	}{
		{goFile, Import{Path: "github.com/spf13/cobra", Line: 4}},
		{goFile, Import{Path: "gopkg.in/yaml.v3", Alias: "y", Line: 5}},
		{jsFile, Import{Path: "lodash", Line: 1}},
	} {
		if err := w.AddImport(ctx, imp.file, imp.Import); err != nil {
			t.Fatalf("add import: %v", err)
		}
	}
	for _, ref := range []struct {
		file int64
		Reference
	}{
		{goFile, Reference{Name: "ExactArgs", Receiver: "cobra", Kind: "reference.call", Line: 12}},
		{goFile, Reference{Name: "Unmarshal", Receiver: "y", Kind: "reference.call", Line: 20}},
		{jsFile, Reference{Name: "require", Kind: "reference.call", Line: 2}},
		{jsFile, Reference{Name: "merge", Receiver: "_", Kind: "reference.call", Line: 5}},
	} {
		if err := w.AddReference(ctx, ref.file, ref.Reference); err != nil {
			t.Fatalf("add reference: %v", err)
		}
	}
	for i := 0; i < extraRefs; i++ {
		if err := w.AddReference(ctx, goFile, Reference{Name: fmt.Sprintf("fn%d", i), Kind: "reference.call", Line: 100 + i}); err != nil {
			t.Fatalf("add reference: %v", err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return run.ID
}

// TestImportSitesCarryTheirFileAndLanguage: a reachability answer must show
// where an import is, and the language is what stops an import in a Python
// file counting as evidence about an npm package.
func TestImportSitesCarryTheirFileAndLanguage(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	runID := seedImportsAndRefs(t, r, 0)

	sites, err := r.ImportSites(ctx, runID)
	if err != nil {
		t.Fatalf("ImportSites: %v", err)
	}
	want := []ImportSite{
		{File: "cmd/main.go", Language: "go", Path: "github.com/spf13/cobra", Line: 4},
		{File: "cmd/main.go", Language: "go", Path: "gopkg.in/yaml.v3", Alias: "y", Line: 5},
		{File: "web/app.js", Language: "javascript", Path: "lodash", Line: 1},
	}
	if len(sites) != len(want) {
		t.Fatalf("got %d import sites, want %d: %+v", len(sites), len(want), sites)
	}
	for i := range want {
		if sites[i] != want[i] {
			t.Errorf("site %d = %+v, want %+v", i, sites[i], want[i])
		}
	}

	// Another run's imports are not this run's.
	if other, err := r.ImportSites(ctx, runID+100); err != nil || len(other) != 0 {
		t.Errorf("ImportSites for an unknown run = %v, %v; want none", other, err)
	}
}

func TestReferencesNamedReturnsOnlyTheNamesAsked(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	runID := seedImportsAndRefs(t, r, 0)

	refs, err := r.ReferencesNamed(ctx, runID, []string{"ExactArgs", "require", "absent"})
	if err != nil {
		t.Fatalf("ReferencesNamed: %v", err)
	}
	want := []ReferenceSite{
		{File: "cmd/main.go", Name: "ExactArgs", Receiver: "cobra", Kind: "reference.call", Line: 12},
		{File: "web/app.js", Name: "require", Kind: "reference.call", Line: 2},
	}
	if len(refs) != len(want) {
		t.Fatalf("got %d references, want %d: %+v", len(refs), len(want), refs)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("reference %d = %+v, want %+v", i, refs[i], want[i])
		}
	}

	if none, err := r.ReferencesNamed(ctx, runID, nil); err != nil || len(none) != 0 {
		t.Errorf("ReferencesNamed with no names = %v, %v; want none and no error", none, err)
	}
}

// TestReferencesNamedChunksALongNameList: an advisory dump can name thousands
// of symbols, and SQLite bounds the parameters of one statement.
func TestReferencesNamedChunksALongNameList(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	const extra = referenceNameChunk*2 + 7
	runID := seedImportsAndRefs(t, r, extra)

	names := make([]string, 0, extra)
	for i := 0; i < extra; i++ {
		names = append(names, fmt.Sprintf("fn%d", i))
	}
	refs, err := r.ReferencesNamed(ctx, runID, names)
	if err != nil {
		t.Fatalf("ReferencesNamed: %v", err)
	}
	if len(refs) != extra {
		t.Errorf("got %d references across chunks, want %d", len(refs), extra)
	}
}
