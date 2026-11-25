package scale

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/capability/knowledge"
	"github.com/sutrakam/graphlume/internal/capability/ownership"
	"github.com/sutrakam/graphlume/internal/config"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
)

// authoredHistory is the synthetic history with an author on every commit.
//
// Shaped like a real team: a few people make most of the commits and a long
// tail makes a handful each, so some directories come out held by one name
// and others spread, and the ranking has both ends to order.
type authoredHistory struct {
	commits []ownership.Commit
}

func (h authoredHistory) Commits(_ context.Context, limit int) ([]ownership.Commit, error) {
	if len(h.commits) > limit {
		return h.commits[:limit], nil
	}
	return h.commits, nil
}

// asKnowledge is the same history through the knowledge port.
type asKnowledge struct{ h authoredHistory }

func (a asKnowledge) Commits(ctx context.Context, limit int) ([]knowledge.Commit, error) {
	cs, _ := a.h.Commits(ctx, limit)
	out := make([]knowledge.Commit, 0, len(cs))
	for _, c := range cs {
		out = append(out, knowledge.Commit{
			SHA: c.SHA, Author: c.AuthorName, AuthorEmail: c.AuthorEmail, When: c.When, Paths: c.Paths,
		})
	}
	return out, nil
}

func newAuthoredHistory(files, commits int) authoredHistory {
	const people = 40
	base := newSyntheticHistory(files, commits)
	out := make([]ownership.Commit, 0, len(base.commits))
	for i, c := range base.commits {
		// The square of a small residue lands on low numbers far more often
		// than high ones: person 0 to 5 carry most of the history.
		r := i % 13
		who := (r * r) % people
		out = append(out, ownership.Commit{
			SHA:         c.SHA,
			AuthorName:  fmt.Sprintf("Person %02d", who),
			AuthorEmail: fmt.Sprintf("person%02d@example.com", who),
			When:        c.When,
			Subject:     "synthetic change",
			Paths:       c.Paths,
		})
	}
	return authoredHistory{commits: out}
}

// Knowledge distribution reads the commit window once and the index's file
// list once. Its cost must follow those two, and its answer must stay inside
// the limit however many directories the history touched.
func TestKnowledgeDistributionIsBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()
	hist := asKnowledge{newAuthoredHistory(f.spec.Files, *flagCommits)}

	for _, c := range []struct {
		name string
		opts knowledge.Options
	}{
		{"knowledge, by directory", knowledge.Options{Limit: 20, CommitWindow: *flagCommits}},
		{"knowledge, by file", knowledge.Options{Limit: 20, CommitWindow: *flagCommits, PerFile: true, MinCommits: 2}},
	} {
		var rep *knowledge.Report
		m := measure(func() {
			var err error
			rep, err = knowledge.Analyze(ctx, hist, f.reg, f.runID, c.opts)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
		})
		t.Logf("%s: %d commits read; %d measured, %d listed, %d excluded for too few commits",
			c.name, rep.CommitsRead, rep.Measured, len(rep.Areas), rep.Excluded)

		if rep.Measured == 0 || len(rep.Areas) == 0 {
			t.Fatalf("%s: nothing was measured", c.name)
		}
		if len(rep.Areas) > 20 {
			t.Errorf("%s: returned %d rows, over the limit of 20", c.name, len(rep.Areas))
		}
		for i := 1; i < len(rep.Areas); i++ {
			if rep.Areas[i].BusFactor < rep.Areas[i-1].BusFactor {
				t.Errorf("%s: rows are not ordered by bus factor at row %d", c.name, i)
			}
		}
		for _, a := range rep.Areas {
			if a.BusFactor < 1 || a.BusFactor > a.Contributors || len(a.Top) > 3 {
				t.Errorf("%s: %s has bus factor %d of %d names with %d listed", c.name, a.Path, a.BusFactor, a.Contributors, len(a.Top))
			}
		}

		// The unit is the number of indexed FILES, read once to learn which
		// directories still exist. 1s fixed, 100µs per file.
		requireWithin(t, c.name, m, budget{fixed: time.Second, perUnit: 100 * time.Microsecond}, f.spec.Files)
		requireRetainedUnder(t, c.name, m, 32<<20)
	}
}

// TestEveryToolAnswerStaysBoundedAtScale calls busfactor on a
// server with no author history, where it answers in one line. This is the
// same bound with the history present and the widest answer it can give.
func TestKnowledgeRisksAnswerStaysBoundedAtScale(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	cfg := config.Defaults()
	cfg.ProjectName = "scale"
	cfg.MCPProfile = config.MCPFull
	srv, err := devmcp.New(devmcp.Deps{
		Registry: f.reg,
		Graph:    f.graph,
		Config:   cfg,
		Owners:   newAuthoredHistory(f.spec.Files, *flagCommits),
	})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	inv, err := srv.NewInvoker(ctx, "scale")
	if err != nil {
		t.Fatalf("invoker: %v", err)
	}
	defer func() { _ = inv.Close() }()

	limit := devmcp.DefaultLimits().MaxTotalBytes
	for _, args := range []map[string]any{
		{},
		{"limit": 1000, "min_commits": 1},
		{"limit": 1000, "min_commits": 1, "files": true},
		{"depth": 1},
	} {
		payload, _ := json.Marshal(args)
		start := time.Now()
		res, err := inv.Call(ctx, "busfactor", payload)
		if err != nil {
			t.Errorf("busfactor %s: %v", payload, err)
			continue
		}
		took := time.Since(start)
		t.Logf("busfactor %-44s %10s  answer %6d bytes", payload, took.Round(time.Microsecond), len(res.Text))

		if len(res.Text) > limit {
			t.Errorf("busfactor %s answered with %d bytes; the inline limit is %d", payload, len(res.Text), limit)
		}
		if !strings.Contains(res.Text, "bus factor") {
			t.Errorf("busfactor %s measured nothing:\n%s", payload, res.Text)
		}
		if strings.Contains(res.Text, "@") {
			t.Errorf("busfactor %s returned an address:\n%s", payload, res.Text)
		}
		b := budget{fixed: 1500 * time.Millisecond, perUnit: 25 * time.Microsecond}
		if took > b.at(f.info.Symbols) {
			t.Errorf("busfactor %s took %s, budget %s at %d symbols",
				payload, took.Round(time.Millisecond), b.at(f.info.Symbols).Round(time.Millisecond), f.info.Symbols)
		}
	}
}
