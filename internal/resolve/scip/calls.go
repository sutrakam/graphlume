package scip

import (
	"fmt"
	"sort"

	"github.com/sutrakam/graphlume/pkg/model"
)

// CALLS FROM TWO INDEPENDENT SOURCES
//
// This file is the one place in GraphLume that produces a CALLS edge at
// confidence=exact, and it is worth being precise about why that is allowed
// here and nowhere else. See edgeKind in kinds.go for the objection this
// construction answers.
//
// A CALLS edge asserts two things at once:
//
//	IS THIS OCCURRENCE A CALL?   answered by GraphLume's own tree-sitter parser,
//	                             which recorded a `reference.call` at this file
//	                             and line. A grammar recognising a call
//	                             expression is a syntactic fact about the source
//	                             text, not an inference about a symbol.
//
//	WHAT DOES IT RESOLVE TO?     answered by the compiler, via the SCIP
//	                             occurrence at that same file and line.
//
// Neither half is guessed, and neither half is derived from the other. What is
// new is only the join. Deriving call-ness from the SCIP side alone — "the
// target is a method, so this must be a call" — remains forbidden, because that
// IS an inference, and it is the thing kinds.go warns about.
//
// THE JOIN IS THE SAME ONE `graphlume accuracy` ALREADY TREATS AS GROUND TRUTH.
// internal/cli/accuracy.go adjudicates the heuristic resolver's precision and
// recall against exactly this intersection: lines the parser recorded a call
// on, where the compiler resolved that line to EXACTLY ONE target. We already
// trust the construction enough to judge our own edges by it. PromoteCalls
// reuses it to create them, and mirrors accuracyCTE predicate for predicate so
// the two cannot drift apart:
//
//   - `unambiguous`: group the file's compiler edges by evidence line, keep
//     only lines with COUNT(DISTINCT dst_id) = 1.
//   - `truth`:       of those, keep the lines the parser recorded a call on.
//
// AND A SECOND: the target must not be a RECEIVER on that line. For
//
//	await svc.delete_line(...)
//
// the parser writes TWO reference.call rows at the same column — `delete_line`
// with receiver `svc`, and a bare `svc` — because its call query captures the
// callee expression and an attribute access has a base. The bare row passes the
// name check on its own and yields "CALLS svc", which is false: `svc` is not
// called, it is what the call was made against. Measured on an 8,500-file
// repository this was 4,784 of 35,226 promotions, 13.6%.
//
// Declining them is not this package overruling the parser. The parser has
// contradicted ITSELF — one row says a call named `svc` happens here, another
// says the call here is `delete_line` ON `svc` — and the second row is strictly
// more specific than the first. Preferring the record that names both the
// callee and the receiver over the one that names neither is reading the two
// rows together, which is the only way they can both be true. What is NOT
// available is stamping the contradiction `exact`: to a user reading a blast
// radius, exact means a compiler proved it, and no compiler proved that `svc`
// is called.
//
// AND ONE CONSTRAINT THE ADJUDICATOR DOES NOT NEED: the name must agree. The
// parser records WHAT each call is written as; a promoted edge is kept only
// where the compiler's single target on that line is a symbol GraphLume indexed
// under exactly that name. accuracy.go can do without this because it scores a
// resolver that already had to pick a name; a producer cannot. Measured on an
// 8,500-file repository, dropping the check admitted 11,459 edges of which a
// hand-read sample was almost entirely wrong — `select(Device)` attributed to
// a call, `pytest.raises(AccountLockedError)` attributed to a call, and worst,
//
//	devices: Mapped[list["Device"]] = relationship(
//
// where the parser's call is `relationship`, the compiler's only in-repository
// target on the line is the type `Device`, and the line survives the
// unambiguity test purely because everything else on it resolves to a library.
// That is a type annotation stamped CALLS at exact. The name check rejects all
// three, and it is not a fourth source of truth: it is the parser's own record
// of what was called, held against GraphLume's own record of what the target is
// named, with the compiler supplying only the link between them.
//
// THE JOIN IS PER CALL, NOT PER LINE. The join does not demand that the compiler
// resolve exactly ONE target on the whole line, as a cruder rule would on the reasoning that on
//
//	total = compute(order.subtotal)
//
// the parser records one call and the compiler resolves two symbols, `compute`
// and the field `order.subtotal`, and promoting both would stamp a field read
// as a call. The guard against that is the NAME check, which already rejects
// the field: the parser's call is written `compute`, and `subtotal` is not
// named as any call on the line. A one-target-per-line rule would be a second,
// cruder guard for the same case, and it would throw away the common one:
//
//	if err := ruleModel().Validate(); err != nil {
//
// is two calls and two compiler targets, and neither would be promoted (6,439 lines
// of this repository), so `callers` would list 8 of 11 callers of a method
// and say nothing was missing.
//
// So the unit of decision is (line, name): the compiler's occurrences of that
// name on that line against the parser's calls written as that name. They must
// pair off one to one, by position when the name occurs more than once (the
// parser's column is where the call expression starts, at or before the
// identifier the compiler resolved). A method value beside a call of the same
// name, or a call the compiler resolved outside the repository beside one it
// resolved inside, cannot be paired and is declined, which costs recall in the
// direction this system chooses to be wrong in. Every decline is counted.

