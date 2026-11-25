package gotypes

import "go/types"

// THE EXACTNESS GUARDS
//
// go/types does not guess: a use it resolves is the object the compiler would
// pick. That holds only while the checker knows every type involved. When an
// import is missing the package is a stub with no members, and anything that
// would have come out of it is an invalid type. For plain identifiers and
// qualified names that is harmless (the use simply does not resolve). It is
// not harmless for a SELECTION through embedding, where the checker searches
// breadth-first and the answer depends on what every embedded type provides:
//
//	type S struct {
//	    unknown.Base      // a stub: contributes nothing here, Run() in reality
//	    repo.Deeper       // has Run() one level further down
//	}
//	s.Run()               // the checker picks Deeper.Run; the program calls Base.Run
//
// A fact about that call would be wrong, and an exact edge that is wrong is
// worse than a missing one. So a selection found through promotion is
// withheld whenever an embedded field of an invalid or stub-package type sits
// at any depth that could have shadowed it.

// isUnknown reports whether t is a type the checker could not really build:
// invalid, or declared by a package this run stubbed.
func (l *loader) isUnknown(t types.Type) bool {
	return l.unknownDepth(t, 0)
}

func (l *loader) unknownDepth(t types.Type, depth int) bool {
	if t == nil {
		return true
	}
	if depth > 12 {
		return true
	}
	switch t := types.Unalias(t).(type) {
	case *types.Basic:
		return t.Kind() == types.Invalid
	case *types.Named:
		if obj := t.Obj(); obj != nil && obj.Pkg() != nil && l.stubSet[obj.Pkg()] {
			return true
		}
		if b, ok := t.Underlying().(*types.Basic); ok && b.Kind() == types.Invalid {
			return true
		}
		return false
	case *types.Pointer:
		return l.unknownDepth(t.Elem(), depth+1)
	case *types.Slice:
		return l.unknownDepth(t.Elem(), depth+1)
	case *types.Array:
		return l.unknownDepth(t.Elem(), depth+1)
	case *types.Map:
		return l.unknownDepth(t.Key(), depth+1) || l.unknownDepth(t.Elem(), depth+1)
	case *types.Chan:
		return l.unknownDepth(t.Elem(), depth+1)
	case *types.Signature:
		return l.tupleUnknown(t.Params(), depth) || l.tupleUnknown(t.Results(), depth)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if l.unknownDepth(t.Field(i).Type(), depth+1) {
				return true
			}
		}
	case *types.Interface:
		for i := 0; i < t.NumExplicitMethods(); i++ {
			if l.unknownDepth(t.ExplicitMethod(i).Type(), depth+1) {
				return true
			}
		}
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if l.unknownDepth(t.EmbeddedType(i), depth+1) {
				return true
			}
		}
	case *types.Tuple:
		return l.tupleUnknown(t, depth)
	}
	return false
}

func (l *loader) tupleUnknown(t *types.Tuple, depth int) bool {
	if t == nil {
		return false
	}
	for i := 0; i < t.Len(); i++ {
		if l.unknownDepth(t.At(i).Type(), depth+1) {
			return true
		}
	}
	return false
}

// derefNamed strips pointers and returns the type underneath.
func derefType(t types.Type) types.Type {
	for {
		p, ok := types.Unalias(t).(*types.Pointer)
		if !ok {
			return types.Unalias(t)
		}
		t = p.Elem()
	}
}

// promotionTainted reports whether a member found at selection path length n
// (len(Selection.Index())) on a value of type recv could have been decided
// differently by an embedded type this run does not really know.
//
// A member declared directly on the type (n == 1) always wins over a promoted
// one, so it is never tainted. For n >= 2 every embedded field at struct level
// j <= n-2 is inspected: its members would sit at depth j+1 and so shadow, or
// collide with, the one found at depth n-1.
func (l *loader) promotionTainted(recv types.Type, n int) bool {
	if n < 2 {
		return false
	}
	level := []types.Type{derefType(recv)}
	seen := map[types.Type]bool{}
	for j := 0; j <= n-2; j++ {
		var next []types.Type
		for _, t := range level {
			if seen[t] {
				continue
			}
			seen[t] = true
			st, ok := t.Underlying().(*types.Struct)
			if !ok {
				continue
			}
			for i := 0; i < st.NumFields(); i++ {
				f := st.Field(i)
				if !f.Embedded() {
					continue
				}
				ft := derefType(f.Type())
				if l.isUnknown(ft) {
					return true
				}
				if iface, ok := ft.Underlying().(*types.Interface); ok && l.ifaceTainted(iface, 0) {
					return true
				}
				next = append(next, ft)
			}
		}
		level = next
	}
	return false
}

// ifaceTainted reports whether an interface embeds, at any depth, a type this
// run does not know.
func (l *loader) ifaceTainted(it *types.Interface, depth int) bool {
	if depth > 8 {
		return true
	}
	for i := 0; i < it.NumEmbeddeds(); i++ {
		et := types.Unalias(it.EmbeddedType(i))
		if l.isUnknown(et) {
			return true
		}
		if sub, ok := et.Underlying().(*types.Interface); ok && l.ifaceTainted(sub, depth+1) {
			return true
		}
	}
	return false
}

// ifaceMemberTainted is the interface half of the guard: a method called on
// an interface value that was found in an EMBEDDED interface could collide
// with a same-named method of an embedded type this run does not know. A
// method the interface declares itself is immune (an overlap with an
// embedded method must have an identical signature, so it is the same call).
func (l *loader) ifaceMemberTainted(recv types.Type, name string) bool {
	it, ok := derefType(recv).Underlying().(*types.Interface)
	if !ok {
		return false
	}
	for i := 0; i < it.NumExplicitMethods(); i++ {
		if it.ExplicitMethod(i).Name() == name {
			return false
		}
	}
	return l.ifaceTainted(it, 0)
}
