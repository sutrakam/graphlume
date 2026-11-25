package deadcode

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability/captest"
	"github.com/sutrakam/graphlume/internal/registry"
)

// fakeCallers serves a fixed slice of confined symbols through the windowing
// contract the registry honours. Its symbols stand in for the whole run: one
// symbol is one unit of window.
type fakeCallers struct {
	files map[int64]string
	rows  []registry.ConfinedSymbol
	// windows counts the statements that carried a file set, which is the
	// work the pass is budgeted on.
	windows int
	// gotSet is the file set of the last such statement.
	gotSet []int64
}

func (f *fakeCallers) FilePaths(context.Context, int64) (map[int64]string, error) {
	return f.files, nil
}

func (f *fakeCallers) SymbolsReferencedOnlyFrom(_ context.Context, _, afterID int64, window int, fileIDs []int64) (registry.ConfinedPage, error) {
	var page registry.ConfinedPage
	for _, r := range f.rows {
		if r.ID <= afterID {
			continue
		}
		if page.Examined == window {
			break
		}
		page.Examined++
		page.LastID = r.ID
		if len(fileIDs) > 0 {
			page.Symbols = append(page.Symbols, r)
		}
	}
	if len(fileIDs) > 0 {
		f.windows++
		f.gotSet = fileIDs
	}
	return page, nil
}

// oneTestFile is the file table most tests need: one production file and one
// test file.
func oneTestFile() map[int64]string {
	return map[int64]string{1: "pkg/x.go", 2: "pkg/x_test.go"}
}

func confined(id int64, name string) registry.ConfinedSymbol {
	return registry.ConfinedSymbol{
		ID: id, Kind: "function", Name: name, FilePath: "pkg/x.go", Language: "go",
		StartLine: int(id) * 10, EndLine: int(id)*10 + 4, Referrers: 1,
	}
}

func testOnlyNames(rep *Report) []string {
	if rep.TestOnly == nil {
		return nil
	}
	out := make([]string, len(rep.TestOnly.Candidates))
	for i, c := range rep.TestOnly.Candidates {
		out[i] = c.Name
	}
	return out
}

// The finding itself: reported, labelled, counted, and never likely, even
// for a row with nothing else against it whose name appears nowhere else.
func TestCodeOnlyTestsReachIsReportedAndNeverLikely(t *testing.T) {
	only := confined(1, "legacyParse")
	only.Referrers = 3
	callers := &fakeCallers{files: oneTestFile(), rows: []registry.ConfinedSymbol{only}}
	// The test file is the only place the name occurs outside its definition.
	src := &fakeSource{
		selfAt:  map[string][]int{"legacyParse": {10}},
		testUse: map[string][]int{"legacyParse": {5, 9, 30}},
	}

	rep := find(t, src, Options{TestOnly: callers})
	if rep.TestOnly == nil {
		t.Fatal("the pass was asked for and the report has no test-only section")
	}
	if len(rep.TestOnly.Candidates) != 1 {
		t.Fatalf("test-only candidates = %v, want [legacyParse]", testOnlyNames(rep))
	}
	c := rep.TestOnly.Candidates[0]
	if c.Certainty != CertaintyPossible {
		t.Errorf("certainty = %q: a definition only tests reach is a judgement call and is never likely", c.Certainty)
	}
	if len(c.Reasons) != 1 || c.Reasons[0] != ReasonTestOnly {
		t.Errorf("reasons = %v, want [%s]", c.Reasons, ReasonTestOnly)
	}
	if c.TestReferences != 3 {
		t.Errorf("test references = %d, want 3", c.TestReferences)
	}
	if rep.TestOnly.Found != 1 || rep.TestOnly.TextChecked != 1 || rep.TestOnly.NameElsewhere != 0 {
		t.Errorf("counts = found %d, checked %d, elsewhere %d; want 1, 1, 0",
			rep.TestOnly.Found, rep.TestOnly.TextChecked, rep.TestOnly.NameElsewhere)
	}
	if rep.TestOnly.TestFiles != 1 {
		t.Errorf("test files = %d, want 1", rep.TestOnly.TestFiles)
	}
	if !reflect.DeepEqual(callers.gotSet, []int64{2}) {
		t.Errorf("the file set passed to the registry = %v, want only the test file's id [2]", callers.gotSet)
	}
	if !rep.Completeness.HasCaveat(TestOnlyLimits) {
		t.Error("a report that ran the pass must carry its limits")
	}
	if ReasonTestOnly.Text() == "" || ReasonTestOnly.Label() == "" {
		t.Error("the test-only reason is missing its sentence or label")
	}
}

