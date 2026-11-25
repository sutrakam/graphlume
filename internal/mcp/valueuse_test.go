package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// passedByName is koa's shape: `respond` is reached only through
// `.then(handleResponse)`, where handleResponse is passed by name. The call
// from handleResponse to respond is recorded; the use of handleResponse is not,
// and it has no caller in the graph.
func passedByName(b *indexBuilder) {
	b.fileText("lib/app.js", "javascript", strings.Join([]string{
		"function respond(ctx) {",
		"  return ctx.body",
		"}",
		"const handleResponse = () => respond(ctx)",
		"function callback() {",
		"  return fn.call(ctx).then(handleResponse)",
		"}",
		"",
	}, "\n"),
		fn("respond", 1, 3), fn("handleResponse", 4, 4), fn("callback", 5, 7))
	b.call(symKey("lib/app.js", "handleResponse", 4), symKey("lib/app.js", "respond", 1),
		"respond", "", "lib/app.js", 4, model.ConfidenceHigh)
	b.wellBound("javascript", ".js")
}

// TestAFunctionPassedAsAValueIsNotReportedAsHavingNoCallers is the koa case:
// every question about `respond` said 1 caller, no test, certainty high, and the
// whole suite goes through it.
func TestAFunctionPassedAsAValueIsNotReportedAsHavingNoCallers(t *testing.T) {
	fs := serverFor(t, passedByName)

	impact := mustAnswer(t, fs, "impact", map[string]any{"symbol": "respond"})
	first := firstLine(impact)
	if !strings.HasPrefix(first, leadIncomplete+" — ") {
		t.Errorf("impact does not lead INCOMPLETE: %q", first)
	}
	for _, want := range []string{
		"handleResponse, on the path to it, is named in 1 other place(s) not recorded as calls",
		"it may be passed as a value",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("impact first line does not say %q: %q", want, first)
		}
	}
	if !strings.Contains(first, "or higher") {
		t.Errorf("a risk on a path that may be longer prints no 'or higher': %q", first)
	}
	if strings.Contains(impact, "Certainty high") {
		t.Errorf("certainty is high with a function passed as a value on the path:\n%s", impact)
	}
	if !strings.Contains(impact, "NOT VISIBLE (name_in_unrecorded_places)") {
		t.Errorf("the gap is not listed among the unknowns:\n%s", impact)
	}
	if strings.Contains(impact, "No test symbol was found reaching this target") {
		t.Errorf("impact states that no test reaches the target as a fact:\n%s", impact)
	}
	if _, urged := containsAny(impact, goAheadWords); urged {
		t.Errorf("impact tells the agent to act on it:\n%s", impact)
	}

	tests := mustAnswer(t, fs, "tests", map[string]any{"symbol": "respond"})
	tfirst := firstLine(tests)
	if !strings.Contains(tfirst, "NOT KNOWN") || strings.Contains(tfirst, "0 test(s)") {
		t.Errorf("tests states an absence of tests as a fact: %q", tfirst)
	}
	if !strings.HasPrefix(tfirst, leadIncomplete+" — ") {
		t.Errorf("tests does not lead INCOMPLETE: %q", tfirst)
	}

	plan := mustAnswer(t, fs, "plan", map[string]any{"symbol": "respond"})
	if pfirst := firstLine(plan); !strings.HasPrefix(pfirst, leadIncomplete+" — ") || !strings.Contains(pfirst, "or higher") {
		t.Errorf("plan does not lead INCOMPLETE with a floor: %q", pfirst)
	}

	// The function that is passed by value is itself a target of callers:
	// it has no caller in the graph, and its name occurs in one more place.
	callers := mustAnswer(t, fs, "callers", map[string]any{"name": "handleResponse"})
	cfirst := firstLine(callers)
	if !strings.HasPrefix(cfirst, leadIncomplete+" — ") ||
		!strings.Contains(cfirst, "its name appears in 1 other place(s) that are not recorded as calls: it may be passed as a value") {
		t.Errorf("callers does not say the name appears elsewhere: %q", cfirst)
	}
}

