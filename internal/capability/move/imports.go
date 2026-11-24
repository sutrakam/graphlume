package move

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// importSpec is one import of a file.
type importSpec struct {
	spec *ast.ImportSpec
	decl *ast.GenDecl
	path string
	// alias is the name written in front of the path: "", "_", "." or an
	// identifier.
	alias string
	// name is what code in the file writes before the dot: the alias, or the
	// package's own name. It is "" for a blank or dot import, and for an
	// import whose package name is not known.
	name string
}

// qualifies reports whether code can write name-dot to reach this import.
func (s importSpec) qualifies() bool { return s.name != "" }

// unknown: an ordinary import whose package name could not be established.
func (s importSpec) unknown() bool { return s.alias == "" && s.name == "" }

// text is the spec as it is written into an import declaration.
func (s importSpec) text() string {
	if s.alias != "" {
		return s.alias + " " + s.spec.Path.Value
	}
	return s.spec.Path.Value
}

// stdRoots are the first path elements of the standard library. An import
// path that starts with one of them, in a file with no go command to ask, is
// taken to be the standard library's, where a package is named after its
// directory. A root added by a future Go release is simply not known here,
// which errs towards a refusal.
var stdRoots = map[string]bool{
	"archive": true, "bufio": true, "bytes": true, "cmp": true, "compress": true, "container": true,
	"context": true, "crypto": true, "database": true, "debug": true, "embed": true, "encoding": true,
	"errors": true, "expvar": true, "flag": true, "fmt": true, "go": true, "hash": true, "html": true,
	"image": true, "index": true, "io": true, "iter": true, "log": true, "maps": true, "math": true,
	"mime": true, "net": true, "os": true, "path": true, "plugin": true, "reflect": true, "regexp": true,
	"runtime": true, "slices": true, "sort": true, "strconv": true, "strings": true, "structs": true,
	"sync": true, "syscall": true, "testing": true, "text": true, "time": true, "unicode": true,
	"unique": true, "unsafe": true, "weak": true,
}

// stdName is the package name of a standard-library import path: its last
// element, or the one before a major-version element (math/rand/v2 is rand).
func stdName(path string) (string, bool) {
	parts := strings.Split(path, "/")
	if !stdRoots[parts[0]] {
		return "", false
	}
	last := parts[len(parts)-1]
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		last = parts[len(parts)-2]
	}
	if !token.IsIdentifier(last) {
		return "", false
	}
	return last, true
}

// importsOf lists a file's imports with the names code uses for them.
func importsOf(p *parsed, names map[string]string) []importSpec {
	var out []importSpec
	for _, d := range p.ast.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		for _, s := range gen.Specs {
			is, ok := s.(*ast.ImportSpec)
			if !ok {
				continue
			}
			path, err := strconv.Unquote(is.Path.Value)
			if err != nil {
				continue
			}
			imp := importSpec{spec: is, decl: gen, path: path}
			switch {
			case is.Name != nil:
				imp.alias = is.Name.Name
				if imp.alias != "_" && imp.alias != "." {
					imp.name = imp.alias
				}
			case names[path] != "":
				imp.name = names[path]
			default:
				imp.name, _ = stdName(path)
			}
			out = append(out, imp)
		}
	}
	return out
}

