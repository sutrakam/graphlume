package mcp

import (
	stdctx "context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sutrakam/graphlume/internal/capability"
	devctx "github.com/sutrakam/graphlume/internal/context"
	"github.com/sutrakam/graphlume/internal/security"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// taskContextArgs is the highest-value tool in the product, so its schema text
// gets the most care: this description is what decides whether an agent calls
// it FIRST or falls back to grepping the repository.
type taskContextArgs struct {
	Task string `json:"task" jsonschema:"Describe the change you are about to make, in plain English, as you would to a colleague. For example: 'Add idempotency to payment callback processing'. Full sentences work better than keywords — the task is parsed for intent, not matched literally."`

	MaxFiles int `json:"max_files,omitempty" jsonschema:"Cap on returned items (default 20). Raise only if the default answer is genuinely too narrow."`

	// MaxTokens is the budget the pack is assembled for. Without it every pack
	// was built for the default 8,000 whatever the caller planned to spend, so
	// a larger budget changed nothing and a smaller one was overrun.
	MaxTokens int `json:"max_tokens,omitempty" jsonschema:"Estimated token budget (characters divided by 4) to assemble the pack for, 1000 to 64000. Default 8000. A larger budget carries more source and more items; a smaller one fewer."`

	pageArg

	// The floor applies to the PACK, not to the traversal. internal/context
	// deliberately walks every edge and discounts weak ones during ranking
	// (see internal/context/expand.go) so a weakly-supported path can still
	// surface and say so; raising a floor inside the walk would silently
	// shrink the reachable set instead. What a caller asking for compiler-grade
	// context wants is the items it can defend, which is a question about the
	// answer, not about the search.
	minConfidenceArg

	// Unlike the floor above, this one DOES apply to the traversal — it is the
	// only knob here that can. The expansion follows every edge by design, so
	// the thing that makes a pack enormous is not a weak edge, it is one wide
	// node whose neighbourhood is the whole repository.
	hubDegreeLimitArg
}

// registerTaskContext adds context when the context engine is wired.
//
// It is registered separately from the other tools because it needs the graph,
// which the other tools do not — keeping that dependency out of the base
// registration means the server still starts and answers symbol queries on an
// index that has never been resolved.
func (s *Server) registerTaskContext(allowed map[string]bool) {
	if s.deps.Graph == nil {
		return
	}
	addTool(s, allowed, &sdk.Tool{
		Name:        "context",
		Description: toolDescriptions()["context"],
	}, s.getTaskContext)
}

func (s *Server) getTaskContext(ctx stdctx.Context, call *sdk.CallToolRequest, args taskContextArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Task) == "" {
		return failWith("invalid_argument",
			"Pass a non-empty task describing the change you intend to make, then call context again.",
			"task must not be empty — describe the change you intend to make")
	}
	minConf, err := parseTaskContextMinConfidence(args.MinConfidence)
	if err != nil {
		return failFrom(err, "%v", err)
	}
	if err := parseHubDegreeLimit(args.HubDegreeLimit); err != nil {
		return failFrom(err, "%v", err)
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, call, err)
	}

	// CLAMPED, not merely defaulted. max_files arrives from the agent, and it
	// bounds the excerpt-read loop inside Assemble — an unclamped 1,000,000
	// would read a million file excerpts of up to 20,000 lines each into
	// memory. The response bounds further down trim the OUTPUT; they do not
	// stop the work.
	//
	// search three files away already clamps its limit against exactly
	// this ceiling. Two answers to one threat in one server would be a defect.
	if args.MaxTokens < 0 {
		return failWith("invalid_argument", "Pass max_tokens as a number from 1000 to 64000, or omit it.",
			"max_tokens must not be negative (got %d)", args.MaxTokens)
	}
	paged := args.Limit > 0 || args.Offset > 0
	page, err := s.page(args.pageArg, "item(s)")
	if err != nil {
		return failWith("invalid_argument", "Pass limit and offset as non-negative numbers, or omit them.", "%v", err)
	}
	req := devctx.Request{Task: args.Task}
	if args.MaxTokens > 0 {
		req.Budget = budgetForTokens(args.MaxTokens)
	}
	if args.MaxFiles > 0 {
		req.Budget.MaxFiles = min(args.MaxFiles, s.limits.MaxItems)
	}
	// Not clamped, unlike MaxFiles: this value can only make the walk SMALLER,
	// so a pathological figure costs nothing to honour. Budget.withDefaults
	// leaves an unset value at zero, which is "no pruning".
	req.Budget.HubDegreeLimit = args.HubDegreeLimit

	// Paging through a pack, or switching back to a budget, asks the same
	// question again within seconds; the assembled pack is kept briefly so the
	// next page is read, not rebuilt. Keyed by the run's content fingerprint,
	// so an incremental re-index is never answered from memory.
	key := ""
	if fp, ferr := s.deps.Registry.CatalogueFingerprint(ctx, runID); ferr == nil {
		key = fmt.Sprintf("%d|%v|%q|%+v", runID, fp, args.Task, req.Budget)
	}
	pack, err := s.packs.get(key, func() (devctx.Pack, error) {
		return s.assembler.Assemble(ctx, s.deps.Registry, s.deps.Graph, runID, req)
	})
	if err != nil {
		return failFrom(err, "assemble context: %v", err)
	}

	// Filtered here rather than inside Assemble, and the token estimate is
	// recomputed from what survives — a pack that reports the unfiltered cost
	// of an answer it did not return would misprice every budget decision the
	// agent makes next.
	dropped := 0
	if minConf != model.ConfidenceLow {
		kept := make([]devctx.Item, 0, len(pack.Items))
		for _, it := range pack.Items {
			// An item with no tier ranks 0 and is dropped by any explicit
			// floor. That is the honest reading: the caller asked for what
			// GraphLume can defend to a stated tier, and an ungraded item is
			// not that.
			if model.Confidence(it.Confidence).AtLeast(minConf) {
				kept = append(kept, it)
			}
		}
		dropped = len(pack.Items) - len(kept)
		pack.Items = kept
		pack.TokenEstimate = pack.EstimateTokens()
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary: fmt.Sprintf("%d item(s) for %q (~%d tokens).",
			len(pack.Items), pack.Task, pack.TokenEstimate),
		// The summary above quotes a bytes/4 token estimate, not a measured
		// count — see internal/mcp/basis.go.
		Basis: BasisTokenCount,
	}

	// The interpretation goes first. Almost every bad pack comes from a
	// misread task rather than bad ranking, so showing what was understood
	// lets an agent correct course in one turn instead of concluding the
	// index is wrong.
	if pack.Interpretation != "" {
		resp.Notes = append(resp.Notes, "Understood as: "+pack.Interpretation)
	}
	if note := floorNote(minConf, dropped); note != "" {
		resp.Notes = append(resp.Notes, note)
	}

	// The pack keeps the assembler's RELEVANCE order. It used to be re-sorted
	// by certainty tier, which put an item three hops away ahead of the one
	// the task named whenever the far one happened to be compiler-proven:
	// certainty says how sure the link is, not how much the item matters to
	// the task. What the byte bound cuts is therefore the least relevant tail,
	// and a caller that wants the rest pages for it (limit, offset).
	items := make([]Item, 0, len(pack.Items))
	secret := 0
	for _, it := range pack.Items {
		// Second barrier: the assembler already leaves these out.
		if security.IsSecretBearingPath(it.Path) {
			secret++
			continue
		}
		items = append(items, Item{
			Kind:       it.Kind,
			Name:       it.Name,
			FQN:        it.FQN,
			Path:       it.Path,
			Line:       it.Line,
			Confidence: it.Confidence,
			Why:        it.Why,
			Relevance:  it.Relevance,
			Excerpt:    it.Excerpt,
		})
	}
	if secret > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d item(s) from environment, key or credential files were left out; they are never part of a context pack.", secret))
	}
	if paged {
		paginate(resp, page, items)
	} else {
		resp.Items = items
	}

	// Exclusions are a trust feature, not noise. An agent that can see WHY a
	// plausible-looking file was left out can judge whether the omission was
	// right, instead of assuming the tool simply missed it.
	for _, ex := range pack.Excluded {
		resp.Notes = append(resp.Notes,
			fmt.Sprintf("Excluded %s: %s", ex.Name, ex.Reason))
	}
	resp.Notes = append(resp.Notes, pack.Notes...)

	// The pack is found by walking the graph, so it is as complete as the
	// graph is. Judged over the run, since a task is not about one symbol.
	cov := s.coverageFor(ctx, runID, "", "", capability.TargetBinding{}, aboutContext)
	s.applyCoverage(resp, cov, "context assembly")

	if len(pack.Items) == 0 {
		note := "Nothing matched. Either the task names code that is not in this repository, " +
			"or the index did not bind the relationships that lead to it."
		// 'graphlume resolve' is named only when it would change something.
		if cov.Pending > 0 {
			note += fmt.Sprintf(" %d reference(s) have not been resolved yet: run 'graphlume resolve'.", cov.Pending)
		}
		resp.Notes = append(resp.Notes, note)
	}
	return s.reply(ctx, call, resp)
}