// TestANameUsedOnlyWhereACallIsRecordedIsLeftAlone: the check must not turn
// every answer into a doubt. `respond` as a target has its definition and one
// recorded call and nothing else.
func TestANameUsedOnlyWhereACallIsRecordedIsLeftAlone(t *testing.T) {
	fs := serverFor(t, passedByName)
	callers := mustAnswer(t, fs, "callers", map[string]any{"name": "respond"})
	if first := firstLine(callers); strings.Contains(first, leadIncomplete) || strings.Contains(first, "passed as a value") {
		t.Errorf("a name with no unrecorded use was doubted: %q", first)
	}
}

// TestARecursiveCallAndAnImportAreNotUsesAsAValue: a mention inside the
// function's own span, and an import line, are not a use by something else.
func TestARecursiveCallAndAnImportAreNotUsesAsAValue(t *testing.T) {
	fs := serverFor(t, func(b *indexBuilder) {
		b.fileText("lib/walk.js", "javascript", strings.Join([]string{
			"import { walk } from './other'",
			"function walk(n) {",
			"  return n && walk(n.next)",
			"}",
			"",
		}, "\n"), fn("walk", 2, 4))
		b.wellBound("javascript", ".js")
	})
	callers := mustAnswer(t, fs, "callers", map[string]any{"name": "walk"})
	if first := firstLine(callers); strings.Contains(first, "passed as a value") {
		t.Errorf("a recursive call or an import counted as a use as a value: %q", first)
	}
}

// TestAConstructorIsNotAFunctionPassedAsAValue: serilog's LoggerConfiguration is
// a method spelled like the class, and the class is written everywhere.
func TestAConstructorIsNotAFunctionPassedAsAValue(t *testing.T) {
	fs := serverFor(t, func(b *indexBuilder) {
		b.fileText("src/Config.cs", "c_sharp", strings.Join([]string{
			"class Config {",
			"  public Config() {}",
			"}",
			"class Use {",
			"  Config Make(Config other) { return other; }",
			"}",
			"",
		}, "\n"),
			typ("Config", model.KindClass, 1, 3), method("Config", 2, 2, 0).ownedBy("Config"),
			typ("Use", model.KindClass, 4, 6))
		b.wellBound("c_sharp", ".cs")
	})
	callers := mustAnswer(t, fs, "callers", map[string]any{"name": "Config"})
	if first := firstLine(callers); strings.Contains(first, "passed as a value") {
		t.Errorf("a constructor was reported as possibly passed as a value: %q", first)
	}
}

// belowTheFloor is google/benchmark's IsZero: callers exist, every one at low
// confidence, and the default floor of impact is medium.
func belowTheFloor(b *indexBuilder) {
	b.file("src/b.cc", "cpp", 80, fn("IsZero", 1, 5), fn("Runner", 10, 20), fn("Instance", 30, 40), fn("Strong", 50, 60))
	b.call(symKey("src/b.cc", "Runner", 10), symKey("src/b.cc", "IsZero", 1), "IsZero", "", "src/b.cc", 12, model.ConfidenceLow)
	b.call(symKey("src/b.cc", "Instance", 30), symKey("src/b.cc", "IsZero", 1), "IsZero", "", "src/b.cc", 32, model.ConfidenceLow)
	b.call(symKey("src/b.cc", "Instance", 30), symKey("src/b.cc", "IsZero", 1), "IsZero", "", "src/b.cc", 33, model.ConfidenceLow)
	b.wellBound("cpp", ".cc")
}

// TestAZeroThatIsOnlyTheFloorIsNotReportedAsZero: "0 direct" was the first
// line of an answer about a function with three callers, and the disclosure was
// after the sixth line.
func TestAZeroThatIsOnlyTheFloorIsNotReportedAsZero(t *testing.T) {
	fs := serverFor(t, belowTheFloor)
	text := mustAnswer(t, fs, "impact", map[string]any{"symbol": "IsZero"})
	first := firstLine(text)
	if !strings.HasPrefix(first, leadIncomplete+" — ") {
		t.Errorf("a zero produced only by the floor does not lead INCOMPLETE: %q", first)
	}
	for _, want := range []string{
		"0 direct callers are shown only because 2 at low confidence are excluded",
		"pass min_confidence=low",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first line does not say %q: %q", want, first)
		}
	}
	if strings.Contains(first, "Risk low (") {
		t.Errorf("a bare low risk over callers the floor removed: %q", first)
	}

	// With the floor lowered the callers are the answer, and nothing is withheld.
	all := mustAnswer(t, fs, "impact", map[string]any{"symbol": "IsZero", "min_confidence": "low"})
	if f := firstLine(all); strings.Contains(f, "excluded") || !strings.Contains(f, "2 direct") {
		t.Errorf("min_confidence=low still reports exclusions or misses the callers: %q", f)
	}
}