// THE PROPERTY THE OPTION EXISTS FOR: asking for the second finding must not
// change the first. Same candidates, same counts, and without the option no
// section and no second pass.
func TestTheDefaultAnswerIsUnchangedByTheTestOnlyPass(t *testing.T) {
	unbound := row(2, "maybe")
	unbound.NameUnbound = true
	rows := []registry.UnreferencedSymbol{row(1, "orphan"), unbound}
	callers := &fakeCallers{files: oneTestFile(), rows: []registry.ConfinedSymbol{confined(7, "testedOnly")}}

	plain := find(t, &fakeSource{rows: rows}, Options{})
	if plain.TestOnly != nil {
		t.Error("the default report has a test-only section; it must be opt-in")
	}
	if plain.Completeness.HasCaveat(TestOnlyLimits) {
		t.Error("a caveat about a pass that did not run is a caveat readers learn to skip")
	}

	with := find(t, &fakeSource{rows: rows}, Options{TestOnly: callers})
	if !reflect.DeepEqual(plain.Candidates, with.Candidates) {
		t.Errorf("candidates changed when the pass was added:\n without %v\n with    %v",
			candidateNames(plain), candidateNames(with))
	}
	if plain.Likely != with.Likely || plain.Possible != with.Possible || plain.Skipped != with.Skipped {
		t.Errorf("counts changed: without %d/%d/%d, with %d/%d/%d",
			plain.Likely, plain.Possible, plain.Skipped, with.Likely, with.Possible, with.Skipped)
	}
	for _, c := range with.Candidates {
		if c.Name == "testedOnly" {
			t.Error("a test-only row leaked into the unreferenced candidates")
		}
	}
	if with.TestOnly == nil || with.TestOnly.Found != 1 {
		t.Errorf("test-only section = %+v, want one finding", with.TestOnly)
	}

	// The likely filter belongs to the first finding. The section is still
	// there when asked for, and still holds nothing likely.
	likely := find(t, &fakeSource{rows: rows}, Options{TestOnly: callers, MinCertainty: CertaintyLikely})
	if got := testOnlyNames(likely); len(got) != 1 || got[0] != "testedOnly" {
		t.Errorf("with min certainty likely, test-only = %v, want [testedOnly]", got)
	}
	for _, c := range likely.Candidates {
		if c.Certainty != CertaintyLikely {
			t.Errorf("%s is %s in a likely-only answer", c.Name, c.Certainty)
		}
	}
}

