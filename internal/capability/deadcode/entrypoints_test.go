package deadcode

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability/captest"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// A function a framework dispatches to has no caller by construction. Neither
// shape the index records may be offered as likely unused, and each must say
// which shape it is.
func TestFrameworkEntryPointsAreNeverLikely(t *testing.T) {
	listener := row(1, "onOrderPlaced")
	listener.ConsumesEvent = true
	handler := row(2, "createUser")
	both := row(3, "ingest")
	both.ConsumesEvent = true
	plain := row(4, "helper")

	src := &fakeSource{
		rows:     []registry.UnreferencedSymbol{listener, handler, both, plain},
		handlers: []string{"createUser", "ingest"},
	}
	rep := find(t, src, Options{})

	by := map[string]Candidate{}
	for _, c := range rep.Candidates {
		by[c.Name] = c
	}
	for name, want := range map[string][]Reason{
		"onOrderPlaced": {ReasonEventConsumer},
		"createUser":    {ReasonRouteHandler},
		"ingest":        {ReasonRouteHandler, ReasonEventConsumer},
	} {
		c := by[name]
		if c.Certainty != CertaintyPossible {
			t.Errorf("%s: certainty %q, want possible: a framework calls it", name, c.Certainty)
		}
		if len(c.Reasons) != len(want) {
			t.Errorf("%s: reasons %v, want %v", name, c.Reasons, want)
			continue
		}
		for i := range want {
			if c.Reasons[i] != want[i] {
				t.Errorf("%s: reasons %v, want %v", name, c.Reasons, want)
			}
		}
	}
	if c := by["helper"]; c.Certainty != CertaintyLikely {
		t.Errorf("helper = %+v: a definition no framework names must still be graded likely", c)
	}
	if rep.Likely != 1 || rep.Possible != 3 {
		t.Errorf("counts: likely %d possible %d, want 1 and 3", rep.Likely, rep.Possible)
	}
	if rep.EntryPoints != 3 {
		t.Errorf("entry points = %d, want 3 so the reader sees why the possible tier grew", rep.EntryPoints)
	}

	// Asked for the likely tier alone, none of them is listed.
	only := find(t, src, Options{MinCertainty: CertaintyLikely})
	if got := candidateNames(only); len(got) != 1 || got[0] != "helper" {
		t.Errorf("likely tier = %v, want [helper]", got)
	}
}

// A route dispatches to a function or a method. A type that happens to share
// the handler's name is not what the route meant, and demoting it would hide
// a real finding behind a coincidence.
func TestARouteHandlerNameDoesNotDemoteATypeOfTheSameName(t *testing.T) {
	class := row(1, "Create")
	class.Kind = "class"
	method := row(2, "Create")
	method.Kind = "method"

	rep := find(t, &fakeSource{
		rows:     []registry.UnreferencedSymbol{class, method},
		handlers: []string{"Create"},
		// Both definitions are named Create; each one's own line is not a use.
		selfAt: map[string][]int{"Create": {10, 20}},
	}, Options{})

	for _, c := range rep.Candidates {
		switch c.Kind {
		case "class":
			if c.Certainty != CertaintyLikely {
				t.Errorf("class Create = %+v, want likely: a route cannot dispatch to a type", c)
			}
		case "method":
			if c.Certainty != CertaintyPossible || len(c.Reasons) != 1 || c.Reasons[0] != ReasonRouteHandler {
				t.Errorf("method Create = %+v, want possible as a route handler", c)
			}
		}
	}
	if rep.EntryPoints != 1 {
		t.Errorf("entry points = %d, want 1", rep.EntryPoints)
	}
}

// Over the real registry, with the edges the indexer writes: endpoint
// -EXPOSES-> handler, and consumer -CONSUMES-> event.
func TestEntryPointsOverTheRealRegistry(t *testing.T) {
	d, runID := captest.Build(t,
		[]captest.Sym{
			captest.S(1, "GET /users", "api/routes.go").WithKind("endpoint"),
			captest.S(2, "listUsers", "api/users.go"),
			captest.S(3, "orders.placed", "events/orders.go").WithKind("event"),
			captest.S(4, "onOrderPlaced", "events/orders.go"),
			captest.S(5, "fetchUsers", "client/users.go"),
			captest.S(6, "orphan", "api/users.go"),
		},
		[]captest.Edge{
			captest.E(1, 2).WithKind(model.EdgeExposes),
			captest.E(4, 3).WithKind(model.EdgeConsumes),
			// A client call draws the same edge kind to an ENDPOINT. Calling
			// an HTTP API does not make the caller an entry point.
			captest.E(5, 1).WithKind(model.EdgeConsumes),
		},
		captest.Refs{},
	)
	src, ok := d.Registry.(Source)
	if !ok {
		t.Fatal("the registry does not satisfy deadcode.Source")
	}
	rep, err := Find(context.Background(), d, src, runID, Options{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Candidate{}
	for _, c := range rep.Candidates {
		by[c.Name] = c
	}

	if c, listed := by["listUsers"]; listed {
		t.Errorf("listUsers is the bound handler of GET /users and must not be a candidate at all: %+v", c)
	}
	if c := by["onOrderPlaced"]; c.Certainty != CertaintyPossible || len(c.Reasons) != 1 || c.Reasons[0] != ReasonEventConsumer {
		t.Errorf("onOrderPlaced = %+v, want possible as an event consumer", c)
	}
	if c := by["fetchUsers"]; c.Certainty != CertaintyLikely {
		t.Errorf("fetchUsers = %+v, want likely: requesting an endpoint is not consuming an event", c)
	}
	if c := by["orphan"]; c.Certainty != CertaintyLikely {
		t.Errorf("orphan = %+v, want likely", c)
	}
	if rep.EntryPoints != 1 {
		t.Errorf("entry points = %d, want 1", rep.EntryPoints)
	}
}
