package parse

// Teal. What the index holds, see fixtures/langs/teal/expected.tsv.
//
// The inferred query knows enums and interfaces and nothing else: no record,
// no function, no type alias. It also records an enum as a class-less "type".
// It is replaced.
//
//   - record, enum and interface declarations are classes (a record, an
//     enum) and interfaces, including the ones nested inside a record body;
//   - `type Name = ...` is a type_alias;
//   - function f, local function f and global function f are functions;
//     function M.f and function M.Inner.f are functions named f; function M:m
//     is a method named m;
//   - `local f = function ...` and `global f = function ...` are functions;
//   - a top-level `local NAME <const> = value` is a constant, unless the value
//     is a require() call (an import, not a definition), and a top-level
//     local or global initialised with a table constructor is a variable;
//   - fields, plain locals, forward declarations (`global x: integer`) and
//     require() imports are not definitions.
func init() {
	registerDefinitions("teal", tealDefinitions, true)
}

const tealDefinitions = `
(record_declaration . (identifier) @name) @definition.class
(enum_declaration . (identifier) @name) @definition.class
(interface_declaration . (identifier) @name) @definition.interface
(type_declaration . (identifier) @name) @definition.type_alias
(typedef . (identifier) @name) @definition.type_alias

(function_statement . (identifier) @name) @definition.function
(function_statement . (function_name (identifier) @name .) @fn
  (#not-match? @fn ":")) @definition.function
(function_statement . (function_name (identifier) @name .) @fn
  (#match? @fn ":")) @definition.method

(var_declaration
  (var_declarators . (var . (identifier) @name))
  (expressions . (anon_function))) @definition.function

(program
  (var_declaration
    (var_declarators . (var . (identifier) @name (attribute)))
    (expressions . [
      (number) (string) (boolean) (nil) (unary_op) (bin_op) (table_constructor)
      (function_call)] @value)
    (#not-match? @value "^require")) @definition.constant)

(program
  (var_declaration
    (var_declarators . (var . (identifier) @name))
    (expressions . (table_constructor))) @definition.variable)
`
