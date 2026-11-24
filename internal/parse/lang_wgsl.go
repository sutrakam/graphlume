package parse

// WGSL. What the index holds, see fixtures/langs/wgsl/expected.tsv: every
// module-scope declaration. fn is a function, struct is a class, `alias` is a
// type alias, `const`, `let` and `override` are a constant and a module-scope
// `var` (with or without @group/@binding and an address space) is a variable.
// Struct members, parameters, locals and enable/requires directives are not
// definitions.
//
// The bundled grammar predates `alias` and `const`: it reads those two as an
// ERROR node holding two identifiers, the keyword and the name, so they are
// matched there by the keyword's text. A declaration that starts with
// attributes (@vertex fn ...) is on the fn line.
func init() {
	registerDefinitions("wgsl", wgslDefinitions, true)
}

const wgslDefinitions = `
(function_declaration (identifier) @name) @definition.function
(struct_declaration . (identifier) @name) @definition.class
(global_constant_declaration "override" . (identifier) @name) @definition.constant
(global_constant_declaration "override" (variable_identifier_declaration . (identifier) @name)) @definition.constant
(global_variable_declaration (variable_declaration (variable_identifier_declaration . (identifier) @name))) @definition.variable
(source_file (ERROR . (identifier) @kw . (identifier) @name (#eq? @kw "alias")) @definition.type_alias)
(source_file (ERROR . (identifier) @kw . (identifier) @name (#eq? @kw "const")) @definition.constant)
`
