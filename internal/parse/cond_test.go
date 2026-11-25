package parse

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func kindWords(dirs []condDirective) string {
	var out []string
	for _, d := range dirs {
		out = append(out, [...]string{"if", "elif", "else", "endif"}[d.kind])
	}
	return strings.Join(out, " ")
}

func TestCondDirectiveForms(t *testing.T) {
	cases := []struct {
		name   string
		csharp bool
		src    string
		want   string
	}{
		{"all four", false, "#if A\n#elif B\n#else\n#endif\n", "if elif else endif"},
		{"ifdef and ifndef", false, "#ifdef A\n#endif\n#ifndef B\n#endif\n", "if endif if endif"},
		{"space after the hash and indentation", false, "  #  if A\n\t# endif\n", "if endif"},
		{"c23 elifdef", false, "#ifdef A\n#elifdef B\n#elifndef C\n#endif\n", "if elif elif endif"},
		{"not a conditional directive", false, "#define X 1\n#include <a.h>\n#pragma once\n#error no\n", ""},
		{"in a line comment", false, "// #if A\n#endif\n", "endif"},
		{"in a block comment", false, "/*\n#if A\n*/\n#endif\n", "endif"},
		{"in a string", false, "const char *s = \"\\n#if A\";\n#endif\n", "endif"},
		{"in a raw string", false, "auto s = R\"x(\n#if A\n)x\";\n#endif\n", "endif"},
		{"after code on the line is not a directive", false, "int a; #if A\n#endif\n", "endif"},
		{"a define does not start a literal", false, "#define Q \"unterminated\n#if A\n#endif\n", "if endif"},
		{"an apostrophe in an error line", false, "#error don't\n#if A\n#endif\n", "if endif"},
		{"continuation lines belong to the directive", false, "#if A && \\\n    B\n#endif\n", "if endif"},
		{"digit separator", false, "int a = 1'000;\n#if A\n#endif\n", "if endif"},
		{"c# keeps if elif else endif", true, "#if A\n#elif B\n#else\n#endif\n", "if elif else endif"},
		{"c# has no ifdef", true, "#ifdef A\n#endif\n", "endif"},
		{"c# region is ignored", true, "#region Name\n#if A\n#endif\n#endregion\n", "if endif"},
		{"c# verbatim string", true, "var s = @\"\n#if A\n\";\n#endif\n", "endif"},
		{"c# raw string", true, "var s = \"\"\"\n#if A\n\"\"\";\n#endif\n", "endif"},
		{"c# trailing comment", true, "#if A // note\n#endif\n", "if endif"},
		{"c# region with an apostrophe", true, "#region Don't\n#if A\n#endif\n#endregion\n", "if endif"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kindWords(scanCondDirectives([]byte(tc.src), tc.csharp)); got != tc.want {
				t.Fatalf("directives = %q, want %q", got, tc.want)
			}
		})
	}
}

func groupsOf(src string, csharp bool) []condGroup {
	return buildCondGroups(scanCondDirectives([]byte(src), csharp))
}

func TestCondGroupsNest(t *testing.T) {
	src := "#if A\nint a;\n#ifdef B\nint b;\n#else\nint c;\n#endif\n#elif C\nint d;\n#else\nint e;\n#endif\n#if D\n#endif\n"
	g := groupsOf(src, false)
	if len(g) != 3 {
		t.Fatalf("groups = %d, want 3", len(g))
	}
	if g[0].parent != -1 || g[1].parent != 0 || g[2].parent != -1 {
		t.Errorf("parents = %d %d %d, want -1 0 -1", g[0].parent, g[1].parent, g[2].parent)
	}
	if len(g[0].branches) != 3 || len(g[1].branches) != 2 || len(g[2].branches) != 1 {
		t.Errorf("branches = %d %d %d, want 3 2 1", len(g[0].branches), len(g[1].branches), len(g[2].branches))
	}
	if body := src[g[1].branches[1].bodyStart:g[1].branches[1].bodyEnd]; !strings.Contains(body, "int c;") || strings.Contains(body, "int b;") {
		t.Errorf("the #else branch of the inner group holds %q", body)
	}
}

