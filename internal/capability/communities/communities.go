// Package communities groups a repository's files by how they refer to each
// other, and reports the groups.
//
// # What a community is here
//
// The unit is the file. Two files are linked when a relationship the index
// bound (a call, a type relationship, a table read, anything but nesting)
// starts in one and ends in the other, and the link weighs as many
// relationships as there are. A community is a set of files whose links run
// between its members more than they run outwards.
//
// Files, not symbols, for three reasons. A file is something a reader can open
// and an agent can pass to its next tool. The file graph is one to two orders
// of magnitude smaller than the symbol graph, so the partition stays cheap on
// an index of any size. And a file's directory is evidence a name can be drawn
// from, where a symbol's is one step further removed.
//
// # What it is not
//
// It is not the architecture. Nobody declared these groups: they are a
// description of the code's current shape, read off the relationships the
// index happened to bind. Declared components come from the project's own
// configuration and are the only grouping this product presents as intent.
// The report's Limits statement says so on every surface.
//
// It is also not complete, in a specific direction. A reference the index
// could not bind is a link that is missing from the graph, so files that work
// together can be reported apart, or in no community at all. The report states
// how many references were left unbound, because that number is the size of
// what the partition did not see.
//
// # Determinism
//
// The partition comes from internal/analyze/cluster, whose label propagation
// visits nodes in a total order and breaks every tie on the smaller key. This
// package adds nothing that could disturb that: every list is sorted on a
// total order, and a community's id is its smallest member path. The same
// index gives the same ids, the same labels and the same order on every call.
//
// # Bounds
//
// Links are streamed from the index and held only for the files in scope. A
// graph with more than MaxLinks of them is not partitioned at all: the report
// comes back empty, says why, and asks for a path. Stopping partway would
// partition an arbitrary slice of the repository and present it as the whole.
// The answer itself is bounded twice, by the number of communities returned
// and by the members listed for each, and both cuts are counted.
package communities

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/cluster"
	"github.com/sutrakam/graphlume/internal/analyze/metrics"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Source is the read surface this package needs: the run's files, its
// relationships folded to file-to-file links, and how much of it resolved.
//
// Declared here, at the consumer. The registry satisfies it; a test satisfies
// it with two slices.
type Source interface {
	Files(ctx context.Context, runID int64) ([]model.File, error)
	EachFileLink(ctx context.Context, runID int64, fn func(registry.FileLink) error) error
	ResolutionStats(ctx context.Context, runID int64) (registry.ResolutionStats, error)
}

var _ Source = (*registry.Registry)(nil)

const (
	// DefaultLimit is how many communities are returned unless asked.
	DefaultLimit = 20
	// DefaultMembers is how many files are listed for each community.
	DefaultMembers = 8
	// MaxLinks is the most file-to-file links one partition reads. Past it the
	// graph costs more memory than an answer one page long is worth, and the
	// report asks for a path instead. It is far above an ordinary repository:
	// this one has under 3,000 links across 700 files.
	MaxLinks = 250_000
)

// DefaultMinConfidence leaves low-confidence relationships out of the graph.
//
// The same floor impact analysis uses, and for a sharper reason here. A low
// binding is mostly a match on the name alone, and a partition is unusually easy for
// one to wreck: on this repository every test's t.Fatalf was bound, low, to a
// Fatalf method one test file happens to define, which made that file the
// centre of a community holding half the repository. One wrong edge misleads a
// caller list by one row; one wrong hub merges every group that touches it.
const DefaultMinConfidence = model.ConfidenceMedium

// Label methods, as internal/analyze/cluster names them.
const (
	// MethodSharedPath: every member sits under the directory the label names.
	// A fact about the tree.
	MethodSharedPath = "path-prefix"
	// MethodTopMember: the members share no directory, so the community is
	// named after the member the others refer to most. A convenience.
	MethodTopMember = "top-ranked-member"
)

// Method says how the partition was produced, for a reader deciding how far to
// trust it.
const Method = "deterministic label propagation over the file graph: files are linked by the " +
	"relationships the index bound between their symbols, weighted by how many there are, " +
	"visited in path order with ties broken on the smaller path"

