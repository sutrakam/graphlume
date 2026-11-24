package parse

import (
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"

	ts "github.com/odvcencio/gotreesitter"
)

// # Python scopes — the facts a tags query cannot state
//
// Everything else this package extracts is a pattern over one node: here is a
// definition, here is a call, here is the identifier it was called on. The
// facts Python resolution needs most are not of that shape, because each is a
// property of where a node SITS rather than of the node:
//
//   - which class a definition belongs to. Two classes in one file that both
//     declare `save` produce two symbols called `save`, and without the owner
//     `self.save()` cannot be told which of them it reaches;
//   - what class a local variable holds, where the source says so outright: a
//     parameter annotated `account: Account`, or `entry = Ledger(...)` assigned
//     once in the function that then calls `entry.post()`;
//   - whether a call is made on something at all. `save()` and
//     `self.repo.save()` both arrive from the tags query with no receiver;
//   - whether a called name is a local VALUE — a parameter, a loop variable —
//     and so names no definition anywhere;
//   - which names an import brought in. `from shop.models import Account`
//     declares that this file's `Account` comes from `shop.models`, and the
//     import query records only the module.
//
// So this file walks the tree once, carrying a scope chain, and reports those.
// It is the one place in this package with per-language code, and it is kept
// apart from the generic parser for that reason.
//
// # What it refuses to infer
//
// A variable's class is reported only when the function binds that name EXACTLY
// ONCE and that one binding states the class. A second assignment, a loop
// target, a `with ... as`, an unpacking, a `global` — any other binding of the
// same name anywhere in the function — withdraws the claim entirely. That is
// flow-insensitive on purpose: tracking which assignment reaches which call is
// inference, and a wrong class is evidence pointing at the wrong method.
//
// Nothing here reads a function's RETURN type, a class ATTRIBUTE's type or a
// module-level variable's. `svc = make_service()` leaves `svc` a variable, and
// so does `self.repo.save()`. Those call sites keep exactly the receiver they
// had before this file existed.
//
// # Qualified names
//
// A definition's qualified name follows Python's own `__qualname__`: a method
// is `Class.method`, a class nested in a class is `Outer.Inner`, and anything
// defined inside a function carries the `<locals>` marker Python itself writes
// — `build.<locals>.Item.render`. The marker is load-bearing rather than
// cosmetic. internal/resolve/infer decides ownership by asking whether a
// qualified name ends `<Owner>.<name>`, so a function's inner `helper` must not
// read as `outer.helper`, a member of a class called `outer`. With the marker
// in between it reads as a member of nothing, which is what it is.

// pyLocals is the segment Python writes between a function and what it defines.
const pyLocals = "<locals>"

// memberAliasPrefix marks an Import whose Alias names a MEMBER imported from
// Path rather than a local name for Path itself.
//
// `import numpy as np` binds `np` to the module, and is recorded with Alias
// "np". `from shop.models import Account` binds `Account` to something INSIDE
// the module, and is recorded with Alias ".Account" — read together with Path
// it spells the dotted object, `shop.models.Account`. The dot cannot begin an
// identifier, so a member can never be mistaken for a module alias, which
// matters because the resolver treats a receiver equal to a module alias as
// package-qualified.
const memberAliasPrefix = "."

// IndirectCallSuffix is appended to the kind of a call whose callee is a local
// VALUE rather than a name for a definition: `callback()` where `callback` is a
// parameter, `handler(x)` where `handler` was assigned two lines up.
//
// Such a call is real and its target is unknowable from the source, so the
// reference is kept — the call site still counts as one — and marked, and the
// resolver binds it to nothing. Unmarked, it was resolved like any other name:
// a parameter called `render` bound to whichever `render` the repository
// happened to define, at whatever confidence that tier reports.
//
// A suffix on the kind rather than a field of its own because the kind is the
// open vocabulary every consumer already reads by substring ("call"), so the
// marked reference is still a call wherever calls are counted.
const IndirectCallSuffix = ".indirect"

