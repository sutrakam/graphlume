package multirepo

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strings"
)

// The Go declarations method stitching needs, read from source text.
//
// WHY SOURCE TEXT. A method call `c.Send()` reaches a provider's method only
// through a TYPE: the type the importer states for c, the type a provider
// variable declares, or the type a provider function declares it returns. The
// index records the first (receiver_kind `type`) and neither of the others for
// Go: internal/parse/returns.go leaves Go out of return types because the type
// checker answers Go inside one repository, and no type checker spans two.
// What the index does hold is the text of every file, so both halves are read
// from that text with go/parser — syntax only, no type checking, which is the
// same weight of evidence the parser's own stated types carry.
//
// Nothing here reads the disk: the provider's package text comes from
// GoPackageFiles and the importer's file from FileContent, so the answer is
// about the indexed code and not about whatever a checkout holds now.

// goPkgSyntax is one provider package directory's package-level declarations.
type goPkgSyntax struct {
	// fset holds the positions of every declaration below, for printing.
	fset  *token.FileSet
	types map[string][]goTypeDecl
	vals  map[string][]goValueDecl
	funcs map[string][]goFuncDecl
	// methods is by receiver base type, then method name: the concrete
	// methods declared on T or *T.
	methods map[string]map[string][]goFuncDecl
	// unparsed are files of the package go/parser rejected. A package with
	// one is not read at all: a declaration missing from it would turn a
	// variant into a single candidate, or a method into "no such method".
	unparsed []string
}

// goTypeDecl is one `type T ...` at package level.
type goTypeDecl struct {
	file  string
	line  int
	alias bool
	expr  ast.Expr
	// shape is the printed definition, so two build-constrained declarations
	// of one type can be compared.
	shape string
}

// goValueDecl is one package-level variable or constant.
type goValueDecl struct {
	file string
	line int
	// typ is the declared type, nil when the declaration states none. A
	// constant in a group without its own type takes the previous one, which
	// is the language's rule for `const ( A Level = iota; B )`.
	typ     ast.Expr
	isConst bool
}

// goFuncDecl is one function, method, or interface method declaration.
type goFuncDecl struct {
	file string
	line int
	typ  *ast.FuncType
	// sig is the printed signature, for comparing build-constrained variants.
	sig string
}

// parseGoPackage reads a provider package's files into its declarations.
func parseGoPackage(files map[string]string) *goPkgSyntax {
	pkg := &goPkgSyntax{
		types: map[string][]goTypeDecl{}, vals: map[string][]goValueDecl{},
		funcs: map[string][]goFuncDecl{}, methods: map[string]map[string][]goFuncDecl{},
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	fset := token.NewFileSet()
	pkg.fset = fset
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, files[p], parser.SkipObjectResolution)
		if err != nil {
			pkg.unparsed = append(pkg.unparsed, p)
			continue
		}
		pkg.add(fset, p, f)
	}
	return pkg
}

func (pkg *goPkgSyntax) add(fset *token.FileSet, file string, f *ast.File) {
	line := func(n ast.Node) int { return fset.Position(n.Pos()).Line }
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			fd := goFuncDecl{file: file, line: line(d), typ: d.Type, sig: printNode(fset, d.Type)}
			if d.Recv == nil || len(d.Recv.List) == 0 {
				pkg.funcs[d.Name.Name] = append(pkg.funcs[d.Name.Name], fd)
				continue
			}
			_, recv, ok := namedType(d.Recv.List[0].Type)
			if !ok {
				continue
			}
			if pkg.methods[recv] == nil {
				pkg.methods[recv] = map[string][]goFuncDecl{}
			}
			pkg.methods[recv][d.Name.Name] = append(pkg.methods[recv][d.Name.Name], fd)
		case *ast.GenDecl:
			var lastType ast.Expr
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					pkg.types[s.Name.Name] = append(pkg.types[s.Name.Name], goTypeDecl{
						file: file, line: line(s), alias: s.Assign.IsValid(), expr: s.Type,
						shape: printNode(fset, s.Type),
					})
				case *ast.ValueSpec:
					typ := s.Type
					if d.Tok == token.CONST {
						if typ == nil && len(s.Values) == 0 {
							typ = lastType
						}
						lastType = typ
						if len(s.Values) > 0 && s.Type == nil {
							// `const B = 2` after a typed spec is untyped: only
							// a spec with neither type nor value repeats one.
							lastType = nil
						}
					}
					for _, n := range s.Names {
						pkg.vals[n.Name] = append(pkg.vals[n.Name], goValueDecl{
							file: file, line: line(n), typ: typ, isConst: d.Tok == token.CONST,
						})
					}
				}
			}
		}
	}
}

