// Package knowledge reports where the recent work on a directory was done by
// so few people that losing one of them loses most of what is known about it.
//
// # The figure
//
// The bus factor of a directory is the smallest number of people whose commits
// make up at least half of the commits that touched it in the history read.
// One means a single person made half or more of the recent changes there.
// Beside it the report gives the share held by the largest contributor and how
// long before the newest commit their last change there was, because "one
// person, and they stopped touching it eight months ago" is a different
// finding from "one person, yesterday".
//
// A commit counts once for each directory it touches, whatever the number of
// files, so a directory's figures are drawn from commits and not from lines.
//
// # This names people, and what it may say about them is narrow
//
// Git records that somebody changed a file. It does not record that they
// understand it, that nobody else does, or that they are still here. The
// figure is a count of authorship in a bounded window, and every report carries
// that as data (Report.Caveats) so a renderer cannot print the names without
// printing what they mean. See internal/capability/ownership for the longer
// form of this argument; it applies here unchanged.
//
// # A person is a name, exactly as spelled
//
// People are grouped by the author name the history records and by nothing
// else. No identities are merged: deciding that "R. Vega" and "Robin Vega" are
// one person is a guess, and a guess here moves somebody's name up or down a
// list about risk. The cost is stated instead of hidden. One person under two
// spellings counts as two, which makes a bus factor read higher than it is, so
// when two names commit from the same address the report says which names,
// and leaves them apart.
//
// No field of a Report holds an email address. The address is read only to
// make that one comparison and is dropped with the commit.
//
// # What distorts it
//
// A shallow clone has no history before the depth it was cloned at. A
// squash-merge workflow records a whole branch as one commit under one name,
// whoever wrote the commits inside it. A mass move or reformat credits whoever
// ran it in every directory it touched. All three are stated in every report,
// because a reader who does not know them reads a wrong number as a fact.
//
// # Bounds
//
// History is read through a commit limit, never unbounded. Directories with
// fewer than MinCommits commits are not measured, since one author out of two
// commits is not a finding, and the report counts them.
package knowledge

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Commit is one commit reduced to what the figure needs. Paths must already be
// in the index's path space; see internal/capability/ownership.Commit, whose
// reasoning for restating the type at the consumer applies here unchanged.
type Commit struct {
	SHA string
	// Author is the author name as the history spells it.
	Author string
	// AuthorEmail is used only to notice two names committing from one
	// address. It is never copied into a Report.
	AuthorEmail string
	When        int64 // author time, Unix seconds
	Paths       []string
	// ShallowBoundary marks a commit at the edge of a shallow clone; see
	// ownership.Commit.ShallowBoundary.
	ShallowBoundary bool
	// Truncated marks the last commit of a walk cut by its time budget; see
	// ownership.Commit.Truncated.
	Truncated bool
}

// History is the bounded commit source. An implementation that cannot honour
// limit must return an error rather than read everything.
type History interface {
	Commits(ctx context.Context, limit int) ([]Commit, error)
}

// Index tells the analysis which paths still exist, so a directory deleted
// since the commits that touched it is not reported as a risk.
type Index interface {
	Files(ctx context.Context, runID int64) ([]model.File, error)
}

var _ Index = (*registry.Registry)(nil)

// Options bounds an analysis.
type Options struct {
	// Limit is the most areas to return. Zero means DefaultLimit.
	Limit int
	// CommitWindow is how many recent commits to read. Zero means
	// DefaultCommitWindow.
	CommitWindow int
	// Months is the time window: commits older than this many months before
	// the newest commit read are left out. Zero means DefaultMonths; negative
	// means no time bound, only CommitWindow. CommitWindow caps the walk
	// either way, so on a busy repository the period read can be shorter than
	// asked, and the report says so (PeriodCovered).
	Months int
	// MinCommits is the fewest commits an area needs to be measured. Zero
	// means DefaultMinCommits.
	MinCommits int
	// PathPrefix restricts the analysis to files under it.
	PathPrefix string
	// Depth groups by the first Depth path segments instead of by each file's
	// own directory. Zero means each file's own directory.
	Depth int
	// PerFile measures each file instead of each directory.
	PerFile bool
	// ShallowClone says the repository was cloned with truncated history,
	// whether or not this walk reached the point where it was cut. A walk that
	// does reach it says so itself (Commit.ShallowBoundary), so this is only
	// needed for the other case: a window smaller than the clone depth, whose
	// figures still leave out everyone older than the depth.
	ShallowClone bool
}

