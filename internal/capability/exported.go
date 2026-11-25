package capability

import (
	"sort"
	"strings"
)

// Whether the index's `exported` flag may be stated as a fact.
//
// The parser sets the flag from the first letter of the name: upper case is
// exported, anything else is not (internal/parse, isExported). That is Go's
// own visibility rule and nobody else's. In a language with an `export`
// keyword or access modifiers it is a guess that is usually wrong in one
// direction: `export function finalizeIssue` and `public T fromJson(...)` both
// start with a lower-case letter and are both recorded as unexported, and a
// report then says "Target is unexported, so its blast radius is bounded by
// this repository" about a library's public API.
//
// Until the index records real visibility, every report asks ExportedKnown
// before it says "exported" or "unexported", and says "unknown" when the
// answer is no.

// exportedFlagTrusted is THE table of languages in which the index's exported
// flag follows the language's own rule. It is keyed by the language name the
// index records for a file; the value says why the flag can be trusted there.
//
// One entry. Add a language only when the index derives its flag from that
// language's visibility syntax. docs/reference/mcp-tools.md restates this
// table, and TestExportedTableIsDocumented fails when the two differ.
var exportedFlagTrusted = map[string]string{
	"go": "an upper-case first letter is the language's own export rule",
}

// ExportedKnown reports whether the exported flag of a symbol defined in a
// file of this language can be stated as fact.
//
// language is the index's name for it. filePath is consulted only when the
// language is empty, which is the case for a Target resolved without its file
// row.
func ExportedKnown(language, filePath string) bool {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" && strings.HasSuffix(strings.ToLower(filePath), ".go") {
		language = "go"
	}
	_, ok := exportedFlagTrusted[language]
	return ok
}

// ExportedFlagTrustedLanguages lists the languages in the table, sorted, for
// documentation and tests that must not restate it by hand.
func ExportedFlagTrustedLanguages() []string {
	out := make([]string, 0, len(exportedFlagTrusted))
	for l := range exportedFlagTrusted {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
