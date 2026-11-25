package parse

// YAML. What the index holds, see fixtures/langs/yaml/expected.tsv: an anchor
// (&name) is a variable, named without the ampersand. An alias (*name) and a
// merge key (<<: *name) are uses of it and are not captured.
//
// Keys are deliberately not definitions. The configuration analyser
// (internal/analyze/config) reports every key of a .yaml file as a
// configuration symbol, the deployment analyser (internal/analyze/infra) reads
// compose and Kubernetes files, and a key such as name or version is in
// nearly every file: indexing keys would put thousands of identical names into
// every symbol lookup. An anchor is the one YAML construct that is declared
// once, in a file, and referred to by name.
func init() {
	registerDefinitions("yaml", yamlDefinitions, false)
}

const yamlDefinitions = `
(anchor (anchor_name) @name) @definition.variable
`
