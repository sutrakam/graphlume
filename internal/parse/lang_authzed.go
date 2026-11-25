package parse

import "strings"

// Authzed (the SpiceDB schema language). The inferred query only finds call
// expressions, so a schema yields no definitions; this adds to it.
//
// What is a definition: each `definition` (class), each `caveat` (function:
// it is a named, parameterised expression), and inside a definition each
// `relation` (field) and each `permission` (method: a computed relation).
// Subject types, `with` caveat references and names used inside permission
// expressions are uses, not definitions.
//
// The grammar lexes the name in `relation admin: user` together with the
// colon, giving "admin:". The name fix below removes it.
func init() {
	registerDefinitions("authzed", `
(definition . (definition_literal) . (identifier) @name) @definition.class
(caveat . (caveat_literal) . (identifier) @name) @definition.function
(relation . (relation_literal) . (identifier) @name) @definition.field
(permission . (permission_literal) . (identifier) @name) @definition.method
`, false)
	registerNameFix("authzed", func(name string) string {
		return strings.TrimSpace(strings.TrimSuffix(name, ":"))
	})
}
