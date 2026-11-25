package multirepo

import (
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/sutrakam/graphlume/internal/analyze/api"
)

// everySpelling is one source text carrying a call and a route in each
// spelling the extractors know. Run under a file name, it shows what the
// extractors read for that EXTENSION — which is how they decide coverage, and
// is the one thing the table in contract_coverage.go has to agree with.
const everySpelling = `
fetch("/api/a");
resp, err := http.Get("/api/a")
app.get("/api/a", handler);
mux.HandleFunc("GET /api/a", handler)

@GetMapping("/api/a")
public String a() { return ""; }

@app.get("/api/a")
def a():
    pass
`

// TestCoverageTableMatchesTheExtractors pins contract_coverage.go against the
// extractors it describes.
//
// The table is a second statement of a fact internal/analyze/api owns. Without
// this test it would rot silently in whichever direction is worse: claiming a
// language is read after its extraction was removed, or — when somebody adds
// Java client extraction — still telling every Java member that its calls are
// invisible. Each row also asserts the index's own name for the extension, so a
// renamed grammar cannot make the table match nothing.
func TestCoverageTableMatchesTheExtractors(t *testing.T) {
	rows := []struct {
		file, language string
	}{
		{"a.go", "go"}, {"a.ts", "typescript"}, {"a.tsx", "tsx"},
		{"a.js", "javascript"}, {"a.jsx", "javascript"}, {"a.mjs", "javascript"},
		{"a.vue", "vue"}, {"a.svelte", "svelte"}, {"a.astro", "astro"},
		{"A.java", "java"}, {"a.py", "python"},
		{"a.kt", "kotlin"}, {"a.scala", "scala"}, {"a.groovy", "groovy"},
		{"a.cs", "c_sharp"}, {"a.rb", "ruby"}, {"a.php", "php"},
		{"a.rs", "rust"}, {"a.swift", "swift"}, {"a.dart", "dart"},
		{"a.ex", "elixir"}, {"a.erl", "erlang"}, {"a.clj", "clojure"},
		{"a.hs", "haskell"}, {"a.ml", "ocaml"}, {"a.cr", "crystal"},
	}
	for _, row := range rows {
		t.Run(row.file, func(t *testing.T) {
			entry := grammars.DetectLanguage(row.file)
			if entry == nil {
				t.Fatalf("the indexer recognises no language for %s, so the name %q in the "+
					"coverage table can never match a member's file counts", row.file, row.language)
			}
			if entry.Name != row.language {
				t.Fatalf("the indexer calls %s %q; the coverage table spells it %q and would "+
					"never match", row.file, entry.Name, row.language)
			}
			if !serviceLanguage(row.language) {
				t.Errorf("%q is not a service language, so a member written in it gets no "+
					"coverage statement at all", row.language)
			}

			callsRead := len(api.ExtractCalls(row.file, []byte(everySpelling))) > 0
			if got := clientCallLanguage(row.language); got != callsRead {
				t.Errorf("clientCallLanguage(%q) = %v, but the extractor read calls from %s: %v",
					row.language, got, row.file, callsRead)
			}
			routesRead := len(api.Extract(row.file, []byte(everySpelling))) > 0
			if got := routeLanguage(row.language); got != routesRead {
				t.Errorf("routeLanguage(%q) = %v, but the extractor read routes from %s: %v",
					row.language, got, row.file, routesRead)
			}
		})
	}
}

// TestDataLanguagesGetNoCoverageWarning: a member is mostly YAML, JSON and
// Markdown by file count, and a warning that calls are not extracted from any
// of them would bury the one about Java.
func TestDataLanguagesGetNoCoverageWarning(t *testing.T) {
	read, notRead := splitLanguages(map[string]int{
		"yaml": 40, "json": 12, "markdown": 9, "gomod": 1, "sql": 3, "dockerfile": 1,
		"go": 7, "java": 5,
	}, clientCallLanguage)

	if len(read) != 1 || read[0] != (LanguageFiles{Language: "go", Files: 7}) {
		t.Errorf("read = %+v, want only go", read)
	}
	if len(notRead) != 1 || notRead[0] != (LanguageFiles{Language: "java", Files: 5}) {
		t.Errorf("notRead = %+v, want only java", notRead)
	}
}