// pySuper is the receiver recorded for `super().method()`.
//
// The receiver query captures only an identifier receiver and `super()` is a
// call, so those sites would arrive with no receiver at all. `super` cannot
// otherwise be a receiver — `super.x()` is not valid Python — so the token is
// unambiguous, and the resolver reads it as "start from the bases of the class
// this call is written in".
const pySuper = "super"

// OpaqueReceiver is the receiver recorded for a call made on an expression the
// parser cannot reduce to a name: `self.repo.save()`, `make().run()`,
// `rows[0].close()`.
//
// It exists because an EMPTY receiver was doing two jobs. The receiver query
// captures only a simple identifier, so `save()` and `self.repo.save()` both
// arrived with no receiver, and the resolver could not tell a bare name — which
// can only be something the file defines or imports — from a method called on
// an object it knows nothing about. A18 in internal/resolve/infer records what
// reading the one as the other costs. Where this token is written, an empty
// receiver means the source wrote none.
//
// Not a valid identifier in any language this package parses, so it can never
// collide with a real receiver, and short enough to fall under every
// LanguageSpec's MinTypeNameLength.
//
// This walk writes it for Python. shape.go writes the same marker for every
// other language, and both record beside it the kind of receiver it stands
// for (model.ReceiverExpr), so the one representation serves all of them.
const OpaqueReceiver = model.OpaqueReceiver

// pyMaxDepth bounds the walk's recursion.
//
// A syntax tree's depth follows the source's nesting, which is attacker
// controlled: this tool indexes repositories it did not write. Past the bound a
// subtree is simply not analysed — its definitions keep their plain names and
// its calls their written receivers, which is the state every file was in
// before this analysis existed.
const pyMaxDepth = 400

// pySite identifies a reference by where its expression STARTS and what it
// names.
//
// The generic receiver map is keyed by line and name, because the receiver
// query captures the method identifier and the tags query reports the start of
// the whole expression, and the two share only a line. This walk visits the
// call node itself, so it can use the position the tags query reports — and
// that is what makes a call that spans several lines, whose method name sits
// below the line the expression begins on, keep its receiver.
type pySite struct {
	line, col int
	name      string
}

// pyFacts is what one walk of a Python file yields.
type pyFacts struct {
	// qualNames maps a definition (the line of its `def`/`class`, and its name)
	// to its qualified name. Only NESTED definitions are present: a module-level
	// definition's qualified name is its name, which is what it already has.
	qualNames map[callSite]string

	// receivers maps EVERY call made on something — `x.f()` in any form — to
	// what it was made on: the class the receiver statically holds, pySuper for
	// `super()`, the identifier as written when nothing more is known, and
	// OpaqueReceiver for an expression with no name. A call absent from the map
	// is a bare call, and that absence is the point: see OpaqueReceiver.
	receivers map[pySite]string

	// producers maps a call on a local value to the call its value was
	// initialised from, when the value has exactly one binding and it is a plain
	// call. The resolver reads the producer's return type. See recordCall.
	producers map[pySite]pySite

	// kinds says what each entry of receivers is, in the vocabulary every
	// language shares: a stated type, the enclosing instance, the superclass,
	// a local value, a dotted path, a name, or an expression. See
	// model.ReceiverBare and its siblings.
	kinds map[pySite]string

	// indirect holds the bare calls whose callee is a local value.
	indirect map[pySite]bool

	// notCalls holds the positions the tags query reports as a call that are
	// not one: the OBJECT of `row.save()` is tagged as a call to `row`, at the
	// same position as the real call to `save`. It is the receiver, and the
	// reference is dropped — for a local value, and equally for a module or a
	// class, which would otherwise be kept and then bound as if `json` in
	// `json.dumps()` were itself a call.
	notCalls map[pySite]bool

	// members are the names imported from a module by `from M import N`.
	members []Import
}

type pyScopeKind uint8

const (
	pyModule pyScopeKind = iota
	pyClass
	pyFunction
)