// MethodLeiden is the Method of a report produced with the Leiden algorithm.
const MethodLeiden = "deterministic Leiden modularity optimisation over the file graph: files are " +
	"linked by the relationships the index bound between their symbols, weighted by how many " +
	"there are, visited in path order with ties broken on the smaller path"

// Limits is the statement every rendering of a report carries.
const Limits = "A community is a description of how the indexed code currently refers to itself, " +
	"not a declared architecture. Files are grouped only by relationships the index bound. A " +
	"reference it could not bind is a missing link, so files that work together can be reported " +
	"in different communities, or in none; and a binding it got wrong is a false link, which can " +
	"join two groups that are separate. Read a community as a place to start looking, not as a " +
	"boundary."

// Options bounds a detection.
type Options struct {
	// Limit is the most communities to return. Zero means DefaultLimit.
	Limit int
	// Members is the most files to list for each community. Zero means
	// DefaultMembers; negative lists none, leaving the counts.
	Members int
	// PathPrefix restricts the graph to files under it. Relationships that
	// cross the prefix are counted and not followed.
	PathPrefix string
	// Algorithm is "leiden" (the default, also the empty string) or
	// "label_propagation".
	Algorithm string
	// MinConfidence is the weakest relationship that counts as a link. The
	// zero value means DefaultMinConfidence. Relationships below it are
	// counted in the report and otherwise ignored.
	MinConfidence model.Confidence
	// MaxLinks overrides the package bound. Zero means MaxLinks.
	MaxLinks int
}

// Member is one file of a community.
type Member struct {
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
	// ReferencedWithin counts relationships that start in another member of
	// the same community and end in this file. It orders the member list:
	// the file the rest of the group leans on comes first.
	ReferencedWithin int `json:"referenced_within"`
}

// Community is one group of files.
type Community struct {
	// ID is the smallest member path. It is a real file, and it does not
	// change between two calls on the same index, whatever the limits.
	ID string `json:"id"`

	// Label is a name for the group and LabelMethod says where it came from:
	// MethodSharedPath or MethodTopMember.
	Label       string `json:"label"`
	LabelMethod string `json:"label_method"`

	// Files is the community's size.
	Files int `json:"files"`

	// Internal counts relationships between two members; External counts
	// relationships with one end here and the other in a file outside. Their
	// ratio is the only evidence that the group means anything, so both are
	// reported instead of a verdict.
	Internal int `json:"internal_relationships"`
	External int `json:"external_relationships"`

	// Directories is where the members are, the fullest directories first, at
	// most directoriesListed of them; DirectoriesOmitted counts the rest. It
	// tells apart communities whose only shared path is the repository's top
	// directory, which is most of them in a tree with one source root.
	Directories        []Directory `json:"directories"`
	DirectoriesOmitted int         `json:"directories_omitted"`

	// Members is the most-referenced files, at most Options.Members of them,
	// and MembersOmitted is how many more there are.
	Members        []Member `json:"members"`
	MembersOmitted int      `json:"members_omitted"`
}

// Directory is how many of a community's files sit directly in one directory.
// Path is "." for the repository root.
type Directory struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
}

// directoriesListed bounds Community.Directories.
const directoriesListed = 3

