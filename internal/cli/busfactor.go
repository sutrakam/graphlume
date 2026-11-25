package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/capability/knowledge"
)

// newBusFactorCmd is the terminal form of busfactor: where recent
// work is concentrated in so few people that losing one loses most of it.
//
// The command is named for the figure a reader already knows to ask for. The
// tool is named for what it lists, because a tool name is a steer to a model
// and "risks" says when to call it.
func newBusFactorCmd(g *Global) *cobra.Command {
	var (
		pathPrefix string
		limit      int
		commits    int
		months     int
		minCommits int
		depth      int
		perFile    bool
	)
	cmd := &cobra.Command{
		Use:   "bus-factor",
		Short: "Show where recent work is concentrated in the fewest people",
		Long: `Reads recent git history and lists the directories whose commits come from
the fewest people, most concentrated first.

  graphlume bus-factor
  graphlume bus-factor --path internal/ --months 6
  graphlume bus-factor --depth 1
  graphlume bus-factor --files --path internal/billing/
  graphlume bus-factor --json

BUS FACTOR is the smallest number of people whose commits make up at least half
of the commits that touched the directory. One means a single person made half
or more of the recent changes there. SHARE is the part made by the largest
contributor, and IDLE is how many days before the newest commit read their last
change there was: one person who stopped touching it months ago is a different
finding from one person who changed it yesterday.

The history read is the last --months months (12 by default), counted back from
the newest commit, at most --commits commits; the output says which bound the
history read.

A commit counts once for each directory it touches. By default a file is
counted under its own directory; --depth N groups by the first N path segments
instead, and --files measures each file.

This counts who CHANGED the code, not who understands it. People are the author
names git recorded, exactly as spelled: names are never merged, so one person
under two spellings counts as two, and email addresses are never printed. A
shallow clone and a squash-merge workflow both distort the figures. Directories
with fewer than --min-commits commits are not measured, because one author out
of two commits is not a finding; they are counted, and they are unmeasured, not
safe.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 0 || commits < 0 || minCommits < 0 || depth < 0 || months < 0 {
				return fmt.Errorf("--limit, --commits, --months, --min-commits and --depth must not be negative")
			}
			ctx := cmd.Context()
			s, err := openQuery(ctx, g)
			if err != nil {
				return err
			}
			defer s.close()

			hist := openHistory(g)
			rep, err := knowledge.Analyze(ctx, knowledgeHistory{hist}, s.reg, s.runID, knowledge.Options{
				Limit: limit, CommitWindow: commits, Months: months, MinCommits: minCommits,
				PathPrefix: strings.TrimSpace(pathPrefix), Depth: depth, PerFile: perFile,
				ShallowClone: hist.repo != nil && hist.repo.Shallow(),
			})
			if err != nil {
				return err
			}
			if hist.note != "" {
				rep.Notes = append([]string{strings.TrimSuffix(hist.note, " The blast radius below is still the index's.")}, rep.Notes...)
			}
			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), rep)
			}
			renderBusFactor(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&pathPrefix, "path", "", "only measure files under this path prefix")
	cmd.Flags().IntVarP(&limit, "limit", "n", knowledge.DefaultLimit, "maximum directories to list")
	cmd.Flags().IntVar(&commits, "commits", knowledge.DefaultCommitWindow, "the most recent commits to read (the commit budget)")
	cmd.Flags().IntVar(&months, "months", knowledge.DefaultMonths, "how many months of history to measure, back from the newest commit")
	cmd.Flags().IntVar(&minCommits, "min-commits", knowledge.DefaultMinCommits, "fewest commits a directory needs to be measured")
	cmd.Flags().IntVar(&depth, "depth", 0, "group by the first N path segments (0 = each file's own directory)")
	cmd.Flags().BoolVar(&perFile, "files", false, "measure each file instead of each directory")
	return cmd
}

// knowledgeHistory adapts the CLI's git history reader to knowledge.History.
// The reader has already rebased commit paths onto the project root.
type knowledgeHistory struct{ h *gitHistory }

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

// renderBusFactor prints the report: what was read, the table, the whole scope
// as one figure, what this run could not see, and what no run can establish.
// The last section is part of the answer, for the reason writeWho gives.
func renderBusFactor(out io.Writer, rep *knowledge.Report) {
	unit, units := "DIRECTORY", "directory(ies)"
	if rep.Unit == knowledge.UnitFile {
		unit, units = "FILE", "file(s)"
	}

	if len(rep.Areas) == 0 {
		fmt.Fprintln(out, "Nothing to measure.")
	} else {
		people := 0
		if rep.Overall != nil {
			people = rep.Overall.Contributors
		}
		window := fmt.Sprintf("the last %d commit(s)", rep.CommitsRead)
		if rep.Months > 0 && rep.PeriodCovered {
			window = fmt.Sprintf("%d commit(s) in the last %d month(s)", rep.CommitsRead, rep.Months)
		} else if rep.Months > 0 {
			window = fmt.Sprintf("the newest %d commit(s), the commit budget", rep.CommitsRead)
		}
		fmt.Fprintf(out, "Knowledge distribution from %s (%s to %s) by %d author name(s).\n",
			window, day(rep.OldestCommit), day(rep.NewestCommit), people)
		fmt.Fprintf(out, "%d %s measured, %d with a bus factor of 1. Most concentrated first.\n\n",
			rep.Measured, units, rep.SolePerson)

		tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		fmt.Fprintf(tw, "BUS FACTOR\t%s\tCOMMITS\tPEOPLE\tTOP CONTRIBUTOR\tSHARE\tTHEIR LAST CHANGE\tIDLE\n", unit)
		for _, a := range rep.Areas {
			top := "-"
			if len(a.Top) > 0 {
				top = a.Top[0].Name
			}
			fmt.Fprintf(tw, "%d\t%s\t%d\t%d\t%s\t%.0f%%\t%s\t%dd\n",
				a.BusFactor, a.Path, a.Commits, a.Contributors, top, 100*a.TopShare,
				day(a.TopLastChanged), a.TopIdleDays)
		}
		_ = tw.Flush()
	}

	if o := rep.Overall; o != nil {
		parts := make([]string, 0, len(o.Top))
		for _, c := range o.Top {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", c.Name, 100*c.Share))
		}
		scope := "Every commit read that changed a file"
		if o.Path != "" {
			scope = "Every commit read that changed a file under " + o.Path
		}
		fmt.Fprintf(out, "\n%s\n", wrapText(fmt.Sprintf(
			"%s, taken together: bus factor %d across %d commit(s) by %d name(s) (%s).",
			scope, o.BusFactor, o.Commits, o.Contributors, strings.Join(parts, ", ")), 100))
	}
	if len(rep.Areas) > 0 {
		fmt.Fprintf(out, "\n%s\n", wrapText("IDLE is the days between the top contributor's last change there "+
			"and the newest commit read, not today.", 100))
	}

	for _, n := range rep.Notes {
		fmt.Fprintf(out, "\n%s\n", wrapText(n, 100))
	}

	fmt.Fprintf(out, "\nWHAT THIS DOES NOT SAY\n\n")
	for _, c := range rep.Caveats {
		fmt.Fprintf(out, "  - %s\n", strings.ReplaceAll(wrapText(c, 96), "\n", "\n    "))
	}
}
