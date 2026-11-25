package move

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestRealPackagesPlanOrAreRefused plans the move of every top-level
// declaration of a few real packages of this repository, once into a new
// file and once into an existing sibling. Hand-written cases show the planner
// right on code written to test it; this shows it on code that was not.
//
// Every plan must end one of two ways: a refusal with a reason, or two files
// that parse, are still gofmt-formatted if they were, and between them hold
// every declaration the two held before, once. A refusal blaming the
// planner's own check ("internal check failed") is a defect and fails the
// test.
//
// GRAPHLUME_MOVE_SWEEP, a list of directories in the PATH syntax of the
// platform, replaces the built-in ones: a way to run the planner over any Go
// package before trusting it there.
func TestRealPackagesPlanOrAreRefused(t *testing.T) {
	dirs := []string{"../../edit", "../rename"}
	if testing.Short() {
		dirs = dirs[:1]
	}
	if extra := os.Getenv("GRAPHLUME_MOVE_SWEEP"); extra != "" {
		dirs = filepath.SplitList(extra)
	}
	var planned, refused int
	reasons := map[string]int{}
	for _, dir := range dirs {
		files := readPackage(t, dir)
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, from := range names {
			for _, symbol := range declared(t, from, files[from]) {
				targets := []string{"zz_moved" + testSuffix(from)}
				if sib := sibling(from, names, files); sib != "" {
					targets = append(targets, sib)
				}
				for _, to := range targets {
					p := planOf(t, symbol, from, to, files, repoPackageNames(files))
					if !p.OK() {
						refused++
						for _, r := range p.Refusals {
							if strings.Contains(r.Reason, "internal check failed") {
								t.Errorf("%s: move %s from %s to %s: %s", dir, symbol, from, to, r.Reason)
							}
							words := strings.Fields(r.Reason)
							reasons[strings.Join(words[:min(4, len(words))], " ")]++
						}
						continue
					}
					planned++
					checkResult(t, dir, symbol, from, to, files, p)
				}
			}
		}
	}
	t.Logf("%d moves planned, %d refused", planned, refused)
	for reason, n := range reasons {
		t.Logf("  refused %3d×  %s …", n, reason)
	}
	if planned < 50 {
		t.Errorf("only %d moves were planned; the sweep is not exercising the planner", planned)
	}
}

func testSuffix(name string) string {
	if strings.HasSuffix(name, "_test.go") {
		return "_test.go"
	}
	return ".go"
}

func readPackage(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// A checkout on Windows may hold CRLF; the property is the same.
		files[e.Name()] = string(b)
	}
	return files
}

// declared lists a file's top-level names the way a user would ask for them.
func declared(t *testing.T, name, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if recv := recvBase(d); recv != "" {
				out = append(out, recv+"."+d.Name.Name)
			} else {
				out = append(out, d.Name.Name)
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			for _, s := range d.Specs {
				for _, id := range specNames(s) {
					if id.Name != "_" {
						out = append(out, id.Name)
					}
				}
			}
		}
	}
	return out
}

// sibling picks another file of the same package and kind (test or not).
func sibling(from string, names []string, files map[string]string) string {
	pkg := packageOf(files[from])
	for _, n := range names {
		if n != from && testSuffix(n) == testSuffix(from) && packageOf(files[n]) == pkg &&
			nameConstraint(n) == nameConstraint(from) {
			return n
		}
	}
	return ""
}

func packageOf(src string) string {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.PackageClauseOnly)
	if err != nil {
		return ""
	}
	return f.Name.Name
}

// repoPackageNames stands in for `go list`: in this repository every package
// is named after the last element of its import path, with the usual
// exceptions for version suffixes, which is all the sweep needs.
func repoPackageNames(files map[string]string) map[string]string {
	var srcs [][]byte
	for _, src := range files {
		srcs = append(srcs, []byte(src))
	}
	out := make(map[string]string)
	for _, p := range UnnamedImports(srcs...) {
		name := path.Base(p)
		if strings.HasPrefix(name, "v") && strings.Trim(name[1:], "0123456789") == "" {
			name = path.Base(path.Dir(p))
		}
		name = strings.TrimSuffix(name, ".v3")
		name = strings.TrimPrefix(name, "go-")
		out[p] = name
	}
	return out
}

func checkResult(t *testing.T, dir, symbol, from, to string, files map[string]string, p Plan) {
	t.Helper()
	where := dir + ": move " + symbol + " from " + from + " to " + to
	before := count(t, where, files[from])
	if existing, ok := files[to]; ok {
		for k, n := range count(t, where, existing) {
			before[k] += n
		}
	}
	after := count(t, where, string(p.SourceAfter))
	for k, n := range count(t, where, string(p.TargetAfter)) {
		after[k] += n
	}
	for k, n := range before {
		if after[k] != n {
			t.Errorf("%s: %s is declared %d time(s) afterwards, %d before", where, k, after[k], n)
		}
	}
	for k, n := range after {
		if before[k] != n {
			t.Errorf("%s: %s is declared %d time(s) afterwards, %d before", where, k, n, before[k])
		}
	}
	for name, pair := range map[string][2]string{from: {files[from], string(p.SourceAfter)}, to: {files[to], string(p.TargetAfter)}} {
		was, now := pair[0], pair[1]
		if was != "" && !formatted(was) {
			continue // not formatted to begin with: left as it was
		}
		if !formatted(now) && formatted(files[from]) {
			t.Errorf("%s: %s is no longer gofmt-formatted", where, name)
		}
	}
	if len(p.Sites) < 2 {
		t.Errorf("%s: sites = %+v, want at least the insertion and the removal", where, p.Sites)
	}
}

func formatted(src string) bool {
	out, err := format.Source([]byte(src))
	return err == nil && bytes.Equal(out, []byte(src))
}

// count returns how many times each top-level name is declared in src.
func count(t *testing.T, where, src string) map[string]int {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s: a result does not parse: %v\n%s", where, err, src)
	}
	out := make(map[string]int)
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			out["func "+recvBase(d)+"."+d.Name.Name]++
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			for _, s := range d.Specs {
				for _, id := range specNames(s) {
					out[d.Tok.String()+" "+id.Name]++
				}
			}
		}
	}
	return out
}
