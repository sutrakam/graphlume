package parse

// Gleam: functions, constants, custom types and type aliases. Type
// constructors (the variants of a custom type) are not definitions of their
// own here; the type is.
func init() {
	registerDefinitions("gleam", `
(function name: (identifier) @name) @definition.function
(constant name: (identifier) @name) @definition.constant
(type_definition (type_name name: (type_identifier) @name)) @definition.class
(external_type (type_name name: (type_identifier) @name)) @definition.class
(type_alias (type_name name: (type_identifier) @name)) @definition.type_alias
`, true)
}
