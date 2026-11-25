package parse

import "strings"

// Odin declares everything with `name :: value` or `name: type`. The grammar
// gives each form its own node, and nests procedures, structs and constants
// inside procedure bodies too, so every pattern is anchored to a place where
// the declaration is package-level: the source file, a `foreign` block, or a
// `when` / `else` block that sits directly in the source file.
func init() {
	// Each entry is the node holding the declarations, as a pattern with %s
	// where one declaration pattern goes.
	scopes := []string{
		"(source_file %s)",
		"(source_file (ERROR %s))",
		"(source_file (foreign_block (block %s)))",
		"(source_file (when_statement (block %s)))",
		"(source_file (when_statement (else_clause (block %s))))",
	}
	decls := []string{
		"(procedure_declaration . (identifier) @name) @definition.function",
		"(overloaded_procedure_declaration . (identifier) @name) @definition.function",
		"(struct_declaration . (identifier) @name) @definition.class",
		"(enum_declaration . (identifier) @name) @definition.class",
		"(union_declaration . (identifier) @name) @definition.class",
		"(bit_field_declaration . (identifier) @name) @definition.class",
		"(const_declaration . (identifier) @name (distinct_type)) @definition.class",
		"(const_declaration . (identifier) @name (bit_set_type)) @definition.class",
		"(const_declaration . (identifier) @name) @definition.constant",
		"(var_declaration . (identifier) @name) @definition.variable",
		"(variable_declaration . (identifier) @name) @definition.variable",
	}
	var b strings.Builder
	for _, scope := range scopes {
		for _, d := range decls {
			// A declaration may carry attributes (`@(private)`), which
			// are a node of their own in front of the name.
			for _, pat := range []string{d, strings.Replace(d, ". (identifier)", "(attributes) . (identifier)", 1)} {
				b.WriteString(strings.Replace(scope, "%s", pat, 1))
				b.WriteByte('\n')
			}
		}
	}
	registerDefinitions("odin", b.String(), true)
}
