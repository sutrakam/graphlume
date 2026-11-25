package gotypes

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// extractor turns one checked package into facts for its report files.
type extractor struct {
	l    *loader
	info *types.Info
	pkg  *types.Package

	callee    map[*ast.Ident]bool              // identifier is the callee of a call expression
	construct map[*ast.Ident]bool              // identifier names the type of a composite literal
	selOf     map[*ast.Ident]*ast.SelectorExpr // the selector an identifier is the Sel of
	skip      map[*ast.Ident]bool              // receiver type identifiers: declarations, not uses
}

func (x *extractor) run(files []*ast.File, report map[string]bool) {
	l := x.l
	for _, f := range files {
		abs := l.fset.File(f.Pos()).Name()
		if !report[abs] {
			continue
		}
		rel, ok := l.repoFile[abs]
		if !ok || !l.report[rel] {
			continue
		}
		ff := l.out.file(rel)
		x.callee = map[*ast.Ident]bool{}
		x.construct = map[*ast.Ident]bool{}
		x.selOf = map[*ast.Ident]*ast.SelectorExpr{}
		x.skip = map[*ast.Ident]bool{}

		x.defs(f, rel, ff)
		if x.dotImportsStub(f) {
			continue
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				encl, ok := x.declRef(d, rel)
				x.walk(d, ok, encl, rel, ff)
			case *ast.GenDecl:
				for _, s := range d.Specs {
					x.walkSpec(d, s, rel, ff)
				}
			}
		}
	}
}

// dotImportsStub reports whether a file dot-imports a package this run
// stubbed: its unqualified names then resolve nowhere, and an identifier that
// resolved to something else instead cannot be told from the real one.
func (x *extractor) dotImportsStub(f *ast.File) bool {
	for _, im := range f.Imports {
		if im.Name == nil || im.Name.Name != "." {
			continue
		}
		p := strings.Trim(im.Path.Value, "\"`")
		if s, ok := x.l.stubs[p]; ok && x.l.stubSet[s] {
			return true
		}
	}
	return false
}

// defs records every package-level declaration of a file: the symbols a use
// can resolve to and the symbols a use can sit inside.
func (x *extractor) defs(f *ast.File, rel string, ff *FileFact) {
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if r, ok := x.declRef(d, rel); ok {
				ff.Defs = append(ff.Defs, r)
				x.l.stats.Defs++
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					kind := model.KindClass
					if _, ok := s.Type.(*ast.InterfaceType); ok {
						kind = model.KindInterface
					}
					ff.Defs = append(ff.Defs, x.ref(s.Name, rel, "", kind))
					x.l.stats.Defs++
					if it, ok := s.Type.(*ast.InterfaceType); ok && it.Methods != nil {
						for _, m := range it.Methods.List {
							for _, n := range m.Names {
								ff.Defs = append(ff.Defs, x.ref(n, rel, s.Name.Name, model.KindMethod))
								x.l.stats.Defs++
							}
						}
					}
				case *ast.ValueSpec:
					kind := model.KindVariable
					if d.Tok == token.CONST {
						kind = model.KindConstant
					}
					for _, n := range s.Names {
						if n.Name == "_" {
							continue
						}
						ff.Defs = append(ff.Defs, x.ref(n, rel, "", kind))
						x.l.stats.Defs++
					}
				}
			}
		}
	}
}

func (x *extractor) ref(id *ast.Ident, rel, recv string, kind model.EntityKind) ObjRef {
	p := x.l.pos(id.Pos())
	return ObjRef{File: rel, Line: p.Line, Col: p.Column - 1, Name: id.Name, Recv: recv, Kind: kind}
}

// declRef is the ObjRef of a function or method declaration.
func (x *extractor) declRef(d *ast.FuncDecl, rel string) (ObjRef, bool) {
	if d.Name == nil || d.Name.Name == "_" {
		return ObjRef{}, false
	}
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return x.ref(d.Name, rel, "", model.KindFunction), true
	}
	recv := recvBase(d.Recv.List[0].Type)
	if recv == "" {
		return ObjRef{}, false
	}
	return x.ref(d.Name, rel, recv, model.KindMethod), true
}