func printNode(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, n); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// namedType reads a type expression as a named type: T, *T, T[A] and *T[A] are
// T, and pkg.T is T qualified by pkg. ok is false for every other type
// (a slice, a map, a function, a channel, a literal struct, a pointer to a
// pointer), none of which has methods a call could name.
func namedType(e ast.Expr) (qual, name string, ok bool) {
	stars := 0
	for {
		switch t := e.(type) {
		case *ast.ParenExpr:
			e = t.X
		case *ast.StarExpr:
			stars++
			if stars > 1 {
				return "", "", false
			}
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return "", t.Name, true
		case *ast.SelectorExpr:
			x, isIdent := t.X.(*ast.Ident)
			if !isIdent {
				return "", "", false
			}
			return x.Name, t.Sel.Name, true
		default:
			return "", "", false
		}
	}
}

// goTypeShape is what a type's single definition says about its method set.
type goTypeShape struct {
	iface bool
	// ifaceMethods are the methods an interface type declares in its body.
	ifaceMethods map[string][]goFuncDecl
	// fields are the named fields of a struct type; a call `x.F()` on a
	// field of function type is not a method call.
	fields map[string]bool
	// embedded are the types a struct or an interface embeds.
	embedded []ast.Expr
	// definedFrom is the named type a defined type (`type T U`) takes its
	// underlying type from, whose promoted methods are not followed.
	definedFrom string
}

// shape returns the shape of type name, following aliases inside the package.
// ok is false, with the reason, when the type is not declared here, an alias
// leaves the package, or build-constrained declarations disagree.
func (pkg *goPkgSyntax) shape(name string) (resolved string, s goTypeShape, reason, why string) {
	for hops := 0; hops < 4; hops++ {
		decls := pkg.types[name]
		if len(decls) == 0 {
			return "", s, reasonNoSuchDecl, fmt.Sprintf("no type %s is declared in the package", name)
		}
		for _, d := range decls[1:] {
			if d.shape != decls[0].shape || d.alias != decls[0].alias {
				return "", s, reasonAmbiguousType, fmt.Sprintf(
					"%d declarations of type %s differ (build-constrained variants)", len(decls), name)
			}
		}
		d := decls[0]
		if d.alias {
			qual, target, ok := namedType(d.expr)
			switch {
			case ok && qual != "":
				return "", s, reasonForeignType, fmt.Sprintf("%s is an alias of %s.%s, a type of another package", name, qual, target)
			case ok:
				name = target
				continue
			}
		}
		s = goTypeShape{fields: map[string]bool{}, ifaceMethods: map[string][]goFuncDecl{}}
		switch t := d.expr.(type) {
		case *ast.StructType:
			for _, f := range t.Fields.List {
				if len(f.Names) == 0 {
					s.embedded = append(s.embedded, f.Type)
					continue
				}
				for _, n := range f.Names {
					s.fields[n.Name] = true
				}
			}
		case *ast.InterfaceType:
			s.iface = true
			for _, f := range t.Methods.List {
				ft, isFunc := f.Type.(*ast.FuncType)
				if len(f.Names) == 0 || !isFunc {
					// An embedded interface, or a type-set element of a
					// constraint, which a value can never be called through.
					if _, _, ok := namedType(f.Type); ok {
						s.embedded = append(s.embedded, f.Type)
					}
					continue
				}
				for _, n := range f.Names {
					s.ifaceMethods[n.Name] = append(s.ifaceMethods[n.Name], goFuncDecl{
						file: d.file, line: pkg.fset.Position(n.Pos()).Line, typ: ft, sig: printNode(pkg.fset, ft),
					})
				}
			}
		default:
			// `type T U` takes U's fields, and so the methods U's embedded
			// fields promote. A predeclared U other than error (`type Level
			// int`) has no methods to give, and is not a doubt.
			if qual, from, ok := namedType(d.expr); ok &&
				(qual != "" || len(pkg.types[from]) > 0 || from == "error") {
				s.definedFrom = from
				if qual != "" {
					s.definedFrom = qual + "." + from
				}
			}
		}
		return name, s, "", ""
	}
	return "", s, reasonNoSuchDecl, fmt.Sprintf("the aliases of %s do not end in a type of the package", name)
}