// Report is the answer to "which files belong together?".
type Report struct {
	RunID       int64       `json:"run_id"`
	Method      string      `json:"method"`
	PathPrefix  string      `json:"path_prefix,omitempty"`
	Communities []Community `json:"communities"`

	// Total is how many communities of two or more files were found, and
	// Omitted how many of them the limit left out, holding OmittedFiles files.
	Total        int `json:"communities_total"`
	Omitted      int `json:"communities_omitted"`
	OmittedFiles int `json:"files_in_omitted_communities"`

	// Files is how many files were in scope. Grouped of them are in a
	// community of two or more. The rest are Unlinked (the index bound no
	// relationship between them and another file in scope) or Alone (linked,
	// but the partition left them in a group of one).
	Files    int `json:"files"`
	Grouped  int `json:"files_grouped"`
	Unlinked int `json:"files_unlinked"`
	Alone    int `json:"files_alone"`

	// MinConfidence is the floor a relationship had to meet to count.
	MinConfidence model.Confidence `json:"min_confidence"`
	// Links is the file-to-file links the partition was computed from, and
	// Relationships the bound relationships they stand for.
	Links int `json:"file_links"`
	// Modularity scores the partition of the linked files on the undirected,
	// weighted file graph (standard modularity, resolution 1).
	Modularity    float64 `json:"modularity"`
	Relationships int     `json:"relationships"`
	// BelowFloor counts relationships between two files in scope that were
	// left out for being weaker than MinConfidence.
	BelowFloor int `json:"relationships_below_min_confidence"`
	// CrossingPrefix counts relationships with exactly one end under
	// PathPrefix. They were not followed.
	CrossingPrefix int `json:"relationships_crossing_path_prefix,omitempty"`

	// Resolution is the run's reference-resolution outcome, published as
	// recorded. Unbound is the references that were extracted and attached to
	// nothing: each is a link this report was computed without.
	Resolution registry.ResolutionStats `json:"resolution"`
	Unbound    int64                    `json:"unbound_references"`

	// Truncated reports that the graph was too large to partition. Communities
	// is then empty and every count except Files is zero because it was never
	// computed, not because it was found to be zero.
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncated_reason,omitempty"`

	// Central is the most central linked files by PageRank over the same
	// file graph the partition used, highest first. Unlike a raw inbound
	// count, PageRank ranks a file above another with the same number of
	// dependents when its dependents are themselves widely depended on, so a
	// logger used by leaves ranks below the service the whole tree goes
	// through. Score is the file's share of the total rank (all files sum to 1).
	Central []CentralFile `json:"central_files,omitempty"`

	Notes []string `json:"notes"`
}

// CentralFile is one file ranked by PageRank.
type CentralFile struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

// maxCentralFiles bounds the central-file list a report carries.
const maxCentralFiles = 10

// errTooManyLinks stops the link scan at the bound.
var errTooManyLinks = errors.New("communities: link bound reached")

// link is one in-scope file-to-file link, by index into the scope's paths.
type link struct {
	src, dst int32
	n        int
}