func recvBase(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.ParenExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// walkSpec walks a package-level var, const or type, attributing each initialiser
// to the variable it initialises.
func (x *extractor) walkSpec(d *ast.GenDecl, s ast.Spec, rel string, ff *FileFact) {
	switch s := s.(type) {
	case *ast.TypeSpec:
		x.walk(s, true, x.ref(s.Name, rel, "", model.KindClass), rel, ff)
	case *ast.ValueSpec:
		kind := model.KindVariable
		if d.Tok == token.CONST {
			kind = model.KindConstant
		}
		first, firstOK := ObjRef{}, false
		for _, n := range s.Names {
			if n.Name != "_" {
				first, firstOK = x.ref(n, rel, "", kind), true
				break
			}
		}
		if s.Type != nil {
			x.walk(s.Type, firstOK, first, rel, ff)
		}
		for i, v := range s.Values {
			encl, ok := first, firstOK
			if len(s.Values) == len(s.Names) && s.Names[i].Name != "_" {
				encl, ok = x.ref(s.Names[i], rel, "", kind), true
			}
			x.walk(v, ok, encl, rel, ff)
		}
	}
}

// walk visits a subtree, marking callees and composite-literal types first
// (pre-order, so the marks exist before the identifiers are reached) and
// recording every use of a repository-declared object.
func (x *extractor) walk(root ast.Node, haveEncl bool, encl ObjRef, rel string, ff *FileFact) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if id := calleeIdent(n.Fun); id != nil {
				x.callee[id] = true
			}
		case *ast.CompositeLit:
			if id := calleeIdent(n.Type); id != nil {
				x.construct[id] = true
			}
		case *ast.SelectorExpr:
			x.selOf[n.Sel] = n
		case *ast.FuncDecl:
			if n.Recv != nil {
				ast.Inspect(n.Recv, func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok {
						x.skip[id] = true
					}
					return true
				})
			}
		case *ast.Ident:
			x.use(n, haveEncl, encl, rel, ff)
		}
		return true
	})
}

// calleeIdent finds the identifier a call (or a composite literal's type)
// names: through parentheses and a generic instantiation, to the plain name or
// the Sel of a qualified or method selector.
func calleeIdent(e ast.Expr) *ast.Ident {
	for e != nil {
		switch t := e.(type) {
		case *ast.ParenExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t
		case *ast.SelectorExpr:
			return t.Sel
		default:
			return nil
		}
	}
	return nil
}

// use records one identifier if it resolves to a repository-declared object,
// and, when the identifier is the callee of a call, what the compiler knows
// about that call whether or not it is a node of the graph.
func (x *extractor) use(id *ast.Ident, haveEncl bool, encl ObjRef, rel string, ff *FileFact) {
	if x.skip[id] {
		return
	}
	l := x.l
	p := l.pos(id.Pos())
	isCallee := x.callee[id]
	site := Callee{Line: p.Line, Col: p.Column - 1, Name: id.Name}
	record := func() {
		if isCallee {
			ff.Callees = append(ff.Callees, site)
		}
	}
	obj := x.info.Uses[id]
	if obj == nil {
		record() // not Known: the checker could not say what this call is
		return
	}
	site.Known = true
	target, repo, emit := l.objRef(obj)
	if !emit {
		record()
		return
	}
	if !repo {
		l.stats.External++
		record()
		return
	}
	if sel := x.selOf[id]; sel != nil {
		if s := x.info.Selections[sel]; s != nil {
			tainted := l.promotionTainted(s.Recv(), len(s.Index()))
			if _, isFunc := obj.(*types.Func); isFunc && !tainted {
				tainted = l.ifaceMemberTainted(s.Recv(), id.Name)
			}
			if tainted {
				l.stats.WithheldTaint++
				site.Known = false
				record()
				return
			}
		}
	}
	site.Dst = target.FQN()
	record()
	if target.Kind == model.KindMethod && target.Recv == "" {
		l.addLocalDef(target)
	}
	if !haveEncl {
		l.stats.NoEnclosing++
		return
	}
	u := Use{
		File: rel, Line: p.Line, Col: p.Column - 1,
		Obj: target, Encl: encl,
	}
	switch target.Kind {
	case model.KindFunction, model.KindMethod:
		u.Call = isCallee
	case model.KindClass:
		u.Construct = isCallee || x.construct[id]
	}
	ff.Uses = append(ff.Uses, u)
	l.stats.Uses++
	switch u.Rel() {
	case model.EdgeCalls:
		l.stats.Calls++
	case model.EdgeInstantiates:
		l.stats.Constructs++
	}
}

