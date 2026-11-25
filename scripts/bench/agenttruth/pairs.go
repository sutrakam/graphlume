package main

// The "pairs" and "impls" modes: the INDEPENDENT truth for every call edge and
// every interface implementation of a module, computed with
// golang.org/x/tools/go/packages (which runs `go list` and the real build
// graph). Nothing here uses GraphLume, and nothing here is shared with the
// built-in checker it is used to judge: different loader, different walk,
// written separately.
//
//	truth -dir <repo> pairs   one line per distinct (callee, caller, kind):
//	                          callee<TAB>caller<TAB>CALLS|INSTANTIATES
//	truth -dir <repo> impls   one line per implementing named type:
//	                          type<TAB>interface
//
// A node is written path:line:Name, where line is the line of the declared
// name, so it can be joined to an index's symbol table without knowing how the
// index names things.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func runPairs(dir, mode string) int {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:   dir,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, append([]string{"./..."}, workspacePatterns(dir)...)...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	root := strings.TrimSuffix(dir, "/") + "/"
	rel := func(fset *token.FileSet, p token.Pos) (string, int, bool) {
		pos := fset.Position(p)
		if !strings.HasPrefix(pos.Filename, root) {
			return "", 0, false
		}
		r := strings.TrimPrefix(pos.Filename, root)
		if strings.HasPrefix(r, "fixtures/") || strings.Contains(r, "/testdata/") || strings.HasPrefix(r, "testdata/") {
			return "", 0, false
		}
		return r, pos.Line, true
	}
	nodeKey := func(fset *token.FileSet, id *ast.Ident) (string, bool) {
		f, l, ok := rel(fset, id.Pos())
		if !ok {
			return "", false
		}
		return fmt.Sprintf("%s:%d:%s", f, l, id.Name), true
	}
	objKey := func(fset *token.FileSet, o types.Object) (string, bool) {
		f, l, ok := rel(fset, o.Pos())
		if !ok {
			return "", false
		}
		return fmt.Sprintf("%s:%d:%s", f, l, o.Name()), true
	}

	if mode == "impls" {
		return runImpls(pkgs, objKey)
	}

	out := map[string]bool{}
	seenFile := map[string]bool{}
	for _, pkg := range pkgs {
		if pkg.Module == nil || !pkg.Module.Main {
			continue
		}
		for _, file := range pkg.Syntax {
			fname := pkg.Fset.Position(file.Pos()).Filename
			if seenFile[fname] {
				continue
			}
			seenFile[fname] = true
			if _, _, ok := rel(pkg.Fset, file.Pos()); !ok {
				continue
			}
			callee := map[*ast.Ident]bool{}
			construct := map[*ast.Ident]bool{}
			visit := func(n ast.Node, encl string) {
				ast.Inspect(n, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.CallExpr:
						if id := calleeOf(n.Fun); id != nil {
							callee[id] = true
						}
					case *ast.CompositeLit:
						if id := calleeOf(n.Type); id != nil {
							construct[id] = true
						}
					case *ast.Ident:
						obj := pkg.TypesInfo.Uses[n]
						if obj == nil || obj.Pkg() == nil {
							return true
						}
						kind := ""
						switch o := obj.(type) {
						case *types.Func:
							if callee[n] {
								kind = "CALLS"
							}
							obj = o.Origin()
						case *types.TypeName:
							if o.Parent() == o.Pkg().Scope() && (callee[n] || construct[n]) {
								if _, isIface := o.Type().Underlying().(*types.Interface); !isIface {
									kind = "INSTANTIATES"
								}
							}
						}
						if kind == "" {
							return true
						}
						ck, ok := objKey(pkg.Fset, obj)
						if !ok {
							return true
						}
						// A function declared in a function body is not a node.
						if fn, ok := obj.(*types.Func); ok && fn.Type().(*types.Signature).Recv() == nil &&
							fn.Parent() != fn.Pkg().Scope() {
							return true
						}
						out[ck+"\t"+encl+"\t"+kind] = true
					}
					return true
				})
			}
			for _, d := range file.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if k, ok := nodeKey(pkg.Fset, d.Name); ok {
						visit(d, k)
					}
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch s := s.(type) {
						case *ast.TypeSpec:
							if k, ok := nodeKey(pkg.Fset, s.Name); ok {
								visit(s, k)
							}
						case *ast.ValueSpec:
							first := ""
							for _, nm := range s.Names {
								if nm.Name != "_" {
									first, _ = nodeKey(pkg.Fset, nm)
									break
								}
							}
							if first == "" {
								continue
							}
							if s.Type != nil {
								visit(s.Type, first)
							}
							for i, v := range s.Values {
								k := first
								if len(s.Values) == len(s.Names) && s.Names[i].Name != "_" {
									k, _ = nodeKey(pkg.Fset, s.Names[i])
								}
								visit(v, k)
							}
						}
					}
				}
			}
		}
	}
	lines := make([]string, 0, len(out))
	for l := range out {
		lines = append(lines, l)
	}
	sort.Strings(lines)
	fmt.Println(strings.Join(lines, "\n"))
	return 0
}

// workspacePatterns returns one pattern per member module a go.work names
// besides the root: "./..." does not cross into another module, even inside a
// workspace, so without them the truth would silently cover only the first.
func workspacePatterns(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "go.work"))
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range strings.Fields(string(data)) {
		f = strings.Trim(f, "\"`")
		if strings.HasPrefix(f, "./") && f != "./" {
			out = append(out, strings.TrimSuffix(f, "/")+"/...")
		}
	}
	return out
}

func calleeOf(e ast.Expr) *ast.Ident {
	for {
		switch t := ast.Unparen(e).(type) {
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
}

func runImpls(pkgs []*packages.Package, objKey func(*token.FileSet, types.Object) (string, bool)) int {
	type named struct {
		n   *types.Named
		key string
	}
	var ts, is []named
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		if pkg.Module == nil || !pkg.Module.Main || pkg.Types == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, nm := range scope.Names() {
			tn, ok := scope.Lookup(nm).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			n, ok := tn.Type().(*types.Named)
			if !ok || n.TypeParams().Len() > 0 {
				continue
			}
			k, ok := objKey(pkg.Fset, tn)
			if !ok || seen[k] {
				continue
			}
			seen[k] = true
			if it, ok := n.Underlying().(*types.Interface); ok {
				if it.NumMethods() > 0 && it.IsMethodSet() {
					is = append(is, named{n, k})
				}
				continue
			}
			ts = append(ts, named{n, k})
		}
	}
	out := map[string]bool{}
	for _, t := range ts {
		for _, i := range is {
			it := i.n.Underlying().(*types.Interface)
			if types.Implements(t.n, it) || types.Implements(types.NewPointer(t.n), it) {
				out[t.key+"\t"+i.key] = true
			}
		}
	}
	lines := make([]string, 0, len(out))
	for l := range out {
		lines = append(lines, l)
	}
	sort.Strings(lines)
	fmt.Println(strings.Join(lines, "\n"))
	return 0
}
