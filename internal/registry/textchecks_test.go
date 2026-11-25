package registry

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// ---------------------------------------------------------------- languages

func TestReferencingLanguagesAreTheResolversFamiliesAndNeverProseOrData(t *testing.T) {
	got := ReferencingLanguages([]string{"typescript"})
	for _, want := range []string{"typescript", "tsx", "javascript", "jsx", "vue"} {
		if !containsLang(got, want) {
			t.Errorf("a TypeScript definition is not referenced from %s files: %v", want, got)
		}
	}
	for _, bad := range []string{"markdown", "yaml", "json", "toml", "python", "java"} {
		if containsLang(got, bad) {
			t.Errorf("a TypeScript definition can be referenced from %s files: %v", bad, got)
		}
	}
	if got := ReferencingLanguages([]string{"go"}); !reflect.DeepEqual(got, []string{"go"}) {
		t.Errorf("a language in no family reaches only itself: %v", got)
	}
	// A Markdown heading is a symbol; a definition there references nothing.
	got = ReferencingLanguages([]string{"markdown", "go"})
	if containsLang(got, "markdown") {
		t.Errorf("a definition in prose made prose a referencing language: %v", got)
	}
	// No definition at all: code, never prose or data.
	got = ReferencingLanguages(nil)
	if containsLang(got, "markdown") || containsLang(got, "yaml") || !containsLang(got, "go") {
		t.Errorf("a name with no definition is searched in %v", got)
	}
}

// ---------------------------------------------------------------- imports

func TestImportLineSetCoversEveryLineOfAStatement(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []int
	}{
		{"js multi-line import list", "import {\n  mergePath,\n  other,\n} from './url'\nmergePath('/')\n", []int{1, 2, 3, 4}},
		{"js single import", "import { mergePath } from './url'\nmergePath()\n", []int{1}},
		{"rust use list", "use crate::a::{\n    b,\n    mergePath,\n};\nfn f() {}\n", []int{1, 2, 3, 4}},
		{"rust pub use", "pub use self::x::mergePath;\n", []int{1}},
		{"python parenthesised from-import", "from x import (\n    a,\n    mergePath,\n)\nmergePath()\n", []int{1, 2, 3, 4}},
		{"commonjs require", "const mergePath = require('./url')\nmergePath()\n", []int{1}},
		{"commonjs destructuring", "const {\n  mergePath,\n} = require('./url')\n", []int{1, 2, 3}},
		{"re-export from", "export {\n  mergePath,\n} from './url'\n", []int{1, 2, 3}},
		{"a local export is a use", "export { mergePath }\n", nil},
		{"a plain const is a use", "const handler = mergePath\n", nil},
		{"include", "#include \"mergePath.h\"\nint x;\n", []int{1}},
		{"sql from is not python's", "from users select mergePath\n", nil},
		{"strings do not unbalance", "import { a } from 'x{'\nfoo(mergePath)\n", []int{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []int
			set := importLineSet(tc.text)
			for n := 1; n <= strings.Count(tc.text, "\n")+1; n++ {
				if set[n] {
					got = append(got, n)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("import lines = %v, want %v\n%s", got, tc.want, tc.text)
			}
		})
	}
}

// ---------------------------------------------------------------- declarations of the function itself