func TestCondStrayAndUnclosedDirectivesAreLeftAlone(t *testing.T) {
	for name, src := range map[string]string{
		"stray endif":     "#endif\nint a;\n",
		"stray else":      "#else\nint a;\n",
		"unclosed":        "#if A\nint a;\n",
		"second else":     "#if A\n#else\n#else\n#endif\n",
		"unclosed nested": "#if A\n#if B\n#endif\n",
	} {
		g := groupsOf(src, false)
		if name == "unclosed nested" {
			if len(g) != 1 || g[0].parent != -1 {
				t.Errorf("%s: groups = %d, want the inner one alone", name, len(g))
			}
			continue
		}
		if len(g) != 0 {
			t.Errorf("%s: %d groups, want none", name, len(g))
		}
	}
}

func TestCondBranchChoice(t *testing.T) {
	cases := []struct {
		name   string
		csharp bool
		src    string
		keep   []int
	}{
		{"the first branch", false, "#if A\nx\n#else\ny\n#endif\n", []int{0}},
		{"#if 0 keeps the else", false, "#if 0\nx\n#else\ny\n#endif\n", []int{1}},
		{"#if 0 keeps the first elif", false, "#if 0\nx\n#elif B\ny\n#else\nz\n#endif\n", []int{1}},
		{"#if (0) with a comment", false, "#if (0) /* off */\nx\n#else\ny\n#endif\n", []int{1}},
		{"#if 0 alone keeps nothing", false, "#if 0\nx\n#endif\n", []int{-1}},
		{"#if 1 keeps the first", false, "#if 1\nx\n#else\ny\n#endif\n", []int{0}},
		{"c# false", true, "#if false\nx\n#else\ny\n#endif\n", []int{1}},
		{"extern C guard keeps its branch", false, "#ifdef __cplusplus\nextern \"C\" {\n#endif\n#ifdef __cplusplus\n}\n#endif\n", []int{0, 0}},
		{"a header guard keeps its only branch", false, "#ifndef X_H\n#define X_H\nint a;\n#endif\n", []int{0}},
		// Two groups that test one macro are read in one world.
		{"ifdef then ifndef of the same macro", false,
			"#ifdef NO_EXC\na;\n#else\ntry {\n#endif\n#ifndef NO_EXC\n} catch (...) {}\n#endif\n", []int{0, -1}},
		{"ifndef then ifdef of the same macro", false,
			"#ifndef NO_EXC\ntry {\n#endif\n#ifdef NO_EXC\nx;\n#else\n} catch (...) {}\n#endif\n", []int{0, 1}},
		{"defined() forms agree with ifdef", false,
			"#if defined(A)\nx;\n#endif\n#if !defined(A)\ny;\n#else\nz;\n#endif\n", []int{0, 1}},
		{"an unrelated macro is not constrained", false,
			"#ifdef A\nx;\n#endif\n#ifdef B\ny;\n#endif\n", []int{0, 0}},
		{"a complex condition is not a test", false,
			"#if A > 1\nx;\n#else\ny;\n#endif\n", []int{0}},
		{"a group in a branch that was passed over sets nothing", false,
			"#if 0\n#ifdef Q\nx;\n#endif\n#endif\n#ifndef Q\ny;\n#endif\n", []int{-1, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := groupsOf(tc.src, tc.csharp)
			var got []int
			for _, grp := range g {
				got = append(got, grp.keep)
			}
			if len(got) != len(tc.keep) {
				t.Fatalf("keep = %v, want %v", got, tc.keep)
			}
			for i := range got {
				if got[i] != tc.keep[i] {
					t.Fatalf("keep = %v, want %v", got, tc.keep)
				}
			}
		})
	}
}

// planAll selects every group, which is what a file-wide error does.
func planAll(src []byte, csharp bool) *condPlan {
	groups := buildCondGroups(scanCondDirectives(src, csharp))
	plan := &condPlan{groups: groups, selected: make([]bool, len(groups))}
	for i := range plan.selected {
		plan.selected[i] = true
	}
	plan.computeBlanked(src)
	return plan
}

