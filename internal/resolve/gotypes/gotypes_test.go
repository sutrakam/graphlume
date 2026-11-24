package gotypes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

func needGoroot(t *testing.T) {
	t.Helper()
	if findGoroot(os.Getenv, "", "") == "" {
		t.Skip("no GOROOT can be found without running anything; set GOROOT")
	}
}

func checkFixture(t *testing.T) *Result {
	t.Helper()
	needGoroot(t)
	res, err := Check(context.Background(), Options{Root: filepath.Join("testdata", "proj")})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// useLines renders every use as "file:line obj kind <- encl" for assertions.
func useLines(res *Result) []string {
	var out []string
	for _, f := range res.Files {
		for _, u := range f.Uses {
			out = append(out, fmt.Sprintf("%s:%d %s %s <- %s", u.File, u.Line, u.Obj.Qualified(), u.Rel(), u.Encl.Qualified()))
		}
	}
	sort.Strings(out)
	return out
}

func has(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func hasPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func TestEveryCallShapeIsRecognisedFromTheSyntaxTree(t *testing.T) {
	res := checkFixture(t)
	lines := useLines(res)

	for _, want := range []string{
		// Two calls on one line, each its own fact, with the receiver's own method.
		"app/app.go:14 Circle.Area CALLS <- Run",
		"app/app.go:14 Square.Area CALLS <- Run",
		// A call on a call result, and a package-qualified call.
		"app/app.go:15 Counter.Name CALLS <- Run",
		"app/app.go:15 Make CALLS <- Run",
		// A method value is a reference and not a call.
		"app/app.go:16 Circle.Area REFERENCES <- Run",
		// A composite literal constructs a type; Hello is promoted from Base.
		"app/app.go:18 Wrapper INSTANTIATES <- Run",
		"app/app.go:18 Base.Hello CALLS <- Run",
		// A call on an interface value resolves to the interface's method; a
		// method value of one is a reference.
		"shapes/shapes.go:35 Shape.Area CALLS <- describeArea",
		"shapes/shapes.go:30 Shape.Name REFERENCES <- Describe",
		// Generic instantiation of a function is a call to the declaration; a
		// method of an instantiated type maps to its declaration.
		"app/app.go:21 Map CALLS <- Run",
		"app/app.go:23 Stack.Push CALLS <- Run",
		// A function literal belongs to the function that contains it.
		"app/app.go:24 Counter.Inc CALLS <- Run",
		// A use in a package-level initialiser belongs to the variable.
		"util/util.go:28 Make CALLS <- registry",
		// A function literal in a package-level variable belongs to it.
		"util/util.go:30 Counter.Name CALLS <- handler",
		// The build constraint picked the file this host builds.
		"app/app.go:29 platformName CALLS <- handlerName",
		// The external test package and the in-package test see their own.
		"app/app_ext_test.go:11 Run CALLS <- TestRun",
		"app/app_test.go:7 handlerName CALLS <- TestHandlerName",
	} {
		if !has(lines, want) {
			t.Errorf("missing fact %q", want)
		}
	}
	if !hasPrefix(lines, "app/app.go:25 Circle INSTANTIATES") {
		t.Errorf("a conversion Circle(...) must be INSTANTIATES")
	}
	// Two types share the method name Name: each use points at its own.
	if has(lines, "app/app.go:15 Circle.Name CALLS <- Run") {
		t.Errorf("the call on util.Make() must not resolve to a Shape's Name")
	}
}

func TestBuildConstraintsAreAppliedAndReported(t *testing.T) {
	res := checkFixture(t)
	if res.Stats.FilesRejected != 1 {
		t.Errorf("FilesRejected = %d, want 1 (platform_other.go)", res.Stats.FilesRejected)
	}
	for _, f := range res.Files {
		if strings.HasSuffix(f.Path, "platform_other.go") {
			t.Errorf("a file the host's build constraints exclude carries facts: %s", f.Path)
		}
	}
}

func TestImplementationsAreProvedNotInferred(t *testing.T) {
	res := checkFixture(t)
	got := map[string]bool{}
	for _, im := range res.Impls {
		got[im.Type.Name+" -> "+im.Iface.Name] = true
		if im.Type.Name == "Square" && !im.PointerOnly {
			t.Errorf("Square implements Shape through its pointer only")
		}
		if im.Type.Name == "Circle" && im.PointerOnly {
			t.Errorf("Circle implements Shape by value")
		}
	}
	for _, want := range []string{"Circle -> Shape", "Square -> Shape"} {
		if !got[want] {
			t.Errorf("missing implementation %s", want)
		}
	}
	if got["Counter -> Shape"] {
		t.Errorf("Counter has a Name but no Area: it does not implement Shape")
	}
	for _, im := range res.Impls {
		if im.Type.Name == "Circle" {
			names := map[string]bool{}
			for _, m := range im.Methods {
				names[m.Concrete.Qualified()+" => "+m.Iface.Qualified()] = true
			}
			for _, w := range []string{"Circle.Area => Shape.Area", "Circle.Name => Shape.Name"} {
				if !names[w] {
					t.Errorf("missing method implementation %q in %v", w, names)
				}
			}
		}
	}
}

func TestPromotionThroughAnUnknownEmbeddedTypeIsWithheld(t *testing.T) {
	res := checkFixture(t)
	lines := useLines(res)
	if hasPrefix(lines, "taint/taint.go:20 Deeper.Run") {
		t.Errorf("s.Run() was proved to be Deeper.Run although S embeds a type from a package that was not found")
	}
	if !has(lines, "taint/taint.go:29 Plain.Go CALLS <- UsePlain") {
		t.Errorf("a method declared directly on the type always wins and may be proved")
	}
	if res.Stats.WithheldTaint == 0 {
		t.Errorf("the withheld selection is not counted")
	}
	if res.Stats.WithStubs != 1 {
		t.Errorf("WithStubs = %d, want 1 (taint)", res.Stats.WithStubs)
	}
	if res.Stats.MissingCount != 1 || res.Stats.MissingImports[0] != "example.org/does/not/exist/unknown" {
		t.Errorf("missing imports = %v", res.Stats.MissingImports)
	}
}

func TestDeterministic(t *testing.T) {
	a := checkFixture(t)
	b := checkFixture(t)
	if fmt.Sprint(useLines(a)) != fmt.Sprint(useLines(b)) {
		t.Errorf("two runs differ")
	}
	fa, fb := a.FileFacts(), b.FileFacts()
	if fmt.Sprintf("%+v", fa) != fmt.Sprintf("%+v", fb) {
		t.Errorf("file facts differ between runs")
	}
}

func TestFactsAreShapedForTheExactWriter(t *testing.T) {
	res := checkFixture(t)
	for _, ff := range res.FileFacts() {
		for _, r := range ff.Relations {
			switch r.Kind {
			case model.EdgeCalls, model.EdgeInstantiates, model.EdgeReferences, model.EdgeImplements:
			default:
				t.Fatalf("unexpected relation kind %s", r.Kind)
			}
			if r.Evidence.Detail != Detail {
				t.Fatalf("detail = %q", r.Evidence.Detail)
			}
			// Source and confidence are NOT set here: scip.Relation.Edge is
			// the only constructor that stamps exact+compiler.
			if r.Evidence.Source != "" {
				t.Fatalf("a relation carries a source before the writer stamped it")
			}
		}
	}
}

func TestNoGorootStillChecksAgainstStubs(t *testing.T) {
	env := func(k string) string {
		if k == "GOROOT" || k == "PATH" {
			return ""
		}
		return os.Getenv(k)
	}
	res, err := Check(context.Background(), Options{Root: filepath.Join("testdata", "proj"), Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Goroot != "" {
		t.Fatalf("a GOROOT was found with none to find: %q", res.Stats.Goroot)
	}
	if res.Stats.Packages == 0 || len(res.Files) == 0 {
		t.Fatalf("nothing was checked without a GOROOT")
	}
	lines := useLines(res)
	if !has(lines, "app/app.go:14 Circle.Area CALLS <- Run") {
		t.Errorf("repository calls must still resolve against stubbed standard-library imports")
	}
}

func TestStripBodiesKeepsLinesAndDeclarations(t *testing.T) {
	src := "package p\n\nfunc F(x struct{ a int }) interface{} {\n\tif x.a > 0 {\n\t\treturn nil\n\t}\n\treturn 1\n}\n\nvar V = func() int { return 2 }\n\nfunc G[T interface{ ~int }](t T) T { return t }\n\ntype X struct{}\n\nfunc (X) M() {}\n"
	out := string(stripBodies([]byte(src)))
	if strings.Count(out, "\n") != strings.Count(src, "\n") {
		t.Errorf("line count changed")
	}
	if strings.Contains(out, "return nil") || strings.Contains(out, "return t") {
		t.Errorf("a function body survived: %q", out)
	}
	if !strings.Contains(out, "return 2") {
		t.Errorf("a package-level function literal was cut")
	}
	for _, d := range []string{"func F(x struct{ a int }) interface{} {", "func G[T interface{ ~int }](t T) T {", "type X struct{}", "func (X) M() {"} {
		if !strings.Contains(out, d) {
			t.Errorf("declaration lost: %s", d)
		}
	}
}

func TestGoModParsing(t *testing.T) {
	mf := parseGoMod([]byte("module example.com/m // c\n\ngo 1.22\n\nrequire (\n\tgithub.com/A/b v1.2.3 // indirect\n\tx.y/z v0.1.0\n)\nrequire q.r/s v2.0.0\nreplace github.com/A/b => ../b\nreplace (\n\tx.y/z v0.1.0 => x.y/zz v0.2.0\n)\n"))
	if mf.Path != "example.com/m" || mf.GoVersion != "1.22" {
		t.Errorf("header: %+v", mf)
	}
	if mf.Require["github.com/A/b"] != "v1.2.3" || mf.Require["x.y/z"] != "v0.1.0" || mf.Require["q.r/s"] != "v2.0.0" {
		t.Errorf("require: %v", mf.Require)
	}
	if len(mf.Replace) != 2 || mf.Replace[0].New != "../b" || mf.Replace[1].NewVersion != "v0.2.0" || mf.Replace[1].OldVersion != "v0.1.0" {
		t.Errorf("replace: %+v", mf.Replace)
	}
	if got := escapePath("github.com/BurntSushi/toml"); got != "github.com/!burnt!sushi/toml" {
		t.Errorf("escape: %s", got)
	}
}
