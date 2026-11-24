package scale

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/capability/deadcode"
	"github.com/sutrakam/graphlume/internal/capability/hotspots"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/capability/tests"
	"github.com/sutrakam/graphlume/internal/config"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
)

// The dead-code scan reads every unreferenced definition in the run. It must
// do so in pages, keep only the best few, and cost time in proportion to what
// it read.
func TestDeadCodeScanIsBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	var rep *deadcode.Report
	m := measure(func() {
		var err error
		rep, err = deadcode.Find(ctx, f.deps, f.reg, f.runID, deadcode.Options{
			Limit: 50, IncludeExported: true,
		})
		if err != nil {
			t.Fatalf("dead-code scan: %v", err)
		}
	})

	found := rep.Likely + rep.Possible
	t.Logf("fixture: %d symbols, %d edges; %d unreferenced definitions found, %d skipped, %d text-checked",
		f.info.Symbols, f.info.Edges, found, rep.Skipped, rep.TextChecked)

	if found == 0 {
		t.Fatal("a random graph of this size always has unreferenced definitions; the scan found none")
	}
	if len(rep.Candidates) > 50 {
		t.Errorf("returned %d candidates, over the limit of 50", len(rep.Candidates))
	}
	if rep.Completeness.Truncated && f.info.Symbols <= deadcode.DefaultScanBudget {
		t.Errorf("the scan reported itself truncated on a fixture inside its budget: %s", rep.Completeness.TruncatedReason)
	}
	// 2s fixed, 60µs per symbol: a million symbols may take a minute.
	requireWithin(t, "dead-code scan", m, budget{fixed: 2 * time.Second, perUnit: 60 * time.Microsecond}, f.info.Symbols)
	// The scan keeps a shortlist of a few thousand rows however many it reads.
	requireRetainedUnder(t, "dead-code scan", m, 48<<20)
}

// A narrow path prefix on a large index must not cost a full scan's worth of
// work twice over, and must still return.
func TestDeadCodeScanWithANarrowPrefixStillReturns(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	var rep *deadcode.Report
	m := measure(func() {
		var err error
		rep, err = deadcode.Find(ctx, f.deps, f.reg, f.runID, deadcode.Options{
			Limit: 10, PathPrefix: "pkg0/", IncludeExported: true,
		})
		if err != nil {
			t.Fatalf("dead-code scan: %v", err)
		}
	})
	for _, c := range rep.Candidates {
		if !strings.HasPrefix(c.Path, "pkg0/") {
			t.Errorf("%s is outside the requested prefix", c.Path)
		}
	}
	requireWithin(t, "dead-code scan, narrow prefix", m, budget{fixed: 2 * time.Second, perUnit: 60 * time.Microsecond}, f.info.Symbols)
}

