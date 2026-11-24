package parse

// VHDL. The inferred query captures interface_declaration, which is a port or
// generic of an entity, so it is replaced.
//
// What is a definition: design units (entity, architecture, configuration,
// package, package body), component declarations, subprogram bodies (nested
// ones included), labelled processes, types and subtypes, and constants and
// signals declared at package or architecture level. Subprogram prototypes in
// a package header are not listed: the body is the definition. Ports, generics,
// variables, locals, instantiation labels and aliases are not.
func init() {
	registerDefinitions("vhdl", `
(entity_declaration (identifier) @name) @definition.class
(architecture_definition (identifier) @name) @definition.class
(configuration_declaration (identifier) @name) @definition.class
(package_declaration (identifier) @name) @definition.module
(package_definition (identifier) @name) @definition.module
(component_declaration (identifier) @name) @definition.interface

(subprogram_definition (function_specification [(identifier) (operator_symbol)] @name)) @definition.function
(subprogram_definition (procedure_specification (identifier) @name)) @definition.function
(process_statement (label_declaration (label) @name)) @definition.function

(type_declaration (identifier) @name (enumeration_type_definition)) @definition.class
(type_declaration (identifier) @name (record_type_definition)) @definition.class
(type_declaration (identifier) @name (array_type_definition)) @definition.type_alias
(subtype_declaration (identifier) @name) @definition.type_alias

(architecture_head (constant_declaration (identifier_list (identifier) @name)) @definition.constant)
(package_declaration_body (constant_declaration (identifier_list (identifier) @name)) @definition.constant)
(package_definition_body (constant_declaration (identifier_list (identifier) @name)) @definition.constant)
(architecture_head (signal_declaration (identifier_list (identifier) @name)) @definition.variable)
(package_declaration_body (signal_declaration (identifier_list (identifier) @name)) @definition.variable)
`, true)
}
