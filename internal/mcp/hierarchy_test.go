package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// layered is monolog's shape: Handler is implemented by two types directly and
// reached by three more through AbstractHandler and Concrete.
func layered(b *indexBuilder) {
	b.file("a/Handler.java", "java", 40, typ("Handler", model.KindInterface, 1, 10))
	b.file("a/Abstract.java", "java", 40, typ("AbstractHandler", model.KindClass, 1, 30))
	b.file("a/Direct.java", "java", 40, typ("Direct", model.KindClass, 1, 30))
	b.file("a/Stream.java", "java", 40, typ("Stream", model.KindClass, 1, 30))
	b.file("a/Mail.java", "java", 40, typ("Mail", model.KindClass, 1, 30))
	b.file("a/Leaf.java", "java", 40, typ("Leaf", model.KindClass, 1, 30))
	h := symKey("a/Handler.java", "Handler", 1)
	abs := symKey("a/Abstract.java", "AbstractHandler", 1)
	b.edge(abs, h, model.EdgeImplements, model.ConfidenceHigh, "a/Abstract.java", 2)
	b.edge(symKey("a/Direct.java", "Direct", 1), h, model.EdgeImplements, model.ConfidenceHigh, "a/Direct.java", 2)
	b.edge(symKey("a/Stream.java", "Stream", 1), abs, model.EdgeExtends, model.ConfidenceHigh, "a/Stream.java", 2)
	b.edge(symKey("a/Mail.java", "Mail", 1), abs, model.EdgeExtends, model.ConfidenceMedium, "a/Mail.java", 2)
	// A type reached through a medium edge, then a low one: its path is low.
	b.edge(symKey("a/Leaf.java", "Leaf", 1), symKey("a/Mail.java", "Mail", 1),
		model.EdgeExtends, model.ConfidenceLow, "a/Leaf.java", 2)
	b.wellBound("java", ".java")
}

// TestFindImplementationsListsTheTypesThatReachTheTarget: it answered "4" for
// the four classes that name HandlerInterface while about a hundred reach it.
func TestFindImplementationsListsTheTypesThatReachTheTarget(t *testing.T) {
	fs := serverFor(t, layered)

	all := mustAnswer(t, fs, "implementations", map[string]any{"name": "Handler"})
	first := firstLine(all)
	if !strings.Contains(first, "5 implementing or extending it (2 directly, 3 through other types; direct_only=true lists the direct ones alone)") {
		t.Errorf("first line does not say how many of each: %q", first)
	}
	var names []string
	for _, line := range strings.Split(all, "\n") {
		if strings.HasPrefix(line, "- subtype ") {
			names = append(names, strings.Fields(line)[2])
		}
	}
	if len(names) != 5 {
		t.Fatalf("rows = %v, want 5 subtypes", names)
	}
	if direct := map[string]bool{names[0]: true, names[1]: true}; !direct["AbstractHandler"] || !direct["Direct"] {
		t.Errorf("direct subtypes are not listed first: %v", names)
	}
	if !strings.Contains(all, "depth 2: extends AbstractHandler, which reaches Handler") ||
		!strings.Contains(all, "depth 3: extends Mail, which reaches Handler") {
		t.Errorf("rows do not say how deep they are and through what:\n%s", all)
	}
	// Leaf's path runs through a low edge; it must not be reported stronger.
	for _, line := range strings.Split(all, "\n") {
		if strings.HasPrefix(line, "- subtype Leaf ") && !strings.Contains(line, "confidence=low") {
			t.Errorf("a type reached through a low edge is reported stronger than it: %s", line)
		}
	}

	direct := mustAnswer(t, fs, "implementations", map[string]any{"name": "Handler", "direct_only": true})
	dfirst := firstLine(direct)
	if !strings.Contains(dfirst, "2 implementing or extending it directly (3 more reach it through other types; direct_only=false lists them)") {
		t.Errorf("direct_only does not say what it left out: %q", dfirst)
	}
	if rowCount(direct) != 2 {
		t.Errorf("direct_only listed %d rows, want 2:\n%s", rowCount(direct), direct)
	}

	// Paged like the other list tools.
	page := mustAnswer(t, fs, "implementations", map[string]any{"name": "Handler", "limit": 2, "offset": 2})
	if !strings.Contains(page, "showing rows 3-4 of 5") || rowCount(page) != 2 {
		t.Errorf("subtypes are not paged:\n%s", page)
	}
}

// matcherTrait is fd's shape: a trait with six impl blocks, of which three are
// on types defined outside the repository and so have no edge.
func matcherTrait(b *indexBuilder) {
	b.file("src/patterns.rs", "rust", 100,
		typ("Matcher", model.KindInterface, 1, 10),
		typ("Glob", model.KindClass, 20, 30),
		typ("Regex", model.KindClass, 40, 50))
	m := symKey("src/patterns.rs", "Matcher", 1)
	for line, impl := range map[int]string{25: "Glob", 45: "Regex"} {
		src := symKey("src/patterns.rs", impl, map[string]int{"Glob": 20, "Regex": 40}[impl])
		b.edge(src, m, model.EdgeImplements, model.ConfidenceHigh, "src/patterns.rs", line)
		b.superRef("src/patterns.rs", "Matcher", line, "reference.implementation", registry.ResolutionResolved, m)
	}
	// On types the index does not define: bound, and no edge.
	for _, line := range []int{60, 70} {
		b.superRef("src/patterns.rs", "Matcher", line, "reference.implementation", registry.ResolutionResolved, m)
	}
	b.superRef("src/patterns.rs", "Matcher", 80, "reference.implementation", registry.ResolutionUnresolved, "")
	b.wellBound("rust", ".rs")
}

