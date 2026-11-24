package mcp

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	acquiregit "github.com/sutrakam/graphlume/internal/acquire/git"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/impact"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// changes answers the reviewer's question: what does this RANGE of
// commits put at risk?
//
// impact starts from one symbol the caller names. A pull request does
// not arrive as a symbol; it arrives as a branch. Until this tool an agent
// reviewing a change had to work out which symbols a diff touched and ask
// about each, which is exactly the bookkeeping the index exists to do. The
// capability already existed behind `graphlume impact --base --head`; this is the
// same function reached from the tool surface, so the CLI, MCP, the REST
// endpoint and the portal give one answer.

type changeArgs struct {
	Base string `json:"base" jsonschema:"The revision the change starts from: a branch, tag or commit, e.g. 'main' or 'origin/main'."`
	Head string `json:"head,omitempty" jsonschema:"The revision the change ends at. Default 'HEAD'."`

	MinConfidence string `json:"min_confidence,omitempty" jsonschema:"Lowest confidence to follow: exact, high, medium or low. Default medium. Use 'high' when the result will gate a merge."`

	// Kind narrows the rows to one list so a long one can be paged. Without
	// it the four lists share one page in a fixed order, and on a range that
	// changes more than a page of symbols the code that depends on them and
	// the tests were never reached.
	Kind string `json:"kind,omitempty" jsonschema:"Return only one list, to page through it with limit and offset: changed (the changed symbols), file (every changed file), reached (code that depends on the changes, tests excluded) or test (tests that reach them). Default: all lists together."`

	pageArg
}

// changeKinds are the values kind accepts.
var changeKinds = map[string]bool{"changed": true, "file": true, "reached": true, "test": true}

// registerChange adds changes. It needs the graph and a git repository;
// without either it does not register, and the server says nothing about a
// tool it cannot run.
func (s *Server) registerChange(allowed map[string]bool) {
	if s.deps.Graph == nil {
		return
	}
	addTool(s, allowed, &sdk.Tool{
		Name:        "changes",
		Description: toolDescriptions()["changes"],
	}, s.analyzeChange)
}

