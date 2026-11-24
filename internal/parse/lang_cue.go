package parse

// CUE. What the index holds, see fixtures/langs/cue/expected.tsv:
//
//   - the package clause is a package;
//   - a definition (#Name: ...), at any depth, is a class;
//   - a top-level field whose label is an identifier (name: ..., _hidden: ...)
//     is a variable, and a top-level let clause is a constant.
//
// Fields nested in a struct, optional and pattern constraints ([string]: _,
// name?: ...), quoted labels, and comprehensions are not definitions.
func init() {
	registerDefinitions("cue", cueDefinitions, true)
}

const cueDefinitions = `
(package_clause (package_identifier) @name) @definition.package

(field
  . (label . (identifier) @name)
  (#match? @name "^#.")) @definition.class

(source_file
  (field
    . (label . (identifier) @name)
    (#not-match? @name "^#")) @definition.variable)

(source_file
  (let_clause . (identifier) @name) @definition.constant)
`
