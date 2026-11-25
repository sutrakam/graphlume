package parse

import "strings"

// Make. What the index holds, see fixtures/langs/make/expected.tsv:
//
//   - a rule's literal target names are a function (`build`, `test`, `lib.a`),
//     one per name in a multi-target rule;
//   - `define NAME ... endef` is a function (a callable macro);
//   - a variable assigned with `=`, `:=`, `::=`, `?=` or `!=`, also after
//     `export` or `override` and inside a conditional, is a variable.
//
// A `+=` appends to a variable that is defined elsewhere, so it is not
// captured: a variable is recorded where it is first assigned, not at every
// append. Make's own settings (SHELL, MAKEFLAGS, MAKEFILES, VPATH) and every
// name that begins with a dot (.DEFAULT_GOAL, and the BSD directives .if,
// .else, .endif and .include, which the grammar reads as assignments) are
// configuration of make, not names of the project, and are not captured. A
// target-specific assignment (`app: CFLAGS = -O0`) is scoped to that target
// and is not captured either.
//
// Not captured: prerequisites; pattern rules (a target containing %), suffix
// rules and special targets such as .PHONY (a target starting with a dot); a
// name that begins with - @ or + or contains a backslash (a recipe line of a
// non-GNU makefile indented with spaces, which the grammar reads as a rule); a
// target spelled with a variable reference ($(BUILD)/out); include and vpath
// directives; conditionals.
func init() {
	registerDefinitions("make", makeDefinitions+makeRecovered(), false)
	registerNameFix("make", makeName)
}

// makeName drops an assignment operator the grammar lexes into the variable
// name when there is no space before it: `CC?=gcc` names the word "CC?".
func makeName(name string) string {
	return strings.TrimRight(name, "?:+!")
}

const makeDefinitions = `
(rule
  (targets (word) @name)
  (#not-match? @name "^\\.")
  (#not-match? @name "%")
  (#not-match? @name "^[-@+]|\\\\")) @definition.function

(define_directive (word) @name) @definition.function

((variable_assignment . (word) @name) @definition.variable
  (#not-match? @name "^(SHELL|MAKEFLAGS|MAKEFILES|VPATH|\\..*)$")
  (#not-match? @definition.variable "^[^=:]*\\+="))

((shell_assignment . (word) @name) @definition.variable
  (#not-match? @name "^(SHELL|MAKEFLAGS|MAKEFILES|VPATH|\\..*)$"))
`

// The grammar cannot read a substitution reference whose replacement is empty
// ($(BOARD_DIR:/=), $(OBJ:.o=)), and it then gives up on the rest of the file:
// no rule or assignment node is built after the bad line, only a left-nested
// pile of concatenation nodes whose children are bare words, with each
// operator (=, :=, :) as an ERROR node between two words. A pattern anchored
// on variable_assignment or rule finds nothing there, and the file would yield
// no symbol at all.
//
// makeRecovered reads that pile. A word followed by an operator error node is
// the name on its left. The word is either the previous sibling of the
// operator or the last child of the previous sibling (a nested concatenation),
// and `NAME ?= v` is two words (NAME, ?) before the error node for `=`. A word
// that follows an error node is the tail of a flag (-Wl,--defsym=x), not the
// start of a line, so it is skipped. What this cannot see: a name that is not
// the word just before the operator (the first of two targets of one rule, a
// name after a variable reference such as $(BUILD)/x), and a line whose tokens
// the grammar dropped altogether (a line holding a '"quoted"' string loses its
// tokens). Those are left out, not guessed.
func makeRecovered() string {
	const prev = `[(word) (variable_reference) (function_call) (archive) (comment) (text) (string) (shell_function) (list) (concatenation) (ifdef_directive)]`
	const varChecks = `(#match? @name "^[A-Za-z_]") (#not-match? @name "[+]$") (#not-match? @name "^(SHELL|MAKEFLAGS|MAKEFILES|VPATH)$")`
	const targetChecks = `(#not-match? @name "^[-@+/.$]|%|\\\\")`
	const assign = `(#match? @op "^(:|::|\\?|!)?=")`

	// where the name word sits relative to the operator's error node, and what
	// precedes it: nothing (it opens the run), a node that ends a line, or an
	// error node that is not the start of a flag (-Wl,--defsym=x).
	type place struct{ open, close, extra string }
	var places []place
	for _, p := range []struct{ pred, extra string }{
		{prev + " . ", ""},
		{"(ERROR) @prev . ", `(#not-match? @prev "^=$|[-,]$")`},
		{". ", ""},
	} {
		for _, parent := range []string{"concatenation", "ERROR"} {
			places = append(places,
				place{"(" + parent + " " + p.pred, "", p.extra},
				place{"(" + parent + " (concatenation " + p.pred, ")", p.extra})
		}
	}
	var b strings.Builder
	for _, pl := range places {
		tail := ""
		if pl.close != "" {
			tail = " ."
		}
		// NAME = v, NAME := v, NAME ::= v, NAME != v, and NAME: deps
		for _, e := range []struct{ kind, op, checks string }{
			{"variable", assign, varChecks},
			{"function", `(#match? @op "^::?($|[^=:])")`, targetChecks},
		} {
			b.WriteString(pl.open + "(word) @name @definition." + e.kind + tail + pl.close + " . (ERROR) @op " + e.op + " " + e.checks + " " + pl.extra + ")\n")
		}
		// NAME ?= v
		b.WriteString(pl.open + "(word) @name @definition.variable . (word) @q" + tail + pl.close +
			" . (ERROR) @op (#match? @q \"^[?]$\") (#match? @op \"^=\") " + varChecks + " " + pl.extra + ")\n")
	}
	return b.String() + `
(_
  (word) @kw . (word) @name @definition.function
  (#eq? @kw "define"))

(ERROR
  (word) @name @definition.variable . ":" . "="
  (#match? @name "^[A-Za-z_]")
  (#not-match? @name "^(SHELL|MAKEFLAGS|MAKEFILES|VPATH)$"))

(ERROR
  (word) @name @definition.variable . [":=" "::=" "?=" "!="]
  (#match? @name "^[A-Za-z_]")
  (#not-match? @name "^(SHELL|MAKEFLAGS|MAKEFILES|VPATH)$"))
`
}
