// Package policy is the declarative half of policy-as-code: the `policy:`
// block of .graphlume.yml, its validation, and the catalogue of rule types.
//
// Evaluation lives in internal/policy/check. The split exists because the
// repository config is decoded by internal/config, which internal/multirepo
// already imports; a config package that imported the evaluator (which needs
// multirepo for the contract rule) would be an import cycle. Keeping only data
// and validation here lets config depend on this package the same way it
// depends on internal/arch.
//
// # Why this is safe in untrusted config
//
// .graphlume.yml comes from the repository, so everything in it is attacker
// supplied when you clone someone else's code. The policy block can only ADD
// checks: every field is a number, an enum, or a repository-relative path that
// Validate keeps lexically inside the repository (and check re-verifies after
// resolving symlinks). Nothing here names a command, a URL or a file to write,
// so the worst a hostile policy can do is make `policy check` fail, which is
// the one thing a policy is for.
package policy

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Severity says whether a violation fails the check.
type Severity string

const (
	// SeverityError violations make `policy check` exit 2.
	SeverityError Severity = "error"
	// SeverityWarning violations are reported and never change the exit code,
	// so a team can introduce a rule before it is enforced.
	SeverityWarning Severity = "warning"
)

// Or returns s, or SeverityError when s is unset. Error is the default because
// a rule somebody wrote is a rule they expect to hold; a quiet default would
// make the first rule they add look enforced while enforcing nothing.
func (s Severity) Or() Severity {
	if s == "" {
		return SeverityError
	}
	return s
}

// Rule type names, which are also the YAML keys.
const (
	RuleMaxBlastRadius = "max_blast_radius"
	RuleNoNewCycles    = "no_new_cycles"
	RuleArchitecture   = "architecture"
	RuleContracts      = "contracts"
)

// Cycle levels for no_new_cycles.
const (
	LevelPackage = "package"
	LevelFile    = "file"
)

// Contract kinds, spelled exactly as multirepo.ContractKind. They are repeated
// here, rather than imported, for the cycle reason in the package comment;
// check's tests assert they stay equal to multirepo's constants.
var ContractKinds = []string{
	"method_mismatch", "path_variable_mismatch", "no_matching_route", "base_path_unverified",
}

// DefaultContractFailOn is what `contracts` fails on when fail_on is omitted:
// the two kinds that have a route on the other side, because those are the
// findings a reader can verify by opening two files. no_matching_route and
// base_path_unverified are far more often a client of something outside the
// estate than a bug, and have to be asked for.
var DefaultContractFailOn = []string{"method_mismatch", "path_variable_mismatch"}

// Policy is the `policy:` block. A nil rule is a rule that is not checked.
type Policy struct {
	MaxBlastRadius *MaxBlastRadius `yaml:"max_blast_radius" json:"max_blast_radius,omitempty"`
	NoNewCycles    *NoNewCycles    `yaml:"no_new_cycles" json:"no_new_cycles,omitempty"`
	Architecture   *ArchRule       `yaml:"architecture" json:"architecture,omitempty"`
	Contracts      *Contracts      `yaml:"contracts" json:"contracts,omitempty"`
}

// MaxBlastRadius fails when a diff's impacted set exceeds a bound.
type MaxBlastRadius struct {
	// Symbols and Files are pointers so that an omitted bound ("do not check
	// this") is distinguishable from a bound of zero ("nothing may be
	// impacted"), which is a legitimate and strict policy.
	Symbols       *int     `yaml:"symbols" json:"symbols,omitempty"`
	Files         *int     `yaml:"files" json:"files,omitempty"`
	MinConfidence string   `yaml:"min_confidence" json:"min_confidence,omitempty"`
	Severity      Severity `yaml:"severity" json:"severity,omitempty"`
}

// NoNewCycles fails when a dependency cycle runs through the change.
type NoNewCycles struct {
	Level    string   `yaml:"level" json:"level,omitempty"`
	Severity Severity `yaml:"severity" json:"severity,omitempty"`
}

// ArchRule promotes the architecture: block's violations to policy
// violations at one severity.
type ArchRule struct {
	Severity Severity `yaml:"severity" json:"severity,omitempty"`
}

// Contracts runs the cross-repository contract comparison.
type Contracts struct {
	// Estate is a repository-relative path to an estate file.
	Estate   string   `yaml:"estate" json:"estate,omitempty"`
	FailOn   []string `yaml:"fail_on" json:"fail_on,omitempty"`
	Severity Severity `yaml:"severity" json:"severity,omitempty"`
}

// Empty reports whether no rule is configured.
func (p Policy) Empty() bool {
	return p.MaxBlastRadius == nil && p.NoNewCycles == nil && p.Architecture == nil && p.Contracts == nil
}

// Configured lists the configured rule types in catalogue order.
func (p Policy) Configured() []string {
	var out []string
	if p.MaxBlastRadius != nil {
		out = append(out, RuleMaxBlastRadius)
	}
	if p.NoNewCycles != nil {
		out = append(out, RuleNoNewCycles)
	}
	if p.Architecture != nil {
		out = append(out, RuleArchitecture)
	}
	if p.Contracts != nil {
		out = append(out, RuleContracts)
	}
	return out
}

// ValidationError carries every problem found in one policy block, so a user
// fixes the file once rather than once per run.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d problems:", len(e.Problems))
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