const (
	DefaultLimit        = 20
	DefaultCommitWindow = 5000
	DefaultMonths       = 12
	DefaultMinCommits   = 5

	// contributorsPerArea is how many names are listed for one area.
	contributorsPerArea = 3
	// sharedAddressGroups is how many groups of names committing from one
	// address are spelled out; the rest are counted.
	sharedAddressGroups = 3

	secondsPerDay = 86400
)

// Unit is what one row of a report measures.
type Unit string

const (
	UnitDirectory Unit = "directory"
	UnitFile      Unit = "file"
)

// Root is the path of the repository's top directory in a report.
const Root = "."

// Contributor is one author name's part in an area.
type Contributor struct {
	// Name is the author name as the history spells it.
	Name string `json:"name"`
	// Commits is how many commits under this name touched the area.
	Commits int `json:"commits"`
	// Share is Commits over the area's commit count.
	Share float64 `json:"share"`
	// LastChanged is the author time of the newest of them.
	LastChanged int64 `json:"last_changed_unix,omitempty"`
}

// Area is one measured directory or file.
type Area struct {
	// Path is the directory or file, "." for the top directory.
	Path string `json:"path"`

	// BusFactor is the smallest number of names whose commits make up at least
	// half of Commits.
	BusFactor int `json:"bus_factor"`
	// Commits is how many commits in the window touched the area.
	Commits int `json:"commits"`
	// Contributors is how many distinct author names those commits carry.
	Contributors int `json:"contributors"`

	// TopShare is the largest contributor's share of Commits.
	TopShare float64 `json:"top_share"`
	// TopLastChanged is when the largest contributor last changed the area.
	TopLastChanged int64 `json:"top_last_changed_unix,omitempty"`
	// TopIdleDays is how many days before the newest commit read that was. It
	// is measured against the history, not the clock, so the same history
	// gives the same number on any day.
	TopIdleDays int `json:"top_idle_days"`

	// LastChanged is when anybody last changed the area.
	LastChanged int64 `json:"last_changed_unix,omitempty"`

	// Top lists the largest contributors, largest first.
	Top []Contributor `json:"top_contributors"`
}

// Report is the answer to "where is knowledge held by too few people?".
type Report struct {
	RunID int64 `json:"run_id"`
	Unit  Unit  `json:"unit"`

	// Areas are the measured areas, most at risk first, cut to the limit.
	Areas []Area `json:"areas"`
	// Measured is how many areas had enough commits to measure, before the
	// limit. SolePerson is how many of those have a bus factor of one.
	Measured   int `json:"areas_measured"`
	SolePerson int `json:"areas_with_bus_factor_one"`
	// Excluded is how many areas the window touched but that had fewer than
	// MinCommits commits. They are not low risk; they are unmeasured.
	Excluded   int `json:"areas_excluded_too_few_commits"`
	MinCommits int `json:"min_commits"`
	// NotIndexed is how many areas the window touched that the index no
	// longer holds: deleted, renamed or ignored since.
	NotIndexed int `json:"areas_not_indexed"`

	// Overall is the same figure for every commit read that changed a file
	// in scope, taken as one area. Its Path is the path prefix asked for,
	// empty when there was none. Nil when no commit changed anything in scope.
	Overall *Area `json:"overall,omitempty"`

	// CommitsRead is how much history the figures are drawn from, and
	// WindowFull says whether more history exists beyond it.
	CommitsRead  int   `json:"commits_read"`
	CommitWindow int   `json:"commit_window"`
	WindowFull   bool  `json:"window_full"`
	OldestCommit int64 `json:"oldest_commit_unix,omitempty"`
	NewestCommit int64 `json:"newest_commit_unix,omitempty"`
	ShallowClone bool  `json:"shallow_clone"`
	// Months is the time window and SinceUnix where it starts (Months before
	// NewestCommit). PeriodCovered says the whole period was read: the walk
	// reached a commit older than SinceUnix, or the start of history.
	Months        int   `json:"months,omitempty"`
	SinceUnix     int64 `json:"since_unix,omitempty"`
	PeriodCovered bool  `json:"period_covered"`
	// readPast says the walk went past SinceUnix and left older commits out.
	readPast bool
	// Partial says the walk stopped at its time budget before reading the
	// whole window; see ownership.Window.Partial.
	Partial bool `json:"partial,omitempty"`

	// SharedAddress lists groups of author names that committed from one
	// address: possibly one person counted more than once. Names only.
	SharedAddress      [][]string `json:"names_sharing_an_address,omitempty"`
	SharedAddressNames int        `json:"names_sharing_an_address_count"`

	// NamesCut is how many author names were recorded as an email address
	// and are shown only up to the @.
	NamesCut int `json:"names_recorded_as_addresses"`

	// Notes say what this run could not see. Caveats say what the figure
	// cannot establish on any run. Both are part of the answer.
	Notes   []string `json:"notes,omitempty"`
	Caveats []string `json:"caveats"`
}

