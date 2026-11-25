package multirepo

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func sym(id int64, kind, name, file string, line int) registry.SymbolHit {
	return registry.SymbolHit{
		ID: id, Kind: kind, Name: name, FQN: file + ":" + name, FilePath: file,
		StartLine: line, EndLine: line, Exported: isExportedGo(name),
	}
}

func encl(id int64, name, file string, start, end int) registry.EnclosingSymbol {
	return registry.EnclosingSymbol{ID: id, Name: name, FQN: file + ":" + name, Kind: "function", StartLine: start, EndLine: end}
}

func calls(src, dst int64, name, file string, line int, conf model.Confidence, source model.SourceType) registry.EdgeHit {
	return registry.EdgeHit{
		SrcID: src, SrcName: name, SrcQualified: name, DstID: dst, Kind: string(model.EdgeCalls),
		Confidence: string(conf), Source: string(source), EvidenceFile: file, EvidenceLine: line,
	}
}

// goEstate is four members: lib, the provider, with a nested v2 module, a
// package whose clause differs from its directory, GOOS variants with one
// signature and GOARCH variants with two; app, which imports lib through a
// replace pointing at lib's checkout; web, whose replace points elsewhere;
// and library, whose module path shares a prefix with lib's.
func goEstate() []Member {
	lib := newFakeGo("abc1234def5678")
	lib.gomods = map[string]string{
		"go.mod":          "module example.com/lib\n\ngo 1.22\n",
		"v2/go.mod":       "module example.com/lib/v2\n",
		"testdata/go.mod": "module example.com/fixture\n",
	}
	lib.symbols = []registry.SymbolHit{
		sym(10, "function", "Do", "lib.go", 4),
		sym(11, "class", "Client", "lib.go", 8),
		{ID: 12, Kind: "method", Name: "Send", FQN: "lib.go:Client.Send", FilePath: "lib.go", StartLine: 12, Exported: true},
		sym(13, "function", "Platform", "do_darwin.go", 5),
		sym(14, "function", "Platform", "do_linux.go", 5),
		sym(15, "function", "Arch", "arch_amd64.go", 5),
		sym(16, "function", "Arch", "arch_arm64.go", 5),
		sym(17, "function", "X", "sub/sub.go", 3),
		sym(18, "function", "New", "v2/new.go", 3),
		sym(19, "function", "Wrap", "caller.go", 4),
		sym(20, "function", "Encode", "internal/codec/codec.go", 3),
		sym(21, "function", "Do", "lib_test.go", 3),
	}
	lib.pkgNames = map[string]string{"": "lib", "sub": "sub", "v2": "lib", "internal/codec": "codecs"}
	// The root package's text, with Do, Client and Send on the lines the
	// symbols above say, so a call on a *lib.Client finds its method.
	lib.sources = map[string]string{"lib.go": "package lib\n\n// Do does it.\nfunc Do() int { return 1 }\n\n" +
		"// Client\n// sends.\ntype Client struct{}\n\nfunc NewClient() *Client { return &Client{} }\n\n" +
		"func (c *Client) Send() error { return nil }\n"}
	lib.excerpts = map[string]string{
		"do_darwin.go:5":  "func Platform() string { return \"darwin\" }",
		"do_linux.go:5":   "func  Platform()   string {\n\treturn \"linux\"\n}",
		"arch_amd64.go:5": "func Arch() string { return \"amd64\" }",
		"arch_arm64.go:5": "func Arch() int { return 64 }",
	}
	lib.incoming = map[int64][]registry.EdgeHit{
		10: {calls(19, 10, "Wrap", "caller.go", 4, model.ConfidenceExact, model.SourceCompiler)},
	}

	app := newFakeGo("")
	app.gomods = map[string]string{
		"go.mod": "module example.com/app\n\nrequire (\n\texample.com/lib v1.4.2\n\texample.com/lib/v2 v2.0.0\n)\n\n" +
			"replace example.com/lib => ../lib\n",
	}
	app.imports = map[string][]registry.Import{
		"main.go":  {{Path: "example.com/lib"}, {Path: "example.com/lib/sub", Alias: "s"}},
		"other.go": {{Path: "example.com/lib/v2"}, {Path: "example.com/lib/internal/codec"}},
	}
	app.calls = []fakeCall{
		{file: "main.go", line: 10, name: "Do", recv: "lib", recvKind: model.ReceiverName},
		{file: "main.go", line: 11, name: "X", recv: "s", recvKind: model.ReceiverName},
		{file: "main.go", line: 12, name: "Platform", recv: "lib", recvKind: model.ReceiverName},
		{file: "main.go", line: 13, name: "Arch", recv: "lib", recvKind: model.ReceiverName},
		{file: "main.go", line: 14, name: "Missing", recv: "lib", recvKind: model.ReceiverName},
		{file: "main.go", line: 15, name: "Send", recv: "c", recvKind: model.ReceiverValue},
		{file: "main.go", line: 16, name: "Send", recv: "lib.Client", recvKind: model.ReceiverType},
		{file: "main.go", line: 17, name: "Println", recv: "fmt", recvKind: model.ReceiverName},
		{file: "main.go", line: 18, name: "helper", recv: "", recvKind: model.ReceiverBare, resolution: "resolved"},
		{file: "other.go", line: 5, name: "New", recv: "lib", recvKind: model.ReceiverName},
		{file: "other.go", line: 6, name: "Encode", recv: "codecs", recvKind: model.ReceiverName},
	}
	app.enclosing = map[string][]registry.EnclosingSymbol{
		"main.go":  {encl(30, "run", "main.go", 8, 20), encl(32, "handler", "main.go", 22, 22)},
		"other.go": {encl(31, "convert", "other.go", 4, 8)},
	}
	app.incoming = map[int64][]registry.EdgeHit{
		30: {calls(32, 30, "handler", "main.go", 22, model.ConfidenceHigh, model.SourceSourceCode)},
		32: {calls(33, 32, "main", "main.go", 24, model.ConfidenceExact, model.SourceCompiler)},
	}

	web := newFakeGo("")
	web.gomods = map[string]string{
		"go.mod": "module example.com/web\n\nrequire example.com/lib v1.4.2\n" +
			"require example.com/library v0.0.0-20240101000000-0123456789ab\n" +
			"replace example.com/lib => ../lib-fork\n",
	}
	web.imports = map[string][]registry.Import{
		"web.go": {{Path: "example.com/lib"}, {Path: "example.com/library"}},
	}
	web.calls = []fakeCall{
		{file: "web.go", line: 3, name: "Do", recv: "lib", recvKind: model.ReceiverName},
		{file: "web.go", line: 4, name: "Do", recv: "library", recvKind: model.ReceiverName},
	}
	web.enclosing = map[string][]registry.EnclosingSymbol{"web.go": {encl(40, "Fetch", "web.go", 2, 6)}}

	library := newFakeGo("0123456789abcdef")
	library.gomods = map[string]string{"go.mod": "module example.com/library\n"}
	library.symbols = []registry.SymbolHit{sym(50, "function", "Do", "library.go", 3)}
	library.pkgNames = map[string]string{"": "library"}

	return []Member{
		{Name: "web", IndexPath: "/estate/web/.graphlume/index.db", Source: web, RunID: 1},
		{Name: "lib", IndexPath: "/estate/lib/.graphlume/index.db", Source: lib, RunID: 1},
		{Name: "app", IndexPath: "/estate/app/.graphlume/index.db", Source: app, RunID: 1},
		{Name: "library", IndexPath: "/estate/library/.graphlume/index.db", Source: library, RunID: 1},
	}
}

