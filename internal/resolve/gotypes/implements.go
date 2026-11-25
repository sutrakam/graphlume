package gotypes

import (
	"go/types"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// implCandidate is a named type or interface declared in a repository file.
type implCandidate struct {
	named *types.Named
	iface *types.Interface // non-nil for an interface with methods
	ref   ObjRef
}

// collectImplCandidates gathers the named types and non-empty interfaces a
// checked package declares in files that carry facts. The in-package test
// variant contributes only what its test files declare (the rest is the plain
// package's, already collected).
func (l *loader) collectImplCandidates(u *unit, v variant, pkg *types.Package) {
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok || named.TypeParams().Len() > 0 {
			continue
		}
		p := l.pos(tn.Pos())
		rel, ok := l.repoFile[p.Filename]
		if !ok || !l.report[rel] {
			continue
		}
		if v == varTest && !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		c := implCandidate{named: named, ref: ObjRef{
			File: rel, Line: p.Line, Col: p.Column - 1, Name: tn.Name(), Kind: model.KindClass,
		}}
		if it, ok := named.Underlying().(*types.Interface); ok {
			it.Complete()
			if !it.IsMethodSet() || it.NumMethods() == 0 {
				continue
			}
			c.iface = it
			c.ref.Kind = model.KindInterface
		}
		l.impl = append(l.impl, c)
	}
}

// implementations computes, for every named type and every non-empty
// interface the repository declares, whether T or *T implements the interface,
// and records the concrete methods that do the implementing.
//
// The pairs are filtered, never inferred: a pair is kept only when
// types.Implements says so AND no type involved is one this run does not
// really know (an invalid method signature would be "identical" to anything,
// and a promoted method could be shadowed by an unknown embedded type).
func (l *loader) implementations() {
	var types_, ifaces []implCandidate
	for _, c := range l.impl {
		if c.iface != nil {
			ifaces = append(ifaces, c)
		} else {
			types_ = append(types_, c)
		}
	}
	key := func(c implCandidate) string { return c.ref.FQN() }
	sort.Slice(types_, func(i, j int) bool { return key(types_[i]) < key(types_[j]) })
	sort.Slice(ifaces, func(i, j int) bool { return key(ifaces[i]) < key(ifaces[j]) })

	type entry struct {
		c    implCandidate
		ptr  *types.Pointer
		mset *types.MethodSet // of *T: includes T's methods
		vset *types.MethodSet
	}
	entries := make([]entry, len(types_))
	byName := map[string][]int{}
	for i, c := range types_ {
		p := types.NewPointer(c.named)
		e := entry{c: c, ptr: p, mset: types.NewMethodSet(p), vset: types.NewMethodSet(c.named)}
		entries[i] = e
		for j := 0; j < e.mset.Len(); j++ {
			n := e.mset.At(j).Obj().Name()
			byName[n] = append(byName[n], i)
		}
	}

	for _, ic := range ifaces {
		if l.ctx.Err() != nil {
			return
		}
		it := ic.iface
		if l.ifaceTainted(it, 0) || l.isUnknown(it) {
			continue
		}
		rarest, rn := "", -1
		for i := 0; i < it.NumMethods(); i++ {
			n := it.Method(i).Name()
			if c := len(byName[n]); rn < 0 || c < rn {
				rarest, rn = n, c
			}
		}
		for _, ei := range byName[rarest] {
			e := entries[ei]
			if e.c.named == ic.named {
				continue
			}
			byPtr := false
			if !types.Implements(e.c.named, it) {
				if !types.Implements(e.ptr, it) {
					continue
				}
				byPtr = true
			}
			set := e.vset
			recv := types.Type(e.c.named)
			if byPtr {
				set, recv = e.mset, e.ptr
			}
			im, ok := l.matchMethods(e.c, ic, it, set, recv)
			if !ok {
				continue
			}
			im.PointerOnly = byPtr
			l.out.impls = append(l.out.impls, im)
			l.stats.Implementations++
			l.stats.ImplementationMethods += len(im.Methods)
		}
	}
}

// matchMethods pairs each interface method with the concrete method the type
// declares, applying the exactness guards. ok is false when the pair must be
// withheld.
func (l *loader) matchMethods(tc, ic implCandidate, it *types.Interface, set *types.MethodSet, recv types.Type) (Impl, bool) {
	im := Impl{Type: tc.ref, Iface: ic.ref}
	for i := 0; i < it.NumMethods(); i++ {
		m := it.Method(i)
		if l.isUnknown(m.Type()) {
			return Impl{}, false
		}
		sel := set.Lookup(m.Pkg(), m.Name())
		if sel == nil {
			return Impl{}, false
		}
		cf, ok := sel.Obj().(*types.Func)
		if !ok || l.isUnknown(cf.Type()) {
			return Impl{}, false
		}
		if n := len(sel.Index()); n > 1 {
			if l.promotionTainted(recv, n) {
				l.stats.WithheldTaint++
				return Impl{}, false
			}
			continue // promoted: the type implements it, the method is another type's
		}
		cref, repo, emit := l.objRef(cf.Origin())
		if !emit || !repo {
			continue
		}
		iref, repo, emit := l.interfaceMethodRef(ic, m)
		if !emit || !repo {
			continue
		}
		im.Methods = append(im.Methods, MethodImpl{Concrete: cref, Iface: iref})
	}
	sort.SliceStable(im.Methods, func(i, j int) bool {
		return im.Methods[i].Concrete.FQN() < im.Methods[j].Concrete.FQN()
	})
	return im, true
}

// interfaceMethodRef names the interface's method declaration. A method the
// interface got by embedding another interface is declared there, and that
// declaration is what the graph holds, so the reference follows it.
func (l *loader) interfaceMethodRef(ic implCandidate, m *types.Func) (ObjRef, bool, bool) {
	p := l.pos(m.Pos())
	rel, ok := l.repoFile[p.Filename]
	if !ok {
		return ObjRef{}, false, true
	}
	recv := ic.ref.Name
	if m.Pkg() == ic.named.Obj().Pkg() && p.Filename != "" {
		if owner := declaringInterface(ic.named, m); owner != "" {
			recv = owner
		}
	}
	return ObjRef{File: rel, Line: p.Line, Col: p.Column - 1, Name: m.Name(), Recv: recv, Kind: model.KindMethod}, true, true
}

// declaringInterface finds the named interface whose declaration holds m, by
// following embedded interfaces; "" when it cannot say.
func declaringInterface(n *types.Named, m *types.Func) string {
	it, ok := n.Underlying().(*types.Interface)
	if !ok {
		return ""
	}
	for i := 0; i < it.NumExplicitMethods(); i++ {
		if it.ExplicitMethod(i).Pos() == m.Pos() {
			return n.Obj().Name()
		}
	}
	for i := 0; i < it.NumEmbeddeds(); i++ {
		if en, ok := types.Unalias(it.EmbeddedType(i)).(*types.Named); ok {
			if s := declaringInterface(en, m); s != "" {
				return s
			}
		}
	}
	return ""
}
