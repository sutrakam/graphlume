package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// These tests stitch real, freshly indexed Go modules: lib, a provider; app,
// which builds against lib's checkout through a replace directive; and web,
// which requires a release of lib and also imports example.com/library, a
// module whose path merely starts like lib's. Each is indexed and resolved
// with the real commands, so the facts reach the stitch through the parser,
// the resolver and the registry reads a user's command takes.

var goLibFiles = map[string]string{
	"go.mod": "module example.com/lib\n\ngo 1.22\n",
	"lib.go": `package lib

// Do does it.
func Do() int { return 1 }

type Client struct{}

func NewClient() *Client { return &Client{} }

func (c *Client) Send() error { return nil }
`,
	"wrap.go": `package lib

// Wrap calls Do.
func Wrap() int { return Do() }
`,
	"do_linux.go": `//go:build linux

package lib

func Platform() string { return "linux" }
`,
	"do_darwin.go": `//go:build darwin

package lib

func Platform() string { return "darwin" }
`,
	"sub/sub.go": "package sub\n\nfunc X() int { return 2 }\n",
	// The declarations method calls are followed into: a value-receiver
	// method beside lib.go's pointer-receiver Send, a variable with and one
	// without a declared type, an interface, and a method promoted through
	// an embedded struct.
	"methods.go": `package lib

func (c Client) Name() string { return "" }

var Default = &Client{}

var Typed *Client = &Client{}

type Sender interface {
	Send() error
}

type Base struct{}

func (Base) Hello() {}

type Wrapper struct {
	Base
}
`,
}

var goAppFiles = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.22\n\nrequire example.com/lib v1.4.2\n\nreplace example.com/lib => ../lib\n",
	"main.go": `package main

import (
	"example.com/lib"
	"example.com/lib/sub"
)

func run() int {
	c := lib.NewClient()
	_ = c.Send()
	_ = lib.Platform()
	return lib.Do() + sub.X()
}

func handler() int { return run() }

func main() { _ = handler() }
`,
	"other.go": `package main

import l2 "example.com/lib"

func viaAlias(c *l2.Client) int {
	_ = c.Send()
	return l2.Do()
}
`,
	"methods.go": `package main

import "example.com/lib"

func methods(s lib.Sender, w lib.Wrapper) {
	_ = lib.Default.Send()
	_ = lib.Typed.Send()
	_ = s.Send()
	w.Hello()
	v := lib.NewClient()
	_ = v.Name()
	r := lib.NewClient()
	r = lib.NewClient()
	_ = r.Send()
}
`,
}

var goWebFiles = map[string]string{
	"go.mod": "module example.com/web\n\ngo 1.22\n\nrequire example.com/lib v1.4.2\n\nrequire example.com/library v1.0.0\n",
	"web.go": `package web

import (
	"example.com/lib"
	"example.com/library"
)

func Fetch() int { return lib.Do() + library.Do() }
`,
}

// indexGoModule writes files into dir and indexes and resolves it.
func indexGoModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []string{"init", "index", "resolve"} {
		if out, err := run(t, dir, step); err != nil {
			t.Fatalf("%s %s: %v\n%s", step, dir, err, out)
		}
	}
}

// goEstateFixture is a workspace holding lib, app and web, indexed, and an
// estate file naming all three.
func goEstateFixture(t *testing.T) (ws, estate string) {
	t.Helper()
	ws = isolate(t)
	indexGoModule(t, filepath.Join(ws, "lib"), goLibFiles)
	indexGoModule(t, filepath.Join(ws, "app"), goAppFiles)
	indexGoModule(t, filepath.Join(ws, "web"), goWebFiles)
	writeEstate(t, ws, "  - name: lib\n    index: lib\n  - name: app\n    index: app\n  - name: web\n    index: web\n")
	return ws, filepath.Join(ws, "estate.yml")
}

