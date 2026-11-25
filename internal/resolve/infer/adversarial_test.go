package infer

import (
	"fmt"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Adversarial receivers for tier 2 (D2-3 in the audit follow-up).
//
// Tier 2 reports high whenever exactly one candidate survives it, so the
// property that matters is narrow: a receiver whose type the program does not
// state must never become a HIGH edge to one of several same-named members.
// Each case below has two or more candidates owned by different types, and a
// receiver that names none of them in the way the language would: a duck-typed
// parameter, `self`/`this` used outside the owner, a generic type parameter,
// an import alias, a shadowing local spelled like a type. Binding is allowed
// only below high, or not at all.
func TestAdversarialReceiversNeverProduceHigh(t *testing.T) {
	type tc struct {
		lang, file, receiver string
		cands                []registry.SymbolIndexEntry
	}
	method := func(id int64, name, owner, file, sep string) registry.SymbolIndexEntry {
		return registry.SymbolIndexEntry{
			ID: id, Name: name, FQN: owner + sep + name, FilePath: file,
			Kind: "method", Exported: true,
		}
	}
	cases := []tc{
		// Python duck typing: `def settle(obj): obj.save()` names no type.
		{"python", "app/settle.py", "obj", []registry.SymbolIndexEntry{
			method(1, "save", "orders.models.Order", "orders/models.py", "."),
			method(2, "save", "payments.models.Payment", "payments/models.py", "."),
		}},
		// `self` in a module-level helper, not inside either owner.
		{"python", "app/helpers.py", "self", []registry.SymbolIndexEntry{
			method(1, "close", "db.Conn", "db/conn.py", "."),
			method(2, "close", "net.Socket", "net/socket.py", "."),
		}},
		// A Java generic parameter `T item; item.validate()`.
		{"java", "src/app/Pipeline.java", "T", []registry.SymbolIndexEntry{
			method(1, "validate", "com.app.Order", "src/app/Order.java", "."),
			method(2, "validate", "com.app.Invoice", "src/app/Invoice.java", "."),
		}},
		{"java", "src/app/Pipeline.java", "item", []registry.SymbolIndexEntry{
			method(1, "validate", "com.app.Order", "src/app/Order.java", "."),
			method(2, "validate", "com.app.Invoice", "src/app/Invoice.java", "."),
		}},
		// A TypeScript import alias: `import { Store as S }` then `s.get()`.
		{"typescript", "src/app/view.ts", "s", []registry.SymbolIndexEntry{
			method(1, "get", "src/cache/store.ts:Store", "src/cache/store.ts", "."),
			method(2, "get", "src/http/client.ts:Client", "src/http/client.ts", "."),
		}},
		// Go: an interface-typed local whose name matches neither concrete type.
		{"go", "internal/app/run.go", "w", []registry.SymbolIndexEntry{
			method(1, "Write", "internal/file/file.go:File", "internal/file/file.go", "."),
			method(2, "Write", "internal/net/conn.go:Conn", "internal/net/conn.go", "."),
		}},
		// A shadowing local spelled like neither owner but like a directory.
		{"go", "internal/app/run.go", "file", []registry.SymbolIndexEntry{
			method(1, "Write", "internal/file/writer.go:Writer", "internal/file/writer.go", "."),
			method(2, "Write", "internal/net/conn.go:Conn", "internal/net/conn.go", "."),
		}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s", c.lang, c.receiver), func(t *testing.T) {
			fr := registry.FileRefs{FileID: 99, Path: c.file, Language: c.lang}
			r := New(nil, Options{})
			spec := NewSpecs().For(fr.Language)
			got, ok := r.pick(spec, fr, buildImportSet(spec, nil),
				registry.Reference{Name: c.cands[0].Name, Receiver: c.receiver, Kind: "reference.call"}, c.cands)
			if ok && got.confidence == model.ConfidenceHigh {
				t.Errorf("receiver %q bound to %s at HIGH (%s); a receiver whose type the program "+
					"does not state must not become a high-confidence edge", c.receiver, got.entry.FQN, got.reason)
			}
		})
	}
}

// TestAdversarialHarnessCanBindHigh is the control: the same candidates with a
// receiver that does name an owner must still bind, so the test above holds
// because tier 2 refuses the adversarial receivers, not because it never binds.
func TestAdversarialHarnessCanBindHigh(t *testing.T) {
	cands := []registry.SymbolIndexEntry{
		{ID: 1, Name: "validate", FQN: "com.app.Order.validate", FilePath: "src/app/Order.java", Kind: "method", Exported: true},
		{ID: 2, Name: "validate", FQN: "com.app.Invoice.validate", FilePath: "src/app/Invoice.java", Kind: "method", Exported: true},
	}
	fr := registry.FileRefs{FileID: 99, Path: "src/app/Pipeline.java", Language: "java"}
	r := New(nil, Options{})
	spec := NewSpecs().For(fr.Language)
	got, ok := r.pick(spec, fr, buildImportSet(spec, nil),
		registry.Reference{Name: "validate", Receiver: "Order", Kind: "reference.call"}, cands)
	if !ok || got.entry.ID != 1 {
		t.Fatalf("receiver Order did not bind to com.app.Order.validate (ok=%v, got %+v)", ok, got.entry)
	}
}
