package parse

// GN. What the index holds, see fixtures/langs/gn/expected.tsv.
//
// The grammar has no node for a target. `executable("demo") { ... }` is two
// siblings, a call_expression with one string argument and then a block, so
// the query matches that pair.
//
//   - template("name") is a function;
//   - a target declaration, a call with a single literal string name followed
//     by a block (executable, static_library, source_set, group, config, action,
//     toolchain, pool and every user template), is a class, at any depth, so a
//     target inside an if block counts;
//   - a variable declared in declare_args() is a variable;
//   - a plain `name = value` assignment at the top level of the file is a
//     variable. += and -= are changes to a variable, not declarations.
//
// set_defaults("t") { } and tool("cc") { } have the same shape as a target and
// are not definitions. Calls whose name is not a literal string
// (source_set(target_name)) or interpolates one ("${target_name}_gen") belong
// to a template body and are skipped.
func init() {
	registerDefinitions("gn", gnDefinitions, true)
}

const gnDefinitions = `
(_
  (primary_expression
    (call_expression
      (identifier) @kw
      . (primary_expression (string (string_content) @name)) .)
    (#eq? @kw "template")
    (#not-match? @name "[$]")) @definition.function
  .
  (primary_expression (block)))

(_
  (primary_expression
    (call_expression
      (identifier) @kw
      . (primary_expression (string (string_content) @name)) .)
    (#not-match? @kw "^(template|set_defaults|tool|declare_args)$")
    (#not-match? @name "[$]")) @definition.class
  .
  (primary_expression (block)))

(_
  (primary_expression
    (call_expression (identifier) @kw)
    (#eq? @kw "declare_args"))
  .
  (primary_expression
    (block (assignment_statement (identifier) @name) @definition.variable)))

(source_file
  (assignment_statement (identifier) @name) @definition.variable
  (#match? @definition.variable "^[A-Za-z_][A-Za-z0-9_]*[ \t]*=[^=]"))
`