// CallSites reports whether GraphLume's parser recognised a call expression at a
// source position.
//
// Declared here, at the consumer, rather than in the registry that will satisfy
// it: this package performs no database access by design, and the whole point
// of the construction is that the call-ness fact arrives from somewhere other
// than the SCIP index. One method, so a test can implement it in a line.
//
// path is repo-relative with forward slashes, as FileFacts.Path is. line is
// 1-indexed, as model.Range is — the same space symbol_references.start_line
// and edges.evidence_line are compared in.
type CallSites interface {
	// HasCall reports whether any call was parsed on this line. It decides
	// whether a line is in the population at all, which is what makes a
	// declined line reportable rather than invisible.
	HasCall(path string, line int) bool
	// IsCallTo reports whether one of the calls parsed on this line is written
	// as name.
	IsCallTo(path string, line int, name string) bool
	// IsReceiver reports whether name is what some call on this line was
	// written AGAINST, rather than what was called. See PromoteCalls.
	IsReceiver(path string, line int, name string) bool
	// CallColumns lists, ascending and without repeats, the columns at which a
	// call written as name starts on this line. A column of -1 means the
	// parser's column was not recorded.
	CallColumns(path string, line int, name string) []int
}

// callSite is one parser-recorded call position.
type callSite struct {
	path string
	line int
}

// namedCallSite is one parser-recorded call position and the name it is
// written as.
type namedCallSite struct {
	path string
	line int
	name string
}

// CallSiteSet is an in-memory CallSites.
//
// A real repository has a few hundred thousand parsed call sites, so this is
// tens of megabytes and held for the duration of one import — cheaper than a
// query per occurrence by three orders of magnitude, and the alternative
// (streaming both sides in path order) would couple the ingester's document
// order to the registry's, which SCIP does not guarantee.
type CallSiteSet struct {
	lines     map[callSite]struct{}
	named     map[namedCallSite]struct{}
	receivers map[namedCallSite]struct{}
	cols      map[namedCallSite][]int
}

// NewCallSiteSet returns an empty set sized for n entries.
func NewCallSiteSet(n int) *CallSiteSet {
	if n < 0 {
		n = 0
	}
	return &CallSiteSet{
		lines:     make(map[callSite]struct{}, n),
		named:     make(map[namedCallSite]struct{}, n),
		receivers: make(map[namedCallSite]struct{}, n),
		cols:      make(map[namedCallSite][]int, n),
	}
}

// Add records that a call written as name was parsed at path:line. Lines at or
// below zero are ignored: the parser writes 0 for a reference whose position it
// could not place, and a position-free call site cannot be joined to anything.
func (s *CallSiteSet) Add(path string, line int, name, receiver string) {
	s.AddAt(path, line, -1, name, receiver)
}

// AddAt is Add with the column at which the call expression starts, or -1 when
// the column is not known.
func (s *CallSiteSet) AddAt(path string, line, col int, name, receiver string) {
	if s == nil || line <= 0 {
		return
	}
	s.lines[callSite{path: path, line: line}] = struct{}{}
	if name != "" {
		k := namedCallSite{path: path, line: line, name: name}
		s.named[k] = struct{}{}
		cs := s.cols[k]
		i := sort.SearchInts(cs, col)
		if i == len(cs) || cs[i] != col {
			cs = append(cs, 0)
			copy(cs[i+1:], cs[i:])
			cs[i] = col
			s.cols[k] = cs
		}
	}
	if receiver != "" {
		s.receivers[namedCallSite{path: path, line: line, name: receiver}] = struct{}{}
	}
}

