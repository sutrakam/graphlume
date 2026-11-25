package infer

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// phpHandlers is monolog's shape: a Handler base declaring close(), a
// SocketHandler that extends it and declares close() again, and a type that is
// unrelated to either and declares it too.
func phpHandlers() map[string][]registry.SymbolIndexEntry {
	return index(
		def(1, "class", "src/Handler.php", "Handler", 10),
		def(2, "method", "src/Handler.php", "Handler.close", 10),
		def(3, "class", "src/SocketHandler.php", "SocketHandler", 11),
		def(4, "method", "src/SocketHandler.php", "SocketHandler.close", 11),
		def(5, "class", "src/Pool.php", "Pool", 12),
		def(6, "method", "src/Pool.php", "Pool.close", 12),
	)
}

func closeOnHandler() registry.FileRefs {
	return registry.FileRefs{
		FileID: 20, Path: "tests/UseTest.php", Language: "php",
		Symbols: within(99, "tests/UseTest.php", "UseTest.testClose"),
		Refs: []registry.Reference{
			callRef(1, "close", model.ReceiverName, "handler", 5),
		},
	}
}

// TestNameHintedBindingIsDemotedWhenASubtypeOverridesIt: `$handler->close()`
// is named after Handler, and SocketHandler extends Handler and declares
// close() itself. The receiver may be either, so the base method is not a
// `high` answer.
func TestNameHintedBindingIsDemotedWhenASubtypeOverridesIt(t *testing.T) {
	anc := &fakeAncestry{edges: map[string][]string{"sockethandler": {"Handler"}}}
	r := New(phpHandlers(), Options{Ancestry: anc})
	edges, _, st := runTwoPasses(t, r, closeOnHandler())
	if len(edges) != 1 {
		t.Fatalf("want the one deferred edge, got %+v", edges)
	}
	e := edges[0]
	if e.DstID != 2 || e.Confidence != model.ConfidenceLow {
		t.Errorf("bound to %d at %s, want Handler.close at low", e.DstID, e.Confidence)
	}
	for _, want := range []string{"SocketHandler", "descend from Handler", "declare close again"} {
		if !strings.Contains(e.Evidence.Detail, want) {
			t.Errorf("reason %q lacks %q", e.Evidence.Detail, want)
		}
	}
	if st.Resolved != 1 || st.Ambiguous != 0 {
		t.Errorf("stats %+v: the parked reference must end up counted once, as resolved", st)
	}
}

// TestNameHintedBindingStandsWhenNoDeclarerDescendsFromIt: Pool is not a
// Handler, so its own close() says nothing about what `$handler` holds.
func TestNameHintedBindingStandsWhenNoDeclarerDescendsFromIt(t *testing.T) {
	anc := &fakeAncestry{edges: map[string][]string{"sockethandler": {"Base"}}}
	r := New(phpHandlers(), Options{Ancestry: anc})
	edges, _, _ := runTwoPasses(t, r, closeOnHandler())
	if len(edges) != 1 || edges[0].DstID != 2 || edges[0].Confidence != model.ConfidenceHigh {
		t.Fatalf("want Handler.close at high, got %+v", edges)
	}
}

// TestNameHintedBindingIsUnchangedWithoutAnAncestry: nothing is parked and the
// binding is made as it always was.
func TestNameHintedBindingIsUnchangedWithoutAnAncestry(t *testing.T) {
	r := New(phpHandlers(), Options{})
	edges, _, _ := resolveOne(t, r, closeOnHandler())
	if len(edges) != 1 || edges[0].DstID != 2 || edges[0].Confidence != model.ConfidenceHigh {
		t.Fatalf("want Handler.close at high, got %+v", edges)
	}
}

// TestAMemberIsLookedUpOnTheReceiversOwnClassFirst pins the order for a
// receiver whose type the source states: the class's own member before an
// ancestor's, for every shape that names the class.
func TestAMemberIsLookedUpOnTheReceiversOwnClassFirst(t *testing.T) {
	idx := phpHandlers()
	fr := registry.FileRefs{
		FileID: 20, Path: "tests/UseTest.php", Language: "php",
		Symbols: within(99, "tests/UseTest.php", "UseTest.testClose"),
		Refs: []registry.Reference{
			callRef(1, "close", model.ReceiverType, "SocketHandler", 5),
			callRef(2, "close", model.ReceiverName, "SocketHandler", 6),
		},
	}
	anc := &fakeAncestry{edges: map[string][]string{"sockethandler": {"Handler"}}}
	r := New(idx, Options{Ancestry: anc})
	edges, _, _ := resolveOne(t, r, fr)
	got := edgesByLine(edges)
	for _, line := range []int{5, 6} {
		if got[line].dst != 4 {
			t.Errorf("line %d: bound to %d, want SocketHandler.close (4), not its base's", line, got[line].dst)
		}
	}
}
