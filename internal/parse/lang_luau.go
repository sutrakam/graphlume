package parse

// Luau. Lua's declarations (see lang_lua.go) plus type aliases.
//
// The inferred query reads the return type of a function as its name
// (`function Tree.Find(p: Instance): Instance` produced a function Instance
// and no Find), misses `function M.f` and `function M:m`, and records an
// alias as a class, so it is replaced.
//
//   - function f, local function f, `local f = function` are functions, at any
//     depth; function M.f is a function named f; function M:m is a method;
//   - `M.f = function` and `f = function` at the top level are functions, and
//     so are function-valued fields of an assigned or returned table (up to
//     three levels deep, key a name or a string);
//   - a top-level `local T = {}` or global `T = {}` is a variable;
//   - `type T = ...` and `export type T<U> = ...` are type_alias.
//
// The grammar cannot read a generic parameter with a default
// (`type T<U = string> = U`); see the fixture's KNOWN_GAPS.md.
func init() {
	registerDefinitions("luau", luauDefinitions, true)
}

const luauDefinitions = `
(function_declaration . (identifier) @name) @definition.function
(function_declaration . (dot_index_expression (identifier) @name .)) @definition.function
(function_declaration . (method_index_expression (identifier) @name .)) @definition.method

(variable_declaration
  (assignment_statement
    (variable_list . (identifier) @name)
    (expression_list . (function_definition))) @definition.function)
(chunk
  (assignment_statement
    (variable_list . (identifier) @name)
    (expression_list . (function_definition))) @definition.function)
(chunk
  (assignment_statement
    (variable_list . (dot_index_expression (identifier) @name .))
    (expression_list . (function_definition))) @definition.function)

(chunk
  (assignment_statement
    (variable_list . (identifier) @name)
    (expression_list . (table_constructor))) @definition.variable)
(chunk
  (variable_declaration
    (assignment_statement
      (variable_list . (identifier) @name)
      (expression_list . (table_constructor)))) @definition.variable)

(assignment_statement
  (expression_list
    (table_constructor
      (field . [(identifier) @name (string (string_content) @name)] . (function_definition)) @definition.function)))
(assignment_statement
  (expression_list
    (table_constructor
      (field (table_constructor
        (field . [(identifier) @name (string (string_content) @name)] . (function_definition)) @definition.function)))))
(assignment_statement
  (expression_list
    (table_constructor
      (field (table_constructor
        (field (table_constructor
        (field . [(identifier) @name (string (string_content) @name)] . (function_definition)) @definition.function)))))))
(return_statement
  (expression_list
    (table_constructor
      (field . [(identifier) @name (string (string_content) @name)] . (function_definition)) @definition.function)))
(return_statement
  (expression_list
    (table_constructor
      (field (table_constructor
        (field . [(identifier) @name (string (string_content) @name)] . (function_definition)) @definition.function)))))

(type_definition . (identifier) @name) @definition.type_alias
(type_definition . (generic_type . (identifier) @name)) @definition.type_alias
`
