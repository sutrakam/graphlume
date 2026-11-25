package infer

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// withParams is def() with a declared parameter count.
func withParams(e registry.SymbolIndexEntry, params model.Arity) registry.SymbolIndexEntry {
	e.ParamCount = params
	return e
}

func argsRef(id int64, name, kind, receiver string, line, args int) registry.Reference {
	ref := callRef(id, name, kind, receiver, line)
	if args >= 0 {
		ref.ArgCount = model.KnownArity(args)
	}
	return ref
}

// TestNarrowByArityExcludesOnlyTheImpossible: a declaration is set aside when
// both counts are known and differ, and for no other reason.
func TestNarrowByArityExcludesOnlyTheImpossible(t *testing.T) {
	group := []registry.SymbolIndexEntry{
		{ID: 1, ParamCount: model.KnownArity(1)},
		{ID: 2, ParamCount: model.KnownArity(2)},
		{ID: 3, ParamCount: model.ArityUnknown}, // variadic or defaulted
	}
	ids := func(cs []registry.SymbolIndexEntry) []int64 {
		var out []int64
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		args model.Arity
		want []int64
	}{
		{"unknown argument count excludes nothing", model.ArityUnknown, []int64{1, 2, 3}},
		{"two arguments", model.KnownArity(2), []int64{2, 3}},
		{"one argument", model.KnownArity(1), []int64{1, 3}},
		{"seven arguments leaves only the declaration that has no count", model.KnownArity(7), []int64{3}},
	} {
		got := ids(narrowByArity(registry.Reference{ArgCount: tc.args}, group))
		if len(got) != len(tc.want) {
			t.Errorf("%s: kept %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: kept %v, want %v", tc.name, got, tc.want)
			}
		}
	}
}

// TestChooseOverload: one left is chosen, several or none is a recorded choice.
func TestChooseOverload(t *testing.T) {
	one := withParams(registry.SymbolIndexEntry{ID: 10, Name: "get"}, model.KnownArity(1))
	two := withParams(registry.SymbolIndexEntry{ID: 11, Name: "get"}, model.KnownArity(2))
	twoAgain := withParams(registry.SymbolIndexEntry{ID: 12, Name: "get"}, model.KnownArity(2))
	none := registry.SymbolIndexEntry{ID: 13, Name: "get"}

	if o := chooseOverload(registry.Reference{ArgCount: model.KnownArity(2)}, []registry.SymbolIndexEntry{one, two}); !o.chosen || o.first.ID != 11 {
		t.Errorf("count 2 over {1,2}: %+v, want 11 chosen", o)
	}
	if o := chooseOverload(registry.Reference{ArgCount: model.KnownArity(2)}, []registry.SymbolIndexEntry{one, two, twoAgain}); o.chosen || !o.overloaded() || o.first.ID != 11 || o.allowed != 2 {
		t.Errorf("count 2 over {1,2,2}: %+v, want 11 taken of 2 allowed, not chosen", o)
	}
	if o := chooseOverload(registry.Reference{ArgCount: model.KnownArity(2)}, []registry.SymbolIndexEntry{one, two, none}); o.chosen {
		t.Errorf("a declaration with no count may take any: %+v chosen", o)
	}
	if o := chooseOverload(registry.Reference{ArgCount: model.KnownArity(5)}, []registry.SymbolIndexEntry{two, one}); o.chosen || o.allowed != 0 || o.first.ID != 10 {
		t.Errorf("count 5 over {2,1}: %+v, want the earliest taken and not chosen", o)
	}
	// No count anywhere (C++, Kotlin, C#, Scala today): the rule is a no-op,
	// and several declarations stay a choice.
	a, b := registry.SymbolIndexEntry{ID: 20, Name: "f"}, registry.SymbolIndexEntry{ID: 21, Name: "f"}
	if o := chooseOverload(registry.Reference{}, []registry.SymbolIndexEntry{b, a}); o.chosen || o.first.ID != 20 || !o.overloaded() {
		t.Errorf("no counts: %+v, want the earliest of two taken, overloaded", o)
	}
}