// goFileSyntax is one importer file, parsed once.
type goFileSyntax struct {
	fset *token.FileSet
	file *ast.File
}

// importerFile returns the parsed text of one of the member's files, or nil
// when it does not parse: a file the compiler would reject has no variable
// whose initialiser can be trusted.
func (gm *goMember) importerFile(ctx context.Context, path string) (*goFileSyntax, error) {
	if gm.fileSyntax == nil {
		gm.fileSyntax = map[string]*goFileSyntax{}
	}
	if fs, ok := gm.fileSyntax[path]; ok {
		return fs, nil
	}
	text, err := gm.src.FileContent(ctx, gm.runID, path)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		// An indexed file can lack stored text (a file too large to keep,
		// or one indexed before its text was). Its value calls are then
		// untraced, which is what they were before tracing existed; failing
		// the whole report over one file would be worse.
		gm.fileSyntax[path] = nil
		return nil, nil
	}
	fset := token.NewFileSet()
	f, perr := parser.ParseFile(fset, path, text, parser.SkipObjectResolution)
	var fs *goFileSyntax
	if perr == nil {
		fs = &goFileSyntax{fset: fset, file: f}
	}
	gm.fileSyntax[path] = fs
	return fs, nil
}

// providerPackage returns the declarations of one of the member's package
// directories, parsed once.
func (gm *goMember) providerPackage(ctx context.Context, dir string) (*goPkgSyntax, error) {
	if gm.pkgSyntax == nil {
		gm.pkgSyntax = map[string]*goPkgSyntax{}
	}
	if p, ok := gm.pkgSyntax[dir]; ok {
		return p, nil
	}
	files, err := gm.src.GoPackageFiles(ctx, gm.runID, dir)
	if err != nil {
		return nil, fmt.Errorf("multirepo: member %q: read the Go files of %s: %w", gm.name, orDot(dir), err)
	}
	texts := make(map[string]string, len(files))
	for _, f := range files {
		texts[f.Path] = f.Content
	}
	p := parseGoPackage(texts)
	gm.pkgSyntax[dir] = p
	return p, nil
}

// valueTrace is what the importer's source says about the local a call is
// made on: the one provider-qualified call it was initialised from, or why
// there is none.
type valueTrace struct {
	// qual and fn are the producing call `qual.fn(...)`; qual is "" when the
	// value was not initialised from a package-qualified call at all.
	qual, fn string
	// at is where the variable is declared, for the leg's detail.
	at int
	// reason is "" when the trace holds, else reasonReassigned or
	// reasonSeveralValues (qual is still set, so the call is counted against
	// the provider it came from), or reasonUntraced.
	reason, why string
	// selfCalls are the methods of `x = x.M(...)` reassignments, which keep
	// the trace only when M returns the variable's own type.
	selfCalls []string
}

