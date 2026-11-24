package scipimport

import (
	"context"
	"testing"

	scippb "github.com/scip-code/scip/bindings/go/scip"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/scip"
)

const tsInnerSym = "scip-typescript npm proj 1.0.0 `src/use.ts`/run().inner."

// topLevelDocs is use.ts as a compiler describes it with code in three places:
// inside `run` (matched), inside a nested definition the index does not hold
// whose lines are inside `run`, and in no definition at all.
func topLevelDocs() []*scippb.Document {
	docs := tsDocs()
	use := docs[1]
	use.Symbols = append(use.Symbols, &scippb.SymbolInformation{Symbol: tsInnerSym, Kind: scippb.SymbolInformation_Function, DisplayName: "inner"})
	use.Occurrences = append(use.Occurrences,
		// A definition the index has no symbol for, written on lines 5-6, which
		// the index's `run` (lines 4-6) encloses.
		def(tsInnerSym, rng(4, 6, 4, 11), rng(4, 0, 5, 1)),
		ref(tsGetSym, rng(4, 14, 4, 17)),
		// Outside every definition the compiler knows: the file's own code.
		ref(tsGetSym, rng(29, 0, 29, 3)),
		// An import line: an occurrence to the compiler, and no reference the
		// parser recorded, so not a use written in code.
		ref(tsGetSym, rng(1, 9, 1, 12)),
	)
	return docs
}

// addParsedReferences records the references the parser would have written for
// the two uses in topLevelDocs, one on a line of `run` and one outside every
// definition. The import line has none.
func addParsedReferences(t *testing.T, reg *registry.Registry, runID int64) {
	t.Helper()
	for _, line := range []int{5, 30} {
		if _, err := dbOf(reg).ExecContext(context.Background(), `
			INSERT INTO symbol_references (index_run_id, file_id, name, kind, start_line, start_col, resolution)
			SELECT ?, f.id, 'get', 'reference.call', ?, 0, 'unresolved' FROM files f
			 WHERE f.index_run_id = ? AND f.path = ?`, runID, line, runID, tsUse); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCompilerRelationsOutsideTheIndexedDefinitionsAreAttributed: a relation the
// compiler puts in a definition the index does not hold, or in no definition,
// becomes an edge from the indexed definition whose lines hold it, else from
// the file's synthetic caller.
func TestCompilerRelationsOutsideTheIndexedDefinitionsAreAttributed(t *testing.T) {
	ctx := context.Background()
	reg, runID, ids := proofRepo(t)
	addParsedReferences(t, reg, runID)
	path := scipFile(t, topLevelDocs()...)
	res, err := NewImporter(reg, runID, scip.Options{RepoRoot: "/build/repo"}).Import(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reattributed != 2 {
		t.Fatalf("reattributed %d relation(s), want 2 (line 5 and line 30, not the import on line 2): %+v", res.Reattributed, res)
	}

	callers, err := reg.FileCallerIDs(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	fc, ok := callers[tsUse]
	if !ok {
		t.Fatalf("no file caller for %s: %v", tsUse, callers)
	}
	if _, ok := callers[tsSvc]; ok {
		t.Errorf("svc.ts has no code outside its definitions and must not get a file caller")
	}
	edges, err := reg.Edges(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	fromRun := 0
	for _, e := range edges {
		if e.Evidence.Range.Start.Line == 5 && e.DstID == ids["get"] && e.SrcID == ids["run"] {
			fromRun++
		}
	}
	if fromRun != 1 {
		t.Errorf("want one edge run -> get at line 5 (the nested definition's lines are run's), got %d", fromRun)
	}
	var n int
	if err := dbOf(reg).QueryRowContext(ctx,
		`SELECT count(*) FROM edges WHERE src_id = ? AND dst_id = ? AND evidence_line = 30 AND source = 'compiler'`,
		fc, ids["get"]).Scan(&n); err != nil || n != 1 {
		t.Errorf("edges from the file caller at line 30 = %d (%v), want 1", n, err)
	}
	// Reading the whole graph never meets the synthetic caller.
	for _, e := range edges {
		if e.SrcID == fc {
			t.Errorf("Edges returned an edge from the file caller")
		}
	}
}

// TestCompilerSettlesAndCorrectsReferencesFromTheFileCaller: with the edge
// attributed, a reference the resolver bound to the wrong definition is moved.
func TestFileCallerEdgesSettleReferences(t *testing.T) {
	ctx := context.Background()
	reg, runID, ids := proofRepo(t)
	addParsedReferences(t, reg, runID)
	path := scipFile(t, topLevelDocs()...)
	res, err := NewImporter(reg, runID, scip.Options{RepoRoot: "/build/repo"}).Import(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolutions.Bound < 1 {
		t.Fatalf("the top-level reference was not settled: %+v", res.Resolutions)
	}
	var dst int64
	var source string
	if err := dbOf(reg).QueryRowContext(ctx, `
		SELECT resolved_symbol_id, resolution_source FROM symbol_references
		 WHERE index_run_id = ? AND start_line = 30`, runID).Scan(&dst, &source); err != nil {
		t.Fatal(err)
	}
	if dst != ids["get"] || source != "compiler" {
		t.Errorf("line 30 is bound to %d by %q, want get by the compiler", dst, source)
	}
	b, err := reg.NameBinding(ctx, runID, ids["get"], "get")
	if err != nil {
		t.Fatal(err)
	}
	if b.WithoutEdge != 0 {
		t.Errorf("%d bound reference(s) still have no relationship; the file caller should hold them", b.WithoutEdge)
	}
}
