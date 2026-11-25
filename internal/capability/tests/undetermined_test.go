package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/captest"
	"github.com/sutrakam/graphlume/internal/registry"
)

// TestNoTestFoundIsNotUntestedWhenTheCallsWereNeverBound: "No test symbol
// reaches this target" was stated for functions whose tests call them through
// references the index left ambiguous. A test reaches its target through a
// reference, so when those could not be bound the search could not see.
func TestNoTestFoundIsNotUntestedWhenTheCallsWereNeverBound(t *testing.T) {
	refs := captest.Refs{Resolved: 6}
	for i := 0; i < 5; i++ {
		refs.Named = append(refs.Named, captest.NamedRef{
			Name: "parse", Line: 10 + i, Resolution: registry.ResolutionAmbiguous})
	}
	deps, runID := captest.Build(t,
		[]captest.Sym{captest.S(1, "parse", "lib/parser.go"), captest.S(2, "other", "lib/other.go")}, nil, refs)

	rep, err := FindTests(context.Background(), deps, runID, "parse")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Untested {
		t.Fatal("fixture: a test was found; this test is about the case where none is")
	}
	if !strings.Contains(rep.Undetermined, "5 of the 5 reference(s) spelled like this name") {
		t.Errorf("Undetermined = %q; want it to say the calls were not bound", rep.Undetermined)
	}
	found := false
	for _, c := range rep.Completeness.Caveats {
		found = found || strings.Contains(c, "could not be determined")
	}
	if !found {
		t.Errorf("no caveat says the absence is undetermined: %v", rep.Completeness.Caveats)
	}
	// No suggestion is built on a search that could not see.
	target := rep.Target
	target.Exported = true
	for _, s := range SuggestMissingScenarios(target, rep) {
		if s.Kind == GapPublicAPIUntested {
			t.Errorf("a missing-test suggestion was made from an undetermined absence: %+v", s)
		}
	}

	// A symbol with nothing unbound keeps its plain finding.
	other, err := FindTests(context.Background(), deps, runID, "other")
	if err != nil {
		t.Fatal(err)
	}
	if !other.Untested || other.Undetermined != "" {
		t.Errorf("other: Untested=%v Undetermined=%q; want a determined absence", other.Untested, other.Undetermined)
	}
}

// TestAProvenTestIsNotDoubted: the doubt is about the graph signal finding
// nothing. When it found a test, there is a test.
func TestAProvenTestIsNotDoubted(t *testing.T) {
	refs := captest.Refs{Resolved: 6, Named: []captest.NamedRef{
		{Name: "parse", Line: 10, Resolution: registry.ResolutionAmbiguous},
	}}
	deps, runID := captest.Build(t,
		[]captest.Sym{captest.S(1, "parse", "lib/parser.go"), captest.S(2, "TestParse", "lib/parser_test.go")},
		[]captest.Edge{captest.E(2, 1)}, refs)
	rep, err := FindTests(context.Background(), deps, runID, "parse")
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProvenByGraph == 0 {
		t.Fatal("fixture: the call edge from the test was not found")
	}
	if rep.Undetermined != "" {
		t.Errorf("Undetermined = %q on a report with a proven test", rep.Undetermined)
	}
}

// TestSuggestionsDoNotAssumeExportStatus: "this symbol is part of the public
// surface" was said of any capitalised name in any language.
func TestSuggestionsDoNotAssumeExportStatus(t *testing.T) {
	rep := TestReport{}
	java := capability.Target{Name: "Parse", FQN: "src/P.java:Parse", Kind: "method",
		FilePath: "src/P.java", Language: "java", Exported: true}
	for _, s := range SuggestMissingScenarios(java, rep) {
		if s.Kind == GapPublicAPIUntested {
			t.Errorf("a public-API suggestion for a Java symbol, whose export status the index does not know: %+v", s)
		}
	}
	goTarget := capability.Target{Name: "Parse", FQN: "p/p.go:Parse", Kind: "function",
		FilePath: "p/p.go", Language: "go", Exported: true}
	got := false
	for _, s := range SuggestMissingScenarios(goTarget, rep) {
		got = got || s.Kind == GapPublicAPIUntested
	}
	if !got {
		t.Error("an exported, untested Go function no longer gets the public-API suggestion")
	}
}

// TestFindTestsForTargetAnswersForTheDefinitionGiven: two definitions under
// one qualified name, and the test calls the second.
func TestFindTestsForTargetAnswersForTheDefinitionGiven(t *testing.T) {
	deps, runID := captest.Build(t,
		[]captest.Sym{
			captest.S(1, "fromJson", "src/Gson.java").WithFQN("src/Gson.java:fromJson"),
			captest.S(2, "fromJson", "src/Gson.java").WithFQN("src/Gson.java:fromJson"),
			captest.S(3, "TestFromJson", "src/GsonTest.java"),
		},
		[]captest.Edge{captest.E(3, 2)}, captest.Refs{Resolved: 4})

	second := capability.Target{Query: "fromJson", SymbolID: 2, Name: "fromJson",
		FQN: "src/Gson.java:fromJson", Kind: "function", FilePath: "src/Gson.java"}
	rep, err := FindTestsForTarget(context.Background(), deps, runID, second, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProvenByGraph != 1 {
		t.Errorf("the second overload has one test calling it; found %d proven", rep.ProvenByGraph)
	}
	first := second
	first.SymbolID = 1
	rep, err = FindTestsForTarget(context.Background(), deps, runID, first, Options{SkipNaming: true, SkipColocation: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProvenByGraph != 0 {
		t.Errorf("the first overload has no test calling it; found %d proven", rep.ProvenByGraph)
	}
	if _, err := FindTestsForTarget(context.Background(), deps, runID, capability.Target{Name: "x"}, Options{}); err == nil {
		t.Error("a target with no symbol id was searched for")
	}
}
