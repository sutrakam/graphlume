package parse

import (
	"sort"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Return types: what a function or method declares it returns, read so that a
// variable assigned from a call to it has a type. See migration
// migration 0001 and internal/resolve/infer/returned.go.
//
// WHICH LANGUAGES. Only those in which a declared return type binds the value:
// the compiler (Java, C#, TypeScript, Swift, Rust) or the runtime (PHP's
// `: Order` throws a TypeError on anything else) refuses a function that
// returns something other than what it declares. Each also has a local
// declaration the shape walk reads with its initialiser (see producerOf), which
// is the other half: a return type nothing can be assigned from is unused.
//
// Left out, and why:
//   - Go: exact already, through its type checker (internal/resolve/gotypes).
//
// WHAT IS READ. The plain type name, through normalizeType: the same reading a
// stated parameter or local type gets, so `?Order`, `Order?` and `&Order` are
// Order, and `Box<Order>` is Order. A generic type is read as its outer type,
// as a stated `List<Order>` is: `Optional<Order>`, `Promise<Order>` and
// `Result<Order, E>` are Optional, Promise and Result, which this repository
// rarely defines, and which the resolver then finds no type for. No other
// wrapper is unwrapped: `await` of a Promise or `?` on a Result would need the
// type argument, and the type argument is not what a call on the wrapper
// reaches. `Self`, `this` and a type parameter the definition declares name no
// type of their own and are dropped.

// returnTypeNodes are, per language, the node types that define a function or
// method, and the field that holds what it returns.
var returnTypeNodes = map[string]map[string]string{
	"java":       {"method_declaration": "type"},
	"c_sharp":    {"method_declaration": "returns", "local_function_statement": "type"},
	"typescript": tsReturnNodes,
	"tsx":        tsReturnNodes,
	"rust":       {"function_item": "return_type", "function_signature_item": "return_type"},
	"php":        {"method_declaration": "return_type", "function_definition": "return_type"},
	// Python's annotation is optional and unchecked at runtime: a stated return type
	// is read as a declaration, never as proof of what the function returns.
	"python": {"function_definition": "return_type"},
	"scala":  {"function_definition": "return_type"},
	// Kotlin's return type is an unlabelled child, read by position as Swift's is.
	"kotlin": {"function_declaration": ""},
	// Dart's return type is the type_identifier before the name, unlabelled.
	"dart": {"function_signature": "", "method_signature": ""},
	// Swift's grammar labels the return type `name`, as it labels a parameter's
	// type; swiftReturnType reads it by position instead.
	"swift": {"function_declaration": "", "protocol_function_declaration": ""},
}

var tsReturnNodes = map[string]string{
	"function_declaration": "return_type", "method_definition": "return_type",
	"method_signature": "return_type", "abstract_method_signature": "return_type",
	"function_signature": "return_type",
}

// returnsTypes reports whether the language's return types are read.
func returnsTypes(language string) bool { return returnTypeNodes[language] != nil }

// definitionReturnTypes returns, keyed by the start byte of a definition's name,
// the plain type each function or method in the tree declares it returns.
func (p *genericParser) definitionReturnTypes(tree *ts.Tree, src []byte, tags []ts.Tag) (out map[uint32]string) {
	nodes := returnTypeNodes[p.entry.Name]
	if nodes == nil || tree == nil {
		return nil
	}
	defer func() {
		// An untrusted tree, contained as every other reading of it is.
		if recover() != nil {
			RecoveredPanics.Add(1)
			out = nil
		}
	}()
	root := tree.RootNode()
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Kind, "definition.method") && !strings.HasPrefix(tag.Kind, "definition.function") {
			continue
		}
		name := root.NamedDescendantForByteRange(tag.NameRange.StartByte, tag.NameRange.EndByte)
		def := name
		for i := 0; def != nil && i < 3; i++ {
			if _, ok := nodes[def.Type(p.lang)]; ok {
				break
			}
			def = def.Parent()
		}
		if def == nil {
			continue
		}
		field, ok := nodes[def.Type(p.lang)]
		if !ok {
			continue
		}
		var typeNode *ts.Node
		switch p.entry.Name {
		case "kotlin":
			typeNode = kotlinReturnType(def, p.lang)
		case "dart":
			typeNode = dartReturnType(def, p.lang)
		case "swift":
			typeNode = swiftReturnType(def, p.lang)
		default:
			typeNode = def.ChildByFieldName(field, p.lang)
		}
		if typeNode == nil {
			continue
		}
		if typ := plainReturnType(typeNode.Text(src), typeParameters(def, p.lang, src)); typ != "" {
			if out == nil {
				out = make(map[uint32]string, 16)
			}
			out[tag.NameRange.StartByte] = typ
		}
	}
	return out
}

// dartReturnType is the type a Dart function or method declares before its name:
// the direct type_identifier of the function_signature, which a method wraps in a
// method_signature. A function with no declared type (`foo() => 1`) or a `void`
// one has none.
func dartReturnType(def *ts.Node, lang *ts.Language) *ts.Node {
	sig := def
	if def.Type(lang) == "method_signature" {
		sig = nil
		for i := 0; i < def.ChildCount(); i++ {
			if c := def.Child(i); c != nil && c.Type(lang) == "function_signature" {
				sig = c
				break
			}
		}
		if sig == nil {
			return nil
		}
	}
	for i := 0; i < sig.ChildCount(); i++ {
		c := sig.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		switch c.Type(lang) {
		case "type_identifier":
			return c
		case "identifier": // the name: no type came before it
			return nil
		}
	}
	return nil
}

