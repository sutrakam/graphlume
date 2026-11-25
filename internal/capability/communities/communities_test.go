package communities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// fakeSource is an index reduced to what this package reads: files, links and
// the resolution outcome.
type fakeSource struct {
	files []model.File
	ids   map[string]int64
	links []registry.FileLink
	stats registry.ResolutionStats

	filesErr error
	linksErr error
	// unsorted hands the links over in the order they were added, to prove
	// the answer does not depend on it.
	unsorted bool
}

func newFake(paths ...string) *fakeSource {
	f := &fakeSource{ids: map[string]int64{}}
	for _, p := range paths {
		f.file(p)
	}
	return f
}

func (f *fakeSource) file(p string) int64 {
	if id, ok := f.ids[p]; ok {
		return id
	}
	id := int64(len(f.files) + 1)
	f.ids[p] = id
	f.files = append(f.files, model.File{ID: id, Path: p, Language: "go"})
	return id
}

// link records n relationships of one confidence from src to dst, adding the
// files if they are new.
func (f *fakeSource) link(src, dst string, n int, conf model.Confidence) *fakeSource {
	f.links = append(f.links, registry.FileLink{
		SrcFileID: f.file(src), DstFileID: f.file(dst), Confidence: conf, Relationships: n,
	})
	return f
}

