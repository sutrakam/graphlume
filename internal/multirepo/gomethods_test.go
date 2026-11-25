package multirepo

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// methodLib is the provider of the method-stitching tests: concrete methods
// on pointer and value receivers, functions with one, two and no plain
// results, variables with and without a declared type, an interface that
// embeds another, promotion through embedding (unique, ambiguous, and next
// to a field of another package), a field of function type, and a typed
// constant group.
const methodLib = `package lib

import "strings"

type Client struct{}

func NewClient() *Client { return &Client{} }

func NewPair() (*Client, error) { return &Client{}, nil }

func NewValue() Client { return Client{} }

func Count() int { return 0 }

func (c *Client) Send() error { return nil }

func (c Client) Name() string { return "" }

func (c *Client) With() *Client { return c }

func (c *Client) Base() *Base { return nil }

var Default = &Client{}

var Typed *Client = &Client{}

var (
	Grouped Client
	A, B    *Client
)

var Log *strings.Builder

type Sender interface {
	Send() error
	Closer
}

type Closer interface {
	Close() error
}

type Base struct{}

func (Base) Hello() {}

type Peer struct{}

func (Peer) Hello() {}

type Wrapper struct {
	Base
	*Client
}

type Both struct {
	Base
	Peer
}

type Ext struct {
	strings.Builder
	Base
}

type Holder struct {
	Inner Client
	Fn    func()
}

type Level int

func (Level) String() string { return "" }

const (
	Low Level = iota
	High
)
`

// methodApp is the importer. Every call the tests look at sits on a line of
// its own ending in a marker comment, so its position is found, not typed.
const methodApp = `package main

import (
	"strings"

	"example.com/lib"
)

func param(c *lib.Client, s lib.Sender, w lib.Wrapper, b lib.Both, e lib.Ext, h lib.Holder) {
	c.Send() // @t-ptr
	c.Name() // @t-value-recv
	s.Send() // @t-iface
	s.Close() // @t-iface-embedded
	w.Hello() // @t-promoted
	w.Send() // @t-promoted-ptr
	b.Hello() // @t-promoted-twice
	e.Hello() // @t-foreign-embed
	h.Fn() // @t-field
	c.Missing() // @t-missing
}

func paths() {
	lib.Default.Send() // @p-untyped
	lib.Typed.Send() // @p-typed
	lib.Grouped.Name() // @p-grouped
	lib.B.Send() // @p-multi-name
	lib.Log.WriteString("") // @p-foreign
	lib.High.String() // @p-const-group
	lib.Typed.Inner.Send() // @p-field
	lib.Nope.Send() // @p-missing
}

func values() {
	c := lib.NewClient()
	c.Send() // @v-new
	d, _ := lib.NewPair()
	d.Send() // @v-pair
	e := lib.NewClient()
	e = lib.NewClient()
	e.Send() // @v-reassigned
	f := lib.NewClient()
	f = f.With()
	f.Send() // @v-self-call
	g := lib.NewClient()
	g = g.Base()
	g.Hello() // @v-self-call-other-type
	var v = lib.NewValue()
	v.Name() // @v-var
	n := lib.Count()
	n.String() // @v-int
	x := strings.NewReader("")
	x.Len() // @v-other-package
	lib.NewClient().Send() // @v-chain
	k := lib.NewClient()
	func() {
		k.Send() // @v-closure
	}()
	m := lib.NewClient()
	if m != nil {
		m := 1
		_ = m
	}
	m.Send() // @v-shadowed
}
`

// markedCall is one call of methodApp: its marker, receiver as the parser
// records it, the kind of that receiver, and the method name.
type markedCall struct {
	marker, recv, kind, name string
}

