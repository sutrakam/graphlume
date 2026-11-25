package parse

import (
	"context"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"

	"github.com/odvcencio/gotreesitter/grammars"
)

// The work limits scale with the length of the file (30 iterations and 52
// nodes per byte), so a very small file would get a budget too small to parse
// anything unless the limits have a floor. They do: gotreesitter takes the
// larger of the scaled figure and 10,000 iterations and 300,000 nodes.
//
// Is that floor sound? What the smallest valid input of the grammar with the
// largest start-up cost needs was measured over all 206 bundled grammars and
// five tiny inputs each (a name, an assignment, an empty block, a newline, a
// line of hash signs): the worst was the `just` grammar, 4,102 iterations and
// 4,103 nodes for the six bytes `x = 1\n`; every other grammar needed under
// 30 iterations. So the floor is 2.4 times the worst start-up cost in
// iterations and 73 times in nodes, and no file is stopped by it.
//
// Two 17-byte files of rack, `### TestFile ###`, were recorded as incomplete,
// which made the whole index degraded. The budget was not the reason: the
// parse used 2 of its 10,000 iterations. The JavaScript grammar's recovery
// accepted a prefix of the file and left the rest outside the root, and that
// was recorded as a stop before the end of the file although every token had
// been read.
func TestTinyFilesParseToCompletion(t *testing.T) {
	pad := func(unit string, n int) string {
		var b strings.Builder
		for b.Len()+len(unit) <= n {
			b.WriteString(unit)
		}
		for b.Len() < n {
			b.WriteByte('\n')
		}
		return b.String()
	}
	cases := map[string]map[int]string{
		"javascript": {1: "x", 17: "function f() {}\n\n", 200: pad("var v = 1;\n", 200)},
		"c":          {1: ";", 17: "int f(void) {}\n\n\n", 200: pad("int a = 1;\n", 200)},
		"python":     {1: "x", 17: "def f():\n  pass\n\n", 200: pad("a = 1\n", 200)},
		"go":         {1: "x", 17: "package p" + strings.Repeat("\n", 8), 200: "package p\n" + pad("var a = 1\n", 190)},
	}
	reg := NewRegistry()
	for lang, bySize := range cases {
		for size, src := range bySize {
			if len(src) != size {
				t.Fatalf("%s: the test source for %d bytes is %d bytes", lang, size, len(src))
			}
			res, err := reg.ForLanguage(lang).Parse(context.Background(), "tiny", []byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if res.Reason == model.ParseReasonBudgetExceeded || res.Reason == model.ParseReasonStoppedEarly || res.Status == StatusFailed {
				t.Errorf("%s, %d bytes: %s (%v): a file this small must be read to the end", lang, size, res.Status, res.Err)
			}
			if size > 1 && lang != "go" && res.Status != StatusOK {
				t.Errorf("%s, %d bytes: status %s (%v), want ok", lang, size, res.Status, res.Err)
			}
		}
	}
}

// A file the grammar cannot make sense of, and that the parser reads to the
// end, is a syntax error, not an incomplete parse.
func TestUnreadableTinyFileIsASyntaxErrorNotAnIncompleteParse(t *testing.T) {
	res, err := NewRegistry().ForLanguage("javascript").Parse(context.Background(), "test.js", []byte("### TestFile ###\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPartial || res.Reason != model.ParseReasonSyntaxErrors {
		t.Fatalf("status %s, reason %q (%v), want partial syntax_errors", res.Status, res.Reason, res.Err)
	}
	if model.ParseAbandoned(string(res.Status), res.Err.Error()) {
		t.Errorf("the file counts as abandoned, which makes the index degraded: %v", res.Err)
	}
}

// Every bundled grammar reads a handful of tiny inputs to the end. This is
// what the floor of the limits is for, checked on all of them.
func TestNoGrammarStopsEarlyOnATinyInput(t *testing.T) {
	if testing.Short() {
		t.Skip("loads every bundled grammar")
	}
	for _, e := range grammars.AllLanguages() {
		entry := e
		p := newGenericParser(entry, defaultParserConfig)
		if p.lang == nil {
			continue
		}
		for _, src := range []string{"a", "x = 1\n", "{}", "\n", "### TestFile ###\n"} {
			_, _, stop, err := p.parseTree(context.Background(), []byte(src))
			if err != nil {
				continue
			}
			if stop.early {
				t.Errorf("%s stopped early on %q: %+v", entry.Name, src, stop)
			}
		}
	}
}