// traceValue reads, in the importer's file, where the local `name` that a
// call at line:col is made on comes from.
//
// It applies the rules the in-repository resolver applies to a local typed by
// what a call returns (internal/parse/shape.go producerOf and reassigned, and
// internal/resolve/infer/returned.go), because they are the rules this tool
// already holds itself to for that claim:
//
//   - one variable, declared once in the function with no stated type, from
//     a value that is exactly one call `pkg.F(...)`: no chain, no conversion
//     of a call's result, no other expression;
//   - several names from one call (`c, err := lib.Open()`) give none of them
//     a type: the in-repository walk binds no producer for a multi-name
//     declaration, so neither is the first value taken here;
//   - any later assignment to the variable withdraws the producer, wherever
//     it sits in the function, except `x = x.M(...)`, which keeps it when M
//     is declared to return the variable's own type.
//
// The function searched is the outermost one holding the call, closures
// included, so a variable captured by a closure is seen with every
// assignment made to it.
func traceValue(fs *goFileSyntax, line, col int, name string) valueTrace {
	untraced := func(why string) valueTrace { return valueTrace{reason: reasonUntraced, why: why} }
	if fs == nil {
		return untraced("the importing file could not be parsed")
	}
	tf := fs.fset.File(fs.file.Pos())
	if tf == nil || line < 1 || line > tf.LineCount() {
		return untraced("the call's line is outside the indexed file")
	}
	pos := tf.LineStart(line) + token.Pos(col)
	if pos >= token.Pos(tf.Base()+tf.Size()) {
		pos = tf.LineStart(line)
	}
	root := outermostFunc(fs.file, pos)
	if root == nil {
		return untraced("the call is not inside a function")
	}

	type decl struct {
		node  ast.Node // the AssignStmt or ValueSpec, nil for a parameter or range variable
		scope ast.Node
		names int
		value ast.Expr
		typed bool
	}
	var decls []decl
	var reassigns []*ast.AssignStmt
	otherWrites := 0
	qualDecls := map[string]int{}
	var stack []ast.Node
	nearestScope := func() ast.Node {
		for i := len(stack) - 2; i >= 0; i-- {
			switch stack[i].(type) {
			case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
				*ast.TypeSwitchStmt, *ast.CaseClause, *ast.CommClause, *ast.FuncLit, *ast.FuncDecl:
				return stack[i]
			}
		}
		return root
	}
	declared := func(id string) {
		qualDecls[id]++
	}
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Recv != nil {
				for _, f := range x.Recv.List {
					for _, id := range f.Names {
						declared(id.Name)
						if id.Name == name {
							decls = append(decls, decl{scope: x})
						}
					}
				}
			}
		case *ast.FuncType:
			// Only a function's own parameters; a func type written as a
			// type (`var f func(c int)`) declares no variable.
			if len(stack) < 2 {
				break
			}
			switch stack[len(stack)-2].(type) {
			case *ast.FuncDecl, *ast.FuncLit:
			default:
				return true
			}
			for _, fl := range []*ast.FieldList{x.Params, x.Results} {
				if fl == nil {
					continue
				}
				for _, f := range fl.List {
					for _, id := range f.Names {
						declared(id.Name)
						if id.Name == name {
							decls = append(decls, decl{scope: stack[len(stack)-2]})
						}
					}
				}
			}
		case *ast.AssignStmt:
			for i, l := range x.Lhs {
				id, ok := l.(*ast.Ident)
				if !ok {
					continue
				}
				if x.Tok == token.DEFINE {
					declared(id.Name)
				}
				if id.Name != name {
					continue
				}
				if x.Tok != token.DEFINE {
					reassigns = append(reassigns, x)
					continue
				}
				decls = append(decls, decl{node: x, scope: nearestScope(), names: len(x.Lhs),
					value: valueFor(x.Rhs, i, len(x.Lhs))})
			}
		case *ast.ValueSpec:
			for i, id := range x.Names {
				declared(id.Name)
				if id.Name != name {
					continue
				}
				decls = append(decls, decl{node: x, scope: nearestScope(), names: len(x.Names),
					typed: x.Type != nil, value: valueFor(x.Values, i, len(x.Names))})
			}
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{x.Key, x.Value} {
				id, ok := e.(*ast.Ident)
				if !ok {
					continue
				}
				if x.Tok == token.DEFINE {
					declared(id.Name)
				}
				if id.Name != name {
					continue
				}
				if x.Tok == token.DEFINE {
					decls = append(decls, decl{scope: x})
				} else {
					otherWrites++
				}
			}
		case *ast.IncDecStmt:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == name {
				otherWrites++
			}
		}
		return true
	})

	if len(decls) != 1 {
		if len(decls) == 0 {
			return untraced(fmt.Sprintf("%s is not declared in the function", name))
		}
		return untraced(fmt.Sprintf("%s is declared %d times in the function", name, len(decls)))
	}
	d := decls[0]
	if d.node == nil || d.typed || d.value == nil {
		return untraced(fmt.Sprintf("%s is not initialised from one call", name))
	}
	if d.node.End() > pos || pos > d.scope.End() {
		return untraced(fmt.Sprintf("the call is outside the scope of %s", name))
	}
	call, ok := unparen(d.value).(*ast.CallExpr)
	if !ok {
		return untraced(fmt.Sprintf("%s is not initialised from a call", name))
	}
	sel, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return untraced(fmt.Sprintf("%s is not initialised from a package-qualified call", name))
	}
	q, ok := sel.X.(*ast.Ident)
	if !ok {
		return untraced(fmt.Sprintf("%s is initialised from a chained call", name))
	}
	if qualDecls[q.Name] > 0 {
		// A local named like the import hides it: q.F is then a method of a
		// local value, not a function of the package.
		return untraced(fmt.Sprintf("%s in %s's initialiser is a local, not a package", q.Name, name))
	}
	t := valueTrace{qual: q.Name, fn: sel.Sel.Name, at: tf.Position(d.node.Pos()).Line}
	if d.names > 1 {
		// Even `a, b := f(), g()`, where a does hold f's result: the
		// in-repository walk binds no producer for any multi-name
		// declaration, and a cross-repository link claims no more than it.
		t.reason, t.why = reasonSeveralValues, fmt.Sprintf(
			"%s is one of %d names declared together from %s.%s(), and none of them is taken to hold its result",
			name, d.names, q.Name, sel.Sel.Name)
		return t
	}
	if otherWrites > 0 {
		t.reason, t.why = reasonReassigned, fmt.Sprintf("%s is assigned again in the function", name)
		return t
	}
	for _, r := range reassigns {
		m, ok := selfCall(r, name)
		if !ok {
			t.reason, t.why = reasonReassigned, fmt.Sprintf("%s is assigned again at line %d",
				name, tf.Position(r.Pos()).Line)
			return t
		}
		t.selfCalls = append(t.selfCalls, m)
	}
	sort.Strings(t.selfCalls)
	return t
}

