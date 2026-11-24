package registry

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

// confinedFixture is the smallest graph that separates every case the
// confined-symbol query and the two entry-point signals have to tell apart.
// The "set" in every test is the one test file, a/a_test.go.
//
//	shared      called from production and from a test    -> not confined
//	prodOnly    called from production                    -> not confined
//	testedOnly  called from two tests                     -> confined, 2 referrers
//	recursive   calls itself; one test calls it           -> confined, 1 referrer
//	uncalled    nothing calls it                          -> not confined (unreferenced)
//	helper      in the test file, called by a test        -> not confined (inside the set)
//	handle      a route's bound handler; a test calls it  -> not confined (the endpoint points at it)
//	listener    consumes an event; a test calls it        -> confined, ConsumesEvent
//	requester   requests an endpoint; a test calls it     -> confined, NOT ConsumesEvent
//	Limit       a constant only a test reads              -> never returned (kind excluded)
//
// Two endpoints: "GET /x" is bound to handle; "GET /y" names a handler,
// create, that nothing was bound to.
func confinedFixture(t *testing.T) (r *Registry, runID int64, testFile int64) {
	t.Helper()
	ctx := context.Background()
	r, db := newTestRegistry(t)

	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	sym := func(path, name string, line int, kind model.EntityKind) symSpec {
		return symSpec{fqn: model.DefaultFQN(path, name), name: name, line: line, kind: kind}
	}
	fn := func(path, name string, line int) symSpec { return sym(path, name, line, model.KindFunction) }

	const prod, test, routes = "a/a.go", "a/a_test.go", "api/routes.go"
	files := []fileSpec{
		{path: prod, lang: "go", hash: "c1", syms: []symSpec{
			fn(prod, "shared", 1), fn(prod, "prodOnly", 10), fn(prod, "testedOnly", 20),
			fn(prod, "recursive", 30), fn(prod, "uncalled", 40), fn(prod, "prodCaller", 50),
			fn(prod, "handle", 60), fn(prod, "listener", 70), fn(prod, "requester", 80),
			sym(prod, "Limit", 90, model.KindConstant),
			sym(prod, "orders.placed", 100, model.KindEvent),
		}},
		{path: test, lang: "go", hash: "c2", syms: []symSpec{
			fn(test, "TestOne", 1), fn(test, "TestTwo", 10), fn(test, "helper", 20),
		}},
		{path: routes, lang: "go", hash: "c3", syms: []symSpec{
			sym(routes, "GET /x", 1, model.KindEndpoint),
			sym(routes, "GET /y", 2, model.KindEndpoint),
		}},
	}
	line := 0
	edge := func(src, dst string, kind model.EdgeKind) edgeSpec {
		line++
		return edgeSpec{srcFQN: src, dstFQN: dst, kind: kind, conf: model.ConfidenceHigh,
			source: model.SourceSourceCode, evFile: "ev.go", evLine: line}
	}
	p := func(n string) string { return model.DefaultFQN(prod, n) }
	ts := func(n string) string { return model.DefaultFQN(test, n) }
	rt := func(n string) string { return model.DefaultFQN(routes, n) }
	writeContent(t, r, run.ID, files, []edgeSpec{
		edge(p("prodCaller"), p("shared"), model.EdgeCalls),
		edge(ts("TestOne"), p("shared"), model.EdgeCalls),
		edge(p("prodCaller"), p("prodOnly"), model.EdgeCalls),
		edge(ts("TestOne"), p("testedOnly"), model.EdgeCalls),
		edge(ts("TestTwo"), p("testedOnly"), model.EdgeReferences),
		edge(p("recursive"), p("recursive"), model.EdgeCalls),
		edge(ts("TestTwo"), p("recursive"), model.EdgeCalls),
		edge(ts("TestOne"), ts("helper"), model.EdgeCalls),
		edge(rt("GET /x"), p("handle"), model.EdgeExposes),
		edge(ts("TestOne"), p("handle"), model.EdgeCalls),
		edge(p("listener"), p("orders.placed"), model.EdgeConsumes),
		edge(ts("TestOne"), p("listener"), model.EdgeCalls),
		edge(p("requester"), rt("GET /x"), model.EdgeConsumes),
		edge(ts("TestTwo"), p("requester"), model.EdgeCalls),
		edge(ts("TestTwo"), p("Limit"), model.EdgeReferences),
	})

	// The writer does not carry a route's handler name; the analyser stores
	// it in the endpoint's signature column, so the fixture does the same.
	for sig, handler := range map[string]string{"GET /x": "handle", "GET /y": "create"} {
		if _, err := db.ExecContext(ctx,
			`UPDATE symbols SET signature = ? WHERE index_run_id = ? AND name = ?`, handler, run.ID, sig); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM files WHERE index_run_id = ? AND path = ?`, run.ID, test).Scan(&testFile); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	return r, run.ID, testFile
}

func TestSymbolsReferencedOnlyFromSeparatesEachCase(t *testing.T) {
	r, runID, testFile := confinedFixture(t)

	page, err := r.SymbolsReferencedOnlyFrom(context.Background(), runID, 0, 1000, []int64{testFile})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ConfinedSymbol{}
	for _, c := range page.Symbols {
		by[c.Name] = c
	}

	for name, why := range map[string]string{
		"shared":     "production also calls it",
		"prodOnly":   "only production calls it",
		"uncalled":   "nothing calls it, which is the unreferenced query's finding",
		"helper":     "it is inside the set itself",
		"TestOne":    "it is inside the set itself",
		"handle":     "its endpoint points at it from outside the set",
		"prodCaller": "nothing calls it",
		"Limit":      "constants are not reported",
	} {
		if c, ok := by[name]; ok {
			t.Errorf("%s must not be returned (%s): %+v", name, why, c)
		}
	}
	if c, ok := by["testedOnly"]; !ok || c.Referrers != 2 {
		t.Errorf("testedOnly = %+v (found %v), want returned with 2 referrers", c, ok)
	}
	if c, ok := by["recursive"]; !ok || c.Referrers != 1 {
		t.Errorf("recursive = %+v (found %v), want returned with 1 referrer: its own call is not a use", c, ok)
	}
	if c, ok := by["listener"]; !ok || !c.ConsumesEvent {
		t.Errorf("listener = %+v (found %v), want returned with ConsumesEvent set", c, ok)
	}
	if c, ok := by["requester"]; !ok || c.ConsumesEvent {
		t.Errorf("requester = %+v (found %v), want returned WITHOUT ConsumesEvent: an endpoint is not an event", c, ok)
	}
	if c := by["testedOnly"]; c.FilePath != "a/a.go" || c.Language != "go" || c.StartLine != 20 {
		t.Errorf("testedOnly location = %s:%d (%s), want a/a.go:20 (go)", c.FilePath, c.StartLine, c.Language)
	}
	if page.Examined == 0 || page.LastID == 0 {
		t.Errorf("page = examined %d, last id %d; a window that covered symbols must say so", page.Examined, page.LastID)
	}
}

// Windows must visit every symbol exactly once whatever their size, and the
// scan must end on an empty window.
func TestSymbolsReferencedOnlyFromWindowsWithoutLossOrRepeat(t *testing.T) {
	r, runID, testFile := confinedFixture(t)
	ctx := context.Background()
	set := []int64{testFile}

	all, err := r.SymbolsReferencedOnlyFrom(ctx, runID, 0, 1000, set)
	if err != nil {
		t.Fatal(err)
	}

	for _, window := range []int{1, 3} {
		var got []ConfinedSymbol
		var after int64
		examined := 0
		for {
			page, err := r.SymbolsReferencedOnlyFrom(ctx, runID, after, window, set)
			if err != nil {
				t.Fatal(err)
			}
			if page.Examined == 0 {
				break
			}
			if page.Examined > window {
				t.Fatalf("a window of %d examined %d symbols", window, page.Examined)
			}
			if page.LastID <= after {
				t.Fatalf("window did not advance: after %d, last id %d", after, page.LastID)
			}
			examined += page.Examined
			after = page.LastID
			got = append(got, page.Symbols...)
		}
		if examined != all.Examined {
			t.Errorf("window %d: examined %d symbols in total, one statement examined %d", window, examined, all.Examined)
		}
		if !reflect.DeepEqual(got, all.Symbols) {
			t.Errorf("window %d: got %v, one statement returned %v", window, confinedNames(got), confinedNames(all.Symbols))
		}
	}
}

// An empty set confines nothing, and the window is still accounted for. The
// capability layer relies on this to ask "is anything left?" for the price of
// one statement that reads no edges.
func TestSymbolsReferencedOnlyFromAnEmptySetReturnsNothingButCountsTheWindow(t *testing.T) {
	r, runID, _ := confinedFixture(t)
	page, err := r.SymbolsReferencedOnlyFrom(context.Background(), runID, 0, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Symbols) != 0 {
		t.Errorf("an empty set returned %v", confinedNames(page.Symbols))
	}
	if page.Examined != 4 {
		t.Errorf("examined = %d, want the window of 4", page.Examined)
	}
}

func TestSymbolsReferencedOnlyFromIgnoresOtherRuns(t *testing.T) {
	r, runID, testFile := confinedFixture(t)
	page, err := r.SymbolsReferencedOnlyFrom(context.Background(), runID+1000, 0, 1000, []int64{testFile})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Symbols) != 0 || page.Examined != 0 {
		t.Errorf("a run that does not exist returned %d rows over %d symbols", len(page.Symbols), page.Examined)
	}
}

// The plan is the feature. This query asks about the callers of every
// referenced definition; with one probe left to the planner it reads every
// edge in the run once per definition. Every edge probe must go through
// idx_edges_dst, the symbols table must be walked in key order with no sort,
// and the only scan allowed is of the bound file set.
func TestConfinedSymbolsUsesItsIndexes(t *testing.T) {
	_, db := newTestRegistry(t)
	args := []any{1, 0, 100, "[1,2]"}
	for _, k := range unreferencedKinds {
		args = append(args, k)
	}
	plan := queryPlan(t, db, confinedSQL(len(unreferencedKinds)), args...)
	joined := strings.Join(plan, "\n")

	for _, index := range []string{"idx_edges_dst", "idx_edges_src", "idx_refs_name_binding"} {
		if !strings.Contains(joined, index) {
			t.Errorf("the plan does not use %s:\n%s", index, joined)
		}
	}
	if strings.Contains(joined, "sqlite_autoindex_edges") {
		t.Errorf("the plan reads edges through the UNIQUE autoindex, which scans the whole run:\n%s", joined)
	}
	if !strings.Contains(joined, "SEARCH s USING INTEGER PRIMARY KEY") {
		t.Errorf("symbols are not walked in key order over the window:\n%s", joined)
	}
	if !strings.Contains(joined, "SEARCH c USING INTEGER PRIMARY KEY") {
		t.Errorf("a referrer's symbol is not a primary-key lookup:\n%s", joined)
	}
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN") && !strings.Contains(step, "json_each") {
			t.Errorf("the plan contains a full scan: %q\n%s", step, joined)
		}
		if strings.Contains(step, "TEMP B-TREE") {
			t.Errorf("the plan sorts: %q\n%s", step, joined)
		}
	}

	window := strings.Join(queryPlan(t, db, confinedWindowSQL, 1, 0, 100), "\n")
	if !strings.Contains(window, "SEARCH s USING INTEGER PRIMARY KEY") || strings.Contains(window, "TEMP B-TREE") {
		t.Errorf("the window query does not seek by key, or sorts:\n%s", window)
	}
}

// The unreferenced scan pages by id. If the plan sorts, every page probes
// every remaining symbol in the run before LIMIT applies, and the scan is
// quadratic in the number of pages. The outer loop must be the symbols table
// in key order.
func TestUnreferencedSymbolsPagesInKeyOrderWithoutSorting(t *testing.T) {
	_, db := newTestRegistry(t)
	args := []any{1, 0}
	for _, k := range unreferencedKinds {
		args = append(args, k)
	}
	args = append(args, 100)
	plan := queryPlan(t, db, unreferencedSQL(len(unreferencedKinds)), args...)
	joined := strings.Join(plan, "\n")

	if !strings.Contains(joined, "SEARCH s USING INTEGER PRIMARY KEY (rowid>?)") {
		t.Errorf("the page does not start with a seek on the symbol id:\n%s", joined)
	}
	for _, step := range plan {
		if strings.Contains(step, "TEMP B-TREE") {
			t.Errorf("the plan sorts, so LIMIT cannot stop the scan: %q\n%s", step, joined)
		}
		if strings.HasPrefix(step, "SCAN") {
			t.Errorf("the plan contains a full scan: %q\n%s", step, joined)
		}
	}
}

func TestUnreferencedSymbolsMarksEventConsumers(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	const path = "svc/svc.go"
	fqn := func(n string) string { return model.DefaultFQN(path, n) }
	spec := func(name string, line int, kind model.EntityKind) symSpec {
		return symSpec{fqn: fqn(name), name: name, line: line, kind: kind}
	}
	edge := func(src, dst string, line int) edgeSpec {
		return edgeSpec{srcFQN: fqn(src), dstFQN: fqn(dst), kind: model.EdgeConsumes, conf: model.ConfidenceHigh,
			source: model.SourceSourceCode, evFile: path, evLine: line}
	}
	writeContent(t, r, run.ID, []fileSpec{{path: path, lang: "go", hash: "e1", syms: []symSpec{
		spec("listener", 1, model.KindFunction),
		spec("requester", 10, model.KindFunction),
		spec("plain", 20, model.KindFunction),
		spec("orders.placed", 30, model.KindEvent),
		spec("GET /x", 40, model.KindEndpoint),
	}}}, []edgeSpec{edge("listener", "orders.placed", 1), edge("requester", "GET /x", 2)})
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	got, err := r.UnreferencedSymbols(ctx, run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]UnreferencedSymbol{}
	for _, u := range got {
		by[u.Name] = u
	}
	if u, ok := by["listener"]; !ok || !u.ConsumesEvent {
		t.Errorf("listener = %+v (found %v), want ConsumesEvent set", u, ok)
	}
	if u, ok := by["requester"]; !ok || u.ConsumesEvent {
		t.Errorf("requester = %+v (found %v): requesting an endpoint is not consuming an event", u, ok)
	}
	if u, ok := by["plain"]; !ok || u.ConsumesEvent {
		t.Errorf("plain = %+v (found %v), want no signal", u, ok)
	}
}

// A bound handler is reported by neither query: the endpoint's EXPOSES edge
// is an incoming relationship. Only the handler nothing was bound to is named.
func TestUnboundRouteHandlersNamesOnlyWhatWasNotBound(t *testing.T) {
	r, runID, _ := confinedFixture(t)
	ctx := context.Background()

	got, err := r.UnboundRouteHandlers(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"create"}) {
		t.Errorf("unbound handlers = %v, want [create]: handle is bound to GET /x", got)
	}

	unreferenced, err := r.UnreferencedSymbols(ctx, runID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range unreferenced {
		if u.Name == "handle" {
			t.Error("handle is a route's bound handler and must not be returned as unreferenced")
		}
	}

	if none, err := r.UnboundRouteHandlers(ctx, runID+1000); err != nil || len(none) != 0 {
		t.Errorf("a run that does not exist returned %v, err %v", none, err)
	}

	_, db := newTestRegistry(t)
	plan := strings.Join(queryPlan(t, db, unboundHandlersSQL, 1, "endpoint", "EXPOSES"), "\n")
	if !strings.Contains(plan, "idx_edges_src") || strings.Contains(plan, "sqlite_autoindex_edges") {
		t.Errorf("the bound-handler probe does not go through idx_edges_src:\n%s", plan)
	}
}

func TestFilePathsListsEveryFileInTheRun(t *testing.T) {
	r, runID, testFile := confinedFixture(t)
	got, err := r.FilePaths(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range got {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if !reflect.DeepEqual(paths, []string{"a/a.go", "a/a_test.go", "api/routes.go"}) {
		t.Errorf("paths = %v", paths)
	}
	if got[testFile] != "a/a_test.go" {
		t.Errorf("file %d = %q, want a/a_test.go", testFile, got[testFile])
	}
}

// queryPlan returns the detail column of EXPLAIN QUERY PLAN for query.
func queryPlan(t *testing.T, db *store.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
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
	return plan
}

func confinedNames(cs []ConfinedSymbol) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}
