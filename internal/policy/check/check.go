// Package check evaluates a repository's policy: block against an index and a
// git diff.
//
// It is the evaluation half of policy-as-code; the declarative half (types and
// validation) is internal/policy. Everything the rules need from the outside
// world arrives through Env as plain data or a function, so the package never
// opens a database, runs git or reads the estate itself, and every rule is
// tested against a real indexed repository by the CLI tests and against small
// hand-built inputs here.
//
// # What a violation is allowed to claim
//
// Each violation carries its evidence (file and line where the index has one)
// and the confidence of the weakest fact behind it. A rule that cannot prove
// what its name says states the weaker claim it CAN prove: see no_new_cycles.
package check

import (
	"context"
	"fmt"
	"sort"

	"github.com/sutrakam/graphlume/internal/capability/architecture"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/policy"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Evidence is one place that supports a violation.
type Evidence struct {
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail"`
}

// Violation is one broken rule.
type Violation struct {
	// Rule is the rule type (the YAML key), RuleID its stable SARIF id.
	Rule     string          `json:"rule"`
	RuleID   string          `json:"rule_id"`
	Severity policy.Severity `json:"severity"`
	Message  string          `json:"message"`
	// Path and Line locate the violation when the index knows where it is,
	// in repository-relative form. Empty for a violation about the change as
	// a whole, such as a blast radius.
	Path       string     `json:"path,omitempty"`
	Line       int        `json:"line,omitempty"`
	Confidence string     `json:"confidence,omitempty"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

// Result is everything one evaluation produced.
type Result struct {
	// Evaluated lists the rule types that ran, so an empty violation list is
	// distinguishable from a check that checked nothing.
	Evaluated  []string    `json:"evaluated"`
	Violations []Violation `json:"violations"`
	// Notes are limits on the answer that are not violations: a member that
	// could not be read, a cycle list that was cut. They are printed with the
	// result because a pass that hid them would claim more than was checked.
	Notes []string `json:"notes,omitempty"`
	// ChangedFiles are the diff's paths in repository form, kept so a report
	// can anchor a whole-change violation to something real.
	ChangedFiles []string `json:"changed_files,omitempty"`
	BaseRev      string   `json:"base_rev,omitempty"`
	HeadRev      string   `json:"head_rev,omitempty"`
}

// Count returns how many violations have the given severity.
func (r Result) Count(s policy.Severity) int {
	n := 0
	for _, v := range r.Violations {
		if v.Severity == s {
			n++
		}
	}
	return n
}

// Failed reports whether any error-severity violation exists. Warnings never
// fail a check.
func (r Result) Failed() bool { return r.Count(policy.SeverityError) > 0 }

// Registry is the index read surface the cycle rule needs.
type Registry = architecture.Registry

// Env is what Evaluate needs from its caller.
type Env struct {
	Policy policy.Policy
	// Root is the repository root. The estate path is resolved against it.
	Root string

	Reg   Registry
	RunID int64

	// Diff returns the diff report computed with the given confidence floor.
	// A function, not a value, because max_blast_radius may ask for a lower
	// floor than the default, and the caller knows how to run the analysis
	// (and can hand back one it already has when the floors agree).
	Diff func(ctx context.Context, floor model.Confidence) (impact.DiffReport, error)

	// Arch returns the architecture report, or architecture.ErrNoModel.
	Arch func(ctx context.Context) (architecture.Report, error)

	// Contracts compares the estate at the verified absolute path.
	Contracts func(ctx context.Context, estatePath string) (*multirepo.ContractReport, error)

	// LocalIndex is this repository's own index path, so a contract finding
	// whose call sits in this repository can be given a file location, and one
	// in another repository is not given a path that would point at the wrong
	// file here.
	LocalIndex string
}

// Evaluate runs every configured rule. A rule that cannot run returns an
// error rather than a pass: a check that silently skipped itself would be
// worse than no check.
func Evaluate(ctx context.Context, env Env) (Result, error) {
	res := Result{Violations: []Violation{}}
	p := env.Policy

	var diff *impact.DiffReport
	getDiff := func(floor model.Confidence) (impact.DiffReport, error) {
		d, err := env.Diff(ctx, floor)
		if err != nil {
			return d, err
		}
		if diff == nil {
			cp := d
			diff = &cp
			res.BaseRev, res.HeadRev = d.BaseRev, d.HeadRev
			res.ChangedFiles = changedRepoSpace(d)
		}
		return d, nil
	}

	if r := p.MaxBlastRadius; r != nil {
		floor := impact.DefaultMinConfidence
		if r.MinConfidence != "" {
			floor = model.Confidence(r.MinConfidence)
		}
		d, err := getDiff(floor)
		if err != nil {
			return res, fmt.Errorf("max_blast_radius: %w", err)
		}
		res.Evaluated = append(res.Evaluated, policy.RuleMaxBlastRadius)
		res.Violations = append(res.Violations, blastRadius(*r, d, floor)...)
	}

	if r := p.NoNewCycles; r != nil {
		// The cycle rule needs the changed files, which only the diff holds,
		// but not any particular floor: the default is fine.
		d, err := getDiff(impact.DefaultMinConfidence)
		if err != nil {
			return res, fmt.Errorf("no_new_cycles: %w", err)
		}
		vs, notes, err := cycles(ctx, env.Reg, env.RunID, *r, d)
		if err != nil {
			return res, fmt.Errorf("no_new_cycles: %w", err)
		}
		res.Evaluated = append(res.Evaluated, policy.RuleNoNewCycles)
		res.Violations = append(res.Violations, vs...)
		res.Notes = append(res.Notes, notes...)
	}

	if r := p.Architecture; r != nil {
		rep, err := env.Arch(ctx)
		if err != nil {
			return res, fmt.Errorf("architecture: %w", err)
		}
		res.Evaluated = append(res.Evaluated, policy.RuleArchitecture)
		res.Violations = append(res.Violations, archViolations(*r, rep)...)
	}

	if r := p.Contracts; r != nil {
		vs, notes, err := contracts(ctx, env, *r)
		if err != nil {
			return res, fmt.Errorf("contracts: %w", err)
		}
		res.Evaluated = append(res.Evaluated, policy.RuleContracts)
		res.Violations = append(res.Violations, vs...)
		res.Notes = append(res.Notes, notes...)
	}

	sort.SliceStable(res.Violations, func(i, j int) bool {
		// Errors first, so the ones that fail the build are read first; the
		// sort is stable, so rule order and each rule's own order survive.
		return res.Violations[i].Severity == policy.SeverityError && res.Violations[j].Severity != policy.SeverityError
	})
	return res, nil
}

// archViolations turns architecture findings into policy violations at the
// rule's severity. The architecture block's own per-rule severity (high,
// medium, low) is kept in the message, because the policy severity decides
// whether the build fails and the rule's severity still tells a reader how
// much the rule's author cared.
func archViolations(r policy.ArchRule, rep architecture.Report) []Violation {
	sev := r.Severity.Or()
	id := policy.RuleID(policy.RuleArchitecture)
	var out []Violation
	for _, v := range rep.Violations {
		rule := v.RuleID
		if rule == "" {
			rule = "(unnamed rule)"
		}
		out = append(out, Violation{
			Rule: policy.RuleArchitecture, RuleID: id, Severity: sev,
			Message: fmt.Sprintf("architecture rule %s (%s, %s): %s -> %s, %s %s %s",
				rule, v.Severity, v.Verdict, v.From, v.To, v.FromName, v.Kind, v.ToName),
			Path: v.FilePath, Line: v.Line, Confidence: v.Confidence,
			Evidence: []Evidence{{Path: v.FilePath, Line: v.Line, Detail: orDefault(v.Detail, v.Kind+" edge")}},
		})
	}
	for _, v := range rep.DataViolations {
		out2 := Violation{
			Rule: policy.RuleArchitecture, RuleID: id, Severity: sev,
			Message: "architecture data rule " + v.String(),
		}
		for _, s := range v.Path.Steps {
			if s.FilePath != "" {
				out2.Path, out2.Line = s.FilePath, s.Line
				break
			}
		}
		out = append(out, out2)
	}
	return out
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
