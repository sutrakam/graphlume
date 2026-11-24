package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func TestFTSQueryTurnsPunctuationIntoPhrases(t *testing.T) {
	cases := []struct {
		raw, want string
		rewritten []string
	}{
		// A plain word is a one-word phrase: what it always was.
		{"handler", `"handler"`, nil},
		{"two words", `"two" "words"`, nil},
		// The punctuation an identifier query carries.
		{"errors.New", `"errors New"`, []string{"errors New"}},
		{"Utils::chooseHandler", `"Utils chooseHandler"`, []string{"Utils chooseHandler"}},
		{"$this->handler", `"this handler"`, []string{"this handler"}},
		{"ptr->next", `"ptr next"`, []string{"ptr next"}},
		{"#include", `"include"`, []string{"include"}},
		{"@Override", `"Override"`, []string{"Override"}},
		{"$scope", `"scope"`, []string{"scope"}},
		{"get_user", `"get user"`, []string{"get user"}},
		{"a.b.c d", `"a b c" "d"`, []string{"a b c"}},
		// The engine's own syntax, kept where it is valid.
		{"alpha OR beta", `"alpha" OR "beta"`, nil},
		{"alpha AND beta NOT gamma", `"alpha" AND "beta" NOT "gamma"`, nil},
		{"hand*", `"hand"*`, nil},
		{`"exact phrase" other`, `"exact phrase" "other"`, nil},
		// The same syntax where it would be an error: treated as words, or dropped.
		{"OR alpha", `"OR" "alpha"`, nil},
		{"alpha OR", `"alpha" "OR"`, nil},
		{"alpha AND ->", `"alpha" "AND"`, nil},
		{"NOT", `"NOT"`, nil},
		// Nothing to search for.
		{"->", "", nil},
		{"::", "", nil},
		{"  ", "", nil},
		{`"`, "", nil},
		{`a"b`, `"a" "b"`, nil},
	}
	for _, tc := range cases {
		got, rewritten := ftsQuery(tc.raw)
		if got != tc.want {
			t.Errorf("ftsQuery(%q) = %s, want %s", tc.raw, got, tc.want)
		}
		if strings.Join(rewritten, "|") != strings.Join(tc.rewritten, "|") {
			t.Errorf("ftsQuery(%q) rewritten = %q, want %q", tc.raw, rewritten, tc.rewritten)
		}
	}
}

func seedSearchable(ctx context.Context, reg *registry.Registry, runID int64) {
	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		panic(err)
	}
	add := func(path, lang, text string) {
		f := model.File{Path: path, Language: lang, ContentHash: "h-" + path, ParseStatus: "ok"}
		id, err := w.AddFile(ctx, &f)
		if err != nil {
			panic(err)
		}
		if err := w.IndexFileText(ctx, id, path, text); err != nil {
			panic(err)
		}
	}
	add("gin.go", "go", "func fail() error {\n\treturn errors.New(\"boom\")\n}\n")
	add("src/Client.php", "php", "<?php\n$handler = Utils::chooseHandler();\n$this->handler = $handler;\n")
	add("list.c", "c", "#include <stdio.h>\nvoid walk(struct node *ptr) { ptr = ptr->next; }\n")
	add("src/Service.java", "java", "class Service {\n  @Override\n  public void run() {}\n}\n")
	add("unrelated.go", "go", "package unrelated\n// nothing of interest\n")
	_ = w.Flush(ctx)
	_ = w.Close()
}