func (s *Server) analyzeChange(ctx context.Context, req *sdk.CallToolRequest, args changeArgs) (*sdk.CallToolResult, *Response, error) {
	base := strings.TrimSpace(args.Base)
	if base == "" {
		return failWith("invalid_argument",
			"Pass 'base' as the branch or commit the change starts from, e.g. base='main'.",
			"base must not be empty")
	}
	head := strings.TrimSpace(args.Head)
	if head == "" {
		head = "HEAD"
	}
	minConf, err := parseImpactFloor(args.MinConfidence)
	if err != nil {
		return failFrom(err, "%v", err)
	}
	kind := strings.ToLower(strings.TrimSpace(args.Kind))
	if kind != "" && !changeKinds[kind] {
		return failWith("invalid_argument",
			"Pass kind as changed, file, reached or test, or omit it for every list.",
			"kind %q is not one of changed, file, reached, test", args.Kind)
	}
	paged := kind != "" || args.Limit > 0 || args.Offset > 0
	page, err := s.page(args.pageArg, kind+" row(s)")
	if err != nil {
		return failWith("invalid_argument", "Pass limit and offset as non-negative numbers, or omit them.", "%v", err)
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}
	if s.headRepo == nil || !s.headRepo.IsRepo() {
		return s.guide(ctx, req, runID, status,
			"This project is not a git repository, so there is no commit range to analyse.",
			"changes compares two git revisions. To ask about one symbol instead, call impact.")
	}

	rep, err := s.diffs.analyze(ctx, s.headRepo, fmt.Sprintf("%d|%s", runID, minConf), base, head, func(ctx context.Context) (impact.DiffReport, error) {
		return impact.AnalyzeDiff(ctx, s.capDeps, runID, impact.DiffRequest{
			Changes:   s.headRepo,
			BaseRev:   base,
			HeadRev:   head,
			Runs:      s.deps.Registry,
			Revisions: s.headRepo,
			IndexRoot: resolvedRoot(s.deps.Config.RepoRoot),
			Options:   impact.Options{MinConfidence: minConf},
		})
	})
	if err != nil {
		// An unknown revision is the caller's to fix and is the common case:
		// say which one, as guidance rather than as a malfunction.
		if strings.Contains(err.Error(), "resolve") || strings.Contains(err.Error(), "revision") ||
			strings.Contains(err.Error(), "reference not found") {
			return s.guide(ctx, req, runID, status,
				fmt.Sprintf("The range %s..%s could not be read: %v.", base, head, err),
				"Check that both revisions exist in this repository. A remote branch is named origin/<branch>.")
		}
		return failFrom(err, "analyse %s..%s: %v", base, head, err)
	}

	tests := 0
	for _, im := range rep.Impacted {
		if im.IsTest {
			tests++
		}
	}
	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary: fmt.Sprintf("%s..%s changes %d symbol(s) in %d file(s) and can reach %d more, %d of them tests. %s",
			base, head, rep.SeedsFound, len(rep.ChangedFiles), rep.TotalReached(), tests, riskSentence(rep.Risk, false)),
	}

	// Changed symbols first: they are what the reviewer reads the diff for.
	// Among them, the ones other code depends on lead. A changed configuration
	// key or constant that nothing reaches is still listed, after them: a
	// review that opens with seven lines of .env keys buries the function
	// with thirty callers underneath.
	seeds := append([]impact.Seed(nil), rep.Seeds...)
	sort.SliceStable(seeds, func(i, j int) bool {
		ri := len(seeds[i].Report.Direct) + len(seeds[i].Report.Indirect)
		rj := len(seeds[j].Report.Direct) + len(seeds[j].Report.Indirect)
		return ri > rj
	})
	var items []Item
	want := func(k string) bool { return kind == "" || kind == k }
	if want("changed") {
		for _, seed := range seeds {
			items = append(items, Item{
				Kind: "changed", Name: seed.Name, FQN: seed.FQN, Path: seed.FilePath,
				Why: fmt.Sprintf("%s in this range; %d direct, %d further", seed.ChangeKind,
					len(seed.Report.Direct), len(seed.Report.Indirect)),
			})
		}
	}
	// Every changed file, only when asked for: the summary counts them, and a
	// reviewer paging through a large range needs the list, not the count.
	if kind == "file" {
		symbols := map[string]int{}
		for _, seed := range rep.Seeds {
			symbols[seed.FilePath]++
		}
		unindexed := map[string]bool{}
		for _, f := range rep.UnindexedFiles {
			unindexed[f] = true
		}
		for _, f := range rep.ChangedFiles {
			ip := strings.TrimPrefix(f, rep.IndexRootPrefix)
			why := fmt.Sprintf("changed in this range; %d changed symbol(s)", symbols[ip])
			if unindexed[f] {
				why = "changed in this range; not in the index"
			}
			items = append(items, Item{Kind: "file", Name: path.Base(f), Path: f, Why: why})
		}
	}
	// Then what they reach, tests first, because "what do I run" is the next
	// question and the answer is otherwise buried under ordinary callers.
	for pass := 0; pass < 2; pass++ {
		for _, im := range rep.Impacted {
			if im.IsTest != (pass == 0) {
				continue
			}
			k := "reached"
			if im.IsTest {
				k = "test"
			}
			if !want(k) {
				continue
			}
			items = append(items, Item{
				Kind: k, Name: im.Name, FQN: im.FQN, Path: im.FilePath,
				Confidence: string(im.Confidence),
				Why:        fmt.Sprintf("%d hop(s) from a changed symbol", im.Depth),
			})
		}
	}
	if paged {
		paginate(resp, page, items)
	} else {
		resp.Items = items
	}

	// Judged over the run: a range of commits spans whatever languages it
	// touched. What could not be determined for individual changed symbols
	// arrives as an unbound_references_to_name unknown below.
	cov := s.coverageFor(ctx, runID, "", "", capability.TargetBinding{}, "what the change reaches")
	s.applyCoverage(resp, cov, "change analysis")
	if !rep.RevisionMatchesRange && rep.IndexedRevision != "" {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"The index was built at %.12s, which is not the head of this range. Symbols added or moved "+
				"since then are not in it. Run 'graphlume index' on the head revision for an exact answer.",
			rep.IndexedRevision))
	}
	if n := len(rep.UnindexedFiles); n > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d changed file(s) are not in the index (deleted, generated, or in a language it does not parse), "+
				"so nothing is known about what they reach: %s.", n, joinSome(rep.UnindexedFiles, 4)))
	}
	for _, u := range rep.Unknown {
		resp.Notes = append(resp.Notes, fmt.Sprintf("NOT VISIBLE (%s): %s", u.Kind, u.Detail))
	}
	if rep.CoChangePartial {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"Partial co-change history: %d of 200 commits were read before the time budget ran out, so "+
				"companion files committed with these changes may be missing.", rep.CoChangeCommitsRead))
	}
	if rep.Truncated {
		resp.markTruncated(rep.TruncatedReason)
	}
	return s.reply(ctx, req, resp)
}

