package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func typeSym(id int64, name, kind, file string, fileID int64) registry.SymbolIndexEntry {
	return registry.SymbolIndexEntry{
		ID: id, Name: name, FQN: file + ":" + name, Kind: kind,
		FileID: fileID, FilePath: file, Exported: true, Language: "rust",
	}
}

// `impl Trait for Type` is IMPLEMENTS from Type, wherever the impl sits. The
// source is the type: in the impl's file when it is declared there, else the
// repository's one type of that name, else the one in the same directory, else
// no edge, with the trait reference still recorded as resolved.
func TestImplementerIsTheEdgeSource(t *testing.T) {
	const trait, own = int64(1), int64(2)
	impl := func(typeName string) registry.Reference {
		return registry.Reference{
			ID: 1, Name: "Sink", Kind: "reference.implementation", Line: 10,
			Receiver: typeName, ReceiverKind: model.ReceiverImplementer,
		}
	}
	cases := []struct {
		name    string
		others  []registry.SymbolIndexEntry
		typ     string
		wantSrc int64 // 0: no edge
	}{
		{"declared in the same file", nil, "Mine", own},
		{"the one type of that name elsewhere", []registry.SymbolIndexEntry{typeSym(5, "Far", "class", "src/far.rs", 30)}, "Far", 5},
		{"two elsewhere, one beside the impl", []registry.SymbolIndexEntry{
			typeSym(5, "Dup", "class", "src/near.rs", 30),
			typeSym(6, "Dup", "class", "lib/far.rs", 31),
		}, "Dup", 5},
		{"two elsewhere, none beside", []registry.SymbolIndexEntry{
			typeSym(5, "Dup", "class", "a/x.rs", 30),
			typeSym(6, "Dup", "class", "b/y.rs", 31),
		}, "Dup", 0},
		{"not a type", []registry.SymbolIndexEntry{typeSym(5, "Free", "function", "src/f.rs", 30)}, "Free", 0},
		{"unknown", nil, "Nowhere", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := append([]registry.SymbolIndexEntry{
				typeSym(trait, "Sink", "interface", "src/sink.rs", 20),
				typeSym(own, "Mine", "class", "src/impls.rs", 10),
			}, tc.others...)
			r := New(index(entries...), Options{})
			edges, outs, _ := resolveOne(t, r, registry.FileRefs{
				FileID: 10, Path: "src/impls.rs", Language: "rust",
				Symbols: []registry.EnclosingSymbol{{ID: own, Name: "Mine", StartLine: 1, EndLine: 1}},
				Refs:    []registry.Reference{impl(tc.typ)},
			})
			if o := outs[1]; o.Resolution != registry.ResolutionResolved || o.SymbolID != trait {
				t.Fatalf("trait reference not resolved to the trait: %+v", o)
			}
			if tc.wantSrc == 0 {
				if len(edges) != 0 {
					t.Fatalf("want no edge, got %+v", edges)
				}
				return
			}
			if len(edges) != 1 || edges[0].Kind != model.EdgeImplements ||
				edges[0].SrcID != tc.wantSrc || edges[0].DstID != trait {
				t.Fatalf("want IMPLEMENTS %d -> %d, got %+v", tc.wantSrc, trait, edges)
			}
		})
	}
}
