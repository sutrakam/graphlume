package parse

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/sutrakam/graphlume/pkg/model"
)

// Constructor delegation: `this(...)` and `super(...)` (Java), `: this(...)`
// and `: base(...)` (C#).
//
// A constructor is indexed as a method named after its class, and a delegation
// is a call of one, but the call is written with a keyword and no name, so no
// query can tag it. The reference is built from the tree instead:
//
//	this(...)   a call, named after the enclosing class, on the receiver
//	            `this` (model.ReceiverSelf). It binds to one of the class's own
//	            constructors, and the argument count chooses among them.
//	super(...)  a call, named after the superclass the enclosing class declares,
//	base(...)   on the receiver `super` (model.ReceiverSuper), which is the
//	            shape the resolver starts at the class's bases from. The
//	            superclass is `extends B` in Java and the first entry of the base
//	            list in C#, which may be an interface when the class has no
//	            base class; such a name has no constructor and binds to nothing.
//
// Nothing is emitted when the enclosing class cannot be named (an anonymous
// class has no constructor to delegate from) or, for super and base, when the
// class declares no superclass: `super()` of a class that extends nothing calls
// Object's.
func (p *genericParser) constructorDelegations(tree *ts.Tree, src []byte) (out []Reference) {
	lang := p.entry.Name
	if tree != nil && lang == "ruby" {
		return p.rubySupers(tree, src)
	}
	if tree == nil || (lang != "java" && lang != "c_sharp") {
		return nil
	}
	defer recoverPanic()
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	var walk func(n *ts.Node, depth int)
	walk = func(n *ts.Node, depth int) {
		if n == nil || depth > 200 {
			return
		}
		typ := n.Type(p.lang)
		if (lang == "java" && typ == "explicit_constructor_invocation") ||
			(lang == "c_sharp" && typ == "constructor_initializer") {
			if ref, ok := p.delegationReference(n, src); ok {
				out = append(out, ref)
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i), depth+1)
		}
	}
	walk(root, 0)
	return out
}

// delegationReference builds the reference for one delegation node.
func (p *genericParser) delegationReference(n *ts.Node, src []byte) (Reference, bool) {
	var keyword string
	text := strings.TrimLeft(strings.TrimSpace(n.Text(src)), ":")
	text = strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(text, "this"):
		keyword = "this"
	case strings.HasPrefix(text, "super"), strings.HasPrefix(text, "base"):
		keyword = "super"
	default:
		return Reference{}, false
	}
	// `outer.super(...)` is qualified, and its constructor is not named by the
	// class's own superclass declaration.
	if p.entry.Name == "java" && n.ChildCount() > 0 {
		if c := n.Child(0); c != nil && c.IsNamed() && c.Type(p.lang) != "this" && c.Type(p.lang) != "super" {
			return Reference{}, false
		}
	}
	class := p.enclosingClassNode(n)
	if class == nil {
		return Reference{}, false
	}
	var name, kind string
	if keyword == "this" {
		name, kind = p.className(class, src), model.ReceiverSelf
	} else {
		name, kind = p.superclassName(class, src), model.ReceiverSuper
	}
	if name == "" {
		return Reference{}, false
	}
	ref := Reference{
		Name:         name,
		Kind:         "reference.call",
		Receiver:     keyword,
		ReceiverKind: kind,
		Range:        toRange(n.Range()),
	}
	if p.hasArity {
		for i := 0; i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil && c.Type(p.lang) == "argument_list" {
				ref.ArgCount = p.arity.count(c, p.lang, src)
			}
		}
	}
	return ref, true
}

// enclosingClassNode is the class (or struct, record) the node is written in.
func (p *genericParser) enclosingClassNode(n *ts.Node) *ts.Node {
	for a := n.Parent(); a != nil; a = a.Parent() {
		switch a.Type(p.lang) {
		case "class_declaration", "record_declaration", "struct_declaration", "enum_declaration":
			return a
		}
	}
	return nil
}

func (p *genericParser) className(class *ts.Node, src []byte) string {
	if name := class.ChildByFieldName("name", p.lang); name != nil {
		return name.Text(src)
	}
	return ""
}

// superclassName reads the superclass a class declares, as the last segment of
// the type written: `extends p.Q<T>` is Q.
func (p *genericParser) superclassName(class *ts.Node, src []byte) string {
	var typ *ts.Node
	for i := 0; i < class.ChildCount(); i++ {
		c := class.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		switch c.Type(p.lang) {
		case "superclass": // Java
			if c.NamedChildCount() > 0 {
				typ = c.NamedChild(0)
			}
		case "base_list": // C#
			if c.NamedChildCount() > 0 {
				typ = c.NamedChild(0)
			}
		}
		if typ != nil {
			break
		}
	}
	return lastTypeName(typ, p.lang, src)
}

// lastTypeName is the unqualified, non-generic name of a type node.
func lastTypeName(n *ts.Node, lang *ts.Language, src []byte) string {
	for n != nil {
		switch n.Type(lang) {
		case "type_identifier", "identifier":
			return n.Text(src)
		case "generic_type", "generic_name":
			if n.NamedChildCount() == 0 {
				return ""
			}
			n = n.NamedChild(0)
		case "scoped_type_identifier", "qualified_name", "alias_qualified_name":
			if n.NamedChildCount() == 0 {
				return ""
			}
			n = n.NamedChild(n.NamedChildCount() - 1)
		default:
			return ""
		}
	}
	return ""
}

// rubySupers records `super` and `super(...)`.
//
// A super call invokes the method of the same name in the ancestors, so the
// reference is named after the method it is written in and has the receiver
// `super`; the resolver starts at the ancestors of the enclosing class. The
// argument count is not recorded: a bare `super` passes whatever the method
// received.
//
// Not recorded: a `super` outside any def (a define_method block, a class
// body), where the method it forwards to is not named by the syntax, and
// `super.name`, which is a call of name on the result.
func (p *genericParser) rubySupers(tree *ts.Tree, src []byte) (out []Reference) {
	defer recoverPanic()
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	var walk func(n *ts.Node, depth int)
	walk = func(n *ts.Node, depth int) {
		if n == nil || depth > 200 {
			return
		}
		if n.Type(p.lang) == "super" {
			if parent := n.Parent(); parent != nil && parent.Type(p.lang) == "call" &&
				parent.ChildByFieldName("receiver", p.lang) == n {
				return
			}
			if name := p.enclosingDefName(n, src); name != "" {
				out = append(out, Reference{
					Name:         name,
					Kind:         "reference.call",
					Receiver:     "super",
					ReceiverKind: model.ReceiverSuper,
					Range:        toRange(n.Range()),
				})
			}
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i), depth+1)
		}
	}
	walk(root, 0)
	return out
}

// enclosingDefName is the name of the def a node is written in, as the source
// writes it.
func (p *genericParser) enclosingDefName(n *ts.Node, src []byte) string {
	for a := n.Parent(); a != nil; a = a.Parent() {
		switch a.Type(p.lang) {
		case "method":
			if name := a.ChildByFieldName("name", p.lang); name != nil {
				return name.Text(src)
			}
			if a.NamedChildCount() > 0 {
				return a.NamedChild(0).Text(src)
			}
			return ""
		case "singleton_method":
			if name := a.ChildByFieldName("name", p.lang); name != nil {
				return name.Text(src)
			}
			return ""
		case "class", "module":
			return ""
		}
	}
	return ""
}
