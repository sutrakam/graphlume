package parse

import (
	"sort"
	"strings"

	"github.com/odvcencio/gotreesitter/grammars"
)

// ExtractsDefinitions reports whether this build can produce a definition for
// a language: its grammar has a definition query, or it is a single-file
// component read through its script block.
//
// A bundled grammar is not the same thing. Most of the grammars the backend
// carries have no definition query, and a file in one of those languages is
// indexed for full-text search only (reason text_only). TierFor reports TierParsed for them as well, because a tier says
// how far resolution goes once definitions exist, not whether extraction
// starts. Anything that describes language support to a person has to ask
// this question too; a count of grammars answers a different one.
func ExtractsDefinitions(language string) bool {
	if isSFC(language) {
		return true
	}
	entry := grammars.DetectLanguageByName(language)
	if entry == nil {
		return false
	}
	return strings.Contains(definitionQueryText(*entry), definitionCapturePrefix)
}

// definitionCapturePrefix opens every capture that records a definition. A
// query without one cannot produce a symbol whatever else it matches: the
// query the Angular template grammar ships has a single call pattern, and
// counting it made the language one of those "with definitions" while its
// fixture records that it declares nothing.
const definitionCapturePrefix = "@definition."

// TextOnlyLanguages returns the languages whose grammar is bundled but which
// have no definition query, sorted. Their files are searchable by text and
// yield no symbols.
func TextOnlyLanguages() []string {
	var out []string
	for _, l := range PureGoBackend().Languages() {
		if !ExtractsDefinitions(l) {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}
