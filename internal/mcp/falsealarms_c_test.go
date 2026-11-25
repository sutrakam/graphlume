package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// ---------------------------------------------------------------- C: a function that holds an anonymous class

// anonymousInsideFunction is hono's Router: an anonymous class expression inside
// buildInitParams extends RegExpRouter, and the index attributes the edge to the
// function that contains it.
func anonymousInsideFunction(b *indexBuilder) {
	b.file("src/router.ts", "typescript", 20, typ("Router", model.KindInterface, 1, 5))
	b.file("src/reg.ts", "typescript", 20, typ("RegExpRouter", model.KindClass, 1, 10), typ("Trie", model.KindClass, 11, 20))
	b.file("src/prepared.ts", "typescript", 30, fn("buildInitParams", 5, 25))
	router := symKey("src/router.ts", "Router", 1)
	reg := symKey("src/reg.ts", "RegExpRouter", 1)
	b.edge(reg, router, model.EdgeImplements, model.ConfidenceHigh, "src/reg.ts", 2)
	b.edge(symKey("src/reg.ts", "Trie", 11), router, model.EdgeImplements, model.ConfidenceHigh, "src/reg.ts", 12)
	b.edge(symKey("src/prepared.ts", "buildInitParams", 5), reg, model.EdgeExtends, model.ConfidenceHigh, "src/prepared.ts", 8)
	b.wellBound("typescript", ".ts")
}

func TestAFunctionHoldingAnAnonymousClassIsNotListedAsASubtype(t *testing.T) {
	fs := serverFor(t, anonymousInsideFunction)
	text := mustAnswer(t, fs, "implementations", map[string]any{"name": "Router"})
	first := firstLine(text)
	if !strings.Contains(first, "2 implementing or extending it, all directly; 1 anonymous or local type(s) inside functions also extend it") {
		t.Errorf("first line does not count the types and the anonymous one apart: %q", first)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- subtype buildInitParams") {
			t.Errorf("a function is listed as a subtype: %s", line)
		}
	}
	if rowCount(text) != 2 {
		t.Errorf("rows = %d, want the two types only:\n%s", rowCount(text), text)
	}
	if !strings.Contains(text, "inside buildInitParams (src/prepared.ts:8)") {
		t.Errorf("the enclosing function is not named:\n%s", text)
	}

	// Seen from the class the function extends, it is a subtype of that too.
	reg := mustAnswer(t, fs, "implementations", map[string]any{"name": "RegExpRouter"})
	if first := firstLine(reg); !strings.Contains(first, "0 implementing or extending it, all directly; 1 anonymous or local type(s) inside functions") {
		t.Errorf("RegExpRouter: %q", first)
	}
}

// anonymousKotlinObject is moshi's JsonAdapter: the named subclasses are all in
// the list and only an anonymous `object :` expression is missing.
func TestAnAnonymousObjectIsSaidPreciselyOnTheFirstLine(t *testing.T) {
	fs := serverFor(t, func(b *indexBuilder) {
		b.fileText("src/JsonAdapter.kt", "kotlin", "abstract class JsonAdapter<T>\n", typ("JsonAdapter", model.KindClass, 1, 1))
		b.fileText("src/Named.kt", "kotlin", "class Named : JsonAdapter<String>()\n", typ("Named", model.KindClass, 1, 1))
		b.edge(symKey("src/Named.kt", "Named", 1), symKey("src/JsonAdapter.kt", "JsonAdapter", 1),
			model.EdgeExtends, model.ConfidenceHigh, "src/Named.kt", 1)
		b.superRef("src/Named.kt", "JsonAdapter", 1, "reference.extends", registry.ResolutionResolved,
			symKey("src/JsonAdapter.kt", "JsonAdapter", 1))
		b.fileText("src/Factory.kt", "kotlin", "fun make() = object : JsonAdapter<Boolean?>() {\n}\n", fn("make", 1, 2))
		b.fileText("README.md", "markdown", "class Doc : JsonAdapter<Foo>()\n")
		b.wellBound("kotlin", ".kt")
	})
	first := firstLine(mustAnswer(t, fs, "implementations", map[string]any{"name": "JsonAdapter"}))
	if !strings.HasPrefix(first, leadIncomplete+" — 1 anonymous class or object expression(s) name it as a supertype and are not listed") {
		t.Errorf("the anonymous object is not what the first line says: %q", first)
	}
	if strings.Contains(first, "read as a type declaration") || strings.Contains(first, "named type") {
		t.Errorf("the first line still claims a named type is missing: %q", first)
	}
}
