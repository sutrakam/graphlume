package planchange

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/captest"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/registry"
)

// TestAPlanSaysWhenItsCallersCouldNotBeDetermined: "Risk LOW (20/100), 0
// direct caller(s)" was the plan for a function whose twelve callers the index
// had bound and then recorded no edge for. A plan with no caller steps is a
// plan to change a signature and update nothing.
func TestAPlanSaysWhenItsCallersCouldNotBeDetermined(t *testing.T) {
	refs := captest.Refs{Resolved: 6}
	for i := 0; i < 12; i++ {
		refs.Named = append(refs.Named, captest.NamedRef{
			Name: "defineGetter", Path: "lib/request.go", Line: 100 + i,
			Resolution: registry.ResolutionResolved, Target: 1})
	}
	deps, runID := captest.Build(t, []captest.Sym{captest.S(1, "defineGetter", "lib/request.go")}, nil, refs)

	plan, err := NewPlanner(deps).Plan(context.Background(), runID, "defineGetter", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if plan.RiskLevel != impact.LevelUnknown {
		t.Errorf("RiskLevel = %q, want %q", plan.RiskLevel, impact.LevelUnknown)
	}
	if !strings.Contains(plan.RiskUndetermined, "12 bound to it from outside any indexed function") {
		t.Errorf("RiskUndetermined = %q", plan.RiskUndetermined)
	}
	if plan.TestsUndetermined == "" {
		t.Error("the plan's test list is not marked undetermined although no call to the target could be followed")
	}
}

// TestPlanTargetPlansTheDefinitionGiven: overloads share a qualified name, and
// the plan must not re-resolve its tests by that name.
func TestPlanTargetPlansTheDefinitionGiven(t *testing.T) {
	deps, runID := captest.Build(t,
		[]captest.Sym{
			captest.S(1, "fromJson", "src/Gson.java").WithFQN("src/Gson.java:fromJson"),
			captest.S(2, "fromJson", "src/Gson.java").WithFQN("src/Gson.java:fromJson"),
			captest.S(3, "load", "src/Loader.java"),
		},
		[]captest.Edge{captest.E(3, 2)}, captest.Refs{Resolved: 4})

	second := capability.Target{Query: "fromJson", SymbolID: 2, Name: "fromJson",
		FQN: "src/Gson.java:fromJson", Kind: "function", FilePath: "src/Gson.java"}
	plan, err := NewPlanner(deps).PlanTarget(context.Background(), runID, second, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target.SymbolID != 2 || plan.DirectCallers != 1 {
		t.Errorf("planned symbol %d with %d direct caller(s); want the second overload and its one caller",
			plan.Target.SymbolID, plan.DirectCallers)
	}

	// By name the two are indistinguishable, and Plan says so instead of
	// choosing.
	_, err = NewPlanner(deps).Plan(context.Background(), runID, "fromJson", DefaultOptions())
	var amb *capability.AmbiguousTargetError
	if !errors.As(err, &amb) {
		t.Errorf("err = %v, want an ambiguity", err)
	}
}