// HasCall implements CallSites.
func (s *CallSiteSet) HasCall(path string, line int) bool {
	if s == nil {
		return false
	}
	_, ok := s.lines[callSite{path: path, line: line}]
	return ok
}

// IsCallTo implements CallSites.
func (s *CallSiteSet) IsCallTo(path string, line int, name string) bool {
	if s == nil || name == "" {
		return false
	}
	_, ok := s.named[namedCallSite{path: path, line: line, name: name}]
	return ok
}

// IsReceiver implements CallSites.
func (s *CallSiteSet) IsReceiver(path string, line int, name string) bool {
	if s == nil || name == "" {
		return false
	}
	_, ok := s.receivers[namedCallSite{path: path, line: line, name: name}]
	return ok
}

// CallColumns implements CallSites.
func (s *CallSiteSet) CallColumns(path string, line int, name string) []int {
	if s == nil || name == "" {
		return nil
	}
	return s.cols[namedCallSite{path: path, line: line, name: name}]
}

// Len is how many distinct positions the set holds. Positions, not names: it
// answers "how much of the repository has a call parsed in it", which is the
// figure that explains a promotion count of zero.
func (s *CallSiteSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.lines)
}

// PromoteStats reports one call to PromoteCalls.
//
// Ambiguous and Unpromoted are the two reasons a compiler-proven reference
// stayed a REFERENCES edge. They are separated because they have different
// meanings and different fixes: Ambiguous is recall this construction
// deliberately declines to claim, while the bulk of Unpromoted is simply
// correct — most references are not calls.
type PromoteStats struct {
	// Promoted is REFERENCES edges rewritten to CALLS.
	Promoted int
	// CallLines is parser-recorded call lines that carried at least one
	// compiler edge — the population the promotion was decided over.
	CallLines int
	// Ambiguous is calls left alone because the compiler's occurrences of a
	// name on a call line could not be paired one to one with the calls the
	// parser recorded under that name: a name the compiler resolved more often
	// (a method value beside a call), or fewer times, than the parser saw it
	// called.
	Ambiguous int
	// Receivers is call lines left alone because the compiler's single target
	// there is what a call on that line was written AGAINST, not what was
	// called. See PromoteCalls.
	Receivers int
	// NameMismatch is call lines left alone because the compiler's single
	// target on the line is not named as any call the parser recorded there.
	// It is the count that separates "the compiler resolved the call" from
	// "the compiler resolved SOMETHING on the same line as a call" — and the
	// second, left unchecked, is where a type annotation becomes a call.
	NameMismatch int
	// Unpromoted is compiler REFERENCES edges left as REFERENCES — the parser
	// recorded no call at that line, or the line was ambiguous. Reported so
	// "how much of the compiler's output became calls" is a ratio the reader
	// can compute rather than a number they have to trust.
	Unpromoted int
}

// Add accumulates s2 into s.
func (s *PromoteStats) Add(s2 PromoteStats) {
	s.Promoted += s2.Promoted
	s.CallLines += s2.CallLines
	s.Ambiguous += s2.Ambiguous
	s.NameMismatch += s2.NameMismatch
	s.Receivers += s2.Receivers
	s.Unpromoted += s2.Unpromoted
}

