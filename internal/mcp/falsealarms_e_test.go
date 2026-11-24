package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// ---------------------------------------------------------------- E: sentences that are true

// oneDefinitionAmbiguousCalls is click's convert_type: one definition and
// references that were not bound to it.
func TestAmbiguousIsExplainedByTheNumberOfDefinitions(t *testing.T) {
	one := serverFor(t, unboundName)
	text := mustAnswer(t, one, "impact", map[string]any{"symbol": "decode"})
	if strings.Contains(text, "several definitions share the name") {
		t.Errorf("a name with one definition is said to be shared by several:\n%s", text)
	}
	if !strings.Contains(text, "9 ambiguous (the index holds one definition of this name; the evidence at these calls did not bind them to it)") {
		t.Errorf("the ambiguous count is not explained for a single definition:\n%s", text)
	}

	many := serverFor(t, func(b *indexBuilder) {
		b.file("a/codec.go", "go", 60, fn("decode", 5, 15))
		b.file("b/codec.go", "go", 60, fn("decode", 5, 15))
		for i := 0; i < 4; i++ {
			b.ref("a/codec.go", "decode", "c", 45+i, registry.ResolutionAmbiguous, "")
		}
		b.wellBound("go", ".go")
	})
	text = mustAnswer(t, many, "impact", map[string]any{"symbol": "decode", "path": "a/codec.go"})
	if !strings.Contains(text, "4 ambiguous (several definitions share the name and none was chosen)") {
		t.Errorf("a name with two definitions lost its explanation:\n%s", text)
	}
}

func recursiveWithTwoCallSites(b *indexBuilder) {
	b.file("lib/q.rb", "ruby", 60, fn("build_query", 1, 10), fn("env_for", 20, 30))
	target := symKey("lib/q.rb", "build_query", 1)
	b.call(symKey("lib/q.rb", "env_for", 20), target, "build_query", "", "lib/q.rb", 22, model.ConfidenceHigh)
	b.call(symKey("lib/q.rb", "env_for", 20), target, "build_query", "", "lib/q.rb", 24, model.ConfidenceHigh)
	b.call(target, target, "build_query", "", "lib/q.rb", 5, model.ConfidenceHigh)
	b.wellBound("ruby", ".rb")
}

func TestTheCrossReferenceToFindCallersQuotesNoWrongNumber(t *testing.T) {
	fs := serverFor(t, recursiveWithTwoCallSites)
	impact := mustAnswer(t, fs, "impact", map[string]any{"symbol": "build_query"})
	callers := mustAnswer(t, fs, "callers", map[string]any{"name": "build_query"})
	// Two functions call it (env_for twice, and itself once): the first line
	// counts the functions and says how many call sites they make.
	if !strings.Contains(firstLine(callers), "2 caller(s) of build_query (lib/q.rb:1) are recorded, at 3 call sites.") {
		t.Fatalf("fixture: callers should count two callers at three call sites:\n%s", callers)
	}
	if strings.Contains(impact, "which is what callers lists") {
		t.Errorf("impact says its site count is what callers lists, which lists 3:\n%s", impact)
	}
	if !strings.Contains(impact, "they make 2 call site(s) between them. callers lists every recorded call site") {
		t.Errorf("the note does not say how the two tools count:\n%s", impact)
	}
}

// deepChainWithUnboundCallers: callers four deep, and references to the target
// that were bound to nothing, so the level is unknown.
func deepChainWithUnboundCallers(b *indexBuilder) {
	b.file("lib/chain.go", "go", 100, fn("f0", 1, 5), fn("f1", 10, 15), fn("f2", 20, 25), fn("f3", 30, 35), fn("f4", 40, 45), fn("f5", 50, 55))
	for i := 1; i <= 5; i++ {
		callee := []string{"f0", "f1", "f2", "f3", "f4"}[i-1]
		calleeLine := []int{1, 10, 20, 30, 40}[i-1]
		caller := []string{"f1", "f2", "f3", "f4", "f5"}[i-1]
		callerLine := []int{10, 20, 30, 40, 50}[i-1]
		b.call(symKey("lib/chain.go", caller, callerLine), symKey("lib/chain.go", callee, calleeLine),
			callee, "", "lib/chain.go", callerLine+2, model.ConfidenceHigh)
	}
	for i := 0; i < 3; i++ {
		b.ref("lib/chain.go", "f0", "x", 70+i, registry.ResolutionAmbiguous, "")
	}
	b.wellBound("go", ".go")
}

func TestAnUnknownRiskStillSaysItWasCutAtADepthLimit(t *testing.T) {
	fs := serverFor(t, deepChainWithUnboundCallers)
	text := mustAnswer(t, fs, "impact", map[string]any{"symbol": "f0"})
	first := firstLine(text)
	if !strings.Contains(first, "Risk unknown") {
		t.Fatalf("fixture: the risk should be unknown: %q", first)
	}
	if !strings.Contains(first, "cut at depth 3") {
		t.Errorf("an unknown risk does not carry its depth limit on the first line: %q", first)
	}
	if !strings.Contains(text, "truncated: depth limit 3 reached") {
		t.Errorf("fixture: the walk was not truncated:\n%s", text)
	}
}
