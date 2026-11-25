package registry

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// addCompilerEdgeAt writes one compiler fact at a position with a column.
func (f *crFixture) addCompilerEdgeAt(t *testing.T, line, col int, dst string) {
	t.Helper()
	if _, err := f.reg.UpgradeEdges(context.Background(), f.runID, []model.Edge{{
		SrcID: f.symIDs["caller"], DstID: f.symIDs[dst],
		Kind: model.EdgeCalls, Confidence: model.ConfidenceExact,
		Evidence: model.Evidence{
			FilePath: "use.go",
			Range:    model.Range{Start: model.Position{Line: line, Col: col}},
			Source:   model.SourceCompiler, Detail: "scip: scip-go v0.9.9",
		},
	}}); err != nil {
		t.Fatal(err)
	}
}

// bindRef makes the resolver's verdict on the reference at (name, line) a
// binding to dst, with the given start column.
func (f *crFixture) bindRef(t *testing.T, name string, line, col int, dst string) {
	t.Helper()
	if _, err := f.reg.db.ExecContext(context.Background(), `
		UPDATE symbol_references
		   SET resolution = 'resolved', resolved_symbol_id = ?, start_col = ?
		 WHERE index_run_id = ? AND name = ? AND start_line = ?`,
		f.symIDs[dst], col, f.runID, name, line); err != nil {
		t.Fatal(err)
	}
}

// TestCompilerMovesAReferenceTheResolverBoundElsewhere is the second half of
// the feature: a reference the resolver bound to one definition of the name is
// moved when a compiler resolved that very position to another, and the
// resolver's own verdict is kept in prior_resolution for the accuracy report.
func TestCompilerMovesAReferenceTheResolverBoundElsewhere(t *testing.T) {
	f := newCRFixture(t)
	f.bindRef(t, "Save", 100, 4, "OtherSave")
	f.addCompilerEdgeAt(t, 100, 9, "Save")

	st, err := f.reg.MarkCompilerResolutions(context.Background(), f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Corrected != 1 || st.Bound != 1 {
		t.Fatalf("stats %+v, want one reference corrected", st)
	}
	r := f.refs(t)["Save@100"]
	if r.resolution != "resolved" || r.dst != f.symIDs["Save"] || r.source != "compiler" || r.prior != "resolved" {
		t.Errorf("Save@100 is %+v, want resolved to Save by the compiler with the resolver's verdict kept", r)
	}
}

// TestCompilerConfirmsAgreeingBindingsWithCompilerProvenance: a reference the resolver
// bound to the definition the compiler also names is confirmed with resolution_source='compiler',
// so coverage and resolution stats count it as compiler-proven.
func TestCompilerConfirmsAgreeingBindingsWithCompilerProvenance(t *testing.T) {
	f := newCRFixture(t)
	f.bindRef(t, "Save", 100, 4, "Save")
	f.addCompilerEdgeAt(t, 100, 9, "Save")

	st, err := f.reg.MarkCompilerResolutions(context.Background(), f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Confirmed != 1 || st.Bound != 0 {
		t.Fatalf("stats %+v, want 1 confirmed and 0 newly bound", st)
	}
	if r := f.refs(t)["Save@100"]; r.source != "compiler" || r.resolution != "resolved" {
		t.Errorf("agreeing binding was not marked compiler-proven: %+v", r)
	}
}

// TestCompilerPairsTwoCallsOfOneNameByColumn: two references spelled Save on
// one line are paired with the compiler's two occurrences by position, so each
// takes its own definition. Where the counts differ nothing is paired and a
// binding the resolver made is left as it is.
func TestCompilerPairsTwoCallsOfOneNameByColumn(t *testing.T) {
	ctx := context.Background()
	f := newCRFixture(t)
	// A second Save call on line 100, so the line holds two references.
	if _, err := f.reg.db.ExecContext(ctx, `
		INSERT INTO symbol_references (index_run_id, file_id, name, kind, start_line, start_col, resolution)
		VALUES (?, ?, 'Save', 'reference.call', 100, 30, 'resolved')`, f.runID, f.useID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.db.ExecContext(ctx, `
		UPDATE symbol_references SET resolved_symbol_id = ?, start_col = 2, resolution = 'resolved'
		 WHERE index_run_id = ? AND name = 'Save' AND start_line = 100 AND start_col <> 30`,
		f.symIDs["OtherSave"], f.runID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.db.ExecContext(ctx, `
		UPDATE symbol_references SET resolved_symbol_id = ? WHERE index_run_id = ? AND start_col = 30`,
		f.symIDs["OtherSave"], f.runID); err != nil {
		t.Fatal(err)
	}
	f.addCompilerEdgeAt(t, 100, 8, "Save")
	f.addCompilerEdgeAt(t, 100, 36, "OtherSave")

	st, err := f.reg.MarkCompilerResolutions(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Corrected != 1 {
		t.Fatalf("stats %+v, want only the first call moved (the second already agrees)", st)
	}
	var first, second int64
	if err := f.reg.db.QueryRowContext(ctx,
		`SELECT resolved_symbol_id FROM symbol_references WHERE index_run_id = ? AND start_line = 100 AND start_col = 2`,
		f.runID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.db.QueryRowContext(ctx,
		`SELECT resolved_symbol_id FROM symbol_references WHERE index_run_id = ? AND start_line = 100 AND start_col = 30`,
		f.runID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != f.symIDs["Save"] || second != f.symIDs["OtherSave"] {
		t.Errorf("first call -> %d, second -> %d; want Save then OtherSave", first, second)
	}
}

// TestCompilerDoesNotMoveABindingWhenTheCountsDisagree: one reference and two
// occurrences of the name (two definitions, so two edges: the edge key is the
// line, so two occurrences of one definition on a line are one edge) cannot be paired, so a bound reference stays where
// the resolver put it.
func TestCompilerDoesNotMoveABindingWhenTheCountsDisagree(t *testing.T) {
	f := newCRFixture(t)
	f.bindRef(t, "Save", 100, 4, "OtherSave")
	f.addCompilerEdgeAt(t, 100, 9, "Save")
	f.addCompilerEdgeAt(t, 100, 40, "OtherSave")

	st, err := f.reg.MarkCompilerResolutions(context.Background(), f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Corrected != 0 {
		t.Fatalf("stats %+v, want no correction on an unpaired line", st)
	}
	if r := f.refs(t)["Save@100"]; r.dst != f.symIDs["OtherSave"] {
		t.Errorf("an unpaired binding moved to %d", r.dst)
	}
}