// PromoteCalls rewrites the REFERENCES edges of one file into CALLS edges
// wherever the parser recorded a call at the same line and the compiler
// resolved that line to exactly one target.
//
// edges must be one file's compiler edges, already resolved to rowids —
// FileFacts.Edges output for a single FileFacts, which is what makes
// "COUNT(DISTINCT dst_id) = 1" computable here without a database. Edges whose
// destination could not be resolved to an indexed symbol never reached this
// slice, which is deliberate and matches the adjudicator: a line's ambiguity is
// judged over the targets that exist in GraphLume's graph, exactly as
// accuracyCTE judges it over the rows in the edges table.
//
// It mutates edges in place and returns what it did. Only Kind changes:
// Confidence and Evidence.Source were set by Relation.Edge, which remains the
// single constructor for a model.Edge in this package, and every rewritten edge
// is re-validated so a kind the model does not accept fails here rather than at
// the SQL CHECK.
//
// calls may be nil, in which case nothing is promoted and the counts describe
// what was available. That is the pre-existing behaviour, kept reachable so an
// import with no parser facts to join against degrades to REFERENCES-only
// rather than failing.
//
// DETERMINISM: the decision for an edge depends only on its own line's target
// set and on calls, never on iteration order or on how many workers ran. The
// grouping map is only ever counted, never ranged over for a winner.
func PromoteCalls(
	path string,
	edges []model.Edge,
	names map[int64]string,
	calls CallSites,
) (PromoteStats, error) {
	var st PromoteStats

	// Pass 1: group the file's compiler references by (line, target name). The
	// grouping key is the name because the parser records WHAT each call is
	// written as, so "the calls named Validate on this line" is a question both
	// sides can answer from their own facts.
	type lineName struct {
		line int
		name string
	}
	groups := make(map[lineName][]int, len(edges))
	for i := range edges {
		if edges[i].Kind != model.EdgeReferences {
			// IMPORTS and IMPLEMENTS are already specific claims the index
			// stated directly; a call site coinciding with one does not make
			// them calls.
			continue
		}
		line := edges[i].Evidence.Range.Start.Line
		if line <= 0 {
			continue
		}
		k := lineName{line: line, name: names[edges[i].DstID]}
		groups[k] = append(groups[k], i)
	}

	// Pass 2: decide each group. Visited in sorted order purely so the counting
	// is reproducible under -race and readable in a failure message; the
	// verdict for a group depends only on that group and on calls.
	keys := make([]lineName, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].line != keys[j].line {
			return keys[i].line < keys[j].line
		}
		return keys[i].name < keys[j].name
	})

	promote := make(map[int]bool, len(edges))
	lastLine := 0
	for _, k := range keys {
		if calls == nil || !calls.HasCall(path, k.line) {
			continue
		}
		if k.line != lastLine {
			st.CallLines++
			lastLine = k.line
		}
		callCols := calls.CallColumns(path, k.line, k.name)
		if len(callCols) == 0 {
			st.NameMismatch++
			continue
		}
		if calls.IsReceiver(path, k.line, k.name) {
			st.Receivers++
			continue
		}
		idx := groups[k]
		if !pairsOneToOne(edges, idx, callCols) {
			st.Ambiguous++
			continue
		}
		for _, i := range idx {
			promote[i] = true
		}
	}

	for i := range edges {
		if edges[i].Kind != model.EdgeReferences {
			continue
		}
		if !promote[i] {
			st.Unpromoted++
			continue
		}
		edges[i].Kind = model.EdgeCalls
		if err := edges[i].Validate(); err != nil {
			return st, fmt.Errorf("scip: promote call at %s:%d: %w",
				path, edges[i].Evidence.Range.Start.Line, err)
		}
		st.Promoted++
	}

	return st, nil
}

// pairsOneToOne reports whether the compiler's occurrences of one name on one
// line (edges idx) and the parser's calls of that name (their start columns)
// pair off exactly: each call has one occurrence and each occurrence is one
// call.
//
// It is the join the line-level rule could not make. On
//
//	if err := ruleModel().Validate(); err != nil {
//
// the compiler resolves two targets and the parser records two calls, and a
// rule that demands ONE target per line promotes neither. Here `ruleModel` and
// `Validate` are separate groups, each with one occurrence and one call, and
// each is promoted on its own.
//
// Equal counts are necessary but not sufficient when a name occurs more than
// once, so occurrences are also matched to calls by position: a call starts at
// the beginning of its callee expression, at or before the identifier the
// compiler resolved. Each occurrence takes the nearest call at or before it,
// and the pairing must be a bijection. Anything else (a method value
// `f(x.Foo)` beside the call `Foo(...)`, a call the compiler resolved outside
// the repository) is declined as ambiguous, which costs recall and never
// precision.
func pairsOneToOne(edges []model.Edge, idx []int, callCols []int) bool {
	occ := make([]int, 0, len(idx))
	targetAt := make(map[int]int64, len(idx))
	for _, i := range idx {
		c := edges[i].Evidence.Range.Start.Col
		if prev, ok := targetAt[c]; ok && prev != edges[i].DstID {
			return false // two targets on one occurrence: nothing to pair
		}
		if _, ok := targetAt[c]; !ok {
			occ = append(occ, c)
		}
		targetAt[c] = edges[i].DstID
	}
	if len(occ) != len(callCols) {
		return false
	}
	if len(occ) == 1 {
		return true
	}
	sort.Ints(occ)
	taken := make(map[int]bool, len(occ))
	for _, c := range occ {
		j := sort.SearchInts(callCols, c+1) - 1 // nearest call at or before c
		if j < 0 || callCols[j] < 0 || taken[j] {
			return false
		}
		taken[j] = true
	}
	return true
}