// Detect partitions the files of runID and reports the communities found.
func Detect(ctx context.Context, src Source, runID int64, opts Options) (*Report, error) {
	if src == nil {
		return nil, errors.New("communities: a source is required")
	}
	limit := orDefault(opts.Limit, DefaultLimit)
	members := opts.Members
	if members == 0 {
		members = DefaultMembers
	}
	members = max(members, 0)
	maxLinks := orDefault(opts.MaxLinks, MaxLinks)
	prefix := strings.TrimSpace(opts.PathPrefix)
	floor := opts.MinConfidence
	if floor == "" {
		floor = DefaultMinConfidence
	}
	if !floor.Valid() {
		return nil, fmt.Errorf("communities: unknown confidence %q: use exact, high, medium or low", floor)
	}

	copts := cluster.DefaultOptions()
	method := Method
	// Leiden is the default: measured on this repository, go-git and a small
	// service, it scored higher modularity than label propagation (0.72 vs
	// 0.57, 0.60 vs 0.53) at the same speed, with fewer fragments and no
	// 500-file catch-all community, and it is deterministic. Label
	// propagation stays available by name.
	switch opts.Algorithm {
	case cluster.AlgorithmLabelPropagation:
	case "", cluster.AlgorithmLeiden:
		copts.Algorithm = cluster.AlgorithmLeiden
		method = MethodLeiden
	default:
		return nil, fmt.Errorf("communities: unknown algorithm %q: use %s or %s",
			opts.Algorithm, cluster.AlgorithmLabelPropagation, cluster.AlgorithmLeiden)
	}

	rep := &Report{
		RunID: runID, Method: method, PathPrefix: prefix, MinConfidence: floor,
		Communities: []Community{},
	}

	stats, err := src.ResolutionStats(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("communities: %w", err)
	}
	rep.Resolution = stats
	rep.Unbound = stats.Unresolved + stats.Ambiguous + stats.Pending

	files, err := src.Files(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("communities: %w", err)
	}
	// The scope: every file under the prefix, by position in path order.
	var (
		paths []string
		langs []string
		index = make(map[int64]int32, len(files))
	)
	for _, f := range files {
		if prefix != "" && !strings.HasPrefix(f.Path, prefix) {
			continue
		}
		index[f.ID] = int32(len(paths)) //nolint:gosec // a run holds far fewer than 2^31 files
		paths = append(paths, f.Path)
		langs = append(langs, f.Language)
	}
	rep.Files = len(paths)

	var links []link
	err = src.EachFileLink(ctx, runID, func(l registry.FileLink) error {
		a, aok := index[l.SrcFileID]
		b, bok := index[l.DstFileID]
		switch {
		case aok && bok:
			if a == b {
				return nil
			}
			if !l.Confidence.AtLeast(floor) {
				rep.BelowFloor += l.Relationships
				return nil
			}
			if l.Relationships <= 0 {
				return nil // metrics.NewGraph would count a weightless link as one
			}
			rep.Relationships += l.Relationships
			// The rows for one pair of files arrive together, one per
			// confidence, and are one link.
			if n := len(links); n > 0 && links[n-1].src == a && links[n-1].dst == b {
				links[n-1].n += l.Relationships
				return nil
			}
			if len(links) >= maxLinks {
				return errTooManyLinks
			}
			links = append(links, link{src: a, dst: b, n: l.Relationships})
		case aok != bok:
			// One end is in scope. With a prefix that is a relationship
			// crossing it. Without one every file is in scope, so the other
			// end is a file the listing did not return (the index changed
			// between the two reads) and there is nothing true to count.
			if prefix != "" && l.Confidence.AtLeast(floor) {
				rep.CrossingPrefix += l.Relationships
			}
		}
		return nil
	})
	if errors.Is(err, errTooManyLinks) {
		rep.Truncated = true
		rep.TruncatedReason = fmt.Sprintf(
			"the file graph has more than %d file-to-file links, which is the most one partition reads", maxLinks)
		rep.Relationships, rep.CrossingPrefix, rep.BelowFloor = 0, 0, 0
		rep.Notes = append(rep.Notes, tooLargeNote(maxLinks, prefix), unseenNote(stats, rep.Unbound), Limits)
		return rep, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communities: %w", err)
	}
	rep.Links = len(links)

	groups, central, modularity, err := partition(ctx, paths, links, copts)
	if err != nil {
		return nil, err
	}
	rep.Central = central
	rep.Modularity = modularity

	linked := 0
	for _, g := range groups {
		linked += len(g.members)
		if len(g.members) < 2 {
			rep.Alone++
			continue
		}
		rep.Total++
		rep.Grouped += len(g.members)
	}
	rep.Unlinked = rep.Files - linked

	// Largest first, then by id: a total order, so which communities fall
	// inside the limit is the same on every call.
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].members) != len(groups[j].members) {
			return len(groups[i].members) > len(groups[j].members)
		}
		return groups[i].id < groups[j].id
	})
	for _, g := range groups {
		if len(g.members) < 2 {
			break // sorted by size: only groups of one remain
		}
		if len(rep.Communities) >= limit {
			rep.Omitted++
			rep.OmittedFiles += len(g.members)
			continue
		}
		c := Community{
			ID: g.id, Label: g.label, LabelMethod: g.method,
			Files: len(g.members), Internal: g.internal, External: g.external,
			Members: []Member{},
		}
		for _, m := range g.members {
			if len(c.Members) >= members {
				break
			}
			c.Members = append(c.Members, Member{Path: paths[m.file], Language: langs[m.file], ReferencedWithin: m.inbound})
		}
		c.MembersOmitted = c.Files - len(c.Members)
		c.Directories, c.DirectoriesOmitted = directories(g.members, paths)
		rep.Communities = append(rep.Communities, c)
	}

	rep.Notes = notes(rep, limit)
	return rep, nil
}

// group is one community before it is bounded for the report.
type group struct {
	id      string
	label   string
	method  string
	members []groupMember // most referenced first
	// internal and external count relationships, not links.
	internal int
	external int
}

type groupMember struct {
	file    int32
	inbound int
}

