package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive `graphlume policy check` and `ci check` over real
// repositories: a git history with a base and a head commit, indexed at the
// head. Everything a policy rule decides comes from the index and the diff, so
// a stub would prove only that the stub agrees with itself.

// policyRepo is an impactRepo with its policy: block written to
// .graphlume.yml. The config is read on every command, so it can be written
// after the commits and the index exist.
type policyRepo struct {
	*impactRepo
}

func (r policyRepo) setConfig(t *testing.T, yaml string) {
	t.Helper()
	r.write(t, ".graphlume.yml", yaml)
}

// exitCode runs a command and returns its output and exit status the way the
// binary would: 0 for nil, the code of an ExitCodeError, 1 for anything else.
func exitCode(t *testing.T, root string, args ...string) (string, int) {
	t.Helper()
	out, err := run(t, root, args...)
	if err == nil {
		return out, 0
	}
	var ec *ExitCodeError
	if errors.As(err, &ec) {
		return out, ec.Code
	}
	return out + "\n" + err.Error(), 1
}

// blastRepo: svc/repo.go changes at HEAD and svc/payment.go depends on it, so
// the diff has dependants. Indexed at HEAD.
func blastRepo(t *testing.T) policyRepo {
	t.Helper()
	r := newImpactRepo(t)
	r.write(t, "svc/repo.go", repoGo)
	r.write(t, "svc/payment.go", paymentGo)
	r.commit(t, "add the service", "svc/repo.go", "svc/payment.go")
	r.write(t, "svc/repo.go", repoGo+"\nfunc (r *Repo) Delete(id string) error { return nil }\n")
	r.commit(t, "add Delete", "svc/repo.go")
	r.index(t)
	return policyRepo{r}
}

func TestPolicyCheckWithoutABlockSaysSoAndPasses(t *testing.T) {
	r := blastRepo(t)
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 0 {
		t.Fatalf("no policy: block must exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "No policy: block") {
		t.Errorf("the message must say plainly that nothing was checked:\n%s", out)
	}

	// A pipeline that uploads the SARIF unconditionally must still get a
	// valid, empty document.
	out, code = exitCode(t, r.root, "policy", "check", "--base", "HEAD~1", "--format", "sarif")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var log map[string]any
	if err := json.Unmarshal([]byte(out), &log); err != nil {
		t.Fatalf("empty-policy SARIF is not JSON: %v\n%s", err, out)
	}
}

func TestPolicyBlastRadiusFailsOverTheBoundAndPassesUnderIt(t *testing.T) {
	r := blastRepo(t)

	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 0}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 2 {
		t.Fatalf("a diff with dependants must break a bound of 0 symbols (exit 2), got %d\n%s", code, out)
	}
	for _, want := range []string{"graphlume.policy.max-blast-radius", "over the bound of 0", "svc/repo.go", "ERROR"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}

	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 1000, files: 1000}\n")
	if out, code = exitCode(t, r.root, "policy", "check", "--base", "HEAD~1"); code != 0 {
		t.Fatalf("a generous bound must pass, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "passed") {
		t.Errorf("a pass must say which rules ran:\n%s", out)
	}

	// The files bound is checked on its own.
	r.setConfig(t, "policy:\n  max_blast_radius: {files: 0}\n")
	if out, code = exitCode(t, r.root, "policy", "check", "--base", "HEAD~1"); code != 2 {
		t.Fatalf("files: 0 must fail, got %d\n%s", code, out)
	}
}

