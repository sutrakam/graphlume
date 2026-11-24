package parse

import "strings"

// BitBake. What the index holds, see fixtures/langs/bitbake/expected.tsv:
// every statement at the top level of a recipe, class or include file that
// declares something. A variable assignment (=, ?=, ??=, :=, +=, =+, .=, =.,
// with or without an override such as :append or :${PN}, and with `export`) is
// a variable named by the variable alone, so the `:append:rpi` suffix is not
// part of the name; the same variable assigned on several lines is several
// symbols. A shell function or task (`do_install() {}`, `fakeroot do_x() {}`),
// a Python task or function (`python do_check() {}`), a Python `def` and an
// `addtask` line are functions. Setting a flag of a variable
// (`do_check[depends] = "..."`, `PACKAGECONFIG[gl] = "..."`), inherit,
// include, require, EXPORT_FUNCTIONS, addhandler and the anonymous
// `python () {}` / `python __anonymous() {}` block are not definitions.
//
// The inferred query is replaced: the grammar has none that fits.
func init() {
	registerDefinitions("bitbake", bitbakeDefinitions, true)
	registerNameFix("bitbake", bitbakeName)
}

// bitbakeName reduces a captured name to the variable or function alone. The
// grammar's identifier stops at a slash, so PREFERRED_PROVIDER_virtual/libc is
// an identifier plus an error; a variable is therefore captured as the whole
// assignment and cut here at the first character that cannot be part of a
// name: white space, the operator characters = and ?, the override colon or a
// flag bracket. A trailing + or . left from += and .= written without a space
// is dropped.
func bitbakeName(name string) string {
	cut := strings.IndexAny(name, " \t\r\n=?:[")
	if cut < 0 {
		return name
	}
	return strings.TrimRight(name[:cut], "+.")
}

// A shell function the grammar failed on can come back as a variable_assignment
// whose text is `name() {`; the third pattern turns that into the function it
// is and the first two refuse it as a variable.
const bitbakeDefinitions = `
(recipe
  (variable_assignment . (identifier)) @name @definition.variable
  (#not-match? @definition.variable "^[^= \t\n\\[(]+[\\[(]"))
(recipe
  (export_statement
    (variable_assignment . (identifier)) @name @definition.variable
    (#not-match? @definition.variable "^[^= \t\n\\[(]+[\\[(]")))
(recipe
  (variable_assignment . (identifier) @name) @definition.function
  (#match? @definition.function "^[^= \t\n\\[(]+[ \t]*\\([ \t]*\\)[ \t]*\\{"))
(recipe (function_definition . (identifier) @name) @definition.function)
(recipe
  (anonymous_python_function . (identifier) @name) @definition.function
  (#not-eq? @name "__anonymous"))
(recipe (python_function_definition . (python_identifier) @name) @definition.function)
(recipe (addtask_statement . (identifier) @name) @definition.function)
`
