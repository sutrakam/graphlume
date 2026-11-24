package mcp

import (
	"context"
	"fmt"
	"math"
	"path"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/capability/knowledge"
	"github.com/sutrakam/graphlume/internal/capability/ownership"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// busfactor answers "where would losing one person lose most of
// what is known": the directories whose recent commits come from the fewest
// people, most concentrated first.
//
// It reads the same history owners does, through the same port, so there
// is one reader of git behind the server and one cache of its walk.
//
// NAMES, NEVER ADDRESSES, for the reason owners.go gives. The capability's
// report has no field that can hold an address, and the name of an author who
// set their git name to their address is cut at the @ before it is returned.

type knowledgeArgs struct {
	Path       string `json:"path,omitempty" jsonschema:"Only measure files under this path prefix, e.g. 'internal/billing/'."`
	Months     int    `json:"months,omitempty" jsonschema:"How many months of history to measure, counted back from the newest commit. Default 12."`
	Commits    int    `json:"commits,omitempty" jsonschema:"The most recent commits to read to cover those months (the commit budget). Default 5000, which is also the maximum."`
	Limit      int    `json:"limit,omitempty" jsonschema:"Most directories to return. Default 15, maximum 200."`
	Offset     int    `json:"offset,omitempty" jsonschema:"Directories to skip, for the next page. The answer names the next offset when more measured directories exist."`
	MinCommits int    `json:"min_commits,omitempty" jsonschema:"Fewest commits a directory needs before it is measured. Default 5. Directories below it are counted in the answer, not listed."`
	Depth      int    `json:"depth,omitempty" jsonschema:"Group by the first N path segments instead of by each file's own directory, e.g. 1 for top-level directories. Default 0."`
	Files      bool   `json:"files,omitempty" jsonschema:"Measure each file instead of each directory. Default false."`
}

// defaultKnowledgeItems is how many areas are listed unless asked. One row
// each, so the answer stays a few kilobytes with its notes.
const defaultKnowledgeItems = 15

// knowledgeNameRunes bounds one author name inside a row's reason. A name is
// repository content of any length, and three of them share a bounded line.
const knowledgeNameRunes = 40

func (s *Server) registerKnowledge(allowed map[string]bool) {
	addTool(s, allowed, &sdk.Tool{
		Name:        "busfactor",
		Description: toolDescriptions()["busfactor"],
	}, s.listKnowledgeRisks)
}

// knowledgeHistory adapts the author-carrying history owners reads to
// the knowledge port. The paths are already in the index's path space.
type knowledgeHistory struct{ h ownership.History }

func (a knowledgeHistory) Commits(ctx context.Context, limit int) ([]knowledge.Commit, error) {
	cs, err := a.h.Commits(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]knowledge.Commit, 0, len(cs))
	for _, c := range cs {
		out = append(out, knowledge.Commit{
			SHA: c.SHA, Author: c.AuthorName, AuthorEmail: c.AuthorEmail, When: c.When, Paths: c.Paths,
			ShallowBoundary: c.ShallowBoundary, Truncated: c.Truncated,
		})
	}
	return out, nil
}

func (s *Server) listKnowledgeRisks(ctx context.Context, req *sdk.CallToolRequest, args knowledgeArgs) (*sdk.CallToolResult, *Response, error) {
	if args.Commits < 0 || args.Limit < 0 || args.MinCommits < 0 || args.Depth < 0 || args.Months < 0 || args.Offset < 0 {
		return failWith("invalid_argument",
			"Pass months, commits, limit, offset, min_commits and depth as positive numbers, or omit them.",
			"months, commits, limit, offset, min_commits and depth must not be negative")
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}
	if s.deps.Owners == nil {
		return s.guide(ctx, req, runID, status,
			"No commit history is available to this server, so knowledge distribution cannot be measured.",
			"This reads git history. The project is not a git repository, or its history could not "+
				"be opened. That is not a finding that knowledge is well spread: nothing was measured. "+
				"Everything else this server answers is unaffected.")
	}

	limit := args.Limit
	if limit == 0 {
		limit = defaultKnowledgeItems
	}
	limit = min(limit, s.maxPage())
	commitWindow := args.Commits
	if commitWindow > knowledge.DefaultCommitWindow {
		commitWindow = knowledge.DefaultCommitWindow
	}

	// Every measured area is ranked, then paged here: the list is one row per
	// directory, so it is small, and a total is only honest when it is counted.
	rep, err := knowledge.Analyze(ctx, knowledgeHistory{s.deps.Owners}, s.deps.Registry, runID, knowledge.Options{
		Limit:        math.MaxInt32,
		Months:       args.Months,
		CommitWindow: commitWindow,
		MinCommits:   args.MinCommits,
		PathPrefix:   strings.TrimSpace(args.Path),
		Depth:        args.Depth,
		PerFile:      args.Files,
		ShallowClone: s.headRepo != nil && s.headRepo.Shallow(),
	})
	if err != nil {
		return failFrom(err, "knowledge distribution: %v", err)
	}

	page := &Page{Offset: args.Offset, Limit: limit, noun: "measured " + unitNoun(rep.Unit), max: s.maxPage()}
	listed := max(0, min(limit, len(rep.Areas)-args.Offset))
	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary:       knowledgeSummary(rep, listed),
		History:       knowledgeWindow(rep),
	}
	var items []Item
	for _, a := range rep.Areas {
		name := path.Base(a.Path)
		if a.Path == knowledge.Root {
			name = "(top directory)"
		}
		items = append(items, Item{
			Kind: string(rep.Unit), Name: name, Path: a.Path, Source: string(model.SourceGit),
			Why: knowledgeWhy(a),
		})
	}
	paginate(resp, page, items)
	if len(rep.Areas) > 0 {
		resp.Notes = append(resp.Notes,
			"Rows are ordered most concentrated first: the lowest bus factor, then the largest share "+
				"held by one name, then the most commits. 'idle' is the days between that person's last "+
				"change there and the newest commit read. Use owners for who has context on one symbol.")
	}
	for _, n := range rep.Notes {
		// The capability ranked every area; the page says what is not shown.
		if strings.Contains(n, "raise the limit or narrow by path") {
			continue
		}
		resp.Notes = append(resp.Notes, n)
	}
	resp.Notes = append(resp.Notes, rep.Caveats...)
	return s.reply(ctx, req, resp)
}

