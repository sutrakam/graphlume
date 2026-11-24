package parse

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Kotlin calls and the types they reach. The inferred tags query for Kotlin
// finds definitions and no call at all, so every "who calls this" answer for
// Kotlin was empty.
//
// A call is a call_expression whose callee is either a bare identifier or a
// navigation_expression ending in a navigation_suffix:
//
//	f(x), Widget(3)        a bare name; Widget(3) constructs when Widget is a
//	                       class, which the resolver decides (CallConstructs)
//	x.m(), x?.m()          a call on x; shape.go reads `?.` already
//	this.m(), super.m()    `self` and `super`
//	A.m()                  a call on the name A: a class's companion object, or
//	                       an object, whose members are indexed under A
//	"s".trim(), f(1).m()   a call on an expression
//	x.ext()                an extension function is called like a method; the
//	                       call is extracted, and bound only where one function
//	                       of that name is the unique candidate
//
// Not extracted: infix calls (`a to b`), operator calls (`a + b`), calls
// through a function-typed value (`callback(x)` is a bare call whose callee is
// a local, which the resolver does not bind), and `object : Base() {}`.
//
// Two definition gaps are closed because calls cannot bind without them: a
// class declared with no body (`class A(val x: Int)`, `data class P(...)`) was
// not indexed at all, and an `object` was indexed as a FUNCTION by the
// catch-all kind, which made `Registry.lookup()` a call on a callable. It is
// now a variable: a singleton value, which is neither called nor constructed,
// so it competes with no class of the same name. (A module was tried and made
// `extends A` ambiguous wherever a class has a companion of its name.)
func init() {
	registerDefinitions("kotlin", `
(call_expression . (simple_identifier) @name) @reference.call
(call_expression (navigation_expression (navigation_suffix (simple_identifier) @name .))) @reference.call
(class_declaration "class" (type_identifier) @name) @definition.class
(object_declaration (type_identifier) @name) @definition.variable
(source_file (property_declaration (binding_pattern_kind) @_k (variable_declaration (simple_identifier) @name) (#eq? @_k "val")) @definition.constant)
(source_file (property_declaration (binding_pattern_kind) @_k (variable_declaration (simple_identifier) @name) (#eq? @_k "var")) @definition.variable)
(secondary_constructor "constructor" @name) @definition.constructor
`, false)
	registerNameFix("kotlin", func(name string) string {
		return strings.Trim(name, "`")
	})
	definitionRefiners["kotlin"] = dropAnonymousObjectMembers
}

// Top-level properties, anonymous objects and backticked names.
//
// A `val`, `const val` or `var` written at the top of a file is a constant or a
// variable, in the sense Kotlin gives it: a `val` is read-only, so a constant
// whether or not it is `const`; a `var` is a variable. moshi's nine misses were
// all of this kind, and the calls in their initialisers
//
//	val NO_ANNOTATIONS = emptySet<Annotation>()
//
// had no caller, because no symbol enclosed them: with the property a symbol,
// the call is written in it. A destructuring declaration (`val (a, b) = p`) has
// no single name and is not a definition. A property of a class is not one
// either, as before (see the fixture).
//
// The methods of an anonymous object (`object : Runnable { override fun run()
// {} }`) are DROPPED, not owned. The object has no name, so no call can reach
// its methods but through the supertype, where the interface's own declaration
// is the target; extracted, they were free-standing methods with the names
// `run` or `compareTo` that a bare call could bind to (moshi: three). A named
// class or object declared inside one keeps its own members.
//
// A name written in backticks (“-JsonUtf8Reader“, “should parse“) is the name
// without them: it is how the language escapes a name that is not an
// identifier, not part of the name. The class symbol is -JsonUtf8Reader and its
// methods are qualified by it; keeping the backticks would hide the class from the
// scope walk and leave every method of it unqualified.
func dropAnonymousObjectMembers(root *ts.Node, lang *ts.Language, _ string, _ []byte, tags []ts.Tag) map[uint32]definitionFix {
	var fixes map[uint32]definitionFix
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Kind, "definition") {
			continue
		}
		name := root.NamedDescendantForByteRange(tag.NameRange.StartByte, tag.NameRange.EndByte)
		if name == nil || name.Type(lang) != "simple_identifier" {
			continue
		}
		decl := name.Parent()
		if decl == nil || decl.Type(lang) != "function_declaration" {
			continue
		}
	climb:
		for a := decl.Parent(); a != nil; a = a.Parent() {
			switch a.Type(lang) {
			case "object_literal":
				if fixes == nil {
					fixes = map[uint32]definitionFix{}
				}
				fixes[tag.NameRange.StartByte] = definitionFix{drop: true}
				break climb
			case "class_declaration", "object_declaration", "source_file":
				break climb
			}
		}
	}
	return fixes
}

// kotlinField answers the field lookups the scope walk (shape.go) makes of a
// declaration, for a grammar that defines no fields at all: the Kotlin grammar
// names no child, so a field lookup of "name" is nil for every class and
// function and the walk saw no class scope in Kotlin. Every method then
// qualified as a free function, and `this.m()`, `super.m()` and `A.m()` could
// not find the type they were made on.
//
// Only "name" and "body", and only for the declarations that open a scope. The
// name is the first identifier among the node's own children (an extension
// function's receiver type is a node of its own, so `fun String.ext()` names
// `ext`), and the body is the block of members.
func (w *shWalker) kotlinField(n *ts.Node, name string) *ts.Node {
	var nameType string
	var bodyTypes []string
	switch n.Type(w.lang) {
	case "class_declaration", "object_declaration":
		nameType, bodyTypes = "type_identifier", []string{"class_body", "enum_class_body"}
	case "function_declaration":
		nameType, bodyTypes = "simple_identifier", []string{"function_body"}
	default:
		return nil
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		ct := c.Type(w.lang)
		if name == "name" && ct == nameType {
			return c
		}
		if name == "body" {
			for _, b := range bodyTypes {
				if ct == b {
					return c
				}
			}
		}
	}
	return nil
}
