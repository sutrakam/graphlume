package parse

import (
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"

	ts "github.com/odvcencio/gotreesitter"
)

// Scala calls and the types they reach. The inferred tags query for Scala
// finds definitions and no call at all, so every "who calls this" answer for
// Scala was empty.
//
//	f(x), Widget(3)        a bare name. Widget(3) is the apply of a companion
//	                       object or the construction of a case class
//	x.m(a), this.m(), super.m()
//	                       a field_expression called; the receiver is read by
//	                       shape.go as for every other language
//	f[T](x), x.m[T](a)     the same through a type application
//	x m a                  an infix expression whose operator is a name is a
//	                       call of m on x. Its receiver is recorded as an
//	                       expression, so it binds only on the name's own
//	                       evidence (the unique member of that name)
//	new A(x), new p.A(x)   a reference of kind reference.call.new whose name is
//	                       the TYPE, bound to a class as INSTANTIATES
//	new A { ... }          an anonymous subclass: a construction of A
//
// Not extracted: a call without parentheses (`x.size`, `x go`), a postfix
// operator, a call through `apply` written implicitly on a value (`f(x)` where
// f is a val), symbolic operators (`a + b`), and string interpolators.
//
// An `object` would be indexed as a FUNCTION by the catch-all kind, so a companion
// would compete with its class for every `Widget(3)`. It is a variable: a
// singleton value, neither called nor constructed, which beats the function it
// also produces when the two are merged. Its members are qualified under its
// name (shape.go), which is what `Widget.make()` finds them by. (A class or a
// module would make `extends Widget` ambiguous wherever a
// class has a companion.)
func init() {
	registerDefinitions("scala", `
(call_expression function: (identifier) @name) @reference.call
(call_expression function: (field_expression field: (identifier) @name)) @reference.call
(call_expression function: (generic_function function: (identifier) @name)) @reference.call
(call_expression function: (generic_function function: (field_expression field: (identifier) @name))) @reference.call
(infix_expression operator: (identifier) @name) @reference.call
(instance_expression (type_identifier) @name @reference.call.new)
(instance_expression (stable_type_identifier (type_identifier) @name .) @reference.call.new)
(instance_expression (generic_type type: (type_identifier) @name) @reference.call.new)
(object_definition name: (identifier) @name) @definition.variable
(function_definition . (operator_identifier) @name) @definition.function
(function_declaration . (operator_identifier) @name) @definition.function
(function_definition (modifiers) . (operator_identifier) @name) @definition.function
(function_declaration (modifiers) . (operator_identifier) @name) @definition.function
(type_definition . (type_identifier) @name) @definition.type_alias
(type_definition (modifiers) . (type_identifier) @name) @definition.type_alias
`, false)
	definitionRefiners["scala"] = dropLocalDefinitions
}

// Operators, type members, and what is not a member.
//
// `def /(that: String): Path` is named by an operator_identifier, a node the
// inferred pattern (which wants an identifier) never saw: os-lib declares its
// whole path algebra that way. `type Segment = String` and the abstract
// `type Self <: Path` are type_definition nodes and define a type alias, the
// way a Go or Kotlin alias does.
//
// A def is a definition when it is a member of a class, trait, object or
// enum, or sits at the top of a file (Scala 3). Two things were taken for
// that and are not, 26 of them in os-lib's five files:
//
//	def run(a: Int) = { def helper(b: Int) = b + 1; helper(a) }    helper is local
//	val r = new Runnable { def run(): Unit = () }                  an anonymous class's
//
// A local def cannot be named from outside its block, and the members of an
// anonymous class are reached only through the type it extends, so neither
// is a definition; both are dropped, not owned. A named class declared in a
// block keeps its own members.
func dropLocalDefinitions(root *ts.Node, lang *ts.Language, _ string, _ []byte, tags []ts.Tag) map[uint32]definitionFix {
	var fixes map[uint32]definitionFix
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Kind, "definition") {
			continue
		}
		name := root.NamedDescendantForByteRange(tag.NameRange.StartByte, tag.NameRange.EndByte)
		if name == nil {
			continue
		}
		decl := name.Parent()
		if decl == nil {
			continue
		}
		switch decl.Type(lang) {
		case "function_definition", "function_declaration", "type_definition":
		default:
			continue
		}
		if !isScalaMemberPosition(decl.Parent(), lang) {
			if fixes == nil {
				fixes = map[uint32]definitionFix{}
			}
			fixes[tag.NameRange.StartByte] = definitionFix{drop: true}
		}
	}
	return fixes
}

