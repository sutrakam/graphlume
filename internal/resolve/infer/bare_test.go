package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// objectMethod is a method written in an object literal: the index qualifies it
// by its file alone, because no type owns it.
func objectMethod(id int64, file, name string) registry.SymbolIndexEntry {
	e := def(id, "method", file, name, 10)
	e.Language = "javascript"
	return e
}

func bareCall(t *testing.T, idx map[string][]registry.SymbolIndexEntry, name, lang, file string, imports ...registry.Import) (edges int, out registry.RefOutcome) {
	t.Helper()
	r := New(idx, Options{Layout: NewLayout([]string{file}, []string{"vary"})})
	es, outs, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: file, Language: lang, Imports: imports,
		Symbols: within(99, file, "wrapper"),
		Refs:    []registry.Reference{callRef(1, name, model.ReceiverBare, "", 5)},
	})
	return len(es), outs[1]
}

// TestBareCallCannotReachAnObjectLiteralMember: in a language with no implicit
// `this`, `vary(...)` is a name in scope, and a method of an object literal is
// not one. koa bound it, at high, 5 times in 30.
func TestBareCallCannotReachAnObjectLiteralMember(t *testing.T) {
	idx := index(objectMethod(1, "lib/response.js", "vary"))
	n, out := bareCall(t, idx, "vary", "javascript", "lib/response.js")
	if n != 0 || out.Resolution == registry.ResolutionResolved {
		t.Errorf("a bare call was bound to an object literal's method: %d edges, %+v", n, out)
	}

	// The same name written as a function is a name in scope.
	idx = index(def(2, "function", "lib/response.js", "vary", 10))
	if n, _ := bareCall(t, idx, "vary", "javascript", "lib/response.js"); n != 1 {
		t.Errorf("a function declared in the file was not bound: %d edges", n)
	}
}

// TestBareCallToAnImportedNameIsExternalWhateverTheFileHolds is the other half
// of the same defect: the name is bound by `const vary = require('vary')`, so
// the member of the same name is not what it denotes.
func TestBareCallToAnImportedNameIsExternalWhateverTheFileHolds(t *testing.T) {
	idx := index(objectMethod(1, "lib/response.js", "vary"))
	n, out := bareCall(t, idx, "vary", "javascript", "lib/response.js",
		registry.Import{Path: "vary", Alias: "vary"})
	if n != 0 {
		t.Errorf("a call to an imported package bound to the file's own member: %d edges", n)
	}
	if !out.External {
		t.Errorf("the call leaves the repository and is not recorded as external: %+v", out)
	}
}

// TestBareConstructionOfAPlatformTypeIsExternal: `new URL(...)` in a file that
// declares and imports no URL is the language's.
func TestBareConstructionOfAPlatformTypeIsExternal(t *testing.T) {
	idx := index(objectMethod(1, "lib/request.js", "URL"))
	n, out := bareCall(t, idx, "URL", "javascript", "lib/response.js")
	if n != 0 || !out.External {
		t.Errorf("new URL(...) bound to another file's getter or not recorded as external: %d edges, %+v", n, out)
	}
}
