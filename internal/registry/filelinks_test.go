package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// linksFixture is the smallest graph that separates everything the fold must
// keep apart and everything it must drop:
//
//	a/a.go -> b/b.go   two high calls, one low call   -> two rows, one per tier
//	b/b.go -> a/a.go   one medium call                -> its own row: links are directed
//	a/a.go -> a/a.go   one call inside the file       -> dropped
//	a/a.go -> c/c.go   one CONTAINS edge              -> dropped
func linksFixture(t *testing.T) (*Registry, int64, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	r, db := newTestRegistry(t)

	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	fn := func(path, name string, line int) symSpec {
		return symSpec{fqn: model.DefaultFQN(path, name), name: name, line: line, kind: model.KindFunction}
	}
	files := []fileSpec{
		{path: "a/a.go", lang: "go", hash: "h1", content: "x", syms: []symSpec{fn("a/a.go", "a1", 1), fn("a/a.go", "a2", 10)}},
		{path: "b/b.go", lang: "go", hash: "h2", content: "x", syms: []symSpec{fn("b/b.go", "b1", 1)}},
		{path: "c/c.go", lang: "go", hash: "h3", content: "x", syms: []symSpec{fn("c/c.go", "c1", 1)}},
	}
	edge := func(src, dst string, kind model.EdgeKind, conf model.Confidence, line int) edgeSpec {
		return edgeSpec{srcFQN: src, dstFQN: dst, kind: kind, conf: conf,
			source: model.SourceSourceCode, evFile: "a/a.go", evLine: line}
	}
	a := func(n string) string { return model.DefaultFQN("a/a.go", n) }
	b := model.DefaultFQN("b/b.go", "b1")
	c := model.DefaultFQN("c/c.go", "c1")
	writeContent(t, r, run.ID, files, []edgeSpec{
		edge(a("a1"), b, model.EdgeCalls, model.ConfidenceHigh, 1),
		edge(a("a2"), b, model.EdgeCalls, model.ConfidenceHigh, 2),
		edge(a("a1"), b, model.EdgeCalls, model.ConfidenceLow, 3),
		edge(b, a("a1"), model.EdgeCalls, model.ConfidenceMedium, 4),
		edge(a("a1"), a("a2"), model.EdgeCalls, model.ConfidenceHigh, 5),
		edge(a("a1"), c, model.EdgeContains, model.ConfidenceHigh, 6),
	})
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	ids := map[string]int64{}
	rows, err := db.QueryContext(ctx, `SELECT path, id FROM files WHERE index_run_id = ?`, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var id int64
		if err := rows.Scan(&p, &id); err != nil {
			t.Fatal(err)
		}
		ids[p] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return r, run.ID, ids
}

func collectLinks(t *testing.T, r *Registry, runID int64) []FileLink {
	t.Helper()
	var got []FileLink
	if err := r.EachFileLink(context.Background(), runID, func(l FileLink) error {
		got = append(got, l)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEachFileLinkFoldsEdgesByFilePairAndConfidence(t *testing.T) {
	r, runID, ids := linksFixture(t)
	a, b := ids["a/a.go"], ids["b/b.go"]

	got := collectLinks(t, r, runID)
	want := []FileLink{
		{SrcFileID: a, DstFileID: b, Confidence: model.ConfidenceHigh, Relationships: 2},
		{SrcFileID: a, DstFileID: b, Confidence: model.ConfidenceLow, Relationships: 1},
		{SrcFileID: b, DstFileID: a, Confidence: model.ConfidenceMedium, Relationships: 1},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("links =\n  %v\nwant\n  %v\n(an edge inside one file and a CONTAINS edge must not appear)", got, want)
	}
}

// The rows for one pair of files must arrive together: the caller merges them
// into one link as they stream past, without holding a map of every pair.
func TestEachFileLinkKeepsAPairsRowsAdjacent(t *testing.T) {
	r, runID, _ := linksFixture(t)
	type pair struct{ src, dst int64 }
	closed := map[pair]bool{}
	var last pair
	for i, l := range collectLinks(t, r, runID) {
		p := pair{l.SrcFileID, l.DstFileID}
		if i > 0 && p != last {
			closed[last] = true
		}
		if closed[p] {
			t.Fatalf("the rows for files %d -> %d are not adjacent", p.src, p.dst)
		}
		last = p
	}
}

func TestEachFileLinkStopsWhenTheCallerSaysSo(t *testing.T) {
	r, runID, _ := linksFixture(t)
	stop := errors.New("enough")
	calls := 0
	err := r.EachFileLink(context.Background(), runID, func(FileLink) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Errorf("err = %v, want the caller's own error back unchanged", err)
	}
	if calls != 1 {
		t.Errorf("fn ran %d times after asking to stop, want 1", calls)
	}
}

func TestEachFileLinkIgnoresOtherRuns(t *testing.T) {
	r, runID, _ := linksFixture(t)
	if got := collectLinks(t, r, runID+1000); len(got) != 0 {
		t.Errorf("a run that does not exist returned %d links", len(got))
	}
}

// The fold reads every edge of the run, and must read each exactly once: one
// range of idx_edges_conf, then a primary-key probe per endpoint. Started from
// symbols instead, it probes the edge table once per symbol, twice over, which
// is the shape that made hotspots take minutes. A plan regresses without any
// test of the RESULT noticing.
func TestFileLinksReadsEachEdgeOnce(t *testing.T) {
	_, db := newTestRegistry(t)
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+fileLinksSQL, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if len(plan) == 0 || !strings.Contains(plan[0], "idx_edges_conf (index_run_id=?)") {
		t.Errorf("the plan does not start from one range of idx_edges_conf:\n%s", joined)
	}
	if n := strings.Count(joined, "USING INTEGER PRIMARY KEY (rowid=?)"); n != 2 {
		t.Errorf("want each endpoint reached by one primary-key probe (2 in all), got %d:\n%s", n, joined)
	}
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN") {
			t.Errorf("the plan contains a full scan: %q\n%s", step, joined)
		}
	}
}