// isScalaMemberPosition reports whether a declaration whose parent is p is a
// member of a named type, or a top-level declaration.
func isScalaMemberPosition(p *ts.Node, lang *ts.Language) bool {
	if p == nil {
		return true
	}
	switch p.Type(lang) {
	case "template_body", "with_template_body":
		// The body of an anonymous class belongs to an instance_expression.
		if owner := p.Parent(); owner != nil && owner.Type(lang) == "instance_expression" {
			return false
		}
		return true
	case "compilation_unit", "package_clause", "enum_body", "extension_definition", "given_definition":
		return true
	}
	return false
}

// scalaImports reads a file's import declarations as import rows with dotted
// paths, the shape the resolver splits:
//
//	import app.util.Helper            app.util.Helper
//	import app.util.{A, B => C}       app.util.A, and app.util.B aliased C
//	import app.util._                 app.util (the package, whole)
//	import app.util.Helper as H       app.util.Helper aliased H (Scala 3)
//
// The grammar spreads a dotted path over sibling identifiers with no node that
// spans it, so a query cannot capture it; taking the last
// identifier alone would name no package at all.
func (p *genericParser) scalaImports(tree *ts.Tree, src []byte) (out []Import) {
	defer recoverPanic()
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	lang := p.lang
	add := func(segs []string, alias string, at *ts.Node) {
		if len(segs) == 0 || at == nil {
			return
		}
		imp := Import{Path: strings.Join(segs, "."), Alias: alias}
		imp.Range = model.Range{
			Start: model.Position{Line: int(at.StartPoint().Row) + 1, Col: int(at.StartPoint().Column)},
			End:   model.Position{Line: int(at.EndPoint().Row) + 1, Col: int(at.EndPoint().Column)},
		}
		out = append(out, imp)
	}
	var walk func(n *ts.Node, depth int)
	walk = func(n *ts.Node, depth int) {
		if n == nil || depth > 8 {
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			ch := n.Child(i)
			if ch == nil {
				continue
			}
			switch ch.Type(lang) {
			case "package_clause", "template_body":
				walk(ch, depth+1)
			case "import_declaration":
				var segs []string
				flush := func(wholePackage bool) {
					if wholePackage {
						add(segs, "", ch)
					}
				}
				wild := false
				done := false
				for j := 0; j < ch.ChildCount(); j++ {
					k := ch.Child(j)
					if k == nil {
						continue
					}
					switch k.Type(lang) {
					case ",":
						if !done {
							flush(len(segs) > 0 && !wild)
						}
						segs, wild, done = nil, false, false
					case "identifier":
						segs = append(segs, k.Text(src))
					case "namespace_wildcard":
						add(segs, "", ch)
						wild, done = true, true
					case "as_renamed_identifier":
						var parts []string
						for m := 0; m < k.ChildCount(); m++ {
							if id := k.Child(m); id != nil && id.IsNamed() && id.Type(lang) == "identifier" {
								parts = append(parts, id.Text(src))
							}
						}
						if len(parts) == 2 {
							add(append(append([]string{}, segs...), parts[0]), parts[1], ch)
						}
						done = true
					case "namespace_selectors":
						for m := 0; m < k.ChildCount(); m++ {
							s := k.Child(m)
							if s == nil || !s.IsNamed() {
								continue
							}
							switch s.Type(lang) {
							case "identifier":
								add(append(append([]string{}, segs...), s.Text(src)), "", ch)
							case "arrow_renamed_identifier":
								var parts []string
								for q := 0; q < s.ChildCount(); q++ {
									if id := s.Child(q); id != nil && id.IsNamed() && id.Type(lang) == "identifier" {
										parts = append(parts, id.Text(src))
									}
								}
								if len(parts) == 2 && parts[1] != "_" {
									add(append(append([]string{}, segs...), parts[0]), parts[1], ch)
								}
							case "namespace_wildcard":
								add(segs, "", ch)
							}
						}
						done = true
					}
				}
				if !done && len(segs) > 0 {
					add(segs, "", ch)
				}
			}
		}
	}
	walk(root, 0)
	return out
}