// What the pass must NOT report, each for its own reason.
func TestTestOnlyLeavesOutWhatIsExpectedToBeReachedThatWay(t *testing.T) {
	exported := confined(1, "Public")
	exported.Exported = true
	listener := confined(2, "onOrderPlaced")
	listener.ConsumesEvent = true
	handler := confined(3, "createUser")
	entry := confined(4, "main")
	lua := confined(5, "luaFn")
	lua.Language = "lua"
	elsewhere := confined(6, "other")
	elsewhere.FilePath = "cmd/tool.go"
	real := confined(7, "real")

	callers := &fakeCallers{
		files: oneTestFile(),
		rows:  []registry.ConfinedSymbol{exported, listener, handler, entry, lua, elsewhere, real},
	}
	src := &fakeSource{handlers: []string{"createUser"}}
	rep := find(t, src, Options{
		TestOnly: callers, PathPrefix: "pkg/",
		Judgeable: func(l string) bool { return l == "go" },
	})

	if got := testOnlyNames(rep); len(got) != 1 || got[0] != "real" {
		t.Fatalf("test-only candidates = %v, want [real]", got)
	}
	to := rep.TestOnly
	if to.Found != 1 {
		t.Errorf("found = %d, want 1", to.Found)
	}
	// Public, main and luaFn.
	if to.Skipped != 3 || to.SkippedUnjudged != 1 {
		t.Errorf("skipped = %d (unjudged %d), want 3 (1)", to.Skipped, to.SkippedUnjudged)
	}
	// A listener and a route handler called directly by their tests are not
	// kept alive by those tests: the framework calls them.
	if to.EntryPoints != 2 {
		t.Errorf("entry points = %d, want 2", to.EntryPoints)
	}
	if len(rep.UnjudgedLangs) != 1 || rep.UnjudgedLangs[0] != "lua" {
		t.Errorf("unjudged languages = %v, want [lua]", rep.UnjudgedLangs)
	}

	// Asked for, an exported definition is listed and says it is exported.
	rep = find(t, src, Options{TestOnly: callers, PathPrefix: "pkg/", IncludeExported: true})
	var public *Candidate
	for i, c := range rep.TestOnly.Candidates {
		if c.Name == "Public" {
			public = &rep.TestOnly.Candidates[i]
		}
	}
	if public == nil {
		t.Fatalf("with exported included, Public is missing: %v", testOnlyNames(rep))
	}
	if public.Certainty != CertaintyPossible || len(public.Reasons) != 2 ||
		public.Reasons[0] != ReasonTestOnly || public.Reasons[1] != ReasonExported {
		t.Errorf("Public = %+v, want possible with [only_reached_from_tests exported]", *public)
	}
}

// Every weaker signal a row carries is shown beside the finding's own reason.
func TestTestOnlyRowsCarryEveryOtherReason(t *testing.T) {
	s := confined(1, "persist")
	s.NameUnbound, s.Overrides, s.PartialParse = true, true, true
	rep := find(t, &fakeSource{}, Options{TestOnly: &fakeCallers{files: oneTestFile(), rows: []registry.ConfinedSymbol{s}}})

	want := []Reason{ReasonTestOnly, ReasonNameUnbound, ReasonOverrides, ReasonPartialParse}
	if got := rep.TestOnly.Candidates[0].Reasons; !reflect.DeepEqual(got, want) {
		t.Errorf("reasons = %v, want %v", got, want)
	}
}

// The graph alone overclaims exactly here: a handler passed as a value has
// test callers and no production CALL. The text holds the production use, and
// a row whose name occurs in code that is not a test is not offered as
// confirmed.
func TestANameUsedInProductionCodeIsListedLastAndSaysSo(t *testing.T) {
	registered := confined(1, "findCallers") // lines 10-14
	registered.EndLine = 400                 // the largest, so it would lead on size
	inTestsOnly := confined(2, "legacyParse")
	opaque := confined(3, "opaque")

	callers := &fakeCallers{files: oneTestFile(), rows: []registry.ConfinedSymbol{registered, inTestsOnly, opaque}}
	src := &fakeSource{
		usedAt:  map[string][]int{"findCallers": {12}}, // pkg/elsewhere.go: addTool(..., s.findCallers)
		testUse: map[string][]int{"findCallers": {3}, "legacyParse": {8}},
		unknown: map[string]bool{"opaque": true},
	}
	rep := find(t, src, Options{TestOnly: callers})

	if got := strings.Join(testOnlyNames(rep), ","); got != "legacyParse,findCallers,opaque" {
		t.Fatalf("order = %s, want the confirmed row first: legacyParse,findCallers,opaque", got)
	}
	by := map[string]Candidate{}
	for _, c := range rep.TestOnly.Candidates {
		by[c.Name] = c
	}
	if c := by["legacyParse"]; len(c.Reasons) != 1 {
		t.Errorf("legacyParse = %+v: a mention in a TEST file is the caller already counted, not a production use", c)
	}
	for _, name := range []string{"findCallers", "opaque"} {
		c := by[name]
		if len(c.Reasons) != 2 || c.Reasons[1] != ReasonMentioned {
			t.Errorf("%s reasons = %v, want [%s %s]", name, c.Reasons, ReasonTestOnly, ReasonMentioned)
		}
		if c.Certainty != CertaintyPossible {
			t.Errorf("%s certainty = %q, want possible", name, c.Certainty)
		}
	}
	to := rep.TestOnly
	if to.Found != 3 || to.TextChecked != 3 || to.NameElsewhere != 2 {
		t.Errorf("counts = found %d, checked %d, elsewhere %d; want 3, 3, 2", to.Found, to.TextChecked, to.NameElsewhere)
	}
}

