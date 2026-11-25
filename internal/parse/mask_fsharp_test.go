package parse

import (
	"bytes"
	"strings"
	"testing"
)

// spanTexts returns the source text each span covers (a span's replacement is
// as long as the text it replaces).
func spanTexts(src string, spans []maskSpan) []string {
	var out []string
	for _, s := range spans {
		out = append(out, src[s.start:s.start+len(s.repl)])
	}
	return out
}

func TestFSharpConstraintCallForms(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"the minimal call", "let a = (^T: (member GetAwaiter: unit -> ^A) (task))\n",
			[]string{"(^T: (member GetAwaiter: unit -> ^A) (task))"}},
		{"spaces around the colon", "let a = (^T : (member GetAwaiter : unit -> ^A) (task))\n",
			[]string{"(^T : (member GetAwaiter : unit -> ^A) (task))"}},
		{"no spaces at all", "let a = (^T:(member GetAwaiter:unit-> ^A)(task))\n",
			[]string{"(^T:(member GetAwaiter:unit-> ^A)(task))"}},
		{"arguments without parentheses", "if (^A: (member get_IsCompleted: unit -> bool) awaiter) then 1 else 2\n",
			[]string{"(^A: (member get_IsCompleted: unit -> bool) awaiter)"}},
		{"static member", "let f x = (^a : (static member op_Implicit : ^b -> ^a) x)\n",
			[]string{"(^a : (static member op_Implicit : ^b -> ^a) x)"}},
		{"two type variables", "let f x = (^T or ^U: (static member Combine: ^T * ^U -> ^V) (x, y))\n",
			[]string{"(^T or ^U: (static member Combine: ^T * ^U -> ^V) (x, y))"}},
		{"three type variables", "let f x = (^T or ^U or ^V : (static member Combine: ^T -> ^V) x)\n",
			[]string{"(^T or ^U or ^V : (static member Combine: ^T -> ^V) x)"}},
		{"an operator member", "let f a b = (^T: (static member (+) : ^T * ^T -> ^T) (a, b))\n",
			[]string{"(^T: (static member (+) : ^T * ^T -> ^T) (a, b))"}},
		{"a signature with parentheses and generics", "let f b m g = (^M: (member Bind: 'd -> ('e -> 'c) -> 'c) (b, m, g))\n",
			[]string{"(^M: (member Bind: 'd -> ('e -> 'c) -> 'c) (b, m, g))"}},
		{"a byref signature", "let f d k = (^D: (member TryGetValue: 'key * byref< ^value > -> bool) (d, k))\n",
			[]string{"(^D: (member TryGetValue: 'key * byref< ^value > -> bool) (d, k))"}},
		{"arguments over two lines", "let p =\n    (^v: (static member TryParse: string * byref< ^v > -> bool) (text,\n                                                                 &output))\nlet q = 1\n",
			[]string{"(^v: (static member TryParse: string * byref< ^v > -> bool) (text,\n                                                                 &output))"}},
		{"the call alone on a line after a pipe", "x\n|> fun v ->\n    (^T: (member Run: unit -> int) v)\n",
			[]string{"(^T: (member Run: unit -> int) v)"}},
		{"wrapped in extra parentheses", "let imp arg = ((^a : (static member op_Implicit : ^b -> ^a) arg))\n",
			[]string{"(^a : (static member op_Implicit : ^b -> ^a) arg)"}},
		{"inside a negation", "if not (^T: (static member IsNaN: 'T -> bool) x) then 1 else 2\n",
			[]string{"(^T: (static member IsNaN: 'T -> bool) x)"}},
		{"in a lambda", "xs |> Seq.iter (fun (k, v) -> (^a: (member Add: string * string -> unit) (c, k, v)))\n",
			[]string{"(^a: (member Add: string * string -> unit) (c, k, v))"}},
		{"several in one file", "let a = (^T: (member A: unit -> int) x)\nlet b = (^T: (member B: unit -> int) x)\n",
			[]string{"(^T: (member A: unit -> int) x)", "(^T: (member B: unit -> int) x)"}},
		{"a call inside the arguments of another is one span", "let a = (^T: (member A: unit -> int) (x, (^U: (member B: unit -> int) y)))\n",
			[]string{"(^T: (member A: unit -> int) (x, (^U: (member B: unit -> int) y)))"}},
		{"a closing parenthesis in a string argument", "let a = (^T: (member A: string -> int) (\")\"))\nlet b = 1\n",
			[]string{"(^T: (member A: string -> int) (\")\"))"}},
		{"a parenthesis character literal argument", "let a = (^T: (member A: char -> int) ('('))\nlet b = 1\n",
			[]string{"(^T: (member A: char -> int) ('('))"}},
		{"a comment in the arguments", "let a = (^T: (member A: int -> int) (* ) *) (x))\nlet b = 1\n",
			[]string{"(^T: (member A: int -> int) (* ) *) (x))"}},
		{"after a type variable that is not a character", "let f (x: 'a) (y: 'b) = (^T: (member M: unit -> int) x)\n",
			[]string{"(^T: (member M: unit -> int) x)"}},
		{"after a character literal", "let c = '('\nlet a = (^T: (member M: unit -> int) x)\n",
			[]string{"(^T: (member M: unit -> int) x)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := spanTexts(c.src, findFSharpConstraintCalls([]byte(c.src)))
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("spans\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// Everything that looks like a call but is not one, or is not code.
func TestFSharpConstraintCallsInCommentsAndStringsAreLeftAlone(t *testing.T) {
	for name, src := range map[string]string{
		"a string":                   "let s = \"(^T: (member GetAwaiter: unit -> ^A) (task))\"\n",
		"a string with an escape":    "let s = \"a\\\" (^T: (member M: unit -> int) x)\"\n",
		"a verbatim string":          "let s = @\"(^T: (member M: unit -> int) x)\"\n",
		"a verbatim string, quote":   "let s = @\"say \"\"hi\"\" (^T: (member M: unit -> int) x)\"\n",
		"a triple-quoted string":     "let s = \"\"\"\n(^T: (member M: unit -> int) x)\n\"\"\"\n",
		"an interpolated string":     "let s = $\"(^T: (member M: unit -> int) x)\"\n",
		"a line comment":             "// (^T: (member M: unit -> int) x)\nlet a = 1\n",
		"a doc comment":              "/// Calls (^T: (member M: unit -> int) x) on it.\nlet a = 1\n",
		"a block comment":            "(* (^T: (member M: unit -> int) x) *)\nlet a = 1\n",
		"a nested block comment":     "(* a (* b *) (^T: (member M: unit -> int) x) *)\nlet a = 1\n",
		"a type annotation":          "let f (x: ^T) = x\n",
		"a plain parenthesis":        "let f x = (x: int)\n",
		"a hat without a colon":      "let f x = (^T)\n",
		"a different construct":      "let f x = (^T: int) x\n",
		"a match guard":              "match x with\n| y when y > 0 -> 1\n| _ -> 2\n",
		"a guard on a type var":      "match x with\n| y when 'a = 1 -> 1\n| _ -> 2\n",
		"a clause in angle brackets": "let f<'T when 'T: struct> (x: 'T) = x\n",
		"the multiplication op":      "let m = (*)\nlet n = 1\n",
		"no closing parenthesis":     "let a = (^T: (member M: unit -> int) x\n",
	} {
		if got := findFSharpConstraintCalls([]byte(src)); len(got) != 0 {
			t.Errorf("%s: %q", name, spanTexts(src, got))
		}
	}
	// A call after a comment or string that holds one is still found, once.
	src := "// (^T: (member A: unit -> int) x)\nlet s = \"(^T: (member B: unit -> int) x)\"\nlet a = (^T: (member C: unit -> int) x)\n"
	got := spanTexts(src, findFSharpConstraintCalls([]byte(src)))
	if len(got) != 1 || got[0] != "(^T: (member C: unit -> int) x)" {
		t.Errorf("got %q", got)
	}
}

func TestFSharpMaskKeepsEveryOffset(t *testing.T) {
	src := "namespace N\n\nlet inline p (text: string) =\n    let a = (^T: (member GetAwaiter: unit -> ^A) (task))\n    let b =\r\n        (^v: (static member TryParse: string * byref< ^v > -> bool) (text,\r\n                                                                     &output))\n    a\n"
	spans := findFSharpConstraintCalls([]byte(src))
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	out := applyMaskSpans([]byte(src), spans)
	if len(out) != len(src) {
		t.Fatalf("length %d, want %d", len(out), len(src))
	}
	for i := range out {
		if (src[i] == '\n' || src[i] == '\r') != (out[i] == '\n' || out[i] == '\r') || (src[i] == '\n' || src[i] == '\r') && src[i] != out[i] {
			t.Fatalf("line structure changed at byte %d", i)
		}
	}
	for _, s := range spans {
		if len(s.repl) < 2 || s.repl[0] != '(' || s.repl[1] != ')' || strings.Trim(string(s.repl[2:]), " \r\n") != "" {
			t.Errorf("filler %q is not `()` and spaces", s.repl)
		}
	}
	// Outside the spans nothing changed.
	in := make([]bool, len(src))
	for _, s := range spans {
		for i := s.start; i < s.start+len(s.repl); i++ {
			in[i] = true
		}
	}
	for i := range out {
		if !in[i] && out[i] != src[i] {
			t.Fatalf("byte %d changed outside the spans", i)
		}
	}
	if bytes.Contains(out, []byte("member")) {
		t.Error("the call survived the mask")
	}
}

// The forms of a type constraint clause after a return type, and the mask over
// each: the clause up to the `=` is blanked, nothing else.
func TestFSharpWhenClauseForms(t *testing.T) {
	for name, c := range map[string][2]string{
		"member":         {"let inline g (t: ^T) : ^A when ^T: (member GetAwaiter: unit -> ^A) = t.GetAwaiter()\n", "when ^T: (member GetAwaiter: unit -> ^A) "},
		"struct":         {"let inline g (t: ^T) : int when ^T: struct = 1\n", "when ^T: struct "},
		"subtype":        {"let inline g (t: ^T) : int when ^T :> System.IDisposable = 1\n", "when ^T :> System.IDisposable "},
		"two":            {"let inline g (t: ^T) : int when ^T: (static member Zero: ^T) and ^T: (static member One: ^T) = 1\n", "when ^T: (static member Zero: ^T) and ^T: (static member One: ^T) "},
		"no return":      {"let inline g (t: ^T) when ^T: (member Length: int) = 1\n", "when ^T: (member Length: int) "},
		"a member":       {"type X() =\n    member inline _.M(t: ^T) : int when ^T: (member Length: int) = 1\n", "when ^T: (member Length: int) "},
		"over two lines": {"let inline g (t: ^T) : int\n    when ^T: (member Length: int)\n    and ^T: struct =\n    1\n", "when ^T: (member Length: int)\n    and ^T: struct "},
	} {
		got := spanTexts(c[0], findFSharpConstraintCalls([]byte(c[0])))
		if len(got) != 1 || got[0] != c[1] {
			t.Errorf("%s: got %q, want %q", name, got, c[1])
		}
	}
}

func TestFSharpExternDeclarations(t *testing.T) {
	for name, c := range map[string]struct{ src, want string }{
		"one line":   {"[<DllImport(\"libc\")>]\nextern int getpid()\n", "let          getpid()=()"},
		"parameters": {"extern uint32 QueryFull(nativeint h, [<Out>] uint32& n)\nlet a = 1\n", "let                    QueryFull()=()"},
		"two lines":  {"    extern int kill(\n        int pid,\n        int signal)\n", ""},
	} {
		spans := findFSharpConstraintCalls([]byte(c.src))
		if len(spans) != 1 {
			t.Errorf("%s: %d spans", name, len(spans))
			continue
		}
		out := string(applyMaskSpans([]byte(c.src), spans))
		if len(out) != len(c.src) || strings.Count(out, "\n") != strings.Count(c.src, "\n") {
			t.Errorf("%s: the text changed shape: %q", name, out)
		}
		if strings.Contains(out, "extern") || !strings.Contains(out, "()=()") {
			t.Errorf("%s: not rewritten: %q", name, out)
		}
	}
	// Not declarations: the word inside a string, a comment, or mid-line.
	for _, src := range []string{"let s = \"extern int f()\"\n", "// extern int f()\n", "let extern = 1\n"} {
		if got := findFSharpConstraintCalls([]byte(src)); len(got) != 0 {
			t.Errorf("%q: %d spans", src, len(got))
		}
	}
}

// `= function` with its cases in the column of the `let` is the form the
// grammar loops on; cases indented further, and a `function` that is not the
// end of a binding line, are read properly and are left alone.
func TestFSharpUndentedFunctionCases(t *testing.T) {
	src := "let f a =\n    let (|One|_|) = function\n    | 1 -> Some ()\n    | _ -> None\n\n    a\n"
	spans := findFSharpConstraintCalls([]byte(src))
	if len(spans) != 1 {
		t.Fatalf("%d spans", len(spans))
	}
	out := string(applyMaskSpans([]byte(src), spans))
	want := "let f a =\n    let (|One|_|) =       ()\n                  \n               \n\n    a\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
	for name, s := range map[string]string{
		"indented cases":     "let f = function\n    | 1 -> 2\n    | _ -> 3\n",
		"a lambda in a call": "xs |> List.map (function\n| 1 -> 2\n| _ -> 3)\n",
		"text after":         "let f = function | 1 -> 2 | _ -> 3\n",
		"a string":           "let s = \"let f = function\\n| 1 -> 2\"\n",
	} {
		if got := findFSharpConstraintCalls([]byte(s)); len(got) != 0 {
			t.Errorf("%s: %d spans", name, len(got))
		}
	}
}

func TestFSharpAbstractTupleResult(t *testing.T) {
	src := "type I =\n    abstract Try: context: int -> int * int option\n    abstract Note: int -> int * int // two\n    abstract Ok: int * int -> int\n    abstract Paren: int -> (int * int)\n    abstract Curried: int -> int -> a * b\n"
	spans := findFSharpConstraintCalls([]byte(src))
	var got []string
	for _, s := range spans {
		got = append(got, src[s.start:s.start+len(s.repl)])
	}
	want := []string{"int * int option", "int * int", "a * b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
	out := string(applyMaskSpans([]byte(src), spans))
	if !strings.Contains(out, "-> _               \n") || len(out) != len(src) {
		t.Errorf("not rewritten as expected: %q", out)
	}
	for name, s := range map[string]string{
		"a string":  "let s = \"abstract Try: int -> int * int\"\n",
		"a comment": "// abstract Try: int -> int * int\n",
		"a member":  "type I =\n    member this.M: int -> int * int = x\n",
	} {
		if got := findFSharpConstraintCalls([]byte(s)); len(got) != 0 {
			t.Errorf("%s: %d spans", name, len(got))
		}
	}
}

func TestFSharpBareDocCommentsAreNormalised(t *testing.T) {
	src := "///\nlet a = 1\n    ///   \n    /// text\nlet s = \"///\"\n(* ///\n*)\n////\n///x\n"
	out := string(fsNormalizeBareDocComments([]byte(src)))
	want := "// \nlet a = 1\n    //    \n    /// text\nlet s = \"///\"\n(* ///\n*)\n////\n///x\n"
	if out != want || len(out) != len(src) {
		t.Errorf("got %q, want %q", out, want)
	}
	if got := fsNormalizeBareDocComments([]byte("/// a\nlet b = 2\n")); string(got) != "/// a\nlet b = 2\n" {
		t.Errorf("text changed: %q", got)
	}
}

// A mask that makes the parse worse is not kept: the guard compares the two
// trees, here a clean file against a broken one standing in for the masked
// text, and a broken file against a clean one.
func TestFSharpGuardJudgesTheMaskedParse(t *testing.T) {
	p := fsharpParser(t)
	good := "module M\n\nlet a x = x\n\nlet b x = x\n"
	bad := "module M\n\nlet a x = x\n\nlet b x = (^T: (member M: int) x\n"
	judge := func(plainSrc, maskedSrc string) bool {
		pt, _, err := p.parseOnce(t.Context(), []byte(plainSrc))
		if err != nil {
			t.Fatal(err)
		}
		mt, _, err := p.parseOnce(t.Context(), []byte(maskedSrc))
		if err != nil {
			t.Fatal(err)
		}
		ptags, _, _ := p.tagTree(pt, []byte(plainSrc))
		mtags, _, _ := p.tagTree(mt, []byte(maskedSrc))
		return maskGuard(pt.RootNode(), ptags, mt.RootNode(), mtags, map[string]bool{}, &maskContext{language: "fsharp"})
	}
	if judge(good, bad) {
		t.Error("the guard kept a worse masked parse")
	}
	if judge(good, good) {
		t.Error("the guard kept a mask that gains nothing")
	}
}

// A clean file has nothing to mask and is parsed once: no retry, the result
// untouched and without a note.
func TestFSharpCleanFileIsParsedOnceAndUntouched(t *testing.T) {
	src := "module M\n\nlet a = 1\n\nlet f x = x + a\n"
	if got := findFSharpConstraintCalls([]byte(src)); got != nil {
		t.Fatalf("spans in a file with none: %v", got)
	}
	p := fsharpParser(t)
	tree, stop, err := p.parseOnce(t.Context(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	tags, _, _ := p.tagTree(tree, []byte(src))
	if mp := p.retryMasked(t.Context(), tree, stop, []byte(src), tags); mp != nil {
		t.Fatal("a clean file was retried")
	}
	res, err := p.Parse(t.Context(), "m.fsx", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.ConstructsMasked != 0 || res.Status != StatusOK || res.Err != nil {
		t.Fatalf("masked=%d status=%s err=%v", res.ConstructsMasked, res.Status, res.Err)
	}
}

// A file whose only "calls" are in comments and strings parses clean and is
// not retried.
func TestFSharpCallInACommentOrStringIsNotRetried(t *testing.T) {
	p := fsharpParser(t)
	// The text of a call in a comment or a string is not a candidate at all.
	for _, src := range []string{
		"module M\n\nlet a = 1\n// (^T: (member M: unit -> int) x)\n",
		"module M\n\nlet a = \"(^T: (member M: unit -> int) x)\"\nlet b = 2\n",
	} {
		tree, stop, err := p.parseOnce(t.Context(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		tags, _, _ := p.tagTree(tree, []byte(src))
		if mp := p.retryMasked(t.Context(), tree, stop, []byte(src), tags); mp != nil {
			t.Errorf("retried a clean parse of %q", src)
		}
	}
}