func TestCondMaskKeepsEveryOffset(t *testing.T) {
	for name, tc := range map[string]struct {
		csharp bool
		src    string
	}{
		"c":    {false, "int f(int a,\n#ifdef X\n  int b)\n#else\n  int b, int c)\n#endif\n{\n  return a;\n}\n#if 0\nint dead;\n#endif\n"},
		"crlf": {false, "#if A\r\nint a;\r\n#else\r\nint b;\r\n#endif\r\n"},
		"cs":   {true, "interface I\n{\n#if X\n    int F() => 1;\n#else\n    int F();\n#endif\n}\n"},
		"nest": {false, "#if A\n#if B\nint a;\n#else\nint b;\n#endif\n#else\nint c;\n#endif\n"},
	} {
		t.Run(name, func(t *testing.T) {
			src := []byte(tc.src)
			plan := planAll(src, tc.csharp)
			masked := plan.apply(src)
			if len(masked) != len(src) {
				t.Fatalf("length changed: %d -> %d", len(src), len(masked))
			}
			for i := range src {
				if (src[i] == '\n') != (masked[i] == '\n') || (src[i] == '\r') != (masked[i] == '\r') {
					t.Fatalf("line break at byte %d moved", i)
				}
				if src[i] != masked[i] && masked[i] != ' ' {
					t.Fatalf("byte %d became %q, not a space", i, masked[i])
				}
			}
			if !bytes.Equal(src, []byte(tc.src)) {
				t.Fatal("the input was modified")
			}
			for _, g := range plan.groups {
				for _, b := range g.branches {
					if strings.TrimSpace(string(masked[b.dir.start:b.dir.end])) != "" {
						t.Fatalf("directive line %q survived", masked[b.dir.start:b.dir.end])
					}
				}
			}
		})
	}
}

func TestCondMaskKeepsOneBranch(t *testing.T) {
	src := []byte("#if 0\nint dead;\n#elif B\nint second;\n#else\nint third;\n#endif\n#if A\nint one;\n#else\nint two;\n#endif\n")
	masked := string(planAll(src, false).apply(src))
	for _, want := range []string{"int second;", "int one;"} {
		if !strings.Contains(masked, want) {
			t.Errorf("%q was blanked: %q", want, masked)
		}
	}
	for _, gone := range []string{"int dead;", "int third;", "int two;", "#if", "#else", "#endif", "#elif"} {
		if strings.Contains(masked, gone) {
			t.Errorf("%q survived: %q", gone, masked)
		}
	}
}

func TestCondPlanCountsBlankedBranches(t *testing.T) {
	src := []byte("#if A\nint a;\n#elif B\nint b;\n#else\n#endif\n")
	plan := planAll(src, false)
	// The #elif and the #else are blanked; the #else holds nothing.
	if plan.branches != 1 {
		t.Errorf("branches = %d, want 1", plan.branches)
	}
}

func TestCondExternCPairIsMaskedTogether(t *testing.T) {
	src := []byte("#ifdef __cplusplus\nextern \"C\" {\n#endif\nint f(void);\n#ifdef __cplusplus\n}\n#endif\n#if X\nint a;\n#else\nint b;\n#endif\n")
	groups := buildCondGroups(scanCondDirectives(src, false))
	// An error on the opener only.
	opener := condSpan{bytes.Index(src, []byte("extern")), bytes.Index(src, []byte("extern")) + 6}
	plan := planCondMask(src, groups, []condSpan{opener})
	if !plan.selected[0] || !plan.selected[1] {
		t.Fatalf("the pair is not masked together: %v", plan.selected)
	}
	if plan.selected[2] {
		t.Errorf("an unrelated group was masked: %v", plan.selected)
	}
	masked := plan.apply(src)
	if strings.Count(string(masked), "{") != strings.Count(string(masked), "}") {
		t.Errorf("braces are not balanced in %q", masked)
	}
}

func TestCondMultiBranchGroupNeedsAnErrorNextToIt(t *testing.T) {
	src := []byte("#if A\nint a;\nint b;\nint c;\nint d;\nint e;\n#else\nint f;\n#endif\n")
	groups := buildCondGroups(scanCondDirectives(src, false))
	inside := bytes.Index(src, []byte("int c;"))
	plan := planCondMask(src, groups, []condSpan{{inside, inside + 6}})
	if plan.selected[0] {
		t.Error("an error in the middle of a branch selected the group")
	}
	at := bytes.Index(src, []byte("#else"))
	plan = planCondMask(src, groups, []condSpan{{at, at + 1}})
	if !plan.selected[0] {
		t.Error("an error on the #else did not select the group")
	}
	// A missing token has no width.
	plan = planCondMask(src, groups, []condSpan{{at, at}})
	if !plan.selected[0] {
		t.Error("a missing token on the #else did not select the group")
	}
}

