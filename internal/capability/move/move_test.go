package move

import (
	"go/build"
	"io"
	"io/fs"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// planOf plans a move among files given by name. Every file that is neither
// the source nor the target is a sibling.
func planOf(t *testing.T, symbol, from, to string, files map[string]string, pkgNames map[string]string) Plan {
	t.Helper()
	in := Input{Source: []byte(files[from]), PackageNames: pkgNames}
	if body, ok := files[to]; ok {
		in.Target, in.TargetExists = []byte(body), true
	}
	var names []string
	for name := range files {
		if name != from && name != to {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		in.Siblings = append(in.Siblings, File{Name: name, Data: []byte(files[name])})
	}
	return Compute(Request{Symbol: symbol, From: from, To: to}, in)
}

func mustApply(t *testing.T, p Plan) (source, target string) {
	t.Helper()
	if !p.OK() {
		t.Fatalf("the move was refused: %+v", p.Refusals)
	}
	return string(p.SourceAfter), string(p.TargetAfter)
}

func mustRefuse(t *testing.T, p Plan, want string) {
	t.Helper()
	if p.OK() || len(p.Refusals) == 0 {
		t.Fatalf("the move was not refused; source would be:\n%s", p.SourceAfter)
	}
	if p.SourceAfter != nil || p.TargetAfter != nil || len(p.Sites) != 0 {
		t.Errorf("a refused plan still carries content or sites")
	}
	var all []string
	for _, r := range p.Refusals {
		all = append(all, r.Reason)
	}
	if !strings.Contains(strings.Join(all, "\n"), want) {
		t.Errorf("refusal does not mention %q:\n%s", want, strings.Join(all, "\n"))
	}
}

func equalText(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n--- got ---\n%s\n--- want ---\n%s", what, got, want)
	}
}

const loadSrc = `package config

import (
	"fmt"
	"os"
	"strings"
)

// Load reads the configuration file.
func Load(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Normalize lower-cases a key and trims it.
//
// It is used by every lookup.
func Normalize(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func describe(n int) string { return fmt.Sprint(n) }
`

// TestMoveIntoAnExistingFileFixesBothImportLists: the moved function is the
// only user of "strings" in the source, and the target does not import it.
func TestMoveIntoAnExistingFileFixesBothImportLists(t *testing.T) {
	files := map[string]string{
		"load.go": loadSrc,
		"keys.go": "package config\n\nimport \"os\"\n\n// Env reads a key from the environment.\nfunc Env(key string) string { return os.Getenv(key) }\n",
	}
	p := planOf(t, "Normalize", "load.go", "keys.go", files, nil)
	source, target := mustApply(t, p)

	equalText(t, "load.go", source, `package config

import (
	"fmt"
	"os"
)

// Load reads the configuration file.
func Load(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func describe(n int) string { return fmt.Sprint(n) }
`)
	equalText(t, "keys.go", target, `package config

import "os"
import "strings"

// Env reads a key from the environment.
func Env(key string) string { return os.Getenv(key) }

// Normalize lower-cases a key and trims it.
//
// It is used by every lookup.
func Normalize(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
`)

	if p.Decl.Name != "Normalize" || p.Decl.Kind != "func" || p.Decl.Line != 14 || p.Decl.EndLine != 19 {
		t.Errorf("declaration = %+v, want func Normalize at lines 14-19", p.Decl)
	}
	var got []string
	for _, s := range p.Sites {
		got = append(got, string(s.Action)+" "+s.Path)
	}
	want := []string{"add-import keys.go", "insert keys.go", "remove load.go", "remove-import load.go"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("sites = %v, want %v", got, want)
	}
	for _, s := range p.Sites {
		switch s.Action {
		case ActionInsert:
			if s.Line != 9 || s.EndLine != 14 || !strings.Contains(s.Detail, "with its doc comment") {
				t.Errorf("insert site = %+v, want lines 9-14 of the new keys.go, doc comment named", s)
			}
		case ActionRemove:
			if s.Line != 14 || s.EndLine != 19 {
				t.Errorf("remove site = %+v, want lines 14-19 of load.go", s)
			}
		case ActionRemoveImport:
			if s.Line != 6 || !strings.Contains(s.Detail, `"strings"`) {
				t.Errorf("remove-import site = %+v, want line 6, \"strings\"", s)
			}
		}
	}
}

// TestMoveCreatesTheTargetWithPackageClauseAndImports: an absent target is a
// new file holding exactly what the declaration needs.
func TestMoveCreatesTheTargetWithPackageClauseAndImports(t *testing.T) {
	p := planOf(t, "Normalize", "load.go", "normalize.go", map[string]string{"load.go": loadSrc}, nil)
	_, target := mustApply(t, p)
	equalText(t, "normalize.go", target, `package config

import "strings"

// Normalize lower-cases a key and trims it.
//
// It is used by every lookup.
func Normalize(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
`)
	if !p.CreatesTarget || p.Sites[0].Action != ActionCreateFile {
		t.Errorf("creates=%v first site=%+v, want a create-file site first", p.CreatesTarget, p.Sites[0])
	}
	if len(p.Notes) == 0 || !strings.Contains(strings.Join(p.Notes, " "), "new file") {
		t.Errorf("notes %q do not say the file is new and has no header", p.Notes)
	}
}

// TestMoveThereAndBackRestoresBothFiles: the last declaration of a formatted
// file, moved out and moved back, leaves the file byte for byte as it was.
func TestMoveThereAndBackRestoresBothFiles(t *testing.T) {
	const a = `package config

import (
	"os"
	"strings"
)

func Load(path string) ([]byte, error) { return os.ReadFile(path) }

// Normalize lower-cases a key.
func Normalize(key string) string { return strings.ToLower(key) }
`
	out := planOf(t, "Normalize", "a.go", "b.go", map[string]string{"a.go": a}, nil)
	a1, b1 := mustApply(t, out)
	if strings.Contains(a1, "strings") || !strings.Contains(b1, `import "strings"`) {
		t.Fatalf("after the move out:\na.go:\n%s\nb.go:\n%s", a1, b1)
	}
	back := planOf(t, "Normalize", "b.go", "a.go", map[string]string{"a.go": a1, "b.go": b1}, nil)
	b2, a2 := mustApply(t, back)
	equalText(t, "a.go after the round trip", a2, a)
	equalText(t, "b.go after the round trip", b2, "package config\n")
	if n := strings.Join(back.Notes, " "); !strings.Contains(n, "holds no declarations") {
		t.Errorf("notes %q do not say b.go is left empty", back.Notes)
	}
}

// TestImportsAreAddedBesideTheirKind: in a parenthesised block that keeps the
// standard library apart, each new import joins its own group.
func TestImportsAreAddedBesideTheirKind(t *testing.T) {
	files := map[string]string{
		"a.go": `package app

import (
	"sort"

	"example.com/x/ids"
	"example.com/x/zeta"
)

func Keep() string { return ids.New() + zeta.Name }

func Sorted(in []string) []string {
	sort.Strings(in)
	return append(in, ids.New())
}
`,
		"b.go": `package app

import (
	"strings"

	"example.com/x/zeta"
)

func Upper() string { return strings.ToUpper(zeta.Name) }
`,
	}
	names := map[string]string{"example.com/x/ids": "ids", "example.com/x/zeta": "zeta"}
	source, target := mustApply(t, planOf(t, "Sorted", "a.go", "b.go", files, names))
	equalText(t, "b.go", target, `package app

import (
	"sort"
	"strings"

	"example.com/x/ids"
	"example.com/x/zeta"
)

func Upper() string { return strings.ToUpper(zeta.Name) }

func Sorted(in []string) []string {
	sort.Strings(in)
	return append(in, ids.New())
}
`)
	// "sort" was the whole first group; its blank line goes with it. ids is
	// still used by Keep and stays.
	equalText(t, "a.go", source, `package app

import (
	"example.com/x/ids"
	"example.com/x/zeta"
)

func Keep() string { return ids.New() + zeta.Name }
`)
}

// TestImportsStartAGroupWhenTheirKindIsNew: the first standard-library import
// goes on top, the first from outside goes at the bottom, each set apart.
func TestImportsStartAGroupWhenTheirKindIsNew(t *testing.T) {
	const moved = `package app

import (
	"os"
	"strings"

	"example.com/x/ids"
)

func Moved() string { return strings.ToUpper(os.Args[0]) + ids.New() }
`
	names := map[string]string{"example.com/x/ids": "ids", "example.com/x/zeta": "zeta"}
	for _, tc := range []struct{ name, target, want string }{
		{"into a block of outside imports only",
			"package app\n\nimport (\n\t\"example.com/x/zeta\"\n)\n\nvar Z = zeta.Name\n",
			"package app\n\nimport (\n\t\"os\"\n\t\"strings\"\n\n\t\"example.com/x/ids\"\n\t\"example.com/x/zeta\"\n)\n\nvar Z = zeta.Name\n\n"},
		{"into a block of standard-library imports only",
			"package app\n\nimport (\n\t\"fmt\"\n)\n\nvar F = fmt.Sprint\n",
			"package app\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n\n\t\"example.com/x/ids\"\n)\n\nvar F = fmt.Sprint\n\n"},
		{"into a file with no imports",
			"// Package app.\npackage app\n\nvar N = 1\n",
			"// Package app.\npackage app\n\nimport (\n\t\"os\"\n\t\"strings\"\n\n\t\"example.com/x/ids\"\n)\n\nvar N = 1\n\n"},
		{"into a file that is only a package clause",
			"package app\n",
			"package app\n\nimport (\n\t\"os\"\n\t\"strings\"\n\n\t\"example.com/x/ids\"\n)\n\n"},
		{"into a file whose imports share a line",
			"package app\n\nimport (\"fmt\"; \"errors\")\n\nvar E = errors.New(fmt.Sprint())\n",
			"package app\n\nimport (\"fmt\"; \"errors\")\nimport (\n\t\"os\"\n\t\"strings\"\n\n\t\"example.com/x/ids\"\n)\n\nvar E = errors.New(fmt.Sprint())\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, target := mustApply(t, planOf(t, "Moved", "a.go", "b.go", map[string]string{"a.go": moved, "b.go": tc.target}, names))
			equalText(t, "b.go", target, tc.want+"func Moved() string { return strings.ToUpper(os.Args[0]) + ids.New() }\n")
			equalText(t, "a.go", source, "package app\n")
		})
	}
}

