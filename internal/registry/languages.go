package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Which files can hold a use of a definition.
//
// The text checks (a function passed as a value, a type declared in a way the
// extractor missed) read stored file text, and file text is not code. A
// Markdown page that says `CompErrorln`, a workflow file with the word `run`,
// and a TOML key are not uses of the Go function or the shell script they
// happen to share a spelling with. Counting them is how a correct answer came
// to open with INCOMPLETE on cobra, rack, serilog and click (see
// docs/reference/mcp-tools.md, "What an answer will not state as fact").
//
// The rule is the resolver's: a use in language X may be a use of a definition
// in X or in a language of the same family, and of nothing else. The table is
// the resolver's `languageFamilies` (internal/resolve/infer/calltraits.go),
// MIRRORED here because internal/registry sits below internal/resolve and may
// not import it (tests/architecture). Keep the two in step; a language in no
// family reaches only itself, and a data, configuration or prose format is in
// no family, which is exactly what keeps it out.
var languageFamilies = [][]string{
	// One module system: ECMAScript modules, whatever the file's envelope.
	{"javascript", "typescript", "tsx", "jsx", "vue", "svelte", "astro"},
	// One linker. A header detected as C is included from C++ and the reverse.
	{"c", "cpp", "objc", "cuda", "arduino"},
	// One class path.
	{"java", "kotlin", "scala", "groovy"},
	// One runtime and one assembly model.
	{"c_sharp", "fsharp", "vb"},
	// Swift and Objective-C share a module map on Apple platforms.
	{"swift", "objc"},
	// Hack is PHP's descendant and still loads PHP.
	{"php", "hack"},
	// Elixir, Erlang and Gleam share the BEAM and call each other directly.
	{"elixir", "erlang", "gleam"},
}

// sourceLanguages are the languages the index reads definitions from. It is
// the fallback for a name that has no definition: its mentions are looked for
// in source and never in prose or data. A language missing here is treated as
// not source, which loses a mention and never invents one.
var sourceLanguages = []string{
	"go", "python", "ruby", "rust", "lua", "dart", "perl", "r", "julia", "haskell", "ocaml",
	"zig", "nim", "clojure", "commonlisp", "bash", "powershell",
	"javascript", "typescript", "tsx", "jsx", "vue", "svelte", "astro",
	"c", "cpp", "objc", "cuda", "arduino",
	"java", "kotlin", "scala", "groovy",
	"c_sharp", "fsharp", "vb", "swift", "php", "hack", "elixir", "erlang", "gleam",
}

// ReferencingLanguages returns the languages whose files can reference a
// definition written in any of defLanguages: the languages themselves and
// every member of their families, sorted. With none given it is every source
// language, so a name with no definition is still searched in code only.
func ReferencingLanguages(defLanguages []string) []string {
	set := map[string]bool{}
	for _, l := range defLanguages {
		l = strings.ToLower(strings.TrimSpace(l))
		// A Markdown heading and a YAML key are symbols too; they define a name
		// in prose and data, and a definition there references nothing.
		if l == "" || !containsLang(sourceLanguages, l) {
			continue
		}
		set[l] = true
		for _, fam := range languageFamilies {
			if !containsLang(fam, l) {
				continue
			}
			for _, m := range fam {
				set[m] = true
			}
		}
	}
	if len(set) == 0 {
		for _, l := range sourceLanguages {
			set[l] = true
		}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func containsLang(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// definitionLanguages is the languages of the files that define name, in run.
func (r *Registry) definitionLanguages(ctx context.Context, runID int64, name string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT f.language
		  FROM symbols s INDEXED BY idx_symbols_name
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.name = ? AND f.language <> ''`, runID, name)
	if err != nil {
		return nil, fmt.Errorf("registry: languages defining %q: %w", name, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, fmt.Errorf("registry: languages defining %q: scan: %w", name, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