var methodAppCalls = []markedCall{
	{"t-ptr", "lib.Client", model.ReceiverType, "Send"},
	{"t-value-recv", "lib.Client", model.ReceiverType, "Name"},
	{"t-iface", "lib.Sender", model.ReceiverType, "Send"},
	{"t-iface-embedded", "lib.Sender", model.ReceiverType, "Close"},
	{"t-promoted", "lib.Wrapper", model.ReceiverType, "Hello"},
	{"t-promoted-ptr", "lib.Wrapper", model.ReceiverType, "Send"},
	{"t-promoted-twice", "lib.Both", model.ReceiverType, "Hello"},
	{"t-foreign-embed", "lib.Ext", model.ReceiverType, "Hello"},
	{"t-field", "lib.Holder", model.ReceiverType, "Fn"},
	{"t-missing", "lib.Client", model.ReceiverType, "Missing"},
	{"p-untyped", "lib.Default", model.ReceiverPath, "Send"},
	{"p-typed", "lib.Typed", model.ReceiverPath, "Send"},
	{"p-grouped", "lib.Grouped", model.ReceiverPath, "Name"},
	{"p-multi-name", "lib.B", model.ReceiverPath, "Send"},
	{"p-foreign", "lib.Log", model.ReceiverPath, "WriteString"},
	{"p-const-group", "lib.High", model.ReceiverPath, "String"},
	{"p-field", "lib.Typed.Inner", model.ReceiverPath, "Send"},
	{"p-missing", "lib.Nope", model.ReceiverPath, "Send"},
	{"v-new", "c", model.ReceiverValue, "Send"},
	{"v-pair", "d", model.ReceiverValue, "Send"},
	{"v-reassigned", "e", model.ReceiverValue, "Send"},
	{"v-self-call", "f", model.ReceiverValue, "Send"},
	{"v-self-call-other-type", "g", model.ReceiverValue, "Hello"},
	{"v-var", "v", model.ReceiverValue, "Name"},
	{"v-int", "n", model.ReceiverValue, "String"},
	{"v-other-package", "x", model.ReceiverValue, "Len"},
	{"v-chain", "?", model.ReceiverExpr, "Send"},
	{"v-closure", "k", model.ReceiverValue, "Send"},
	{"v-shadowed", "m", model.ReceiverValue, "Send"},
}

// markerAt is the line of a marker in src and the 0-based column the call's
// receiver starts at, which is where the index records a call.
func markerAt(t *testing.T, src, marker string) (int, int) {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if strings.HasSuffix(line, "// @"+marker) {
			return i + 1, len(line) - len(strings.TrimLeft(line, "\t "))
		}
	}
	t.Fatalf("no marker %q", marker)
	return 0, 0
}

// symbolsOf indexes a Go file the way the parser does for the symbols table:
// functions, methods (owner between file and name), types, an interface's
// methods, variables and constants, with ids from next.
func symbolsOf(t *testing.T, file, src string, next int64) []registry.SymbolHit {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []registry.SymbolHit
	add := func(kind, name, qual string, pos token.Pos) {
		next++
		line := fset.Position(pos).Line
		out = append(out, registry.SymbolHit{
			ID: next, Kind: kind, Name: name, FQN: file + ":" + qual, FilePath: file,
			StartLine: line, EndLine: line, Exported: isExportedGo(name),
		})
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				add("function", d.Name.Name, d.Name.Name, d.Pos())
				continue
			}
			_, owner, _ := namedType(d.Recv.List[0].Type)
			add("method", d.Name.Name, owner+"."+d.Name.Name, d.Pos())
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					it, iface := s.Type.(*ast.InterfaceType)
					kind := "class"
					if iface {
						kind = "interface"
					}
					add(kind, s.Name.Name, s.Name.Name, s.Pos())
					if iface {
						for _, m := range it.Methods.List {
							for _, n := range m.Names {
								add("method", n.Name, s.Name.Name+"."+n.Name, n.Pos())
							}
						}
					}
				case *ast.ValueSpec:
					kind := "variable"
					if d.Tok == token.CONST {
						kind = "constant"
					}
					for _, n := range s.Names {
						add(kind, n.Name, n.Name, n.Pos())
					}
				}
			}
		}
	}
	return out
}

// enclosingOf is the importer's function spans, as FileSymbols reads them.
func enclosingOf(t *testing.T, file, src string, next int64) []registry.EnclosingSymbol {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []registry.EnclosingSymbol
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			next++
			out = append(out, encl(next, fd.Name.Name, file,
				fset.Position(fd.Pos()).Line, fset.Position(fd.End()).Line))
		}
	}
	return out
}

// methodEstate is lib, the provider of methodLib, and app, which imports it
// and makes every call of methodAppCalls.
func methodEstate(t *testing.T) []Member {
	t.Helper()
	lib := newFakeGo("")
	lib.gomods = map[string]string{"go.mod": "module example.com/lib\n"}
	lib.sources = map[string]string{"lib.go": methodLib}
	lib.symbols = symbolsOf(t, "lib.go", methodLib, 100)
	lib.pkgNames = map[string]string{"": "lib"}

	app := newFakeGo("")
	app.gomods = map[string]string{"go.mod": "module example.com/app\n\nrequire example.com/lib v1.0.0\n"}
	app.sources = map[string]string{"main.go": methodApp}
	app.imports = map[string][]registry.Import{"main.go": {{Path: "strings"}, {Path: "example.com/lib"}}}
	for _, c := range methodAppCalls {
		line, col := markerAt(t, methodApp, c.marker)
		app.calls = append(app.calls, fakeCall{
			file: "main.go", line: line, col: col, name: c.name, recv: c.recv, recvKind: c.kind,
		})
	}
	app.enclosing = map[string][]registry.EnclosingSymbol{"main.go": enclosingOf(t, "main.go", methodApp, 200)}
	return []Member{
		{Name: "lib", IndexPath: "/estate/lib/.graphlume/index.db", Source: lib, RunID: 1},
		{Name: "app", IndexPath: "/estate/app/.graphlume/index.db", Source: app, RunID: 1},
	}
}