// partition runs the clustering over the linked files and summarises each
// cluster. Files with no link are not passed in: they are communities of one by
// definition, and at scale they are most of the nodes.
func partition(ctx context.Context, paths []string, links []link, copts cluster.Options) ([]group, []CentralFile, float64, error) {
	if len(links) == 0 {
		return nil, nil, 0, nil
	}
	// A link names two files, so there are at most twice as many linked files
	// as links, and never more than there are files.
	most := min(2*len(links), len(paths))
	nodes := make([]metrics.Node, 0, most)
	fileOf := make(map[string]int32, most)
	add := func(i int32) {
		if _, ok := fileOf[paths[i]]; ok {
			return
		}
		nodes = append(nodes, metrics.Node{Key: paths[i], Kind: model.KindFile})
		fileOf[paths[i]] = i
	}
	edges := make([]metrics.Edge, 0, len(links))
	for _, l := range links {
		add(l.src)
		add(l.dst)
		edges = append(edges, metrics.Edge{From: paths[l.src], To: paths[l.dst], Weight: l.n})
	}
	g := metrics.NewGraph(nodes, edges)

	assignments, err := cluster.Detect(ctx, g, copts)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("communities: %w", err)
	}

	pr, err := metrics.PageRank(ctx, g, metrics.DefaultPageRankOptions())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("communities: pagerank: %w", err)
	}
	central := make([]CentralFile, 0, min(len(pr), maxCentralFiles))
	for _, sc := range pr { // highest first
		if len(central) >= maxCentralFiles {
			break
		}
		central = append(central, CentralFile{Path: sc.Key, Score: sc.Value})
	}
	modularity := cluster.Modularity(g, assignments, 1)
	clusterOf := make(map[int32]string, len(assignments))
	for _, a := range assignments {
		clusterOf[fileOf[a.Key]] = a.ClusterID
	}

	// Per-cluster relationship counts and per-file inbound counts, from the
	// directed links. An external relationship is charged to both ends, since
	// it crosses both boundaries.
	internal := map[string]int{}
	external := map[string]int{}
	inbound := make(map[int32]int, len(assignments))
	for _, l := range links {
		a, b := clusterOf[l.src], clusterOf[l.dst]
		if a == b {
			internal[a] += l.n
			inbound[l.dst] += l.n
			continue
		}
		external[a] += l.n
		external[b] += l.n
	}

	// The label's rank is the same figure that orders the members, so a
	// community named after its top member is named after the file listed
	// first.
	ranks := make([]metrics.Score, 0, len(assignments))
	for _, a := range assignments {
		ranks = append(ranks, metrics.Score{Key: a.Key, Value: float64(inbound[fileOf[a.Key]])})
	}
	labels, err := cluster.Labels(ctx, g, assignments, ranks)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("communities: %w", err)
	}

	byID := make(map[string]*group, len(labels))
	out := make([]group, len(labels))
	for i, l := range labels {
		out[i] = group{
			id: l.ClusterID, label: l.Name, method: l.Method,
			internal: internal[l.ClusterID], external: external[l.ClusterID],
			members: make([]groupMember, 0, l.Members),
		}
		byID[l.ClusterID] = &out[i]
	}
	for _, a := range assignments { // key-ascending, so members start in path order
		f := fileOf[a.Key]
		grp := byID[a.ClusterID]
		grp.members = append(grp.members, groupMember{file: f, inbound: inbound[f]})
	}
	for i := range out {
		ms := out[i].members
		sort.SliceStable(ms, func(a, b int) bool { return ms[a].inbound > ms[b].inbound })
	}
	return out, central, modularity, nil
}