// The test-only pass asks a harder question than the unreferenced scan: not
// "does anything point at this definition" but "where does everything that
// points at it live". Asked naively that reads every edge in the run once per
// definition. It has its own budget here, measured on its own, so a change
// that makes it quadratic fails this test and not somebody else's.
//
// What keeps it linear, and what this test is the tripwire for:
//
//   - every edge probe goes through idx_edges_dst (pinned by
//     TestConfinedSymbolsUsesItsIndexes in internal/registry);
//   - the probe stops at the first caller outside a test file, so a hub with a
//     thousand ordinary callers costs a few lookups, not a thousand;
//   - the set of test files is bound once per window, not once per definition.
func TestTestOnlyScanIsBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()
	opts := deadcode.Options{Limit: 50, IncludeExported: true}

	// The unreferenced scan alone, as the baseline the pass is added to.
	var base *deadcode.Report
	baseline := measure(func() {
		var err error
		if base, err = deadcode.Find(ctx, f.deps, f.reg, f.runID, opts); err != nil {
			t.Fatalf("dead-code scan: %v", err)
		}
	})

	opts.TestOnly = f.reg
	var rep *deadcode.Report
	m := measure(func() {
		var err error
		if rep, err = deadcode.Find(ctx, f.deps, f.reg, f.runID, opts); err != nil {
			t.Fatalf("dead-code scan with the test-only pass: %v", err)
		}
	})
	to := rep.TestOnly
	if to == nil {
		t.Fatal("the pass was asked for and the report has no test-only section")
	}
	t.Logf("fixture: %d symbols, %d edges, %d test files of %d; %d definitions reached only from tests, "+
		"%d skipped, %d text-checked, %d symbols examined",
		f.info.Symbols, f.info.Edges, to.TestFiles, f.info.Files, to.Found, to.Skipped, to.TextChecked, to.SymbolsExamined)
	t.Logf("the pass itself: %s on top of a %s unreferenced scan",
		(m.elapsed - baseline.elapsed).Round(time.Microsecond), baseline.elapsed.Round(time.Microsecond))

	// A tenth of the fixture's files are test files and its edges are random,
	// so some definitions are always called only from them.
	if to.Found == 0 {
		t.Fatal("a random graph with test files always has definitions only they call; the pass found none")
	}
	if to.TestFiles == 0 {
		t.Fatal("the fixture has test files; the pass recognised none")
	}
	if len(to.Candidates) > 50 {
		t.Errorf("returned %d test-only candidates, over the limit of 50", len(to.Candidates))
	}
	for _, c := range to.Candidates {
		if c.Certainty != deadcode.CertaintyPossible {
			t.Errorf("%s is graded %s; a test-only row is never likely", c.Name, c.Certainty)
		}
		if strings.HasSuffix(c.Path, "_test.go") {
			t.Errorf("%s is in a test file (%s); test code is not what the pass reports", c.Name, c.Path)
		}
		if c.TestReferences == 0 {
			t.Errorf("%s has no test references; it is listed because it has them", c.Name)
		}
	}
	// Every symbol is walked exactly once.
	if to.SymbolsExamined != f.info.Symbols && f.info.Symbols <= deadcode.DefaultTestOnlyScanBudget {
		t.Errorf("examined %d symbols of %d", to.SymbolsExamined, f.info.Symbols)
	}
	if to.Truncated && f.info.Symbols <= deadcode.DefaultTestOnlyScanBudget {
		t.Errorf("the pass reported itself truncated on a fixture inside its budget: %s", to.TruncatedReason)
	}
	// The pass adds a section. It does not change the answer it is added to.
	if rep.Likely != base.Likely || rep.Possible != base.Possible || len(rep.Candidates) != len(base.Candidates) {
		t.Errorf("the unreferenced answer changed: %d likely, %d possible, %d rows without the pass; %d, %d, %d with it",
			base.Likely, base.Possible, len(base.Candidates), rep.Likely, rep.Possible, len(rep.Candidates))
	}

	// The whole call, both passes: 2s fixed, 90µs per symbol. That is the
	// unreferenced scan's 60µs plus 30µs for this pass, which walks every
	// symbol once and reads the edges of the few it reports.
	requireWithin(t, "dead-code scan with the test-only pass", m,
		budget{fixed: 2 * time.Second, perUnit: 90 * time.Microsecond}, f.info.Symbols)
	// Two shortlists of a few thousand rows, and the ids of the test files.
	requireRetainedUnder(t, "dead-code scan with the test-only pass", m, 48<<20)
}