func (f *fakeSource) Files(context.Context, int64) ([]model.File, error) {
	if f.filesErr != nil {
		return nil, f.filesErr
	}
	out := append([]model.File(nil), f.files...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *fakeSource) EachFileLink(_ context.Context, _ int64, fn func(registry.FileLink) error) error {
	if f.linksErr != nil {
		return f.linksErr
	}
	links := append([]registry.FileLink(nil), f.links...)
	if !f.unsorted {
		sort.Slice(links, func(i, j int) bool {
			a, b := links[i], links[j]
			if a.SrcFileID != b.SrcFileID {
				return a.SrcFileID < b.SrcFileID
			}
			if a.DstFileID != b.DstFileID {
				return a.DstFileID < b.DstFileID
			}
			return a.Confidence < b.Confidence
		})
	}
	for _, l := range links {
		if err := fn(l); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSource) ResolutionStats(context.Context, int64) (registry.ResolutionStats, error) {
	return f.stats, nil
}

const high = model.ConfidenceHigh

// twoGroups is two triangles of files, each tightly linked, joined by one
// relationship. Two isolated files sit beside them.
func twoGroups() *fakeSource {
	f := newFake("docs/readme.md", "notes.txt")
	f.link("a/x1.go", "a/x2.go", 5, high).link("a/x2.go", "a/x3.go", 5, high).link("a/x3.go", "a/x1.go", 5, high)
	f.link("b/y1.go", "b/y2.go", 5, high).link("b/y2.go", "b/y3.go", 5, high).link("b/y3.go", "b/y1.go", 5, high)
	f.link("a/x3.go", "b/y1.go", 1, high)
	f.stats = registry.ResolutionStats{Total: 100, Resolved: 60, Ambiguous: 7, Unresolved: 30, Pending: 3}
	return f
}

func detect(t *testing.T, src Source, opts Options) *Report {
	t.Helper()
	rep, err := Detect(context.Background(), src, 1, opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func hasNote(rep *Report, substr string) bool {
	for _, n := range rep.Notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func TestTightlyLinkedFilesFormACommunity(t *testing.T) {
	rep := detect(t, twoGroups(), Options{})

	if rep.Total != 2 || len(rep.Communities) != 2 {
		t.Fatalf("found %d communities (%d returned), want 2: %+v", rep.Total, len(rep.Communities), rep.Communities)
	}
	a, b := rep.Communities[0], rep.Communities[1]
	if a.ID != "a/x1.go" || b.ID != "b/y1.go" {
		t.Errorf("ids = %q, %q; want each community's smallest member path", a.ID, b.ID)
	}
	if a.Files != 3 || b.Files != 3 {
		t.Errorf("sizes = %d, %d; want 3 and 3", a.Files, b.Files)
	}
	// Fifteen relationships run inside each triangle; the one between them is
	// charged to both, because it crosses both boundaries.
	if a.Internal != 15 || a.External != 1 || b.Internal != 15 || b.External != 1 {
		t.Errorf("inside/crossing = %d/%d and %d/%d, want 15/1 and 15/1", a.Internal, a.External, b.Internal, b.External)
	}
	if rep.Files != 8 || rep.Grouped != 6 || rep.Unlinked != 2 || rep.Alone != 0 {
		t.Errorf("files %d, grouped %d, unlinked %d, alone %d; want 8, 6, 2, 0", rep.Files, rep.Grouped, rep.Unlinked, rep.Alone)
	}
	if rep.Links != 7 || rep.Relationships != 31 {
		t.Errorf("links %d, relationships %d; want 7 and 31", rep.Links, rep.Relationships)
	}
}

// PageRank, not raw in-degree, decides the central files: svc.go and log.go
// each have three direct dependents, but svc.go's dependents are themselves
// depended on, so it must rank first.
func TestCentralFilesAreRankedByPageRank(t *testing.T) {
	f := newFake()
	for _, l := range []string{"leaf/l1.go", "leaf/l2.go", "leaf/l3.go"} {
		f.link(l, "util/log.go", 1, high)
	}
	for i, m := range []string{"mid/m1.go", "mid/m2.go", "mid/m3.go"} {
		f.link(m, "core/svc.go", 1, high)
		f.link(fmt.Sprintf("top/a%d.go", i), m, 1, high)
		f.link(fmt.Sprintf("top/b%d.go", i), m, 1, high)
	}
	rep := detect(t, f, Options{})

	if len(rep.Central) == 0 {
		t.Fatal("no central files reported")
	}
	if rep.Central[0].Path != "core/svc.go" {
		t.Errorf("most central = %s, want core/svc.go: %+v", rep.Central[0].Path, rep.Central)
	}
	rank := map[string]int{}
	for i, c := range rep.Central {
		rank[c.Path] = i
		if i > 0 && c.Score > rep.Central[i-1].Score {
			t.Errorf("central files not in descending score order: %+v", rep.Central)
		}
	}
	if r, ok := rank["util/log.go"]; ok && r < rank["core/svc.go"] {
		t.Errorf("log.go (leaf dependents) outranks svc.go: %+v", rep.Central)
	}
	if len(rep.Central) > maxCentralFiles {
		t.Errorf("%d central files, bound is %d", len(rep.Central), maxCentralFiles)
	}
}

// A shared directory is a fact about the tree and is used when there is one.
func TestACommunityUnderOneDirectoryIsNamedForIt(t *testing.T) {
	rep := detect(t, twoGroups(), Options{})
	for i, want := range []string{"a", "b"} {
		c := rep.Communities[i]
		if c.Label != want || c.LabelMethod != MethodSharedPath {
			t.Errorf("community %s is labelled %q by %q, want %q by %q", c.ID, c.Label, c.LabelMethod, want, MethodSharedPath)
		}
		if len(c.Directories) != 1 || c.Directories[0] != (Directory{Path: want, Files: 3}) || c.DirectoriesOmitted != 0 {
			t.Errorf("community %s directories = %+v (+%d), want all three files in %q", c.ID, c.Directories, c.DirectoriesOmitted, want)
		}
	}
}

// With no directory in common the name is the member the rest refer to most,
// which is also the member listed first, and the method says it is a guess.
func TestACommunityAcrossDirectoriesIsNamedForItsMostReferencedFile(t *testing.T) {
	f := newFake()
	f.link("cmd/main.go", "lib/core.go", 4, high).link("web/handler.go", "lib/core.go", 3, high)
	f.link("web/handler.go", "cmd/main.go", 1, high).link("x/a.go", "lib/core.go", 1, high)
	f.link("y/b.go", "lib/core.go", 1, high)
	rep := detect(t, f, Options{})

	if len(rep.Communities) != 1 {
		t.Fatalf("want one community, got %+v", rep.Communities)
	}
	c := rep.Communities[0]
	if c.LabelMethod != MethodTopMember || c.Label != "lib/core.go" {
		t.Errorf("label = %q by %q, want lib/core.go by %q", c.Label, c.LabelMethod, MethodTopMember)
	}
	if c.Members[0].Path != c.Label || c.Members[0].ReferencedWithin != 9 {
		t.Errorf("first member = %+v, want the file the community is named for, referenced 9 times", c.Members[0])
	}
	// Five directories, one file each: the three listed are the first three
	// by path and the other two are counted.
	if len(c.Directories) != directoriesListed || c.DirectoriesOmitted != 2 || c.Directories[0].Path != "cmd" {
		t.Errorf("directories = %+v (+%d), want %d listed starting at cmd and 2 omitted",
			c.Directories, c.DirectoriesOmitted, directoriesListed)
	}
}

// The requirement is byte-identical output: same ids, same labels, same order,
// same members, whatever order the index hands its rows over in.
func TestTheAnswerIsTheSameOnEveryRunAndInEveryInputOrder(t *testing.T) {
	render := func(f *fakeSource) string {
		raw, err := json.Marshal(detect(t, f, Options{}))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	want := render(twoGroups())
	for i := 0; i < 20; i++ {
		if got := render(twoGroups()); got != want {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, got, want)
		}
	}

	reversed := twoGroups()
	reversed.unsorted = true
	for i, j := 0, len(reversed.links)-1; i < j; i, j = i+1, j-1 {
		reversed.links[i], reversed.links[j] = reversed.links[j], reversed.links[i]
	}
	if got := render(reversed); got != want {
		t.Errorf("the answer depends on the order links arrive in:\n%s\n---\n%s", got, want)
	}
}

// An id must not move when the caller asks for less: it names a community,
// not a row in one particular answer.
func TestIDsDoNotDependOnTheLimits(t *testing.T) {
	full := detect(t, twoGroups(), Options{})
	one := detect(t, twoGroups(), Options{Limit: 1, Members: 1})
	if one.Communities[0].ID != full.Communities[0].ID || one.Communities[0].Label != full.Communities[0].Label {
		t.Errorf("limit changed the first community from %q (%q) to %q (%q)",
			full.Communities[0].ID, full.Communities[0].Label, one.Communities[0].ID, one.Communities[0].Label)
	}
}

func TestEveryCutIsCounted(t *testing.T) {
	rep := detect(t, twoGroups(), Options{Limit: 1, Members: 2})

	if len(rep.Communities) != 1 || rep.Total != 2 || rep.Omitted != 1 || rep.OmittedFiles != 3 {
		t.Errorf("returned %d of %d, omitted %d holding %d files; want 1 of 2, 1 omitted holding 3",
			len(rep.Communities), rep.Total, rep.Omitted, rep.OmittedFiles)
	}
	c := rep.Communities[0]
	if len(c.Members) != 2 || c.MembersOmitted != 1 {
		t.Errorf("listed %d members and counted %d more, want 2 and 1", len(c.Members), c.MembersOmitted)
	}
	if !hasNote(rep, "Showing the 1 largest of 2 communities") {
		t.Errorf("the limit is not stated in the notes: %q", rep.Notes)
	}

	none := detect(t, twoGroups(), Options{Members: -1})
	if c := none.Communities[0]; len(c.Members) != 0 || c.MembersOmitted != 3 {
		t.Errorf("a negative member bound should list none and count all three, got %d listed, %d omitted",
			len(c.Members), c.MembersOmitted)
	}
}

func TestMembersAreListedMostReferencedFirst(t *testing.T) {
	f := newFake()
	f.link("p/a.go", "p/hub.go", 6, high).link("p/b.go", "p/hub.go", 6, high).link("p/hub.go", "p/b.go", 2, high)
	c := detect(t, f, Options{}).Communities[0]

	var got []string
	for _, m := range c.Members {
		got = append(got, m.Path)
	}
	if strings.Join(got, ",") != "p/hub.go,p/b.go,p/a.go" {
		t.Errorf("member order = %v, want the hub, then b (referenced twice), then a", got)
	}
	if c.Members[0].ReferencedWithin != 12 || c.Members[1].ReferencedWithin != 2 || c.Members[2].ReferencedWithin != 0 {
		t.Errorf("referenced-within counts = %+v", c.Members)
	}
}

// The honest-incompleteness rule: every answer says what the index could not
// see, with the numbers, and says what a community is not.
func TestEveryReportStatesWhatTheIndexCouldNotSee(t *testing.T) {
	rep := detect(t, twoGroups(), Options{})

	if rep.Unbound != 40 || rep.Resolution.Unresolved != 30 {
		t.Errorf("unbound = %d (resolution %+v), want 40 = 30 unresolved + 7 ambiguous + 3 pending", rep.Unbound, rep.Resolution)
	}
	if !hasNote(rep, "40 of its 100 reference(s) are bound to nothing") {
		t.Errorf("the unbound references are not stated: %q", rep.Notes)
	}
	if !hasNote(rep, "graphlume resolve") {
		t.Errorf("pending references should point at the command that binds them: %q", rep.Notes)
	}
	if !hasNote(rep, "2 of 8 file(s) are in no community") || !hasNote(rep, "Unlinked is not unused") {
		t.Errorf("the files left out are not accounted for: %q", rep.Notes)
	}
	if rep.Notes[len(rep.Notes)-1] != Limits {
		t.Errorf("the last note must be the Limits statement, got %q", rep.Notes[len(rep.Notes)-1])
	}
}

// Zero references recorded is not "everything resolved".
func TestNoReferencesIsReportedAsUnknownNotComplete(t *testing.T) {
	f := twoGroups()
	f.stats = registry.ResolutionStats{}
	if rep := detect(t, f, Options{}); !hasNote(rep, "recorded no references at all") {
		t.Errorf("an index with no references must say coverage is unknown: %q", rep.Notes)
	}
}

// An empty answer is never left to speak for itself.
func TestNothingToGroupIsExplained(t *testing.T) {
	noLinks := detect(t, newFake("a.go", "b.go"), Options{})
	if noLinks.Total != 0 || len(noLinks.Communities) != 0 || noLinks.Unlinked != 2 {
		t.Errorf("no links: %+v", noLinks)
	}
	if !hasNote(noLinks, "bound no relationship of medium confidence or better between two different files") ||
		!hasNote(noLinks, "describes the index, not the code") {
		t.Errorf("no links must be explained as a limit of the index: %q", noLinks.Notes)
	}

	noFiles := detect(t, newFake(), Options{})
	if !hasNote(noFiles, "holds no files") {
		t.Errorf("an empty index must say so: %q", noFiles.Notes)
	}

	wrongPrefix := detect(t, twoGroups(), Options{PathPrefix: "nowhere/"})
	if wrongPrefix.Files != 0 || !hasNote(wrongPrefix, `No indexed file is under "nowhere/"`) {
		t.Errorf("a prefix matching nothing must say so: %q", wrongPrefix.Notes)
	}
	for _, rep := range []*Report{noLinks, noFiles, wrongPrefix} {
		if rep.Notes[len(rep.Notes)-1] != Limits {
			t.Errorf("an empty report must still carry the Limits statement: %q", rep.Notes)
		}
		if rep.Communities == nil {
			t.Error("communities must encode as an empty list, not null")
		}
	}
}

// One wrong name match can merge unrelated groups, so low-confidence
// relationships are left out unless asked for, and counted either way.
func TestLowConfidenceLinksAreLeftOutAndCounted(t *testing.T) {
	f := newFake().link("p/a.go", "q/b.go", 3, model.ConfidenceLow)

	rep := detect(t, f, Options{})
	if rep.MinConfidence != model.ConfidenceMedium {
		t.Errorf("default floor = %q, want medium", rep.MinConfidence)
	}
	if rep.Total != 0 || rep.BelowFloor != 3 || rep.Unlinked != 2 {
		t.Errorf("default: %d communities, %d below the floor, %d unlinked; want 0, 3, 2", rep.Total, rep.BelowFloor, rep.Unlinked)
	}
	if !hasNote(rep, "3 relationship(s) between these files are below medium confidence") {
		t.Errorf("what was left out is not stated: %q", rep.Notes)
	}
	if !hasNote(rep, "no relationship of medium confidence or better") {
		t.Errorf("an unlinked file's note must name the floor that made it unlinked: %q", rep.Notes)
	}

	all := detect(t, f, Options{MinConfidence: model.ConfidenceLow})
	if all.Total != 1 || all.BelowFloor != 0 || all.Communities[0].Files != 2 {
		t.Errorf("with the floor lowered: %d communities, %d below the floor", all.Total, all.BelowFloor)
	}
}

func TestAnUnknownConfidenceIsRefused(t *testing.T) {
	if _, err := Detect(context.Background(), twoGroups(), 1, Options{MinConfidence: "probable"}); err == nil {
		t.Error("a misspelt confidence must be an error, not a silent default")
	}
}

// The index reports one row per confidence for a pair of files. They are one
// link, weighing the sum.
func TestRowsForOnePairAreOneLink(t *testing.T) {
	f := newFake().link("p/a.go", "p/b.go", 2, high).link("p/a.go", "p/b.go", 1, model.ConfidenceMedium)
	rep := detect(t, f, Options{})
	if rep.Links != 1 || rep.Relationships != 3 || rep.Communities[0].Internal != 3 {
		t.Errorf("links %d, relationships %d, inside %d; want 1, 3, 3", rep.Links, rep.Relationships, rep.Communities[0].Internal)
	}
}

func TestPathPrefixRestrictsTheGraphAndCountsWhatCrosses(t *testing.T) {
	rep := detect(t, twoGroups(), Options{PathPrefix: "a/"})

	if rep.Files != 3 || rep.Total != 1 || rep.Communities[0].ID != "a/x1.go" {
		t.Fatalf("under a/: %d files, %d communities: %+v", rep.Files, rep.Total, rep.Communities)
	}
	if rep.Communities[0].External != 0 {
		t.Errorf("a relationship leaving the prefix was followed: crossing = %d", rep.Communities[0].External)
	}
	if rep.CrossingPrefix != 1 || !hasNote(rep, `connect a file under "a/" to one outside it`) {
		t.Errorf("crossing the prefix = %d, notes %q; the one link to b/ must be counted and stated", rep.CrossingPrefix, rep.Notes)
	}
}

// Over the bound the graph is not partitioned at all. A partition of the first
// N links would describe an arbitrary slice of the repository as the whole.
func TestAGraphOverTheBoundIsRefusedNotHalfRead(t *testing.T) {
	rep := detect(t, twoGroups(), Options{MaxLinks: 3})

	if !rep.Truncated || !strings.Contains(rep.TruncatedReason, "more than 3 file-to-file links") {
		t.Errorf("truncated = %v, reason %q", rep.Truncated, rep.TruncatedReason)
	}
	if len(rep.Communities) != 0 || rep.Total != 0 || rep.Relationships != 0 {
		t.Errorf("a refused graph must report nothing computed from part of it: %+v", rep)
	}
	if !hasNote(rep, "This is a bound, not a finding") || !hasNote(rep, "Pass a path") {
		t.Errorf("the refusal must say what to do next: %q", rep.Notes)
	}
	if !hasNote(rep, "40 of its 100 reference(s) are bound to nothing") {
		t.Errorf("a refusal must still state what the index could not see: %q", rep.Notes)
	}
	if rep.Notes[len(rep.Notes)-1] != Limits {
		t.Errorf("a refusal must still carry the Limits statement: %q", rep.Notes)
	}

	// Exactly at the bound is inside it.
	if rep := detect(t, twoGroups(), Options{MaxLinks: 7}); rep.Truncated || rep.Total != 2 {
		t.Errorf("a graph of exactly MaxLinks links must be partitioned: truncated=%v total=%d", rep.Truncated, rep.Total)
	}
}

// Memory follows the links held, and the scan stops at the bound: the source
// is not read to the end just to be thrown away.
func TestTheScanStopsAtTheBound(t *testing.T) {
	f := newFake()
	for i := 0; i < 50; i++ {
		f.link("p/hub.go", "p/"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go", 1, high)
	}
	counting := &countingSource{fakeSource: f}
	rep := detect(t, counting, Options{MaxLinks: 5})
	if !rep.Truncated {
		t.Fatal("want the graph refused")
	}
	if counting.delivered != 6 {
		t.Errorf("the scan delivered %d links before stopping, want 6 (the bound, and the one that broke it)", counting.delivered)
	}
}

type countingSource struct {
	*fakeSource
	delivered int
}

func (c *countingSource) EachFileLink(ctx context.Context, runID int64, fn func(registry.FileLink) error) error {
	return c.fakeSource.EachFileLink(ctx, runID, func(l registry.FileLink) error {
		c.delivered++
		return fn(l)
	})
}

func TestSourceErrorsAreReturned(t *testing.T) {
	boom := errors.New("boom")
	for name, f := range map[string]*fakeSource{
		"files": {filesErr: boom},
		"links": {linksErr: boom},
	} {
		if _, err := Detect(context.Background(), f, 1, Options{}); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the source's error", name, err)
		}
	}
	if _, err := Detect(context.Background(), nil, 1, Options{}); err == nil {
		t.Error("a nil source must be an error")
	}
}
