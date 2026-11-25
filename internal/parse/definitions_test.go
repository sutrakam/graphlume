package parse

import (
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/sutrakam/graphlume/pkg/model"
)

// ExtractsDefinitions must agree with what building the parser actually does:
// a language it calls extractable has a parser, and one it does not has none.
// If these drift, `graphlume languages` describes a different binary.
func TestExtractsDefinitionsMatchesTheParser(t *testing.T) {
	for _, e := range grammars.AllLanguages() {
		if isSFC(e.Name) {
			if !ExtractsDefinitions(e.Name) {
				t.Errorf("%s is read through its script block and should count as extractable", e.Name)
			}
			continue
		}
		built := newGenericParser(e, defaultParserConfig).initErr == nil
		if got := ExtractsDefinitions(e.Name); got != built {
			t.Errorf("%s: ExtractsDefinitions = %v, but a parser can be built = %v", e.Name, got, built)
		}
	}
}

func TestTextOnlyLanguagesAreNotExtractable(t *testing.T) {
	only := TextOnlyLanguages()
	if len(only) == 0 {
		t.Skip("every bundled grammar has a definition query")
	}
	for _, l := range only {
		if ExtractsDefinitions(l) {
			t.Errorf("%s is listed as text-only and as extractable", l)
		}
	}
	// A language people index every day must never be in this list unnoticed.
	for _, must := range []string{"go", "java", "python", "typescript", "javascript", "c", "cpp", "rust"} {
		if !ExtractsDefinitions(must) {
			t.Errorf("%s has no definition query", must)
		}
	}
}

// A grammar whose query has no definition capture is text-only, however many
// other patterns it has. The Angular template grammar ships one call pattern;
// it was counted among the languages "with definitions" while its fixture
// directory recorded that it declares nothing.
func TestAQueryThatRecordsNoDefinitionIsTextOnly(t *testing.T) {
	if ExtractsDefinitions("angular") {
		t.Error("angular is reported as extracting definitions; its query has no @definition capture")
	}
	found := false
	for _, l := range TextOnlyLanguages() {
		if l == "angular" {
			found = true
		}
	}
	if !found {
		t.Error("angular is missing from the text-only languages")
	}

	p := newGenericParser(*grammars.DetectLanguageByName("angular"), defaultParserConfig)
	res, err := p.Parse(t.Context(), "list.component.html", []byte("<button (click)=\"save()\">Save</button>\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != model.ParseReasonTextOnly {
		t.Errorf("reason = %q, want %q", res.Reason, model.ParseReasonTextOnly)
	}
	if len(res.Symbols) != 0 {
		t.Errorf("symbols = %d, want none", len(res.Symbols))
	}
}