// tally is one author name's commits in one area.
type tally struct {
	commits int
	last    int64
}

// ledger is the commits touching one area, by author name.
type ledger struct {
	commits int
	last    int64
	by      map[string]*tally
}

func (l *ledger) add(author string, when int64) {
	if l.by == nil {
		l.by = map[string]*tally{}
	}
	l.commits++
	l.last = max(l.last, when)
	t := l.by[author]
	if t == nil {
		t = &tally{}
		l.by[author] = t
	}
	t.commits++
	t.last = max(t.last, when)
}

// Analyze reads the commit window and measures every area it touched.
func Analyze(ctx context.Context, hist History, idx Index, runID int64, opts Options) (*Report, error) {
	if hist == nil || idx == nil {
		return nil, fmt.Errorf("knowledge: history and index are both required")
	}
	limit := orDefault(opts.Limit, DefaultLimit)
	window := orDefault(opts.CommitWindow, DefaultCommitWindow)
	months := opts.Months
	if months == 0 {
		months = DefaultMonths
	}
	months = max(months, 0)
	minCommits := orDefault(opts.MinCommits, DefaultMinCommits)
	unit := UnitDirectory
	if opts.PerFile {
		unit = UnitFile
	}

	rep := &Report{
		RunID: runID, Unit: unit, MinCommits: minCommits, CommitWindow: window, Months: months,
		ShallowClone: opts.ShallowClone,
		Caveats:      caveats(unit),
	}

	// The reader stops at a shallow clone's boundary and returns what is
	// there, so a failure here is a failure and needs no special case.
	commits, err := hist.Commits(ctx, window)
	if err != nil {
		return nil, fmt.Errorf("knowledge: read history: %w", err)
	}
	rep.CommitsRead, rep.WindowFull = len(commits), len(commits) >= window
	if months > 0 && len(commits) > 0 {
		// Measured from the newest commit, not the clock, so the same history
		// gives the same window on any day. Not assuming the walk's order.
		_, newest := span(commits)
		rep.SinceUnix = time.Unix(newest, 0).UTC().AddDate(0, -months, 0).Unix()
		kept := make([]Commit, 0, len(commits))
		for _, c := range commits {
			if c.When >= rep.SinceUnix {
				kept = append(kept, c)
			}
		}
		if len(kept) < len(commits) {
			// Read past the start of the window: the whole period is here, and
			// what is older was left out on purpose.
			rep.PeriodCovered, rep.readPast = true, true
			rep.WindowFull = false
			commits = kept
			rep.CommitsRead = len(kept)
		}
	}
	for i := range commits {
		if commits[i].ShallowBoundary {
			rep.ShallowClone = true
		}
		if commits[i].Truncated {
			rep.Partial = true
			rep.WindowFull = true // more history exists; it was not read in time
		}
	}
	if len(commits) == 0 {
		rep.Notes = append(rep.Notes, "No commit history could be read, so nothing can be measured. "+
			"Knowledge distribution needs a git repository with at least one commit.")
		return rep, nil
	}
	rep.OldestCommit, rep.NewestCommit = span(commits)
	if months > 0 && !rep.WindowFull && !rep.ShallowClone {
		rep.PeriodCovered = true // the whole history is inside the window
	}

	files, err := idx.Files(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("knowledge: %w", err)
	}
	live := make(map[string]struct{}, len(files))
	for _, f := range files {
		live[areaOf(f.Path, opts)] = struct{}{}
	}

	areas := map[string]*ledger{}
	gone := map[string]struct{}{}
	overall := &ledger{}
	addresses := map[string]map[string]struct{}{}
	asAddress := map[string]struct{}{}

	for _, c := range commits {
		author := strings.TrimSpace(c.Author)
		inScope := false
		touched := map[string]struct{}{}
		for _, p := range c.Paths {
			if opts.PathPrefix != "" && !strings.HasPrefix(p, opts.PathPrefix) {
				continue
			}
			inScope = true
			a := areaOf(p, opts)
			if _, ok := live[a]; !ok {
				gone[a] = struct{}{}
				continue
			}
			touched[a] = struct{}{}
		}
		if !inScope {
			continue
		}
		for a := range touched {
			l := areas[a]
			if l == nil {
				l = &ledger{}
				areas[a] = l
			}
			l.add(author, c.When)
		}
		// The whole-scope figure counts every commit that changed something
		// in scope, indexed now or not, so it does not move with how the
		// areas are grouped.
		overall.add(author, c.When)
		if strings.Contains(author, "@") {
			asAddress[author] = struct{}{}
		}

		if addr := strings.ToLower(strings.TrimSpace(c.AuthorEmail)); addr != "" {
			if addresses[addr] == nil {
				addresses[addr] = map[string]struct{}{}
			}
			addresses[addr][author] = struct{}{}
		}
	}
	rep.NotIndexed = len(gone)

	if overall.commits > 0 {
		o := measure(opts.PathPrefix, overall, rep.NewestCommit)
		rep.Overall = &o
	}

	measured := make([]Area, 0, len(areas))
	for p, l := range areas {
		if l.commits < minCommits {
			rep.Excluded++
			continue
		}
		measured = append(measured, measure(p, l, rep.NewestCommit))
	}
	sort.Slice(measured, func(i, j int) bool { return riskier(measured[i], measured[j]) })
	rep.Measured = len(measured)
	for _, a := range measured {
		if a.BusFactor == 1 {
			rep.SolePerson++
		}
	}
	if len(measured) > limit {
		measured = measured[:limit]
	}
	rep.Areas = measured

	rep.SharedAddress, rep.SharedAddressNames = sharedAddresses(addresses)
	rep.NamesCut = len(asAddress)
	rep.Notes = notes(rep, opts, len(areas))
	return rep, nil
}