// The answer an agent gets with the second section on must fit the inline
// limit like every other, however much test-only code the index holds.
func TestTestOnlyAnswerStaysBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	cfg := config.Defaults()
	cfg.ProjectName = "scale"
	cfg.MCPProfile = config.MCPFull
	srv, err := devmcp.New(devmcp.Deps{
		Registry: f.reg, Graph: f.graph, Config: cfg,
		Judgeable: func(string) bool { return true },
	})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	inv, err := srv.NewInvoker(ctx, "scale")
	if err != nil {
		t.Fatalf("invoker: %v", err)
	}
	defer func() { _ = inv.Close() }()

	payload, _ := json.Marshal(map[string]any{"include_test_only": true, "include_exported": true, "limit": 50})
	if _, err := inv.Call(ctx, "deadcode", payload); err != nil {
		t.Fatalf("deadcode: %v", err)
	}
	const samples = 5
	times := make([]time.Duration, 0, samples)
	var text string
	for i := 0; i < samples; i++ {
		start := time.Now()
		res, err := inv.Call(ctx, "deadcode", payload)
		if err != nil {
			t.Fatalf("deadcode: %v", err)
		}
		times = append(times, time.Since(start))
		text = res.Text
	}
	if !strings.Contains(text, "reached only from tests") {
		t.Errorf("the answer has no test-only rows on a fixture that holds them:\n%s", firstLines(text, 12))
	}
	if limit := devmcp.DefaultLimits().MaxTotalBytes; len(text) > limit {
		t.Errorf("deadcode with include_test_only answered with %d bytes on a %d-symbol index; the inline limit is %d",
			len(text), f.info.Symbols, limit)
	}
	// 1.5s fixed, 40µs per symbol at p95: the every-tool budget of 25µs plus
	// 15µs for the second pass.
	b := budget{fixed: 1500 * time.Millisecond, perUnit: 40 * time.Microsecond}
	got := p95(times)
	t.Logf("%-26s p95 %10s  answer %6d bytes", "deadcode +test-only", got.Round(time.Microsecond), len(text))
	if got > b.at(f.info.Symbols) {
		t.Errorf("deadcode with include_test_only: p95 %s over %d warm calls, budget %s at %d symbols",
			got.Round(time.Millisecond), samples, b.at(f.info.Symbols).Round(time.Millisecond), f.info.Symbols)
	}
}

// Hotspot ranking reads structure for exactly the files history touched, in
// chunks, through the destination index. Its cost must follow the commit
// window, not the repository.
func TestHotspotRankingIsBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()
	hist := newSyntheticHistory(f.spec.Files, *flagCommits)

	var rep *hotspots.Report
	m := measure(func() {
		var err error
		rep, err = hotspots.Rank(ctx, hist, f.reg, f.runID, hotspots.Options{
			Limit: 20, CommitWindow: *flagCommits, IncludeTests: true,
		})
		if err != nil {
			t.Fatalf("hotspot ranking: %v", err)
		}
	})

	t.Logf("history: %d commits over %d files; %d files changed, %d large commits excluded from coupling",
		rep.CommitsRead, f.spec.Files, rep.FilesChanged, rep.LargeCommitsSkipped)

	if len(rep.Hotspots) == 0 {
		t.Fatal("no hotspots ranked")
	}
	if len(rep.Hotspots) > 20 {
		t.Errorf("returned %d hotspots, over the limit of 20", len(rep.Hotspots))
	}
	if rep.Hotspots[0].Score != 100 {
		t.Errorf("the top hotspot scores %d, want 100 (scores are normalised to the largest)", rep.Hotspots[0].Score)
	}
	for i := 1; i < len(rep.Hotspots); i++ {
		if rep.Hotspots[i].Score > rep.Hotspots[i-1].Score {
			t.Errorf("hotspots are not in descending score order at row %d", i)
		}
	}
	if rep.LargeCommitsSkipped == 0 {
		t.Error("the synthetic history contains sweeps; none was excluded from coupling")
	}

	// The unit is the number of changed FILES: each costs one index-driven
	// lookup of its symbols and their inbound edges. 3s fixed, 4ms per file.
	requireWithin(t, "hotspot ranking", m, budget{fixed: 3 * time.Second, perUnit: 4 * time.Millisecond}, rep.FilesChanged)
	requireRetainedUnder(t, "hotspot ranking", m, 32<<20)
}

// A path search explores outward level by level. On a hub-heavy graph the
// frontier explodes, and the node cap is what stops "how does A reach B" from
// loading the graph.
func TestPathSearchIsBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	var routes [][]int64
	m := measure(func() {
		var err error
		routes, err = f.graph.Paths(ctx, f.info.TargetOrdinary, f.info.TargetHub, 3, 6)
		if err != nil {
			t.Fatalf("path search: %v", err)
		}
	})
	t.Logf("%d route(s) from an ordinary symbol to a hub", len(routes))
	for _, r := range routes {
		if len(r) < 2 || r[0] != f.info.TargetOrdinary || r[len(r)-1] != f.info.TargetHub {
			t.Errorf("malformed route %v", r)
		}
		if len(r)-1 > 6 {
			t.Errorf("route of %d hops exceeds max depth 6", len(r)-1)
		}
	}
	if len(routes) > 3 {
		t.Errorf("returned %d routes, over the limit of 3", len(routes))
	}
	// 2s fixed, 40µs per symbol.
	requireWithin(t, "path search", m, budget{fixed: 2 * time.Second, perUnit: 40 * time.Microsecond}, f.info.Symbols)
	requireRetainedUnder(t, "path search", m, 64<<20)
}