// directories counts a community's members by the directory holding them,
// fullest first and then by path, and reports how many were not listed.
func directories(members []groupMember, paths []string) ([]Directory, int) {
	counts := map[string]int{}
	for _, m := range members {
		counts[path.Dir(paths[m.file])]++
	}
	out := make([]Directory, 0, len(counts))
	for dir, n := range counts {
		out = append(out, Directory{Path: dir, Files: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Path < out[j].Path
	})
	omitted := 0
	if len(out) > directoriesListed {
		omitted = len(out) - directoriesListed
		out = out[:directoriesListed]
	}
	return out, omitted
}

// notes are the statements both front ends print, in the order a reader needs
// them: what the answer was computed without, what it left out, what it is.
func notes(rep *Report, limit int) []string {
	var out []string
	scope := ""
	if rep.PathPrefix != "" {
		scope = fmt.Sprintf(" under %q", rep.PathPrefix)
	}
	// "No relationship" is only true above the floor, so the floor is named
	// unless it admits everything.
	kind := "no relationship"
	if rep.MinConfidence != model.ConfidenceLow {
		kind = fmt.Sprintf("no relationship of %s confidence or better", rep.MinConfidence)
	}

	switch {
	case rep.Files == 0 && rep.PathPrefix != "":
		out = append(out, fmt.Sprintf("No indexed file is under %q, so there was nothing to group. "+
			"Check the prefix: it is matched from the start of the repository-relative path.", rep.PathPrefix))
	case rep.Files == 0:
		out = append(out, "The index holds no files, so there was nothing to group.")
	case rep.Links == 0:
		out = append(out, fmt.Sprintf("The index bound %s between two different files%s, so no "+
			"community can be reported. That describes the index, not the code: run 'graphlume resolve' "+
			"if references are still pending.", kind, scope))
	}

	out = append(out, unseenNote(rep.Resolution, rep.Unbound))

	if rep.Omitted > 0 {
		out = append(out, fmt.Sprintf("Showing the %d largest of %d communities. The %d not shown hold %d file(s); "+
			"raise the limit or narrow with a path to see them.", limit, rep.Total, rep.Omitted, rep.OmittedFiles))
	}
	if n := rep.Unlinked + rep.Alone; n > 0 && rep.Files > 0 {
		s := fmt.Sprintf("%d of %d file(s)%s are in no community: ", n, rep.Files, scope)
		if rep.Alone == 0 {
			s += fmt.Sprintf("the index bound %s between any of them and another file%s", kind, scope)
		} else {
			s += fmt.Sprintf("for %d the index bound %s with another file%s, and %d are linked but ended "+
				"in a group of one", rep.Unlinked, kind, scope, rep.Alone)
		}
		out = append(out, s+". Unlinked is not unused: documents, data, entry points and files reached only "+
			"through references the index could not bind are all here.")
	}
	if rep.BelowFloor > 0 {
		out = append(out, fmt.Sprintf("%d relationship(s) between these files are below %s confidence and were "+
			"left out. A low-confidence binding is mostly a match on a name alone, and one wrong one can "+
			"merge unrelated groups; lower the minimum confidence to count them anyway.",
			rep.BelowFloor, rep.MinConfidence))
	}
	if rep.CrossingPrefix > 0 {
		out = append(out, fmt.Sprintf("%d relationship(s) that would have counted connect a file under %q to one "+
			"outside it. They were not followed, so a community here may be part of a larger one.",
			rep.CrossingPrefix, rep.PathPrefix))
	}
	return append(out, Limits)
}

// unseenNote states how much of the reference graph the partition never saw.
func unseenNote(rs registry.ResolutionStats, unbound int64) string {
	if rs.Total == 0 {
		return "This index recorded no references at all, so how much is missing from the graph is unknown " +
			"rather than nothing."
	}
	s := fmt.Sprintf("What the index could not see: %d of its %d reference(s) are bound to nothing "+
		"(%d matched no definition here, mostly library calls; %d were too ambiguous to choose between; "+
		"%d are still pending). Each one that is really a use of code in this repository is a link "+
		"these communities were computed without.", unbound, rs.Total, rs.Unresolved, rs.Ambiguous, rs.Pending)
	if rs.Pending > 0 {
		s += " Run 'graphlume resolve' to bind the pending ones."
	}
	return s
}

func tooLargeNote(maxLinks int, prefix string) string {
	s := fmt.Sprintf("No community is reported because the file graph is too large to partition in one "+
		"request: it has more than %d file-to-file links. This is a bound, not a finding. ", maxLinks)
	if prefix == "" {
		return s + "Pass a path to partition one part of the repository."
	}
	return s + fmt.Sprintf("Pass a narrower path than %q.", prefix)
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