func TestSystemStitchesGoModuleCallsAcrossMembers(t *testing.T) {
	ws, estate := goEstateFixture(t)

	out, err := run(t, ws, "--json", "system", "--members", estate)
	if err != nil {
		t.Fatalf("system: %v\n%s", err, out)
	}
	var rep struct {
		GoModules struct {
			Pairs []struct {
				Importer, Provider, Module  string
				Links, Ambiguous, Unmatched int
				MethodLinks                 int            `json:"method_links"`
				MethodAmbiguous             int            `json:"method_ambiguous"`
				MethodNotResolvable         map[string]int `json:"method_not_resolvable"`
				RequiredVersions            []string       `json:"required_versions"`
				Warnings                    []string
			}
			Links []struct {
				FromMember string `json:"from_member"`
				FromSymbol string `json:"from_symbol"`
				FromFile   string `json:"from_file"`
				FromLine   int    `json:"from_line"`
				Call       string
				CallKind   string `json:"call_kind"`
				ToMember   string `json:"to_member"`
				ToSymbol   string `json:"to_symbol"`
				ToFile     string `json:"to_file"`
				ToLine     int    `json:"to_line"`
				Confidence string
				Legs       []struct{ Name, Confidence string }
			}
			MethodsNotResolved []struct {
				File, Call, Reason string
				Line               int
			} `json:"methods_not_resolved"`
		} `json:"go_modules"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}

	var got []string
	for _, l := range rep.GoModules.Links {
		got = append(got, strings.Join([]string{
			l.FromMember, l.FromSymbol, l.FromFile + ":" + strconv.Itoa(l.FromLine), l.Call,
			l.ToMember, l.ToSymbol, l.ToFile + ":" + strconv.Itoa(l.ToLine), l.Confidence,
		}, " "))
		wantLegs := map[string]int{"function": 3, "method": 4}[l.CallKind]
		if len(l.Legs) != wantLegs {
			t.Errorf("%s link %s carries %d legs, want %d", l.CallKind, l.Call, len(l.Legs), wantLegs)
		}
	}
	want := []string{
		"app run main.go:9 lib.NewClient lib NewClient lib.go:8 high",
		// c := lib.NewClient(): NewClient declares *Client, so c.Send() is
		// Client.Send, at medium.
		"app run main.go:10 c.Send lib Client.Send lib.go:10 medium",
		"app run main.go:11 lib.Platform lib Platform do_darwin.go:5 medium",
		"app run main.go:11 lib.Platform lib Platform do_linux.go:5 medium",
		"app run main.go:12 lib.Do lib Do lib.go:4 high",
		"app run main.go:12 sub.X lib X sub/sub.go:3 high",
		// lib.Default declares no type and is not linked; lib.Typed does.
		"app methods methods.go:7 lib.Typed.Send lib Client.Send lib.go:10 medium",
		// An interface's method, and a method promoted through embedding.
		"app methods methods.go:8 (lib.Sender).Send lib Sender.Send methods.go:10 medium",
		"app methods methods.go:9 (lib.Wrapper).Hello lib Base.Hello methods.go:15 medium",
		"app methods methods.go:10 lib.NewClient lib NewClient lib.go:8 high",
		// A value-receiver method through a traced local.
		"app methods methods.go:11 v.Name lib Client.Name methods.go:3 medium",
		// r is reassigned: its producers link as functions, r.Send() does not.
		"app methods methods.go:12 lib.NewClient lib NewClient lib.go:8 high",
		"app methods methods.go:13 lib.NewClient lib NewClient lib.go:8 high",
		// A parameter of a stated provider type, through an aliased import:
		// the pointer-receiver method, at high.
		"app viaAlias other.go:6 (l2.Client).Send lib Client.Send lib.go:10 high",
		"app viaAlias other.go:7 l2.Do lib Do lib.go:4 high",
		"web Fetch web.go:8 lib.Do lib Do lib.go:4 high",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("links =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if len(rep.GoModules.Pairs) != 2 {
		t.Fatalf("pairs = %+v", rep.GoModules.Pairs)
	}
	app, web := rep.GoModules.Pairs[0], rep.GoModules.Pairs[1]
	wantReasons := map[string]int{"no_declared_type": 1, "reassigned": 1}
	if app.Importer != "app" || app.Links != 8 || app.Ambiguous != 0 || app.Unmatched != 0 ||
		app.MethodLinks != 6 || app.MethodAmbiguous != 0 || !reflect.DeepEqual(app.MethodNotResolvable, wantReasons) {
		t.Errorf("app pair = %+v", app)
	}
	var notLinked []string
	for _, s := range rep.GoModules.MethodsNotResolved {
		notLinked = append(notLinked, s.File+":"+strconv.Itoa(s.Line)+" "+s.Call+" — "+s.Reason)
	}
	if want := []string{
		"methods.go:6 lib.Default.Send — var Default declares no type; its initialiser is not read",
		"methods.go:14 r.Send — r is assigned again at line 13",
	}; !reflect.DeepEqual(notLinked, want) {
		t.Errorf("methods not resolved =\n%s\nwant\n%s", strings.Join(notLinked, "\n"), strings.Join(want, "\n"))
	}
	if len(app.Warnings) != 0 {
		t.Errorf("a replace pointing at lib's checkout warned: %q", app.Warnings)
	}
	wantWarn := "web requires example.com/lib v1.4.2; lib's index records no commit (go.mod has no version), " +
		"so whether the indexed code is that version cannot be checked"
	if web.Importer != "web" || web.Links != 1 || len(web.Warnings) != 1 || web.Warnings[0] != wantWarn {
		t.Errorf("web pair = %+v", web)
	}

	text, err := run(t, ws, "system", "--members", estate)
	if err != nil {
		t.Fatalf("system: %v\n%s", err, text)
	}
	for _, s := range []string{
		"GO MODULE LINKS (15 calls linked: 9 function, 6 method)",
		"lib defines example.com/lib (.)",
		"app -> lib  example.com/lib",
		"function calls: linked 8, ambiguous 0, unmatched 0",
		"method calls: linked 6, ambiguous 0, not resolvable 2 (1 no declared type,",
		"requires v1.4.2",
		"calls l2.Do  ->  Do (function)  lib.go:4",
		"\n  method calls linked:\n",
		"calls (l2.Client).Send  ->  Client.Send (method)  lib.go:10",
		"! Sender is an interface",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("report lacks %q", s)
		}
	}
	if strings.Contains(text, "library.Do") {
		t.Error("a call into example.com/library was linked into example.com/lib")
	}

	again, err := run(t, ws, "system", "--members", estate)
	if err != nil || again != text {
		t.Fatalf("a second run differs (err %v)", err)
	}
	// The member table carries each index's age in fractional hours, which
	// moves with the clock; the Go section must not move at all.
	againJSON, err := run(t, ws, "--json", "system", "--members", estate)
	if err != nil {
		t.Fatal(err)
	}
	if goSection(t, againJSON) != goSection(t, out) {
		t.Fatal("a second JSON run's go_modules section differs")
	}
}

// TestSystemGoCommandsAreDocumented: a subcommand nobody can find in the
// reference is a subcommand nobody runs.
func TestSystemGoCommandsAreDocumented(t *testing.T) {
	for _, doc := range []string{"CLI.md", filepath.Join("reference", "cli.md")} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"### `system callers <member>:<symbol>`", "### `system impact <member>:<symbol>`",
			"**Go module links.**", "never `exact`",
		} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("docs/%s does not mention %q", doc, want)
			}
		}
	}
}

func goSection(t *testing.T, js string) string {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		t.Fatal(err)
	}
	return string(raw["go_modules"])
}

func TestSystemCallersAndImpactCrossRepositories(t *testing.T) {
	ws, estate := goEstateFixture(t)

	out, err := run(t, ws, "system", "callers", "lib:Do", "--members", estate)
	if err != nil {
		t.Fatalf("system callers: %v\n%s", err, out)
	}
	for _, s := range []string{
		"lib:Do (function) at lib.go:4",
		"lib     Wrap      wrap.go:4",
		"app     run       main.go:12",
		"app     viaAlias  other.go:7",
		"web     Fetch     web.go:8",
		"go-module lib.Do",
		"4 caller(s).",
		"web requires example.com/lib v1.4.2",
		"LOWER BOUND",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("callers lacks %q:\n%s", s, out)
		}
	}

	js, err := run(t, ws, "--json", "system", "callers", "lib:Do", "--members", estate)
	if err != nil {
		t.Fatalf("system callers --json: %v\n%s", err, js)
	}
	var callers struct {
		Target struct {
			Member, Name, File string
			Line               int
		}
		Callers []struct {
			Member, Caller, File, Confidence, Leg string
			Line                                  int
		}
	}
	if err := json.Unmarshal([]byte(js), &callers); err != nil {
		t.Fatalf("decode: %v\n%s", err, js)
	}
	if callers.Target.Member != "lib" || callers.Target.File != "lib.go" || len(callers.Callers) != 4 {
		t.Fatalf("callers JSON = %+v", callers)
	}
	for _, c := range callers.Callers {
		if c.Leg == "go-module" && c.Confidence != "high" {
			t.Errorf("stitched caller %s:%s at %s", c.Member, c.Caller, c.Confidence)
		}
	}

	imp, err := run(t, ws, "--json", "system", "impact", "lib:Do", "--depth", "3", "--members", estate)
	if err != nil {
		t.Fatalf("system impact: %v\n%s", err, imp)
	}
	var radius struct {
		Members    []string
		Dependents []struct {
			Member, Symbol, Confidence, Via, Reaches string
			Depth                                    int
		}
		Caveats []string
	}
	if err := json.Unmarshal([]byte(imp), &radius); err != nil {
		t.Fatalf("decode: %v\n%s", err, imp)
	}
	seen := map[string]string{}
	for _, d := range radius.Dependents {
		seen[d.Member+":"+d.Symbol] = strconv.Itoa(d.Depth) + " " + d.Via + " " + d.Confidence
		if d.Member != "lib" && d.Confidence == "exact" {
			t.Errorf("%s:%s is exact across a repository boundary", d.Member, d.Symbol)
		}
	}
	for k, want := range map[string]string{
		"app:run":     "1 go-module high",
		"web:Fetch":   "1 go-module high",
		"app:handler": "2 CALLS high",
		"app:main":    "3 CALLS high",
	} {
		if seen[k] != want {
			t.Errorf("%s = %q, want %q (all: %v)", k, seen[k], want, seen)
		}
	}
	if strings.Join(radius.Members, ",") != "lib,app,web" {
		t.Errorf("members = %v", radius.Members)
	}
	if len(radius.Caveats) == 0 || !strings.Contains(radius.Caveats[0], "LOWER BOUND") {
		t.Errorf("caveats = %q", radius.Caveats)
	}

	text, err := run(t, ws, "system", "impact", "lib:Do", "--members", estate)
	if err != nil {
		t.Fatalf("system impact: %v\n%s", err, text)
	}
	if !strings.Contains(text, "members reached: lib, app, web") || !strings.Contains(text, "go-module") {
		t.Errorf("impact text:\n%s", text)
	}

	if _, err := run(t, ws, "system", "callers", "nobody:Do", "--members", estate); err == nil ||
		!strings.Contains(err.Error(), `no member is named "nobody"`) {
		t.Errorf("unknown member: %v", err)
	}
	if _, err := run(t, ws, "system", "callers", "Do", "--members", estate); err == nil ||
		!strings.Contains(err.Error(), "<member>:<symbol>") {
		t.Errorf("missing member prefix: %v", err)
	}
}

// Method links reach callers and impact as function links do: the callers of
// Client.Send in other repositories are a parameter of its stated type, a
// local traced to NewClient and a provider variable that declares its type,
// and neither the untyped lib.Default nor the reassigned local is among them.
func TestSystemCallersAndImpactFollowMethodLinks(t *testing.T) {
	ws, estate := goEstateFixture(t)

	js, err := run(t, ws, "--json", "system", "callers", "lib:Client.Send", "--members", estate)
	if err != nil {
		t.Fatalf("system callers: %v\n%s", err, js)
	}
	var callers struct {
		Target  struct{ Member, Qualified, Kind string }
		Callers []struct {
			Member, Caller, File, Confidence, Leg, Call string
			Line                                        int
		}
	}
	if err := json.Unmarshal([]byte(js), &callers); err != nil {
		t.Fatalf("decode: %v\n%s", err, js)
	}
	if callers.Target.Qualified != "Client.Send" || callers.Target.Kind != "method" {
		t.Fatalf("target = %+v", callers.Target)
	}
	var got []string
	for _, c := range callers.Callers {
		got = append(got, strings.Join([]string{c.Member, c.Caller, c.File + ":" + strconv.Itoa(c.Line),
			c.Confidence, c.Leg, c.Call}, " "))
	}
	want := []string{
		"app run main.go:10 medium go-module c.Send",
		"app methods methods.go:7 medium go-module lib.Typed.Send",
		"app viaAlias other.go:6 high go-module (l2.Client).Send",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("callers of Client.Send =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	imp, err := run(t, ws, "--json", "system", "impact", "lib:Client.Send", "--depth", "3", "--members", estate)
	if err != nil {
		t.Fatalf("system impact: %v\n%s", err, imp)
	}
	var radius struct {
		Dependents []struct {
			Member, Symbol, Confidence, Via string
			Depth                           int
		}
	}
	if err := json.Unmarshal([]byte(imp), &radius); err != nil {
		t.Fatalf("decode: %v\n%s", err, imp)
	}
	seen := map[string]string{}
	for _, d := range radius.Dependents {
		seen[d.Member+":"+d.Symbol] = strconv.Itoa(d.Depth) + " " + d.Via + " " + d.Confidence
	}
	for k, want := range map[string]string{
		"app:viaAlias": "1 go-module high",
		"app:run":      "1 go-module medium",
		"app:methods":  "1 go-module medium",
		// The weakest hop on the path is the method link's medium.
		"app:handler": "2 CALLS medium",
	} {
		if seen[k] != want {
			t.Errorf("%s = %q, want %q (all: %v)", k, seen[k], want, seen)
		}
	}
}
