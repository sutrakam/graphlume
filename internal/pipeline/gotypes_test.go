package pipeline

import (
	"context"
	"testing"
)

// goTypesFiles is a one-package module whose call `w.Run()` resolves, through
// embedding, to Deeper.Run until a file adds Run to W itself.
var goTypesFiles = map[string]string{
	"go.mod":    "module example.com/inc\n\ngo 1.22\n",
	"p/d.go":    "package p\n\ntype Deeper struct{}\n\nfunc (Deeper) Run() string { return \"deeper\" }\n",
	"p/w.go":    "package p\n\ntype W struct{ Deeper }\n",
	"p/call.go": "package p\n\nfunc Caller(w W) string {\n\treturn w.Run()\n}\n",
}

type edgeRow struct {
	src, dst, kind, confidence, detail string
}

func (h *harness) edgesInto(dstName string) []edgeRow {
	h.t.Helper()
	rows, err := h.db.QueryContext(context.Background(), `
		SELECT s.name, d.name, e.kind, e.confidence, COALESCE(e.detail,'')
		  FROM edges e
		  JOIN symbols s ON s.id = e.src_id
		  JOIN symbols d ON d.id = e.dst_id
		 WHERE e.index_run_id = ? AND d.name = ? AND e.kind = 'CALLS'
		 ORDER BY s.name, e.confidence`, h.runID, dstName)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []edgeRow
	for rows.Next() {
		var r edgeRow
		if err := rows.Scan(&r.src, &r.dst, &r.kind, &r.confidence, &r.detail); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func hasExact(rows []edgeRow, src string) bool {
	for _, r := range rows {
		if r.src == src && r.confidence == "exact" && r.detail == "go/types (built in)" {
			return true
		}
	}
	return false
}

// TestGoTypesLandsExactEdgesAndLeavesNoStaleOnes is the contract of the
// built-in Go type check against an incremental index:
//
//   - after resolve, a call the compiler proves is an exact edge whose detail
//     says where the proof came from;
//   - when a change elsewhere re-targets that call WITHOUT touching either file
//     the edge connects, the old exact edge does not survive the next pass.
func TestGoTypesLandsExactEdgesAndLeavesNoStaleOnes(t *testing.T) {
	h := newHarness(t, goTypesFiles)
	ctx := context.Background()

	if _, err := Resolve(ctx, h.reg, h.runID, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
	out := CheckGoTypes(ctx, h.reg, h.runID, GoTypesOptions{Root: h.root})
	if out.Err != nil {
		t.Fatalf("go types: %v", out.Err)
	}
	if !out.Ran {
		t.Fatalf("go types did not run: %s", out.Reason)
	}
	if out.Check.Goroot == "" {
		t.Skip("no GOROOT can be found without running anything")
	}
	if rows := h.edgesInto("Run"); !hasExact(rows, "Caller") {
		t.Fatalf("Caller -> Deeper.Run is not an exact go/types edge: %+v", rows)
	}

	// W gains its own Run in a NEW file. call.go, d.go and w.go are untouched.
	h.write("p/w2.go", "package p\n\nfunc (W) Run() string { return \"w\" }\n")
	h.sync()
	if _, err := Resolve(ctx, h.reg, h.runID, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
	out = CheckGoTypes(ctx, h.reg, h.runID, GoTypesOptions{Root: h.root})
	if out.Err != nil {
		t.Fatalf("go types after the change: %v", out.Err)
	}

	rows := h.edgesInto("Run")
	exactTo := map[string]bool{}
	for _, r := range rows {
		if r.confidence == "exact" && r.src == "Caller" {
			exactTo[r.dst] = true
		}
	}
	callers := 0
	for _, r := range rows {
		if r.confidence == "exact" && r.src == "Caller" {
			callers++
		}
	}
	if callers != 1 {
		t.Fatalf("Caller has %d exact edges to a Run, want exactly the one to W.Run: %+v", callers, rows)
	}
	if out.Import.Retracted.Deleted+out.Import.Retracted.Restored+out.Import.Contradicted == 0 {
		t.Errorf("the stale exact edge was neither retracted nor contradicted: %+v", out.Import)
	}
}

// TestGoTypesDoesNothingWithoutGo: a repository with no Go costs nothing and
// says nothing.
func TestGoTypesDoesNothingWithoutGo(t *testing.T) {
	h := newHarness(t, map[string]string{"a.py": "def f():\n    return 1\n"})
	out := CheckGoTypes(context.Background(), h.reg, h.runID, GoTypesOptions{Root: h.root})
	if out.Ran || out.Err != nil {
		t.Fatalf("ran=%v err=%v on a repository with no Go", out.Ran, out.Err)
	}
}

// TestIncrementalRefreshPreservesExactGoTypesEdges verifies that an incremental
// refresh does not downgrade compiler-proven exact edges to heuristic ones (Finding A1).
func TestIncrementalRefreshPreservesExactGoTypesEdges(t *testing.T) {
	h := newHarness(t, goTypesFiles)
	ctx := context.Background()

	if _, err := Resolve(ctx, h.reg, h.runID, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
	out := CheckGoTypes(ctx, h.reg, h.runID, GoTypesOptions{Root: h.root})
	if out.Err != nil || !out.Ran {
		t.Fatalf("initial go types failed: %v", out.Err)
	}
	if out.Check.Goroot == "" {
		t.Skip("no GOROOT can be found without running anything")
	}
	if rows := h.edgesInto("Run"); !hasExact(rows, "Caller") {
		t.Fatalf("initial edge is not exact: %+v", rows)
	}

	// Make an edit to a file
	h.write("p/d.go", "package p\n\ntype Deeper struct{}\nfunc (Deeper) Run() string { return \"d\" }\n// comment\n")

	changes, err := Diff(ctx, h.reg, h.runID, h.walker, harnessBuild)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	t.Logf("changes count: %d, changes: %+v", len(changes), changes)
	stats, err := Incremental(ctx, h.reg, h.runID, h.walker, changes, IncrementalOptions{
		Root:    h.root,
		Resolve: true,
	})
	if err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if stats.Modified == 0 {
		t.Fatalf("expected at least 1 modified file, got %+v", stats)
	}

	// Verify that exact edge was preserved/re-proven by the incremental pass
	if rows := h.edgesInto("Run"); !hasExact(rows, "Caller") {
		t.Fatalf("exact compiler edge was lost after incremental refresh: %+v", rows)
	}
}
