package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	acquiregit "github.com/sutrakam/graphlume/internal/acquire/git"
	"github.com/sutrakam/graphlume/internal/capability/architecture"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/policy"
	policycheck "github.com/sutrakam/graphlume/internal/policy/check"
	"github.com/sutrakam/graphlume/internal/report"
	"github.com/sutrakam/graphlume/pkg/model"
)

// newCICmd builds the 'graphlume ci' command suite for CI/CD pipelines.
func newCICmd(g *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Continuous Integration gates, PR blast-radius checks and SARIF export",
		Long: `Continuous Integration automated safety gates and pull-request checkers.

  graphlume ci check --base origin/main
  graphlume ci check --base origin/main --format sarif --out graphlume.sarif
  graphlume ci check --base main --head HEAD --format markdown --out pr-comment.md
  graphlume ci check --fail-on-violations`,
	}

	cmd.AddCommand(newCICheckCmd(g))
	return cmd
}

func newCICheckCmd(g *Global) *cobra.Command {
	var (
		base             string
		head             string
		format           string
		outPath          string
		failOnViolations bool
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Analyze PR blast-radius, verify architectural rules, and emit Markdown or SARIF",
		Long: `Inspects the pull-request commit diff against a base branch, evaluates blast radius,
audits architectural policy rules declared in .graphlume.yml, and emits a PR comment or SARIF 2.1.0 report.

Exit status is 0 when checks pass. If --fail-on-violations is set and architectural boundary
violations are found, exit status is 2. The same applies to error-severity violations of the policy: block
in .graphlume.yml (see 'graphlume policy explain'), which are also added to the Markdown and SARIF
output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			root := g.Config().RepoRoot

			repo, err := acquiregit.Open(root)
			if err != nil {
				return fmt.Errorf("ci check: open repository at %s: %w", root, err)
			}
			if !repo.IsRepo() {
				return fmt.Errorf("ci check: %s is not a git repository", root)
			}

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

			diffRep, err := analyzeDiff(ctx, g, s, repo, base, head, impact.DefaultMinConfidence)
			if err != nil {
				return fmt.Errorf("ci check: analyze diff between %s and %s: %w", base, head, err)
			}

			// Check architecture rules if model declared
			var archRep *architecture.Report
			cfg := g.Config()
			if aRep, aErr := architecture.Check(ctx, s.reg, s.runID, cfg.Architecture, model.ConfidenceLow); aErr == nil {
				archRep = &aRep
			} else if !errors.Is(aErr, architecture.ErrNoModel) {
				return fmt.Errorf("ci check: architecture check failed: %w", aErr)
			}

			// The policy: block, when there is one, is evaluated here too so a
			// CI user gets it without a second command. The diff and the
			// architecture report computed above are handed over rather than
			// recomputed.
			var policyRes *policycheck.Result
			if !cfg.Policy.Empty() {
				res, perr := evaluatePolicy(ctx, g, s, repo, base, head, &precomputed{
					diff: &diffRep, diffFloor: impact.DefaultMinConfidence, arch: archRep,
				})
				if perr != nil {
					return fmt.Errorf("ci check: policy: %w", perr)
				}
				policyRes = &res
			}

			prInput := report.PRInput{
				Impact: &diffRep,
				Arch:   archRep,
				Policy: policyForPR(policyRes),
			}

			formatLower := strings.ToLower(strings.TrimSpace(format))
			var outputBytes []byte

			switch formatLower {
			case "sarif":
				var findings []report.Finding
				if archRep != nil {
					findings = append(findings, report.FromViolations(archRep.Violations)...)
					findings = append(findings, report.FromDataViolations(archRep.DataViolations)...)
				}
				var sarifLog report.SARIFLog
				if policyRes != nil {
					sarifLog = policySARIFLog(*policyRes, findings)
				} else {
					sarifLog = report.BuildSARIF(Version, findings)
				}
				outputBytes, err = json.MarshalIndent(sarifLog, "", "  ")
				if err != nil {
					return fmt.Errorf("ci check: encode SARIF: %w", err)
				}
				outputBytes = append(outputBytes, '\n')

			case "json":
				outputBytes, err = json.MarshalIndent(prInput, "", "  ")
				if err != nil {
					return fmt.Errorf("ci check: encode JSON: %w", err)
				}
				outputBytes = append(outputBytes, '\n')

			default: // "markdown" or "md"
				md := report.RenderPR(prInput)
				outputBytes = []byte(md)
			}

			if outPath != "" {
				if err := os.WriteFile(outPath, outputBytes, 0o600); err != nil {
					return fmt.Errorf("ci check: write output %s: %w", outPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "CI check report written to %s\n", outPath)
			} else {
				_, _ = cmd.OutOrStdout().Write(outputBytes)
			}

			// When the policy has an architecture rule, its severity decides
			// whether architecture violations fail the build, so the older
			// unconditional gate below stands down: otherwise a rule set to
			// severity: warning would still exit 2.
			if failOnViolations && archRep != nil && len(archRep.Violations) > 0 &&
				(policyRes == nil || cfg.Policy.Architecture == nil) {
				return &ExitCodeError{
					Code: 2,
					Err:  fmt.Errorf("architectural policy violation: %d boundary violation(s) detected", len(archRep.Violations)),
				}
			}
			if failOnViolations && policyRes != nil && policyRes.Failed() {
				return &ExitCodeError{
					Code: 2,
					Err:  fmt.Errorf("policy violation: %d error-severity violation(s) of the policy: block", policyRes.Count(policy.SeverityError)),
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&base, "base", "", "base git revision (default: origin/main, main, or master)")
	cmd.Flags().StringVar(&head, "head", "HEAD", "head git revision of the PR")
	cmd.Flags().StringVar(&format, "format", "markdown", "output format: 'markdown', 'sarif', or 'json'")
	cmd.Flags().StringVar(&outPath, "out", "", "write report to file instead of stdout")
	cmd.Flags().BoolVar(&failOnViolations, "fail-on-violations", false, "fail with exit code 2 if architectural or error-severity policy violations are found")

	return cmd
}