// A row never reached by the text check is never returned, and the check
// stops once the answer is full.
func TestTestOnlyTextCheckStopsWhenTheAnswerIsFull(t *testing.T) {
	var rows []registry.ConfinedSymbol
	for i := int64(1); i <= 300; i++ {
		s := confined(i, fmt.Sprintf("fn%d", i))
		s.StartLine, s.EndLine = 1, int(i) // later rows are longer
		rows = append(rows, s)
	}
	rep := find(t, &fakeSource{}, Options{Limit: 5, TestOnly: &fakeCallers{files: oneTestFile(), rows: rows}})

	to := rep.TestOnly
	if len(to.Candidates) != 5 || to.Candidates[0].Name != "fn300" {
		t.Errorf("candidates = %v, want the five longest, fn300 first", testOnlyNames(rep))
	}
	if to.Found != 300 {
		t.Errorf("found = %d, want 300: the count covers what was examined, not what was returned", to.Found)
	}
	if to.TextChecked != 5 {
		t.Errorf("text checked = %d, want 5", to.TextChecked)
	}
}

// The pass has its own budget, counted in symbols walked, and says when it
// stopped short. Reaching the end exactly on budget is not truncation.
func TestTestOnlyScanBudgetTruncatesAndSaysSo(t *testing.T) {
	var rows []registry.ConfinedSymbol
	for i := int64(1); i <= 100; i++ {
		rows = append(rows, confined(i, fmt.Sprintf("fn%d", i)))
	}

	callers := &fakeCallers{files: oneTestFile(), rows: rows}
	rep := find(t, &fakeSource{}, Options{TestOnly: callers, TestOnlyScanBudget: 10})
	to := rep.TestOnly
	if !to.Truncated || to.TruncatedReason == "" {
		t.Fatalf("a pass stopped by its budget must report itself truncated: %+v", to)
	}
	if to.SymbolsExamined != 10 || to.Found != 10 {
		t.Errorf("examined %d, found %d; want 10 and 10", to.SymbolsExamined, to.Found)
	}
	// The unreferenced scan's own verdict is not the test-only pass's.
	if rep.Completeness.Truncated {
		t.Error("the unreferenced scan finished; its completeness must not report the other pass's budget")
	}

	exact := find(t, &fakeSource{}, Options{
		TestOnly: &fakeCallers{files: oneTestFile(), rows: rows}, TestOnlyScanBudget: 100,
	})
	if exact.TestOnly.Truncated {
		t.Error("a pass that read the last symbol exactly on budget cut nothing off")
	}
	if exact.TestOnly.SymbolsExamined != 100 {
		t.Errorf("examined %d, want 100", exact.TestOnly.SymbolsExamined)
	}
}

