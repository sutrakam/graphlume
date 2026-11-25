package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

type fakeHistory struct {
	commits  []Commit
	gotLimit int
	err      error
}

func (f *fakeHistory) Commits(_ context.Context, limit int) ([]Commit, error) {
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	if len(f.commits) > limit {
		return f.commits[:limit], nil
	}
	return f.commits, nil
}

type fakeIndex []string

func (f fakeIndex) Files(context.Context, int64) ([]model.File, error) {
	out := make([]model.File, 0, len(f))
	for _, p := range f {
		out = append(out, model.File{Path: p})
	}
	return out, nil
}

const dayOne = int64(1_700_000_000)

// by builds a commit on the given day by the given author name. The address is
// derived from the name unless the name carries one after a "|".
func by(day int, author string, paths ...string) Commit {
	name, addr, ok := strings.Cut(author, "|")
	if !ok {
		addr = strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@example.com"
	}
	return Commit{
		SHA: fmt.Sprintf("c%d-%s", day, name), Author: name, AuthorEmail: addr,
		When: dayOne + int64(day)*secondsPerDay, Paths: paths,
	}
}

func repeat(n, fromDay int, author string, paths ...string) []Commit {
	out := make([]Commit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, by(fromDay+i, author, paths...))
	}
	return out
}

