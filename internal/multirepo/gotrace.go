package multirepo

import (
	"context"
	"fmt"
	"sort"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Callers and impact across an estate: the single-repository questions, asked
// of a symbol in one member, with Go-module links as the hop between members.
//
// Rows that never leave one member carry that member's own recorded
// confidence, which may be `exact` when a compiler proved the edge inside
// that repository. A row whose path crosses a go-module link carries the
// weakest confidence on its path, and that is at most `high`, because the
// link itself is: no compiler checked the two repositories together.

// LegLocal is the leg name of a row read from a member's own graph.
const LegLocal = "local"

// Bounds for the estate-wide traversals.
const (
	DefaultSystemCallersLimit = 200
	DefaultSystemImpactDepth  = 3
	DefaultSystemImpactRows   = 500
	// impactFanIn bounds the incoming edges read per node. A hub with more
	// callers than this is counted as truncated rather than loaded whole.
	impactFanIn = 200
)

// SymbolRef names one declaration in one member: the subject of a system
// callers or impact question.
type SymbolRef struct {
	Member    string `json:"member"`
	ID        int64  `json:"-"`
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// SystemCaller is one caller of the subject, in any member.
type SystemCaller struct {
	Member     string           `json:"member"`
	Caller     string           `json:"caller"`
	File       string           `json:"file"`
	Line       int              `json:"line"`
	Confidence model.Confidence `json:"confidence"`
	// Leg is LegLocal for an edge from the member's own graph and
	// LegGoModule for a stitched call from another member.
	Leg string `json:"leg"`
	// Source is the recorded source of a local edge (compiler, source_code);
	// empty on a stitched row, whose legs are in the system report.
	Source string `json:"source,omitempty"`
	// Call is the call as written, on a stitched row.
	Call string `json:"call,omitempty"`
}

// SystemCallersReport answers "who calls this, in any member".
type SystemCallersReport struct {
	Target  SymbolRef      `json:"target"`
	Callers []SystemCaller `json:"callers"`
	// Truncated counts rows past the limit, and TruncationNote says when that
	// count is itself a floor.
	Truncated      int    `json:"truncated,omitempty"`
	TruncationNote string `json:"truncation_note,omitempty"`
	// Warnings are the version warnings of every importer/provider pair a
	// stitched row came through, so a caller row is never read without them.
	Warnings []string `json:"warnings"`
	// UnavailableMembers could contribute no callers.
	UnavailableMembers []string `json:"unavailable_members"`
	Caveats            []string `json:"caveats"`
}

// Complete reports whether every member was read.
func (r *SystemCallersReport) Complete() bool { return len(r.UnavailableMembers) == 0 }

// SystemImpactRow is one dependent of the subject, at some depth, in some
// member.
type SystemImpactRow struct {
	Member string `json:"member"`
	Symbol string `json:"symbol"`
	Kind   string `json:"kind,omitempty"`
	// File and Line are the evidence of the hop that reached this symbol: the
	// call site, in this row's member.
	File  string `json:"file"`
	Line  int    `json:"line"`
	Depth int    `json:"depth"`
	// Confidence is the weakest on the path from the subject to here.
	Confidence model.Confidence `json:"confidence"`
	// Via is the edge kind of the last hop (CALLS, REFERENCES, ...) or
	// LegGoModule for a hop across a repository boundary.
	Via string `json:"via"`
	// Reaches is the symbol this row depends on, as member:symbol.
	Reaches string `json:"reaches"`
}

// SystemImpactReport is the cross-repository blast radius of one symbol.
type SystemImpactReport struct {
	Target        SymbolRef         `json:"target"`
	MaxDepth      int               `json:"max_depth"`
	MinConfidence model.Confidence  `json:"min_confidence,omitempty"`
	Rows          []SystemImpactRow `json:"dependents"`
	// Members lists every member the radius reaches, the subject's own first.
	Members []string `json:"members"`
	// Truncated counts dependents not listed because a bound was hit, and
	// TruncationNote says which bound.
	Truncated          int      `json:"truncated,omitempty"`
	TruncationNote     string   `json:"truncation_note,omitempty"`
	Warnings           []string `json:"warnings"`
	UnavailableMembers []string `json:"unavailable_members"`
	Caveats            []string `json:"caveats"`
}

// Complete reports whether every member was read.
func (r *SystemImpactReport) Complete() bool { return len(r.UnavailableMembers) == 0 }

// ImpactOptions bounds one estate-wide impact walk. Kinds and MinConfidence
// come from the caller so the walk inside each member uses the same edge
// kinds and floor as the single-repository impact command.
type ImpactOptions struct {
	MaxDepth      int
	MaxRows       int
	Kinds         []model.EdgeKind
	MinConfidence model.Confidence
}

// estate is the opened, stitched view the two traversals share.
type estate struct {
	byName      map[string]*goMember
	st          *goStitch
	unavailable []string
}

func (f *Federator) openEstate(ctx context.Context, members []Member) (*estate, error) {
	facts, err := loadMembersWith(ctx, members, false)
	if err != nil {
		return nil, err
	}
	st, err := f.stitchGo(ctx, facts)
	if err != nil {
		return nil, err
	}
	e := &estate{byName: map[string]*goMember{}, st: st, unavailable: []string{}}
	for _, mf := range facts {
		if !mf.member.Available() {
			e.unavailable = append(e.unavailable, mf.member.Name)
			continue
		}
		src, ok := mf.member.Source.(GoModuleSource)
		if !ok {
			continue
		}
		e.byName[mf.member.Name] = &goMember{name: mf.member.Name, src: src, runID: mf.member.RunID}
	}
	return e, nil
}

// pairWarnings are the warnings of the pairs the given stitched links came
// through, deduplicated and sorted.
func (e *estate) pairWarnings(links []GoLink) []string {
	seen := map[string]bool{}
	for _, l := range links {
		if p, ok := e.st.pairs[pairKey{l.FromMember, l.ToMember, l.Module}]; ok {
			for _, w := range p.Warnings {
				seen[w] = true
			}
		}
	}
	return sortedKeys(seen)
}

func (e *estate) linksInto(member string, id int64) []GoLink {
	idx := e.st.byTarget[symKey{member, id}]
	out := make([]GoLink, 0, len(idx))
	for _, i := range idx {
		out = append(out, e.st.links[i])
	}
	return out
}

// SystemCallers lists the callers of target in its own member and, through
// Go-module links, in every other member.
func (f *Federator) SystemCallers(ctx context.Context, members []Member, target SymbolRef, limit int) (*SystemCallersReport, error) {
	if limit <= 0 {
		limit = DefaultSystemCallersLimit
	}
	e, err := f.openEstate(ctx, members)
	if err != nil {
		return nil, err
	}
	own, ok := e.byName[target.Member]
	if !ok {
		return nil, fmt.Errorf("multirepo: member %q is not readable, so its callers cannot be listed", target.Member)
	}

	rep := &SystemCallersReport{
		Target: target, Callers: []SystemCaller{},
		UnavailableMembers: e.unavailable, Caveats: systemTraceCaveats(),
	}

	// One more than the limit, so a full page can say there was more.
	edges, err := own.src.IncomingEdges(ctx, own.runID, target.ID, limit+1,
		registry.EdgeFilter{Kinds: []string{string(model.EdgeCalls), string(model.EdgeInstantiates)}})
	if err != nil {
		return nil, fmt.Errorf("multirepo: member %q: read callers of %s: %w", own.name, target.Qualified, err)
	}
	var rows []SystemCaller
	for _, ed := range edges {
		rows = append(rows, SystemCaller{
			Member: own.name, Caller: nonEmpty(ed.SrcQualified, ed.SrcName),
			File: ed.EvidenceFile, Line: ed.EvidenceLine,
			Confidence: model.Confidence(ed.Confidence), Leg: LegLocal, Source: ed.Source,
		})
	}
	stitched := e.linksInto(target.Member, target.ID)
	for _, l := range stitched {
		rows = append(rows, SystemCaller{
			Member: l.FromMember, Caller: l.FromSymbol, File: l.FromFile, Line: l.FromLine,
			Confidence: l.Confidence, Leg: LegGoModule, Call: l.Call,
		})
	}
	rows = dedupeCallers(rows)
	sortCallers(rows, target.Member)
	if len(rows) > limit {
		rep.Truncated = len(rows) - limit
		rows = rows[:limit]
		if len(edges) > limit {
			// The local read stopped at limit+1, so the count cut is a floor.
			rep.TruncationNote = fmt.Sprintf("%s has more than %d callers of its own; "+
				"at least %d row(s) were cut (raise --limit)", own.name, limit, rep.Truncated)
		}
	}
	rep.Callers = rows
	rep.Warnings = e.pairWarnings(stitched)
	if rep.Warnings == nil {
		rep.Warnings = []string{}
	}
	return rep, nil
}

// SystemImpact walks inward from target across the estate: dependents inside
// its own member, callers in other members through Go-module links, and their
// dependents inside their own members, up to MaxDepth hops.
func (f *Federator) SystemImpact(ctx context.Context, members []Member, target SymbolRef, opts ImpactOptions) (*SystemImpactReport, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultSystemImpactDepth
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = DefaultSystemImpactRows
	}
	e, err := f.openEstate(ctx, members)
	if err != nil {
		return nil, err
	}
	if _, ok := e.byName[target.Member]; !ok {
		return nil, fmt.Errorf("multirepo: member %q is not readable, so its impact cannot be walked", target.Member)
	}

	rep := &SystemImpactReport{
		Target: target, MaxDepth: opts.MaxDepth, MinConfidence: opts.MinConfidence,
		Rows: []SystemImpactRow{}, UnavailableMembers: e.unavailable, Caveats: systemTraceCaveats(),
	}
	filter := registry.EdgeFilter{MinConfidence: opts.MinConfidence}
	for _, k := range opts.Kinds {
		filter.Kinds = append(filter.Kinds, string(k))
	}

	type node struct {
		key  symKey
		name string
		conf model.Confidence
	}
	visited := map[symKey]bool{{target.Member, target.ID}: true}
	reached := map[string]bool{target.Member: true}
	frontier := []node{{key: symKey{target.Member, target.ID}, name: target.Qualified, conf: model.ConfidenceExact}}
	var crossed []GoLink
	full := false

	for depth := 1; depth <= opts.MaxDepth && len(frontier) > 0 && !full; depth++ {
		var next []node
		var level []SystemImpactRow
		levelKeys := map[symKey]bool{}
		for _, n := range frontier {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			gm := e.byName[n.key.member]
			reachesName := n.key.member + ":" + n.name

			if gm != nil {
				edges, err := gm.src.IncomingEdges(ctx, gm.runID, n.key.id, impactFanIn+1, filter)
				if err != nil {
					return nil, fmt.Errorf("multirepo: member %q: read dependents: %w", gm.name, err)
				}
				if len(edges) > impactFanIn {
					edges = edges[:impactFanIn]
					rep.Truncated++
					rep.TruncationNote = fmt.Sprintf("a symbol has more than %d direct dependents; "+
						"only the first %d of each such symbol were followed", impactFanIn, impactFanIn)
				}
				for _, ed := range edges {
					k := symKey{n.key.member, ed.SrcID}
					if visited[k] || levelKeys[k] {
						continue
					}
					levelKeys[k] = true
					c := weakest(n.conf, model.Confidence(ed.Confidence))
					name := nonEmpty(ed.SrcQualified, ed.SrcName)
					level = append(level, SystemImpactRow{
						Member: n.key.member, Symbol: name,
						File: ed.EvidenceFile, Line: ed.EvidenceLine, Depth: depth,
						Confidence: c, Via: ed.Kind, Reaches: reachesName,
					})
					next = append(next, node{key: k, name: name, conf: c})
				}
			}

			for _, l := range e.linksInto(n.key.member, n.key.id) {
				c := capHeuristic(weakest(n.conf, l.Confidence))
				if !c.AtLeast(opts.MinConfidence) {
					continue
				}
				k := symKey{l.FromMember, l.FromSymbolID}
				if l.FromSymbolID != 0 && (visited[k] || levelKeys[k]) {
					continue
				}
				if l.FromSymbolID != 0 {
					levelKeys[k] = true
				}
				crossed = append(crossed, l)
				level = append(level, SystemImpactRow{
					Member: l.FromMember, Symbol: l.FromSymbol,
					File: l.FromFile, Line: l.FromLine, Depth: depth,
					Confidence: c, Via: LegGoModule, Reaches: reachesName,
				})
				if l.FromSymbolID != 0 {
					// A call at the top level of a file has no symbol to
					// walk on from; it is listed and the walk stops there.
					next = append(next, node{key: k, name: l.FromSymbol, conf: c})
				}
			}
		}

		sortImpactRows(level)
		for _, r := range level {
			if len(rep.Rows) >= opts.MaxRows {
				rep.Truncated++
				full = true
				continue
			}
			rep.Rows = append(rep.Rows, r)
			reached[r.Member] = true
		}
		if full {
			rep.TruncationNote = fmt.Sprintf("the walk stopped at %d dependents (--limit); "+
				"raise it to see the rest", opts.MaxRows)
		}
		for k := range levelKeys {
			visited[k] = true
		}
		sort.Slice(next, func(i, j int) bool {
			if next[i].key.member != next[j].key.member {
				return next[i].key.member < next[j].key.member
			}
			return next[i].name < next[j].name
		})
		frontier = next
	}
	if len(frontier) > 0 && !full {
		// Nodes were reached at the last allowed depth and not expanded.
		rep.TruncationNote = nonEmpty(rep.TruncationNote, fmt.Sprintf(
			"the walk stopped at depth %d, so what depends on the symbols at that depth, if anything, "+
				"is not listed (raise --depth)",
			opts.MaxDepth))
	}

	rep.Members = []string{target.Member}
	others := make([]string, 0, len(reached))
	for m := range reached {
		if m != target.Member {
			others = append(others, m)
		}
	}
	sort.Strings(others)
	rep.Members = append(rep.Members, others...)
	rep.Warnings = e.pairWarnings(crossed)
	if rep.Warnings == nil {
		rep.Warnings = []string{}
	}
	return rep, nil
}

// weakest is the weaker of two confidences, without the heuristic ceiling: a
// path that never leaves one member keeps that member's own grade.
func weakest(a, b model.Confidence) model.Confidence {
	if !b.Valid() {
		return a
	}
	if !a.Valid() || b.Rank() < a.Rank() {
		return b
	}
	return a
}

func nonEmpty(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func dedupeCallers(rows []SystemCaller) []SystemCaller {
	type k struct {
		member, caller, file string
		line                 int
	}
	seen := map[k]bool{}
	out := rows[:0]
	for _, r := range rows {
		key := k{r.Member, r.Caller, r.File, r.Line}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

// sortCallers orders the subject's own member first, then the others by
// name, and within a member by file and line.
func sortCallers(rows []SystemCaller, own string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.Member == own) != (b.Member == own) {
			return a.Member == own
		}
		switch {
		case a.Member != b.Member:
			return a.Member < b.Member
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		default:
			return a.Caller < b.Caller
		}
	})
}

func sortImpactRows(rows []SystemImpactRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case a.Member != b.Member:
			return a.Member < b.Member
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		default:
			return a.Symbol < b.Symbol
		}
	})
}

// systemTraceCaveats are the reading rules of the callers and impact output.
func systemTraceCaveats() []string {
	return []string{
		"This is a LOWER BOUND. A caller the index did not resolve, a method call whose " +
			"receiver type could not be read (counted by reason above), and any repository " +
			"outside the estate are all absent, so an empty answer is \"nothing found\", never \"nothing breaks\".",
		"Rows marked go-module cross a repository boundary on a Go import and are never " +
			"`exact`: no compiler checked the two repositories together. Rows marked local carry " +
			"the member's own recorded confidence.",
	}
}