// With no test file in the index the pass has nothing to look for. That is
// reported as such and costs no scan.
func TestNoTestFilesMeansNothingToLookFor(t *testing.T) {
	callers := &fakeCallers{
		files: map[int64]string{1: "pkg/x.go", 2: "pkg/y.go"},
		rows:  []registry.ConfinedSymbol{confined(1, "fn")},
	}
	rep := find(t, &fakeSource{}, Options{TestOnly: callers})
	to := rep.TestOnly
	if to == nil || to.TestFiles != 0 || to.Found != 0 || len(to.Candidates) != 0 {
		t.Errorf("test-only = %+v, want an empty section that says there are no test files", to)
	}
	if callers.windows != 0 {
		t.Errorf("the registry was scanned %d time(s) with no test file to scan for", callers.windows)
	}
}

// End to end over the real registry and the real query.
//
//	usedEverywhere  called from production and from a test   -> not listed
//	testedOnly      called from two tests                    -> listed
//	recursive       calls itself, and a test calls it        -> listed (recursion is not a use)
//	orphan          nothing calls it                         -> the OTHER finding, not this one
//	testHelper      in a test file, called by a test         -> not listed (it is test code)
//	listUsers       a route's handler, also called by a test -> not listed (the route is a production referrer)
func TestTestOnlyOverTheRealRegistry(t *testing.T) {
	d, runID := captest.Build(t,
		[]captest.Sym{
			captest.S(1, "usedEverywhere", "pkg/a.go"),
			captest.S(2, "testedOnly", "pkg/a.go"),
			captest.S(3, "recursive", "pkg/a.go"),
			captest.S(4, "orphan", "pkg/a.go"),
			captest.S(5, "prodCaller", "pkg/b.go"),
			captest.S(6, "TestA", "pkg/a_test.go"),
			captest.S(7, "TestB", "pkg/a_test.go"),
			captest.S(8, "testHelper", "pkg/a_test.go"),
			captest.S(9, "GET /users", "api/routes.go").WithKind("endpoint"),
			captest.S(10, "listUsers", "api/users.go"),
		},
		[]captest.Edge{
			captest.E(5, 1), captest.E(6, 1),
			captest.E(6, 2), captest.E(7, 2),
			captest.E(3, 3), captest.E(7, 3),
			captest.E(6, 8),
			captest.E(9, 10).WithKind("EXPOSES"), captest.E(6, 10),
		},
		captest.Refs{},
	)
	reg, ok := d.Registry.(*registry.Registry)
	if !ok {
		t.Fatal("captest did not return the real registry")
	}
	rep, err := Find(context.Background(), d, reg, runID, Options{TestOnly: reg})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Candidate{}
	for _, c := range rep.TestOnly.Candidates {
		got[c.Name] = c
	}
	if len(got) != 2 {
		t.Errorf("test-only candidates = %v, want testedOnly and recursive", testOnlyNames(rep))
	}
	if c := got["testedOnly"]; c.TestReferences != 2 || c.Certainty != CertaintyPossible {
		t.Errorf("testedOnly = %+v, want possible with 2 test references", c)
	}
	if _, ok := got["recursive"]; !ok {
		t.Error("recursive is called only by itself and a test; its own call is not a production use")
	}
	for _, name := range []string{"usedEverywhere", "orphan", "testHelper", "listUsers", "TestA"} {
		if c, listed := got[name]; listed {
			t.Errorf("%s must not be listed as reached only from tests: %+v", name, c)
		}
	}
	// orphan is the first finding's, and stays there.
	unreferenced := map[string]bool{}
	for _, c := range rep.Candidates {
		unreferenced[c.Name] = true
	}
	if !unreferenced["orphan"] {
		t.Errorf("orphan has no caller and belongs in the unreferenced list: %v", candidateNames(rep))
	}
	if unreferenced["testedOnly"] {
		t.Error("testedOnly has callers and must not be in the unreferenced list")
	}
	if rep.TestOnly.TestFiles != 1 {
		t.Errorf("test files = %d, want 1", rep.TestOnly.TestFiles)
	}
}
