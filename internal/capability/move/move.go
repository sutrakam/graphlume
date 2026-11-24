// Package move plans the move of one top-level Go declaration from one file
// to another file of the same package.
//
// A move inside a package changes no reference: every file of a package
// shares one scope, so nothing that names the declaration needs editing. What
// is left is exact, and all of it is syntax the Go standard parser reports:
// which bytes are the declaration and its doc comment, which imports the
// moved code needs in the file it lands in, and which imports the file it
// left no longer uses.
//
// The package decides and computes; it writes nothing. It takes the bytes of
// the files and returns the bytes they should hold, plus every reason the
// move must not happen. internal/edit does the writing, and only when
// Refusals is empty.
//
// Everything that is not provably safe is refused with its reason: other
// languages, another package, a method leaving its type, an iota group, an
// init function, cgo and generated files, files with different build
// constraints, a variable whose move could change initialisation order, an
// import whose package name cannot be established.
package move

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// Request names the move.
type Request struct {
	// Symbol is a top-level name ("ParseConfig") or, for a method, the
	// receiver's type and the method ("Server.Close"). A method never moves
	// on its bare name.
	Symbol string
	// From and To are relative to the project root, forward slashes.
	From, To string
}

// File is one Go file of the package directory.
type File struct {
	Name string // base name
	Data []byte
}

// Input is every byte the plan is computed from.
type Input struct {
	Source []byte
	// Target is the destination's content; TargetExists false means the file
	// is to be created.
	Target       []byte
	TargetExists bool
	// Siblings are the other .go files of the directory the go command would
	// consider (not From, not To). They are read, never changed: they say
	// which names the package declares.
	Siblings []File
	// PackageNames maps an import path to the name of the package it
	// declares, as the go command reports it. A path absent from the map has
	// a known name only if it is a standard-library path; any other is
	// unknown, and a move that depends on it is refused.
	PackageNames map[string]string
}

// Action is what happens at a site.
type Action string

// The actions a move is made of.
const (
	ActionCreateFile   Action = "create-file"
	ActionInsert       Action = "insert"
	ActionRemove       Action = "remove"
	ActionAddImport    Action = "add-import"
	ActionRemoveImport Action = "remove-import"
)

// Site is one place a file changes.
type Site struct {
	Path string `json:"path"`
	// Line and EndLine are 1-indexed. For a removal they are lines of the
	// file as it is; for an insertion, of the file as it will be.
	Line    int    `json:"line"`
	EndLine int    `json:"end_line,omitempty"`
	Action  Action `json:"action"`
	Detail  string `json:"detail"`
	// Confidence is set by the caller: the plan locates every site from the
	// syntax tree, and only a compiler run can raise that to exact.
	Confidence model.Confidence `json:"confidence,omitempty"`
}

// Decl is the declaration being moved.
type Decl struct {
	Name     string `json:"name"`
	Receiver string `json:"receiver,omitempty"`
	Kind     string `json:"kind"` // func, method, type, const, var
	Path     string `json:"path"`
	Line     int    `json:"line"`
	EndLine  int    `json:"end_line"`
}

// Refusal is one reason the move must not be made.
type Refusal struct {
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
}

// Plan is the move, computed and not written.
type Plan struct {
	Decl     Decl
	Sites    []Site
	Refusals []Refusal
	// Notes are things the user should know that do not stop the move.
	Notes []string

	// SourceAfter and TargetAfter are the complete new contents. Both are nil
	// when Refusals is not empty.
	SourceAfter, TargetAfter []byte
	// CreatesTarget: To does not exist and TargetAfter is a whole new file.
	CreatesTarget bool
	// TestFiles: both files are _test.go files, so only a test build
	// compiles them.
	TestFiles bool
}

// OK reports whether the plan can be applied.
func (p Plan) OK() bool { return len(p.Refusals) == 0 && p.SourceAfter != nil && p.TargetAfter != nil }

// refused carries a refusal out of the planning steps as an error.
type refused struct{ r Refusal }

func (e *refused) Error() string { return e.r.Reason }

func refuse(file string, line int, format string, args ...any) error {
	return &refused{Refusal{Path: file, Line: line, Reason: fmt.Sprintf(format, args...)}}
}

