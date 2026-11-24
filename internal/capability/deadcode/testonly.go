package deadcode

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/registry"
)

// This file holds the second finding: definitions that DO have callers, every
// one of them in a test file.
//
// # Why it is a separate finding and not a third certainty
//
// "Nothing calls this" and "only tests call this" are different claims with
// different remedies. The first says the code is unreachable; the second says
// it is reachable and exercised, by code that does not ship. Deleting the
// second kind also means deleting tests, and whether that is right depends on
// something no index holds: whether the definition is finished work that lost
// its last production caller, a helper written for the tests, or an API
// awaiting its first user. So the rows are always `possible`, they are listed
// in their own section, and they are counted in their own totals. Nothing here
// changes Report.Likely, Report.Possible or Report.Candidates.
//
// # Why it is opt-in
//
// It costs a second pass: where the unreferenced scan asks one question of
// each definition ("does anything point at you?"), this one has to look at who
// is pointing. And its answer is longer and softer than the first. A report
// that led with it would bury the definitions nothing calls at all.
//
// # What the graph cannot see, and the text check that covers it
//
// The measured failure of a graph-only dead-code list was a function passed as
// a VALUE: registered with a router, stored in a table, handed to a framework.
// That leaves no call edge. Here it is worse, not better, because the tests of
// such a definition usually use it directly: the index then holds test
// referrers and no production one, which is exactly this finding's shape.
//
// So a row is confirmed against the indexed text before it is offered as the
// finding: its name must not occur in any file that is not a test, outside
// its own definition. A row that fails is kept, after the confirmed ones and
// carrying ReasonMentioned, for the same reason verifyAgainstText demotes
// rather than removes: the mention may be an unrelated symbol with the same
// name.
//
// Both halves of that were measured on GraphLume's own source. Of the nine
// unexported definitions the graph offered, the five the text confirmed were
// all used from tests alone. Of the four it did not, one was a struct that
// production code uses as a type and in composite literals, which the graph
// has no edge for and which the row was right to doubt; the other three were
// used from tests alone, and were doubted because their names (`stats`, `empty`)
// are ordinary words that occur elsewhere. Demoted is right for both kinds;
// removed would have been wrong for three of the four.

// TestOnlySource is the read surface the test-only pass adds to Source.
//
// Separate from Source because it is needed only when the pass is asked for:
// a caller that never asks does not have to provide it, and a test of the
// ordinary scan does not have to fake it.
type TestOnlySource interface {
	// FilePaths returns the path of every file in the run by file id. The
	// pass turns capability.IsTestPath into a set of ids with it.
	FilePaths(ctx context.Context, runID int64) (map[int64]string, error)
	// SymbolsReferencedOnlyFrom returns the definitions outside fileIDs whose
	// every incoming relationship starts inside fileIDs, examining at most
	// window symbols after afterID. An Examined of zero ends the scan.
	SymbolsReferencedOnlyFrom(ctx context.Context, runID, afterID int64, window int, fileIDs []int64) (registry.ConfinedPage, error)
}

var _ TestOnlySource = (*registry.Registry)(nil)

// TestOnlyReport is the answer to "what does only test code reach?".
type TestOnlyReport struct {
	// Candidates are the rows returned: confirmed against the text first,
	// largest first, then the ones whose name also appears in code that is
	// not a test. Every row is `possible` and carries ReasonTestOnly.
	Candidates []Candidate `json:"candidates"`

	// Found counts every definition whose bound callers are all in test
	// files, not just the ones returned. It is what the graph says, so it is
	// an upper bound on what the text check would leave.
	Found int `json:"found"`

	// TextChecked is how many of Found were checked against the indexed text,
	// and NameElsewhere how many of those have their name in code that is not
	// a test. Every returned row was checked.
	TextChecked   int `json:"text_checked"`
	NameElsewhere int `json:"name_elsewhere"`

	// Skipped counts definitions that met the graph test and were left out
	// for the reasons the unreferenced scan leaves definitions out: exported
	// (unless asked for), named like an entry point, or in a language the
	// index does not bind references for. SkippedUnjudged is that last part.
	Skipped         int `json:"skipped"`
	SkippedUnjudged int `json:"skipped_unjudged_language"`

	// EntryPoints counts definitions left out because a framework calls them:
	// a route handler or an event consumer whose only CALL expressions are in
	// its tests is not kept alive by its tests.
	EntryPoints int `json:"entry_points"`

	// TestFiles is how many of the run's files count as test files. Zero
	// means the pass had nothing to look for, which is not the same as
	// finding nothing.
	TestFiles int `json:"test_files"`

	// SymbolsExamined is how many symbols the pass walked. Truncated reports
	// that it stopped at its budget with symbols still unread.
	SymbolsExamined int    `json:"symbols_examined"`
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncated_reason,omitempty"`
}