// TestImportNamesAreNeverGuessed: an import outside the standard library with
// no explicit name and no answer from the go command has an unknown package
// name, and a move that depends on it is refused.
func TestImportNamesAreNeverGuessed(t *testing.T) {
	files := map[string]string{"a.go": `package app

import "gopkg.in/yaml.v3"

func Encode(v any) ([]byte, error) { return yaml.Marshal(v) }

func Other() {}
`}
	mustRefuse(t, planOf(t, "Encode", "a.go", "b.go", files, nil), `package name of "gopkg.in/yaml.v3" is not known`)

	// The go command's answer makes it exact.
	source, target := mustApply(t, planOf(t, "Encode", "a.go", "b.go", files, map[string]string{"gopkg.in/yaml.v3": "yaml"}))
	if strings.Contains(source, "yaml") || !strings.Contains(target, `import "gopkg.in/yaml.v3"`) {
		t.Errorf("source:\n%s\ntarget:\n%s", source, target)
	}

	// So does a name written in the file.
	files["a.go"] = strings.Replace(files["a.go"], `import "gopkg.in/yaml.v3"`, `import yaml "gopkg.in/yaml.v3"`, 1)
	_, target = mustApply(t, planOf(t, "Encode", "a.go", "b.go", files, nil))
	if !strings.Contains(target, `import yaml "gopkg.in/yaml.v3"`) {
		t.Errorf("the explicit name did not travel:\n%s", target)
	}

	// A function that does not touch the unknown import is unaffected by it.
	mustApply(t, planOf(t, "Other", "a.go", "b.go", files, nil))
}