// Compute plans the move. It never fails: anything that stops the move is a
// Refusal in the returned plan.
func Compute(req Request, in Input) Plan {
	var plan Plan
	if err := compute(req, in, &plan); err != nil {
		plan.Sites, plan.SourceAfter, plan.TargetAfter = nil, nil, nil
		if r := (*refused)(nil); errors.As(err, &r) {
			plan.Refusals = append(plan.Refusals, r.r)
		} else {
			plan.Refusals = append(plan.Refusals, Refusal{Reason: err.Error()})
		}
	}
	return plan
}

func compute(req Request, in Input, plan *Plan) error {
	name, recv, err := parseSymbol(req.Symbol)
	if err != nil {
		return err
	}
	if err := checkPaths(req, recv != ""); err != nil {
		return err
	}

	fset := token.NewFileSet()
	src, err := parseFile(fset, req.From, in.Source)
	if err != nil {
		return err
	}
	var dst *parsed
	if in.TargetExists {
		if dst, err = parseFile(fset, req.To, in.Target); err != nil {
			return err
		}
	}
	if err := checkFiles(req, src, dst); err != nil {
		return err
	}
	src.noteFormatting()
	if dst != nil {
		dst.noteFormatting()
	}

	target, err := locate(src, name, recv)
	if err != nil {
		return err
	}
	plan.Decl = target.decl
	plan.TestFiles = strings.HasSuffix(req.From, "_test.go")

	pkg, err := packageNames(fset, req, src, dst, in.Siblings)
	if err != nil {
		return err
	}
	if dst != nil {
		if err := checkNotDefined(dst, target); err != nil {
			return err
		}
	}
	if err := checkInitOrder(src, target, pkg); err != nil {
		return err
	}

	imp, err := planImports(src, dst, target, pkg, in.PackageNames)
	if err != nil {
		return err
	}

	sourceAfter, importCuts, err := cutSource(src, target, imp)
	if err != nil {
		return err
	}
	var targetAfter []byte
	if dst != nil {
		targetAfter, err = extendTarget(dst, target, imp)
	} else {
		targetAfter, err = newTarget(src, target, imp)
	}
	if err != nil {
		return err
	}

	sites, err := selfCheck(req, src, dst, target, imp, importCuts, sourceAfter, targetAfter)
	if err != nil {
		return err
	}
	plan.Sites = sites
	plan.SourceAfter, plan.TargetAfter = sourceAfter, targetAfter
	plan.CreatesTarget = dst == nil
	plan.Notes = notes(req, target, sourceAfter, dst == nil)
	return nil
}

// CheckRequest returns the reason the request can be refused without reading
// any file — a symbol that is not one, another language, another package —
// or nil. Compute makes the same checks; a caller uses this to say so before
// it reads anything.
func CheckRequest(req Request) *Refusal {
	_, recv, err := parseSymbol(req.Symbol)
	if err == nil {
		err = checkPaths(req, recv != "")
	}
	if r := (*refused)(nil); errors.As(err, &r) {
		return &r.r
	}
	return nil
}

// parseSymbol splits "Name" or "Recv.Name".
func parseSymbol(s string) (name, recv string, err error) {
	name = s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		recv, name = s[:i], s[i+1:]
		if !token.IsIdentifier(recv) {
			return "", "", refuse("", 0, "%q is not a symbol: write a top-level name, or Type.Method for a method", s)
		}
	}
	switch {
	case !token.IsIdentifier(name):
		return "", "", refuse("", 0, "%q is not a symbol: write a top-level name, or Type.Method for a method", s)
	case name == "_" || recv == "_":
		return "", "", refuse("", 0, "the blank identifier names nothing, so there is no declaration to find")
	case recv == "" && name == "init":
		return "", "", refuse("", 0, "an init function cannot be moved: a package's init functions run in the order "+
			"of the files that hold them, so moving one can change what the program does at start-up")
	}
	return name, recv, nil
}