func join(parts ...[]Commit) []Commit {
	var out []Commit
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func analyze(t *testing.T, commits []Commit, idx fakeIndex, opts Options) *Report {
	t.Helper()
	rep, err := Analyze(context.Background(), &fakeHistory{commits: commits}, idx, 7, opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func area(t *testing.T, rep *Report, p string) Area {
	t.Helper()
	for _, a := range rep.Areas {
		if a.Path == p {
			return a
		}
	}
	t.Fatalf("no area %q in %v", p, areaPaths(rep))
	return Area{}
}

func areaPaths(rep *Report) []string {
	out := make([]string, 0, len(rep.Areas))
	for _, a := range rep.Areas {
		out = append(out, a.Path)
	}
	return out
}

func flat(rep *Report) string {
	return strings.Join(strings.Fields(strings.Join(append(append([]string{}, rep.Notes...), rep.Caveats...), " ")), " ")
}

// The bus factor is the smallest number of people covering at least half of
// the commits, and a commit counts once however many files it touches.
func TestBusFactorIsTheFewestPeopleCoveringHalf(t *testing.T) {
	idx := fakeIndex{"solo/a.go", "solo/b.go", "pair/a.go", "spread/a.go"}
	commits := join(
		// solo: Robin 8 of 10.
		repeat(8, 0, "Robin Vega", "solo/a.go", "solo/b.go"),
		repeat(2, 20, "Sam Okafor", "solo/a.go"),
		// pair: 4, 3, 2, 1 of 10. The top name has 40%; two cover 70%.
		repeat(4, 30, "Robin Vega", "pair/a.go"),
		repeat(3, 40, "Sam Okafor", "pair/a.go"),
		repeat(2, 50, "Ash Berg", "pair/a.go"),
		repeat(1, 60, "Kit Lund", "pair/a.go"),
		// spread: six people, one commit each. Three cover exactly half.
		[]Commit{
			by(70, "A", "spread/a.go"), by(71, "B", "spread/a.go"), by(72, "C", "spread/a.go"),
			by(73, "D", "spread/a.go"), by(74, "E", "spread/a.go"), by(75, "F", "spread/a.go"),
		},
	)
	rep := analyze(t, commits, idx, Options{})

	solo := area(t, rep, "solo")
	if solo.BusFactor != 1 || solo.Commits != 10 || solo.Contributors != 2 {
		t.Errorf("solo = %+v, want bus factor 1 over 10 commits by 2 names", solo)
	}
	if solo.TopShare != 0.8 || solo.Top[0].Name != "Robin Vega" || solo.Top[0].Commits != 8 {
		t.Errorf("solo top = %+v share %.2f, want Robin Vega with 8 commits, 80%%", solo.Top[0], solo.TopShare)
	}
	if pair := area(t, rep, "pair"); pair.BusFactor != 2 || pair.TopShare != 0.4 {
		t.Errorf("pair = %+v, want bus factor 2 with a 40%% top share", pair)
	}
	if spread := area(t, rep, "spread"); spread.BusFactor != 3 || spread.Contributors != 6 {
		t.Errorf("spread = %+v, want bus factor 3 of 6: three of six cover exactly half", spread)
	}

	if got := strings.Join(areaPaths(rep), ","); got != "solo,pair,spread" {
		t.Errorf("order = %s, want the lowest bus factor first", got)
	}
	if rep.Measured != 3 || rep.SolePerson != 1 {
		t.Errorf("measured %d, bus factor one in %d; want 3 and 1", rep.Measured, rep.SolePerson)
	}
}

// Exactly half is "at least half": one person with 5 of 10 commits is a bus
// factor of one.
func TestExactlyHalfCountsAsCovering(t *testing.T) {
	commits := join(
		repeat(5, 0, "Robin Vega", "svc/a.go"),
		repeat(3, 10, "Sam Okafor", "svc/a.go"),
		repeat(2, 20, "Ash Berg", "svc/a.go"),
	)
	if a := area(t, analyze(t, commits, fakeIndex{"svc/a.go"}, Options{}), "svc"); a.BusFactor != 1 {
		t.Errorf("bus factor = %d, want 1", a.BusFactor)
	}
}

// Among equal bus factors, the larger share held by one name ranks first, then
// the busier area.
func TestOrderIsBusFactorThenShareThenActivity(t *testing.T) {
	idx := fakeIndex{"all/a.go", "most/a.go", "busy/a.go"}
	commits := join(
		repeat(6, 0, "Robin Vega", "all/a.go"), // 100% of 6
		repeat(9, 10, "Robin Vega", "most/a.go"),
		repeat(1, 20, "Sam Okafor", "most/a.go"),  // 90% of 10
		repeat(40, 30, "Robin Vega", "busy/a.go"), // 100% of 40
	)
	rep := analyze(t, commits, idx, Options{})
	if got := strings.Join(areaPaths(rep), ","); got != "busy,all,most" {
		t.Errorf("order = %s, want busy (100%% of 40), all (100%% of 6), most (90%%)", got)
	}
}

// How recent the top contributor's last change was is measured against the
// newest commit read, so the same history gives the same answer on any day.
func TestIdleDaysAreMeasuredAgainstTheNewestCommit(t *testing.T) {
	commits := join(
		repeat(6, 0, "Robin Vega", "old/a.go"), // last on day 5
		repeat(1, 3, "Sam Okafor", "old/a.go"),
		repeat(5, 96, "Sam Okafor", "new/a.go"), // last on day 100, the newest
	)
	rep := analyze(t, commits, fakeIndex{"old/a.go", "new/a.go"}, Options{})

	old := area(t, rep, "old")
	if old.TopIdleDays != 95 || old.TopLastChanged != dayOne+5*secondsPerDay {
		t.Errorf("old = %+v, want its top contributor idle 95 days, last change on day 5", old)
	}
	if fresh := area(t, rep, "new"); fresh.TopIdleDays != 0 {
		t.Errorf("new: idle %d days, want 0", fresh.TopIdleDays)
	}
	if rep.NewestCommit != dayOne+100*secondsPerDay || rep.OldestCommit != dayOne {
		t.Errorf("span = %d..%d", rep.OldestCommit, rep.NewestCommit)
	}
}

// Two commits by one person is not a finding. Such areas are left out and
// counted, and the report says they are unmeasured rather than safe.
func TestAreasWithTooFewCommitsAreExcludedAndCounted(t *testing.T) {
	idx := fakeIndex{"deep/a.go", "thin/a.go", "thinner/a.go"}
	commits := join(
		repeat(5, 0, "Robin Vega", "deep/a.go"),
		repeat(4, 10, "Robin Vega", "thin/a.go"),
		repeat(1, 20, "Sam Okafor", "thinner/a.go"),
	)
	rep := analyze(t, commits, idx, Options{})
	if got := areaPaths(rep); len(got) != 1 || got[0] != "deep" {
		t.Errorf("areas = %v, want only deep", got)
	}
	if rep.Excluded != 2 || rep.MinCommits != DefaultMinCommits {
		t.Errorf("excluded %d at a minimum of %d, want 2 at %d", rep.Excluded, rep.MinCommits, DefaultMinCommits)
	}
	if text := flat(rep); !strings.Contains(text, "2 directory(ies) with fewer than 5 commits") ||
		!strings.Contains(text, "unmeasured, not low risk") {
		t.Errorf("the exclusion is not explained:\n%s", text)
	}

	lower := analyze(t, commits, idx, Options{MinCommits: 1})
	if lower.Measured != 3 || lower.Excluded != 0 {
		t.Errorf("at a minimum of 1: measured %d, excluded %d", lower.Measured, lower.Excluded)
	}
}

func TestNothingMeasurableIsExplainedNotReportedAsSafe(t *testing.T) {
	rep := analyze(t, repeat(2, 0, "Robin Vega", "svc/a.go"), fakeIndex{"svc/a.go"}, Options{})
	if len(rep.Areas) != 0 || rep.Excluded != 1 {
		t.Fatalf("areas %v, excluded %d", areaPaths(rep), rep.Excluded)
	}
	if text := flat(rep); !strings.Contains(text, "none can be measured") ||
		!strings.Contains(text, "not a finding of low risk") {
		t.Errorf("an empty result must say why:\n%s", text)
	}
	// The whole scope is still summarised.
	if rep.Overall == nil || rep.Overall.Commits != 2 {
		t.Errorf("overall = %+v", rep.Overall)
	}
}

// No history is an answer with an explanation, not an error.
func TestNoHistoryIsAnAnswer(t *testing.T) {
	rep := analyze(t, nil, fakeIndex{"svc/a.go"}, Options{})
	if len(rep.Areas) != 0 || rep.CommitsRead != 0 || rep.Overall != nil {
		t.Errorf("rep = %+v", rep)
	}
	if text := flat(rep); !strings.Contains(text, "No commit history could be read") {
		t.Errorf("missing explanation:\n%s", text)
	}
	if len(rep.Caveats) == 0 {
		t.Error("the standing caveats must be present on every report")
	}
}

// Every report says how much history it read and names what distorts it.
func TestEveryReportStatesTheWindowAndWhatDistortsIt(t *testing.T) {
	commits := repeat(6, 0, "Robin Vega", "svc/a.go")
	idx := fakeIndex{"svc/a.go"}

	whole := analyze(t, commits, idx, Options{})
	if whole.WindowFull || whole.CommitsRead != 6 {
		t.Errorf("whole: read %d, full %v", whole.CommitsRead, whole.WindowFull)
	}
	text := flat(whole)
	for _, want := range []string{"All 6 commit(s) reachable from HEAD were read (2023-11-14 to 2023-11-19)",
		"shallow clone", "squash-merge", "Names are never merged", "Email addresses are never reported"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}

	cut := analyze(t, commits, idx, Options{CommitWindow: 5})
	if !cut.WindowFull || cut.CommitsRead != 5 || cut.CommitWindow != 5 {
		t.Errorf("cut: read %d of window %d, full %v", cut.CommitsRead, cut.CommitWindow, cut.WindowFull)
	}
	if text := flat(cut); !strings.Contains(text, "Only the newest 5 commit(s) were read") ||
		!strings.Contains(text, "Any earlier history was not read") {
		t.Errorf("a full window must say history beyond it was not read:\n%s", text)
	}

	shallow := analyze(t, commits, idx, Options{ShallowClone: true})
	if text := flat(shallow); !shallow.ShallowClone || !strings.Contains(text, "This is a shallow clone") ||
		strings.Contains(text, "All 6 commit(s) reachable") {
		t.Errorf("a shallow clone must not be described as the whole history:\n%s", text)
	}
}

func TestTheCommitWindowIsPassedToHistory(t *testing.T) {
	h := &fakeHistory{}
	if _, err := Analyze(context.Background(), h, fakeIndex{}, 1, Options{}); err != nil {
		t.Fatal(err)
	}
	if h.gotLimit != DefaultCommitWindow {
		t.Errorf("history was asked for %d commits, want the default %d", h.gotLimit, DefaultCommitWindow)
	}
	if _, err := Analyze(context.Background(), h, fakeIndex{}, 1, Options{CommitWindow: 40}); err != nil {
		t.Fatal(err)
	}
	if h.gotLimit != 40 {
		t.Errorf("history was asked for %d commits, want 40", h.gotLimit)
	}
}

// People are the author name exactly as spelled. Two spellings are two people,
// even from one address, and the report says so instead of merging them.
func TestNamesAreNeverMergedAndSharedAddressesAreNamed(t *testing.T) {
	commits := join(
		repeat(3, 0, "Robin Vega|robin@example.com", "svc/a.go"),
		repeat(3, 10, "R. Vega|Robin@Example.com", "svc/a.go"),
		repeat(2, 20, "Sam Okafor", "svc/a.go"),
	)
	rep := analyze(t, commits, fakeIndex{"svc/a.go"}, Options{})
	a := area(t, rep, "svc")
	if a.Contributors != 3 || a.BusFactor != 2 {
		t.Errorf("svc = %+v, want 3 names and a bus factor of 2: spellings are not merged", a)
	}
	want := [][]string{{"R. Vega", "Robin Vega"}}
	if !reflect.DeepEqual(rep.SharedAddress, want) || rep.SharedAddressNames != 2 {
		t.Errorf("shared = %v (%d names), want %v", rep.SharedAddress, rep.SharedAddressNames, want)
	}
	if text := flat(rep); !strings.Contains(text, "'R. Vega' and 'Robin Vega'") ||
		!strings.Contains(text, "merging them would be a guess") {
		t.Errorf("the possible double count is not explained:\n%s", text)
	}
}

// No field of a report may carry an address: not the ones in the history, and
// not a name that was itself set to an address.
func TestNoEmailAddressReachesAReport(t *testing.T) {
	commits := join(
		repeat(4, 0, "Robin Vega|robin@example.com", "svc/a.go"),
		repeat(3, 10, "sam@example.org|sam@example.org", "svc/a.go"),
		repeat(2, 20, "|nobody@example.net", "svc/a.go"),
		repeat(1, 30, "Robin V|robin@example.com", "svc/a.go"),
	)
	rep := analyze(t, commits, fakeIndex{"svc/a.go"}, Options{})
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); strings.Contains(s, "@") || strings.Contains(s, "example.") {
		t.Errorf("an address reached the report:\n%s", s)
	}
	a := area(t, rep, "svc")
	names := []string{a.Top[0].Name, a.Top[1].Name, a.Top[2].Name}
	if !reflect.DeepEqual(names, []string{"Robin Vega", "sam", "(no name recorded)"}) {
		t.Errorf("names = %q", names)
	}
	// A name that was cut is not what git recorded, so the report says so.
	if rep.NamesCut != 1 || !strings.Contains(flat(rep), "1 author name(s) were recorded as an email address") {
		t.Errorf("names cut = %d; notes:\n%s", rep.NamesCut, flat(rep))
	}
}

// The figure for everything in scope counts every commit that changed a file
// there, so it does not move with how the areas are grouped.
func TestOverallDoesNotDependOnGrouping(t *testing.T) {
	idx := fakeIndex{"internal/cli/a.go"}
	commits := join(
		repeat(5, 0, "Robin Vega", "internal/cli/a.go"),
		repeat(3, 10, "Sam Okafor", "internal/removed/b.go"), // directory gone; "internal" is not
		repeat(2, 20, "Ash Berg", "docs/guide.md"),           // never indexed
		[]Commit{by(30, "Kit Lund")},                         // a merge that changed nothing
	)
	for _, opts := range []Options{{}, {Depth: 1}, {PerFile: true}} {
		rep := analyze(t, commits, idx, opts)
		if rep.Overall == nil || rep.Overall.Commits != 10 || rep.Overall.Contributors != 3 {
			t.Errorf("%+v: overall = %+v, want 10 commits by 3 names", opts, rep.Overall)
		}
	}
}

// A walk that ends at a shallow clone's boundary says so itself, whether or
// not the caller knew the repository was shallow, and is never described as
// every commit reachable from HEAD.
func TestAWalkThatEndsAtAShallowBoundaryIsNotTheWholeHistory(t *testing.T) {
	commits := repeat(6, 0, "Robin Vega", "svc/a.go")
	// Oldest last is not required: the marker is read wherever it sits.
	commits[0].ShallowBoundary = true

	rep := analyze(t, commits, fakeIndex{"svc/a.go"}, Options{})
	text := flat(rep)
	if !rep.ShallowClone || rep.WindowFull {
		t.Errorf("shallow %v, window full %v; want true and false", rep.ShallowClone, rep.WindowFull)
	}
	for _, want := range []string{"Shallow clone: 6 commit(s) available", "fetch --unshallow"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "All 6 commit(s) reachable") {
		t.Errorf("a shallow clone was described as the whole history:\n%s", text)
	}
}

// A walk cut by its time budget says partial, n of N, and never "all".
func TestAWalkCutByItsBudgetIsPartial(t *testing.T) {
	commits := repeat(6, 0, "Robin Vega", "svc/a.go")
	commits[len(commits)-1].Truncated = true

	rep := analyze(t, commits, fakeIndex{"svc/a.go"}, Options{})
	text := flat(rep)
	if !rep.Partial || !rep.WindowFull {
		t.Errorf("partial %v, window full %v; want both true", rep.Partial, rep.WindowFull)
	}
	if !strings.Contains(text, "Partial: 6 of") || strings.Contains(text, "All 6 commit(s) reachable") {
		t.Errorf("want the partial walk stated and no whole-history claim:\n%s", text)
	}
}

// The reader stops at the boundary and returns what is there, so a failure to
// read history is a failure, in a shallow clone as anywhere else. This package
// must not turn it into an answer, because the reader may fail on a shallow
// clone; that would hide a damaged repository behind advice to
// fetch more history.
func TestAHistoryFailureIsAFailureEvenInAShallowClone(t *testing.T) {
	boom := errors.New("object not found")
	if _, err := Analyze(context.Background(), &fakeHistory{err: boom}, fakeIndex{"a.go"}, 1,
		Options{ShallowClone: true}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the history failure", err)
	}
}

// A directory deleted since the commits that touched it is not a risk to
// report, and the report says how many were dropped.
func TestAreasNoLongerInTheIndexAreLeftOutAndCounted(t *testing.T) {
	commits := join(
		repeat(6, 0, "Robin Vega", "kept/a.go", "removed/a.go"),
		repeat(2, 10, "Sam Okafor", "kept/deleted_file.go"),
	)
	rep := analyze(t, commits, fakeIndex{"kept/a.go"}, Options{})
	if got := areaPaths(rep); len(got) != 1 || got[0] != "kept" {
		t.Errorf("areas = %v, want only kept", got)
	}
	// Work on a file since deleted still counts toward its directory.
	if a := area(t, rep, "kept"); a.Commits != 8 || a.Contributors != 2 {
		t.Errorf("kept = %+v, want 8 commits by 2 names", a)
	}
	if rep.NotIndexed != 1 || !strings.Contains(flat(rep), "Renames are not followed") {
		t.Errorf("not indexed = %d; notes:\n%s", rep.NotIndexed, flat(rep))
	}
}

func TestPathPrefixDepthAndPerFile(t *testing.T) {
	idx := fakeIndex{"internal/cli/a.go", "internal/cli/sub/b.go", "internal/mcp/c.go", "docs/d.md", "top.go"}
	commits := join(
		repeat(5, 0, "Robin Vega", "internal/cli/a.go"),
		repeat(5, 10, "Sam Okafor", "internal/cli/sub/b.go"),
		repeat(5, 20, "Ash Berg", "internal/mcp/c.go"),
		repeat(5, 30, "Ash Berg", "docs/d.md", "top.go"),
	)

	own := analyze(t, commits, idx, Options{})
	if got := strings.Join(areaPaths(own), ","); got != ".,docs,internal/cli,internal/cli/sub,internal/mcp" {
		t.Errorf("by own directory = %s", got)
	}

	rolled := analyze(t, commits, idx, Options{Depth: 1})
	if got := strings.Join(areaPaths(rolled), ","); got != ".,docs,internal" {
		t.Errorf("depth 1 = %s", got)
	}
	if in := area(t, rolled, "internal"); in.Commits != 15 || in.Contributors != 3 || in.BusFactor != 2 {
		t.Errorf("internal = %+v, want 15 commits by 3 names, bus factor 2", in)
	}

	narrowed := analyze(t, commits, idx, Options{PathPrefix: "internal/cli/"})
	if got := strings.Join(areaPaths(narrowed), ","); got != "internal/cli,internal/cli/sub" {
		t.Errorf("prefix = %s", got)
	}
	if narrowed.Overall == nil || narrowed.Overall.Commits != 10 || narrowed.Overall.Path != "internal/cli/" {
		t.Errorf("overall under a prefix = %+v, want the 10 commits under it", narrowed.Overall)
	}
	if narrowed.NotIndexed != 0 {
		t.Errorf("paths outside the prefix were counted as missing from the index: %d", narrowed.NotIndexed)
	}

	files := analyze(t, commits, idx, Options{PerFile: true})
	if files.Unit != UnitFile || len(files.Areas) != 5 {
		t.Errorf("per file: unit %s, areas %v", files.Unit, areaPaths(files))
	}
	if a := area(t, files, "internal/cli/sub/b.go"); a.Top[0].Name != "Sam Okafor" {
		t.Errorf("per file top = %+v", a.Top[0])
	}

	none := analyze(t, commits, idx, Options{PathPrefix: "nowhere/"})
	if len(none.Areas) != 0 || !strings.Contains(flat(none), `under "nowhere/"`) {
		t.Errorf("an unmatched prefix must be explained:\n%s", flat(none))
	}
}

func TestTheLimitBoundsTheListAndSaysWhatItCut(t *testing.T) {
	var idx fakeIndex
	var commits []Commit
	for i := 0; i < 30; i++ {
		p := fmt.Sprintf("pkg%02d/a.go", i)
		idx = append(idx, p)
		commits = append(commits, repeat(5, i*5, "Robin Vega", p)...)
	}
	rep := analyze(t, commits, idx, Options{CommitWindow: 1000})
	if len(rep.Areas) != DefaultLimit || rep.Measured != 30 {
		t.Errorf("listed %d of %d measured, want %d of 30", len(rep.Areas), rep.Measured, DefaultLimit)
	}
	if !strings.Contains(flat(rep), "10 more measured directory(ies) are not listed") {
		t.Errorf("the cut is not stated:\n%s", flat(rep))
	}
	if small := analyze(t, commits, idx, Options{CommitWindow: 1000, Limit: 3}); len(small.Areas) != 3 {
		t.Errorf("limit 3 returned %d", len(small.Areas))
	}
}

// Only the largest few names are listed for an area; the count is the whole.
func TestTopContributorsAreBoundedPerArea(t *testing.T) {
	var commits []Commit
	for i, n := range []string{"A", "B", "C", "D", "E"} {
		commits = append(commits, repeat(5-i, i*10, n, "svc/a.go")...)
	}
	a := area(t, analyze(t, commits, fakeIndex{"svc/a.go"}, Options{}), "svc")
	if len(a.Top) != contributorsPerArea || a.Contributors != 5 {
		t.Errorf("listed %d of %d names", len(a.Top), a.Contributors)
	}
}

// The same history must give the same report, whatever order it arrives in
// and whatever order a map happens to iterate in.
func TestTheReportIsDeterministic(t *testing.T) {
	idx := fakeIndex{"a/x.go", "b/x.go", "c/x.go"}
	commits := join(
		repeat(3, 0, "Robin Vega", "a/x.go", "b/x.go", "c/x.go"),
		repeat(3, 0, "Sam Okafor", "a/x.go", "b/x.go", "c/x.go"),
		repeat(3, 0, "Ash Berg", "a/x.go", "b/x.go", "c/x.go"),
	)
	first, err := json.Marshal(analyze(t, commits, idx, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	reversed := make([]Commit, len(commits))
	for i, c := range commits {
		reversed[len(commits)-1-i] = c
	}
	for i := 0; i < 20; i++ {
		again, err := json.Marshal(analyze(t, reversed, idx, Options{}))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, again)
		}
	}
}

func TestErrorsAreReturned(t *testing.T) {
	ctx := context.Background()
	if _, err := Analyze(ctx, nil, fakeIndex{}, 1, Options{}); err == nil {
		t.Error("a nil history must be rejected")
	}
	if _, err := Analyze(ctx, &fakeHistory{}, nil, 1, Options{}); err == nil {
		t.Error("a nil index must be rejected")
	}
	boom := errors.New("boom")
	if _, err := Analyze(ctx, &fakeHistory{err: boom}, fakeIndex{}, 1, Options{}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap the history error", err)
	}
}

// A busy repository's last few hundred commits can be two days and one name
// everywhere. The window is a period (12 months by default) measured from the
// newest commit, capped by the commit budget, and the report says which bound
// the history read.
func TestTheWindowIsAPeriodCappedByABudget(t *testing.T) {
	idx := fakeIndex{"svc/a.go"}
	// Newest first, as a walk returns them: five recent commits by Robin, five
	// from 200 days earlier by Sam, five from 500 days earlier by Ash.
	commits := join(repeat(5, 700, "Robin Vega", "svc/a.go"), repeat(5, 500, "Sam Okafor", "svc/a.go"),
		repeat(5, 200, "Ash Berg", "svc/a.go"))

	rep := analyze(t, commits, idx, Options{})
	if rep.Months != DefaultMonths || !rep.PeriodCovered || rep.WindowFull || rep.CommitsRead != 10 {
		t.Fatalf("default window: months %d, covered %v, full %v, read %d; want 12, true, false, 10",
			rep.Months, rep.PeriodCovered, rep.WindowFull, rep.CommitsRead)
	}
	if len(rep.Areas) != 1 || rep.Areas[0].Contributors != 2 {
		t.Fatalf("areas = %+v, want one area with the two names inside the window", rep.Areas)
	}
	if text := flat(rep); !strings.Contains(text, "The last 12 month(s) of history were read: 10 commit(s)") {
		t.Errorf("the period read is not stated:\n%s", text)
	}

	// The budget runs out first: the period is not covered and the note says so.
	short := analyze(t, commits, idx, Options{CommitWindow: 4})
	if short.PeriodCovered || !short.WindowFull || short.CommitsRead != 4 {
		t.Errorf("budget: covered %v, full %v, read %d", short.PeriodCovered, short.WindowFull, short.CommitsRead)
	}
	if text := flat(short); !strings.Contains(text, "the commit budget ran out before the 12-month window did") {
		t.Errorf("a budget-capped window is not disclosed:\n%s", text)
	}

	// No time bound: every commit read counts.
	all := analyze(t, commits, idx, Options{Months: -1})
	if all.CommitsRead != 15 || all.Months != 0 {
		t.Errorf("no time bound: read %d, months %d", all.CommitsRead, all.Months)
	}
}
