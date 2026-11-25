package parse

// Hare. What the index holds, see fixtures/langs/hare/expected.tsv.
//
// The inferred query takes any identifier that is a direct child of a
// function, which includes a return type written as a named type
// (`fn dup(e: *dirent) dirent`), and it knows no type, def or global, so it is
// replaced.
//
//   - fn is a function, with or without @test, @init, @fini or @symbol
//     attributes and with or without a body (an @symbol declaration);
//   - type NAME = struct, union or enum is a class; every other type
//     (an alias such as `int`, a tuple, a tagged union, a function type, a
//     pointer, an error type `!void`) is a type_alias;
//   - def NAME is a constant, a top-level const is a constant, a top-level let
//     is a variable. Locals inside a function body are not definitions;
//   - fields and enum members are not definitions.
//
// An export keyword is not part of the tree and does not change the result.
func init() {
	registerDefinitions("hare", hareDefinitions, true)
}

const hareDefinitions = `
(function_declaration . (identifier) @name) @definition.function
(function_declaration (function_attribute) . (identifier) @name) @definition.function

(type_declaration . (identifier) @name . [(struct_type) (union_type) (enum_type)]) @definition.class
(type_declaration . (identifier) @name . [
  (builtin_type) (identifier) (tuple_type) (tagged_union_type) (function_type)
  (pointer_type) (error_type) (array_type) (slice_type) (const_type)
  (scoped_type_identifier)]) @definition.type_alias

(constant_declaration . (identifier) @name) @definition.constant

(declarations
  (global_declaration "let" (global_binding . (identifier) @name)) @definition.variable)
(declarations
  (global_declaration "const" (global_binding . (identifier) @name)) @definition.constant)
`