// pyBinding is what a function scope knows about one local name.
type pyBinding struct {
	// producer is the plain call (`f()` or `x.f()`) the name's one binding
	// was initialised from, meaningful only while sites is 1. See recordCall.
	producer *pySite
	// sites counts every place the function binds the name.
	sites int
	// class is the class the binding states, meaningful only while sites is 1.
	class string
	// value is true when some binding makes the name a VALUE — a parameter, an
	// assignment, a loop or `with` target — rather than a nested definition or
	// an import, which are names for something that can be looked up.
	value bool
}

type pyScope struct {
	kind   pyScopeKind
	parent *pyScope
	// prefix is what a definition made directly in this scope is qualified
	// with: "" at module level, "Class." in a class, "f.<locals>." in a function.
	prefix     string
	bindings   map[string]*pyBinding
	classAttrs map[string]string
}

func (s *pyScope) enclosingClass() *pyScope {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.kind == pyClass {
			return cur
		}
	}
	return nil
}

type pyWalker struct {
	lang  *ts.Language
	src   []byte
	facts *pyFacts
}

// scopeAnalysis walks one language's syntax tree for its scope-dependent facts.
type scopeAnalysis func(lang *ts.Language, tree *ts.Tree, src []byte) *pyFacts

// scopeAnalysisFor returns the scope walk for a language, or nil.
//
// The one place that says which languages have one. The parser calls what this
// returns, and TierFor asks whether it returns anything, so the tier table
// cannot claim a walk the parser does not run.
func scopeAnalysisFor(language string) scopeAnalysis {
	if language == "python" {
		return analyzePython
	}
	return nil
}

// analyzeScopes runs the language's scope walk, or returns nil when it has
// none.
func (p *genericParser) analyzeScopes(tree *ts.Tree, src []byte) *pyFacts {
	walk := scopeAnalysisFor(p.entry.Name)
	if walk == nil {
		return nil
	}
	return walk(p.lang, tree, src)
}

// analyzePython walks a Python tree and returns its scope-dependent facts, or
// nil when there is nothing to analyse.
func analyzePython(lang *ts.Language, tree *ts.Tree, src []byte) (facts *pyFacts) {
	if tree == nil {
		return nil
	}
	defer func() {
		// The same containment every other extractor here has: a malformed tree
		// is an untrusted-input path, and losing this analysis costs the file
		// nothing it had before.
		if recover() != nil {
			RecoveredPanics.Add(1)
			facts = nil
		}
	}()
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	w := &pyWalker{
		lang: lang,
		src:  src,
		facts: &pyFacts{
			qualNames: make(map[callSite]string, 32),
			receivers: make(map[pySite]string, 64),
			producers: make(map[pySite]pySite, 16),
			indirect:  make(map[pySite]bool, 8),
			notCalls:  make(map[pySite]bool, 32),
			kinds:     make(map[pySite]string, 64),
		},
	}
	w.visit(root, &pyScope{kind: pyModule}, 0)
	sort.SliceStable(w.facts.members, func(i, j int) bool {
		a, b := w.facts.members[i], w.facts.members[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Alias < b.Alias
	})
	return w.facts
}

func (w *pyWalker) typeOf(n *ts.Node) string { return n.Type(w.lang) }

func (w *pyWalker) field(n *ts.Node, name string) *ts.Node {
	return n.ChildByFieldName(name, w.lang)
}

func (w *pyWalker) text(n *ts.Node) string { return n.Text(w.src) }

func lineOf(n *ts.Node) int { return int(n.StartPoint().Row) + 1 }

// siteOf is the position the tags query reports for a call, with a name.
func siteOf(call *ts.Node, name string) pySite {
	start := call.StartPoint()
	return pySite{line: int(start.Row) + 1, col: int(start.Column), name: name}
}

// visit walks one node in scope.
func (w *pyWalker) visit(n *ts.Node, scope *pyScope, depth int) {
	if n == nil || depth > pyMaxDepth {
		return
	}
	switch w.typeOf(n) {
	case "class_definition":
		w.visitDefinition(n, scope, depth, pyClass)
		return
	case "function_definition":
		w.visitDefinition(n, scope, depth, pyFunction)
		return
	case "lambda":
		// A lambda is a scope of its own: its parameters shadow the enclosing
		// function's names for the length of its body.
		inner := &pyScope{kind: pyFunction, parent: scope, prefix: scope.prefix,
			bindings: map[string]*pyBinding{}}
		w.bindParameters(w.field(n, "parameters"), inner)
		w.visit(w.field(n, "body"), inner, depth+1)
		return
	case "call":
		w.recordCall(n, scope)
	case "import_from_statement":
		w.recordMembers(n)
	}
	for i := 0; i < n.ChildCount(); i++ {
		w.visit(n.Child(i), scope, depth+1)
	}
}

// visitDefinition records a class or function's qualified name and walks its
// body in the scope it opens.
func (w *pyWalker) visitDefinition(n *ts.Node, scope *pyScope, depth int, kind pyScopeKind) {
	nameNode := w.field(n, "name")
	body := w.field(n, "body")
	if nameNode == nil {
		return
	}
	name := w.text(nameNode)
	qual := scope.prefix + name
	if scope.prefix != "" {
		w.facts.qualNames[callSite{line: lineOf(n), name: name}] = qual
	}

	inner := &pyScope{kind: kind, parent: scope}
	if kind == pyClass {
		inner.prefix = qual + "."
		inner.classAttrs = make(map[string]string)
		w.collectClassAttributes(body, inner)
	} else {
		inner.prefix = qual + "." + pyLocals + "."
		inner.bindings = map[string]*pyBinding{}
		w.bindParameters(w.field(n, "parameters"), inner)
		w.collectBindings(body, inner, depth+1)
	}

	// Everything that is not the body — base classes, decorators' arguments,
	// default values, annotations — is evaluated in the ENCLOSING scope.
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || c == body {
			continue
		}
		w.visit(c, scope, depth+1)
	}
	w.visit(body, inner, depth+1)
}

