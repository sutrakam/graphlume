package multirepo

import "sort"

// Which languages the two extractions behind a contract report read.
//
// WHY THIS IS A TABLE HERE AND NOT A CALL INTO internal/analyze/api. That
// package decides coverage by file extension inside an unexported function and
// has no notion of the index's language names, which are what a member's index
// actually records. So the mapping has to be written down somewhere, and it is
// written here, next to its one consumer. contract_coverage_test.go runs the
// real extractors over one file per language and fails if this table and their
// behaviour ever disagree, in either direction — which is the property a second
// copy needs in order not to be a liability.
//
// The names are the grammar names the indexer stores in files.language.

// clientCallLanguage reports whether outbound HTTP calls are extracted from
// source in this language (fetch and axios in the JavaScript family, net/http
// in Go).
func clientCallLanguage(lang string) bool {
	switch lang {
	case "go", "javascript", "typescript", "tsx", "vue", "svelte", "astro":
		return true
	}
	return false
}

// routeLanguage reports whether route declarations are extracted from source
// in this language. A superset of clientCallLanguage: Spring and the three
// Python frameworks declare routes this tool reads, and make calls it does not.
func routeLanguage(lang string) bool {
	switch lang {
	case "java", "python":
		return true
	}
	return clientCallLanguage(lang)
}

// serviceLanguage reports whether a language is one services and their clients
// are commonly written in, so that its ABSENCE from the two functions above is
// worth a sentence.
//
// A closed list, and the limit is real: the indexer recognises some two hundred
// grammars, most of them data and markup, and warning that "outbound calls are
// not extracted from YAML" would bury the warnings that matter. The price is
// that a member written in a language not named here gets no coverage warning,
// which the report's caveats state rather than hide. C and C++ are left out on
// purpose: in the repositories this tool meets they are far more often a cgo
// shim or a vendored library than an HTTP service, and a warning that fires on
// every one of those would cost every finding a tier for nothing. F# is left
// out because the indexer files .fs under another grammar's name, so no row
// here could ever match it.
func serviceLanguage(lang string) bool {
	switch lang {
	case "java", "python", "kotlin", "scala", "groovy", "c_sharp",
		"ruby", "php", "rust", "swift", "dart", "elixir", "erlang", "clojure",
		"haskell", "ocaml", "crystal":
		return true
	}
	return clientCallLanguage(lang)
}

// splitLanguages divides the service languages present in a member into those
// an extraction reads and those it does not, largest first.
func splitLanguages(counts map[string]int, covered func(string) bool) (read, notRead []LanguageFiles) {
	read, notRead = []LanguageFiles{}, []LanguageFiles{}
	for lang, n := range counts {
		if n <= 0 || !serviceLanguage(lang) {
			continue
		}
		lf := LanguageFiles{Language: lang, Files: n}
		if covered(lang) {
			read = append(read, lf)
		} else {
			notRead = append(notRead, lf)
		}
	}
	byFiles := func(ls []LanguageFiles) {
		sort.Slice(ls, func(i, j int) bool {
			if ls[i].Files != ls[j].Files {
				return ls[i].Files > ls[j].Files
			}
			return ls[i].Language < ls[j].Language
		})
	}
	byFiles(read)
	byFiles(notRead)
	return read, notRead
}
