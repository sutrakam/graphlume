package parse

// templ. What the index holds, see fixtures/langs/templ/expected.tsv: the
// declarations a .templ file makes at the top level. A `templ Name(...) {}`
// component is a function (a method when it has a receiver, `templ (v V) Name()`),
// and so are `css name() {}` and `script name() {}` components. The Go
// declarations that share the file are indexed as Go is: func is a function,
// a method is a method, a struct or defined type is a class, an interface is an
// interface and each method it declares a method, `type A = B` is a type
// alias, and package-level const and var are a constant and a variable.
// Locals, struct fields, imports and the package clause are not definitions.
//
// The inferred query is replaced. It is written for Go and, run on this
// grammar, takes the result type of a function (`string`) as a function and the
// underlying type of `type Money float64` (`float64`) as a class, and records a
// var declared inside a function body.
func init() {
	registerDefinitions("templ", templDefinitions, true)
}

const templDefinitions = `
(component_declaration . (component_identifier) @name) @definition.function
(component_declaration . (parameter_list) . (component_identifier) @name) @definition.method
(css_declaration . (css_identifier) @name) @definition.function
(script_declaration . (script_identifier) @name) @definition.function

(source_file (function_declaration . (identifier) @name) @definition.function)
(source_file (method_declaration (field_identifier) @name) @definition.method)

(source_file (type_declaration (type_spec . (type_identifier) @name . (struct_type)) @definition.class))
(source_file (type_declaration (type_spec . (type_identifier) @name . (interface_type)) @definition.interface))
(source_file (type_declaration (type_spec . (type_identifier) @name . (type_parameter_list) . (struct_type)) @definition.class))
(source_file (type_declaration (type_spec . (type_identifier) @name . (type_parameter_list) . (interface_type)) @definition.interface))
(source_file (type_declaration (type_alias . (type_identifier) @name) @definition.type_alias))
(source_file
  (type_declaration
    (type_spec . (type_identifier) @name . [(type_identifier) (qualified_type) (pointer_type) (slice_type) (array_type) (map_type) (channel_type) (function_type) (generic_type)]) @definition.class))
(source_file
  (type_declaration
    (type_spec (interface_type (method_elem (field_identifier) @name) @definition.method))))

(source_file (const_declaration (const_spec (identifier) @name) @definition.constant))
(source_file (var_declaration (var_spec (identifier) @name) @definition.variable))
(source_file (var_declaration (var_spec_list (var_spec (identifier) @name) @definition.variable)))
`