// checkPaths refuses everything the two paths alone rule out.
func checkPaths(req Request, method bool) error {
	for _, p := range []string{req.From, req.To} {
		if !strings.HasSuffix(p, ".go") {
			return refuse(p, 0, "not a Go source file. This version moves Go declarations only: "+
				"it has no exact way to cut a declaration out of another language's file, "+
				"and a text move that is only probably right is not offered")
		}
	}
	if req.From == req.To {
		return refuse(req.From, 0, "the source and the target are the same file")
	}
	if path.Dir(req.From) != path.Dir(req.To) {
		if method {
			return refuse(req.To, 0, "a method cannot leave the package of its receiver type, "+
				"and this file is in another directory than %s, so in another package", req.From)
		}
		return refuse(req.To, 0, "it is in another directory than %s, so in another package: every reference "+
			"would need a new qualifier and import, and unexported names would stop resolving. "+
			"This version moves a declaration only between files of one package", req.From)
	}
	for _, p := range []string{req.From, req.To} {
		if base := path.Base(p); strings.HasPrefix(base, "_") || strings.HasPrefix(base, ".") {
			return refuse(p, 0, "the go command ignores files whose names begin with _ or ., "+
				"so a declaration moved across that line would enter or leave the build")
		}
	}
	return nil
}

// checkFiles refuses on what the two files are, before anything is located.
func checkFiles(req Request, src, dst *parsed) error {
	for _, f := range []*parsed{src, dst} {
		if f == nil {
			continue
		}
		if ast.IsGenerated(f.ast) {
			return refuse(f.path, 0, "generated code (it carries a \"Code generated ... DO NOT EDIT.\" line): "+
				"the next run of its generator would undo the move. Change the generator instead")
		}
		for _, s := range f.ast.Imports {
			if s.Path.Value == `"C"` {
				return refuse(f.path, f.line(s.Pos()), "a cgo file (it imports \"C\"): its preamble and //export "+
					"comments tie declarations to this file in ways the syntax tree does not show")
			}
		}
	}
	srcCons, err := constraintOf(src)
	if err != nil {
		return err
	}
	if dst == nil {
		// A new file takes the source's //go:build line; its name must not
		// add or drop a constraint of its own.
		if a, b := nameConstraint(path.Base(req.From)), nameConstraint(path.Base(req.To)); a != b {
			return refuse(req.To, 0, "its name builds it %s, but %s is built %s: the declaration would be compiled "+
				"under different conditions", describeConstraint(b), req.From, describeConstraint(a))
		}
		return nil
	}
	if src.ast.Name.Name != dst.ast.Name.Name {
		return refuse(dst.path, dst.line(dst.ast.Package), "it is package %s and %s is package %s: "+
			"a move between packages is not supported in this version",
			dst.ast.Name.Name, src.path, src.ast.Name.Name)
	}
	dstCons, err := constraintOf(dst)
	if err != nil {
		return err
	}
	if srcCons != dstCons {
		return refuse(dst.path, 0, "it is built %s, but %s is built %s: the declaration would be compiled "+
			"under different conditions, which no build on this machine can check",
			describeConstraint(dstCons), src.path, describeConstraint(srcCons))
	}
	return nil
}

// checkNotDefined refuses a target that already declares the name.
func checkNotDefined(dst *parsed, t *located) error {
	for _, d := range dst.ast.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name != t.decl.Name || recvBase(d) != t.decl.Receiver {
				continue
			}
			return refuse(dst.path, dst.line(d.Pos()), "it already declares %s", t.label())
		case *ast.GenDecl:
			if t.decl.Receiver != "" {
				continue
			}
			for _, s := range d.Specs {
				for _, id := range specNames(s) {
					if id.Name == t.decl.Name {
						return refuse(dst.path, dst.line(id.Pos()), "it already declares %s", t.decl.Name)
					}
				}
			}
		}
	}
	return nil
}

// notes are the things worth saying about a move that will be made.
func notes(req Request, t *located, sourceAfter []byte, created bool) []string {
	var out []string
	if t.decl.Kind == "type" {
		out = append(out, fmt.Sprintf("Methods of %s stay in %s; move each with %s.<Method> if they should follow.",
			t.decl.Name, req.From, t.decl.Name))
	}
	if created {
		out = append(out, fmt.Sprintf("%s is a new file: it gets the package clause, the build constraint and the "+
			"imports the declaration needs, and no licence or header comment. Add one if this repository requires it.", req.To))
	}
	if f, err := parseFile(token.NewFileSet(), req.From, sourceAfter); err == nil && len(f.ast.Decls) == len(importDecls(f)) {
		out = append(out, fmt.Sprintf("%s holds no declarations after the move. It is left in place; delete it by hand if it is not wanted.", req.From))
	}
	sort.Strings(out)
	return out
}
