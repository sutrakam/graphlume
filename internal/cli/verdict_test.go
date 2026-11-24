package cli

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
)

// The verdict is the line a reader who skips the table acts on, so it must
// not read as proof where nothing was proven, and must name the sizeable
// languages no compiler checked.
func TestQualityVerdict(t *testing.T) {
	if got := qualityVerdict(statusReport{}); got != "" {
		t.Errorf("an empty index got a verdict: %q", got)
	}

	none := qualityVerdict(statusReport{Edges: 100, Compiler: []registry.CheckedLanguage{
		{Language: "cpp", Files: 1049}, {Language: "c", Files: 334}, {Language: "perl", Files: 10}, {Language: "scss", Files: 81},
	}})
	for _, want := range []string{"no relationship is compiler-proven", "C++ (1049 files), C (334 files)", "graphlume indexers list"} {
		if !strings.Contains(none, want) {
			t.Errorf("verdict missing %q:\n%s", want, none)
		}
	}
	if strings.Contains(none, "Perl") {
		t.Errorf("a language under the file floor was named:\n%s", none)
	}
	if strings.Contains(none, "Scss") {
		t.Errorf("a language no compiler indexer exists for was named:\n%s", none)
	}

	proven := qualityVerdict(statusReport{Edges: 1000,
		GoTypes:  &goTypesStatus{GoFiles: 50, FilesWithProof: 50, ExactEdges: 300},
		Compiler: []registry.CheckedLanguage{{Language: "python", Files: 80, Checked: 80, ExactEdges: 579}},
	})
	if !strings.Contains(proven, "88% of relationships are compiler-proven") || strings.Contains(proven, "Raise it") {
		t.Errorf("fully checked verdict = %q", proven)
	}
}