func javaIndex(counts ...model.Arity) map[string][]registry.SymbolIndexEntry {
	idx := []registry.SymbolIndexEntry{def(1, "class", "a/ClassName.java", "ClassName", 10)}
	for i, c := range counts {
		idx = append(idx, withParams(def(int64(2+i), "method", "a/ClassName.java", "ClassName.emit", 10), c))
	}
	return index(idx...)
}

func resolveGet(t *testing.T, idx map[string][]registry.SymbolIndexEntry, kind, receiver string, args int) (boundTo, string, bool) {
	t.Helper()
	r := New(idx, Options{})
	edges, _, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 20, Path: "a/Use.java", Language: "java",
		Symbols: within(99, "a/Use.java", "Use.run"),
		Refs:    []registry.Reference{argsRef(1, "emit", kind, receiver, 5, args)},
	})
	if len(edges) == 0 {
		return boundTo{}, "", false
	}
	return boundTo{edges[0].DstID, edges[0].Confidence, edges[0].Kind}, edges[0].Evidence.Detail, true
}

// TestOverloadedMemberOfAKnownTypeUsesTheArgumentCount is javapoet's
// ClassName.get("pkg", "Name"): three declarations, one count that fits.
func TestOverloadedMemberOfAKnownTypeUsesTheArgumentCount(t *testing.T) {
	idx := javaIndex(model.KnownArity(1), model.KnownArity(1), model.ArityUnknown)
	got, why, ok := resolveGet(t, idx, model.ReceiverName, "ClassName", 2)
	if !ok || got.dst != 4 || got.confidence != model.ConfidenceHigh {
		t.Fatalf("get(a, b) bound as %+v (%s), want the varargs declaration at high", got, why)
	}
}

// TestOverloadedMemberNeverClaimsMoreThanLow: two declarations the count
// allows is a choice, whatever else is known, and the reason says so and names
// the declaration taken.
func TestOverloadedMemberNeverClaimsMoreThanLow(t *testing.T) {
	idx := javaIndex(model.KnownArity(1), model.KnownArity(1))
	got, why, ok := resolveGet(t, idx, model.ReceiverName, "ClassName", 1)
	if !ok || got.dst != 2 || got.confidence != model.ConfidenceLow {
		t.Fatalf("get(a) bound as %+v (%s), want the first declaration at low", got, why)
	}
	for _, want := range []string{"overloaded", "2 declarations of emit", "a/ClassName.java:ClassName.emit"} {
		if !strings.Contains(why, want) {
			t.Errorf("reason %q lacks %q", why, want)
		}
	}
	if strings.Contains(why, "only member") {
		t.Errorf("reason %q calls an overloaded name the only member", why)
	}
}

// TestUnknownCountsLeaveOverloadsAtLow: where the parser records no counts the
// rule is a no-op, and the choice is still not claimed.
func TestUnknownCountsLeaveOverloadsAtLow(t *testing.T) {
	idx := javaIndex(model.ArityUnknown, model.ArityUnknown)
	got, _, ok := resolveGet(t, idx, model.ReceiverName, "ClassName", -1)
	if !ok || got.confidence != model.ConfidenceLow {
		t.Fatalf("bound as %+v, want low", got)
	}
}

// TestOnlyMemberRuleDoesNotSayOnlyForAnOverloadedName is the unknown-receiver
// rule: one type declares the name, but several times.
func TestOnlyMemberRuleDoesNotSayOnlyForAnOverloadedName(t *testing.T) {
	idx := javaIndex(model.KnownArity(1), model.KnownArity(1))
	got, why, ok := resolveGet(t, idx, model.ReceiverValue, "thing", 1)
	if !ok {
		t.Fatal("expected a binding")
	}
	if got.confidence != model.ConfidenceLow {
		t.Errorf("tier %s, want low", got.confidence)
	}
	if strings.Contains(why, "the only member") || !strings.Contains(why, "overloaded") {
		t.Errorf("reason %q must say the name is overloaded, not that it is the only member", why)
	}
	// With the count deciding, the rule keeps the tier it earned.
	idx = javaIndex(model.KnownArity(1), model.KnownArity(2))
	if got, why, ok = resolveGet(t, idx, model.ReceiverValue, "thing", 2); !ok || got.dst != 3 {
		t.Fatalf("get(a, b) bound as %+v (%s), want declaration 3", got, why)
	}
}
