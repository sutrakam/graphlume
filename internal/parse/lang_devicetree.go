package parse

// Devicetree. What the index holds, see fixtures/langs/devicetree/expected.tsv:
// every node block at any depth is a class named by its node name (without the
// unit address, so memory@80000000 is "memory"), and every label on a node is a
// variable, since a label is what other nodes refer to with &label. The root
// node "/", &label { } overrides, /delete-node/ and properties are not
// definitions.
func init() {
	registerDefinitions("devicetree", devicetreeDefinitions, true)
}

const devicetreeDefinitions = `
(node
  name: (identifier) @name
  (#not-eq? @name "/")) @definition.class

(node label: (identifier) @name) @definition.variable
`
