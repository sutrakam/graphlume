package scale

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/capability/communities"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Community detection reads every edge in the run once and partitions the file
// graph they fold into. Both halves must stay linear: the read is one indexed
// range (pinned by TestFileLinksReadsEachEdgeOnce in internal/registry), and
// the partition visits each link a bounded number of times.
//
// The floor is lowered to `low` so that every edge in the fixture counts. That
// is the most work the capability can be asked to do on this index.
func TestCommunityDetectionStaysLinearAndBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()
	opts := communities.Options{MinConfidence: model.ConfidenceLow}

	var rep *communities.Report
	m := measure(func() {
		var err error
		rep, err = communities.Detect(ctx, f.reg, f.runID, opts)
		if err != nil {
			t.Fatalf("community detection: %v", err)
		}
	})
	t.Logf("%d files, %d file-to-file links from %d relationships -> %d communities (%d returned), truncated=%v",
		rep.Files, rep.Links, rep.Relationships, rep.Total, len(rep.Communities), rep.Truncated)

	// 1.5s fixed, 25µs per symbol: the allowance every tool answer gets in
	// TestEveryToolAnswerStaysBoundedAtScale, since this is what one answers.
	requireWithin(t, "community detection", m, budget{fixed: 1500 * time.Millisecond, perUnit: 25 * time.Microsecond}, f.info.Symbols)
	// What is left behind is the report: a page of communities, whatever the
	// size of the graph they were found in.
	requireRetainedUnder(t, "community detection", m, 4<<20)

	if rep.Files == 0 {
		t.Fatal("the fixture has files; none were in scope")
	}
	if rep.Truncated {
		// The synthetic graph is far denser than a real one (every file linked
		// to a hundred others), so a large fixture is over the link bound and
		// is refused. That is the designed answer, and its cost is what was
		// just measured; what a refusal must contain is asserted below.
		if len(rep.Communities) != 0 || rep.Total != 0 {
			t.Errorf("a refused graph reported %d communities (%d returned)", rep.Total, len(rep.Communities))
		}
		return
	}
	if rep.Links == 0 {
		t.Error("the fixture's edges cross files by construction; no link was read")
	}

	// The answer is bounded however many communities were found.
	if len(rep.Communities) > communities.DefaultLimit {
		t.Errorf("returned %d communities, over the default limit of %d", len(rep.Communities), communities.DefaultLimit)
	}
	for _, c := range rep.Communities {
		if len(c.Members) > communities.DefaultMembers {
			t.Errorf("community %s lists %d members, over the default of %d", c.ID, len(c.Members), communities.DefaultMembers)
		}
		if c.MembersOmitted != c.Files-len(c.Members) {
			t.Errorf("community %s: %d files, %d listed, %d counted as omitted", c.ID, c.Files, len(c.Members), c.MembersOmitted)
		}
	}
	if got := rep.Total - len(rep.Communities); got != rep.Omitted {
		t.Errorf("%d communities found and %d returned, but %d counted as omitted", rep.Total, len(rep.Communities), rep.Omitted)
	}
	if grouped := rep.Files - rep.Unlinked - rep.Alone; grouped != rep.Grouped {
		t.Errorf("file accounting does not add up: %d files, %d unlinked, %d alone, %d grouped",
			rep.Files, rep.Unlinked, rep.Alone, rep.Grouped)
	}

	// Stable ids, labels and order on a second run over the same index.
	again, err := communities.Detect(ctx, f.reg, f.runID, opts)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(rep)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Error("two runs over the same index produced different reports")
	}
}

// Past its link bound the capability refuses instead of partitioning part of
// the graph, and holds nothing while doing so. The refusal still costs the
// fold of every edge, which the database finishes before the first link can be
// counted, so its budget is the same linear one. The bound is set far below
// the fixture here so the refusal is exercised at every fixture size.
//
// The advice a refusal gives is "pass a path", so that is checked too: one
// directory of the same index must partition, whatever the size of the rest.
func TestCommunityDetectionRefusesAnOversizedGraphAndAPathStillAnswers(t *testing.T) {
	f := load(t)
	const bound = 10

	var rep *communities.Report
	m := measure(func() {
		var err error
		rep, err = communities.Detect(context.Background(), f.reg, f.runID,
			communities.Options{MinConfidence: model.ConfidenceLow, MaxLinks: bound})
		if err != nil {
			t.Fatalf("community detection: %v", err)
		}
	})
	if !rep.Truncated || len(rep.Communities) != 0 {
		t.Fatalf("a graph over the bound must be refused, got truncated=%v with %d communities", rep.Truncated, len(rep.Communities))
	}
	if len(rep.Notes) == 0 {
		t.Error("a refusal must say what to do next")
	}
	requireWithin(t, "community detection (refused)", m, budget{fixed: 1500 * time.Millisecond, perUnit: 25 * time.Microsecond}, f.info.Symbols)
	requireRetainedUnder(t, "community detection (refused)", m, 1<<20)

	// pkg0/ is ten files at every fixture size.
	var scoped *communities.Report
	m = measure(func() {
		var err error
		scoped, err = communities.Detect(context.Background(), f.reg, f.runID,
			communities.Options{MinConfidence: model.ConfidenceLow, PathPrefix: "pkg0/"})
		if err != nil {
			t.Fatalf("community detection under a path: %v", err)
		}
	})
	if scoped.Truncated {
		t.Fatalf("one directory of %d files was refused as too large: %s", scoped.Files, scoped.TruncatedReason)
	}
	if scoped.Files == 0 || scoped.Files > 20 {
		t.Errorf("pkg0/ holds %d files in scope, want the directory's ten", scoped.Files)
	}
	requireWithin(t, "community detection (one directory)", m, budget{fixed: 1500 * time.Millisecond, perUnit: 25 * time.Microsecond}, f.info.Symbols)
	requireRetainedUnder(t, "community detection (one directory)", m, 1<<20)
}
