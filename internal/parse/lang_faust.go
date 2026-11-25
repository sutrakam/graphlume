package parse

// Faust. What the index holds, see fixtures/langs/faust/expected.tsv: every
// definition made at the top level of the file. `name(args) = expr;` is a
// function, each clause of a pattern-matching definition counts, `process` is a
// function (the program's entry point), `name = library("x.lib");` and
// `name = environment { ... };` are a package (a namespace of definitions) and
// any other `name = expr;` is a constant. declare and import are not
// definitions, nor is a definition inside a `with`, `letrec` or `environment`
// block, which belongs to the enclosing definition.
//
// The inferred query is replaced: it is built for the node names of other
// grammars and does not see Faust's definition node at all.
func init() {
	registerDefinitions("faust", faustDefinitions, true)
}

const faustDefinitions = `
(program (function_definition . (identifier) @name) @definition.function)

(program
  (definition . (identifier) @name) @definition.function
  (#eq? @name "process"))

(program
  (definition . (identifier) @name . [(library) (environment)]) @definition.package)

(program
  (definition . (identifier) @name) @definition.constant
  (#not-eq? @name "process")
  (#not-match? @definition.constant "^[^=]*=[ \t\r\n]*(library|environment)[ \t\r\n({]"))
`
