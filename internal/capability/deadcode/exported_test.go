package deadcode

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability/captest"
	"github.com/sutrakam/graphlume/internal/registry"
)

// TestExportStatusIsNotAssumedOutsideTheTrustedLanguages: the index's exported
// flag follows the first letter of the name. In Java that hid `class Parser`
// as exported and graded `public void run()` likely unused "because it is
// unexported".
func TestExportStatusIsNotAssumedOutsideTheTrustedLanguages(t *testing.T) {
	javaLower := row(1, "run")
	javaLower.Language, javaLower.FilePath = "java", "src/Job.java"

	javaUpper := row(2, "Parser")
	javaUpper.Language, javaUpper.FilePath, javaUpper.Exported = "java", "src/Parser.java", true

	goLower := row(3, "helper")

	goUpper := row(4, "Public")
	goUpper.Exported = true

	rep := find(t, &fakeSource{rows: []registry.UnreferencedSymbol{javaLower, javaUpper, goLower, goUpper}}, Options{})

	by := map[string]Candidate{}
	for _, c := range rep.Candidates {
		by[c.Name] = c
	}
	// Both Java definitions are candidates, neither better than possible, and
	// each says why.
	for _, name := range []string{"run", "Parser"} {
		c, ok := by[name]
		if !ok {
			t.Errorf("%s is missing: a Java definition must not be skipped on a guessed export flag", name)
			continue
		}
		if c.Certainty != CertaintyPossible {
			t.Errorf("%s is graded %q; with its export status unknown it cannot be likely", name, c.Certainty)
		}
		if len(c.Reasons) == 0 || c.Reasons[0] != ReasonExportUnknown {
			t.Errorf("%s reasons = %v, want %s first", name, c.Reasons, ReasonExportUnknown)
		}
	}
	// Go keeps the rule the flag actually follows.
	if c, ok := by["helper"]; !ok || c.Certainty != CertaintyLikely || len(c.Reasons) != 0 {
		t.Errorf("an unexported Go function = %+v, want likely with no reason", c)
	}
	if _, ok := by["Public"]; ok {
		t.Error("an exported Go function was listed without include_exported")
	}

	if rep.ExportUnknown != 2 || len(rep.ExportUnknownLangs) != 1 || rep.ExportUnknownLangs[0] != "java" {
		t.Errorf("ExportUnknown = %d %v, want 2 [java]", rep.ExportUnknown, rep.ExportUnknownLangs)
	}
	if rep.Likely != 1 || rep.Possible != 2 {
		t.Errorf("likely %d possible %d, want 1 and 2", rep.Likely, rep.Possible)
	}
	if ReasonExportUnknown.Text() == "" || ReasonExportUnknown.Label() == "" {
		t.Error("the new reason has no text or label, so it would print as an empty cell")
	}
	listed := false
	for _, r := range Reasons() {
		listed = listed || r == ReasonExportUnknown
	}
	if !listed {
		t.Error("Reasons() does not list the new reason, so the legend would omit it")
	}
}

// TestLanguagesWithoutReferencesAreNotJudged: the parser's tier table calls
// PHP, Kotlin and Scala resolved, so Judgeable passes them. An index of them
// holds no reference, so every definition in them has no caller by
// construction. What the index holds decides, not the table.
func TestLanguagesWithoutReferencesAreNotJudged(t *testing.T) {
	// The run: Kotlin definitions with nothing recording what they use, beside
	// Go files that do have references.
	deps, runID := captest.Build(t,
		[]captest.Sym{captest.S(1, "emit", "okio/Sink.kt"), captest.S(2, "main", "cmd/main.go")},
		nil, captest.Refs{Resolved: 3})

	kotlin := row(10, "flushAll")
	kotlin.Language, kotlin.FilePath = "kotlin", "okio/Buffer.kt"
	src := &fakeSource{rows: []registry.UnreferencedSymbol{kotlin, row(11, "goFn")}}

	rep, err := Find(context.Background(), deps, src, runID, Options{
		// The tier table's answer: both languages bind references.
		Judgeable: func(string) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateNames(rep); len(got) != 1 || got[0] != "goFn" {
		t.Errorf("candidates = %v, want [goFn]: a Kotlin definition is unreferenced because nothing is", got)
	}
	if rep.SkippedUnjudged != 1 || len(rep.UnjudgedLangs) != 1 || rep.UnjudgedLangs[0] != "kotlin" {
		t.Errorf("unjudged = %d %v, want 1 [kotlin]", rep.SkippedUnjudged, rep.UnjudgedLangs)
	}
}