// collectClassAttributes collects type-annotated or constructed attributes
// directly on the class body.
func (w *pyWalker) collectClassAttributes(body *ts.Node, classScope *pyScope) {
	if body == nil {
		return
	}
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c == nil {
			continue
		}
		target := c
		if w.typeOf(c) == "expression_statement" && c.NamedChildCount() > 0 {
			target = c.NamedChild(0)
		}
		if target != nil && w.typeOf(target) == "assignment" {
			left := w.field(target, "left")
			if left != nil && w.typeOf(left) == "identifier" {
				class := w.annotationClass(w.field(target, "type"))
				if class == "" && w.field(target, "type") == nil {
					class = w.constructedClass(w.field(target, "right"))
				}
				if class != "" {
					classScope.classAttrs[w.text(left)] = class
				}
			}
		}
	}
}

// bindParameters records a parameter list's names in scope, with the class an
// annotation states where there is one.
func (w *pyWalker) bindParameters(params *ts.Node, scope *pyScope) {
	if params == nil {
		return
	}
	for i := 0; i < params.ChildCount(); i++ {
		c := params.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		switch w.typeOf(c) {
		case "identifier":
			w.bindValue(scope, w.text(c), "")
		case "typed_parameter":
			// The name is the first named child; `*args: int` wraps it in a
			// splat pattern, which is a tuple of the type and not an instance.
			first := c.NamedChild(0)
			if first != nil && w.typeOf(first) == "identifier" {
				w.bindValue(scope, w.text(first), w.annotationClass(w.field(c, "type")))
			} else {
				w.bindTargets(first, scope, 0)
			}
		case "typed_default_parameter":
			if name := w.field(c, "name"); name != nil {
				w.bindValue(scope, w.text(name), w.annotationClass(w.field(c, "type")))
			}
		case "default_parameter":
			w.bindTargets(w.field(c, "name"), scope, 0)
		default:
			// *args, **kwargs, and the lambda forms of all of the above.
			w.bindTargets(c, scope, 0)
		}
	}
}

