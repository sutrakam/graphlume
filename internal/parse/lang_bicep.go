package parse

// Bicep. What the index holds, see fixtures/langs/bicep/expected.tsv: every
// named declaration. param and output are a variable, var is a constant,
// resource and module are a class (each deploys a thing of its own), type is a
// type alias and func is a function. Properties inside a resource's object,
// loop variables and the values assigned by a .bicepparam file are not
// definitions. In a .bicepparam file `param x = 'v'` assigns a value to a
// parameter declared elsewhere; the grammar reads it as a parameter whose type
// is a missing token, and the empty type text is how the query leaves it out.
func init() {
	registerDefinitions("bicep", bicepDefinitions, true)
}

const bicepDefinitions = `
(parameter_declaration
  . (identifier) @name
  . (type) @type
  (#match? @type ".")) @definition.variable
(output_declaration . (identifier) @name) @definition.variable
(variable_declaration . (identifier) @name) @definition.constant
(resource_declaration . (identifier) @name) @definition.class
(module_declaration . (identifier) @name) @definition.class
(type_declaration . (identifier) @name) @definition.type_alias
(user_defined_function . (identifier) @name) @definition.function
`
