package infer

import (
	"context"
	"strconv"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// manyDefs builds n definitions of one name, each in its own file, the first
// being the one an import names. File ids are 100+i.
func manyDefs(name, kind string, n int, first string) []registry.SymbolIndexEntry {
	out := make([]registry.SymbolIndexEntry, 0, n)
	for i := 0; i < n; i++ {
		file := "extensions/e" + strconv.Itoa(i) + "/types.ts"
		if i == 0 {
			file = first
		}
		d := def(int64(1000+i), kind, file, name, int64(100+i))
		d.Exported = true
		out = append(out, d)
	}
	return out
}

// TestAnOverCapNameBindsToTheDefinitionTheImportNames is vscode's
// `class X implements IDisposable` in a file that begins
// `import { IDisposable } from '../../base/common/lifecycle.js'`. The name has
// eleven definitions, more than the cap allows, and the import names the file.
func TestAnOverCapNameBindsToTheDefinitionTheImportNames(t *testing.T) {
	lifecycle := "src/vs/base/common/lifecycle.ts"
	idx := map[string][]registry.SymbolIndexEntry{"IDisposable": manyDefs("IDisposable", "interface", 11, lifecycle)}
	file := "src/vs/workbench/services/x/x.ts"
	fr := registry.FileRefs{
		FileID: 1, Path: file, Language: "typescript",
		Imports: []registry.Import{
			{Path: "../../../base/common/lifecycle.js", Alias: ".IDisposable", Line: 1},
		},
		Symbols: within(90, file, "X"),
		Refs: []registry.Reference{
			{ID: 1, Name: "IDisposable", Kind: "reference.implementation", Line: 5},
			{ID: 2, Name: "IDisposable", Kind: "reference.implementation", Line: 6, Receiver: ""},
		},
	}
	r := New(idx, Options{})
	edges, outs, st := resolveOne(t, r, fr)
	if outs[1].Resolution != registry.ResolutionResolved || outs[1].SymbolID != 1000 {
		t.Fatalf("outcome = %+v, want resolved to the lifecycle.ts definition (1000)", outs[1])
	}
	if len(edges) == 0 || edges[0].Confidence != model.ConfidenceHigh || edges[0].Kind != model.EdgeImplements {
		t.Errorf("edges = %+v, want a high IMPLEMENTS edge", edges)
	}
	if st.CapGuided < 1 || st.CapExceeded < 1 {
		t.Errorf("CapExceeded=%d CapGuided=%d, want both counted", st.CapExceeded, st.CapGuided)
	}
	if st.Resolved+st.Ambiguous != st.References {
		t.Errorf("references %d != resolved %d + ambiguous %d: the first look was not undone", st.References, st.Resolved, st.Ambiguous)
	}
}

// TestAnOverCapNameWithNothingToGoOnStaysAmbiguous is the cap doing its job:
// no import, not the same file, not a neighbour. The guided look finds nothing
// and the reference is recorded exactly as before.
func TestAnOverCapNameWithNothingToGoOnStaysAmbiguous(t *testing.T) {
	idx := map[string][]registry.SymbolIndexEntry{"IDisposable": manyDefs("IDisposable", "interface", 11, "src/vs/base/common/lifecycle.ts")}
	file := "src/vs/workbench/services/x/x.ts"
	fr := registry.FileRefs{
		FileID: 1, Path: file, Language: "typescript",
		Symbols: within(90, file, "X"),
		Refs:    []registry.Reference{{ID: 1, Name: "IDisposable", Kind: "reference.implementation", Line: 5}},
	}
	edges, outs, st := resolveOne(t, New(idx, Options{}), fr)
	if outs[1].Resolution != registry.ResolutionAmbiguous || len(edges) != 0 {
		t.Fatalf("outcome %+v edges %d, want ambiguous and no edge", outs[1], len(edges))
	}
	if st.CapGuided != 0 || st.Ambiguous != 1 {
		t.Errorf("CapGuided=%d Ambiguous=%d, want 0 and 1", st.CapGuided, st.Ambiguous)
	}
}

func TestAnOverCapNameInSameFileIsRescued(t *testing.T) {
	file := "backend/python/auth/test_auth.py"
	defs := manyDefs("_seed", "function", 15, file)
	// Make defs[0] match the caller's FileID (100)
	idx := map[string][]registry.SymbolIndexEntry{"_seed": defs}
	fr := registry.FileRefs{
		FileID:   100,
		Path:     file,
		Language: "python",
		Symbols:  within(90, file, "test_login"),
		Refs: []registry.Reference{
			{ID: 1, Name: "_seed", Kind: "reference.call", Line: 5},
		},
	}
	edges, outs, _ := resolveOne(t, New(idx, Options{}), fr)
	if len(outs) == 0 || outs[1].Resolution != registry.ResolutionResolved || outs[1].SymbolID != 1000 {
		t.Fatalf("outcome = %+v, want resolved to same-file definition (1000)", outs[1])
	}
	if len(edges) == 0 || edges[0].Confidence != model.ConfidenceHigh {
		t.Fatalf("edges = %+v, want a high CALLS edge", edges)
	}
}

// TestAGuidedSetNeverClaimsTheNameIsUniqueInTheRepository: the neighbour
// defines the name once and the repository has ten more. The neighbour is
// evidence of its own and is reported as that, never as "unique in the
// repository", which a set chosen by the source cannot show.
func TestAGuidedSetNeverClaimsTheNameIsUniqueInTheRepository(t *testing.T) {
	idx := map[string][]registry.SymbolIndexEntry{"Thing": manyDefs("Thing", "class", 11, "src/vs/base/common/lifecycle.ts")}
	file := "src/vs/base/common/other.ts" // same directory as the first definition
	fr := registry.FileRefs{
		FileID: 1, Path: file, Language: "typescript",
		Symbols: within(90, file, "X"),
		Refs:    []registry.Reference{{ID: 1, Name: "Thing", Kind: "reference.extends", Line: 5}},
	}
	edges, _, _ := resolveOne(t, New(idx, Options{}), fr)
	for _, e := range edges {
		if claimsRepositoryUniqueness(e.Evidence.Detail) {
			t.Errorf("edge reason %q claims uniqueness over a guided set", e.Evidence.Detail)
		}
	}
}

// cutCandidates serves a name's first `limit` definitions, as the candidate
// cache does, while InFiles can still reach the rest.
type cutCandidates struct {
	mapCandidates
	limit int
}

func (c cutCandidates) PerNameLimit() int { return c.limit }

func (c cutCandidates) Candidates(ctx context.Context, names []string) (map[string][]registry.SymbolIndexEntry, error) {
	out, _ := c.mapCandidates.Candidates(ctx, names)
	for n, cs := range out {
		if len(cs) > c.limit {
			out[n] = cs[:c.limit]
		}
	}
	return out, nil
}

// TestADefinitionPastTheCandidateLimitIsStillFoundThroughTheImport: the name has
// more definitions than the candidate list holds, and the one the import names
// is not among the rows that were read. The guided look asks the index for the
// import's file instead of truncating the global list.
func TestADefinitionPastTheCandidateLimitIsStillFoundThroughTheImport(t *testing.T) {
	defs := manyDefs("IDisposable", "interface", 40, "")
	// The import's target is the LAST definition, well past a limit of 10.
	lifecycle := "src/vs/base/common/lifecycle.ts"
	last := def(1999, "interface", lifecycle, "IDisposable", 999)
	last.Exported = true
	all := map[string][]registry.SymbolIndexEntry{"IDisposable": append(defs[1:], last)}
	file := "src/vs/workbench/services/x/x.ts"
	fr := registry.FileRefs{
		FileID: 1, Path: file, Language: "typescript",
		Imports: []registry.Import{{Path: "../../../base/common/lifecycle.js", Alias: ".IDisposable", Line: 1}},
		Symbols: within(90, file, "X"),
		Refs:    []registry.Reference{{ID: 1, Name: "IDisposable", Kind: "reference.implementation", Line: 5}},
	}
	r := NewStreaming(cutCandidates{mapCandidates: all, limit: 10}, Options{})
	_, outs, _ := resolveOne(t, r, fr)
	if outs[1].Resolution != registry.ResolutionResolved || outs[1].SymbolID != 1999 {
		t.Fatalf("outcome = %+v, want resolved to the definition in lifecycle.ts (1999)", outs[1])
	}
}
