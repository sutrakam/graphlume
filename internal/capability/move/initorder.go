package move

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// Initialisation order is the one thing a move inside a package can change.
//
// Go initialises package-level variables in declaration order, taking the
// files in the order the go command hands them to the compiler (by name), and
// deferring a variable only until the variables its initialiser refers to are
// ready. Moving a variable to another file therefore changes its place in
// that order, and with it the order in which the package's initialisers run.
//
// That is invisible exactly when no initialiser in the package runs code: no
// function is called, so nothing can observe a half-initialised package, and
// every order produces the same values. It is not provably invisible
// otherwise — an initialiser that registers a flag, or reads through an
// interface a variable the dependency analysis cannot see — so a variable
// with an initialiser is moved only in a package whose initialisers are all
// inert, and refused, naming the initialiser that is not, everywhere else.
//
// Functions, types, constants and variables without an initialiser are not
// affected: none of them is "initialised" in an order.

// inertCalls are the standard-library functions an initialiser may call and
// still count as inert: each computes a value from its arguments, calls
// nothing of the package's, and touches no shared state.
var inertCalls = map[string]bool{
	"errors.New":                true,
	"math/big.NewInt":           true,
	"net/netip.MustParseAddr":   true,
	"net/netip.MustParsePrefix": true,
	"regexp.MustCompile":        true,
	"regexp.MustCompilePOSIX":   true,
	"strings.NewReplacer":       true,
	"sync.OnceFunc":             true,
	"sync.OnceValue":            true,
	"sync.OnceValues":           true,
	"time.Duration":             true,
	"unsafe.Alignof":            true,
	"unsafe.Offsetof":           true,
	"unsafe.Sizeof":             true,
}

// literalOnlyCalls are inert when every argument is a literal: given a value
// of one of the package's types they would call its String or Error method.
var literalOnlyCalls = map[string]bool{
	"fmt.Errorf":  true,
	"fmt.Sprint":  true,
	"fmt.Sprintf": true,
}

// inertBuiltins are the predeclared functions that only compute. append is
// not among them: it can write into the array of a slice another variable
// holds.
var inertBuiltins = map[string]bool{
	"cap": true, "complex": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "real": true,
}

// universeTypes are the predeclared types; calling one is a conversion.
var universeTypes = map[string]bool{
	"any": true, "bool": true, "byte": true, "complex64": true, "complex128": true, "error": true,
	"float32": true, "float64": true, "int": true, "int8": true, "int16": true, "int32": true,
	"int64": true, "rune": true, "string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true,
}

// checkInitOrder refuses the move of a variable with an initialiser unless
// every variable initialiser in the package is inert.
func checkInitOrder(src *parsed, t *located, pkg *pkgNames) error {
	if len(t.values) == 0 {
		return nil
	}
	for _, f := range pkg.files {
		quals := qualifierPaths(f)
		for _, d := range f.ast.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, s := range gen.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					pos, what := runsCode(v, pkg, quals)
					if what == "" {
						continue
					}
					names := make([]string, len(vs.Names))
					for i, id := range vs.Names {
						names[i] = id.Name
					}
					return refuse(src.path, t.decl.Line, "moving a variable changes the order in which the package's "+
						"variables are initialised (declaration order, files taken by name), and the initialiser of "+
						"%s at %s:%d %s, so the move could change what the program does at start-up. "+
						"A variable with an initialiser is moved only when no initialiser in the package runs code",
						strings.Join(names, ", "), f.path, f.line(pos), what)
				}
			}
		}
	}
	return nil
}

// qualifierPaths maps the names a file's code can write before a dot to the
// standard-library import paths they stand for. Only the standard library
// matters here: nothing else is on the inert list.
func qualifierPaths(f *parsed) map[string]string {
	out := make(map[string]string)
	for _, s := range importsOf(f, nil) {
		if _, std := stdName(s.path); std && s.qualifies() {
			out[s.name] = s.path
		}
	}
	return out
}

// runsCode reports the first thing in an initialiser that executes code
// which could observe or change the program's state, as a position and a
// phrase, or "" when the initialiser is inert.
func runsCode(expr ast.Expr, pkg *pkgNames, quals map[string]string) (token.Pos, string) {
	var (
		at   token.Pos
		what string
	)
	ast.Inspect(expr, func(n ast.Node) bool {
		if what != "" {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false // a function value is not a call; calling it is, and is caught as one
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				at, what = n.Pos(), "receives from a channel"
			}
		case *ast.CallExpr:
			if !inertCall(n, pkg, quals) {
				at, what = n.Pos(), fmt.Sprintf("calls %s", callee(n.Fun))
			}
		}
		return what == ""
	})
	return at, what
}

// inertCall reports whether a call is a conversion, an inert builtin or an
// inert standard-library function.
func inertCall(call *ast.CallExpr, pkg *pkgNames, quals map[string]string) bool {
	switch fn := call.Fun.(type) {
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType, *ast.StructType:
		return true // a conversion to a type written out in full
	case *ast.ParenExpr:
		// (*T)(x), the conversion in "var _ I = (*T)(nil)".
		if star, ok := fn.X.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); ok {
				return isType(id, pkg)
			}
		}
		return false
	case *ast.Ident:
		if isType(fn, pkg) {
			return true
		}
		return fn.Obj == nil && !pkg.all[fn.Name] && inertBuiltins[fn.Name]
	case *ast.SelectorExpr:
		q, ok := fn.X.(*ast.Ident)
		if !ok || q.Obj != nil || pkg.all[q.Name] {
			return false
		}
		path, ok := quals[q.Name]
		if !ok {
			return false
		}
		key := path + "." + fn.Sel.Name
		if literalOnlyCalls[key] {
			for _, a := range call.Args {
				if _, lit := a.(*ast.BasicLit); !lit {
					return false
				}
			}
			return true
		}
		return inertCalls[key]
	}
	return false
}

// isType reports whether id names a type: one the package declares, or a
// predeclared one nothing in the package shadows.
func isType(id *ast.Ident, pkg *pkgNames) bool {
	if id.Obj != nil {
		return id.Obj.Kind == ast.Typ
	}
	if pkg.all[id.Name] {
		return pkg.types[id.Name]
	}
	return universeTypes[id.Name]
}

// callee renders what a call calls, for a refusal.
func callee(fun ast.Expr) string {
	switch fn := fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name + "." + fn.Sel.Name
		}
		return "a method (" + fn.Sel.Name + ")"
	case *ast.FuncLit:
		return "a function literal"
	}
	return "a function"
}
