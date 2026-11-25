package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
)

type fakeDocs struct {
	files    []registry.DocFile
	text     map[string]string
	sections map[int64][]registry.DocSection
	defs     map[string][]registry.SymbolHit
}

func (f fakeDocs) DocFiles(context.Context, int64) ([]registry.DocFile, error) { return f.files, nil }
func (f fakeDocs) DocSections(_ context.Context, _, id int64) ([]registry.DocSection, error) {
	return f.sections[id], nil
}
func (f fakeDocs) FileContent(_ context.Context, _ int64, p string) (string, error) {
	return f.text[p], nil
}
func (f fakeDocs) FindSymbolsByName(_ context.Context, _ int64, n, _ string, _ int) ([]registry.SymbolHit, error) {
	return f.defs[n], nil
}

func TestFindMentionsFormsAndFences(t *testing.T) {
	text := "Intro about the TransactionService.\n" + // distinctive prose
		"Use `TransactionService` here.\n" + // inline code
		"```go\nsvc := TransactionService{}\n```\n" + // fenced
		"Not TransactionServiceFactory.\n" // part of a longer identifier
	got := findMentions(text, "TransactionService")
	if len(got) != 3 {
		t.Fatalf("want 3 mentions (prose, inline, fenced), got %d: %+v", len(got), got)
	}
	if got[0].Form != formProse || got[1].Form != formCode || got[2].Form != formCode {
		t.Errorf("forms: %+v", got)
	}
}

func TestPlainWordsAreNotMentionsUnlessCoded(t *testing.T) {
	text := "The service handles orders.\nCall `service` directly.\n"
	got := findMentions(text, "service")
	if len(got) != 1 || got[0].Form != formCode {
		t.Errorf("a plain word counts only inside code: %+v", got)
	}
	if got := findMentions("see `id`", "id"); len(got) != 0 {
		t.Errorf("a name under %d letters is a word: %+v", minMentionName, got)
	}
}

func TestDocsMentioningOrdersAndNamesSections(t *testing.T) {
	f := fakeDocs{
		files: []registry.DocFile{{ID: 1, Path: "docs/b.md"}, {ID: 2, Path: "docs/a.md"}},
		text: map[string]string{
			"docs/b.md": "# B\nthe TransactionService does it\n",
			"docs/a.md": "# A\n## Design\nUse `TransactionService` here\n",
		},
		sections: map[int64][]registry.DocSection{
			1: {{Name: "B", StartLine: 1}},
			2: {{Name: "A", StartLine: 1}, {Name: "Design", StartLine: 2}},
		},
	}
	ms, err := docsMentioning(context.Background(), f, 1, "TransactionService", 10)
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v %+v", err, ms)
	}
	if ms[0].Doc != "docs/a.md" || ms[0].Section != "Design" || ms[0].Form != formCode {
		t.Errorf("code mentions first, with their section: %+v", ms[0])
	}
	var b bytes.Buffer
	writeMentions(&b, "TransactionService", ms)
	if !strings.Contains(b.String(), "not proven relationships") {
		t.Errorf("output:\n%s", b.String())
	}
}

func TestSymbolsInDocSeparatesCleanFromAmbiguous(t *testing.T) {
	f := fakeDocs{
		text: map[string]string{"adr.md": "We chose `RetryPolicy`, `SaveOrder` and `data`.\n```\n`Ignored` in fence\n```\nAlso `Missing` and `RetryPolicy()`.\n"},
		defs: map[string][]registry.SymbolHit{
			"RetryPolicy": {{Name: "RetryPolicy", FilePath: "a.go", StartLine: 3}},
			"SaveOrder":   {{Name: "SaveOrder"}, {Name: "SaveOrder"}},
			"data":        {{Name: "data", FilePath: "x.go", StartLine: 1}},
		},
	}
	ss, err := symbolsInDoc(context.Background(), f, 1, "adr.md")
	if err != nil || len(ss) != 2 {
		t.Fatalf("a plain word like `data` is not a symbol link: %v %+v", err, ss)
	}
	if ss[0].Name != "RetryPolicy" || ss[0].Where != "a.go:3" || len(ss[0].Lines) != 2 {
		t.Errorf("clean link with both lines: %+v", ss[0])
	}
	if ss[1].Name != "SaveOrder" || ss[1].Definitions != 2 || ss[1].Where != "" {
		t.Errorf("ambiguous name has no single location: %+v", ss[1])
	}
}