// collectBindings finds every name a function body binds, without entering the
// scopes nested inside it.
//
// It runs over the whole body BEFORE any call in it is looked at, because the
// rule is about the whole function: an assignment further down must withdraw a
// class stated further up.
func (w *pyWalker) collectBindings(n *ts.Node, scope *pyScope, depth int) {
	if n == nil || depth > pyMaxDepth {
		return
	}
	switch w.typeOf(n) {
	case "function_definition", "class_definition":
		// A nested definition binds its own name here and is otherwise a
		// different scope.
		if name := w.field(n, "name"); name != nil {
			w.bindName(scope, w.text(name))
		}
		return
	case "lambda":
		return
	case "assignment":
		left := w.field(n, "left")
		if left != nil && w.typeOf(left) == "identifier" {
			class := w.annotationClass(w.field(n, "type"))
			if class == "" && w.field(n, "type") == nil {
				class = w.constructedClass(w.field(n, "right"))
			}
			w.bindValue(scope, w.text(left), class)
			if class == "" && w.field(n, "type") == nil {
				if b := w.lookup(w.text(left), scope); b != nil && b.sites == 1 {
					b.producer = w.producerOf(w.field(n, "right"))
				}
			}
		} else if left != nil && w.typeOf(left) == "attribute" {
			obj, attr := w.field(left, "object"), w.field(left, "attribute")
			if obj != nil && attr != nil && w.typeOf(obj) == "identifier" && w.text(obj) == "self" {
				class := w.annotationClass(w.field(n, "type"))
				if class == "" && w.field(n, "type") == nil {
					class = w.constructedClass(w.field(n, "right"))
				}
				if class == "" && w.field(n, "type") == nil {
					right := w.field(n, "right")
					if right != nil && w.typeOf(right) == "identifier" {
						class = w.classOf(w.text(right), scope)
					}
				}
				if class != "" {
					if cs := scope.enclosingClass(); cs != nil && cs.classAttrs != nil {
						cs.classAttrs[w.text(attr)] = class
					}
				}
			}
			w.bindTargets(left, scope, 0)
		} else {
			w.bindTargets(left, scope, 0)
		}
	case "augmented_assignment":
		w.bindTargets(w.field(n, "left"), scope, 0)
	case "named_expression":
		w.bindTargets(w.field(n, "name"), scope, 0)
	case "for_statement", "for_in_clause":
		w.bindTargets(w.field(n, "left"), scope, 0)
	case "as_pattern_target":
		for i := 0; i < n.ChildCount(); i++ {
			w.bindTargets(n.Child(i), scope, 0)
		}
	case "global_statement", "nonlocal_statement":
		// The name lives in another scope, where this function cannot see how
		// often it is bound or to what.
		for i := 0; i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil && w.typeOf(c) == "identifier" {
				w.bindValue(scope, w.text(c), "")
				w.bindValue(scope, w.text(c), "")
			}
		}
	case "import_statement", "import_from_statement":
		w.bindImported(n, scope)
		return
	case "case_pattern":
		// `case Point(x=0) as p` and `case p` both bind.
		w.bindTargets(n, scope, 0)
	}
	for i := 0; i < n.ChildCount(); i++ {
		w.collectBindings(n.Child(i), scope, depth+1)
	}
}

// bindImported records the names a function-local import binds. They are names
// for modules and their members, never instances, so they carry no class.
func (w *pyWalker) bindImported(n *ts.Node, scope *pyScope) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || !c.IsNamed() || n.FieldNameForChild(i, w.lang) != "name" {
			continue
		}
		switch w.typeOf(c) {
		case "aliased_import":
			if alias := w.field(c, "alias"); alias != nil {
				w.bindName(scope, w.text(alias))
			}
		case "dotted_name":
			if first := c.NamedChild(0); first != nil {
				w.bindName(scope, w.text(first))
			}
		}
	}
}