func TestCondCutsDeclaration(t *testing.T) {
	cases := map[string]bool{
		"int f(int a,\n#if X\n int b)\n#else\n int c)\n#endif\n{ }\n":         true,
		"A::A() : a_(1),\n#if X\n b_(2),\n#endif\n c_(3) {}\n":                true,
		"x = 1\n#if X\n  + 2\n#endif\n;\n":                                    false, // '+' is not in the set; the tree decides
		"f(a\n#if X\n , b\n#endif\n);\n":                                      true,
		"int f()\n#if X\n{ return 1; }\n#else\n;\n#endif\n":                   true,
		"#if X\n[Attr]\n#endif\nvoid F();\n":                                  true,
		"int a;\n#if X\nint b;\n#else\nint c;\n#endif\nint d;\n":              false,
		"void f() {}\n#if X\nvoid g() {}\n#endif\n":                           false,
		"#ifdef __cplusplus\nextern \"C\" {\n#endif\nint a;\n":                false,
		"const char *s =\n#if X\n \"a\";\n#else\n \"b\";\n#endif\n":           true,
		"enum { A,\n#if X\n B,\n#endif\n C };\n":                              true,
		"#if X\n#include <a.h>\n#endif\n#if Y\n#define Z 1\n#endif\nint a;\n": false,
	}
	for src, want := range cases {
		g := groupsOf(src, false)
		if len(g) == 0 {
			t.Errorf("no group in %q", src)
			continue
		}
		got := false
		for i := range g {
			got = got || g[i].cuts()
		}
		if got != want {
			t.Errorf("cuts(%q) = %v, want %v", src, got, want)
		}
	}
}

