package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// loadPolicy writes a repo config and loads it, returning the error text too.
func loadPolicy(t *testing.T, yaml string) (Config, error) {
	t.Helper()
	repo := t.TempDir()
	t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())
	writeFile(t, filepath.Join(repo, RepoConfigName), yaml)
	return Load(repo)
}

func TestPolicyBlockLoads(t *testing.T) {
	cfg, err := loadPolicy(t, `
architecture:
  components:
    - name: a
      include: ["a/**"]
policy:
  max_blast_radius: {symbols: 100, files: 0, min_confidence: high, severity: warning}
  no_new_cycles: {level: file}
  architecture: {severity: error}
  contracts: {estate: ci/estate.yml, fail_on: [method_mismatch]}
`)
	if err != nil {
		t.Fatalf("a valid policy block must load: %v", err)
	}
	p := cfg.Policy
	if p.MaxBlastRadius == nil || *p.MaxBlastRadius.Symbols != 100 || p.MaxBlastRadius.Files == nil || *p.MaxBlastRadius.Files != 0 {
		t.Errorf("max_blast_radius not decoded, and a bound of 0 must stay distinguishable from no bound: %+v", p.MaxBlastRadius)
	}
	if p.NoNewCycles == nil || p.NoNewCycles.Level != "file" {
		t.Errorf("no_new_cycles = %+v", p.NoNewCycles)
	}
	if p.Contracts == nil || p.Contracts.Estate != "ci/estate.yml" {
		t.Errorf("contracts = %+v", p.Contracts)
	}
	if len(p.Configured()) != 4 {
		t.Errorf("configured = %v", p.Configured())
	}
}

func TestNoPolicyBlockIsEmpty(t *testing.T) {
	cfg, err := loadPolicy(t, "project:\n  name: x\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Policy.Empty() {
		t.Errorf("a repository with no policy: block must have an empty policy: %+v", cfg.Policy)
	}
}

// Every bad case is refused, and several problems in one file are all
// reported together rather than one per run.
func TestPolicyValidationReportsEveryProblem(t *testing.T) {
	_, err := loadPolicy(t, `
policy:
  max_blast_radius: {symbols: -1, files: -5, min_confidence: certain, severity: fatal}
  no_new_cycles: {level: module}
  architecture: {severity: loud}
  contracts: {estate: ../outside.yml, fail_on: [method_mismatch, bogus_kind]}
`)
	if err == nil {
		t.Fatal("a policy with every field wrong must not load")
	}
	msg := err.Error()
	for _, want := range []string{
		"max_blast_radius.symbols -1",
		"max_blast_radius.files -5",
		"min_confidence \"certain\"",
		"max_blast_radius.severity \"fatal\"",
		"no_new_cycles.level \"module\"",
		"policy.architecture.severity \"loud\"",
		"escapes the repository root",
		"fail_on[1] \"bogus_kind\"",
		// architecture needs the architecture: block; reported with the rest.
		"no architecture: block",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q; every problem must be reported at once:\n%s", want, msg)
		}
	}
}

func TestPolicyRejectsAnUnknownRule(t *testing.T) {
	_, err := loadPolicy(t, "policy:\n  run_command: {cmd: rm -rf /}\n")
	if err == nil {
		t.Fatal("an unknown rule must be rejected by the strict decoder, not ignored")
	}
}

func TestPolicyRejectsRulesWithNoBound(t *testing.T) {
	_, err := loadPolicy(t, "policy:\n  max_blast_radius: {min_confidence: high}\n")
	if err == nil || !strings.Contains(err.Error(), "checks nothing") {
		t.Fatalf("a blast-radius rule with no bound must be refused, got %v", err)
	}
}

func TestPolicyEstatePathMustStayInsideTheRepository(t *testing.T) {
	for _, estate := range []string{"/etc/estate.yml", "../estate.yml", "a/../../estate.yml", "", "."} {
		_, err := loadPolicy(t, "policy:\n  contracts: {estate: \""+estate+"\"}\n")
		if err == nil {
			t.Errorf("estate %q must be refused", estate)
		}
	}
	if _, err := loadPolicy(t, "policy:\n  contracts: {estate: ci/../ci/estate.yml}\n"); err != nil {
		t.Errorf("a path that stays inside the repository must be accepted: %v", err)
	}
}
