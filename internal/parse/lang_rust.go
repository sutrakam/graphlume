package parse

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Rust. The struct, enum, trait and const patterns are in the supplement
// (queries.go); this registers the rest and replaces the inferred query, whose
// macro pattern cannot be told apart from a call.
//
// # Definitions
//
//	fn, a method, a trait's fn signature   function
//	struct / enum / trait / const          as before
//	static X: T = v                        constant
//	static mut X: T = v                    variable
//	type Alias = T                         type_alias
//	type Item = T  (in an impl)            type_alias, owned by the impl's type
//	type Item;     (in a trait)            type_alias, owned by the trait
//	mod inline { ... }                     package
//	macro_rules! name { ... }              function, qualified name ends in !
//
// `mod external;` declares a file, not a module body, and defines nothing here;
// the file it names is indexed as itself. A static is a constant unless it is
// mut, as in the language; a macro_rules! is a function because it is invoked
// by name and defines a name nothing else does, like a function-like macro in C.
//
// # A macro definition is a function whose qualified name ends in `!`
//
// The symbol's kind stays `function`: every tool that asks for "the function or
// method of this name" (callers, callees, impact, dead code) keeps finding a
// macro by its bare name, and nothing that prints a kind learns a new word. What
// marks it is its qualified name, `src/error.rs:print_error!`, which is how the
// language writes it. The resolver (internal/resolve/infer/macro.go) reads the
// suffix: a `.macro` reference binds only to such a symbol, and a call that is
// not a macro invocation never does.
const (
	// macroDefinitionTag is the query capture of a macro_rules! definition.
	macroDefinitionTag = "definition.function.macro"
	// macroFQNMarker ends the qualified name of a macro definition. No Rust
	// identifier ends in it.
	macroFQNMarker = "!"
)

// # Macro invocations are not calls
//
// `format!(..)`, `vec![..]`, `print_error!(..)` and `std::println!(..)` are
// recorded with the kind reference.call.macro, named after the macro (without
// the `!`), and a path in front of it is the receiver, as for a call. Until now
// they were reference.call, and a macro of the same name as a function bound to
// that function: a macro and a function of one name are different things in
// separate namespaces. A consumer that counts calls by the prefix
// "reference.call" still counts them; one that must not bind them to functions
// tests for the suffix ".macro". A reference to a macro_rules! macro defined in
// the repository can be bound to the macro_rules! symbol of that name.
func init() {
	registerDefinitions("rust", `
(function_item (identifier) @name) @definition.function
(function_signature_item (identifier) @name) @definition.function
(static_item name: (identifier) @name) @definition.constant
(type_item name: (type_identifier) @name) @definition.type_alias
(associated_type name: (type_identifier) @name) @definition.type_alias
(mod_item name: (identifier) @name body: (declaration_list)) @definition.module
(macro_definition name: (identifier) @name) @definition.function.macro

(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
(call_expression (scoped_identifier (identifier) @name)) @reference.call
(macro_invocation macro: (identifier) @name) @reference.call.macro
(macro_invocation macro: (scoped_identifier name: (identifier) @name)) @reference.call.macro
`, true)
	definitionRefiners["rust"] = mutableStatics
}

// mutableStatics makes `static mut` a variable.
func mutableStatics(root *ts.Node, lang *ts.Language, _ string, _ []byte, tags []ts.Tag) map[uint32]definitionFix {
	var fixes map[uint32]definitionFix
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Kind, "definition") {
			continue
		}
		name := root.NamedDescendantForByteRange(tag.NameRange.StartByte, tag.NameRange.EndByte)
		if name == nil {
			continue
		}
		item := name.Parent()
		if item == nil || item.Type(lang) != "static_item" {
			continue
		}
		for i := 0; i < item.ChildCount(); i++ {
			if c := item.Child(i); c != nil && c.Type(lang) == "mutable_specifier" {
				if fixes == nil {
					fixes = map[uint32]definitionFix{}
				}
				fixes[tag.NameRange.StartByte] = definitionFix{kind: "definition.variable"}
			}
		}
	}
	return fixes
}
