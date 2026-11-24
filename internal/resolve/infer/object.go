package infer

import (
	"path"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// # An object is called like a function
//
// In Scala `object exists { def apply(p: Path): Boolean }` is called
// `os.exists(p)`: the callee names an object, and the call is its `apply`.
// os-lib's whole API is written that way (`os.exists`, `os.isFile`, `os.walk`,
// `os.remove`). The index holds an object as a symbol of kind `variable`, which
// a call cannot denote, so without special handling every such call is dropped from the candidates and
// falls through to whatever method of the same name is left: 772 references
// unresolved, 380 ambiguous, and `os.isFile(p)` bound to the instance method
// `StatInfo.isFile`, `os.root(...)` to `Path.root`.
//
// Two rules, both for a language whose objects are called
// (CallTraits.ObjectsAreCallable):
//
//   - a call qualified by a package (`os.exists`) names a definition of that
//     package: a module-level function, an object, or a case class, in a file
//     under a directory of that name. Scala spells packages as directories by
//     convention and the index records no package clause, so the directory is
//     the evidence, and a definition elsewhere is not what the call reaches. A
//     qualified call that finds nothing there is bound to nothing: it does NOT
//     fall through to a method of the same name on some type.
//   - a bare call names an object defined in the same file, or in the same
//     directory (one package), by the same convention.
//
// The binding is to the object's `apply` when the index holds it among the
// candidates, chosen among overloads by the argument count as any member is,
// and to the object itself otherwise: a CALLS edge to an object is what "who
// calls os.exists" is asked of.

// callInPackage binds `pkg.name(...)` where pkg is not a type. handled is false
// when the qualifier is not shown to be a package of this repository, and the
// ordinary rules go on.
func (r *Resolver) callInPackage(c call, qualifier string) (decision, bool) {
	if !c.spec.Calls.ObjectsAreCallable {
		return decision{}, false
	}
	if _, found, known := r.typeNamed(c, qualifier); found || known {
		return decision{}, false
	}
	if !r.layout.Known() || !r.layout.HasDirNamed(qualifier) {
		return decision{}, false
	}
	var defs []registry.SymbolIndexEntry
	for _, s := range c.symbols[c.ref.Name] {
		if s.Language != "" && s.Language != c.spec.Name {
			continue
		}
		if r.callableByObjectRule(s) && hasPathSegment(s.FilePath, qualifier) {
			defs = append(defs, s)
		}
	}
	if len(defs) == 0 {
		// The package is here and defines nothing of that name: the call is
		// not to a method of some type.
		return unresolvedCall, true
	}
	return r.bindObjectOrFunction(c, defs, "declared in the package the call is qualified by"), true
}

// callableByObjectRule reports whether a module-level definition is something
// a call can name under this rule.
func (r *Resolver) callableByObjectRule(s registry.SymbolIndexEntry) bool {
	if !isModuleLevel(s) {
		return false
	}
	return isCallableKind(s.Kind) || isConstructibleKind(s.Kind) || isObjectKind(s.Kind)
}

// isObjectKind reports whether a symbol kind is how a singleton object is
// indexed.
func isObjectKind(kind string) bool { return kind == string(model.KindVariable) }

// callBareObject binds `exists(p)` to an object defined in the same file or in
// the same directory.
func (r *Resolver) callBareObject(c call) (decision, bool) {
	if !c.spec.Calls.ObjectsAreCallable {
		return decision{}, false
	}
	for _, s := range c.cands {
		if isConstructibleKind(s.Kind) && (s.FileID == c.fr.FileID || path.Dir(s.FilePath) == path.Dir(c.fr.Path)) {
			// A case class and its companion object share a name, and
			// `Shellable(x)` constructs the class: the type rules say so.
			return decision{}, false
		}
	}
	var here, pkg []registry.SymbolIndexEntry
	dir := path.Dir(c.fr.Path)
	for _, s := range c.symbols[c.ref.Name] {
		if !isObjectKind(s.Kind) || !isModuleLevel(s) || (s.Language != "" && s.Language != c.spec.Name) {
			continue
		}
		switch {
		case s.FileID == c.fr.FileID:
			here = append(here, s)
		case path.Dir(s.FilePath) == dir:
			pkg = append(pkg, s)
		}
	}
	switch {
	case len(here) > 0:
		return r.bindObjectOrFunction(c, here, "an object defined in the same file"), true
	case len(pkg) > 0:
		return r.bindObjectOrFunction(c, pkg, "an object defined in the same package directory"), true
	}
	return decision{}, false
}

// bindObjectOrFunction chooses among the definitions a call may name.
func (r *Resolver) bindObjectOrFunction(c call, defs []registry.SymbolIndexEntry, rule string) decision {
	// A case class and its companion object share a name; the call constructs
	// the class.
	var classes []registry.SymbolIndexEntry
	for _, s := range defs {
		if isConstructibleKind(s.Kind) {
			classes = append(classes, s)
		}
	}
	if len(classes) > 0 {
		defs = classes
	}
	if o, ok := oneDeclaration(c.spec, c.ref, defs); ok {
		if !isObjectKind(o.first.Kind) {
			if r.callsTestHelper(c, o.first) {
				return unresolvedCall
			}
			if o.overloaded() {
				return bound(candidate{o.first, model.ConfidenceLow, overloadedReason(rule, "", o)})
			}
			return bound(candidate{o.first, model.ConfidenceHigh, rule})
		}
		if r.callsTestHelper(c, o.first) {
			return unresolvedCall
		}
		return r.applyOf(c, o.first, rule)
	}
	return ambiguousCall
}

// applyOf binds a call of an object to the object's `apply` when the candidates
// hold it, and to the object itself when they do not.
func (r *Resolver) applyOf(c call, obj registry.SymbolIndexEntry, rule string) decision {
	qual, _ := qualOf(obj)
	var applies []registry.SymbolIndexEntry
	// The candidates for a name are the first few the index holds (MaxCandidates
	// of them), and every object has an `apply`. Past that cap the object's own
	// overloads cannot all be seen, and naming one of them would be a guess the
	// index cannot check: the call is bound to the object.
	if len(c.symbols["apply"]) <= c.spec.MaxCandidates {
		for _, s := range c.symbols["apply"] {
			if s.FileID == obj.FileID && isCallableKind(s.Kind) && strings.HasPrefix(s.FQN, obj.FilePath+":"+qual+".") && ownerQual(s) == qual {
				applies = append(applies, s)
			}
		}
	}
	if o, ok := oneDeclaration(c.spec, c.ref, applies); ok {
		if o.overloaded() {
			return bound(candidate{o.first, model.ConfidenceLow, overloadedReason(rule+", called through apply", qual, o)})
		}
		return bound(candidate{o.first, model.ConfidenceHigh, rule + ", called through apply"})
	}
	return bound(candidate{obj, model.ConfidenceHigh, rule + ": the object is called, and its apply is not among the candidates"})
}

// callInObject binds `os.read.bytes(p)` and `os.move.matching { ... }`: a call
// qualified by a package and then an object, naming a member of that object (a
// function, a nested object, a nested case class). The object is found the way
// callInPackage finds one, and the member is declared in the same file under
// its qualified name. A qualified call that finds no such member binds to
// nothing: `bytes` of some other type is not what `os.read.bytes` reaches.
func (r *Resolver) callInObject(c call, segs []string) (decision, bool) {
	if !c.spec.Calls.ObjectsAreCallable || len(segs) < 1 {
		return decision{}, false
	}
	head := segs[0]
	if _, found, known := r.typeNamed(c, head); found || known {
		return decision{}, false
	}
	// The chain starts at a package (`os.read.bytes`: os is a directory) or at
	// an object of the same file or package directory (`read.bytes` written
	// beside `object read`).
	owner := strings.Join(segs[1:], ".")
	where := func(s registry.SymbolIndexEntry) bool { return hasPathSegment(s.FilePath, head) }
	if !r.layout.Known() || !r.layout.HasDirNamed(head) || len(segs) < 2 {
		var objects []registry.SymbolIndexEntry
		for _, s := range c.symbols[head] {
			if isObjectKind(s.Kind) && isModuleLevel(s) && (s.FileID == c.fr.FileID || path.Dir(s.FilePath) == path.Dir(c.fr.Path)) {
				objects = append(objects, s)
			}
		}
		if len(objects) == 0 {
			return decision{}, false
		}
		owner = strings.Join(segs, ".")
		where = func(s registry.SymbolIndexEntry) bool {
			return s.FileID == objects[0].FileID
		}
	}
	var members []registry.SymbolIndexEntry
	for _, s := range c.symbols[c.ref.Name] {
		if s.Language != "" && s.Language != c.spec.Name {
			continue
		}
		if !where(s) {
			continue
		}
		if qual, ok := qualOf(s); ok && qual == owner+"."+c.ref.Name &&
			(isCallableKind(s.Kind) || isConstructibleKind(s.Kind) || isObjectKind(s.Kind)) {
			members = append(members, s)
		}
	}
	if len(members) == 0 {
		return unresolvedCall, true
	}
	return r.bindObjectOrFunction(c, members, "declared in the object the call is qualified by"), true
}

// hasPathSegment reports whether a file lies under a directory of this name.
func hasPathSegment(filePath, segment string) bool {
	for _, seg := range strings.Split(path.Dir(filePath), "/") {
		if seg == segment {
			return true
		}
	}
	return false
}