// markerOfLine names the marker of a line of methodApp, for readable diffs.
func markerOfLine(line int) string {
	lines := strings.Split(methodApp, "\n")
	if line < 1 || line > len(lines) {
		return "?"
	}
	_, m, ok := strings.Cut(lines[line-1], "// @")
	if !ok {
		return "?"
	}
	return m
}

func TestGoMethodStitchLinksByTheRuleOfEachReceiver(t *testing.T) {
	g := build(t, methodEstate(t)).GoModules
	if g == nil {
		t.Fatal("no Go module section")
	}

	var got []string
	for _, l := range g.Links {
		if l.CallKind != callKindMethod {
			continue
		}
		got = append(got, markerOfLine(l.FromLine)+" "+l.FromSymbol+" "+l.Call+" -> "+l.ToSymbol+" "+string(l.Confidence))
		if len(l.Legs) != 4 || l.Legs[2].Name != "type resolution" || l.Legs[3].Name != "method lookup" {
			t.Errorf("%s carries legs %+v", l.Call, l.Legs)
		}
		if l.Confidence == model.ConfidenceExact {
			t.Errorf("%s is exact", l.Call)
		}
	}
	want := []string{
		// A type the importer states: high for a method declared on it,
		// pointer receiver or value receiver alike.
		"t-ptr param (lib.Client).Send -> Client.Send high",
		"t-value-recv param (lib.Client).Name -> Client.Name high",
		// An interface's method is a real target with an unknown
		// implementation: medium, its embedded interface's too.
		"t-iface param (lib.Sender).Send -> Sender.Send medium",
		"t-iface-embedded param (lib.Sender).Close -> Closer.Close medium",
		// Promotion through a field of the same package: medium.
		"t-promoted param (lib.Wrapper).Hello -> Base.Hello medium",
		"t-promoted-ptr param (lib.Wrapper).Send -> Client.Send medium",
		// A provider variable whose declaration states its type: medium,
		// in a var group and a multi-name spec too, and a constant that
		// takes its group's type.
		"p-typed paths lib.Typed.Send -> Client.Send medium",
		"p-grouped paths lib.Grouped.Name -> Client.Name medium",
		"p-multi-name paths lib.B.Send -> Client.Send medium",
		"p-const-group paths lib.High.String -> Level.String medium",
		// A local assigned once from a provider function that declares its
		// result: medium; `x = x.With()` keeps it, With returning *Client.
		"v-new values c.Send -> Client.Send medium",
		"v-self-call values f.Send -> Client.Send medium",
		"v-var values v.Name -> Client.Name medium",
		"v-closure values k.Send -> Client.Send medium",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("method links =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	reasons := map[string]string{}
	for _, s := range g.MethodsNotResolved {
		reasons[markerOfLine(s.Line)] = s.Reason
	}
	for marker, part := range map[string]string{
		"t-foreign-embed":        "strings.Builder",
		"t-field":                "Holder.Fn is a field",
		"t-missing":              "declares no method Missing",
		"p-untyped":              "declares no type",
		"p-foreign":              "belongs to another package",
		"p-field":                "through a field",
		"p-missing":              "no package-level variable or constant Nope",
		"v-pair":                 "one of 2 names",
		"v-reassigned":           "assigned again at line",
		"v-self-call-other-type": "own method (Base)",
		"v-int":                  "int is not a type of the package",
	} {
		if !strings.Contains(reasons[marker], part) {
			t.Errorf("%s: reason %q lacks %q", marker, reasons[marker], part)
		}
	}
	for _, m := range []string{"v-other-package", "v-chain", "v-shadowed"} {
		if _, sampled := reasons[m]; sampled {
			t.Errorf("untraced call %s was sampled", m)
		}
	}
	if len(g.Ambiguous) != 1 || markerOfLine(g.Ambiguous[0].Line) != "t-promoted-twice" ||
		!reflect.DeepEqual(g.Ambiguous[0].Candidates, []string{"Base.Hello", "Peer.Hello"}) {
		t.Errorf("ambiguous = %+v", g.Ambiguous)
	}

	if len(g.Pairs) != 1 {
		t.Fatalf("pairs = %+v", g.Pairs)
	}
	p := g.Pairs[0]
	wantReasons := map[string]int{
		reasonEmbedding: 1, reasonNoSuchMethod: 2, reasonNoDeclaredType: 2, reasonForeignType: 1,
		reasonFieldAccess: 1, reasonNoSuchDecl: 1, reasonSeveralValues: 1, reasonReassigned: 2,
		// strings.NewReader's value, the chain and the shadowed local.
		reasonUntraced: 3,
	}
	if p.MethodLinks != len(want) || p.MethodAmbiguous != 1 || !reflect.DeepEqual(p.MethodNotResolvable, wantReasons) {
		t.Errorf("pair = links %d, ambiguous %d, not resolvable %v; want %d, 1, %v",
			p.MethodLinks, p.MethodAmbiguous, p.MethodNotResolvable, len(want), wantReasons)
	}
	// Every call is accounted for exactly once.
	total := p.MethodLinks + p.MethodAmbiguous + sumCounts(p.MethodNotResolvable)
	if total != len(methodAppCalls) {
		t.Errorf("%d calls counted, %d made", total, len(methodAppCalls))
	}
	// Method calls are never counted among the package-qualified ones.
	if p.Links != 0 || p.Ambiguous != 0 || p.Unmatched != 0 {
		t.Errorf("method calls leaked into the function counts: %+v", p)
	}
}

// Method links feed callers and impact with no change to either: they are
// links into the method's symbol id.
func TestGoMethodLinksReachSystemCallers(t *testing.T) {
	members := methodEstate(t)
	var send registry.SymbolHit
	for _, s := range members[0].Source.(*fakeGo).symbols {
		if s.FQN == "lib.go:Client.Send" {
			send = s
		}
	}
	target := SymbolRef{Member: "lib", ID: send.ID, Name: "Send", Qualified: "Client.Send", Kind: "method",
		File: "lib.go", Line: send.StartLine}
	rep, err := federator(t).SystemCallers(t.Context(), members, target, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range rep.Callers {
		got = append(got, markerOfLine(c.Line)+" "+string(c.Confidence)+" "+c.Leg)
	}
	sort.Strings(got)
	want := []string{
		"p-multi-name medium go-module", "p-typed medium go-module",
		"t-promoted-ptr medium go-module", "t-ptr high go-module",
		"v-closure medium go-module", "v-new medium go-module", "v-self-call medium go-module",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("callers of Client.Send =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Build-constrained declarations of one method: one signature links to each
// at medium, two signatures are ambiguous, as for functions.
func TestGoMethodVariants(t *testing.T) {
	for _, tc := range []struct {
		name, darwin string
		links        int
	}{
		{"same signature", "func (c *Client) Send() error { return nil }\n", 2},
		{"different signature", "func (c *Client) Send(n int) error { return nil }\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members := methodEstate(t)
			lib := members[0].Source.(*fakeGo)
			src := strings.Replace(methodLib, "func (c *Client) Send() error { return nil }\n", "", 1)
			lib.sources = map[string]string{
				"lib.go":           src,
				"send_linux.go":    "//go:build linux\n\npackage lib\n\nfunc (c *Client) Send() error { return nil }\n",
				"send_darwin.go":   "//go:build darwin\n\npackage lib\n\n" + tc.darwin,
				"send_test.go":     "package lib\n\nfunc (c *Client) Send() error { return nil }\n",
				"sub/elsewhere.go": "package sub\n\ntype Client struct{}\n\nfunc (c *Client) Send() error { return nil }\n",
			}
			lib.symbols = symbolsOf(t, "lib.go", src, 100)
			lib.symbols = append(lib.symbols, symbolsOf(t, "send_linux.go", lib.sources["send_linux.go"], 300)...)
			lib.symbols = append(lib.symbols, symbolsOf(t, "send_darwin.go", lib.sources["send_darwin.go"], 400)...)

			g := build(t, members).GoModules
			n := 0
			for _, l := range g.Links {
				if markerOfLine(l.FromLine) == "t-ptr" {
					n++
					if l.Confidence != model.ConfidenceMedium || !strings.Contains(l.Note, "GOOS") {
						t.Errorf("variant link %+v", l)
					}
				}
			}
			if n != tc.links {
				t.Fatalf("t-ptr linked to %d declarations, want %d", n, tc.links)
			}
			if tc.links == 0 {
				found := false
				for _, s := range g.Ambiguous {
					if markerOfLine(s.Line) == "t-ptr" && len(s.Candidates) == 2 {
						found = true
					}
				}
				if !found {
					t.Errorf("differing variants not reported ambiguous: %+v", g.Ambiguous)
				}
			}
		})
	}
}

// A provider package that does not parse is not read at all: a declaration
// missing from it could turn a variant into a single candidate.
func TestGoMethodUnparsedProviderIsCounted(t *testing.T) {
	members := methodEstate(t)
	lib := members[0].Source.(*fakeGo)
	lib.sources["broken.go"] = "package lib\n\nfunc (c *Client) {\n"
	p := build(t, members).GoModules.Pairs[0]
	if p.MethodLinks != 0 || p.MethodNotResolvable[reasonUnreadable] == 0 {
		t.Fatalf("pair = %+v", p)
	}
}

func TestGoMethodStitchIsDeterministic(t *testing.T) {
	render := func() (string, string) {
		rep := build(t, methodEstate(t))
		var text bytes.Buffer
		if err := Render(&text, rep); err != nil {
			t.Fatal(err)
		}
		js, err := json.Marshal(rep.GoModules)
		if err != nil {
			t.Fatal(err)
		}
		return text.String(), string(js)
	}
	t1, j1 := render()
	t2, j2 := render()
	if t1 != t2 || j1 != j2 {
		t.Fatal("two builds of one estate rendered differently")
	}
	for _, want := range []string{
		"GO MODULE LINKS (14 calls linked: 0 function, 14 method)",
		"function calls: linked 0, ambiguous 0, unmatched 0",
		// Wrapped by the report; the reasons themselves are checked above.
		"method calls: linked 14, ambiguous 1, not resolvable 14 (1 through a\n",
		"\n  method calls linked:\n",
		"calls (lib.Client).Send  ->  Client.Send (method)  lib.go:",
		"~ type resolution            high   the importer's source states the receiver's type, lib.Client",
		"! Sender is an interface",
		"\n  method calls not linked:\n",
		`"method_not_resolvable":{`,
	} {
		if !strings.Contains(t1+j1, want) {
			t.Errorf("report lacks %q:\n%s", want, t1)
		}
	}
}

func TestTraceValueRules(t *testing.T) {
	src := `package main

func f() {
	a := lib.New()
	a.M() // @a
	var b, c = lib.New(), lib.New()
	b.M() // @b
	d := (lib.New())
	d.M() // @d
	e := lib.New().Next()
	e.M() // @e
	g := lib.New()
	g++
	g.M() // @g
	for h := range lib.All() {
		h.M() // @h
	}
	if i := lib.New(); i != nil {
		i.M() // @i
	}
	k := lib.New()
	if true {
		k.M() // @k
	}
}

func shadow() {
	lib := other()
	j := lib.New()
	j.M() // @j
}

func g(p *T) {
	p.M() // @p
	q := lib.New()
	q.M() // @q
	q, r := lib.New(), 1
	_ = r
	if true {
		s := lib.New()
		_ = s
	}
	s.M() // @s
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	fs := &goFileSyntax{fset: fset, file: f}
	for _, tc := range []struct {
		marker, name, qual, reason string
	}{
		{"a", "a", "lib", ""},
		{"b", "b", "lib", reasonSeveralValues}, // declared with another name
		{"d", "d", "lib", ""},                  // parentheses are the call itself
		{"e", "e", "", reasonUntraced},         // a chain
		{"g", "g", "lib", reasonReassigned},    // ++ is an assignment
		{"h", "h", "", reasonUntraced},         // a range variable
		{"i", "i", "lib", ""},                  // declared in the if's own scope
		{"k", "k", "lib", ""},                  // used in a nested block
		{"j", "j", "", reasonUntraced},         // lib is a local here
		{"p", "p", "", reasonUntraced},         // a parameter
		// `q, r :=` declares q a second time as far as a syntax-only reader
		// can tell, which is doubt enough.
		{"q", "q", "", reasonUntraced},
		{"s", "s", "", reasonUntraced}, // the call is outside s's scope
	} {
		line, col := markerAt(t, src, tc.marker)
		got := traceValue(fs, line, col, tc.name)
		if got.qual != tc.qual || got.reason != tc.reason {
			t.Errorf("%s: trace = %+v, want qual %q reason %q", tc.marker, got, tc.qual, tc.reason)
		}
	}
}