// bindTargets binds every plain name in an assignment target as a value.
//
// `a.b = x` and `a[i] = x` bind no name, so attribute and subscript targets
// are left alone rather than descended into.
func (w *pyWalker) bindTargets(n *ts.Node, scope *pyScope, depth int) {
	if n == nil || depth > pyMaxDepth {
		return
	}
	switch w.typeOf(n) {
	case "identifier":
		w.bindValue(scope, w.text(n), "")
	case "attribute", "subscript", "call":
		return
	default:
		for i := 0; i < n.ChildCount(); i++ {
			w.bindTargets(n.Child(i), scope, depth+1)
		}
	}
}

// producerOf returns the site of the plain call an assignment's right side
// makes: a bare `f()` or a `x.f()` whose receiver is a name, not another call.
// nil for anything else.
func (w *pyWalker) producerOf(rhs *ts.Node) *pySite {
	if rhs == nil || w.typeOf(rhs) != "call" {
		return nil
	}
	fn := w.field(rhs, "function")
	if fn == nil {
		return nil
	}
	var name string
	switch w.typeOf(fn) {
	case "identifier":
		name = w.text(fn)
	case "attribute":
		obj, attr := w.field(fn, "object"), w.field(fn, "attribute")
		if obj == nil || attr == nil || w.typeOf(obj) != "identifier" {
			return nil
		}
		name = w.text(attr)
	default:
		return nil
	}
	site := siteOf(rhs, name)
	return &site
}

// bindValue records a binding that makes name a value, with the class it
// states if it states one.
func (w *pyWalker) bindValue(scope *pyScope, name, class string) {
	if b := w.bind(scope, name, class); b != nil {
		b.value = true
	}
}

// bindName records a binding that makes name a NAME for something — a nested
// definition, an import — which can be looked up like any other.
func (w *pyWalker) bindName(scope *pyScope, name string) { w.bind(scope, name, "") }

func (w *pyWalker) bind(scope *pyScope, name, class string) *pyBinding {
	if scope.bindings == nil || name == "" {
		return nil
	}
	b, ok := scope.bindings[name]
	if !ok {
		b = &pyBinding{sites: 1, class: class}
		scope.bindings[name] = b
		return b
	}
	b.sites++
	b.class = ""
	return b
}

// lookup returns what the nearest FUNCTION scope knows about a name, or nil.
//
// The nearest function scope that binds the name decides, whatever it decides:
// a name the function binds twice is unknown even if an enclosing function
// states a class for it. Class scopes are skipped, as Python's own name
// resolution skips them, and the module scope is never consulted — see the
// file comment.
func (w *pyWalker) lookup(name string, scope *pyScope) *pyBinding {
	for s := scope; s != nil; s = s.parent {
		if s.kind != pyFunction {
			continue
		}
		if b, ok := s.bindings[name]; ok {
			return b
		}
	}
	return nil
}

// classOf reports the class a name statically holds at a use in scope.
func (w *pyWalker) classOf(name string, scope *pyScope) string {
	if b := w.lookup(name, scope); b != nil && b.sites == 1 {
		return b.class
	}
	return ""
}

// isLocalValue reports whether a name is a local value at a use in scope.
func (w *pyWalker) isLocalValue(name string, scope *pyScope) bool {
	b := w.lookup(name, scope)
	return b != nil && b.value
}