// UnnamedImports returns the import paths the given Go files import without
// an explicit name, sorted. Those are the paths whose package names the
// caller should ask the go command for before calling Compute. A file that
// does not parse contributes nothing; Compute refuses it.
func UnnamedImports(files ...[]byte) []string {
	seen := make(map[string]bool)
	for _, src := range files {
		f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, s := range f.Imports {
			if path, err := strconv.Unquote(s.Path.Value); err == nil && s.Name == nil && path != "C" {
				seen[path] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// names are the identifiers a subtree leaves for its surroundings to
// declare, by where they stand.
type names struct {
	// qualifiers are unresolved identifiers before a dot: a package name, or
	// a package-level value whose field or method is being selected.
	qualifiers map[string]token.Pos
	// bare are every other unresolved identifier: package-level names from
	// another file, universe names, names a dot-import supplies — and, since
	// syntax alone cannot tell them apart, struct-literal keys.
	bare map[string]token.Pos
}

// namesIn collects the unresolved identifiers under each root. skip, when
// not nil, is a subtree left out.
func namesIn(skip ast.Node, roots ...ast.Node) names {
	out := names{qualifiers: map[string]token.Pos{}, bare: map[string]token.Pos{}}
	note := func(m map[string]token.Pos, id *ast.Ident) {
		if id.Obj != nil || id.Name == "_" {
			return
		}
		if _, ok := m[id.Name]; !ok {
			m[id.Name] = id.Pos()
		}
	}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if n == nil || (skip != nil && n == skip) {
			return false
		}
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok {
				note(out.qualifiers, id)
			} else {
				ast.Inspect(n.X, visit)
			}
			return false // n.Sel is a field or method name, never a reference
		case *ast.Ident:
			note(out.bare, n)
		}
		return true
	}
	for _, r := range roots {
		ast.Inspect(r, visit)
	}
	return out
}

// referencing returns the parts of a declaration that can refer to other
// declarations: all of it, except that a function's own name is left out (a
// method's name is declared nowhere the parser resolves, and would read as a
// reference to something else).
func referencing(n ast.Node) []ast.Node {
	fn, ok := n.(*ast.FuncDecl)
	if !ok {
		return []ast.Node{n}
	}
	var out []ast.Node
	if fn.Recv != nil {
		out = append(out, fn.Recv)
	}
	if fn.Type != nil {
		out = append(out, fn.Type)
	}
	if fn.Body != nil {
		out = append(out, fn.Body)
	}
	return out
}

// importDecls returns a file's import declarations.
func importDecls(p *parsed) []*ast.GenDecl {
	var out []*ast.GenDecl
	for _, d := range p.ast.Decls {
		if gen, ok := d.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			out = append(out, gen)
		}
	}
	return out
}

// universe is every predeclared identifier.
var universe = map[string]bool{
	"any": true, "bool": true, "byte": true, "comparable": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true, "int8": true, "int16": true, "int32": true,
	"int64": true, "rune": true, "string": true, "uint": true, "uint8": true, "uint16": true, "uint32": true,
	"uint64": true, "uintptr": true,
	"true": true, "false": true, "iota": true, "nil": true,
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true, "delete": true,
	"imag": true, "len": true, "make": true, "max": true, "min": true, "new": true, "panic": true,
	"print": true, "println": true, "real": true, "recover": true,
}

// pkgNames are the names a package declares at top level, across its files.
type pkgNames struct {
	all   map[string]bool
	types map[string]bool
	files []*parsed // every file of the package, the source and target included
}

// packageNames parses the directory's other files and lists what the package
// declares. Files of another package (an external _test package, a
// "//go:build ignore" program) are skipped.
func packageNames(fset *token.FileSet, req Request, src, dst *parsed, siblings []File) (*pkgNames, error) {
	pkg := &pkgNames{all: map[string]bool{}, types: map[string]bool{}, files: []*parsed{src}}
	if dst != nil {
		pkg.files = append(pkg.files, dst)
	}
	dir := ""
	if i := strings.LastIndexByte(req.From, '/'); i >= 0 {
		dir = req.From[:i+1]
	}
	for _, s := range siblings {
		f, err := parseFile(fset, dir+s.Name, s.Data)
		if err != nil {
			return nil, refuse(dir+s.Name, 0, "this file of the package does not parse, so the names the package "+
				"declares cannot be listed and the moved code's imports cannot be told from them")
		}
		if f.ast.Name.Name == src.ast.Name.Name {
			pkg.files = append(pkg.files, f)
		}
	}
	for _, f := range pkg.files {
		for _, d := range f.ast.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					pkg.all[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					for _, id := range specNames(s) {
						pkg.all[id.Name] = true
						if d.Tok == token.TYPE {
							pkg.types[id.Name] = true
						}
					}
				}
			}
		}
	}
	return pkg, nil
}

// importPlan is what the move does to the two import lists.
type importPlan struct {
	add    []importSpec // the source's specs the target needs and does not have
	remove []importSpec // the source's specs nothing in it uses once the declaration is gone
}