// addLocalDef records the declaration of a method of an interface that is not
// a package-level named type (an interface written inline in a signature or
// declared inside a function). The index holds such a method as a symbol, so a
// use of it can land, but no top-level declaration walk reaches it.
func (l *loader) addLocalDef(r ObjRef) {
	if !l.report[r.File] {
		return
	}
	key := r.FQN()
	if l.localDefs[key] {
		return
	}
	l.localDefs[key] = true
	ff := l.out.file(r.File)
	ff.Defs = append(ff.Defs, r)
	l.stats.Defs++
}

// objRef maps a types.Object to the declaration it stands for. emit is false
// for objects that are never a node of the graph (locals, fields, packages,
// builtins, type parameters); repo is false for objects declared outside the
// repository (the standard library, a dependency).
func (l *loader) objRef(obj types.Object) (ref ObjRef, repo, emit bool) {
	pkg := obj.Pkg()
	if pkg == nil {
		return ObjRef{}, false, false
	}
	switch o := obj.(type) {
	case *types.Func:
		o = o.Origin()
		obj = o
		sig, _ := o.Type().(*types.Signature)
		if sig == nil {
			return ObjRef{}, false, false
		}
		if recv := sig.Recv(); recv != nil {
			named, isNamed := derefType(recv.Type()).(*types.Named)
			switch {
			case isNamed && named.Origin().Obj().Parent() == named.Origin().Obj().Pkg().Scope():
				ref = ObjRef{Name: o.Name(), Recv: named.Origin().Obj().Name(), Kind: model.KindMethod}
			case isInterfaceType(recv.Type()):
				// A method of an interface written inline or declared in a
				// function body: it has no receiver name the index could hold.
				ref = ObjRef{Name: o.Name(), Kind: model.KindMethod}
			default:
				return ObjRef{}, false, false
			}
		} else {
			if o.Parent() != pkg.Scope() {
				return ObjRef{}, false, false
			}
			ref = ObjRef{Name: o.Name(), Kind: model.KindFunction}
		}
	case *types.TypeName:
		if o.Parent() != pkg.Scope() {
			return ObjRef{}, false, false
		}
		ref = ObjRef{Name: o.Name(), Kind: model.KindClass}
		if _, ok := o.Type().Underlying().(*types.Interface); ok {
			ref.Kind = model.KindInterface
		}
	case *types.Var:
		o = o.Origin()
		obj = o
		if o.IsField() || o.Parent() != pkg.Scope() {
			return ObjRef{}, false, false
		}
		ref = ObjRef{Name: o.Name(), Kind: model.KindVariable}
	case *types.Const:
		if o.Parent() != pkg.Scope() {
			return ObjRef{}, false, false
		}
		ref = ObjRef{Name: o.Name(), Kind: model.KindConstant}
	default:
		return ObjRef{}, false, false
	}
	p := l.pos(obj.Pos())
	rel, ok := l.repoFile[p.Filename]
	if !ok {
		return ObjRef{}, false, true
	}
	ref.File, ref.Line, ref.Col = rel, p.Line, p.Column-1
	return ref, true, true
}

func isInterfaceType(t types.Type) bool {
	_, ok := t.Underlying().(*types.Interface)
	return ok
}
