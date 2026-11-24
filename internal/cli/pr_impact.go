package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	acquiregit "github.com/sutrakam/graphlume/internal/acquire/git"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/graph"
	"github.com/sutrakam/graphlume/internal/report"
)

func newPRImpactCmd(g *Global) *cobra.Command {
	var base string
	var head string
	var outPath string

	cmd := &cobra.Command{
		Use:   "pr-impact",
		Short: "Direct PR & commit diff impact analysis and review dashboard",
		Long: `Inspects a git range (such as a pull request branch) against a base branch,
maps modified line spans to indexed symbols, and renders an immediate PR impact
dashboard in GitHub-ready Markdown or JSON.

  graphlume pr-impact
  graphlume pr-impact --base main
  graphlume pr-impact --base origin/main --head feature-branch --out comment.md
  graphlume pr-impact --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			root := g.Config().RepoRoot

			repo, err := acquiregit.Open(root)
			if err != nil {
				return fmt.Errorf("pr-impact: open repository at %s: %w", root, err)
			}
			if !repo.IsRepo() {
				return fmt.Errorf("pr-impact: %s is not a git repository", root)
			}

			// Resolve default base branch if not explicitly provided
			if base == "" {
				base = resolveDefaultBase(ctx, repo)
			}
			if head == "" {
				head = "HEAD"
			}

			s, err := openQuery(ctx, g)
			if err != nil {
				return err
			}
			defer s.close()

			opts := impact.Options{
				MinConfidence: impact.DefaultMinConfidence,
			}

			deps := capability.NewDeps(s.reg, graph.NewForRun(s.db, s.runID))
			rep, err := impact.AnalyzeDiff(ctx, deps, s.runID, impact.DiffRequest{
				Changes:   repo,
				BaseRev:   base,
				HeadRev:   head,
				Runs:      s.reg,
				Revisions: repo,
				IndexRoot: canonicalRoot(root),
				Options:   opts,
			})
			if err != nil {
				return fmt.Errorf("pr-impact: analyze diff between %s and %s: %w", base, head, err)
			}

			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), rep)
			}

			md := report.RenderPR(report.PRInput{
				Impact: &rep,
			})

			if outPath != "" {
				if err := os.WriteFile(outPath, []byte(md), 0o600); err != nil {
					return fmt.Errorf("pr-impact: write %s: %w", outPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "PR impact report written to %s\n", outPath)
				return nil
			}

			fmt.Fprint(cmd.OutOrStdout(), md)
			return nil
		},
	}

	cmd.Flags().StringVar(&base, "base", "", "the base revision to compare against (default: origin/main, main, or master)")
	cmd.Flags().StringVar(&head, "head", "HEAD", "the head revision of the pull request")
	cmd.Flags().StringVar(&outPath, "out", "", "write Markdown report to file instead of stdout")

	return cmd
}

func resolveDefaultBase(ctx context.Context, repo *acquiregit.Repo) string {
	candidates := []string{"origin/main", "main", "origin/master", "master", "HEAD~1"}
	for _, c := range candidates {
		if _, err := repo.ResolveRevision(ctx, c); err == nil {
			return c
		}
	}
	return "main"
}
