package parse

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// defs renders a result's definitions as "line kind name", sorted by line.
func defList(res *Result) []string {
	var out []string
	for _, s := range res.Symbols {
		out = append(out, strconv.Itoa(s.Range.Start.Line)+" "+string(s.Kind)+" "+s.Name)
	}
	return out
}

func wantList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func refNames(res *Result, kindPrefix string) []string {
	var out []string
	for _, r := range res.References {
		if strings.HasPrefix(r.Kind, kindPrefix) {
			out = append(out, r.Name+"/"+r.Receiver)
		}
	}
	sort.Strings(out)
	return out
}

func TestTypeScriptPrivateMembers(t *testing.T) {
	res := parse(t, "ctx.ts", `class Ctx {
  #status: number = 200
  static #count = 0
  #init(a: string): void {}
  get #size() { return 1 }
  async #load(): Promise<void> {}
  init() {}
  run() {
    this.#init('x')
    this.#load()
    Ctx.#count++
    this.#headers?.get('a')
    init()
  }
}
`)
	wantList(t, "definitions", defList(res), []string{
		"1 class Ctx", "2 field #status", "3 field #count", "4 method #init",
		"5 method #size", "6 method #load", "7 method init", "8 method run",
	})
	wantList(t, "calls", refNames(res, "reference.call"), []string{"#init/this", "#load/this", "get/?", "init/"})
}

func TestJavaScriptPrivateMembers(t *testing.T) {
	res := parse(t, "ctx.js", `class Ctx {
  #status = 200
  pub = 1
  #init(a) {}
  run() { this.#init('x') }
}
`)
	// A public field is still not a definition in JavaScript.
	wantList(t, "definitions", defList(res), []string{"1 class Ctx", "2 field #status", "4 method #init", "5 method run"})
	wantList(t, "calls", refNames(res, "reference.call"), []string{"#init/this"})
}

func TestTypeScriptOverloadsFoldIntoTheImplementation(t *testing.T) {
	res := parse(t, "o.ts", `function f(a: string): void
function f(a: number): void
function f(a: any) {}

export function g(a: string): string
export function g(a: any): any { return a }

declare function h(a: string): void

interface I {
  m(a: string): void
  m(a: number): void
}

class Over {
  m(a: string): void
  m(a: number): void
  m(a: any) {}
  get v() { return 1 }
  set v(x: number) {}
  other() {}
}
`)
	wantList(t, "definitions", defList(res), []string{
		"1 function f", "5 function g", "8 function h", "10 interface I", "11 method m",
		"15 class Over", "16 method m", "19 method v", "20 method v", "21 method other",
	})
	for _, s := range res.Symbols {
		if s.Name == "f" && s.Range.End.Line != 3 {
			t.Errorf("f should span its signatures and implementation, ends at line %d", s.Range.End.Line)
		}
		if s.Name == "f" && s.ParamCount != model.ArityUnknown {
			t.Errorf("f stands for several signatures and has no single parameter count")
		}
	}
}

func TestTypeScriptNamespaceIsAPackage(t *testing.T) {
	res := parse(t, "n.ts", `export namespace Outer {
  export const X = 1
  export function inner() {}
}
namespace A.B {
  export class K {}
}
`)
	var got []string
	for _, s := range res.Symbols {
		got = append(got, string(s.Kind)+" "+s.Name)
	}
	sort.Strings(got)
	wantList(t, "symbols", got, []string{"class K", "function inner", "package A.B", "package Outer"})
}

// tremor: `import { Calendar as CalendarPrimitive }` then `<CalendarPrimitive />`.
// The use keeps the name it is written with; the import row names the export.
func TestRenamedImportKeepsItsNameAndNamesTheExport(t *testing.T) {
	res := parse(t, "d.tsx", `import { Calendar as CalendarPrimitive, Other, type T as U } from "./cal"
import { z as zed } from "./z"
export const Picker = () => <CalendarPrimitive mode="x" />
export const q = () => { zed(1); CalendarPrimitive.Root(); Other(); return <Other /> }
class K extends CalendarPrimitive {}
`)
	var rows []string
	for _, imp := range res.Imports {
		rows = append(rows, imp.Path+" "+imp.Alias)
	}
	sort.Strings(rows)
	wantList(t, "imports", rows, []string{
		"./cal ", "./cal .CalendarPrimitive=Calendar", "./cal .Other", "./cal .U=T",
		"./z ", "./z .zed=z",
	})
	var calls []string
	for _, r := range res.References {
		calls = append(calls, r.Kind+" "+r.Name+"/"+r.Receiver)
	}
	sort.Strings(calls)
	wantList(t, "references", calls, []string{
		"reference.call CalendarPrimitive/",
		"reference.call Other/",
		"reference.call Other/",
		"reference.call Root/CalendarPrimitive",
		"reference.call zed/",
		"reference.extends CalendarPrimitive/",
	})
}
