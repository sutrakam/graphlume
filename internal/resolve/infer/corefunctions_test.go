package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// TestCSharpAndCppConstructionBindsToTheType: `new X(...)` and a C++ declaration
// that constructs a variable are reference.call.new, and bind to the type.
func TestCSharpAndCppConstructionBindsToTheType(t *testing.T) {
	for _, tc := range []struct{ lang, file, user string }{
		{"c_sharp", "src/Widget.cs", "src/Use.cs"},
		{"cpp", "src/widget.cc", "src/use.cc"},
	} {
		cls := def(1, "class", tc.file, "Widget", 10)
		ctor := def(2, "method", tc.file, "Widget.Widget", 10)
		r := New(index(cls, ctor), Options{})
		edges, _, _ := resolveOne(t, r, registry.FileRefs{
			FileID: 20, Path: tc.user, Language: tc.lang, Symbols: within(90, tc.user, "Use.run"),
			Refs: []registry.Reference{{ID: 1, Name: "Widget", Kind: "reference.call.new", ReceiverKind: model.ReceiverBare, Line: 5}},
		})
		if len(edges) != 1 || edges[0].DstID != 1 || edges[0].Kind != model.EdgeInstantiates {
			t.Errorf("%s: construction bound as %+v, want INSTANTIATES to the type", tc.lang, edges)
		}
	}
}

func TestCoreFunctionsAndSyntax(t *testing.T) {
	for _, tc := range []struct {
		lang, name string
		fn, syntax bool
	}{
		{"kotlin", "require", true, false},
		{"kotlin", "Pair", true, false},
		{"kotlin", "reposition", false, false},
		{"ruby", "include", true, false},
		{"ruby", "extend", true, false},
		{"ruby", "app", false, false},
		{"c", "fgets", true, false},
		{"cpp", "fgets", true, false},
		{"c", "clearerr", true, false},
		{"c", "feof", true, false},
		{"c", "fflush", true, false},
		{"c", "fopen", true, false},
		{"c", "freopen", true, false},
		{"c", "fseek", true, false},
		{"c", "ftell", true, false},
		{"c", "rewind", true, false},
		{"c", "fputs", true, false},
		{"c", "fputc", true, false},
		{"c", "fgetc", true, false},
		{"c", "fprintf", true, false},
		{"c", "fscanf", true, false},
		{"c", "fwrite", true, false},
		{"c", "getc", true, false},
		{"c", "putc", true, false},
		{"c", "ungetc", true, false},
		{"c", "setvbuf", true, false},
		{"c", "jv_free", false, false},
		{"rust", "format", false, true},
		{"rust", "vec", false, true},
		{"rust", "println", false, true},
		{"rust", "write", true, false}, // a macro, and a plausible function: the rules that go by the name alone refuse it
		{"rust", "errored", false, false},
		{"javascript", "URL", true, false},
		{"typescript", "Response", true, false},
		{"go", "format", false, false},
	} {
		spec := LanguageSpec{Name: tc.lang}
		if got := spec.isCoreFunction(tc.name); got != tc.fn {
			t.Errorf("%s: isCoreFunction(%q) = %v, want %v", tc.lang, tc.name, got, tc.fn)
		}
		if got := spec.isCoreSyntax(tc.name); got != tc.syntax {
			t.Errorf("%s: isCoreSyntax(%q) = %v, want %v", tc.lang, tc.name, got, tc.syntax)
		}
	}
}

func TestCoreMembersOfThePythonRubyAndScriptLibraries(t *testing.T) {
	for _, tc := range []struct {
		lang, name string
		want       bool
	}{
		{"python", "close", true},
		{"python", "start", true},
		{"python", "isatty", true},
		{"python", "assertEqual", true},
		{"python", "assert_called_once_with", true},
		{"python", "invoke", false},
		{"ruby", "include", true},
		{"ruby", "bytesize", true},
		{"ruby", "assert_equal", true},
		{"ruby", "must_equal", true},
		{"ruby", "media_type", false},
		{"typescript", "clone", true},
		{"javascript", "clone", true},
		{"typescript", "header", false},
	} {
		spec := LanguageSpec{Name: tc.lang}
		if got := spec.isCoreMember(tc.name); got != tc.want {
			t.Errorf("%s: isCoreMember(%q) = %v, want %v", tc.lang, tc.name, got, tc.want)
		}
	}
}

func bareCallIn(t *testing.T, lang, file string, idx map[string][]registry.SymbolIndexEntry, symbols []registry.EnclosingSymbol, name string) (edges []modelEdge, out registry.RefOutcome) {
	t.Helper()
	r := New(idx, Options{})
	es, outs, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: file, Language: lang, Symbols: symbols,
		Refs: []registry.Reference{callRef(1, name, model.ReceiverBare, "", 5)},
	})
	for _, e := range es {
		edges = append(edges, modelEdge{e.DstID, e.Confidence})
	}
	return edges, outs[1]
}

type modelEdge struct {
	dst        int64
	confidence model.Confidence
}

// TestRustMacroNeverBindsToAFunctionOfTheSameName: the parser records
// `format!(x)` as a bare call, and a function named `format` in the same file
// is not what it reaches.
func TestRustMacroNeverBindsToAFunctionOfTheSameName(t *testing.T) {
	idx := index(def(1, "function", "tests/tests.rs", "format", 10))
	edges, out := bareCallIn(t, "rust", "tests/tests.rs", idx, within(99, "tests/tests.rs", "run"), "format")
	if len(edges) != 0 || !out.External {
		t.Errorf("format! bound to a function: %+v %+v", edges, out)
	}
	// A name that is not a standard macro binds as before.
	idx = index(def(1, "function", "tests/tests.rs", "render", 10))
	edges, _ = bareCallIn(t, "rust", "tests/tests.rs", idx, within(99, "tests/tests.rs", "run"), "render")
	if len(edges) != 1 {
		t.Errorf("a function of the same file did not bind: %+v", edges)
	}
}

// TestMacroInvocationBindsOnlyToAMacroDefinition is the rule the parser's
// `reference.call.macro` will drive.
func TestMacroInvocationBindsOnlyToAMacroDefinition(t *testing.T) {
	macro := def(1, "macro", "src/lib.rs", "print_error", 10)
	fn := def(2, "function", "src/util.rs", "print_error", 11)
	r := New(index(macro, fn), Options{})
	fr := registry.FileRefs{
		FileID: 12, Path: "src/exec.rs", Language: "rust",
		Symbols: within(99, "src/exec.rs", "run"),
		Refs: []registry.Reference{
			{ID: 1, Name: "print_error", Kind: "reference.call.macro", ReceiverKind: model.ReceiverBare, Line: 5},
			{ID: 2, Name: "print_error", Kind: "reference.call", ReceiverKind: model.ReceiverBare, Line: 6},
			{ID: 3, Name: "format", Kind: "reference.call.macro", ReceiverKind: model.ReceiverBare, Line: 7},
		},
	}
	edges, outs, _ := resolveOne(t, r, fr)
	got := edgesByLine(edges)
	if got[5].dst != 1 || got[5].confidence != model.ConfidenceMedium {
		t.Errorf("the invocation bound as %+v, want the macro (1) at medium", got[5])
	}
	if got[6].dst == 1 {
		t.Errorf("a function call was bound to a macro: %+v", got[6])
	}
	if _, bound := got[7]; bound || !outs[3].External {
		t.Errorf("std's format! bound to something or is not external: %+v %+v", got[7], outs[3])
	}
}