// planImports works out which imports the moved code needs and which the
// source stops needing. Every identifier the moved code leaves for its file
// to supply must be accounted for — an import with a known name, or a name
// the package declares — or the move is refused: an unexplained identifier
// is exactly where a missing import would hide.
func planImports(src, dst *parsed, t *located, pkg *pkgNames, pkgName map[string]string) (*importPlan, error) {
	srcImps := importsOf(src, pkgName)
	used := namesIn(nil, referencing(t.node)...)

	var unknown, dots []importSpec
	for _, s := range srcImps {
		switch {
		case s.unknown():
			unknown = append(unknown, s)
		case s.alias == ".":
			dots = append(dots, s)
		}
	}

	var needed []importSpec
	for _, q := range sortedKeys(used.qualifiers) {
		line := src.line(used.qualifiers[q])
		var bound []importSpec
		for _, s := range srcImps {
			if s.qualifies() && s.name == q {
				bound = append(bound, s)
			}
		}
		switch {
		case len(bound) > 0 && pkg.all[q]:
			return nil, refuse(src.path, line, "%s is both the name of an import in this file and a name the "+
				"package declares, so what %s.… refers to in the moved code cannot be established", q, q)
		case len(bound) > 1 && bound[0].path != bound[1].path:
			return nil, refuse(src.path, line, "two imports in this file are both named %s (%q and %q)",
				q, bound[0].path, bound[1].path)
		case len(bound) > 0:
			needed = append(needed, bound[0])
		case pkg.all[q] || universe[q]:
			// A package-level value or a predeclared type: nothing to import.
		case len(unknown) > 0:
			return nil, refuse(src.path, line, "the moved code uses %s.… and it cannot be established which import "+
				"that is: the package name of %q is not known. With go on PATH and the module's dependencies "+
				"present, graphlume asks 'go list'; otherwise give the import an explicit name", q, unknown[0].path)
		case len(dots) > 0:
			return nil, refuse(src.path, line, "the moved code uses %s.… and this file dot-imports %q, so the name "+
				"may come from there; what a dot-import supplies cannot be read from syntax", q, dots[0].path)
		default:
			return nil, refuse(src.path, line, "the moved code uses %s.…, which neither this file's imports nor the "+
				"package's declarations account for", q)
		}
	}
	if len(dots) > 0 {
		for _, n := range sortedKeys(used.bare) {
			if !universe[n] && !pkg.all[n] {
				return nil, refuse(src.path, src.line(used.bare[n]), "this file dot-imports %q and the moved code "+
					"names %s, which the package does not declare: it may come from that import, and what a "+
					"dot-import supplies cannot be read from syntax, so the target's imports cannot be made exact",
					dots[0].path, n)
			}
		}
	}

	plan := &importPlan{}
	var dstImps []importSpec
	if dst != nil {
		dstImps = importsOf(dst, pkgName)
		if err := checkCapture(src, dst, srcImps, dstImps, used); err != nil {
			return nil, err
		}
	}
	for _, n := range needed {
		ok, err := targetHas(dst, dstImps, n)
		if err != nil {
			return nil, err
		}
		if !ok {
			plan.add = append(plan.add, n)
		}
	}

	// What the rest of the source still uses, the moved declaration left out.
	rest := namesIn(t.node, src.ast)
	for _, n := range needed {
		if _, still := rest.qualifiers[n.name]; !still {
			plan.remove = append(plan.remove, n)
		}
	}
	return plan, nil
}

// targetHas reports whether the target already imports n the way the moved
// code uses it, and refuses when adding it would clash with what is there.
func targetHas(dst *parsed, dstImps []importSpec, n importSpec) (bool, error) {
	if dst == nil {
		return false, nil
	}
	for _, d := range dstImps {
		if d.path == n.path && d.alias == n.alias {
			return true, nil
		}
	}
	for _, d := range dstImps {
		line := dst.line(d.spec.Pos())
		switch {
		case d.path == n.path && d.qualifies():
			return false, refuse(dst.path, line, "it imports %q as %s, and the moved code uses it as %s. "+
				"Use one name for it in both files, then try again", n.path, d.name, n.name)
		case d.path != n.path && d.name == n.name:
			return false, refuse(dst.path, line, "it already imports %q as %s, the name the moved code uses for %q",
				d.path, d.name, n.path)
		case d.alias == "." && exported(n.name):
			return false, refuse(dst.path, line, "it dot-imports %q, and the import %s the moved code needs could "+
				"collide with a name that brings in", d.path, n.name)
		}
	}
	return false, nil
}

// checkCapture refuses when a predeclared name the moved code uses means
// something else in the target: an import there is named after it. (Any
// other name the moved code leaves unresolved is the package's, and an
// import of that name in any file of the package would already not compile.)
func checkCapture(src, dst *parsed, srcImps, dstImps []importSpec, used names) error {
	inSource := make(map[string]bool, len(srcImps))
	for _, s := range srcImps {
		inSource[s.name] = true
	}
	check := func(m map[string]token.Pos) error {
		for _, n := range sortedKeys(m) {
			if !universe[n] || inSource[n] {
				continue
			}
			for _, d := range dstImps {
				if d.name == n {
					return refuse(dst.path, dst.line(d.spec.Pos()), "it imports %q under the name %s, and the moved "+
						"code (%s:%d) uses the predeclared %s, which that import would hide",
						d.path, n, src.path, src.line(m[n]), n)
				}
			}
		}
		return nil
	}
	if err := check(used.bare); err != nil {
		return err
	}
	return check(used.qualifiers)
}

func exported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

func sortedKeys(m map[string]token.Pos) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// describe renders an import for a site's detail.
func (s importSpec) describe() string { return fmt.Sprintf("import %s", s.text()) }