// measure turns one area's ledger into its figures.
func measure(p string, l *ledger, newest int64) Area {
	people := make([]Contributor, 0, len(l.by))
	for name, t := range l.by {
		people = append(people, Contributor{
			Name: displayName(name), Commits: t.commits,
			Share: float64(t.commits) / float64(l.commits), LastChanged: t.last,
		})
	}
	// Total order: commits, then the more recent, then the name. Without the
	// last two keys two people with equal counts would swap places between
	// runs, and the name at the top of a risk list must not depend on map
	// iteration.
	sort.Slice(people, func(i, j int) bool {
		a, b := people[i], people[j]
		if a.Commits != b.Commits {
			return a.Commits > b.Commits
		}
		if a.LastChanged != b.LastChanged {
			return a.LastChanged > b.LastChanged
		}
		return a.Name < b.Name
	})

	// The smallest number of people covering at least half. Twice the running
	// total against the whole keeps the comparison in integers.
	factor, covered := 0, 0
	for _, c := range people {
		factor++
		covered += c.Commits
		if 2*covered >= l.commits {
			break
		}
	}

	a := Area{
		Path: p, BusFactor: factor, Commits: l.commits, Contributors: len(people),
		LastChanged: l.last,
	}
	if len(people) > 0 {
		top := people[0]
		a.TopShare = top.Share
		a.TopLastChanged = top.LastChanged
		if newest > top.LastChanged {
			a.TopIdleDays = int((newest - top.LastChanged) / secondsPerDay)
		}
	}
	if len(people) > contributorsPerArea {
		people = people[:contributorsPerArea]
	}
	a.Top = people
	return a
}

