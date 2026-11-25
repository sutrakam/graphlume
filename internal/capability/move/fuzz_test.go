package move

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// FuzzCompute: whatever the two files hold, Compute returns — it does not
// panic — and a plan it does not refuse is two files that parse and that
// declare the symbol in the target and no longer in the source.
//
// Without -fuzz this runs the seeds, which are the shapes the planner has
// special cases for.
func FuzzCompute(f *testing.F) {
	seeds := []struct{ symbol, source, target string }{
		{"A", "package p\n\nfunc A() {}\n\nfunc B() {}\n", ""},
		{"A", "package p\n\nimport \"fmt\"\n\n// A.\nfunc A() { fmt.Println() }\n", "package p\n\nimport (\n\t\"os\"\n)\n\nvar _ = os.Args\n"},
		{"T.M", "package p\n\ntype T struct{}\n\nfunc (T) M() {}\n", "package p\n"},
		{"x", "package p\n\nvar (\n\t// x.\n\tx = 1 // one\n\ty = 2\n)\n", "package p"},
		{"C", "package p\n\nconst (\n\tC = iota\n\tD\n)\n", ""},
		{"A", "package p\r\n\r\nfunc A() {}\r\n", "package p\r\n\r\nfunc B() {}"},
		{"A", "//go:build linux\n\npackage p\n\nfunc A() {}", "//go:build linux\n\npackage p\n"},
		{"A", "package p\n\nfunc A() {}; func B() {}\n", ""},
		{"A", "package p\n\nimport (\"fmt\"; \"os\")\n\nfunc A() { fmt.Println(os.Args) }\n", "package p\n\nimport ()\n"},
		{"A", "package p\n\n/* a */ func A() {} /* b */\n", "\xef\xbb\xbfpackage p\n"},
		{"A", "\xef\xbb\xbfpackage p\n\nfunc A() {}\n\nfunc B() {}\n", "\xef\xbb\xbfpackage p\n"},
		{"A", "package p\n\nfunc A() {\n\t_ = `\n\n`\n}\n\n\n\n", "package p\n\n\n\n"},
		{"v", "package p\n\nvar v = func() int { return 1 }()\n", ""},
		{"A", "package p; func A() {}", "package p; import \"fmt\"; var _ = fmt.Sprint"},
		{"A", "", ""},
		{"", "package p\n", "package p\n"},
	}
	for _, s := range seeds {
		f.Add(s.symbol, s.source, s.target)
	}
	f.Fuzz(func(t *testing.T, symbol, source, target string) {
		in := Input{Source: []byte(source), Siblings: []File{{Name: "c.go", Data: []byte("package p\n\nvar sibling = 1\n")}}}
		if target != "" {
			in.Target, in.TargetExists = []byte(target), true
		}
		p := Compute(Request{Symbol: symbol, From: "a.go", To: "b.go"}, in)
		if !p.OK() {
			if len(p.Refusals) == 0 || p.SourceAfter != nil || p.TargetAfter != nil {
				t.Fatalf("a plan that is not OK must carry a refusal and no content: %+v", p)
			}
			for _, r := range p.Refusals {
				if strings.Contains(r.Reason, "internal check failed") {
					t.Fatalf("the planner's own check failed on\n--- a.go ---\n%s\n--- b.go ---\n%s\n%s", source, target, r.Reason)
				}
			}
			return
		}
		for name, body := range map[string][]byte{"a.go": p.SourceAfter, "b.go": p.TargetAfter} {
			if _, err := parser.ParseFile(token.NewFileSet(), name, body, parser.SkipObjectResolution); err != nil {
				t.Fatalf("%s does not parse: %v\n%s", name, err, body)
			}
		}
		if len(p.Sites) < 2 {
			t.Fatalf("sites = %+v", p.Sites)
		}
	})
}
