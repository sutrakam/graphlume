package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLessonsRoundTripAndAggregate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".graphlume", lessonsFile)
	for _, l := range []lesson{
		{Task: "one", Missed: []string{"RetryPolicy", "RetryPolicy", "ledger"}, Used: []string{"Service"}, Note: "ranking ignored the retry config"},
		{Task: "two", Missed: []string{"RetryPolicy"}, Used: []string{"Service", "Repo"}},
		{Task: "three", Used: []string{"Repo"}},
	} {
		if err := appendLesson(path, l); err != nil {
			t.Fatal(err)
		}
	}
	// A hand-damaged line must not hide the others.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ls, err := readLessons(path)
	if err != nil || len(ls) != 3 {
		t.Fatalf("read %d lessons, %v", len(ls), err)
	}
	s := summarizeLessons(ls, 5)
	if s.Lessons != 3 || s.WithMisses != 2 {
		t.Errorf("counts: %+v", s)
	}
	// RetryPolicy is in two lessons, though written twice in one.
	if len(s.TopMissed) == 0 || s.TopMissed[0].Name != "RetryPolicy" || s.TopMissed[0].Count != 2 {
		t.Errorf("top missed: %+v", s.TopMissed)
	}
	if len(s.Notes) != 1 {
		t.Errorf("notes: %v", s.Notes)
	}

	var b bytes.Buffer
	writeLessonSummary(&b, s)
	if !strings.Contains(b.String(), "Missed most often") {
		t.Errorf("report:\n%s", b.String())
	}
}

func TestLessonsEmptyAndRequiredTask(t *testing.T) {
	dir := t.TempDir()
	if ls, err := readLessons(filepath.Join(dir, "none.jsonl")); err != nil || ls != nil {
		t.Errorf("a missing file is no lessons, not an error: %v %v", ls, err)
	}
	if err := appendLesson(filepath.Join(dir, "x.jsonl"), lesson{Task: "  "}); err == nil {
		t.Error("a lesson with no task was accepted")
	}
	var b bytes.Buffer
	writeLessonSummary(&b, summarizeLessons(nil, 5))
	if !strings.Contains(b.String(), "No lessons recorded yet") {
		t.Errorf("empty report:\n%s", b.String())
	}
}