// Impact analysis from a hub is the worst realistic query the product
// answers: a thousand direct callers, three levels deep. Its bounds must bite
// and must say so, at every size.
func TestImpactFromAHubIsBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	var rep impact.Report
	m := measure(func() {
		var err error
		rep, err = impact.AnalyzeWith(ctx, f.deps, f.runID, f.info.TargetHubName, impact.Options{MaxDepth: 3})
		if err != nil {
			t.Fatalf("impact analysis: %v", err)
		}
	})
	if len(rep.Direct) == 0 {
		t.Fatal("the hub has no direct callers; the fixture is not hub-shaped")
	}
	// 3s fixed, 30µs per symbol: the first call also loads the symbol index.
	requireWithin(t, "impact from a hub", m, budget{fixed: 3 * time.Second, perUnit: 30 * time.Microsecond}, f.info.Symbols)

	var found tests.TestReport
	m = measure(func() {
		var err error
		found, err = tests.FindTests(ctx, f.deps, f.runID, f.info.TargetTestedName)
		if err != nil {
			t.Fatalf("test discovery: %v", err)
		}
	})
	if len(found.Tests) == 0 {
		t.Error("the tested target has a test by construction; none was found")
	}
	requireWithin(t, "test discovery", m, budget{fixed: 2 * time.Second, perUnit: 20 * time.Microsecond}, f.info.Symbols)
}