// budgetForTokens is the pack budget for a caller's token figure. The token
// ceiling is the figure itself, clamped; the item and file counts follow the
// calibrated profile nearest to it, so a 32k pack can hold more than an 8k
// one instead of the same items with longer excerpts. The graph walk is not
// widened: depth multiplies the work, and a budget is about what is returned.
func budgetForTokens(tokens int) devctx.Budget {
	tokens = max(1000, min(tokens, 64000))
	b := devctx.DefaultBudget()
	switch {
	case tokens <= 4000:
		p := devctx.BudgetForProfile(devctx.ProfileCompact)
		b.MaxFiles, b.MaxSymbols, b.MaxLines = p.MaxFiles, p.MaxSymbols, p.MaxLines
	case tokens >= 24000:
		p := devctx.BudgetForProfile(devctx.ProfileDeep)
		b.MaxFiles, b.MaxSymbols, b.MaxLines = max(b.MaxFiles, p.MaxFiles), max(b.MaxSymbols, p.MaxSymbols), max(b.MaxLines, p.MaxLines)
	}
	b.MaxTokensEstimate = tokens
	return b
}

// PrewarmContext loads what the first context pack needs (the catalogue of
// the current run) so a person's first pack is not the one that pays for it.
// Meant to be run in the background by a long-lived front end; it never
// fails loudly, since the first real request loads anything it missed.
func (s *Server) PrewarmContext(ctx stdctx.Context) {
	if s == nil || s.deps.Graph == nil || s.assembler == nil {
		return
	}
	runID, _, err := s.currentRun(ctx)
	if err != nil {
		return
	}
	if s.assembler.Warm(ctx, s.deps.Registry, runID) != nil {
		return
	}
	// One throwaway pack and the run's coverage figures: the first request
	// otherwise also pays for the full-text tables and the coverage queries
	// reaching the page cache, measured at seconds on a large index.
	_, _ = s.assembler.Assemble(ctx, s.deps.Registry, s.deps.Graph, runID,
		devctx.Request{Task: "warm the context index for the first request"})
	_ = s.coverageFor(ctx, runID, "", "", capability.TargetBinding{}, aboutContext)
	// The symbol table every impact and change analysis walks from.
	_, _ = s.capDeps.SymbolIndex(ctx, runID)
}