// kotlinReturnType is the type a Kotlin function declares after its parameter
// list: the direct user_type child before the body. A parameter's type lives
// inside its own node, so the first direct type child is the return type.
func kotlinReturnType(def *ts.Node, lang *ts.Language) *ts.Node {
	var found *ts.Node
	for i := 0; i < def.ChildCount(); i++ {
		c := def.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		switch c.Type(lang) {
		case "function_body":
			return found
		case "user_type":
			found = c
		}
	}
	return found
}

// swiftReturnType is the type a Swift function declares after `->`: the direct
// child that is a type and follows the parameters. A parameter's own type is
// inside its `parameter` node, never a direct child of the declaration.
func swiftReturnType(def *ts.Node, lang *ts.Language) *ts.Node {
	var found *ts.Node
	for i := 0; i < def.ChildCount(); i++ {
		c := def.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		typ := c.Type(lang)
		if strings.HasSuffix(typ, "_type") && typ != "type_parameters" && typ != "type_constraints" {
			found = c
		}
		if typ == "function_body" {
			break
		}
	}
	return found
}

// typeParameters is the text of the type parameters a definition declares, ""
// when it declares none.
func typeParameters(def *ts.Node, lang *ts.Language, src []byte) string {
	if tp := def.ChildByFieldName("type_parameters", lang); tp != nil {
		return tp.Text(src)
	}
	for i := 0; i < def.ChildCount(); i++ {
		if c := def.Child(i); c != nil && c.IsNamed() && c.Type(lang) == "type_parameters" {
			return c.Text(src)
		}
	}
	return ""
}

// plainReturnType reduces a declared return type to the one type a call on its
// value reaches, or "" when it names none this resolver should act on.
func plainReturnType(text, typeParams string) string {
	t := normalizeType(text)
	if t == "" {
		return ""
	}
	last := t
	if i := strings.LastIndexAny(t, ".:\\"); i >= 0 {
		last = t[i+1:]
	}
	switch {
	case last == "" || last == "Self" || last == "this" || last == "This":
		// The enclosing type, by a word that names none.
		return ""
	case !isCapitalised(last):
		// A primitive or a keyword (`int`, `string`, `void`, `bool`): no
		// repository type. Every language read here capitalises its classes.
		return ""
	case len(last) == 1 || containsWord(typeParams, last):
		// `<T> T get()`: a type parameter, which names no type of its own.
		return ""
	}
	return t
}

// containsWord reports whether text contains word delimited by non-identifier
// bytes.
func containsWord(text, word string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		before := start == 0 || !isIdentByte(text[start-1])
		after := end == len(text) || !isIdentByte(text[end])
		if before && after {
			return true
		}
		i = start + 1
	}
}

// producerWrappers are expressions whose value is, for a call on it, the value
// of the one expression they wrap: `await f()` and `try f()` evaluate to what
// f returns (an `await` of a Promise is not: the declared type is then
// Promise, which the resolver reads as the outer type and finds no member of
// here), and `f()!` and `(f())` are f() itself.
var producerWrappers = map[string]bool{
	"await_expression": true, "try_expression": true, "parenthesized_expression": true,
	"non_null_expression": true,
}

// producerOf returns the start byte of the name of the call a declaration's
// value is, or 0 when the value is not exactly one call whose callee is a
// plain name or a member of a plain receiver.
//
// A chain (`a.b().c()`) is refused: the producing call's own receiver would
// need a return type first, and a link per hop compounds what a single link
// already risks. So is any call whose name the tags query did not report as a
// call, since the resolver finds the producer by that reference's position.
func (w *shWalker) producerOf(value *ts.Node) uint32 {
	if w.callEnds == nil || value == nil {
		return 0
	}
	n := value
	for i := 0; i < 4 && n != nil && producerWrappers[n.Type(w.lang)]; i++ {
		var inner *ts.Node
		for j := 0; j < n.ChildCount(); j++ {
			if c := n.Child(j); c != nil && c.IsNamed() && c.Type(w.lang) != "try_operator" {
				if inner != nil {
					return 0
				}
				inner = c
			}
		}
		n = inner
	}
	if n == nil {
		return 0
	}
	typ := n.Type(w.lang)
	if !strings.Contains(typ, "call") && !strings.Contains(typ, "invocation") {
		return 0
	}
	args := n.ChildByFieldName("arguments", w.lang)
	if args == nil {
		for j := 0; j < n.ChildCount(); j++ {
			if c := n.Child(j); c != nil && c.IsNamed() && (strings.Contains(c.Type(w.lang), "argument") || c.Type(w.lang) == "call_suffix") {
				args = c
				break
			}
		}
	}
	if args == nil || args.StartByte() <= n.StartByte() {
		return 0
	}
	callee := w.src[n.StartByte():args.StartByte()]
	if strings.ContainsAny(string(callee), "()") {
		// A receiver that is itself a call, or a parenthesised expression.
		return 0
	}
	// The last call name that starts before the arguments.
	i := sort.Search(len(w.callStarts), func(i int) bool { return w.callStarts[i] >= args.StartByte() }) - 1
	if i < 0 || w.callStarts[i] < n.StartByte() {
		return 0
	}
	best := w.callStarts[i]
	bestEnd := w.callEnds[best]
	if bestEnd > args.StartByte() {
		return 0
	}
	// The name must be the callee's last word: what follows it is at most a
	// type argument list, never a further member.
	rest := strings.TrimSpace(string(w.src[bestEnd:args.StartByte()]))
	if rest != "" && !strings.HasPrefix(rest, "<") && !strings.HasPrefix(rest, "::<") {
		return 0
	}
	return best
}