// TestAHierarchyAnswerSaysWhatDidNotBecomeAnEdge: "3 … verdict strong" for a
// trait with six impl blocks, three of them absent without a word.
func TestAHierarchyAnswerSaysWhatDidNotBecomeAnEdge(t *testing.T) {
	fs := serverFor(t, matcherTrait)
	text := mustAnswer(t, fs, "implementations", map[string]any{"name": "Matcher"})
	first := firstLine(text)
	if !strings.HasPrefix(first, leadIncomplete+" — ") {
		t.Errorf("an answer with supertype references outstanding does not lead INCOMPLETE: %q", first)
	}
	if !strings.Contains(first, `3 of the 5 supertype reference(s) spelled "Matcher" did not become a subtype below (2 on a type this index does not define, 1 unresolved)`) {
		t.Errorf("first line does not count the references that became no edge: %q", first)
	}
	if strings.Contains(text, "verdict: strong") || strings.Contains(text, "verdict: proven") {
		t.Errorf("a hierarchy with references outstanding was graded strong:\n%s", text)
	}
	if !strings.Contains(first, "2 implementing or extending it, all directly") {
		t.Errorf("first line lost the count of what was found: %q", first)
	}
}

// subscripted is click's shape: `class Choice(ParamType[str])` is a subtype the
// extractor did not read, so it is no reference and no edge.
func subscripted(b *indexBuilder) {
	b.fileText("src/types.py", "python", strings.Join([]string{
		"class ParamType:",
		"    name = 'x'",
		"",
		"class Plain(ParamType):",
		"    pass",
		"",
		"class Choice(ParamType[str]):",
		"    pass",
		"",
		"def convert(value) -> ParamType:",
		"    return value",
		"",
	}, "\n"),
		typ("ParamType", model.KindClass, 1, 2), typ("Plain", model.KindClass, 4, 5), typ("Choice", model.KindClass, 7, 8))
	b.edge(symKey("src/types.py", "Plain", 4), symKey("src/types.py", "ParamType", 1),
		model.EdgeExtends, model.ConfidenceHigh, "src/types.py", 4)
	b.superRef("src/types.py", "ParamType", 4, "reference.inherit", registry.ResolutionResolved, symKey("src/types.py", "ParamType", 1))
	b.wellBound("python", ".py")
}

// TestADeclarationTheExtractorMissedIsCounted: 13 of 31 returned as plain fact.
func TestADeclarationTheExtractorMissedIsCounted(t *testing.T) {
	fs := serverFor(t, subscripted)
	text := mustAnswer(t, fs, "implementations", map[string]any{"name": "ParamType"})
	first := firstLine(text)
	if !strings.HasPrefix(first, leadIncomplete+" — ") ||
		!strings.Contains(first, "1 other line(s) declare a named type whose supertype list names it and are not recorded as a subtype") {
		t.Errorf("a class declaration the index did not record is not counted: %q", first)
	}
	if strings.Contains(text, "verdict: strong") {
		t.Errorf("graded strong with a declaration unaccounted for:\n%s", text)
	}
	// The return annotation names the type on a line that declares nothing.
	if strings.Contains(first, "2 other line(s)") {
		t.Errorf("a signature that names the type was counted as a declaration: %q", first)
	}
}

// TestACyclicHierarchyTerminates: bad data (two types that extend each other)
// must neither loop nor list the target as its own subtype.
func TestACyclicHierarchyTerminates(t *testing.T) {
	fs := serverFor(t, func(b *indexBuilder) {
		b.file("a/Cycle.java", "java", 40, typ("A", model.KindClass, 1, 10), typ("B", model.KindClass, 20, 30))
		b.edge(symKey("a/Cycle.java", "B", 20), symKey("a/Cycle.java", "A", 1), model.EdgeExtends, model.ConfidenceHigh, "a/Cycle.java", 21)
		b.edge(symKey("a/Cycle.java", "A", 1), symKey("a/Cycle.java", "B", 20), model.EdgeExtends, model.ConfidenceHigh, "a/Cycle.java", 2)
		b.wellBound("java", ".java")
	})
	text := mustAnswer(t, fs, "implementations", map[string]any{"name": "A"})
	if first := firstLine(text); !strings.Contains(first, "1 implementing or extending it") {
		t.Errorf("a cycle was not collapsed to the one other type: %q", first)
	}
}

// TestACompleteHierarchyIsLeftAlone: nothing outstanding, nothing said.
func TestACompleteHierarchyIsLeftAlone(t *testing.T) {
	fs := serverFor(t, layered)
	first := firstLine(mustAnswer(t, fs, "implementations", map[string]any{"name": "AbstractHandler"}))
	if strings.Contains(first, leadIncomplete) && !strings.Contains(first, "rows") {
		t.Errorf("a hierarchy with nothing outstanding was doubted: %q", first)
	}
}
