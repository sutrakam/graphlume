package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// collapse removes the wrapping a terminal width imposes, so an assertion on a
// sentence does not depend on where a temporary directory's name broke a line.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// whoFixture has four commits, all under svc/: Sam one, Robin two, Ash one.
func TestBusFactorMeasuresFromGitHistory(t *testing.T) {
	root := whoFixture(t)
	out, err := run(t, root, "bus-factor", "--min-commits", "2")
	if err != nil {
		t.Fatalf("bus-factor: %v\n%s", err, out)
	}

	row := ""
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[1] == "svc" {
			row = collapse(l)
			break
		}
	}
	// Robin made 2 of the 4 commits: exactly half, so one person covers it.
	// Their last change was the third commit, one day before the newest.
	if row != "1 svc 4 3 Robin Vega 50% 2023-11-17 1d" {
		t.Errorf("svc row = %q, want bus factor 1, 4 commits, 3 people, Robin Vega at 50%%:\n%s", row, out)
	}

	flat := collapse(out)
	for _, want := range []string{
		"from 4 commit(s) in the last 12 month(s) (2023-11-15 to 2023-11-18) by 3 author name(s)",
		"1 directory(ies) measured, 1 with a bus factor of 1",
		"bus factor 1 across 4 commit(s) by 3 name(s)",
		"All 4 commit(s) reachable from HEAD were read",
		"WHAT THIS DOES NOT SAY",
		"not who understands it",
		"Names are never merged",
		"shallow clone", "squash-merge",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// The terminal prints names to the person who owns the history, and still
// never an address: this report is pasted into tickets and chat.
func TestBusFactorNeverPrintsAnAddress(t *testing.T) {
	root := whoFixture(t) // every author has an example.com address
	for _, args := range [][]string{
		{"bus-factor", "--min-commits", "1"},
		{"bus-factor", "--min-commits", "1", "--files"},
		{"--json", "bus-factor", "--min-commits", "1"},
		{"--json", "tool", "call", "busfactor", "min_commits=1"},
	} {
		out, err := run(t, root, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if !strings.Contains(out, "Robin Vega") {
			t.Errorf("%v: the people in the history should be named:\n%s", args, out)
		}
		if strings.Contains(out, "example.com") || strings.Contains(out, "@") {
			t.Errorf("%v: an email address reached the output:\n%s", args, out)
		}
	}
}

// Four commits is under the default minimum. The answer must say the directory
// was not measured, and must not read as "nothing is at risk".
func TestBusFactorSaysWhenThereIsTooLittleHistoryToMeasure(t *testing.T) {
	root := whoFixture(t)
	out, err := run(t, root, "bus-factor")
	if err != nil {
		t.Fatalf("bus-factor: %v\n%s", err, out)
	}
	flat := collapse(out)
	for _, want := range []string{"Nothing to measure", "has 5 or more commits", "not a finding of low risk"} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestBusFactorWithoutARepositoryExplainsItself(t *testing.T) {
	root := queryFixture(t) // indexed, but not a git repository
	out, err := run(t, root, "bus-factor")
	if err != nil {
		t.Fatalf("no history is an answer, not an error: %v\n%s", err, out)
	}
	flat := collapse(out)
	if !strings.Contains(flat, "Nothing to measure") || !strings.Contains(flat, "not a git repository") ||
		!strings.Contains(flat, "No commit history could be read") {
		t.Errorf("the output should say why nothing was measured:\n%s", out)
	}

	tool, err := run(t, root, "tool", "call", "busfactor")
	if err != nil || !strings.Contains(tool, "No commit history") {
		t.Errorf("no history should be guidance from the tool, not a failure: %v\n%s", err, tool)
	}
}

func TestBusFactorJSONIsTheReport(t *testing.T) {
	root := whoFixture(t)
	raw, err := run(t, root, "--json", "bus-factor", "--min-commits", "2", "--files")
	if err != nil {
		t.Fatalf("bus-factor --json: %v\n%s", err, raw)
	}
	var rep struct {
		Unit  string `json:"unit"`
		Areas []struct {
			Path      string `json:"path"`
			BusFactor int    `json:"bus_factor"`
			Commits   int    `json:"commits"`
			Top       []struct {
				Name  string  `json:"name"`
				Share float64 `json:"share"`
			} `json:"top_contributors"`
		} `json:"areas"`
		Excluded    int      `json:"areas_excluded_too_few_commits"`
		CommitsRead int      `json:"commits_read"`
		WindowFull  bool     `json:"window_full"`
		Shallow     bool     `json:"shallow_clone"`
		Caveats     []string `json:"caveats"`
	}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	if rep.Unit != "file" || len(rep.Areas) != 1 || rep.Areas[0].Path != "svc/payment.go" {
		t.Fatalf("areas = %+v, want only svc/payment.go, the one file with two commits", rep.Areas)
	}
	a := rep.Areas[0]
	if a.BusFactor != 1 || a.Commits != 2 || len(a.Top) != 1 || a.Top[0].Name != "Robin Vega" || a.Top[0].Share != 1 {
		t.Errorf("svc/payment.go = %+v", a)
	}
	if rep.Excluded != 2 || rep.CommitsRead != 4 || rep.WindowFull || rep.Shallow {
		t.Errorf("excluded %d, read %d, window full %v, shallow %v", rep.Excluded, rep.CommitsRead, rep.WindowFull, rep.Shallow)
	}
	if len(rep.Caveats) == 0 {
		t.Error("the JSON report must carry its caveats")
	}
}

func TestBusFactorRejectsNegativeBounds(t *testing.T) {
	root := whoFixture(t)
	if out, err := run(t, root, "bus-factor", "--limit", "-1"); err == nil {
		t.Errorf("a negative limit must be rejected:\n%s", out)
	}
}