// THE PROPERTY THAT MATTERS MOST: every tool's answer fits the host's inline
// limit, and comes back promptly, on a large index.
//
// An answer over the limit is written to a file by the host and read back in
// pieces, which costs the agent more than the search the tool replaced. So the
// bound is asserted for every tool the server registers, on every profile, at
// whatever size the fixture is.
func TestEveryToolAnswerStaysBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	cfg := config.Defaults()
	cfg.ProjectName = "scale"
	cfg.MCPProfile = config.MCPFull
	srv, err := devmcp.New(devmcp.Deps{
		Registry:  f.reg,
		Graph:     f.graph,
		Config:    cfg,
		History:   newSyntheticHistory(f.spec.Files, *flagCommits),
		Judgeable: func(string) bool { return true },
	})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	inv, err := srv.NewInvoker(ctx, "scale")
	if err != nil {
		t.Fatalf("invoker: %v", err)
	}
	defer func() { _ = inv.Close() }()

	hub, ordinary, tested := f.info.TargetHubName, f.info.TargetOrdinaryName, f.info.TargetTestedName
	calls := map[string]map[string]any{
		"context":         {"task": "change how " + hub + " validates its input"},
		"overview":        {},
		"search":          {"query": ordinary},
		"symbol":          {"name": ordinary},
		"definition":      {"name": ordinary},
		"references":      {"name": hub},
		"callers":         {"name": hub},
		"callees":         {"name": ordinary},
		"implementations": {"name": hub},
		"trace":           {"from": ordinary, "to": hub},
		"tests":           {"symbol": tested},
		"impact":          {"symbol": hub},
		"changes":         {"base": "HEAD"},
		"owners":          {"symbol": hub},
		"busfactor":       {},
		"coverage":        {},
		"rename":          {"name": ordinary, "new_name": ordinary + "Renamed"},
		"plan":            {"symbol": hub},
		"endpoints":       {},
		"tables":          {},
		"config":          {"key": "DATABASE_URL"},
		"outline":         {"path": "pkg0/file0.go"},
		"patch":           {"patch": "diff --git a/pkg0/file0.go b/pkg0/file0.go\n--- a/pkg0/file0.go\n+++ b/pkg0/file0.go\n@@ -1 +1 @@\n-old\n+new\n"},
		"diff":            {"diff": "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n"},
		"blindspots":      {},
		"deadcode":        {"include_exported": true},
		"hotspots":        {"include_tests": true},
		// The widest call it accepts: every confidence counted, so every
		// edge in the fixture becomes part of a link.
		"communities": {"min_confidence": "low"},
		"graph":       {"query": "SELECT count(*) FROM symbols"},
		"stats":       {},
		"explore":     {"name": ordinary},
		"structure":   {},
	}

	tools, err := inv.Tools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	limit := devmcp.DefaultLimits().MaxTotalBytes

	for _, tool := range tools {
		args, ok := calls[tool.Name]
		if !ok {
			t.Errorf("tool %q has no call in this test; add one so its answer is bounded at scale too", tool.Name)
			continue
		}
		payload, _ := json.Marshal(args)

		// Warm once, then sample: the first call of several tools builds a
		// per-server cache, which is a one-off a session pays once.
		if _, err := inv.Call(ctx, tool.Name, payload); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
			continue
		}
		const samples = 5
		times := make([]time.Duration, 0, samples)
		size := 0
		for i := 0; i < samples; i++ {
			start := time.Now()
			res, err := inv.Call(ctx, tool.Name, payload)
			if err != nil {
				t.Errorf("%s: %v", tool.Name, err)
				break
			}
			times = append(times, time.Since(start))
			size = len(res.Text)
		}

		if size > limit {
			t.Errorf("%s answered with %d bytes on a %d-symbol index; the inline limit is %d. "+
				"An answer that grows with the repository is one the host stops showing.",
				tool.Name, size, f.info.Symbols, limit)
		}
		// 1.5s fixed, 25µs per symbol, at p95 over warm calls. The two audit
		// tools get a larger fixed allowance: each runs dozens of bounded
		// traversals by design (blindspots runs test discovery from
		// the fifty widest functions), which is a cost that does not grow
		// with the repository and is not what this budget is hunting.
		b := budget{fixed: 1500 * time.Millisecond, perUnit: 25 * time.Microsecond}
		if auditTools[tool.Name] {
			b.fixed = 10 * time.Second
		}
		got := p95(times)
		t.Logf("%-26s p95 %10s  answer %6d bytes", tool.Name, got.Round(time.Microsecond), size)
		if got > b.at(f.info.Symbols) {
			t.Errorf("%s: p95 %s over %d warm calls, budget %s at %d symbols",
				tool.Name, got.Round(time.Millisecond), samples, b.at(f.info.Symbols).Round(time.Millisecond), f.info.Symbols)
		}
	}
	if len(calls) != len(tools) {
		t.Errorf("this test calls %d tools; the full profile registers %d", len(calls), len(tools))
	}
}

// auditTools answer a whole-repository question by running many bounded
// queries. See the budget note in TestEveryToolAnswerStaysBoundedAtScale.
var auditTools = map[string]bool{"blindspots": true, "plan": true}

// Many agents asking at once is the HTTP server's ordinary load. Answers must
// stay correct and bounded when the in-process client is shared.
func TestConcurrentToolCallsStayCorrectAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	cfg := config.Defaults()
	cfg.ProjectName = "scale"
	cfg.MCPProfile = config.MCPFull
	srv, err := devmcp.New(devmcp.Deps{Registry: f.reg, Graph: f.graph, Config: cfg})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	inv, err := srv.NewInvoker(ctx, "scale")
	if err != nil {
		t.Fatalf("invoker: %v", err)
	}
	defer func() { _ = inv.Close() }()

	payload, _ := json.Marshal(map[string]any{"name": f.info.TargetHubName})
	want, err := inv.Call(ctx, "callers", payload)
	if err != nil {
		t.Fatal(err)
	}

	const workers, each = 8, 6
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < each; i++ {
				got, err := inv.Call(ctx, "callers", payload)
				if err != nil {
					errs <- err
					return
				}
				// A front-end session is stateless, so the same question has
				// the same answer whoever asked first.
				if firstLines(got.Text, 2) != firstLines(want.Text, 2) {
					errs <- fmt.Errorf("a concurrent call answered differently:\n%s\n---\n%s", got.Text, want.Text)
					return
				}
			}
			errs <- nil
		}()
	}
	for w := 0; w < workers; w++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
