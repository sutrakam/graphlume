package parse

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

// TestDumpTree is a development aid for writing a language's definition query.
// It is skipped unless GRAPHLUME_DUMP_FILE names a source file.
//
//	GRAPHLUME_DUMP_FILE=/abs/path/sample.nim GRAPHLUME_DUMP_LANG=nim \
//	    go test -count=1 -run TestDumpTree -v ./internal/parse/
//
// It prints three things: the syntax tree the grammar builds for the file, so
// a query can be written against the node types that are really there; the
// query text in force for the language (inferred, supplement and registered);
// and what that query extracts, in the form expected.tsv uses.
//
// GRAPHLUME_DUMP_LANG may be left out when the file's name decides the
// grammar. The tree is printed for a grammar with no definition query too:
// that is the starting point for writing one.
func TestDumpTree(t *testing.T) {
	file := os.Getenv("GRAPHLUME_DUMP_FILE")
	if file == "" {
		t.Skip("set GRAPHLUME_DUMP_FILE to print a file's syntax tree and what is extracted from it")
	}
	src, err := os.ReadFile(file) //nolint:gosec // a path the developer running the test supplied
	if err != nil {
		t.Fatal(err)
	}

	var entry *grammars.LangEntry
	if name := os.Getenv("GRAPHLUME_DUMP_LANG"); name != "" {
		entry = grammars.DetectLanguageByName(name)
	} else {
		entry = grammars.DetectLanguage(file)
	}
	if entry == nil {
		t.Fatalf("no grammar for %s; set GRAPHLUME_DUMP_LANG to a grammar name", file)
	}

	p := newGenericParser(*entry, defaultParserConfig)
	if p.lang == nil {
		t.Fatalf("grammar %q did not load: %v", entry.Name, p.initErr)
	}

	_, tree, stop, err := p.parseTree(t.Context(), src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fmt.Printf("== grammar: %s\n", entry.Name)
	if stop.early {
		fmt.Printf("== the parser stopped before the end of the file: %+v\n", stop)
	}
	fmt.Printf("== syntax tree\n%s\n", indentSExpr(tree.RootNode().SExpr(tree.Language())))

	fmt.Printf("\n== definition query in force\n%s\n", strings.TrimSpace(definitionQueryText(*entry)))
	if p.initErr != nil {
		fmt.Printf("\n== nothing is extracted: %v\n", p.initErr)
		return
	}

	res, err := p.Parse(t.Context(), file, src)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	fmt.Printf("\n== extracted (status %s", res.Status)
	if res.Err != nil {
		fmt.Printf(": %v", res.Err)
	}
	fmt.Printf(")\n")
	for _, s := range res.Symbols {
		fmt.Printf("%d\t%s\t%s\n", s.Range.Start.Line, s.Kind, s.Name)
	}
	fmt.Printf("\n== references: %d, imports: %d\n", len(res.References), len(res.Imports))
	for _, r := range res.References {
		fmt.Printf("%d\t%s\t%s\t%q %s\n", r.Range.Start.Line, r.Kind, r.Name, r.Receiver, r.ReceiverKind)
	}
}

// indentSExpr breaks an S-expression onto one line per node, indented by
// depth, so a tree of any size can be read and searched.
func indentSExpr(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '(':
			if b.Len() > 0 {
				b.WriteByte('\n')
				b.WriteString(strings.Repeat("  ", depth))
			}
			b.WriteByte(c)
			depth++
		case ')':
			b.WriteByte(c)
			if depth > 0 {
				depth--
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