// TestDotAndBlankImportsAreKept: neither is removed from the source, and
// neither is copied on a guess.
func TestDotAndBlankImportsAreKept(t *testing.T) {
	files := map[string]string{"a.go": `package app

import (
	_ "embed"
	"fmt"
	. "math"
)

func Area(r float64) float64 { return Pi * r * r }

func Label(n int) string { return fmt.Sprint(n) }
`}
	source, target := mustApply(t, planOf(t, "Label", "a.go", "b.go", files, nil))
	for _, want := range []string{`_ "embed"`, `. "math"`} {
		if !strings.Contains(source, want) {
			t.Errorf("the source lost %s:\n%s", want, source)
		}
		if strings.Contains(target, want) {
			t.Errorf("the target gained %s:\n%s", want, target)
		}
	}
	if strings.Contains(source, `"fmt"`) || !strings.Contains(target, `import "fmt"`) {
		t.Errorf("fmt did not follow Label:\nsource:\n%s\ntarget:\n%s", source, target)
	}

	// Area names Pi, which only the dot-import can supply: what that import
	// provides is not syntax, so the target's imports cannot be made exact.
	mustRefuse(t, planOf(t, "Area", "a.go", "b.go", files, nil), "dot-imports")
}

func TestRefusals(t *testing.T) {
	const two = "package app\n\nfunc A() {}\n\nfunc B() {}\n"
	tests := []struct {
		name   string
		symbol string
		from   string
		to     string
		files  map[string]string
		want   string
	}{
		{name: "another language", symbol: "A", from: "a.py", to: "b.py",
			files: map[string]string{"a.py": "def A(): pass\n"}, want: "moves Go declarations only"},
		{name: "a Go declaration into a non-Go file", symbol: "A", from: "a.go", to: "notes.txt",
			files: map[string]string{"a.go": two}, want: "not a Go source file"},
		{name: "another directory", symbol: "A", from: "app/a.go", to: "other/a.go",
			files: map[string]string{"app/a.go": two}, want: "another package"},
		{name: "a method to another directory", symbol: "T.A", from: "app/a.go", to: "other/a.go",
			files: map[string]string{"app/a.go": two}, want: "cannot leave the package of its receiver type"},
		{name: "the same file", symbol: "A", from: "a.go", to: "a.go",
			files: map[string]string{"a.go": two}, want: "same file"},
		{name: "another package in the same directory", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two, "b.go": "package app_test\n"}, want: "is package app_test"},
		{name: "a file the go command ignores", symbol: "A", from: "a.go", to: "_b.go",
			files: map[string]string{"a.go": two}, want: "ignores files"},
		{name: "not an identifier", symbol: "A B", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two}, want: "is not a symbol"},
		{name: "the blank identifier", symbol: "_", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nvar _ = 1\n"}, want: "blank identifier"},
		{name: "no such declaration", symbol: "C", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two}, want: "no top-level declaration named C"},
		{name: "an init function", symbol: "init", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nfunc init() {}\n\nfunc A() {}\n"}, want: "init function cannot be moved"},
		{name: "the target already defines the name", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two, "b.go": "package app\n\nvar A = 1\n"}, want: "already declares A"},
		{name: "the source does not parse", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nfunc A( {}\n"}, want: "does not parse"},
		{name: "the target does not parse", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two, "b.go": "package app\n\nfunc {\n"}, want: "does not parse"},
		{name: "a sibling does not parse", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two, "c.go": "package app\n\nfunc {\n"}, want: "this file of the package does not parse"},
		{name: "a generated source", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "// Code generated by protoc-gen-go. DO NOT EDIT.\n\n" + two}, want: "generated code"},
		{name: "a generated target", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two, "b.go": "// Code generated by stringer. DO NOT EDIT.\n\npackage app\n"}, want: "generated code"},
		{name: "a cgo source", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\n// #include <stdio.h>\nimport \"C\"\n\nfunc A() {}\n\nfunc B() {}\n"}, want: "cgo file"},
		{name: "a cgo target", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": two, "b.go": "package app\n\nimport \"C\"\n"}, want: "cgo file"},
		{name: "two declarations on one line", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nfunc A() {}; func B() {}\n"}, want: "followed by other code or a /* */ comment on its last line"},
		{name: "a name the file declares twice", symbol: "x", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nvar x = 1\n\nfunc x() {}\n"}, want: "x is declared 2 times in this file (lines 3, 5)"},
		{name: "a declaration that starts on another's line", symbol: "x", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nvar y = 2; var x = 1\n"}, want: "shares its first line with other code"},
		{name: "a group member behind a comment on its line", symbol: "x", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nvar (\n\t/* note */ x = 1\n\ty = 2\n)\n"}, want: "shares its first line with other code"},
		{name: "a name declared with others", symbol: "x", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nvar x, y = 1, 2\n"}, want: "declared in one statement with y"},
		{name: "a go:embed directive", symbol: "page", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nimport _ \"embed\"\n\n//go:embed page.html\nvar page string\n"}, want: "//go:embed directive"},
		{name: "a go:generate directive in the doc comment", symbol: "Kind", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\n//go:generate stringer -type=Kind\ntype Kind int\n"}, want: "//go:generate directive"},
		{name: "an import the target names differently", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{
				"a.go": "package app\n\nimport \"strings\"\n\nfunc A() string { return strings.ToUpper(\"a\") }\n",
				"b.go": "package app\n\nimport str \"strings\"\n\nfunc B() string { return str.ToLower(\"b\") }\n",
			}, want: `imports "strings" as str`},
		{name: "an import name the target uses for something else", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{
				"a.go": "package app\n\nimport \"text/template\"\n\nfunc A() *template.Template { return nil }\n",
				"b.go": "package app\n\nimport \"html/template\"\n\nfunc B() *template.Template { return nil }\n",
			}, want: `already imports "html/template" as template`},
		{name: "a predeclared name the target hides", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{
				"a.go": "package app\n\nfunc A(a, b int) int { return min(a, b) }\n",
				"b.go": "package app\n\nimport min \"math\"\n\nvar B = min.Pi\n",
			}, want: "uses the predeclared min"},
		{name: "an identifier nothing accounts for", symbol: "A", from: "a.go", to: "b.go",
			files: map[string]string{"a.go": "package app\n\nfunc A() { ghost.Call() }\n"}, want: "neither this file's imports nor the package's declarations account for"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mustRefuse(t, planOf(t, tc.symbol, tc.from, tc.to, tc.files, nil), tc.want)
		})
	}
}

// TestAQualifierThePackageDeclaresIsNotAnImport: cfg.Name selects a field of
// a package variable declared in another file. It needs no import, and must
// not be mistaken for one.
func TestAQualifierThePackageDeclaresIsNotAnImport(t *testing.T) {
	files := map[string]string{
		"a.go":   "package app\n\nfunc Name() string { return cfg.Name }\n",
		"cfg.go": "package app\n\nvar cfg struct{ Name string }\n",
	}
	_, target := mustApply(t, planOf(t, "Name", "a.go", "b.go", files, nil))
	equalText(t, "b.go", target, "package app\n\nfunc Name() string { return cfg.Name }\n")
}

// TestALocalThatShadowsAnImportIsNotAUseOfIt: inside Shadow, fmt is a
// parameter. The import must stay with Print, the function that uses it.
func TestALocalThatShadowsAnImportIsNotAUseOfIt(t *testing.T) {
	files := map[string]string{"a.go": `package app

import "fmt"

type printer struct{}

func (printer) Println(string) {}

func Shadow(fmt printer) { fmt.Println("local") }

func Print() { fmt.Println("import") }
`}
	source, target := mustApply(t, planOf(t, "Shadow", "a.go", "b.go", files, nil))
	if !strings.Contains(source, `import "fmt"`) || strings.Contains(target, "import") {
		t.Errorf("fmt followed a function that only shadows it:\nsource:\n%s\ntarget:\n%s", source, target)
	}

	source, target = mustApply(t, planOf(t, "Print", "a.go", "b.go", files, nil))
	if strings.Contains(source, "import") || !strings.Contains(target, `import "fmt"`) {
		t.Errorf("fmt did not follow its only user:\nsource:\n%s\ntarget:\n%s", source, target)
	}
}

func TestMethods(t *testing.T) {
	files := map[string]string{"a.go": `package app

type Server struct{}

type Client struct{}

// Close stops the server.
func (s *Server) Close() error { return nil }

func (c Client) Close() error { return nil }

func Stack[T any]() {}

type List[T any] struct{ items []T }

func (l *List[T]) Push(v T) { l.items = append(l.items, v) }
`}

	// The bare name finds only methods, and a method is never moved on it.
	mustRefuse(t, planOf(t, "Close", "a.go", "b.go", files, nil), "A method moves only when asked for by its receiver: move Client.Close")

	source, target := mustApply(t, planOf(t, "Server.Close", "a.go", "b.go", files, nil))
	equalText(t, "b.go", target, "package app\n\n// Close stops the server.\nfunc (s *Server) Close() error { return nil }\n")
	if strings.Contains(source, "(s *Server) Close") || !strings.Contains(source, "(c Client) Close") {
		t.Errorf("the wrong method left the source:\n%s", source)
	}

	p := planOf(t, "List.Push", "a.go", "b.go", files, nil)
	_, target = mustApply(t, p)
	if !strings.Contains(target, "func (l *List[T]) Push(v T)") || p.Decl.Kind != "method" || p.Decl.Receiver != "List" {
		t.Errorf("generic method: decl=%+v target:\n%s", p.Decl, target)
	}

	mustRefuse(t, planOf(t, "Server.Open", "a.go", "b.go", files, nil), "no method Open with receiver type Server")
	mustRefuse(t, planOf(t, "Client.Close", "a.go", "b.go",
		map[string]string{"a.go": files["a.go"], "b.go": "package app\n\nfunc (c Client) Close() error { return nil }\n"}, nil),
		"already declares method Client.Close")

	// Moving the type says where its methods are.
	p = planOf(t, "Server", "a.go", "b.go", files, nil)
	mustApply(t, p)
	if n := strings.Join(p.Notes, " "); !strings.Contains(n, "Methods of Server stay in a.go") {
		t.Errorf("notes %q do not say the methods stay", p.Notes)
	}
}

func TestGroupedDeclarations(t *testing.T) {
	const src = `package app

import "time"

// Limits.
var (
	// Timeout bounds one request.
	Timeout = 30 * time.Second // generous

	Retries  = 3
	MaxConns int
)

type (
	// ID names a thing.
	ID string

	Pair struct {
		A, B ID
	}
)

const (
	Small = 1
	Large = 100
)

const (
	Red = iota
	Green
	Blue
)

const (
	KB = 1 << 10
	MB
)

var (
	only = "one"
)
`
	files := map[string]string{"a.go": src}

	t.Run("a member becomes a declaration of its own", func(t *testing.T) {
		source, target := mustApply(t, planOf(t, "Timeout", "a.go", "b.go", files, nil))
		equalText(t, "b.go", target, `package app

import "time"

// Timeout bounds one request.
var Timeout = 30 * time.Second // generous
`)
		if strings.Contains(source, "Timeout") || strings.Contains(source, `"time"`) {
			t.Errorf("Timeout or its import is still in the source:\n%s", source)
		}
		if !strings.Contains(source, "// Limits.\nvar (\n\tRetries  = 3\n\tMaxConns int\n)\n") {
			t.Errorf("the rest of the group was disturbed:\n%s", source)
		}
	})

	t.Run("a multi-line member", func(t *testing.T) {
		source, target := mustApply(t, planOf(t, "Pair", "a.go", "b.go", files, nil))
		equalText(t, "b.go", target, "package app\n\ntype Pair struct {\n\tA, B ID\n}\n")
		if !strings.Contains(source, "type (\n\t// ID names a thing.\n\tID string\n)\n") {
			t.Errorf("the rest of the group was disturbed:\n%s", source)
		}
	})

	t.Run("a member of a constant group with explicit values", func(t *testing.T) {
		source, target := mustApply(t, planOf(t, "Large", "a.go", "b.go", files, nil))
		equalText(t, "b.go", target, "package app\n\nconst Large = 100\n")
		if !strings.Contains(source, "const (\n\tSmall = 1\n)\n") {
			t.Errorf("the rest of the group was disturbed:\n%s", source)
		}
	})

	t.Run("the only member takes its group with it", func(t *testing.T) {
		source, target := mustApply(t, planOf(t, "only", "a.go", "b.go", files, nil))
		equalText(t, "b.go", target, "package app\n\nvar (\n\tonly = \"one\"\n)\n")
		if strings.Contains(source, "only") || strings.HasSuffix(source, "\n\n") {
			t.Errorf("the group or a trailing blank line was left behind:\n%q", source)
		}
	})

	t.Run("an iota group is a unit", func(t *testing.T) {
		for _, name := range []string{"Red", "Green", "Blue"} {
			mustRefuse(t, planOf(t, name, "a.go", "b.go", files, nil), "iota numbers the members")
		}
	})

	t.Run("so is a group with an implicit value", func(t *testing.T) {
		for _, name := range []string{"KB", "MB"} {
			mustRefuse(t, planOf(t, name, "a.go", "b.go", files, nil), "takes its value from the one above it")
		}
	})

	t.Run("a constant declared alone may use iota", func(t *testing.T) {
		_, target := mustApply(t, planOf(t, "Zero", "a.go", "b.go",
			map[string]string{"a.go": "package app\n\nconst Zero = iota\n\nfunc F() {}\n"}, nil))
		equalText(t, "b.go", target, "package app\n\nconst Zero = iota\n")
	})
}

func TestBuildConstraints(t *testing.T) {
	const fn = "package app\n\nfunc A() {}\n\nfunc B() {}\n"
	tagged := "//go:build linux && amd64\n\n" + fn

	t.Run("a new file takes the source's constraint", func(t *testing.T) {
		p := planOf(t, "A", "a.go", "b.go", map[string]string{"a.go": tagged}, nil)
		_, target := mustApply(t, p)
		equalText(t, "b.go", target, "//go:build linux && amd64\n\npackage app\n\nfunc A() {}\n")
		if !strings.Contains(p.Sites[0].Detail, "//go:build linux && amd64") {
			t.Errorf("the create-file site %q does not state the constraint", p.Sites[0].Detail)
		}
	})

	t.Run("the same constraint, written differently", func(t *testing.T) {
		mustApply(t, planOf(t, "A", "a.go", "b.go", map[string]string{
			"a.go": tagged,
			"b.go": "//go:build linux&&amd64\n\npackage app\n",
		}, nil))
	})

	refused := []struct {
		name     string
		from, to string
		files    map[string]string
		want     string
	}{
		{"an untagged target", "a.go", "b.go", map[string]string{"a.go": tagged, "b.go": "package app\n"}, "compiled under different conditions"},
		{"a tagged target", "a.go", "b.go", map[string]string{"a.go": fn, "b.go": "//go:build windows\n\npackage app\n"}, "only under //go:build windows"},
		{"a different tag", "a.go", "b.go", map[string]string{"a.go": tagged, "b.go": "//go:build linux\n\npackage app\n"}, "compiled under different conditions"},
		{"a new file whose name constrains it", "a.go", "b_windows.go", map[string]string{"a.go": fn}, "only under GOOS=windows"},
		{"an existing file whose name constrains it", "a.go", "b_linux_arm64.go", map[string]string{"a.go": fn, "b_linux_arm64.go": "package app\n"}, "GOOS=linux, GOARCH=arm64"},
		{"out of a platform file", "a_darwin.go", "b.go", map[string]string{"a_darwin.go": fn}, "only under GOOS=darwin"},
		{"into a test file", "a.go", "a_test.go", map[string]string{"a.go": fn, "a_test.go": "package app\n"}, "only under a test build"},
		{"into a new test file", "a.go", "b_test.go", map[string]string{"a.go": fn}, "only under a test build"},
		{"out of a test file", "a_test.go", "a.go", map[string]string{"a_test.go": fn, "a.go": "package app\n"}, "only under a test build"},
		{"an old-style constraint", "a.go", "b.go", map[string]string{"a.go": "// +build linux\n\n" + fn}, "old-style"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			mustRefuse(t, planOf(t, "A", tc.from, tc.to, tc.files, nil), tc.want)
		})
	}

	t.Run("between files with the same name constraint", func(t *testing.T) {
		mustApply(t, planOf(t, "A", "a_linux.go", "b_linux.go", map[string]string{"a_linux.go": fn}, nil))
		mustApply(t, planOf(t, "A", "a_linux.go", "b_linux.go", map[string]string{"a_linux.go": fn, "b_linux.go": "package app\n"}, nil))
	})

	t.Run("between test files", func(t *testing.T) {
		p := planOf(t, "TestA", "a_test.go", "b_test.go", map[string]string{
			"a_test.go": "package app\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n",
		}, nil)
		_, target := mustApply(t, p)
		equalText(t, "b_test.go", target, "package app\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n")
		if !p.TestFiles {
			t.Error("the plan does not say only a test build compiles these files")
		}
	})
}

// TestADocCommentIsPutInGofmtsFormWhereTheFileIsFormatted: a doc comment that
// gofmt would rewrite — an empty last line, an unindented list — is left
// alone inside a group, where it is not a top-level doc comment, and in a
// file that was never formatted. Once the declaration stands at top level in
// a formatted file, gofmt's form is the only one that keeps the file
// formatted, and the planner's own check has to recognise it as the same
// comment. (go/ast's own source is written this way.)
func TestADocCommentIsPutInGofmtsFormWhereTheFileIsFormatted(t *testing.T) {
	const grouped = `package app

type (
	// A Node is one of:
	//   - a leaf
	//   - a branch
	//
	Node struct {
		Kids []Node // none for a leaf
	}

	// Other stays.
	Other int
)
`
	const wantDoc = "// A Node is one of:\n//   - a leaf\n//   - a branch\ntype Node struct {\n\tKids []Node // none for a leaf\n}\n"

	t.Run("a group member into a new file", func(t *testing.T) {
		source, target := mustApply(t, planOf(t, "Node", "a.go", "b.go", map[string]string{"a.go": grouped}, nil))
		equalText(t, "b.go", target, "package app\n\n"+wantDoc)
		equalText(t, "a.go", source, "package app\n\ntype (\n\t// Other stays.\n\tOther int\n)\n")
	})
	t.Run("a group member into a formatted file", func(t *testing.T) {
		_, target := mustApply(t, planOf(t, "Node", "a.go", "b.go",
			map[string]string{"a.go": grouped, "b.go": "package app\n\nvar B = 1\n"}, nil))
		equalText(t, "b.go", target, "package app\n\nvar B = 1\n\n"+wantDoc)
	})

	// The same comment on a top-level declaration of a file that was not
	// formatted: moved as it is into an unformatted file, in gofmt's form
	// into a formatted one.
	const loose = "package app\n\n// F does a thing.\n//\nfunc  F( )  { }\n\nfunc  G( )  { }\n"
	t.Run("an unformatted declaration into an unformatted file", func(t *testing.T) {
		_, target := mustApply(t, planOf(t, "F", "a.go", "b.go",
			map[string]string{"a.go": loose, "b.go": "package app\n\nfunc  H( )  { }\n"}, nil))
		equalText(t, "b.go", target, "package app\n\nfunc  H( )  { }\n\n// F does a thing.\n//\nfunc  F( )  { }\n")
	})
	t.Run("an unformatted declaration into a formatted file", func(t *testing.T) {
		_, target := mustApply(t, planOf(t, "F", "a.go", "b.go",
			map[string]string{"a.go": loose, "b.go": "package app\n\nfunc H() {}\n"}, nil))
		equalText(t, "b.go", target, "package app\n\nfunc H() {}\n\n// F does a thing.\nfunc F() {}\n")
	})
}

// TestKnownPlatformsMatchGoBuild: the two lists are copies of go/build's, and
// a platform missing from them would make its file names look unconstrained.
// go/build is asked directly: a name it refuses to build for an impossible
// platform is one it reads a constraint from.
func TestKnownPlatformsMatchGoBuild(t *testing.T) {
	ctxt := build.Default
	ctxt.GOOS, ctxt.GOARCH = "noos", "noarch"
	ctxt.OpenFile = func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("package p\n")), nil
	}
	ctxt.ReadDir = func(string) ([]fs.FileInfo, error) { return nil, nil }
	constrained := func(name string) bool {
		ok, err := ctxt.MatchFile("", name)
		if err != nil {
			t.Fatalf("MatchFile(%s): %v", name, err)
		}
		return !ok
	}
	for name := range knownOS {
		if !constrained("x_" + name + ".go") {
			t.Errorf("go/build does not treat %q as an operating system", name)
		}
	}
	for name := range knownArch {
		if !constrained("x_" + name + ".go") {
			t.Errorf("go/build does not treat %q as an architecture", name)
		}
	}
	if !knownOS[runtime.GOOS] || !knownArch[runtime.GOARCH] {
		t.Errorf("the running platform %s/%s is missing from the lists", runtime.GOOS, runtime.GOARCH)
	}
	for name, want := range map[string]string{
		"a.go":               "",
		"linux.go":           "",
		"a_linux.go":         "GOOS=linux",
		"a_amd64.go":         "GOARCH=amd64",
		"a_linux_amd64.go":   "GOOS=linux, GOARCH=amd64",
		"a_amd64_linux.go":   "GOOS=linux",
		"linux_amd64.go":     "GOARCH=amd64",
		"a_test.go":          "a test build",
		"a_windows_test.go":  "a test build, GOOS=windows",
		"a_linux.gen.go":     "GOOS=linux",
		"a_unknown.go":       "",
		"a_linux_helpers.go": "",
	} {
		if got := nameConstraint(name); got != want {
			t.Errorf("nameConstraint(%q) = %q, want %q", name, got, want)
		}
		if (want != "" && want != "a test build") != constrained(name) {
			t.Errorf("go/build disagrees about whether %q is constrained to a platform", name)
		}
	}
}

