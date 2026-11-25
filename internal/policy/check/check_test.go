package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/policy"
	"github.com/sutrakam/graphlume/pkg/model"
)

// policy cannot import multirepo (config, which policy feeds, is imported by
// it), so the contract kinds are spelled twice. This is the test that keeps
// the two spellings from drifting.
func TestContractKindsMatchMultirepo(t *testing.T) {
	want := []multirepo.ContractKind{
		multirepo.ContractMethodMismatch, multirepo.ContractPathVariables,
		multirepo.ContractNoRoute, multirepo.ContractBasePath,
	}
	if len(want) != len(policy.ContractKinds) {
		t.Fatalf("policy knows %d contract kinds, multirepo %d", len(policy.ContractKinds), len(want))
	}
	for i, k := range want {
		if string(k) != policy.ContractKinds[i] {
			t.Errorf("kind %d: policy %q, multirepo %q", i, policy.ContractKinds[i], k)
		}
	}
}

func impacted(id int64, file string, conf model.Confidence) impact.Impacted {
	return impact.Impacted{
		Symbol: capability.Symbol{ID: id, Name: "S", Kind: "function", FilePath: file},
		Depth:  1, Confidence: conf,
	}
}

func TestBlastRadiusCountsOnlyAtOrAboveTheFloor(t *testing.T) {
	two := 2
	d := impact.DiffReport{
		ChangedFiles: []string{"x.go"},
		Impacted: []impact.Impacted{
			impacted(1, "a.go", model.ConfidenceHigh),
			impacted(2, "b.go", model.ConfidenceMedium),
			impacted(3, "b.go", model.ConfidenceLow),
			impacted(4, "c.go", model.ConfidenceLow),
		},
	}
	r := policy.MaxBlastRadius{Symbols: &two, Files: &two}

	if vs := blastRadius(r, d, model.ConfidenceMedium); len(vs) != 0 {
		t.Errorf("two symbols in two files are within a bound of 2: %+v", vs)
	}
	vs := blastRadius(r, d, model.ConfidenceLow)
	if len(vs) != 2 {
		t.Fatalf("four symbols in three files exceed both bounds, got %d violations", len(vs))
	}
	if vs[0].Path != "x.go" || vs[0].Severity != policy.SeverityError {
		t.Errorf("a whole-change violation is anchored to a changed file at the default severity: %+v", vs[0])
	}

	d.Truncated = true
	if vs := blastRadius(r, d, model.ConfidenceLow); !strings.Contains(vs[0].Message, "at least 4") {
		t.Errorf("a truncated count is a lower bound and must say so: %s", vs[0].Message)
	}
}

func TestBlastRadiusZeroBoundIsStrictAndOmittedBoundIsNotChecked(t *testing.T) {
	zero := 0
	d := impact.DiffReport{Impacted: []impact.Impacted{impacted(1, "a.go", model.ConfidenceHigh)}}
	if len(blastRadius(policy.MaxBlastRadius{Symbols: &zero}, d, model.ConfidenceMedium)) != 1 {
		t.Error("a bound of 0 must fail when anything is impacted")
	}
	if len(blastRadius(policy.MaxBlastRadius{Files: &zero}, d, model.ConfidenceMedium)) != 1 {
		t.Error("files: 0 must fail when a file is impacted")
	}
	if len(blastRadius(policy.MaxBlastRadius{Files: &zero}, impact.DiffReport{}, model.ConfidenceMedium)) != 0 {
		t.Error("nothing impacted must pass any bound")
	}
}

func TestShortestPathStaysInsideTheComponentAndIsDeterministic(t *testing.T) {
	adj := map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}, "d": {"a"}, "x": {"a"}}
	comp := map[string]int{"a": 1, "b": 1, "c": 1, "d": 1, "x": 2}
	got := strings.Join(shortestPath(adj, comp, "a", "d"), ">")
	if got != "a>b>d" {
		t.Errorf("path = %s, want a>b>d (sorted neighbours break the tie)", got)
	}
	if shortestPath(adj, comp, "a", "x") != nil {
		t.Error("a path must not leave the component")
	}
}

func TestCanonicalCycleIgnoresWhereItStarts(t *testing.T) {
	if canonical([]string{"b", "c", "a"}) != canonical([]string{"a", "b", "c"}) {
		t.Error("the same cycle found from different edges must have one key")
	}
	if canonical([]string{"a", "b", "c"}) == canonical([]string{"a", "c", "b"}) {
		t.Error("a different direction is a different cycle")
	}
}

func TestResolveEstateRefusesSymlinksOutOfTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "e.yml")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.yml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.yml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveEstate(root, "ok.yml"); err != nil {
		t.Errorf("a file inside the root must resolve: %v", err)
	}
	if _, err := ResolveEstate(root, "link.yml"); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("a symlink leaving the root must be refused, got %v", err)
	}
	if _, err := ResolveEstate(root, "missing.yml"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a missing estate must say so, got %v", err)
	}
}

func TestSeverityDefaultsToErrorAndWarningsDoNotFail(t *testing.T) {
	if policy.Severity("").Or() != policy.SeverityError {
		t.Error("an unset severity must be error")
	}
	res := Result{Violations: []Violation{{Severity: policy.SeverityWarning}}}
	if res.Failed() {
		t.Error("a warning must not fail the check")
	}
	res.Violations = append(res.Violations, Violation{Severity: policy.SeverityError})
	if !res.Failed() {
		t.Error("an error must fail the check")
	}
}
