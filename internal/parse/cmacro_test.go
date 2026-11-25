package parse

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

func candidateNames(src, language string) []string {
	var out []string
	for _, c := range findMacroCandidates([]byte(src), language) {
		out = append(out, string([]byte(src)[c.start:c.end]))
	}
	return out
}

func TestMacroCandidateForms(t *testing.T) {
	cases := []struct {
		name string
		lang string
		src  string
		want []string
	}{
		{"export on a class", "cpp", "class LEVELDB_EXPORT Comparator {\n};", []string{"LEVELDB_EXPORT"}},
		{"export on a struct with a base", "cpp", "struct API_EXPORT Foo : Bar {};", []string{"API_EXPORT"}},
		{"export on a union", "c", "union PACKED_U Word { int a; };", []string{"PACKED_U"}},
		{"export on an enum class", "cpp", "enum class API_EXPORT Mode { A };", []string{"API_EXPORT"}},
		{"export before final", "cpp", "class API_EXPORT Foo final { };", []string{"API_EXPORT"}},
		{"export on a forward declaration", "cpp", "class API_EXPORT Foo;", []string{"API_EXPORT"}},
		{"export with arguments", "cpp", "class DECLSPEC(dllexport) Foo {};", []string{"DECLSPEC(dllexport)"}},
		{"two macros", "cpp", "class API_EXPORT API_FINAL Foo {};", []string{"API_EXPORT", "API_FINAL"}},
		{"macro after the name", "c", "struct node PACKED_ATTR {\n int a;\n};", []string{"PACKED_ATTR"}},
		{"a class named by an upper-case word is not touched", "cpp", "class API_EXPORT final {};", nil},
		{"function return macro", "c", "JK_STATIC_INLINE void jk_foo(int a) { }", []string{"JK_STATIC_INLINE"}},
		{"export on a declaration", "cpp", "EXPORT_API int f(void);", []string{"EXPORT_API"}},
		{"export in front of a user type", "cpp", "LEVELDB_EXPORT Status DestroyDB(const std::string& name);", []string{"LEVELDB_EXPORT"}},
		{"attribute macro with arguments after static", "c", "static REDIS_NO_SANITIZE(\"bounds\") int f(int a) { return a; }", []string{"REDIS_NO_SANITIZE(\"bounds\")"}},
		{"macro that is the return type", "c", "PY_LOCAL(int) f(int a) { return a; }", []string{"PY_LOCAL(int)"}},
		{"macro after a closing brace", "c", "int a(void) { return 1; }\nAPI_EXPORT int b(void) { return 2; }", []string{"API_EXPORT"}},
		{"macro in a class body after an access label", "cpp", "class A {\n public:\n  API_EXPORT virtual int f();\n};", []string{"API_EXPORT"}},
		{"macro alone on a line before a directive", "c", "G_BEGIN_DECLS\n#include <x.h>\nint f(void);", []string{"G_BEGIN_DECLS"}},
		{"macro alone on a line before an Objective-C declaration", "objc", "NS_ASSUME_NONNULL_BEGIN\n@interface Foo : NSObject\n@end", []string{"NS_ASSUME_NONNULL_BEGIN"}},

		{"annotation after the parameter list", "cpp", "void DBImpl::Compact() LOCKS_EXCLUDED(mutex_) {\n}", []string{"LOCKS_EXCLUDED(mutex_)"}},
		{"annotation after const", "cpp", "int Get() const EXCLUSIVE_LOCKS_REQUIRED(mu_) { return 1; }", []string{"EXCLUSIVE_LOCKS_REQUIRED(mu_)"}},
		{"annotation without arguments on a prototype", "c", "void die(int code) NORETURN;", []string{"NORETURN"}},
		{"a macro loop after a condition is not an annotation", "c", "void f(void) {\n  if (x) FOREACH(a, b) {\n  }\n  while (y) EACH(c) {\n  }\n}", nil},

		{"macros in the middle of the head", "c", "JE_EXPORT JE_ALLOCATOR\nvoid JE_NOTHROW *\nJE_ATTR(malloc) JE_SIZE(1)\nje_malloc(size_t size) {\n  return 0;\n}", []string{"JE_EXPORT", "JE_ALLOCATOR", "JE_NOTHROW", "JE_ATTR(malloc)", "JE_SIZE(1)"}},
		{"an upper-case type with a pointer before the name", "c", "static FILE *open_it(void) { return 0; }", nil},
		{"namespace macro alone on its line", "cpp", "FMT_BEGIN_NAMESPACE\nnamespace detail {\nint f();\n}\nFMT_END_NAMESPACE\n", []string{"FMT_BEGIN_NAMESPACE"}},

		// What is not a candidate.
		{"a pointer-returning function of a library type", "c", "FILE *open_it(void) { return 0; }", nil},
		{"a variable of a library type", "c", "DWORD x;", nil},
		{"a macro call statement", "c", "int main(void) {\n  DO_THING(x);\n  return 0;\n}", nil},
		{"a macro call statement at file level", "c", "DO_THING(x);\nint a;", nil},
		{"an upper-case enum value", "c", "enum { RED_VAL, GREEN_VAL };", nil},
		{"an assignment", "c", "int a;\nLIMIT = 3;", nil},
		{"a comment", "cpp", "// class API_EXPORT Foo {\n/* API_EXPORT int f(void); */\nint a;", nil},
		{"a string", "c", "const char *s = \"class API_EXPORT Foo {\";", nil},
		{"a #define body", "c", "#define DECL API_EXPORT int f(void);\nint a;", nil},
		{"a lower-case identifier", "c", "static inline int f(void) { return 1; }", nil},
		{"a language that has no mask", "go", "class API_EXPORT Foo {}", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := candidateNames(tc.src, tc.lang)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("candidates = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMaskKeepsEveryOffset(t *testing.T) {
	src := []byte("class LEVELDB_EXPORT Comparator {\n};\n\nstatic REDIS_NO_SANITIZE(\"a\",\n  \"b\") int f(int a) { return a; }\nJK_STATIC_INLINE void g(void) { }\n")
	cands := findMacroCandidates(src, "cpp")
	if len(cands) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(cands))
	}
	masked := maskCandidates(src, cands)
	if len(masked) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(masked))
	}
	if bytes.Count(masked, []byte{'\n'}) != bytes.Count(src, []byte{'\n'}) {
		t.Fatal("the number of lines changed")
	}
	for i := range src {
		if src[i] == '\n' && masked[i] != '\n' {
			t.Fatalf("newline at byte %d moved", i)
		}
	}
	for _, c := range cands {
		for i := c.start; i < c.end; i++ {
			if masked[i] != ' ' && masked[i] != '\n' {
				t.Fatalf("byte %d of the candidate %q is %q, not a space", i, c.name, masked[i])
			}
		}
	}
	// Outside every candidate nothing is touched.
	for i := range src {
		inside := false
		for _, c := range cands {
			if i >= c.start && i < c.end {
				inside = true
			}
		}
		if !inside && src[i] != masked[i] {
			t.Fatalf("byte %d changed outside a candidate", i)
		}
	}
	if !bytes.Equal(src[:5], []byte("class")) {
		t.Fatal("the input was modified")
	}
}

