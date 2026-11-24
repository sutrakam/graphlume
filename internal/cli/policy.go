package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	acquiregit "github.com/sutrakam/graphlume/internal/acquire/git"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/architecture"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/graph"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/policy"
	policycheck "github.com/sutrakam/graphlume/internal/policy/check"
	"github.com/sutrakam/graphlume/internal/report"
	"github.com/sutrakam/graphlume/pkg/model"
)

// policyExitViolations is the exit status when an error-severity violation
// exists. It matches `ci check --fail-on-violations`, so a pipeline can treat
// the two commands alike: 2 is "the policy said no", 1 is "the tool failed".
const policyExitViolations = 2

// policyContractRows is how many contract findings the comparison may return.
// Large because a policy must see every finding it fails on; a cap that cut
// the list would let a violation past the gate. Truncation is still reported.
const policyContractRows = 5000

func newPolicyCmd(g *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Enforce the policy: rules in .graphlume.yml and emit SARIF for code scanning",
		Long: `Policy-as-code. Rules live in the policy: block of the repository's
.graphlume.yml, next to architecture:, and are checked against the diff and
the index.

  graphlume policy explain             # the rule types, with an example block
  graphlume policy check --base origin/main
  graphlume policy check --format sarif --out policy.sarif`,
	}
	cmd.AddCommand(newPolicyCheckCmd(g), newPolicyExplainCmd())
	return cmd
}

