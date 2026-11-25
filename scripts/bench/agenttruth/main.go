// truth computes, with the Go type checker, who calls a method and which
// tests reach it. It is the ground truth for the agent benchmark and uses
// nothing from the tool under test.
//
//	truth -dir <repo> list                 candidates: method names defined on several types
//	truth -dir <repo> callers Recv.Name    functions whose body calls exactly that method
//	truth -dir <repo> tests Recv.Name      Test functions that call it directly or through one function
//	truth -dir <repo> pairs                every callee<TAB>caller<TAB>kind of the module (see pairs.go)
//	truth -dir <repo> impls                every type<TAB>interface the module declares (see pairs.go)
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type fn struct {
	name string // Recv.Name or Name
	file string
	line int
	test bool
}

func main() {
	dir := flag.String("dir", ".", "repository root")
	flag.Parse()
	if a := flag.Arg(0); a == "pairs" || a == "impls" {
		os.Exit(runPairs(strings.TrimSuffix(*dir, "/"), a))
	}
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:   *dir,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	abs := func(p token.Position) string {
		f := p.Filename
		if i := strings.Index(f, *dir); i == 0 {
			f = strings.TrimPrefix(f[len(*dir):], "/")
		}
		return f
	}

	// definitions: every method declared in the module, keyed by position
	// (a package and its test variant type-check the same file twice).
	type def struct {
		recv, name, file string
		line             int
	}
	defs := map[string]def{}              // pos key -> def
	ifaceNames := map[string]bool{}       // method names any module interface declares
	calls := map[string]map[string]fn{}   // callee pos key -> caller key -> caller
	edges := map[string]map[string]bool{} // caller pos key -> callee pos keys
	funcs := map[string]fn{}              // pos key -> function
	seenFile := map[string]bool{}

	key := func(fset *token.FileSet, pos token.Pos) string {
		p := fset.Position(pos)
		return fmt.Sprintf("%s:%d:%d", abs(p), p.Line, p.Column)
	}

	for _, pkg := range pkgs {
		if pkg.Module == nil || !pkg.Module.Main {
			continue
		}
		for i, file := range pkg.Syntax {
			fname := abs(pkg.Fset.Position(file.Pos()))
			if strings.HasPrefix(fname, "fixtures/") || strings.Contains(fname, "/testdata/") {
				continue
			}
			_ = i
			if seenFile[fname] {
				continue
			}
			seenFile[fname] = true
			ast.Inspect(file, func(n ast.Node) bool {
				if it, ok := n.(*ast.InterfaceType); ok && it.Methods != nil {
					for _, m := range it.Methods.List {
						for _, nm := range m.Names {
							ifaceNames[nm.Name] = true
						}
					}
				}
				return true
			})
			for _, d := range file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				recv := ""
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					t := fd.Recv.List[0].Type
					if s, ok := t.(*ast.StarExpr); ok {
						t = s.X
					}
					switch x := t.(type) {
					case *ast.Ident:
						recv = x.Name
					case *ast.IndexExpr:
						if id, ok := x.X.(*ast.Ident); ok {
							recv = id.Name
						}
					case *ast.IndexListExpr:
						if id, ok := x.X.(*ast.Ident); ok {
							recv = id.Name
						}
					}
				}
				p := pkg.Fset.Position(fd.Name.Pos())
				k := key(pkg.Fset, fd.Name.Pos())
				full := fd.Name.Name
				if recv != "" {
					full = recv + "." + fd.Name.Name
					defs[k] = def{recv, fd.Name.Name, abs(p), p.Line}
				}
				f := fn{name: full, file: abs(p), line: p.Line,
					test: recv == "" && strings.HasPrefix(fd.Name.Name, "Test") && strings.HasSuffix(abs(p), "_test.go")}
				funcs[k] = f
				if fd.Body == nil {
					continue
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					var id *ast.Ident
					switch fun := ast.Unparen(call.Fun).(type) {
					case *ast.Ident:
						id = fun
					case *ast.SelectorExpr:
						id = fun.Sel
					case *ast.IndexExpr:
						switch x := ast.Unparen(fun.X).(type) {
						case *ast.Ident:
							id = x
						case *ast.SelectorExpr:
							id = x.Sel
						}
					}
					if id == nil {
						return true
					}
					obj, _ := pkg.TypesInfo.Uses[id].(*types.Func)
					if obj == nil {
						return true
					}
					obj = obj.Origin()
					ck := key(pkg.Fset, obj.Pos())
					if calls[ck] == nil {
						calls[ck] = map[string]fn{}
					}
					calls[ck][k] = f
					if edges[k] == nil {
						edges[k] = map[string]bool{}
					}
					edges[k][ck] = true
					return true
				})
			}
		}
	}

	find := func(q string) []string {
		var out []string
		for k, d := range defs {
			if d.recv+"."+d.name == q {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}

	switch flag.Arg(0) {
	case "list":
		byName := map[string][]string{}
		for k, d := range defs {
			byName[d.name] = append(byName[d.name], k)
		}
		var names []string
		for n, ks := range byName {
			if len(ks) >= 4 && !ifaceNames[n] {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			ks := byName[n]
			sort.Strings(ks)
			total := 0
			for _, k := range ks {
				total += len(calls[k])
			}
			fmt.Printf("%s: %d definitions, %d callers in total\n", n, len(ks), total)
			for _, k := range ks {
				d := defs[k]
				tests := 0
				for _, c := range calls[k] {
					if strings.HasSuffix(c.file, "_test.go") {
						tests++
					}
				}
				fmt.Printf("    %-34s %-52s callers=%d (in tests %d)\n", d.recv+"."+d.name, fmt.Sprintf("%s:%d", d.file, d.line), len(calls[k]), tests)
			}
		}
	case "callers":
		for _, k := range find(flag.Arg(1)) {
			d := defs[k]
			fmt.Printf("# %s.%s at %s:%d\n", d.recv, d.name, d.file, d.line)
			var out []string
			for _, c := range calls[k] {
				out = append(out, fmt.Sprintf("%s\t%s:%d", c.name, c.file, c.line))
			}
			sort.Strings(out)
			fmt.Println(strings.Join(out, "\n"))
		}
	case "tests":
		for _, k := range find(flag.Arg(1)) {
			d := defs[k]
			fmt.Printf("# %s.%s at %s:%d\n", d.recv, d.name, d.file, d.line)
			out := map[string]bool{}
			for ck, c := range calls[k] { // depth 1
				if c.test {
					out[fmt.Sprintf("%s\t%s:%d\tdirect", c.name, c.file, c.line)] = true
				}
				for ck2, c2 := range calls[ck] { // depth 2
					_ = ck2
					if c2.test {
						direct := fmt.Sprintf("%s\t%s:%d\tdirect", c2.name, c2.file, c2.line)
						if !out[direct] {
							out[fmt.Sprintf("%s\t%s:%d\tthrough %s", c2.name, c2.file, c2.line, c.name)] = true
						}
					}
				}
			}
			var lines []string
			for l := range out {
				lines = append(lines, l)
			}
			sort.Strings(lines)
			fmt.Println(strings.Join(lines, "\n"))
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: truth -dir <repo> list | callers Recv.Name | tests Recv.Name")
		os.Exit(2)
	}
}
