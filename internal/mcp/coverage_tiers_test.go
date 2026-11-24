package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
)

// A language a compiler read in one file of two hundred must not be reported
// as compiler-checked: the tier follows the share, and only a language a
// compiler read in full is TierCompiler.
func TestLanguageCoverageTierFollowsTheShare(t *testing.T) {
	langs := map[string]registry.LanguageBinding{
		"typescript": {Language: "typescript", Files: 120, Definitions: 900},
		"javascript": {Language: "javascript", Files: 200, Definitions: 300},
		"go":         {Language: "go", Files: 40, Definitions: 500},
		"python":     {Language: "python", Files: 30, Definitions: 100},
		"yaml":       {Language: "yaml", Files: 9}, // no definitions: no row
	}
	checked := map[string]registry.CheckedLanguage{
		"typescript": {Language: "typescript", Files: 100, Checked: 93, Tool: "scip-typescript"},
		"javascript": {Language: "javascript", Files: 200, Checked: 1, Tool: "scip-typescript"},
		"go":         {Language: "go", Files: 40, Checked: 40, Tool: "go/types"},
		"python":     {Language: "python", Files: 30},
	}
	rows := languageCoverage(langs, checked, true)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (a language with no definitions has no row): %+v", len(rows), rows)
	}
	by := map[string]LanguageCoverage{}
	for _, r := range rows {
		by[r.Language] = r
	}
	if r := by["javascript"]; r.Tier != TierPartial || r.CompilerFiles != 1 || r.CompilerShare >= 0.01 {
		t.Errorf("javascript = %+v, want partial with 1 of 200 files", r)
	}
	if r := by["typescript"]; r.Tier != TierPartial || r.Files != 100 || r.CompilerShare != 0.93 {
		t.Errorf("typescript = %+v, want partial, 93 of the 100 files holding definitions", r)
	}
	if r := by["go"]; r.Tier != TierCompiler || r.CompilerShare != 1 {
		t.Errorf("go = %+v, want compiler: every file read", r)
	}
	if r := by["python"]; r.Tier != TierParsed || r.CompilerFiles != 0 {
		t.Errorf("python = %+v, want parsed", r)
	}
	if rows[0].Language != "typescript" {
		t.Errorf("first row = %s, want the language with the most definitions", rows[0].Language)
	}
}

// When the compiler figures could not be read nothing is called parsed: the
// tier is unknown.
func TestLanguageCoverageUnknownWhenCompilerFiguresFail(t *testing.T) {
	langs := map[string]registry.LanguageBinding{"go": {Language: "go", Files: 4, Definitions: 10}}
	for _, r := range languageCoverage(langs, nil, false) {
		if r.Tier != TierUnknown {
			t.Errorf("%s tier = %q, want %q", r.Language, r.Tier, TierUnknown)
		}
	}
}

// The why of a row above the floor still says a compiler read only part of it.
func TestLanguageRowsSayPartialCompilerCoverageAboveTheFloor(t *testing.T) {
	langs := map[string]registry.LanguageBinding{"python": {Language: "python", Files: 10, Definitions: 50}}
	langs["python"] = withStats(langs["python"], 90, 10)
	checked := map[string]registry.CheckedLanguage{"python": {Language: "python", Files: 10, Checked: 7, Tool: "scip-python"}}
	items := languageRows(langs, checked)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if want := "compiler-checked in 7 of 10 file(s)"; !strings.Contains(items[0].Why, want) {
		t.Errorf("why = %q, want it to carry %q", items[0].Why, want)
	}
}

func withStats(l registry.LanguageBinding, resolved, ambiguous int64) registry.LanguageBinding {
	l.Resolved, l.Ambiguous, l.Total = resolved, ambiguous, resolved+ambiguous
	return l
}