// recordCall notes what a call was made on, and whether it is a call through a
// local value.
func (w *pyWalker) recordCall(call *ts.Node, scope *pyScope) {
	fn := w.field(call, "function")
	if fn == nil {
		return
	}
	switch w.typeOf(fn) {
	case "identifier":
		// A bare call. `callback()` where callback is a parameter names no
		// definition; `helper()` where helper is a nested def does.
		if name := w.text(fn); w.isLocalValue(name, scope) {
			w.facts.indirect[siteOf(call, name)] = true
		}
		return
	case "attribute":
	default:
		return
	}

	object, attr := w.field(fn, "object"), w.field(fn, "attribute")
	if object == nil || attr == nil {
		return
	}
	site := siteOf(call, w.text(attr))

	recv, kind := OpaqueReceiver, model.ReceiverExpr
	switch w.typeOf(object) {
	case "identifier":
		// The class the scope states for this name, or failing that the name:
		// a variable the resolver may still recognise as a module or a class.
		name := w.text(object)
		recv, kind = name, model.ReceiverName
		local := w.isLocalValue(name, scope)
		switch class := w.classOf(name, scope); {
		case class != "":
			recv, kind = class, model.ReceiverType
		case name == "self" || name == "cls":
			kind = model.ReceiverSelf
		case local:
			kind = model.ReceiverValue
		}
		// The object of a method call is tagged as a call to that name, at the
		// position of the real call. It is the receiver, and a receiver is not
		// a call — whether it is a local value, a module or a class.
		w.facts.notCalls[siteOf(call, name)] = true
	case "call":
		// `super().method()`. An argument list is allowed — `super(C, self)` is
		// the same dispatch spelled out — but only the bare name `super`.
		if callee := w.field(object, "function"); callee != nil &&
			w.typeOf(callee) == "identifier" && w.text(callee) == pySuper {
			recv, kind = pySuper, model.ReceiverSuper
		}
	case "attribute":
		subObj, subAttr := w.field(object, "object"), w.field(object, "attribute")
		if subObj != nil && subAttr != nil && w.typeOf(subObj) == "identifier" && w.text(subObj) == "self" {
			attrName := w.text(subAttr)
			if cs := scope.enclosingClass(); cs != nil && cs.classAttrs != nil {
				if cls, ok := cs.classAttrs[attrName]; ok && cls != "" {
					recv, kind = cls, model.ReceiverType
					w.facts.notCalls[siteOf(call, attrName)] = true
				}
			}
		}
	}

	// `a.f().f()` is two calls to `f` that begin at the same character: the
	// inner one on `a`, the outer one on its result. A position cannot say
	// which reference is which, so both are recorded as made on something
	// unnamed — which is true of one and loses only a hint for the other.
	if prev, seen := w.facts.receivers[site]; seen && prev != recv {
		recv, kind = OpaqueReceiver, model.ReceiverExpr
	}
	w.facts.receivers[site] = recv
	w.facts.kinds[site] = kind
	if kind == model.ReceiverValue && w.typeOf(object) == "identifier" {
		if b := w.lookup(w.text(object), scope); b != nil && b.sites == 1 && b.producer != nil {
			w.facts.producers[site] = *b.producer
		}
	}
}

// receiverKind reports what the call at a site was made on. A call the walk
// recorded no receiver for is a bare call.
func (f *pyFacts) receiverKind(at pySite) string {
	if kind, ok := f.kinds[at]; ok {
		return kind
	}
	return model.ReceiverBare
}

// annotationClass reads the class out of a type annotation, or "".
//
// Only the forms that name ONE class: `Account`, `models.Account`, the string
// forms of both, `Optional[Account]` and `Account | None`. A union of two
// classes, a container, a callable or a type variable names no single owner of
// a method, and is unknown.
func (w *pyWalker) annotationClass(n *ts.Node) string {
	if n == nil {
		return ""
	}
	switch w.typeOf(n) {
	case "type":
		if n.NamedChildCount() != 1 {
			return ""
		}
		return w.annotationClass(n.NamedChild(0))
	case "identifier":
		return className(w.text(n))
	case "attribute":
		// `models.Account`: the class is the last segment.
		return w.annotationClass(w.field(n, "attribute"))
	case "string":
		return dottedClassName(strings.Trim(w.text(n), `"'`))
	case "generic_type":
		// Optional[X] only.
		if n.NamedChildCount() != 2 || w.text(n.NamedChild(0)) != "Optional" {
			return ""
		}
		params := n.NamedChild(1)
		if params.NamedChildCount() != 1 {
			return ""
		}
		return w.annotationClass(params.NamedChild(0))
	case "binary_operator":
		// X | None, in either order.
		left, right := w.field(n, "left"), w.field(n, "right")
		if left == nil || right == nil {
			return ""
		}
		switch {
		case w.typeOf(right) == "none":
			return w.annotationClass(left)
		case w.typeOf(left) == "none":
			return w.annotationClass(right)
		}
		return ""
	}
	return ""
}