type linkView struct {
	from, call, to string
	conf           model.Confidence
}

func linkViews(ls []GoLink) []linkView {
	var out []linkView
	for _, l := range ls {
		out = append(out, linkView{
			from: l.FromMember + " " + l.FromSymbol + " " + l.FromFile + ":" + strconv.Itoa(l.FromLine),
			call: l.Call,
			to:   l.ToMember + " " + l.ToSymbol + " " + l.ToFile + ":" + strconv.Itoa(l.ToLine),
			conf: l.Confidence,
		})
	}
	return out
}

func TestGoStitchLinksQualifiedCallsAcrossMembers(t *testing.T) {
	rep := build(t, goEstate())
	g := rep.GoModules
	if g == nil {
		t.Fatal("no Go module section")
	}

	got := linkViews(g.Links)
	want := []linkView{
		{"app run main.go:10", "lib.Do", "lib Do lib.go:4", model.ConfidenceHigh},
		// An aliased import binds its alias, and a nested package maps to its
		// own directory.
		{"app run main.go:11", "s.X", "lib X sub/sub.go:3", model.ConfidenceHigh},
		// GOOS variants with one signature: linked to each, at medium.
		{"app run main.go:12", "lib.Platform", "lib Platform do_darwin.go:5", model.ConfidenceMedium},
		{"app run main.go:12", "lib.Platform", "lib Platform do_linux.go:5", model.ConfidenceMedium},
		// A call on a value of a type the importer states, to the method
		// declared on that type.
		{"app run main.go:16", "(lib.Client).Send", "lib Client.Send lib.go:12", model.ConfidenceHigh},
		// A /v2 module is its own module with its own directory, and its
		// package is named without the version element.
		{"app convert other.go:5", "lib.New", "lib New v2/new.go:3", model.ConfidenceHigh},
		// The package clause names the package when the path does not.
		{"app convert other.go:6", "codecs.Encode", "lib Encode internal/codec/codec.go:3", model.ConfidenceHigh},
		{"web Fetch web.go:3", "lib.Do", "lib Do lib.go:4", model.ConfidenceHigh},
		// example.com/library is not a package of example.com/lib: the call
		// links to the member that defines it.
		{"web Fetch web.go:4", "library.Do", "library Do library.go:3", model.ConfidenceHigh},
	}
	if len(got) != len(want) {
		t.Fatalf("links =\n%v\nwant\n%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, l := range g.Links {
		if l.Confidence == model.ConfidenceExact {
			t.Errorf("link %s -> %s is exact", l.Call, l.ToSymbol)
		}
		if want := map[string]int{callKindFunction: 3, callKindMethod: 4}[l.CallKind]; len(l.Legs) != want {
			t.Errorf("%s link %s carries %d legs, want %d", l.CallKind, l.Call, len(l.Legs), want)
		}
	}

	pairs := map[string]GoModulePair{}
	for _, p := range g.Pairs {
		pairs[p.Importer+">"+p.Provider+">"+p.Module] = p
	}
	main := pairs["app>lib>example.com/lib"]
	// The call on c, a local app's index holds no text for, is untraced.
	if main.Links != 4 || main.Ambiguous != 1 || main.Unmatched != 1 || main.MethodLinks != 1 ||
		main.MethodAmbiguous != 0 || !reflect.DeepEqual(main.MethodNotResolvable, map[string]int{reasonUntraced: 1}) {
		t.Errorf("app>lib counts = %+v", main)
	}
	if len(main.Warnings) != 0 {
		t.Errorf("a replace pointing at lib's own checkout warned: %v", main.Warnings)
	}
	v2 := pairs["app>lib>example.com/lib/v2"]
	if len(v2.Warnings) != 1 || v2.Warnings[0] != "app requires example.com/lib/v2 v2.0.0; lib's index is at commit "+
		"abc1234def (go.mod has no version), so whether the indexed code is that version cannot be checked" {
		t.Errorf("v2 warnings = %q", v2.Warnings)
	}
	webLib := pairs["web>lib>example.com/lib"]
	if len(webLib.Warnings) != 1 || !strings.Contains(webLib.Warnings[0], "replace directive points elsewhere") ||
		!strings.Contains(webLib.Warnings[0], "../lib-fork") {
		t.Errorf("web>lib warnings = %q", webLib.Warnings)
	}
	if w := pairs["web>library>example.com/library"].Warnings; len(w) != 0 {
		t.Errorf("a pseudo-version naming the indexed commit warned: %q", w)
	}

	if len(g.Ambiguous) != 1 || g.Ambiguous[0].Call != "lib.Arch" || len(g.Ambiguous[0].Candidates) != 2 {
		t.Errorf("ambiguous = %+v", g.Ambiguous)
	}
	if len(g.Unmatched) != 1 || g.Unmatched[0].Call != "lib.Missing" {
		t.Errorf("unmatched = %+v", g.Unmatched)
	}
	for _, m := range g.Modules {
		if m.Module == "example.com/fixture" {
			t.Error("a go.mod under testdata was taken for a module")
		}
	}
}

func TestGoStitchRefusesAModuleTwoMembersDefine(t *testing.T) {
	members := goEstate()
	fork := newFakeGo("")
	fork.gomods = map[string]string{"go.mod": "module example.com/lib\n"}
	fork.symbols = []registry.SymbolHit{sym(1, "function", "Do", "lib.go", 4)}
	members = append(members, Member{Name: "lib-fork", Source: fork, RunID: 1})

	g := build(t, members).GoModules
	for _, l := range g.Links {
		if l.Module == "example.com/lib" {
			t.Fatalf("linked %s into a module two members define", l.Call)
		}
	}
	if len(g.Notes) != 1 || !strings.Contains(g.Notes[0], "module example.com/lib is defined by lib and lib-fork") {
		t.Fatalf("notes = %q", g.Notes)
	}
}

func TestGoStitchIsDeterministic(t *testing.T) {
	render := func() (string, string) {
		rep := build(t, goEstate())
		var text bytes.Buffer
		if err := Render(&text, rep); err != nil {
			t.Fatal(err)
		}
		js, err := json.Marshal(rep)
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
		"GO MODULE LINKS (8 calls linked: 7 function, 1 method)",
		"app -> lib  example.com/lib",
		"function calls: linked 4, ambiguous 1, unmatched 1",
		"method calls: linked 1, ambiguous 0, not resolvable 1 (1 on values not",
		"calls lib.Do  ->  Do (function)  lib.go:4",
		"could be lib: arch_amd64.go:5",
	} {
		if !strings.Contains(t1, want) {
			t.Errorf("report lacks %q:\n%s", want, t1)
		}
	}
}

// A member whose source cannot be read for Go facts, and a system with no Go
// module at all, print no Go section: the existing report is unchanged.
func TestGoStitchAbsentWithoutGoModules(t *testing.T) {
	if rep := build(t, webAndAPI(t)); rep.GoModules != nil {
		t.Fatalf("HTTP-only estate grew a Go section: %+v", rep.GoModules)
	}
}

func federator(t *testing.T) *Federator {
	t.Helper()
	f, err := New(WithRunningVersion(testVersion))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSystemCallersListsLocalAndStitchedCallers(t *testing.T) {
	target := SymbolRef{Member: "lib", ID: 10, Name: "Do", Qualified: "Do", Kind: "function", File: "lib.go", Line: 4}
	rep, err := federator(t).SystemCallers(context.Background(), goEstate(), target, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range rep.Callers {
		got = append(got, c.Member+" "+c.Caller+" "+c.File+":"+strconv.Itoa(c.Line)+" "+string(c.Confidence)+" "+c.Leg)
	}
	want := []string{
		"lib Wrap caller.go:4 exact local",
		"app run main.go:10 high go-module",
		"web Fetch web.go:3 high go-module",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("callers =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "replace directive points elsewhere") {
		t.Errorf("warnings = %q", rep.Warnings)
	}
}

func TestSystemImpactCrossesMembersAndNeverClaimsExactAcrossThem(t *testing.T) {
	target := SymbolRef{Member: "lib", ID: 10, Name: "Do", Qualified: "Do", Kind: "function", File: "lib.go", Line: 4}
	rep, err := federator(t).SystemImpact(context.Background(), goEstate(), target, ImpactOptions{
		MaxDepth: 3, MinConfidence: model.ConfidenceMedium,
		Kinds: []model.EdgeKind{model.EdgeCalls},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rep.Rows {
		got = append(got, strconv.Itoa(r.Depth)+" "+r.Member+" "+r.Symbol+" "+string(r.Confidence)+" "+r.Via+" "+r.Reaches)
	}
	want := []string{
		"1 app run high go-module lib:Do",
		"1 lib Wrap exact CALLS lib:Do",
		"1 web Fetch high go-module lib:Do",
		"2 app handler high CALLS app:run",
		// main reaches handler by a compiler-proven edge, and still is not
		// exact: its path crossed a repository boundary.
		"3 app main high CALLS app:handler",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("impact =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Join(rep.Members, ",") != "lib,app,web" {
		t.Errorf("members = %v", rep.Members)
	}
	if rep.TruncationNote == "" || !strings.Contains(rep.Caveats[0], "LOWER BOUND") {
		t.Errorf("truncation %q, caveats %q", rep.TruncationNote, rep.Caveats)
	}

	short, err := federator(t).SystemImpact(context.Background(), goEstate(), target, ImpactOptions{
		MaxDepth: 1, MaxRows: 2, Kinds: []model.EdgeKind{model.EdgeCalls},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(short.Rows) != 2 || short.Truncated != 1 || !strings.Contains(short.TruncationNote, "stopped at 2") {
		t.Fatalf("bounded walk: %d rows, truncated %d, note %q", len(short.Rows), short.Truncated, short.TruncationNote)
	}
}

func TestGoModParsingAndNaming(t *testing.T) {
	m := parseGoModText("svc/go.mod", "module \"example.com/svc\" // c\nrequire (\n\ta v1\n\tb v2 // indirect\n)\n"+
		"replace (\n\ta v1 => ../a\n\ta => other.com/a v1.1.0\n)\n")
	if m.Path != "example.com/svc" || m.Dir != "svc" || m.Requires["a"] != "v1" || m.Requires["b"] != "v2" {
		t.Fatalf("parsed %+v", m)
	}
	if r, _ := m.replaceFor("a", "v1"); r.New != "../a" {
		t.Errorf("version-specific replace not preferred: %+v", r)
	}
	if r, _ := m.replaceFor("a", "v9"); r.New != "other.com/a" || r.NewVersion != "v1.1.0" {
		t.Errorf("unversioned replace: %+v", r)
	}
	for in, want := range map[string]string{
		"example.com/lib": "lib", "example.com/lib/v2": "lib", "gopkg.in/yaml.v3": "yaml",
		"example.com/x/sub": "sub",
	} {
		if got := defaultPackageName(in); got != want {
			t.Errorf("defaultPackageName(%q) = %q, want %q", in, got, want)
		}
	}
	if pseudoVersionRev("v0.0.0-20240101000000-0123456789ab") != "0123456789ab" || pseudoVersionRev("v1.4.2") != "" {
		t.Error("pseudo-version commit not read")
	}
	if !importInModule("example.com/lib/sub", "example.com/lib") || importInModule("example.com/library", "example.com/lib") {
		t.Error("module membership is a prefix test, not a path test")
	}
}

// Only the go.mod governing the importing file speaks for the import: a
// nested tools module's replace says nothing about it, and a governing go.mod
// that does not require the module is itself the warning.
func TestGoStitchVersionWarningsReadTheGoverningGoMod(t *testing.T) {
	members := goEstate()
	ops := newFakeGo("")
	ops.gomods = map[string]string{
		"go.mod":       "module example.com/ops\n",
		"tools/go.mod": "module example.com/ops/tools\n\nrequire example.com/lib v0.1.0\nreplace example.com/lib => ../../elsewhere\n",
	}
	ops.imports = map[string][]registry.Import{"run.go": {{Path: "example.com/lib"}}}
	ops.calls = []fakeCall{{file: "run.go", line: 3, name: "Do", recv: "lib", recvKind: model.ReceiverName}}
	members = append(members, Member{Name: "ops", IndexPath: "/estate/ops/.graphlume/index.db", Source: ops, RunID: 1})

	for _, p := range build(t, members).GoModules.Pairs {
		if p.Importer != "ops" {
			continue
		}
		if p.Links != 1 || len(p.RequiredVersions) != 0 || len(p.Warnings) != 1 ||
			!strings.Contains(p.Warnings[0], "no go.mod governing the importing files requires it") {
			t.Fatalf("ops pair = %+v", p)
		}
		return
	}
	t.Fatal("no ops pair")
}
