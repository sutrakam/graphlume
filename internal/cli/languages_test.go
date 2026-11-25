package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/parse"
)

// The report must not count a grammar that extracts nothing as a language
// with definitions: that is how "206 languages" came to be published.
func TestLanguagesSeparatesGrammarsThatExtractNothing(t *testing.T) {
	out, err := run(t, t.TempDir(), "--json", "languages")
	if err != nil {
		t.Fatalf("languages: %v\n%s", err, out)
	}
	var rep languageReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if rep.WithDefinitions != len(rep.Typed)+len(rep.Resolved)+len(rep.Parsed) {
		t.Errorf("with_definitions = %d, but the three tiers hold %d", rep.WithDefinitions, len(rep.Typed)+len(rep.Resolved)+len(rep.Parsed))
	}
	if rep.Total != rep.WithDefinitions+len(rep.TextOnly) {
		t.Errorf("total = %d, want %d with definitions plus %d text-only", rep.Total, rep.WithDefinitions, len(rep.TextOnly))
	}
	for _, group := range [][]string{rep.Typed, rep.Resolved, rep.Parsed} {
		for _, l := range group {
			if !parse.ExtractsDefinitions(l) {
				t.Errorf("%s is listed in a tier but nothing is extracted for it", l)
			}
		}
	}
	for _, l := range rep.TextOnly {
		if parse.ExtractsDefinitions(l) {
			t.Errorf("%s is listed as text-only but definitions are extracted for it", l)
		}
	}
}

func TestLanguagesTextNamesTheTextOnlyGroup(t *testing.T) {
	out, err := run(t, t.TempDir(), "languages")
	if err != nil {
		t.Fatalf("languages: %v\n%s", err, out)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"with definitions", "text only", "full-text search only"} {
		if !strings.Contains(flat, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