// plainFails says whether the plain parse of src has errors, so that a test of
// the mask fails if its input stops being something the grammar chokes on.
func plainFails(t *testing.T, language, src string) bool {
	t.Helper()
	p := cppParser(t, language)
	tree, _, err := p.parseOnce(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return tree.RootNode().HasError()
}

func parseSource(t *testing.T, language, path, src string) *Result {
	t.Helper()
	res, err := cppParser(t, language).Parse(context.Background(), path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func condSymbolNames(res *Result) map[string]int {
	out := map[string]int{}
	for _, s := range res.Symbols {
		out[s.Name]++
	}
	return out
}

// The shape of serilog's ILogger.cs: default interface methods behind a
// feature symbol, a body or a `;` behind a parameter list, an attribute and
// an expression body in groups of their own.
const iloggerShaped = `using System;
#if FEATURE_DEFAULT_INTERFACE
using System.Diagnostics;
#endif

namespace Demo;

public interface ILog
{
#if FEATURE_DEFAULT_INTERFACE
    private static readonly object[] NoValues = Array.Empty<object>();
#endif

#if FEATURE_DEFAULT_INTERFACE
    [DefaultImplementation]
#endif
    int Level(string name)
#if FEATURE_DEFAULT_INTERFACE
        => name.Length
#endif
    ;

    int Total(int[] items)
#if FEATURE_DEFAULT_INTERFACE
    {
        return items.Length;
    }
#else
        ;
#endif

    void Write(string message);
}

public class Log : ILog
{
    public int Level(string name) => 0;
    public int Total(int[] items) => 0;
    public void Write(string message) { }
}
`

func TestCSharpDefaultInterfaceMethodsBehindIf(t *testing.T) {
	if !plainFails(t, "c_sharp", iloggerShaped) {
		t.Fatal("the plain parse is clean: this input no longer shows the problem")
	}
	res := parseSource(t, "c_sharp", "ILog.cs", iloggerShaped)
	names := condSymbolNames(res)
	for _, want := range []string{"ILog", "Level", "Total", "Write", "Log"} {
		if names[want] == 0 {
			t.Errorf("missing %s; got %v", want, names)
		}
	}
	if names["Level"] != 2 || names["Total"] != 2 || names["Write"] != 2 {
		t.Errorf("each method should be one in the interface and one in the class: %v", names)
	}
	if res.Status != StatusOK {
		t.Errorf("status = %s (%v), want ok", res.Status, res.Err)
	}
	if res.BranchesBlanked == 0 {
		t.Error("no branch was blanked, so the mask was not what read the file")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "conditional branch") {
		t.Errorf("the blanked branch is not said in the stored text: %v", res.Err)
	}
	var super []string
	for _, r := range res.References {
		if strings.Contains(r.Kind, "extend") || strings.Contains(r.Kind, "implement") {
			super = append(super, r.Name)
		}
	}
	if len(super) != 1 || super[0] != "ILog" {
		t.Errorf("supertype references = %v, want [ILog]", super)
	}
}

// The shape of google/benchmark's reporter header: an export macro on the
// classes, a header guard, and a group inside a constructor's initialiser list
// and one in a parameter list.
const reporterShaped = `#ifndef REPORTER_H_
#define REPORTER_H_

#ifdef __cplusplus
extern "C" {
#endif

namespace bench {

class BENCH_EXPORT Reporter {
 public:
  struct Context {
    int cpus;
  };

  struct Run {
    Run()
        : failed(false),
#if defined(BENCH_PMU)
          counters(0),
#endif
          iterations(1) {}
    bool failed;
    int iterations;
  };

  virtual bool Report(const Context& context) = 0;
  virtual ~Reporter();
};

class BENCH_EXPORT ConsoleReporter : public Reporter {
 public:
  bool Report(const Context& context) override;

 protected:
  virtual void Print(int level,
#if defined(BENCH_COLOR)
                     int color)
#else
                     int plain)
#endif
  {
    (void)level;
  }
};

class BENCH_EXPORT JsonReporter : public Reporter {
 public:
  bool Report(const Context& context) override;
};

}  // namespace bench

#ifdef __cplusplus
}
#endif

#endif  // REPORTER_H_
`

func TestCppHeaderWithConditionalsInsideClasses(t *testing.T) {
	if !plainFails(t, "cpp", reporterShaped) {
		t.Fatal("the plain parse is clean: this input no longer shows the problem")
	}
	res := parseSource(t, "cpp", "reporter.h", reporterShaped)
	names := condSymbolNames(res)
	for _, want := range []string{"Reporter", "ConsoleReporter", "JsonReporter", "Run", "Context", "Print"} {
		if names[want] == 0 {
			t.Errorf("missing %s; got %v", want, names)
		}
	}
	for _, bad := range []string{"BENCH_EXPORT", "counters", "color", "plain"} {
		if names[bad] != 0 {
			t.Errorf("%s is a symbol", bad)
		}
	}
	if res.MacrosMasked == 0 || res.BranchesBlanked == 0 {
		t.Errorf("macros masked %d, branches blanked %d: both masks should have read this file", res.MacrosMasked, res.BranchesBlanked)
	}
	var supers []string
	for _, r := range res.References {
		if strings.Contains(r.Kind, "extend") {
			supers = append(supers, r.Name)
		}
	}
	if len(supers) != 2 {
		t.Errorf("supertype references = %v, want two Reporter", supers)
	}
}

func TestIfInsideAnInitializerList(t *testing.T) {
	src := `Env::Env(int flags)
    : flags_(flags),
#if HAVE_MMAP
      mmap_(true),
#else
      mmap_(false),
#endif
      limit_(10) {
}

int Env::Limit() const { return limit_; }

static int table[] = {
    1,
#ifdef TWO
    2,
#endif
    3,
};

int sum(void) { return table[0]; }
`
	res := parseSource(t, "cpp", "env.cc", src)
	names := condSymbolNames(res)
	for _, want := range []string{"Env", "Limit", "sum"} {
		if names[want] == 0 {
			t.Errorf("missing %s; got %v", want, names)
		}
	}
	if res.Status != StatusOK {
		t.Errorf("status = %s (%v), want ok", res.Status, res.Err)
	}
	c := parseSource(t, "c", "t.c", "int f(int a,\n#ifdef X\n  int b)\n#else\n  int b, int c)\n#endif\n{ return a; }\nint after(void) { return 1; }\n")
	cn := condSymbolNames(c)
	if cn["f"] == 0 || cn["after"] == 0 {
		t.Errorf("C: got %v", cn)
	}
}

func TestExternCGuardsStayBalanced(t *testing.T) {
	src := `#ifdef __cplusplus
extern "C" {
#endif

int open_db(int flags);
int close_db(void);

int checked(int a,
#ifdef RANGE
            int b, int limit)
#else
            int b)
#endif
{
    return a + b;
}

#ifdef __cplusplus
}
#endif
`
	for _, lang := range []string{"c", "cpp"} {
		res := parseSource(t, lang, "db.h", src)
		if condSymbolNames(res)["checked"] == 0 {
			t.Errorf("%s: checked is missing: %v", lang, condSymbolNames(res))
		}
		if res.Status != StatusOK {
			t.Errorf("%s: status = %s (%v), want ok", lang, res.Status, res.Err)
		}
	}
}

// The guard has to refuse a mask that does not help: here the first branch is
// not code, and the second is the only place the definition is.
func TestCondGuardRefusesAMaskThatDoesNotHelp(t *testing.T) {
	src := "int keep_me(void) { return 1; }\n#ifdef FOO\nint x = ;\n#else\nint only_in_the_else(void) { return 2; }\n#endif\n"
	if !wouldMask(t, "c", src) {
		t.Fatal("the mask is not due for this input, so the test proves nothing")
	}
	res := parseSource(t, "c", "x.c", src)
	if res.BranchesBlanked != 0 {
		t.Errorf("a mask that lost a definition was kept: %d branches blanked", res.BranchesBlanked)
	}
	names := condSymbolNames(res)
	if names["keep_me"] == 0 || names["only_in_the_else"] == 0 {
		t.Errorf("the plain result should stand: %v", names)
	}
}

func TestCondGuardRefusesAKeywordSymbol(t *testing.T) {
	names := map[string]bool{}
	mc := &maskContext{plan: &condPlan{}, language: "c_sharp"}
	p := cppParser(t, "c_sharp")
	good := []byte("class A { void F() { } }")
	tree, _, err := p.parseOnce(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	tags, _, _ := p.tagTree(tree, good)
	if !maskGuard(tree.RootNode(), nil, tree.RootNode(), tags, names, mc) {
		t.Fatal("the guard refused a plain good parse against an empty plain result")
	}
	tags = append(tags, tags[0])
	tags[len(tags)-1].Name = "new"
	if maskGuard(tree.RootNode(), nil, tree.RootNode(), tags, names, mc) {
		t.Fatal("the guard accepted a symbol named after a keyword")
	}
}

// A file with nothing to mask is parsed once and its text untouched; so is a
// file whose groups are each complete declarations.
func TestCleanConditionalsAreParsedOnceAndUntouched(t *testing.T) {
	for name, tc := range map[string]struct{ lang, src string }{
		"c":    {"c", "#ifdef WIN\nint open_it(void) { return 1; }\n#else\nint open_it_posix(void) { return 2; }\n#endif\nint other(void) { return 3; }\n"},
		"cpp":  {"cpp", "#if defined(_WIN32)\nclass Win { };\n#else\nclass Posix { };\n#endif\nint other() { return 1; }\n"},
		"cs":   {"c_sharp", "#if NET6\nclass Six { }\n#else\nclass Old { }\n#endif\nclass Other { }\n"},
		"none": {"cpp", "class Plain { public: int Get() const { return 1; } };\n"},
	} {
		t.Run(name, func(t *testing.T) {
			before := parseOnceCalls.Load()
			res := parseSource(t, tc.lang, "x", tc.src)
			if n := parseOnceCalls.Load() - before; n != 1 {
				t.Errorf("%d parses, want 1", n)
			}
			if res.BranchesBlanked != 0 || res.MacrosMasked != 0 || res.Status != StatusOK || res.Err != nil {
				t.Errorf("masked %d, blanked %d, status %s, err %v", res.MacrosMasked, res.BranchesBlanked, res.Status, res.Err)
			}
		})
	}
	// Both branches of a clean group are in the index.
	res := parseSource(t, "cpp", "x.cc", "#if defined(_WIN32)\nclass Win { };\n#else\nclass Posix { };\n#endif\n")
	if n := condSymbolNames(res); n["Win"] == 0 || n["Posix"] == 0 {
		t.Errorf("a clean group lost a branch: %v", n)
	}
}

// Whatever the file needs, it is parsed at most twice: the macro mask and the
// conditional mask share the one retry.
func TestBothMasksShareOneRetry(t *testing.T) {
	before := parseOnceCalls.Load()
	res := parseSource(t, "cpp", "reporter.h", reporterShaped)
	if n := parseOnceCalls.Load() - before; n > 2 {
		t.Errorf("%d parses, want at most 2", n)
	}
	if res.MacrosMasked == 0 || res.BranchesBlanked == 0 {
		t.Errorf("macros %d, branches %d: the test needs both masks", res.MacrosMasked, res.BranchesBlanked)
	}
	before = parseOnceCalls.Load()
	parseSource(t, "c_sharp", "ILog.cs", iloggerShaped)
	if n := parseOnceCalls.Load() - before; n > 2 {
		t.Errorf("%d parses of the C# file, want at most 2", n)
	}
}

func TestBlankedBranchesAreInTheSyntaxErrorText(t *testing.T) {
	// A mask that leaves an error behind must say it was applied.
	src := iloggerShaped + "\nclass Broken { void M( { }\n"
	res := parseSource(t, "c_sharp", "x.cs", src)
	if res.BranchesBlanked == 0 {
		t.Skip("the mask was not kept for this input")
	}
	if res.Status != StatusPartial || !strings.Contains(res.Err.Error(), "conditional branch") {
		t.Errorf("status %s, err %v: want a partial parse that mentions the blanked branches", res.Status, res.Err)
	}
}

func TestCondMaskHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cppParser(t, "c_sharp").Parse(ctx, "x.cs", []byte(iloggerShaped)); err == nil {
		t.Fatal("a cancelled parse returned no error")
	}
}

func TestOtherLanguagesAreNotMasked(t *testing.T) {
	if got := scanCondDirectives([]byte("#if A\n#endif\n"), false); len(got) != 2 {
		t.Fatalf("scanner: %d directives", len(got))
	}
	res := parseSource(t, "go", "x.go", "package p\n\n// #if A\nfunc F() {}\n")
	if res.BranchesBlanked != 0 {
		t.Errorf("blanked %d branches in Go", res.BranchesBlanked)
	}
}

// A mask that would blank most of what the plain parse found has to be paid
// for by what it gains; here it gains nothing, so the plain result stands. The
// shape is fmt's test/enum-test.cc, where `#if !FEATURE` holds a stub and the
// `#else` holds the file.
func TestCondGuardRefusesAMaskThatBlanksMostOfTheFile(t *testing.T) {
	var b strings.Builder
	b.WriteString("#ifndef FEATURE\nint stub(void) { return 0; }\n#else\n")
	for _, n := range []string{"one", "two", "three", "four", "five", "six"} {
		b.WriteString("int " + n + "(void) { return 1; }\n")
	}
	b.WriteString("#endif\nint broken( ;\n")
	if !wouldMask(t, "c", b.String()) {
		t.Fatal("the mask is not due for this input, so the test proves nothing")
	}
	res := parseSource(t, "c", "x.c", b.String())
	if res.BranchesBlanked != 0 {
		t.Errorf("%d branches blanked: the mask threw the file away", res.BranchesBlanked)
	}
	names := condSymbolNames(res)
	for _, want := range []string{"stub", "one", "two", "three", "four", "five", "six"} {
		if names[want] == 0 {
			t.Errorf("%s is missing: %v", want, names)
		}
	}
}

// The same name in the branch that is blanked does not excuse a loss of every
// copy of it: twenty-two `TEST` definitions are not one.
func TestCondGuardCountsRepeatedNames(t *testing.T) {
	var b strings.Builder
	b.WriteString("#if !FEATURE\nint TEST(void) { return 0; }\n#else\n")
	for i := 0; i < 8; i++ {
		b.WriteString("int TEST(void) { return 1; }\n")
	}
	b.WriteString("#endif\nint broken( ;\n")
	if !wouldMask(t, "c", b.String()) {
		t.Fatal("the mask is not due for this input, so the test proves nothing")
	}
	res := parseSource(t, "c", "x.c", b.String())
	if res.BranchesBlanked != 0 {
		t.Errorf("%d branches blanked", res.BranchesBlanked)
	}
	if n := condSymbolNames(res)["TEST"]; n != 9 {
		t.Errorf("TEST symbols = %d, want 9", n)
	}
}

// wouldMask says whether the conditional mask is due for src: the plain tree
// has an error that selects a group which has a branch to blank. A test of the
// guard asserts it, so that it cannot pass because the mask was never tried.
func wouldMask(t *testing.T, language, src string) bool {
	t.Helper()
	p := cppParser(t, language)
	tree, _, err := p.parseOnce(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	plan := planCondMask([]byte(src), buildCondGroups(scanCondDirectives([]byte(src), language == "c_sharp")), leafErrorSpans(tree.RootNode()))
	return !plan.empty() && plan.branches > 0
}