func TestPolicyMinConfidenceDecidesWhatIsCounted(t *testing.T) {
	r := blastRepo(t)
	count := func(min string) int {
		t.Helper()
		r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 0, min_confidence: "+min+", severity: warning}\n")
		out, _ := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1", "--format", "json")
		var rep struct {
			Violations []struct{ Message string } `json:"violations"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("json: %v\n%s", err, out)
		}
		return len(rep.Violations)
	}
	// Counting at low sees every dependant, so a bound of 0 is exceeded. The
	// message names the floor it counted at, so a reader can tell which of
	// the two answers they are looking at.
	if count("low") != 1 {
		t.Error("counting at low confidence must see the dependant and exceed a bound of 0")
	}
	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 0, min_confidence: low}\n")
	out, _ := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if !strings.Contains(out, "low confidence or better") {
		t.Errorf("the violation must state the floor it counted at:\n%s", out)
	}
}

func TestPolicyWarningNeverFailsTheCheck(t *testing.T) {
	r := blastRepo(t)
	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 0, severity: warning}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 0 {
		t.Fatalf("a warning must not fail the check, got exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "0 error(s), 1 warning(s)") {
		t.Errorf("the warning must still be reported:\n%s", out)
	}
}

// cycleRepo: package a calls package b at both commits; at HEAD, package b
// starts calling package a, closing a package cycle in a changed file.
// Package c has a cycle of its own, which exists at both commits and is not
// touched by the change.
func cycleRepo(t *testing.T) policyRepo {
	t.Helper()
	r := newImpactRepo(t)
	r.write(t, "go.mod", "module m\n\ngo 1.22\n")
	r.write(t, "a/a.go", "package a\n\nimport \"m/b\"\n\nfunc A() { b.B() }\n")
	r.write(t, "b/b.go", "package b\n\nfunc B() {}\n")
	r.write(t, "c/c.go", "package c\n\nimport \"m/d\"\n\nfunc C() { d.D() }\n")
	r.write(t, "d/d.go", "package d\n\nimport \"m/c\"\n\nfunc D() { c.C() }\n")
	r.write(t, "e/e.go", "package e\n\nfunc E() {}\n")
	r.commit(t, "base", "go.mod", "a/a.go", "b/b.go", "c/c.go", "d/d.go", "e/e.go")

	r.write(t, "b/b.go", "package b\n\nimport \"m/a\"\n\nfunc B() { a.A() }\n")
	r.commit(t, "b calls a", "b/b.go")
	r.index(t)
	return policyRepo{r}
}

func TestPolicyNoNewCyclesReportsACycleTheChangeCloses(t *testing.T) {
	r := cycleRepo(t)
	r.setConfig(t, "policy:\n  no_new_cycles: {level: package}\n")

	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 2 {
		t.Fatalf("a cycle closed by a changed file must fail, got exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"graphlume.policy.no-new-cycles",
		"b -> a -> b",
		"b/b.go",
		// Never claims newness it cannot prove.
		"cannot tell whether this cycle already existed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "c -> d") || strings.Contains(out, "d -> c") {
		t.Errorf("a cycle the change does not touch must not be reported:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "new cycle") {
		t.Errorf("the output must not call a cycle new:\n%s", out)
	}

	r.setConfig(t, "policy:\n  no_new_cycles: {level: file}\n")
	out, code = exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 2 || !strings.Contains(out, "b/b.go -> a/a.go -> b/b.go") {
		t.Fatalf("file level: exit %d\n%s", code, out)
	}
}

func TestPolicyNoNewCyclesIgnoresCyclesTheChangeDoesNotTouch(t *testing.T) {
	r := cycleRepo(t)
	r.setConfig(t, "policy:\n  no_new_cycles: {level: package}\n")

	// Head is now an unrelated commit: only e/e.go changes. The a-b and c-d
	// cycles are both still in the index, and neither runs through the change.
	r.write(t, "e/e.go", "package e\n\nfunc E() {}\n\nfunc F() {}\n")
	r.commit(t, "touch e", "e/e.go")
	for _, args := range [][]string{{"index"}, {"resolve"}} {
		if out, err := run(t, r.root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 0 {
		t.Fatalf("existing cycles the change does not touch must not fail the check, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "no_new_cycles") {
		t.Errorf("the pass must say the rule ran:\n%s", out)
	}
}

// archPolicyRepo has a controller reaching past the service into the
// repository, which the architecture block forbids.
func archPolicyRepo(t *testing.T) policyRepo {
	t.Helper()
	r := newImpactRepo(t)
	r.write(t, "src/repository/TransactionRepository.java", `package com.pay.repository;

@Repository
public class TransactionRepository {
    public Object findById(String id) { return null; }
}`)
	r.write(t, "src/service/TransactionService.java", `package com.pay.service;

public class TransactionService {
    public void updateStatus(String id) { }
}`)
	r.commit(t, "base", "src/repository/TransactionRepository.java", "src/service/TransactionService.java")
	r.write(t, "src/controller/CallbackController.java", `package com.pay.controller;

import com.pay.repository.TransactionRepository;

public class CallbackController {
    private final TransactionRepository repository = new TransactionRepository();

    public void handleCallback(String id) {
        repository.findById(id);
    }
}`)
	r.commit(t, "controller", "src/controller/CallbackController.java")
	r.index(t)
	return policyRepo{r}
}

const archBlock = `architecture:
  components:
    - name: controller
      include: ["src/controller/**"]
    - name: service
      include: ["src/service/**"]
    - name: repository
      include: ["src/repository/**"]
  rules:
    - id: controllers-go-through-services
      from: [controller]
      allowed_to: [service]
      forbidden: [repository]
      severity: high
`

func TestPolicyArchitectureRuleCountsViolationsAtItsSeverity(t *testing.T) {
	r := archPolicyRepo(t)

	r.setConfig(t, archBlock+"policy:\n  architecture: {severity: error}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 2 {
		t.Fatalf("an architecture violation at severity error must exit 2, got %d\n%s", code, out)
	}
	for _, want := range []string{"graphlume.policy.architecture", "controllers-go-through-services", "CallbackController.java"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}

	r.setConfig(t, archBlock+"policy:\n  architecture: {severity: warning}\n")
	if out, code = exitCode(t, r.root, "policy", "check", "--base", "HEAD~1"); code != 0 {
		t.Fatalf("severity warning must not fail the check, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "WARNING") {
		t.Errorf("the violation must still be reported as a warning:\n%s", out)
	}
}

func TestPolicyArchitectureRuleNeedsAnArchitectureBlock(t *testing.T) {
	r := blastRepo(t)
	r.setConfig(t, "policy:\n  architecture: {severity: error}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 1 || !strings.Contains(out, "no architecture: block") {
		t.Fatalf("a rule with nothing to enforce must be a config error (exit 1), got %d\n%s", code, out)
	}
}

// estateFor writes an estate file inside the repository naming two seeded
// member indexes (a client that has drifted from its server, and the server).
func estateFor(t *testing.T, r policyRepo, web, api string) {
	t.Helper()
	r.write(t, "ci/estate.yml", "members:\n  - name: web\n    index: "+web+"\n  - name: api\n    index: "+api+"\n")
}

func TestPolicyContractsFailsOnTheListedKinds(t *testing.T) {
	// Seeded before the repository so that each helper's own isolate() runs
	// first; the policy run below only needs the paths.
	web, api := driftedWebIndex(t), apiIndex(t)
	r := blastRepo(t)
	estateFor(t, r, web, api)

	r.setConfig(t, "policy:\n  contracts: {estate: ci/estate.yml, fail_on: [method_mismatch]}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 2 {
		t.Fatalf("a client calling DELETE on a GET-only route must fail, got %d\n%s", code, out)
	}
	for _, want := range []string{"graphlume.policy.contracts", "method_mismatch", "src/orders.ts:20", "internal/http/routes.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "path_variable_mismatch") || strings.Contains(out, "no_matching_route") {
		t.Errorf("kinds not in fail_on must not be reported:\n%s", out)
	}

	// Omitting fail_on uses the two kinds with a route on the other side.
	r.setConfig(t, "policy:\n  contracts: {estate: ci/estate.yml}\n")
	out, _ = exitCode(t, r.root, "policy", "check", "--base", "HEAD~1", "--format", "json")
	var rep struct {
		Violations []struct{ Message string } `json:"violations"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(rep.Violations) != 2 {
		t.Errorf("default fail_on is method_mismatch and path_variable_mismatch, want 2 violations, got %d\n%s", len(rep.Violations), out)
	}

	// A kind nothing matches passes.
	r.setConfig(t, "policy:\n  contracts: {estate: ci/estate.yml, fail_on: [base_path_unverified], severity: warning}\n")
	if out, code = exitCode(t, r.root, "policy", "check", "--base", "HEAD~1"); code != 0 {
		t.Fatalf("warning severity must not fail, got %d\n%s", code, out)
	}
}

func TestPolicyContractsRefusesAnEstateThatEscapesThroughASymlink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "estate.yml")
	if err := os.WriteFile(outside, []byte("members: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := blastRepo(t)
	if err := os.MkdirAll(filepath.Join(r.dir, "ci"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.dir, "ci", "estate.yml")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	r.setConfig(t, "policy:\n  contracts: {estate: ci/estate.yml}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 1 || !strings.Contains(out, "outside the repository") {
		t.Fatalf("a symlinked estate leaving the repository must be refused (exit 1), got %d\n%s", code, out)
	}
}

func TestPolicyContractsRefusesALexicalEscape(t *testing.T) {
	r := blastRepo(t)
	r.setConfig(t, "policy:\n  contracts: {estate: ../estate.yml}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1")
	if code != 1 || !strings.Contains(out, "escapes the repository root") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestPolicySARIFIsValidAndCarriesRuleIDsAndALocation(t *testing.T) {
	r := cycleRepo(t)
	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 0}\n  no_new_cycles: {level: package}\n")

	sarifPath := filepath.Join(t.TempDir(), "policy.sarif")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1", "--format", "sarif", "--out", sarifPath)
	if code != 2 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	raw, err := os.ReadFile(sarifPath)
	if err != nil {
		t.Fatal(err)
	}
	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Rules []struct{ ID string } `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID    string `json:"ruleId"`
				Level     string `json:"level"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct{ URI string }     `json:"artifactLocation"`
						Region           *struct{ StartLine int } `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatalf("SARIF is not valid JSON: %v\n%s", err, raw)
	}
	if log.Version != "2.1.0" || len(log.Runs) != 1 {
		t.Fatalf("not a SARIF 2.1.0 log: %s", raw)
	}
	ids := map[string]bool{}
	for _, rule := range log.Runs[0].Tool.Driver.Rules {
		ids[rule.ID] = true
	}
	for _, want := range []string{"graphlume.policy.max-blast-radius", "graphlume.policy.no-new-cycles"} {
		if !ids[want] {
			t.Errorf("SARIF rules lack %q: %v", want, ids)
		}
	}
	located := map[string]bool{}
	for _, res := range log.Runs[0].Results {
		if len(res.Locations) == 0 || res.Locations[0].PhysicalLocation.ArtifactLocation.URI == "" {
			t.Errorf("result %s has no location; code scanning would drop it", res.RuleID)
			continue
		}
		located[res.RuleID] = true
		if res.Level != "error" {
			t.Errorf("an error-severity violation must be SARIF level error, got %q", res.Level)
		}
	}
	if !located["graphlume.policy.no-new-cycles"] || !located["graphlume.policy.max-blast-radius"] {
		t.Errorf("results = %v", located)
	}
	// The cycle result points at the changed line that closes it.
	for _, res := range log.Runs[0].Results {
		if res.RuleID == "graphlume.policy.no-new-cycles" {
			loc := res.Locations[0].PhysicalLocation
			if loc.ArtifactLocation.URI != "b/b.go" || loc.Region == nil || loc.Region.StartLine == 0 {
				t.Errorf("cycle location = %+v, want b/b.go with a line", loc)
			}
		}
	}
}

func TestPolicySARIFListsConfiguredRulesEvenWithNoResults(t *testing.T) {
	r := blastRepo(t)
	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 1000}\n")
	out, code := exitCode(t, r.root, "policy", "check", "--base", "HEAD~1", "--format", "sarif")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "graphlume.policy.max-blast-radius") {
		t.Errorf("a configured rule with no results must still be declared:\n%s", out)
	}
}

func TestPolicyExplainListsTheRuleTypes(t *testing.T) {
	r := blastRepo(t)
	for _, name := range []string{"explain"} {
		out, code := exitCode(t, r.root, "policy", name)
		if code != 0 {
			t.Fatalf("policy %s: exit %d\n%s", name, code, out)
		}
		for _, want := range []string{"max_blast_radius", "no_new_cycles", "architecture", "contracts",
			"graphlume.policy.no-new-cycles", "policy:", "severity"} {
			if !strings.Contains(out, want) {
				t.Errorf("policy %s output does not mention %q:\n%s", name, want, out)
			}
		}
	}
}

func TestCICheckAppliesThePolicy(t *testing.T) {
	r := blastRepo(t)
	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 0}\n")

	// Without --fail-on-violations the report is produced and the exit is 0.
	out, code := exitCode(t, r.root, "ci", "check", "--base", "HEAD~1")
	if code != 0 {
		t.Fatalf("ci check without the flag must not fail, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "| Policy |") || !strings.Contains(out, "graphlume.policy.max-blast-radius") {
		t.Errorf("the Markdown must carry the policy result:\n%s", out)
	}

	out, code = exitCode(t, r.root, "ci", "check", "--base", "HEAD~1", "--fail-on-violations")
	if code != 2 {
		t.Fatalf("ci check --fail-on-violations must exit 2 on an error-severity policy violation, got %d\n%s", code, out)
	}

	out, code = exitCode(t, r.root, "ci", "check", "--base", "HEAD~1", "--format", "sarif")
	if code != 0 || !strings.Contains(out, "graphlume.policy.max-blast-radius") {
		t.Errorf("the SARIF must carry the policy result (exit %d):\n%s", code, out)
	}

	r.setConfig(t, "policy:\n  max_blast_radius: {symbols: 0, severity: warning}\n")
	if out, code = exitCode(t, r.root, "ci", "check", "--base", "HEAD~1", "--fail-on-violations"); code != 0 {
		t.Fatalf("a warning must not fail ci check, got %d\n%s", code, out)
	}

	// No policy block: ci check keeps its old output, with no Policy row.
	r.setConfig(t, "project:\n  name: x\n")
	out, code = exitCode(t, r.root, "ci", "check", "--base", "HEAD~1", "--fail-on-violations")
	if code != 0 || strings.Contains(out, "| Policy |") {
		t.Errorf("without a policy: block ci check must be unchanged (exit %d):\n%s", code, out)
	}
}

func TestCICheckPolicyArchitectureSeverityOverridesTheLegacyGate(t *testing.T) {
	r := archPolicyRepo(t)

	r.setConfig(t, archBlock)
	if out, code := exitCode(t, r.root, "ci", "check", "--base", "HEAD~1", "--fail-on-violations"); code != 2 {
		t.Fatalf("the existing gate must still fail on architecture violations, got %d\n%s", code, out)
	}

	r.setConfig(t, archBlock+"policy:\n  architecture: {severity: warning}\n")
	if out, code := exitCode(t, r.root, "ci", "check", "--base", "HEAD~1", "--fail-on-violations"); code != 0 {
		t.Fatalf("policy.architecture at severity warning governs, so the build must pass, got %d\n%s", code, out)
	}
}