func TestDeclaresFunctionRecognisesPrototypesAndLeavesUsesAlone(t *testing.T) {
	for _, tc := range []struct {
		line   string
		lang   string
		paired bool
		want   bool
	}{
		{"void jv_parser_free(jv_parser*);", "c", false, true},
		{"static inline void jv_parser_free(struct jv_parser *p);", "c", false, true},
		{"extern \"C\" void jv_parser_free(jv_parser*);", "cpp", false, true},
		{"    jv_parser_free(parser);", "c", false, false},
		{"    x = jv_parser_free(parser);", "c", false, false},
		{"    return jv_parser_free(parser);", "c", false, false},
		{"    if (ok) jv_parser_free(parser);", "c", false, false},
		{"    obj.jv_parser_free(parser);", "c", false, false},
		{"    register_cb(jv_parser_free);", "c", false, false},
		{"    cb = &jv_parser_free;", "c", false, false},
		{"JV_API void jv_parser_free(jv_parser*);", "c", false, true},
		// a macro-decorated prototype that the plain reading cannot see, inside
		// the header that pairs with the source defining the function
		{"JV_ATTR(noreturn) void jv_parser_free(jv_parser*);", "c", true, true},
		{"    jv_parser_free(parser);", "c", true, false},
		{"def jv_parser_free(self):", "python", false, true},
		{"async def jv_parser_free(self):", "python", false, true},
		{"export function jv_parser_free(a: string): void;", "typescript", false, true},
		{"pub fn jv_parser_free(&self);", "rust", false, true},
		{"func (s *Server) jv_parser_free() {", "go", false, true},
		{"  def self.jv_parser_free", "ruby", false, true},
		{"  jv_parser_free(x)", "python", false, false},
		{"  handlers = [jv_parser_free, other]", "python", false, false},
		{"  public abstract void jv_parser_free();", "java", false, true},
		{"  foo(jv_parser_free(a), jv_parser_free)", "java", false, false},
	} {
		if got := declaresFunction(tc.line, "jv_parser_free", tc.lang, tc.paired); got != tc.want {
			t.Errorf("declaresFunction(%q, %s, paired=%v) = %v, want %v", tc.line, tc.lang, tc.paired, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------- against the database

func seedTextFiles(t *testing.T, files []fileSpec) (*Registry, int64) {
	t.Helper()
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	writeContent(t, r, run.ID, files, nil)
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	return r, run.ID
}

func defSym(name string, line int) symSpec {
	return symSpec{name: name, fqn: name, kind: model.KindFunction, line: line}
}

// TestUnrecordedMentionsReadOnlySourceOfACompatibleLanguage: a Markdown line, a
// workflow file's word and a TOML key are not uses of a Go function, and the
// real use in Go still is.
func TestUnrecordedMentionsReadOnlySourceOfACompatibleLanguage(t *testing.T) {
	r, run := seedTextFiles(t, []fileSpec{
		{path: "completions.go", lang: "go", hash: "a1", content: "package cobra\nfunc run() {}\nvar table = map[string]func(){\"x\": run}\n",
			syms: []symSpec{defSym("run", 2)}},
		{path: ".github/workflows/ci.yml", lang: "yaml", hash: "a2", content: "steps:\n  - run: make test\n  - name: run\n"},
		{path: "README.md", lang: "markdown", hash: "a3", content: "Call run(x) to start.\nrun\n"},
		{path: "site/_index.md", lang: "markdown", hash: "a4", content: "```go\nfmt.Println(run)\n```\n"},
		{path: "pyproject.toml", lang: "toml", hash: "a5", content: "[tool]\nrun = 'x'\n"},
		{path: "scripts/run.py", lang: "python", hash: "a6", content: "def go():\n    pass\nhandlers = [run]\n"},
	})
	got, err := r.UnrecordedMentions(context.Background(), run, "run", []DefSpan{{Path: "completions.go", From: 2, To: 2}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Place{{"completions.go", 3}}
	if !got.Known || got.Total != 1 || !reflect.DeepEqual(got.Sample, want) {
		t.Errorf("UnrecordedMentions = %+v, want only %v", got, want)
	}
}

func TestUnrecordedMentionsKeepsTheRealValueUses(t *testing.T) {
	r, run := seedTextFiles(t, []fileSpec{
		{path: "lib/app.js", lang: "javascript", hash: "b1", content: strings.Join([]string{
			"function respond(ctx) {}",
			"const handleResponse = () => respond(ctx)",
			"fn.call(ctx).then(handleResponse)",
		}, "\n"), syms: []symSpec{defSym("handleResponse", 2)}},
		{path: "lib/app.test.ts", lang: "typescript", hash: "b2", content: "it('x', () => p.then(handleResponse))\n"},
		{path: "docs/guide.md", lang: "markdown", hash: "b3", content: "Pass handleResponse to then.\n"},
	})
	got, err := r.UnrecordedMentions(context.Background(), run, "handleResponse", []DefSpan{{Path: "lib/app.js", From: 2, To: 2}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Place{{"lib/app.js", 3}, {"lib/app.test.ts", 1}}
	if got.Total != 2 || !reflect.DeepEqual(got.Sample, want) {
		t.Errorf("UnrecordedMentions = %+v, want %v", got, want)
	}
}

func TestUnrecordedMentionsIgnoresImportListsAndPrototypes(t *testing.T) {
	r, run := seedTextFiles(t, []fileSpec{
		{path: "src/url.ts", lang: "typescript", hash: "c1", content: "export const mergePath = (a: string) => a\n",
			syms: []symSpec{defSym("mergePath", 1)}},
		{path: "src/client.ts", lang: "typescript", hash: "c2", content: strings.Join([]string{
			"import {", "  mergePath,", "  other,", "} from './url'", "export const x = 1"}, "\n")},
		{path: "src/hono.ts", lang: "typescript", hash: "c3", content: "import { getPath, mergePath } from './url'\n"},
		{path: "src/lib.rs", lang: "rust", hash: "c4", content: "use crate::a::{\n    mergePath,\n};\n"},
	})
	got, err := r.UnrecordedMentions(context.Background(), run, "mergePath", []DefSpan{{Path: "src/url.ts", From: 1, To: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 {
		t.Errorf("import lists counted as uses: %+v", got)
	}

	r, run = seedTextFiles(t, []fileSpec{
		{path: "src/jv_parse.c", lang: "c", hash: "d1", content: "#include \"jv.h\"\nvoid jv_parser_free(struct jv_parser* p) {\n  free(p);\n}\n",
			syms: []symSpec{defSym("jv_parser_free", 2)}},
		{path: "src/jv.h", lang: "c", hash: "d2", content: "jv jv_parse(const char*);\nvoid jv_parser_free(jv_parser*);\n"},
		{path: "src/util.c", lang: "c", hash: "d3", content: "void f() {\n  jv_parser_free(p);\n  register_cb(jv_parser_free);\n}\n"},
	})
	got, err = r.UnrecordedMentions(context.Background(), run, "jv_parser_free", []DefSpan{{Path: "src/jv_parse.c", From: 2, To: 4}})
	if err != nil {
		t.Fatal(err)
	}
	// The call on line 2 of util.c is a call (the index would record it, and
	// this fixture records nothing), and the prototype is not a use.
	want := []Place{{"src/util.c", 2}, {"src/util.c", 3}}
	if got.Total != 2 || !reflect.DeepEqual(got.Sample, want) {
		t.Errorf("a prototype counted as a use: %+v, want %v", got, want)
	}
}

func TestUnrecordedMentionsIgnoresJsxClosingTags(t *testing.T) {
	r, run := seedTextFiles(t, []fileSpec{
		{path: "components/main-nav.tsx", lang: "tsx", hash: "jsx1", content: "export function MainNav({ items, children }: any) {\n  return <div>{children}</div>\n}\n",
			syms: []symSpec{defSym("MainNav", 1)}},
		{path: "app/layout.tsx", lang: "tsx", hash: "jsx2", content: "import { MainNav } from '@/components/main-nav'\nexport default function Layout() {\n  return (\n    <MainNav items={[]}>\n      <span>hello</span>\n    </MainNav>\n  )\n}\n"},
	})
	got, err := r.UnrecordedMentions(context.Background(), run, "MainNav", []DefSpan{{Path: "components/main-nav.tsx", From: 1, To: 3}})
	if err != nil {
		t.Fatal(err)
	}
	// The JSX opening tag on line 4 is a call; line 6 is a closing tag </MainNav> and must NOT be counted as a value use.
	for _, p := range got.Sample {
		if p.Path == "app/layout.tsx" && p.Line == 6 {
			t.Errorf("JSX closing tag </MainNav> counted as a value use: %+v", got)
		}
	}
}