func TestInitialisationOrder(t *testing.T) {
	const inert = `package app

import (
	"errors"
	"regexp"
	"time"
)

type Kind int

type handler struct{ name string }

var (
	ErrMissing = errors.New("missing")
	pattern    = regexp.MustCompile("^[a-z]+$")
	timeout    = time.Duration(30) * time.Second
	table      = map[string]Kind{"a": Kind(1), "b": 2}
	first      = &handler{name: "first"}
	lazy       = func() int { return compute() }
	_ I        = (*handler)(nil)
	size       = len(table)
	zero       int
)

type I interface{}

func compute() int { return 1 }
`
	t.Run("a package whose initialisers run no code", func(t *testing.T) {
		for _, name := range []string{"ErrMissing", "pattern", "timeout", "table", "first", "lazy", "size"} {
			if p := planOf(t, name, "a.go", "b.go", map[string]string{"a.go": inert}, nil); !p.OK() {
				t.Errorf("%s was refused: %+v", name, p.Refusals)
			}
		}
	})

	withCall := map[string]string{
		"a.go":    inert,
		"flag.go": "package app\n\nimport \"flag\"\n\nvar verbose = flag.Bool(\"v\", false, \"\")\n\nvar plain = 7\n",
	}
	t.Run("an initialiser elsewhere in the package that runs code", func(t *testing.T) {
		p := planOf(t, "ErrMissing", "a.go", "b.go", withCall, nil)
		mustRefuse(t, p, "the initialiser of verbose at flag.go:5 calls flag.Bool")
		mustRefuse(t, p, "order in which the package's variables are initialised")
	})

	t.Run("a variable without an initialiser is not ordered at all", func(t *testing.T) {
		mustApply(t, planOf(t, "zero", "a.go", "b.go", withCall, nil))
	})

	t.Run("functions, types and constants are not ordered either", func(t *testing.T) {
		for _, name := range []string{"compute", "Kind", "I"} {
			mustApply(t, planOf(t, name, "a.go", "b.go", withCall, nil))
		}
	})

	for name, init := range map[string]string{
		"a call to one of the package's functions": "compute()",
		"a method call":                            "first.String()",
		"an immediately called function literal":   "func() int { return 1 }()",
		"a channel receive":                        "<-make(chan int)",
		"append, which can write through a slice":  "append([]int(nil), 1)",
		"fmt with a value that may have a String":  `fmt.Sprint(first)`,
		"a call through a pointer conversion look": "(*compute)(nil)",
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"a.go": "package app\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n\ntype handler struct{}\n\n" +
				"var first handler\n\nfunc compute() int { return 1 }\n\nvar moved = " + init + "\n"}
			mustRefuse(t, planOf(t, "moved", "a.go", "b.go", files, nil), "initialiser of moved at a.go:")
		})
	}

	t.Run("fmt with literals only is inert", func(t *testing.T) {
		mustApply(t, planOf(t, "msg", "a.go", "b.go",
			map[string]string{"a.go": "package app\n\nimport \"fmt\"\n\nvar msg = fmt.Sprintf(\"%d items\", 3)\n\nfunc F() {}\n"}, nil))
	})

	t.Run("a package function that shadows a builtin is a call", func(t *testing.T) {
		mustRefuse(t, planOf(t, "n", "a.go", "b.go",
			map[string]string{"a.go": "package app\n\nfunc len(s string) int { return 0 }\n\nvar n = len(\"x\")\n"}, nil),
			"calls len")
	})
}