// TestTheFloorIsOnTheFirstLineWhenItHidesMore: callers shown and more at low
// confidence beyond them.
func TestTheFloorIsOnTheFirstLineWhenItHidesMore(t *testing.T) {
	fs := serverFor(t, func(b *indexBuilder) {
		belowTheFloor(b)
		b.call(symKey("src/b.cc", "Strong", 50), symKey("src/b.cc", "IsZero", 1), "IsZero", "", "src/b.cc", 52, model.ConfidenceHigh)
	})
	first := firstLine(mustAnswer(t, fs, "impact", map[string]any{"symbol": "IsZero"}))
	if !strings.Contains(first, "1 direct") || !strings.Contains(first, "2 more symbol(s) at low confidence are excluded") ||
		!strings.Contains(first, "pass min_confidence=low") {
		t.Errorf("first line does not size what the floor kept out: %q", first)
	}
	if strings.HasPrefix(first, leadIncomplete) {
		t.Errorf("a non-empty answer was marked INCOMPLETE for the floor alone: %q", first)
	}
}

func mustAnswer(t *testing.T, fs fullServer, tool string, args map[string]any) string {
	t.Helper()
	text, res := answer(t, fs.sess, tool, args)
	if res.IsError {
		t.Fatalf("%s failed:\n%s", tool, text)
	}
	return text
}

// cutAtTheDepthLimit is cobra's getCompletions: a chain of callers longer than
// the depth limit, with a test inside it so that the score alone is low.
func cutAtTheDepthLimit(b *indexBuilder) {
	b.file("c/chain.go", "go", 120, fn("target", 1, 5), fn("one", 10, 15), fn("two", 20, 25), fn("three", 30, 35), fn("four", 40, 45))
	b.file("c/chain_test.go", "go", 40, fn("TestTwo", 1, 8))
	b.call(symKey("c/chain.go", "one", 10), symKey("c/chain.go", "target", 1), "target", "", "c/chain.go", 12, model.ConfidenceHigh)
	b.call(symKey("c/chain.go", "two", 20), symKey("c/chain.go", "one", 10), "one", "", "c/chain.go", 22, model.ConfidenceHigh)
	b.call(symKey("c/chain.go", "three", 30), symKey("c/chain.go", "two", 20), "two", "", "c/chain.go", 32, model.ConfidenceHigh)
	b.call(symKey("c/chain.go", "four", 40), symKey("c/chain.go", "three", 30), "three", "", "c/chain.go", 42, model.ConfidenceHigh)
	b.call(symKey("c/chain_test.go", "TestTwo", 1), symKey("c/chain.go", "two", 20), "two", "", "c/chain_test.go", 3, model.ConfidenceHigh)
	b.wellBound("go", ".go")
}

// TestALowRiskOnACutAnalysisIsAFloor: "Risk low" was printed over four nodes at
// the depth limit that were never expanded.
func TestALowRiskOnACutAnalysisIsAFloor(t *testing.T) {
	fs := serverFor(t, cutAtTheDepthLimit)
	text := mustAnswer(t, fs, "impact", map[string]any{"symbol": "target"})
	first := firstLine(text)
	if !strings.Contains(text, "truncated: depth limit 3 reached") {
		t.Fatalf("the fixture was not cut at the depth limit:\n%s", text)
	}
	if !strings.Contains(first, "Risk low or higher (") || !strings.Contains(first, "the analysis was cut at depth 3") {
		t.Errorf("a low risk over a cut analysis is not marked as a floor: %q", first)
	}
	if strings.Contains(first, "Risk low (score") {
		t.Errorf("a bare low risk over a cut analysis: %q", first)
	}
}