// TestOnlyLimits is what a reader must know before acting on a test-only row.
// Find appends it to the report's caveats whenever the pass ran.
const TestOnlyLimits = "A definition listed as reached only from tests HAS callers; every one the index bound " +
	"is in a test file. Direct callers only: a helper called by such a definition is not listed, because " +
	"its own caller is not a test. A use the index could not bind is not counted, tests that live in the " +
	"same file as the code they test are not seen as tests, and code written to support tests is listed " +
	"alongside code that lost its last production caller, because nothing in an index tells them apart. " +
	"Whether to delete any of it is a judgement this report does not make."

const (
	// DefaultTestOnlyScanBudget is how many symbols the test-only pass
	// examines unless told otherwise.
	DefaultTestOnlyScanBudget = 2_000_000
	// testOnlyWindow is how many symbols one registry statement covers. Each
	// statement re-reads the set of test files, so a window is large; its
	// result is bounded by it all the same.
	testOnlyWindow = 10_000
)

// findTestOnly runs the test-only pass. handlers is the set of unbound route
// handler names; unjudged collects the languages left unjudged, shared with
// the unreferenced scan so the report names each language once.
func findTestOnly(
	ctx context.Context, src Source, runID int64, opts Options,
	limit int, handlers, unjudged map[string]bool,
) (*TestOnlyReport, error) {
	callers := opts.TestOnly
	budget := opts.TestOnlyScanBudget
	if budget <= 0 {
		budget = DefaultTestOnlyScanBudget
	}

	t := &TestOnlyReport{}
	testIDs, err := testFileIDs(ctx, callers, runID)
	if err != nil {
		return nil, err
	}
	t.TestFiles = len(testIDs)
	if len(testIDs) == 0 {
		// No test file, so no definition can have all its callers in one.
		return t, nil
	}

	keep := newTopN(shortlistSize(limit))
	var after int64
	for {
		if t.SymbolsExamined >= budget {
			// Out of budget. Whether that cut anything off is a fact worth
			// one cheap statement: a one-symbol window over an empty set
			// reads no edges and says whether a symbol remains.
			rest, err := callers.SymbolsReferencedOnlyFrom(ctx, runID, after, 1, nil)
			if err != nil {
				return nil, fmt.Errorf("deadcode: %w", err)
			}
			if rest.Examined > 0 {
				t.Truncated = true
				t.TruncatedReason = fmt.Sprintf("the test-only pass stopped after examining %d symbols; "+
					"narrow it with a path to cover the rest", t.SymbolsExamined)
			}
			break
		}
		page, err := callers.SymbolsReferencedOnlyFrom(ctx, runID, after,
			min(testOnlyWindow, budget-t.SymbolsExamined), testIDs)
		if err != nil {
			return nil, fmt.Errorf("deadcode: %w", err)
		}
		if page.Examined == 0 {
			break
		}
		t.SymbolsExamined += page.Examined
		after = page.LastID

		for _, s := range page.Symbols {
			if opts.PathPrefix != "" && !strings.HasPrefix(s.FilePath, opts.PathPrefix) {
				continue
			}
			if isExpectedUncalled(s.Kind, s.Name, s.FilePath) {
				t.Skipped++
				continue
			}
			if opts.Judgeable != nil && !opts.Judgeable(s.Language) {
				t.Skipped++
				t.SkippedUnjudged++
				unjudged[s.Language] = true
				continue
			}
			if s.Exported && !opts.IncludeExported {
				t.Skipped++
				continue
			}
			if s.ConsumesEvent || namesRouteHandler(handlers, s.Kind, s.Name) {
				t.EntryPoints++
				continue
			}
			t.Found++
			keep.offer(testOnlyCandidate(s))
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	t.Candidates, err = confirmTestOnly(ctx, src, runID, keep.sorted(), limit, t)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// testFileIDs returns the ids of the run's test files, in ascending order so
// the statement that takes them is the same on every run.
func testFileIDs(ctx context.Context, callers TestOnlySource, runID int64) ([]int64, error) {
	paths, err := callers.FilePaths(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("deadcode: %w", err)
	}
	var ids []int64
	for id, p := range paths {
		if capability.IsTestPath(p) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// testOnlyCandidate turns a registry row into a test-only candidate.
//
// The certainty is fixed. Every other reason a row can carry is added too, so
// the reader sees each thing that makes the row weaker still, but none of
// them and no absence of them moves it to likely.
func testOnlyCandidate(s registry.ConfinedSymbol) Candidate {
	c := Candidate{
		Name: s.Name, FQN: s.FQN, Kind: s.Kind,
		Path: s.FilePath, Line: s.StartLine, EndLine: s.EndLine,
		Language:       s.Language,
		Certainty:      CertaintyPossible,
		Reasons:        []Reason{ReasonTestOnly},
		TestReferences: s.Referrers,
	}
	if s.Exported {
		c.Reasons = append(c.Reasons, ReasonExported)
	}
	if s.NameUnbound {
		c.Reasons = append(c.Reasons, ReasonNameUnbound)
	}
	if s.Overrides {
		c.Reasons = append(c.Reasons, ReasonOverrides)
	}
	if s.PartialParse {
		c.Reasons = append(c.Reasons, ReasonPartialParse)
	}
	return c
}

// confirmTestOnly checks shortlisted rows against the indexed text and returns
// up to want of them: the confirmed rows in rank order, then, if those do not
// fill the answer, the rows whose name also appears in code that is not a
// test.
//
// It walks the shortlist in rank order and stops once want rows are
// confirmed, so the cost is bounded the way verifyAgainstText's is. A row it
// never reached is not returned: nothing is offered as this finding without
// the check having been made.
func confirmTestOnly(ctx context.Context, src Source, runID int64, shortlist []Candidate, want int, t *TestOnlyReport) ([]Candidate, error) {
	spans := map[string][]span{}
	for _, c := range shortlist {
		spans[c.Name] = append(spans[c.Name], span{path: c.Path, from: c.Line, to: max(c.EndLine, c.Line)})
	}
	seen := map[string]bool{} // name -> appears in non-test code, or unknown

	var confirmed, doubted []Candidate
	for _, c := range shortlist {
		if len(confirmed) >= want {
			break
		}
		elsewhere, done := seen[c.Name]
		if !done {
			mentions, ok, err := src.IdentifierMentions(ctx, runID, c.Name)
			if err != nil {
				return nil, fmt.Errorf("deadcode: %w", err)
			}
			// Unknown counts as mentioned, as it does for the unreferenced
			// scan: "the text index could not look" confirms nothing.
			elsewhere = !ok || usedOutside(outsideTests(mentions), spans[c.Name])
			seen[c.Name] = elsewhere
		}
		t.TextChecked++
		if elsewhere {
			t.NameElsewhere++
			c.Reasons = append(c.Reasons, ReasonMentioned)
			doubted = append(doubted, c)
		} else {
			confirmed = append(confirmed, c)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	out := confirmed
	if room := want - len(out); room > 0 {
		out = append(out, doubted[:min(room, len(doubted))]...)
	}
	return out, nil
}

// outsideTests drops the mentions in test files. Those are the callers the
// graph already counted; the question the text is asked is whether anything
// ELSE names the definition.
func outsideTests(mentions []registry.Mention) []registry.Mention {
	out := mentions[:0:0]
	for _, m := range mentions {
		if !capability.IsTestPath(m.Path) {
			out = append(out, m)
		}
	}
	return out
}