// TestAFileThatWasNotFormattedIsLeftAsItWas: only the moved lines change. A
// file its author did not gofmt is not reformatted behind their back.
func TestAFileThatWasNotFormattedIsLeftAsItWas(t *testing.T) {
	const a = "package app\n\nimport   \"os\"\n\n\n\nfunc  Keep( )  { _ = os.Args }\n\n\n// Go moves.\nfunc  Go( x int ) int {\n  return x+1\n}\n\n\nfunc  Stay( )  { }\n"
	const b = "package app\n\n\nfunc  Other( )  { }\n"
	source, target := mustApply(t, planOf(t, "Go", "a.go", "b.go", map[string]string{"a.go": a, "b.go": b}, nil))
	equalText(t, "a.go", source, "package app\n\nimport   \"os\"\n\n\n\nfunc  Keep( )  { _ = os.Args }\n\n\nfunc  Stay( )  { }\n")
	equalText(t, "b.go", target, "package app\n\n\nfunc  Other( )  { }\n\n// Go moves.\nfunc  Go( x int ) int {\n  return x+1\n}\n")
}

// TestLineEndingsAreKept: a CRLF file stays CRLF, and a declaration arriving
// in one takes its line endings.
func TestLineEndingsAreKept(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	a := crlf("package app\n\nimport \"os\"\n\n// Args returns them.\nfunc Args() []string {\n\treturn os.Args\n}\n\nfunc Stay() {}\n")

	t.Run("a new file from a CRLF source", func(t *testing.T) {
		source, target := mustApply(t, planOf(t, "Args", "a.go", "b.go", map[string]string{"a.go": a}, nil))
		equalText(t, "a.go", source, crlf("package app\n\nfunc Stay() {}\n"))
		equalText(t, "b.go", target, crlf("package app\n\nimport \"os\"\n\n// Args returns them.\nfunc Args() []string {\n\treturn os.Args\n}\n"))
	})

	t.Run("a CRLF declaration into an LF file", func(t *testing.T) {
		_, target := mustApply(t, planOf(t, "Args", "a.go", "b.go", map[string]string{"a.go": a, "b.go": "package app\n\nfunc B() {}\n"}, nil))
		if strings.Contains(target, "\r") {
			t.Errorf("a carriage return reached an LF file: %q", target)
		}
		equalText(t, "b.go", target, "package app\n\nimport \"os\"\n\nfunc B() {}\n\n// Args returns them.\nfunc Args() []string {\n\treturn os.Args\n}\n")
	})

	t.Run("an LF declaration into a CRLF file", func(t *testing.T) {
		_, target := mustApply(t, planOf(t, "B", "b.go", "a.go", map[string]string{"a.go": a, "b.go": "package app\n\nfunc B() {\n\tprintln()\n}\n"}, nil))
		if strings.Count(target, "\n") != strings.Count(target, "\r\n") {
			t.Errorf("a bare line feed reached a CRLF file: %q", target)
		}
	})
}

