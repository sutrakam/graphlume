package parse

import (
	"context"
	"strings"
	"testing"
)

func parseScript(t *testing.T, language, file, src string) *Result {
	t.Helper()
	p, err := parserForLanguage(language)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Parse(context.Background(), file, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == StatusFailed {
		t.Fatalf("parse failed: %v", res.Err)
	}
	return res
}

// TestScriptOwners pins which object a method belongs to: the qualified name
// is what the resolver reads an owner from.
func TestScriptOwners(t *testing.T) {
	src := `function Shape() {}
Shape.prototype.area = function () { return 1; };
var app = module.exports = {};
app.init = function init() {};
exports.parse = function (s) { return s; };
other.thing = function () {};
function wrapper() { app.late = function () {}; Gadget.prototype.spin = function () {}; }
`
	res := parseScript(t, "javascript", "lib/shape.js", src)
	got := map[string]string{}
	for _, s := range res.Symbols {
		got[s.Name] = strings.TrimPrefix(s.FQN, "")
	}
	for name, want := range map[string]string{
		"area": "lib/shape.js:Shape.area",
		"init": "lib/shape.js:app.init",
		"spin": "lib/shape.js:Gadget.spin",
	} {
		if got[name] != want {
			t.Errorf("%s has the qualified name %q, want %q", name, got[name], want)
		}
	}
	for _, name := range []string{"parse", "Shape"} {
		if q, ok := got[name]; ok && q != "" && strings.Contains(q, ".") {
			t.Errorf("%s is at the top level of its file and has the qualified name %q", name, q)
		}
	}
	for _, name := range []string{"thing", "late"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s is an assignment and defines nothing", name)
		}
	}
}

// TestCommonJSImportBindings pins the rows a top-level `require` produces.
func TestCommonJSImportBindings(t *testing.T) {
	src := `'use strict';
var parse = require('./parse');
const { a, b: c } = require('./lib');
var q = require('./q').y;
var lazy;
function f() { var inner = require('./inner'); }
`
	res := parseScript(t, "javascript", "x.js", src)
	got := map[string]bool{}
	for _, imp := range res.Imports {
		got[imp.Path+"|"+imp.Alias] = true
	}
	for _, want := range []string{
		"./parse|parse", "./parse|",
		"./lib|.a", "./lib|.c=b", "./lib|",
		"./q|.q=y", "./q|",
		"./inner|",
	} {
		if !got[want] {
			t.Errorf("missing import row %q; have %v", want, got)
		}
	}
	for _, unwanted := range []string{"./inner|inner", "./inner|.inner", "./lib|.b", "./lib|b"} {
		if got[unwanted] {
			t.Errorf("unexpected import row %q", unwanted)
		}
	}
}

// TestJSXAndNewReferences pins what a rendered component and a construction
// record: a call on the component, shaped like any other call.
func TestJSXAndNewReferences(t *testing.T) {
	src := `import { Button } from "./button";
export function Page() {
  const a = <Button variant="x" />;
  const b = <Foo.Bar>text</Foo.Bar>;
  const c = <Ctx.Provider value={1}><span /></Ctx.Provider>;
  const d = <div className="a"><svg:rect /></div>;
  const e = new Widget(1);
  const f = new ns.Gadget();
  return [a, b, c, d, e, f];
}
`
	res := parseScript(t, "tsx", "page.tsx", src)
	type ref struct{ kind, name, receiver, shape string }
	var got []ref
	for _, r := range res.References {
		got = append(got, ref{r.Kind, r.Name, r.Receiver, r.ReceiverKind})
	}
	want := []ref{
		{"reference.call", "Button", "", "bare"},
		{"reference.call", "Bar", "Foo", "name"},
		{"reference.jsx", "Ctx", "", ""},
		{"reference.call", "Widget", "", "bare"},
		{"reference.call", "Gadget", "ns", "name"},
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("missing reference %+v; have %+v", w, got)
		}
	}
	for _, g := range got {
		switch g.name {
		case "div", "span", "svg", "rect", "Provider":
			t.Errorf("%q is HTML or a context's own part and records no reference; have %+v", g.name, g)
		}
	}
}

// TestStemName pins the rule that names an anonymous module.exports.
func TestStemName(t *testing.T) {
	for file, want := range map[string]string{
		"lib/view.js":                 "view",
		"/abs/lib/merge-options.js":   "mergeOptions",
		"router/index.js":             "router",
		"index.js":                    "index",
		"a/b/foo.config.js":           "fooConfig",
		"lib/9lives.js":               "",
		"C:\\proj\\lib\\thing.min.js": "thingMin",
	} {
		if got := stemName(file); got != want {
			t.Errorf("stemName(%q) = %q, want %q", file, got, want)
		}
	}
}