func cppParser(t *testing.T, language string) *genericParser {
	t.Helper()
	p, ok := NewRegistry().ForLanguage(language).(*genericParser)
	if !ok || p == nil {
		t.Fatalf("no generic parser for %s", language)
	}
	return p
}

// verdict parses src plain and masked and returns the guard's answer, and
// whether there were candidates at all.
func verdict(t *testing.T, language, src string) (better, hadCandidates bool) {
	t.Helper()
	p := cppParser(t, language)
	b := []byte(src)
	cands := findMacroCandidates(b, language)
	if len(cands) == 0 {
		return false, false
	}
	names := map[string]bool{}
	for _, c := range cands {
		names[c.name] = true
	}
	tree, _, err := p.parseOnce(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	tags, _, err := p.tagTree(tree, b)
	if err != nil {
		t.Fatal(err)
	}
	masked := maskCandidates(b, cands)
	tree2, _, err := p.parseOnce(context.Background(), masked)
	if err != nil {
		t.Fatal(err)
	}
	tags2, _, err := p.tagTree(tree2, masked)
	if err != nil {
		t.Fatal(err)
	}
	return maskIsBetter(tree.RootNode(), tags, tree2.RootNode(), tags2, names), true
}

func TestGuardAcceptsAMaskThatHelps(t *testing.T) {
	for _, src := range []string{
		"class LEVELDB_EXPORT Comparator {\n public:\n  virtual int Compare(int a, int b) const = 0;\n};\n",
		"struct API_EXPORT Foo : Bar {\n  int a;\n};\n",
		"void Foo::Compact() LOCKS_EXCLUDED(mu_) {\n}\nint Foo::Other() { return 1; }\n",
		"static REDIS_NO_SANITIZE(\"bounds\") int f(int a) { return a; }\n",
	} {
		better, had := verdict(t, "cpp", src)
		if !had || !better {
			t.Errorf("want a kept mask for %q, got had=%v better=%v", src, had, better)
		}
	}
}

// A wrong candidate must be harmless: the type is real, the parse was clean,
// and the guard says no.
func TestGuardRefusesAMaskThatDoesNotHelp(t *testing.T) {
	for _, src := range []string{
		"BOOL WINAPI Foo(void) { return 1; }\n",
		"EXPORT_TYPE Wrapper Make(int a) { Wrapper w; return w; }\n",
		"BOOL WINAPI Foo(void) { return 1; }\nint x = ;\n",
	} {
		better, had := verdict(t, "cpp", src)
		if had && better {
			t.Errorf("the guard kept a mask for %q", src)
		}
	}
}

func TestParseKeepsThePlainResultWhenTheMaskCannotHelp(t *testing.T) {
	p := cppParser(t, "cpp")
	for name, src := range map[string]string{
		"library return type":  "FILE *open_it(void) { return 0; }\nint other(void) { return 1; }\n",
		"calling convention":   "BOOL WINAPI Foo(void) { return 1; }\n",
		"macro call statement": "DO_THING(x);\nint a(void) { return 1; }\n",
		"enum values":          "enum { RED_VAL, GREEN_VAL };\nint a(void) { return RED_VAL; }\n",
	} {
		res, err := p.Parse(context.Background(), "x.cc", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if res.MacrosMasked != 0 {
			t.Errorf("%s: %d macros masked", name, res.MacrosMasked)
		}
		for _, s := range res.Symbols {
			switch s.Name {
			case "FILE", "BOOL", "WINAPI", "DO_THING", "RED_VAL":
				t.Errorf("%s: unexpected symbol %s", name, s.Name)
			}
		}
	}
}

func kindsByName(res *Result) map[string]model.EntityKind {
	out := map[string]model.EntityKind{}
	for _, s := range res.Symbols {
		out[s.Name] = s.Kind
	}
	return out
}

func TestParseReadsThroughAnExportMacro(t *testing.T) {
	src := `class LEVELDB_EXPORT Comparator {
 public:
  virtual ~Comparator();
  virtual int Compare(const Slice& a, const Slice& b) const = 0;
};

class LEVELDB_EXPORT Env : public Comparator {
 public:
  int Size() const { return 1; }
};

JK_STATIC_INLINE void jk_foo(int a) { }
`
	p := cppParser(t, "cpp")
	res, err := p.Parse(context.Background(), "include/db.h", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.MacrosMasked != 3 {
		t.Fatalf("MacrosMasked = %d, want 3", res.MacrosMasked)
	}
	names := kindsByName(res)
	for _, want := range []string{"Comparator", "Env", "jk_foo", "Size"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing %s; got %v", want, names)
		}
	}
	for _, bad := range []string{"LEVELDB_EXPORT", "JK_STATIC_INLINE"} {
		if _, ok := names[bad]; ok {
			t.Errorf("%s is a symbol", bad)
		}
	}
	var supers []string
	for _, r := range res.References {
		if strings.Contains(r.Kind, "extend") {
			supers = append(supers, r.Name)
		}
	}
	if len(supers) != 1 || supers[0] != "Comparator" {
		t.Errorf("supertype references = %v, want [Comparator]", supers)
	}
	if res.Status != StatusOK {
		t.Errorf("status = %s (%v), want ok", res.Status, res.Err)
	}
}

func TestMaskNoteIsInTheParseError(t *testing.T) {
	// A mask that leaves an error behind must say it was applied.
	src := "class LEVELDB_EXPORT Foo {\n};\nint g( {\n"
	res, err := cppParser(t, "cpp").Parse(context.Background(), "x.h", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.MacrosMasked == 0 {
		t.Skip("the mask was not kept for this input")
	}
	if res.Status != StatusPartial || !strings.Contains(res.Err.Error(), "masked") {
		t.Errorf("status %s, err %v: want a partial parse that mentions the mask", res.Status, res.Err)
	}
}

func TestCleanFileIsParsedOnceAndUntouched(t *testing.T) {
	src := "class Foo : public Bar {\n public:\n  int Get() const { return 1; }\n};\nint add(int a, int b) { return a + b; }\n"
	if got := findMacroCandidates([]byte(src), "cpp"); len(got) != 0 {
		t.Fatalf("candidates in a file with none: %v", got)
	}
	p := cppParser(t, "cpp")
	tree, stop, err := p.parseOnce(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	tags, _, _ := p.tagTree(tree, []byte(src))
	if mp := p.retryMasked(context.Background(), tree, stop, []byte(src), tags); mp != nil {
		t.Fatal("a clean file was retried")
	}
	res, err := p.Parse(context.Background(), "x.cc", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.MacrosMasked != 0 || res.Status != StatusOK {
		t.Fatalf("masked=%d status=%s", res.MacrosMasked, res.Status)
	}
}

// A candidate whose plain parse is clean is not retried either: the guard's
// first condition is trouble where the candidate sits.
func TestCandidateWithCleanParseIsNotRetried(t *testing.T) {
	src := "static int keep(int a) { return a; }\nJK_STATIC_INLINE void jk_foo(int a) { }\nJK_STATIC_INLINE int jk_bar(int a) { return a; }\n"
	p := cppParser(t, "cpp")
	tree, stop, err := p.parseOnce(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	tags, _, _ := p.tagTree(tree, []byte(src))
	if len(findMacroCandidates([]byte(src), "cpp")) == 0 {
		t.Fatal("expected a candidate")
	}
	if tree.RootNode().HasError() {
		t.Skip("the plain parse already shows an error")
	}
	if mp := p.retryMasked(context.Background(), tree, stop, []byte(src), tags); mp != nil {
		t.Fatal("a clean plain parse was retried")
	}
}

func TestMaskedParseHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := []byte("class LEVELDB_EXPORT Foo {\n};\n")
	p := cppParser(t, "cpp")
	if _, err := p.Parse(ctx, "x.h", src); err == nil {
		t.Fatal("a cancelled parse returned no error")
	}
}

func TestOutOfLineMethodsAreOwnedByTheirClass(t *testing.T) {
	src := `namespace ns {
void Table::Open(int x) { }
Table* Table::Make() { return nullptr; }
int outer::Table::Close() { return 0; }
char* name(int a) { return 0; }
}
`
	res, err := cppParser(t, "cpp").Parse(context.Background(), "t.cc", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		kind  model.EntityKind
		owned string
	}{
		"Open":  {model.KindMethod, ":Table.Open"},
		"Make":  {model.KindMethod, ":Table.Make"},
		"Close": {model.KindMethod, ":Table.Close"},
		"name":  {model.KindFunction, ""},
	}
	for _, s := range res.Symbols {
		w, ok := want[s.Name]
		if !ok {
			t.Errorf("unexpected symbol %s", s.Name)
			continue
		}
		if s.Kind != w.kind || !strings.HasSuffix(s.FQN, w.owned) {
			t.Errorf("%s: kind %s fqn %s, want kind %s fqn ending %s", s.Name, s.Kind, s.FQN, w.kind, w.owned)
		}
		delete(want, s.Name)
	}
	for n := range want {
		t.Errorf("missing %s", n)
	}
}

func TestForwardDeclarationsAndFunctionPointerTypedefs(t *testing.T) {
	src := `class Cache;
struct Foo;
typedef Iterator* (*BlockFunction)(void*, const ReadOptions&);
typedef int (*Plain)(int);
class Real { };
`
	res, err := cppParser(t, "cpp").Parse(context.Background(), "t.h", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	names := kindsByName(res)
	for _, bad := range []string{"Cache", "Foo", "Iterator"} {
		if _, ok := names[bad]; ok {
			t.Errorf("%s is a symbol", bad)
		}
	}
	for _, want := range []string{"BlockFunction", "Plain", "Real"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing %s; got %v", want, names)
		}
	}
	cres, err := cppParser(t, "c").Parse(context.Background(), "t.h", []byte("struct Foo;\ntypedef struct Foo Foo_t;\nstruct Bar { int a; };\n"))
	if err != nil {
		t.Fatal(err)
	}
	cn := kindsByName(cres)
	if _, ok := cn["Foo"]; ok {
		t.Error("C: a struct that is only named is a symbol")
	}
	if _, ok := cn["Bar"]; !ok {
		t.Error("C: struct Bar is missing")
	}
}
