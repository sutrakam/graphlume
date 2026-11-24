package parse

// C++: the C declarations plus classes, enums and out-of-line members.
//
// The query the library infers is replaced for the reasons lang_c.go gives, and
// for three more:
//
//   - `class Cache;` and `struct Foo;` are forward declarations. They name a
//     type and define nothing, and a symbol for one is where an inheritance
//     edge ends instead of at the definition. A class or struct is a
//     definition only when it has a body (the supplement in queries.go says
//     so for both).
//   - A member defined outside its class, `void Table::Open(int) { }`, has a
//     qualified_identifier for a name and matched nothing. It is recorded as a
//     method named by its last segment; shape.go reads the owner from the
//     source (`Table.Open`), and a namespace in front (`ns::Table::Open`) does
//     not change either.
//   - The same for a function returning a pointer or a reference, which has a
//     pointer_declarator or reference_declarator in between.
func init() {
	registerDefinitions("cpp", `
(function_definition (function_declarator (identifier) @name) body: (_) @_body (#not-match? @_body "^=")) @definition.function
(function_definition (function_declarator (field_identifier) @name) body: (_) @_body (#not-match? @_body "^=")) @definition.function
(function_definition [
  (pointer_declarator (function_declarator [(identifier) (field_identifier)] @name))
  (pointer_declarator (pointer_declarator (function_declarator [(identifier) (field_identifier)] @name)))
  (reference_declarator (function_declarator [(identifier) (field_identifier)] @name))
] body: (_) @_body (#not-match? @_body "^=")) @definition.function

(function_definition [
  (function_declarator [(destructor_name) (operator_name)] @name)
  (pointer_declarator (function_declarator (operator_name) @name))
  (pointer_declarator (pointer_declarator (function_declarator (operator_name) @name)))
  (reference_declarator (function_declarator (operator_name) @name))
] body: (_) @_body (#not-match? @_body "^=")) @definition.method

(function_definition [
  (function_declarator (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .))
  (function_declarator (qualified_identifier (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .) .))
  (function_declarator (qualified_identifier (qualified_identifier (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .) .) .))
  (pointer_declarator (function_declarator (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .)))
  (pointer_declarator (function_declarator (qualified_identifier (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .) .)))
  (pointer_declarator (function_declarator (qualified_identifier (qualified_identifier (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .) .) .)))
  (reference_declarator (function_declarator (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .)))
  (reference_declarator (function_declarator (qualified_identifier (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .) .)))
  (reference_declarator (function_declarator (qualified_identifier (qualified_identifier (qualified_identifier [(identifier) (destructor_name) (operator_name)] @name .) .) .)))
] body: (_) @_body (#not-match? @_body "^=")) @definition.method

(type_definition (type_identifier) @name .) @definition.type
(type_definition (function_declarator (parenthesized_declarator (pointer_declarator (type_identifier) @name)))) @definition.type
(type_definition (pointer_declarator (function_declarator (parenthesized_declarator (pointer_declarator (type_identifier) @name))))) @definition.type

(function_definition (function_declarator (function_declarator (identifier) @name)) body: (_) @_body (#not-match? @_body "^=")) @definition.function

(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call

(declaration type: (type_identifier) @name @reference.call.new (init_declarator declarator: (identifier) value: (argument_list)))
(declaration type: (type_identifier) @name @reference.call.new (init_declarator declarator: (identifier) value: (initializer_list)))
(declaration type: (template_type name: (type_identifier) @name) @reference.call.new (init_declarator declarator: (identifier) value: (argument_list)))
(declaration type: (template_type name: (type_identifier) @name) @reference.call.new (init_declarator declarator: (identifier) value: (initializer_list)))
(declaration type: (qualified_identifier scope: (namespace_identifier) @_s name: (type_identifier) @name) @reference.call.new (init_declarator declarator: (identifier) value: (argument_list)) (#not-eq? @_s "std"))
(declaration type: (qualified_identifier scope: (namespace_identifier) @_s name: (type_identifier) @name) @reference.call.new (init_declarator declarator: (identifier) value: (initializer_list)) (#not-eq? @_s "std"))
(compound_statement (declaration type: (type_identifier) @name @reference.call.new (function_declarator declarator: (identifier) parameters: (parameter_list . (parameter_declaration type: (type_identifier) !declarator) .))))
(compound_statement (declaration type: (type_identifier) @name @reference.call.new (function_declarator declarator: (identifier) parameters: (parameter_list . (parameter_declaration type: (type_identifier) !declarator) (parameter_declaration type: (type_identifier) !declarator) .))))
(compound_statement (declaration type: (type_identifier) @name @reference.call.new (function_declarator declarator: (identifier) parameters: (parameter_list . (parameter_declaration type: (type_identifier) !declarator) (parameter_declaration type: (type_identifier) !declarator) (parameter_declaration type: (type_identifier) !declarator) .))))
(compound_statement (declaration type: (type_identifier) @name @reference.call.new (function_declarator declarator: (identifier) parameters: (parameter_list . (parameter_declaration type: (type_identifier) !declarator) (parameter_declaration type: (type_identifier) !declarator) (parameter_declaration type: (type_identifier) !declarator) (parameter_declaration type: (type_identifier) !declarator) .))))
(declaration type: (qualified_identifier scope: (namespace_identifier) @_s name: (template_type name: (type_identifier) @name)) @reference.call.new (init_declarator declarator: (identifier) value: (argument_list)) (#not-eq? @_s "std"))
`, true)
}