// riskier orders areas: fewer people covering half first, then the larger
// share held by one name, then the busier area, then the path.
//
// The share is compared as a cross product of the two integer ratios, so two
// equal shares compare equal exactly rather than to within a float's rounding.
func riskier(a, b Area) bool {
	if a.BusFactor != b.BusFactor {
		return a.BusFactor < b.BusFactor
	}
	at, bt := topCommits(a), topCommits(b)
	if l, r := at*b.Commits, bt*a.Commits; l != r {
		return l > r
	}
	if a.Commits != b.Commits {
		return a.Commits > b.Commits
	}
	return a.Path < b.Path
}

func topCommits(a Area) int {
	if len(a.Top) == 0 {
		return 0
	}
	return a.Top[0].Commits
}

// areaOf maps a file path to the area it is counted under.
func areaOf(p string, opts Options) string {
	if opts.PerFile {
		return p
	}
	dir := path.Dir(p)
	if opts.Depth <= 0 || dir == Root {
		return dir
	}
	segs := strings.Split(dir, "/")
	if len(segs) > opts.Depth {
		segs = segs[:opts.Depth]
	}
	return strings.Join(segs, "/")
}

// displayName is the name a report prints for an author.
//
// It is the recorded name with one exception. Some people set their git name
// to their email address, and a report must never carry an address, so a name
// holding an @ is cut at it. The grouping still uses the full recorded name:
// two such names stay two people.
func displayName(recorded string) string {
	if recorded == "" {
		return "(no name recorded)"
	}
	if at := strings.IndexByte(recorded, '@'); at >= 0 {
		local := strings.TrimSpace(recorded[:at])
		if local == "" {
			return "(name withheld: recorded as an address)"
		}
		return local
	}
	return recorded
}

// sharedAddresses finds groups of two or more names that committed from one
// address. It returns the first few groups and the number of distinct names
// in all of them. Only names leave this function.
func sharedAddresses(addresses map[string]map[string]struct{}) ([][]string, int) {
	var groups [][]string
	seen := map[string]struct{}{}
	names := map[string]struct{}{}
	for _, set := range addresses {
		if len(set) < 2 {
			continue
		}
		group := make([]string, 0, len(set))
		for n := range set {
			group = append(group, displayName(n))
			names[n] = struct{}{}
		}
		sort.Strings(group)
		// Two addresses can hold the same set of names; one mention is enough.
		key := strings.Join(group, "\x00")
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return strings.Join(groups[i], "\x00") < strings.Join(groups[j], "\x00")
	})
	if len(groups) > sharedAddressGroups {
		groups = groups[:sharedAddressGroups]
	}
	return groups, len(names)
}

// span returns the oldest and newest author times in the commits read.
func span(commits []Commit) (oldest, newest int64) {
	oldest, newest = commits[0].When, commits[0].When
	for _, c := range commits[1:] {
		oldest = min(oldest, c.When)
		newest = max(newest, c.When)
	}
	return oldest, newest
}

// caveats are the limits that hold for every report, whatever it found.
func caveats(unit Unit) []string {
	return []string{
		fmt.Sprintf("The bus factor is the smallest number of author names whose commits make up at least "+
			"half of the commits touching a %s in the history read. It counts who CHANGED the code, not who "+
			"understands it: review, pairing and co-authors named only in a commit message are not counted.", unit),
		"A person is an author name exactly as git recorded it. Names are never merged: one person " +
			"committing under two spellings counts as two, which makes a bus factor read higher than it is, " +
			"and two people who share a name count as one. Email addresses are never reported.",
		"A shallow clone has no history before its depth, and a squash-merge workflow records a whole " +
			"branch as one commit under one name, so both distort these figures. So does a mass move or " +
			"reformat, which credits whoever ran it in every place it touched.",
	}
}