func newPolicyCheckCmd(g *Global) *cobra.Command {
	var base, head, format, outPath string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Evaluate every configured policy rule; exit 2 when an error-severity rule is violated",
		Long: `Evaluates the rules in the policy: block of .graphlume.yml against the change
between --base and --head and the committed index, and prints each violation
with its evidence (file:line where the index has one, and the confidence).

Rules (see 'graphlume policy explain' for an example of each):

  max_blast_radius  fails when the diff puts more symbols or files at risk than
                    the bound, counting dependants reached at or above
                    min_confidence (default medium).
  no_new_cycles     fails when a dependency cycle (level: package, a directory,
                    or file) contains a dependency written in a changed file.
                    Only the head index exists, so this cannot know whether such
                    a cycle also existed at base; it never claims a cycle is new.
                    It reports every cycle the change touches, which includes
                    every cycle the change creates. Cycles elsewhere are ignored.
  architecture      counts violations of the architecture: block as policy
                    violations at the rule's severity.
  contracts         compares the HTTP calls and routes of an estate file inside
                    the repository and fails on the listed finding kinds.

Every rule has severity: error (default) or warning. Warnings are printed and
never change the exit status.

Exit status: 0 when no error-severity rule is violated (and when there is no
policy: block, which is said plainly), 2 when one is, 1 when the check itself
could not run.

--format sarif writes SARIF 2.1.0 for GitHub code scanning. Each rule type is a
SARIF rule (graphlume.policy.max-blast-radius, .no-new-cycles, .architecture,
.contracts). A violation about the change as a whole is anchored to a changed
file, because code scanning drops results that have no location.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := g.Config()
			if cfg.Policy.Empty() {
				return emitNoPolicy(cmd, format, outPath)
			}

			root := cfg.RepoRoot
			repo, err := acquiregit.Open(root)
			if err != nil {
				return fmt.Errorf("policy check: open repository at %s: %w", root, err)
			}
			if !repo.IsRepo() {
				return fmt.Errorf("policy check: %s is not a git repository", root)
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

			res, err := evaluatePolicy(ctx, g, s, repo, base, head, nil)
			if err != nil {
				return fmt.Errorf("policy check: %w", err)
			}
			return writePolicyResult(cmd, res, format, outPath)
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "base git revision (default: origin/main, main, or master)")
	cmd.Flags().StringVar(&head, "head", "HEAD", "head git revision")
	cmd.Flags().StringVar(&format, "format", "text", "output format: 'text', 'json' or 'sarif'")
	cmd.Flags().StringVar(&outPath, "out", "", "write the report to this file instead of stdout")
	return cmd
}

func newPolicyExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain",
		Short: "List the policy rule types, what each means, and an example policy: block",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			var example strings.Builder
			example.WriteString("policy:\n")
			for _, r := range policy.Catalogue() {
				fmt.Fprintf(w, "%s  (%s)\n  %s\n\n", r.Name, r.ID, r.Summary)
				for _, line := range strings.Split(r.Example, "\n") {
					example.WriteString("  " + line + "\n")
				}
			}
			fmt.Fprintln(w, "Every rule also takes severity: error (default) or warning. Warnings are reported")
			fmt.Fprintln(w, "and never fail the check. A rule that is absent is not checked.")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Example, in .graphlume.yml:")
			fmt.Fprintln(w)
			fmt.Fprint(w, example.String())
			return nil
		},
	}
}

// precomputed lets `ci check` hand over what it already computed, so the
// policy does not repeat the diff analysis or the architecture check.
type precomputed struct {
	diff      *impact.DiffReport
	diffFloor model.Confidence
	arch      *architecture.Report
}

// analyzeDiff is the one place the diff request is spelled, shared by
// `ci check` and `policy check` so they cannot drift apart on how a diff is
// read.
func analyzeDiff(ctx context.Context, g *Global, s *session, repo *acquiregit.Repo, base, head string, floor model.Confidence) (impact.DiffReport, error) {
	deps := capability.NewDeps(s.reg, graph.NewForRun(s.db, s.runID))
	return impact.AnalyzeDiff(ctx, deps, s.runID, impact.DiffRequest{
		Changes:   repo,
		BaseRev:   base,
		HeadRev:   head,
		Runs:      s.reg,
		Revisions: repo,
		IndexRoot: canonicalRoot(g.Config().RepoRoot),
		Options:   impact.Options{MinConfidence: floor},
	})
}

// evaluatePolicy runs the configured policy against the open index.
func evaluatePolicy(ctx context.Context, g *Global, s *session, repo *acquiregit.Repo, base, head string, pre *precomputed) (policycheck.Result, error) {
	cfg := g.Config()
	env := policycheck.Env{
		Policy:     cfg.Policy,
		Root:       cfg.RepoRoot,
		Reg:        s.reg,
		RunID:      s.runID,
		LocalIndex: g.DBPath(),
		Diff: func(ctx context.Context, floor model.Confidence) (impact.DiffReport, error) {
			if pre != nil && pre.diff != nil && pre.diffFloor == floor {
				return *pre.diff, nil
			}
			d, err := analyzeDiff(ctx, g, s, repo, base, head, floor)
			if err != nil {
				return d, fmt.Errorf("analyze diff between %s and %s: %w", base, head, err)
			}
			return d, nil
		},
		Arch: func(ctx context.Context) (architecture.Report, error) {
			if pre != nil && pre.arch != nil {
				return *pre.arch, nil
			}
			return architecture.Check(ctx, s.reg, s.runID, cfg.Architecture, model.ConfidenceLow)
		},
		Contracts: func(ctx context.Context, estate string) (*multirepo.ContractReport, error) {
			return policyContracts(ctx, estate)
		},
	}
	return policycheck.Evaluate(ctx, env)
}

// policyContracts opens the estate's members read-only and compares them.
// It uses the same member handling as `system contracts`, so an unreadable
// member is data in the report rather than an error here.
func policyContracts(ctx context.Context, estate string) (*multirepo.ContractReport, error) {
	entries, err := multirepo.LoadEstate(estate)
	if err != nil {
		return nil, err
	}
	entries, err = normalizeMembers(entries)
	if err != nil {
		return nil, err
	}
	opened := &memberCatalogue{byPath: map[string]*openIndex{}}
	defer opened.closeAll()
	members := make([]multirepo.Member, 0, len(entries))
	for _, e := range entries {
		members = append(members, opened.member(ctx, e))
	}
	fed, err := newContractFederator(policyContractRows, 0)
	if err != nil {
		return nil, err
	}
	return fed.Contracts(ctx, members)
}

// policyReport is the JSON shape: the result plus the arithmetic a script
// would otherwise recompute.
type policyReport struct {
	policycheck.Result
	Errors   int  `json:"errors"`
	Warnings int  `json:"warnings"`
	Passed   bool `json:"passed"`
}

func newPolicyReport(res policycheck.Result) policyReport {
	if res.Violations == nil {
		res.Violations = []policycheck.Violation{}
	}
	if res.Evaluated == nil {
		res.Evaluated = []string{}
	}
	return policyReport{
		Result:   res,
		Errors:   res.Count(policy.SeverityError),
		Warnings: res.Count(policy.SeverityWarning),
		Passed:   !res.Failed(),
	}
}

// emitNoPolicy handles a repository with no policy: block: say so, exit 0.
// SARIF and JSON still produce a valid empty document, so a pipeline that
// uploads the file unconditionally does not fail on a repository that has not
// adopted policy yet.
func emitNoPolicy(cmd *cobra.Command, format, outPath string) error {
	const msg = "No policy: block in .graphlume.yml, so there is nothing to check. " +
		"Run 'graphlume policy explain' for the rules and an example."
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json", "sarif":
		return writePolicyResult(cmd, policycheck.Result{}, format, outPath)
	}
	fmt.Fprintln(cmd.OutOrStdout(), msg)
	return nil
}

func writePolicyResult(cmd *cobra.Command, res policycheck.Result, format, outPath string) error {
	var out []byte
	var err error
	switch f := strings.ToLower(strings.TrimSpace(format)); f {
	case "sarif":
		out, err = policySARIF(res)
	case "json":
		out, err = json.MarshalIndent(newPolicyReport(res), "", "  ")
		out = append(out, '\n')
	case "text", "":
		var b strings.Builder
		renderPolicyText(&b, res)
		out = []byte(b.String())
	default:
		return fmt.Errorf("policy check: unknown --format %q: want text, json or sarif", format)
	}
	if err != nil {
		return fmt.Errorf("policy check: encode report: %w", err)
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, out, 0o600); err != nil {
			return fmt.Errorf("policy check: write %s: %w", outPath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Policy report written to %s\n%s\n", outPath, policySummary(res))
	} else {
		_, _ = cmd.OutOrStdout().Write(out)
	}

	if res.Failed() {
		return &ExitCodeError{Code: policyExitViolations, Silent: true,
			Err: fmt.Errorf("policy check: %d error-severity violation(s)", res.Count(policy.SeverityError))}
	}
	return nil
}

func policySummary(res policycheck.Result) string {
	if len(res.Evaluated) == 0 {
		return "policy: no rules were evaluated"
	}
	verdict := "passed"
	if res.Failed() {
		verdict = "FAILED"
	}
	return fmt.Sprintf("policy %s: %d error(s), %d warning(s); rules checked: %s",
		verdict, res.Count(policy.SeverityError), res.Count(policy.SeverityWarning), strings.Join(res.Evaluated, ", "))
}

func renderPolicyText(w io.Writer, res policycheck.Result) {
	if res.BaseRev != "" || res.HeadRev != "" {
		fmt.Fprintf(w, "Policy check, %s..%s, %d changed file(s)\n\n", res.BaseRev, res.HeadRev, len(res.ChangedFiles))
	}
	for _, v := range res.Violations {
		fmt.Fprintf(w, "%-7s %s\n", strings.ToUpper(string(v.Severity)), v.RuleID)
		fmt.Fprintf(w, "        %s\n", v.Message)
		if loc := locText(v.Path, v.Line); loc != "" {
			conf := ""
			if v.Confidence != "" {
				conf = ", " + v.Confidence + " confidence"
			}
			fmt.Fprintf(w, "        at %s%s\n", loc, conf)
		}
		for _, e := range v.Evidence {
			if loc := locText(e.Path, e.Line); loc != "" {
				fmt.Fprintf(w, "        - %s: %s\n", loc, e.Detail)
			} else {
				fmt.Fprintf(w, "        - %s\n", e.Detail)
			}
		}
		fmt.Fprintln(w)
	}
	for _, n := range res.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	if len(res.Notes) > 0 {
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, policySummary(res))
}

func locText(path string, line int) string {
	switch {
	case path == "":
		return ""
	case line > 0:
		return fmt.Sprintf("%s:%d", path, line)
	}
	return path
}

// policySARIF builds the SARIF log with internal/report's writer.
//
// Every violation needs a location or code scanning drops it, and several
// violations are about the change as a whole. Those are anchored to the first
// changed file, and failing that to .graphlume.yml, the file that declares the
// rule; the message still says what the violation really is about.
//
// Every CONFIGURED rule type is listed under tool.driver.rules even when it has
// no results, so the rule ids a repository's alerts will use exist in the log
// from the first run.
func policySARIF(res policycheck.Result) ([]byte, error) {
	b, err := json.MarshalIndent(policySARIFLog(res, nil), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// policyForPR converts the result to the pull-request comment's input type.
func policyForPR(res *policycheck.Result) *report.PolicyResult {
	if res == nil {
		return nil
	}
	out := &report.PolicyResult{Evaluated: res.Evaluated, Notes: res.Notes}
	for _, v := range res.Violations {
		out.Violations = append(out.Violations, report.PolicyViolation{
			RuleID: v.RuleID, Severity: string(v.Severity), Message: v.Message,
			Path: v.Path, Line: v.Line, Confidence: v.Confidence,
		})
	}
	return out
}

// policySARIFLog is policySARIF's log, with extra findings from another
// producer (ci check's architecture findings) merged into the same run.
func policySARIFLog(res policycheck.Result, extra []report.Finding) report.SARIFLog {
	infos := map[string]policy.RuleInfo{}
	for _, r := range policy.Catalogue() {
		infos[r.Name] = r
	}
	anchor := config.RepoConfigName
	if len(res.ChangedFiles) > 0 {
		anchor = res.ChangedFiles[0]
	}
	findings := append([]report.Finding(nil), extra...)
	for _, v := range res.Violations {
		f := report.Finding{
			RuleID:          v.RuleID,
			RuleDescription: infos[v.Rule].Summary,
			Severity:        "high",
			Message:         v.Message,
			Path:            v.Path,
			Line:            v.Line,
			Confidence:      v.Confidence,
		}
		if v.Severity == policy.SeverityWarning {
			f.Severity = "medium" // SARIF level "warning"
		}
		if f.Path == "" {
			f.Path = anchor
		}
		findings = append(findings, f)
	}
	log := report.BuildSARIF(Version, findings)

	run := &log.Runs[0]
	have := map[string]bool{}
	for _, r := range run.Tool.Driver.Rules {
		have[r.ID] = true
	}
	for _, name := range res.Evaluated {
		info := infos[name]
		if !have[info.ID] {
			run.Tool.Driver.Rules = append(run.Tool.Driver.Rules,
				report.SARIFRule{ID: info.ID, ShortDescription: report.SARIFText{Text: info.Summary}})
		}
	}
	sort.Slice(run.Tool.Driver.Rules, func(i, j int) bool { return run.Tool.Driver.Rules[i].ID < run.Tool.Driver.Rules[j].ID })

	if len(res.Notes) > 0 {
		inv := report.SARIFInvocation{ExecutionSuccessful: true}
		for _, n := range res.Notes {
			inv.ToolExecutionNotifications = append(inv.ToolExecutionNotifications,
				report.SARIFNotification{Level: "note", Message: report.SARIFText{Text: n}})
		}
		run.Invocations = append(run.Invocations, inv)
	}

	return log
}
