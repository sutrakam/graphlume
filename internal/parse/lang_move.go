package parse

// Move. What the index holds, see fixtures/langs/move/expected.tsv: a module is
// a package, a struct and an enum are a class, a const is a constant and every
// fun is a function (public, public(friend), entry, native, and the ones
// inside a script). `spec fun` is a function; a `spec name { ... }` block
// states conditions on a function declared elsewhere and is not a definition.
// Struct fields, locals, use declarations and calls are not definitions.
//
// The bundled grammar cannot read the Move 2024 module label,
// `module a::b;`, which most Sui code uses: the rest of the file becomes a flat
// ERROR node of identifiers in which the keywords `fun`, `struct`, `enum` and
// `const` are identifiers too. The ERROR patterns below take the identifier
// that follows one of those words. They are reserved words in Move, so the
// text is exact, and the name itself is the captured node, so the line is the
// name's. See KNOWN_GAPS.md for what they do not recover.
func init() {
	registerDefinitions("move", moveDefinitions, true)
}

const moveDefinitions = `
(module_declaration . (identifier) @name) @definition.module
(module_declaration . (module_identity (identifier) @name .)) @definition.module
(ERROR . (module_identity (identifier) @name .) @definition.module)
(struct_declaration . (identifier) @name) @definition.class
(enum_declaration . (identifier) @name) @definition.class
(constant_declaration . (identifier) @name) @definition.constant
(function_declaration (identifier) @name) @definition.function
(usual_spec_function . (identifier) @name) @definition.function

(ERROR "fun" . (identifier) @name @definition.function)
(ERROR ["struct" "enum"] . (identifier) @name @definition.class)
(ERROR "const" . (identifier) @name @definition.constant)
`