// TestSearchCodeAcceptsThePunctuationIdentifiersCarry: each of these failed as
// `error_code: internal ... fts5: syntax error`, with the hint to fall back to
// search.
func TestSearchCodeAcceptsThePunctuationIdentifiersCarry(t *testing.T) {
	sess := newTestServer(t, seedSearchable)

	cases := []struct{ query, wantPath string }{
		{"errors.New", "gin.go"},
		{"Utils::chooseHandler", "src/Client.php"},
		{"$this->handler", "src/Client.php"},
		{"ptr->next", "list.c"},
		{"#include", "list.c"},
		{"@Override", "src/Service.java"},
		{"$handler", "src/Client.php"},
		{"stdio.h", "list.c"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			text, res := answer(t, sess, "search", map[string]any{"query": tc.query})
			if res.IsError {
				t.Fatalf("search failed on %q:\n%s", tc.query, text)
			}
			if strings.Contains(text, "error_code") {
				t.Errorf("answer carries an error code:\n%s", text)
			}
			if !strings.Contains(text, "- file  "+tc.wantPath) {
				t.Errorf("%q did not find %s:\n%s", tc.query, tc.wantPath, text)
			}
			if strings.Contains(text, "- file  unrelated.go") {
				t.Errorf("%q matched a file that holds none of its words:\n%s", tc.query, text)
			}
		})
	}

	// Adjacent words, not merely both present: `errors.New` must not match a
	// file that says "new errors" somewhere.
	if text, _ := answer(t, sess, "search", map[string]any{"query": "New.errors"}); !strings.Contains(text, "0 file(s)") {
		t.Errorf("a phrase matched with its words in the wrong order:\n%s", text)
	}
}

func TestSearchCodeSaysWhatItSearchedFor(t *testing.T) {
	sess := newTestServer(t, seedSearchable)
	text, _ := answer(t, sess, "search", map[string]any{"query": "errors.New"})
	if !strings.Contains(text, `Searched for "errors New" as adjacent words`) {
		t.Errorf("the answer does not say the query was read as a phrase:\n%s", text)
	}
	plain, _ := answer(t, sess, "search", map[string]any{"query": "errors"})
	if strings.Contains(plain, "Searched for") {
		t.Errorf("a plain word was reported as rewritten:\n%s", plain)
	}
}

// TestSearchCodeWithNothingToSearchForIsGuidance: a query of punctuation alone
// has no word in it. That is the caller's to fix and is not a malfunction.
func TestSearchCodeWithNothingToSearchForIsGuidance(t *testing.T) {
	sess := newTestServer(t, seedSearchable)
	for _, q := range []string{"->", "::", "...", "$", "@#"} {
		text, res := answer(t, sess, "search", map[string]any{"query": q})
		if res.IsError {
			t.Errorf("query %q is flagged isError:\n%s", q, text)
		}
		if !strings.Contains(firstLine(text), "Nothing to search for") {
			t.Errorf("query %q: first line = %q", q, firstLine(text))
		}
	}
}

// TestSearchCodeLimitIsClampedNotReset: limit=500 must not return the default
// twenty.
func TestSearchCodeLimitIsClampedNotReset(t *testing.T) {
	sess := newTestServer(t, func(ctx context.Context, reg *registry.Registry, runID int64) {
		w, _ := reg.NewWriter(ctx, runID)
		for i := 0; i < 60; i++ {
			f := model.File{Path: "f" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".go",
				Language: "go", ContentHash: "h", ParseStatus: "ok"}
			id, err := w.AddFile(ctx, &f)
			if err != nil {
				panic(err)
			}
			_ = w.IndexFileText(ctx, id, f.Path, "needle here\n")
		}
		_ = w.Flush(ctx)
		_ = w.Close()
	})
	def, _ := answer(t, sess, "search", map[string]any{"query": "needle"})
	if n := rowCount(def); n != defaultSearchItems {
		t.Errorf("default carried %d files, want %d", n, defaultSearchItems)
	}
	if !strings.Contains(def, "more match") || !strings.Contains(def, "truncated:") {
		t.Errorf("a cut search does not say more files match:\n%s", def)
	}
	all, _ := answer(t, sess, "search", map[string]any{"query": "needle", "limit": 500})
	if n := rowCount(all); n != 60 {
		t.Errorf("limit=500 carried %d of 60 files", n)
	}
	if strings.Contains(all, "truncated:") {
		t.Errorf("a complete search says it was cut:\n%s", all)
	}
}
