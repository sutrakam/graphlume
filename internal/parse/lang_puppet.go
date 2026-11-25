package parse

// Puppet. What the index holds, see fixtures/langs/puppet/expected.tsv: a
// class is a class, a defined type (define) is a class, a node block is a
// class named by its quoted host name or "default", a Puppet-language function
// is a function and a type alias is a type_alias. The grammar reads a
// class { 'x': } resource declaration as a class definition whose name is the
// whole block, so a name with anything but identifier characters is dropped.
// Resource declarations
// (package { 'x': }, class { 'x': }), variables, parameters and include
// statements are uses, not definitions.
func init() {
	registerDefinitions("puppet", puppetDefinitions, true)
}

const puppetDefinitions = `
(class_definition
  . [(identifier) (class_identifier)] @name
  (#match? @name "^[A-Za-z0-9_:]+$")) @definition.class
(defined_resource_type . [(identifier) (class_identifier)] @name) @definition.class
(node_definition (node_name (string (string_content) @name))) @definition.class
(node_definition (node_name (default) @name)) @definition.class
(function_declaration . [(identifier) (class_identifier)] @name) @definition.function
(type_declaration . [(identifier) (class_identifier)] @name) @definition.type_alias
`