// packCache keeps the last few assembled packs for packCacheTTL. A pack is a
// value (its slices are not mutated by the handler, which copies them into
// the response), so sharing one between two answers is safe.
type packCache struct {
	mu      sync.Mutex
	entries map[string]packEntry
}

type packEntry struct {
	at   time.Time
	pack devctx.Pack
}

const (
	packCacheTTL  = 2 * time.Minute
	packCacheSize = 8
)

func (c *packCache) get(key string, build func() (devctx.Pack, error)) (devctx.Pack, error) {
	if key == "" {
		return build()
	}
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Sub(e.at) <= packCacheTTL {
		c.mu.Unlock()
		return clonePack(e.pack), nil
	}
	c.mu.Unlock()
	p, err := build()
	if err != nil {
		return p, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]packEntry{}
	}
	for k, e := range c.entries {
		if now.Sub(e.at) > packCacheTTL {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= packCacheSize {
		var oldest string
		for k, e := range c.entries {
			if oldest == "" || e.at.Before(c.entries[oldest].at) {
				oldest = k
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = packEntry{at: now, pack: clonePack(p)}
	c.mu.Unlock()
	return p, nil
}

// clonePack copies the slices the handler filters in place.
func clonePack(p devctx.Pack) devctx.Pack {
	p.Items = append([]devctx.Item(nil), p.Items...)
	p.Excluded = append([]devctx.Exclusion(nil), p.Excluded...)
	p.Notes = append([]string(nil), p.Notes...)
	return p
}