// resolvedRoot is the project root with symlinks resolved, the form the git
// layer reports its own root in. See canonicalRoot in internal/cli for why the
// two must agree.
func resolvedRoot(root string) string {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		return real
	}
	return root
}

// joinSome joins up to n items and says how many were left out.
func joinSome(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:n], ", "), len(items)-n)
}

// diffCache keeps the last few range analyses for a short while.
//
// One review asks for the same range several times in a row: the summary,
// then a page of each list. Each answer re-ran the whole analysis, seconds on
// a large range, so paging a list cost as much as opening the review. Entries
// are keyed by the RESOLVED commits, so a branch that moved is a new key, and
// expire after diffCacheTTL so a re-index is never answered from memory for
// long. Concurrent asks for the same key share one analysis.
type diffCache struct {
	mu      sync.Mutex
	entries map[string]*diffEntry
}

type diffEntry struct {
	done chan struct{}
	at   time.Time
	rep  impact.DiffReport
	err  error
}

const (
	diffCacheTTL  = 2 * time.Minute
	diffCacheSize = 8
)

func (c *diffCache) analyze(ctx context.Context, repo *acquiregit.Repo, scope, base, head string,
	run func(context.Context) (impact.DiffReport, error)) (impact.DiffReport, error) {
	b, berr := repo.ResolveRevision(ctx, base)
	h, herr := repo.ResolveRevision(ctx, head)
	if berr != nil || herr != nil {
		return run(ctx) // the analysis reports the bad revision in its own words
	}
	key := scope + "|" + b + "|" + h

	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*diffEntry{}
	}
	now := time.Now()
	for k, e := range c.entries {
		select {
		case <-e.done:
			if now.Sub(e.at) > diffCacheTTL || e.err != nil {
				delete(c.entries, k)
			}
		default:
		}
	}
	if e, ok := c.entries[key]; ok {
		c.mu.Unlock()
		select {
		case <-e.done:
			return e.rep, e.err
		case <-ctx.Done():
			return impact.DiffReport{}, ctx.Err()
		}
	}
	if len(c.entries) >= diffCacheSize {
		var oldest string
		for k, e := range c.entries {
			if oldest == "" || e.at.Before(c.entries[oldest].at) {
				oldest = k
			}
		}
		delete(c.entries, oldest)
	}
	e := &diffEntry{done: make(chan struct{}), at: now}
	c.entries[key] = e
	c.mu.Unlock()

	// The analysis runs on its own context: a caller that gives up must not
	// fail the callers sharing it. It is bounded by the analysis' own budgets.
	rep, err := run(context.WithoutCancel(ctx))
	c.mu.Lock()
	e.rep, e.err, e.at = rep, err, time.Now()
	c.mu.Unlock()
	close(e.done)
	return e.rep, e.err
}