// valueFor is the expression the i-th of n declared names is given: its own
// value when each name has one (`a, b := f(), g()`), the one call when all
// share it (`a, b := f()`), nil otherwise.
func valueFor(values []ast.Expr, i, n int) ast.Expr {
	switch {
	case len(values) == n:
		return values[i]
	case len(values) == 1:
		return values[0]
	default:
		return nil
	}
}

// selfCall reports whether an assignment is `name = name.M(...)` and nothing
// else, returning M: the one reassignment the in-repository resolver lets a
// producer survive (internal/parse/shape.go selfCall).
func selfCall(a *ast.AssignStmt, name string) (string, bool) {
	if a.Tok != token.ASSIGN || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
		return "", false
	}
	call, ok := unparen(a.Rhs[0]).(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok || recv.Name != name {
		return "", false
	}
	return sel.Sel.Name, true
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// outermostFunc is the top-level function declaration, or the function
// literal in a package-level declaration, whose span holds pos.
func outermostFunc(f *ast.File, pos token.Pos) ast.Node {
	for _, d := range f.Decls {
		if pos < d.Pos() || pos > d.End() {
			continue
		}
		if fd, ok := d.(*ast.FuncDecl); ok {
			if fd.Body == nil {
				return nil
			}
			return fd
		}
		var lit ast.Node
		ast.Inspect(d, func(n ast.Node) bool {
			if lit != nil {
				return false
			}
			if fl, ok := n.(*ast.FuncLit); ok && pos >= fl.Pos() && pos <= fl.End() {
				lit = fl
				return false
			}
			return true
		})
		return lit
	}
	return nil
}
