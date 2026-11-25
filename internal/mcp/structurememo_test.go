package mcp

import (
	"reflect"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
)

// A folder after a folder reads the shared base once and answers exactly as a
// fresh read would: the memo changes the cost of structure, never its answer
// (g2 F08: the roll-up for any folder within budget on a large index).
func TestStructureBaseIsReadOncePerRunAndFloorAndChangesNoAnswer(t *testing.T) {
	fs := newFullServer(t, seedFolders)
	first, _, isErr := structureCall(t, fs, map[string]any{"path": "svc", "depth": 2, "owners": false})
	if isErr {
		t.Fatal("structure failed")
	}
	runID, _, err := fs.srv.currentRun(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	base1, err := fs.srv.loadStructureBase(t.Context(), runID, "low")
	if err != nil {
		t.Fatal(err)
	}
	base2, err := fs.srv.loadStructureBase(t.Context(), runID, "low")
	if err != nil {
		t.Fatal(err)
	}
	if base1 != base2 {
		t.Error("the base was read twice for one run and floor")
	}
	// the same folder again, after other folders, gives the same rows and lines
	_, _, _ = structureCall(t, fs, map[string]any{"path": "lib", "owners": false})
	_, _, _ = structureCall(t, fs, map[string]any{"owners": false})
	again, _, _ := structureCall(t, fs, map[string]any{"path": "svc", "depth": 2, "owners": false})
	if !reflect.DeepEqual(first.Folders, again.Folders) || !reflect.DeepEqual(first.Edges, again.Edges) {
		t.Errorf("a remembered base changed the answer:\nfirst %+v\nagain %+v", first, again)
	}
}

// One route listed twice with the same signature, file, line and handler is
// one route (g2 F11); the same route at another line is not folded.
func TestDedupeEndpointsFoldsExactRepeatsOnly(t *testing.T) {
	a := registry.Endpoint{Signature: "GET /a", FilePath: "x.py", Line: 3, Handler: "a"}
	b := registry.Endpoint{Signature: "GET /a", FilePath: "x.py", Line: 9, Handler: "a"}
	got, folded := dedupeEndpoints([]registry.Endpoint{a, b, a, a})
	if folded != 2 || len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("dedupe = %+v (folded %d), want [a b] and 2 folded", got, folded)
	}
}
