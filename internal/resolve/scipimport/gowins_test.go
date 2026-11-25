package scipimport

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/scip"
	"github.com/sutrakam/graphlume/pkg/model"
)

// TestGoTypesOverridesAScipGoEdgeItContradicts is the guarantee behind "the Go
// type check wins": resolve never runs scip-go, but a user may put an index.scip
// from it in the repository, and that file is imported before the type check
// runs. Where the two disagree about a call the type check has resolved, the
// scip-go edge must go, or the graph would hold two exact answers to one call.
func TestGoTypesOverridesAScipGoEdgeItContradicts(t *testing.T) {
	ctx := context.Background()
	reg, runID, ids := repoIndex(t)
	// The parser saw a call to updateStatus on line 7, so the scip-go reference
	// there is promoted to a CALLS edge.
	files0, err := reg.Files(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files0 {
		if f.Path == apiPath {
			if err := reg.ReplaceFileReferences(ctx, runID, f.ID, []registry.Reference{
				{Name: "updateStatus", Kind: "reference.call", Line: 7, Col: 8, ArgCount: model.ArityUnknown},
			}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := NewImporter(reg, runID, scip.Options{RepoRoot: "/build/repo"}).
		Import(ctx, scipFile(t, serviceDoc(), handlerDoc())); err != nil {
		t.Fatal(err)
	}
	if n := countEdges(t, reg, runID, ids["Handle"], ids["updateStatus"]); n != 1 {
		t.Fatalf("setup: %d edges Handle -> updateStatus, want the scip-go one", n)
	}

	// The type check read both files and says the call on line 7 named
	// updateStatus resolves to something this index does not define (another
	// package's updateStatus, say): Known, and no target in the facts.
	sym := func(id int64, kind model.EntityKind, name string, line int) model.Symbol {
		return model.Symbol{
			ID: id, Kind: kind, Name: name, FQN: "go/types:" + name,
			Range:      model.Range{Start: model.Position{Line: line}, End: model.Position{Line: line + 2}},
			Confidence: model.ConfidenceExact, Source: model.SourceCompiler,
		}
	}
	files := []scip.FileFacts{
		{Path: svcPath, Language: "go", Symbols: []model.Symbol{
			sym(-2, model.KindClass, "Service", 6), sym(-3, model.KindMethod, "updateStatus", 9)}},
		{Path: apiPath, Language: "go", Symbols: []model.Symbol{sym(-2, model.KindFunction, "Handle", 4)}},
	}
	sites := []CallSite{{Path: apiPath, Line: 7, Name: "updateStatus", Known: true}}
	imp := NewImporter(reg, runID, scip.Options{RepoRoot: "/build/repo"})
	res, err := imp.ImportFacts(ctx, files, sites, scip.Tool{Name: "go/types", Version: "test"}, "go/types (built in)")
	if err != nil {
		t.Fatalf("import facts: %v", err)
	}
	if res.Contradicted != 1 {
		t.Errorf("withdrew %d edges, want the one scip-go edge the type check contradicts", res.Contradicted)
	}
	if n := countEdges(t, reg, runID, ids["Handle"], ids["updateStatus"]); n != 0 {
		t.Errorf("%d edges Handle -> updateStatus remain; the scip-go edge outlived the type check", n)
	}
}