// Validate reports every problem in the block, or nil. An empty policy is
// valid: policy checking is opt-in.
func (p Policy) Validate() error {
	var problems []string
	sev := func(rule string, s Severity) {
		switch s {
		case "", SeverityError, SeverityWarning:
		default:
			problems = append(problems, fmt.Sprintf("policy.%s.severity %q: want error or warning", rule, s))
		}
	}

	if r := p.MaxBlastRadius; r != nil {
		sev(RuleMaxBlastRadius, r.Severity)
		if r.Symbols == nil && r.Files == nil {
			problems = append(problems, "policy.max_blast_radius: set symbols, files or both; a rule with no bound checks nothing")
		}
		if r.Symbols != nil && *r.Symbols < 0 {
			problems = append(problems, fmt.Sprintf("policy.max_blast_radius.symbols %d: must be >= 0", *r.Symbols))
		}
		if r.Files != nil && *r.Files < 0 {
			problems = append(problems, fmt.Sprintf("policy.max_blast_radius.files %d: must be >= 0", *r.Files))
		}
		switch r.MinConfidence {
		case "", "high", "medium", "low":
		default:
			problems = append(problems, fmt.Sprintf("policy.max_blast_radius.min_confidence %q: want high, medium or low", r.MinConfidence))
		}
	}

	if r := p.NoNewCycles; r != nil {
		sev(RuleNoNewCycles, r.Severity)
		switch r.Level {
		case "", LevelPackage, LevelFile:
		default:
			problems = append(problems, fmt.Sprintf("policy.no_new_cycles.level %q: want package or file", r.Level))
		}
	}

	if r := p.Architecture; r != nil {
		sev(RuleArchitecture, r.Severity)
	}

	if r := p.Contracts; r != nil {
		sev(RuleContracts, r.Severity)
		if msg := EstatePathProblem(r.Estate); msg != "" {
			problems = append(problems, "policy.contracts.estate "+fmt.Sprintf("%q", r.Estate)+": "+msg)
		}
		known := map[string]bool{}
		for _, k := range ContractKinds {
			known[k] = true
		}
		for i, k := range r.FailOn {
			if !known[k] {
				problems = append(problems, fmt.Sprintf("policy.contracts.fail_on[%d] %q: want one of %s", i, k, strings.Join(ContractKinds, ", ")))
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: problems}
}

// EstatePathProblem returns why an estate path named by a repository's own
// config is unacceptable, or "". It is the rule for every estate path
// .graphlume.yml can name: policy.contracts.estate and the top-level estate:.
//
// The test is lexical because Validate has no filesystem to consult; check
// repeats it after resolving symlinks, which is the half a string cannot see.
// Both are needed: an untrusted config must not be able to aim the tool at
// ../../somewhere/else, nor at a symlink inside the repository that leads
// there.
func EstatePathProblem(p string) string {
	if strings.TrimSpace(p) == "" {
		return "is required; name the estate file, relative to the repository root"
	}
	slash := filepath.ToSlash(p)
	if filepath.IsAbs(p) || strings.HasPrefix(slash, "/") || filepath.VolumeName(p) != "" {
		return "must be relative to the repository root, not absolute"
	}
	clean := path.Clean(slash)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "escapes the repository root; a repository's config may only name files inside it"
	}
	if clean == "." {
		return "names the repository root, not a file"
	}
	return ""
}

// RuleInfo describes one rule type for `policy explain` and for SARIF.
type RuleInfo struct {
	// Name is the YAML key.
	Name string
	// ID is the stable SARIF rule id. It is part of the public surface: code
	// scanning dismissals and alerts are keyed on it, so it never changes.
	ID      string
	Summary string
	Example string
}

// Catalogue is every rule type, in the order they are evaluated and listed.
func Catalogue() []RuleInfo {
	return []RuleInfo{
		{
			Name: RuleMaxBlastRadius, ID: "graphlume.policy.max-blast-radius",
			Summary: "fails when the diff puts more symbols or files at risk than the bound, counting only dependants reached at or above min_confidence",
			Example: "max_blast_radius:\n  symbols: 200\n  files: 40\n  min_confidence: medium   # high | medium | low (default medium)",
		},
		{
			Name: RuleNoNewCycles, ID: "graphlume.policy.no-new-cycles",
			Summary: "fails when a dependency cycle contains an edge written in a changed file; a cycle that was already there and merely passes through changed code is reported too, since only one index exists",
			Example: "no_new_cycles:\n  level: package           # package (a directory) | file (default package)",
		},
		{
			Name: RuleArchitecture, ID: "graphlume.policy.architecture",
			Summary: "counts every violation of the architecture: block's rules as a policy violation at this severity",
			Example: "architecture:\n  severity: error          # error | warning",
		},
		{
			Name: RuleContracts, ID: "graphlume.policy.contracts",
			Summary: "compares this estate's HTTP calls with the routes its members declare and fails on the listed finding kinds",
			Example: "contracts:\n  estate: ci/estate.yml    # inside the repository\n  fail_on: [method_mismatch, path_variable_mismatch]",
		},
	}
}

// RuleID returns the SARIF id of a rule type, or "" for an unknown one.
func RuleID(name string) string {
	for _, r := range Catalogue() {
		if r.Name == name {
			return r.ID
		}
	}
	return ""
}

// SortedKinds returns kinds sorted and de-duplicated, for stable output.
func SortedKinds(kinds []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