// constructedClass reads the class out of `Name(...)` or `module.Name(...)`.
//
// A syntax tree cannot tell a class from a function, and this does not try to:
// a capitalised name is reported as written (see className), and the resolver
// accepts it only if it can identify a class of exactly that name.
//
// The qualified form is read only when `module` is a plain name, because the
// same shape is also every method call that returns something — and never on
// `self` or `cls`, where `self.Meta(...)` is an attribute of the instance.
func (w *pyWalker) constructedClass(n *ts.Node) string {
	if n == nil || w.typeOf(n) != "call" {
		return ""
	}
	fn := w.field(n, "function")
	if fn == nil {
		return ""
	}
	switch w.typeOf(fn) {
	case "identifier":
		return className(w.text(fn))
	case "attribute":
		object, attr := w.field(fn, "object"), w.field(fn, "attribute")
		if object == nil || attr == nil || w.typeOf(object) != "identifier" {
			return ""
		}
		switch w.text(object) {
		case "self", "cls":
			return ""
		}
		return className(w.text(attr))
	}
	return ""
}

// className accepts a name as a class only when it is SPELLED as one: it
// begins with a capital.
//
// The restriction is what makes replacing a variable with a class safe to do
// from syntax alone. A tree cannot tell `acct = Account()` from
// `conn = connect()`, and the second replaced `conn` with `connect` — a name
// the resolver may know as a function, or as a module the file imported, and
// would then read `conn.close()` as qualified by. A capitalised name is, by the
// language's own convention, a class or nothing; a class named in lower case is
// left as the variable that holds it, which costs a binding and never a wrong
// one.
func className(name string) string {
	if name == "" || name[0] < 'A' || name[0] > 'Z' || name == "None" {
		return ""
	}
	return name
}

// dottedClassName takes the last segment of a dotted name written in a string
// annotation, or "" when the string is anything but a dotted name.
func dottedClassName(s string) string {
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ident := c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9')
		if !ident {
			return ""
		}
	}
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = s[i+1:]
	}
	return className(s)
}

// recordMembers notes the names a `from M import a, b` brings in.
//
// Recorded for the whole file wherever the statement sits. A function-local
// import binds its names in that function only, but a bare use of the same
// name elsewhere in the file would be an error unless something else bound it,
// and "something else" — a definition in the file — is consulted first.
//
// The path keeps a relative import's leading dots (`.helpers`, `..pkg.mod`), as
// the import query does: they are what lets the resolver anchor the module to
// the importing file's own directory instead of matching any `helpers.py`.
//
// Aliased members (`from M import a as b`) are not recorded: the local name is
// not the definition's name, so a reference written `b` shares no name with
// anything the module defines, and there is nothing for the resolver to weigh.
// A wildcard names nothing. A bare relative import (`from . import x`) is
// recorded against the package itself, the path being just its dots.
func (w *pyWalker) recordMembers(n *ts.Node) {
	module := w.field(n, "module_name")
	if module == nil {
		return
	}
	path := ""
	switch w.typeOf(module) {
	case "dotted_name", "relative_import":
		path = w.text(module)
	}
	if path == "" {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || c == module || n.FieldNameForChild(i, w.lang) != "name" {
			continue
		}
		if w.typeOf(c) != "dotted_name" || c.NamedChildCount() != 1 {
			continue
		}
		start := c.StartPoint()
		end := c.EndPoint()
		imp := Import{Path: path, Alias: memberAliasPrefix + w.text(c)}
		imp.Range.Start.Line, imp.Range.Start.Col = int(start.Row)+1, int(start.Column)
		imp.Range.End.Line, imp.Range.End.Col = int(end.Row)+1, int(end.Column)
		w.facts.members = append(w.facts.members, imp)
	}
}
