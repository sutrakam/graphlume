package parse

// TLA+. The inferred query names function_definition wherever it occurs,
// including inside LET, and knows nothing else, so it is replaced.
//
// What is a definition: the module (module), operator and function definitions
// made at module level, with or without LOCAL (function; an infix, prefix or
// postfix operator is named by its symbol), INSTANCE bindings (module),
// CONSTANT and VARIABLE declarations (constant, variable; a constant that is an
// operator, `CONSTANT F(_)`, counts), and named ASSUME, THEOREM and LEMMA
// statements (constant: a named proposition). Operators defined in a LET,
// bound variables, unnamed theorems and RECURSIVE declarations are not.
func init() {
	registerDefinitions("tlaplus", `
(module . (header_line) . (identifier) @name) @definition.module

(module (operator_definition . (identifier) @name . [(identifier) (operator_declaration) (def_eq)]) @definition.function)
(module (operator_definition (infix_op_symbol) @name) @definition.function)
(module (operator_definition . (prefix_op_symbol) @name) @definition.function)
(module (operator_definition (postfix_op_symbol) @name) @definition.function)
(module (function_definition . (identifier) @name) @definition.function)
(module (module_definition . (identifier) @name) @definition.module)

(local_definition (operator_definition . (identifier) @name . [(identifier) (operator_declaration) (def_eq)]) @definition.function)
(local_definition (operator_definition (infix_op_symbol) @name) @definition.function)
(local_definition (operator_definition . (prefix_op_symbol) @name) @definition.function)
(local_definition (operator_definition (postfix_op_symbol) @name) @definition.function)
(local_definition (function_definition . (identifier) @name) @definition.function)
(local_definition (module_definition . (identifier) @name) @definition.module)

(constant_declaration (identifier) @name) @definition.constant
(constant_declaration (operator_declaration . (identifier) @name)) @definition.constant
(variable_declaration (identifier) @name) @definition.variable

(assumption (identifier) @name) @definition.constant
(theorem (identifier) @name) @definition.constant
`, true)
}