// HistoryWindow is the stretch of history a commit-based answer was measured
// over: Months back from the newest commit (Since to To), at most Budget
// commits. Covered says the whole period was read; false means the commit or
// time budget ran out first and From is later than Since.
type HistoryWindow struct {
	Months  int    `json:"months,omitempty"`
	Since   string `json:"since,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Commits int    `json:"commits"`
	Budget  int    `json:"budget"`
	Covered bool   `json:"covered"`
	Partial bool   `json:"partial,omitempty"`
	Shallow bool   `json:"shallow,omitempty"`
}

func ymd(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02")
}

func knowledgeWindow(rep *knowledge.Report) *HistoryWindow {
	return &HistoryWindow{
		Months: rep.Months, Since: ymd(rep.SinceUnix), From: ymd(rep.OldestCommit), To: ymd(rep.NewestCommit),
		Commits: rep.CommitsRead, Budget: rep.CommitWindow, Covered: rep.PeriodCovered,
		Partial: rep.Partial, Shallow: rep.ShallowClone,
	}
}

func unitNoun(u knowledge.Unit) string {
	if u == knowledge.UnitFile {
		return "file(s)"
	}
	return "directory(ies)"
}

// windowPhrase says what history the figures come from: "1,234 commit(s) in
// the last 12 month(s)", or the newest n when the budget ran out first.
func windowPhrase(months, read int, covered bool) string {
	switch {
	case months > 0 && covered:
		return fmt.Sprintf("%d commit(s) in the last %d month(s)", read, months)
	case months > 0:
		return fmt.Sprintf("the newest %d commit(s) (the commit budget ran out before %d month(s))", read, months)
	default:
		return fmt.Sprintf("the last %d commit(s)", read)
	}
}

// knowledgeSummary is the one-line answer.
func knowledgeSummary(rep *knowledge.Report, listed int) string {
	unit := "directory(ies)"
	if rep.Unit == knowledge.UnitFile {
		unit = "file(s)"
	}
	if rep.CommitsRead == 0 {
		return "No commit history could be read, so knowledge distribution was not measured."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d measured %s listed, most concentrated first, from %s",
		listed, rep.Measured, unit, windowPhrase(rep.Months, rep.CommitsRead, rep.PeriodCovered))
	if o := rep.Overall; o != nil {
		fmt.Fprintf(&b, " by %d author name(s)", o.Contributors)
	}
	fmt.Fprintf(&b, ". Bus factor of 1: %d of the %d measured.", rep.SolePerson, rep.Measured)
	if rep.Excluded > 0 {
		fmt.Fprintf(&b, " %d more had fewer than %d commits and were not measured.", rep.Excluded, rep.MinCommits)
	}
	if o := rep.Overall; o != nil {
		fmt.Fprintf(&b, " All %d commit(s) in scope taken together: bus factor %d.", o.Commits, o.BusFactor)
	}
	return b.String()
}

// knowledgeWhy is the one-line account of an area's place in the list.
func knowledgeWhy(a knowledge.Area) string {
	var b strings.Builder
	fmt.Fprintf(&b, "bus factor %d: %d commit(s) by %d name(s)", a.BusFactor, a.Commits, a.Contributors)
	for i, c := range a.Top {
		if i == 0 {
			b.WriteString("; ")
		} else {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %.0f%%", clipRunes(c.Name, knowledgeNameRunes), 100*c.Share)
		if i == 0 && c.LastChanged > 0 {
			fmt.Fprintf(&b, " (last change here %s, idle %dd)",
				time.Unix(c.LastChanged, 0).UTC().Format("2006-01-02"), a.TopIdleDays)
		}
	}
	return b.String()
}

// clipRunes cuts s to at most n runes, marking the cut.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