// TestCommentsThatAreNotTheDeclarationsStay: a comment set apart by a blank
// line is not the doc comment, and a trailing comment on the last line is
// part of the declaration.
func TestCommentsThatAreNotTheDeclarationsStay(t *testing.T) {
	const a = `package app

// A section heading, not a doc comment.

// A does a thing.
func A() {
	// inside
} // end of A

// B stays.
func B() {}
`
	source, target := mustApply(t, planOf(t, "A", "a.go", "b.go", map[string]string{"a.go": a}, nil))
	equalText(t, "a.go", source, "package app\n\n// A section heading, not a doc comment.\n\n// B stays.\nfunc B() {}\n")
	equalText(t, "b.go", target, "package app\n\n// A does a thing.\nfunc A() {\n\t// inside\n} // end of A\n")
}

// TestUnnamedImports: the paths whose package names the caller must ask the
// go command for.
func TestUnnamedImports(t *testing.T) {
	got := UnnamedImports(
		[]byte("package a\n\nimport (\n\t\"fmt\"\n\ty \"gopkg.in/yaml.v3\"\n\t_ \"embed\"\n\t\"example.com/x\"\n)\n"),
		[]byte("package a\n\nimport \"C\"\nimport \"fmt\"\nimport \"os\"\n"),
		[]byte("not go"),
	)
	if strings.Join(got, " ") != "example.com/x fmt os" {
		t.Errorf("UnnamedImports = %v", got)
	}
}

func TestStandardLibraryNames(t *testing.T) {
	for path, want := range map[string]string{
		"fmt": "fmt", "net/http": "http", "math/rand/v2": "rand", "encoding/json": "json",
		"gopkg.in/yaml.v3": "", "example.com/fmt": "", "myapp/internal/x": "",
	} {
		if got, _ := stdName(path); got != want {
			t.Errorf("stdName(%q) = %q, want %q", path, got, want)
		}
	}
}