// notes are what this particular run could and could not see.
func notes(rep *Report, opts Options, touched int) []string {
	var out []string
	plural := string(rep.Unit) + "(s)"
	if rep.Unit == UnitDirectory {
		plural = "directory(ies)"
	}

	switch {
	case rep.ShallowClone:
		available := ""
		if !rep.WindowFull {
			available = fmt.Sprintf(" Shallow clone: %d commit(s) available.", rep.CommitsRead)
		}
		out = append(out, fmt.Sprintf("This is a shallow clone: history was cut off when it was cloned, so "+
			"the %d commit(s) read (%s to %s) are not the repository's whole history even where no more could "+
			"be read.%s Fetch the full history (git fetch --unshallow) before relying on these figures.",
			rep.CommitsRead, day(rep.OldestCommit), day(rep.NewestCommit), available))
	case rep.Partial:
		out = append(out, fmt.Sprintf("Partial: %d of %d commit(s) were read (%s to %s) before the history "+
			"time budget ran out. Earlier history in the window was not read, so anyone whose work is all in "+
			"it is absent. Ask again to read further: the walk continues where it stopped.",
			rep.CommitsRead, rep.CommitWindow, day(rep.OldestCommit), day(rep.NewestCommit)))
	case rep.WindowFull && rep.Months > 0:
		out = append(out, fmt.Sprintf("Only the newest %d commit(s) were read (%s to %s): the commit budget ran "+
			"out before the %d-month window did. Any earlier history was not read, so anyone whose "+
			"work is all in it is absent. Read further back with a larger commit budget.",
			rep.CommitsRead, day(rep.OldestCommit), day(rep.NewestCommit), rep.Months))
	case rep.readPast:
		out = append(out, fmt.Sprintf("The last %d month(s) of history were read: %d commit(s) (%s to %s). "+
			"Older commits are not counted, so anyone whose work here is all older than that is absent.",
			rep.Months, rep.CommitsRead, day(rep.SinceUnix), day(rep.NewestCommit)))
	case rep.WindowFull:
		out = append(out, fmt.Sprintf("Only the newest %d commit(s) were read (%s to %s), which is the limit "+
			"asked for. Any earlier history was not read, so anyone whose work is all in it is absent. Read "+
			"further back with a larger commit window.", rep.CommitsRead, day(rep.OldestCommit), day(rep.NewestCommit)))
	default:
		out = append(out, fmt.Sprintf("All %d commit(s) reachable from HEAD were read (%s to %s).",
			rep.CommitsRead, day(rep.OldestCommit), day(rep.NewestCommit)))
	}

	switch {
	case touched == 0 && opts.PathPrefix != "":
		out = append(out, fmt.Sprintf("None of those commits touched anything the index holds under %q.", opts.PathPrefix))
	case touched == 0:
		out = append(out, "None of those commits touched anything the index holds.")
	case rep.Measured == 0:
		out = append(out, fmt.Sprintf("None of the %d %s those commits touched has %d or more commits, so "+
			"none can be measured. That is not a finding of low risk. Lower the minimum, group by fewer path "+
			"segments or read more history.", touched, plural, rep.MinCommits))
	}
	if rep.Excluded > 0 && rep.Measured > 0 {
		out = append(out, fmt.Sprintf("%d %s with fewer than %d commits in this history were not measured: "+
			"too few to say anything. They are unmeasured, not low risk.", rep.Excluded, plural, rep.MinCommits))
	}
	if n := rep.Measured - len(rep.Areas); n > 0 {
		out = append(out, fmt.Sprintf("%d more measured %s are not listed; raise the limit or narrow by path "+
			"to see them.", n, plural))
	}
	if rep.NotIndexed > 0 {
		out = append(out, fmt.Sprintf("%d %s changed in this history hold nothing the index has now (never "+
			"indexed, or deleted or renamed since) and are not measured. Renames are not followed, so work "+
			"done under an old path is not credited to the new one.", rep.NotIndexed, plural))
	}
	if rep.SharedAddressNames > 0 {
		quoted := make([]string, 0, len(rep.SharedAddress))
		listed := 0
		for _, g := range rep.SharedAddress {
			quoted = append(quoted, "'"+strings.Join(g, "' and '")+"'")
			listed += len(g)
		}
		among := ""
		if listed < rep.SharedAddressNames {
			among = ", among others"
		}
		out = append(out, fmt.Sprintf("%d author name(s) committed from an address another name also used "+
			"(%s%s). They may be one person counted more than once; they are left apart, because merging "+
			"them would be a guess.", rep.SharedAddressNames, strings.Join(quoted, "; "), among))
	}
	if rep.NamesCut > 0 {
		out = append(out, fmt.Sprintf("%d author name(s) were recorded as an email address. Only the part "+
			"before the at sign is shown, because this report never carries an address.", rep.NamesCut))
	}
	return out
}

func day(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02")
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
