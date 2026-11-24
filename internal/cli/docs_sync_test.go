package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateWorkflowMatchesUserGuide: the guide tells users to copy the example
// workflow the repository ships, so the guide's YAML block must contain
// docs/examples/graphlume-gate.yml verbatim.
func TestGateWorkflowMatchesUserGuide(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "graphlume-gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile(filepath.Join("..", "..", "docs", "USER_GUIDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(guide), string(wf)) {
		t.Error("docs/USER_GUIDE.md does not contain docs/examples/graphlume-gate.yml verbatim; update the guide's YAML block")
	}
}

// TestGateWorkflowCanFail: a gate whose steps end in `|| true` cannot fail,
// and `impact` takes --base/--head rather than a file argument.
func TestGateWorkflowCanFail(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "graphlume-gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(wf)
	if strings.Contains(text, "|| true") {
		t.Error("gate workflow swallows failures with '|| true'")
	}
	for _, want := range []string{
		"graphlume resolve", "--fail-on", "impact --base",
		"report sarif", "report pr", "upload-sarif", "graphlume:pr-report",
		"indexers pins", "indexers install --yes --out-dir", "scip import", "actions/cache@v6",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("gate workflow is missing %q", want)
		}
	}
	// A fork PR's token cannot write security events; an unguarded upload
	// would fail the job before the gate runs.
	i := strings.Index(text, "name: Upload SARIF to Code Scanning")
	if i < 0 {
		t.Fatal("gate workflow has no SARIF upload step")
	}
	step := text[i:]
	if j := strings.Index(step[1:], "- name:"); j >= 0 {
		step = step[:j+1]
	}
	if !strings.Contains(step, "github.event.pull_request.head.repo.full_name == github.repository") {
		t.Error("SARIF upload step must be skipped for fork pull requests")
	}
}

// stepBody returns the text of the workflow step whose "- name:" line is name,
// up to the next step.
func stepBody(t *testing.T, text, name string) string {
	t.Helper()
	i := strings.Index(text, "- name: "+name)
	if i < 0 {
		t.Fatalf("gate workflow has no step %q", name)
	}
	step := text[i:]
	if j := strings.Index(step[1:], "- name:"); j >= 0 {
		step = step[:j+1]
	}
	return step
}

// TestGateWorkflowImportsCompilerFactsBeforeImpact: compiler facts only help
// if impact reads them, the stage must be switchable off without editing
// steps, and the gate must never build the repository (that is what
// --allow-repo-build authorises for Java).
func TestGateWorkflowImportsCompilerFactsBeforeImpact(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "graphlume-gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(wf)

	resolve := strings.Index(text, "./bin/graphlume resolve")
	imported := strings.Index(text, "./bin/graphlume scip import")
	impactAt := strings.Index(text, "impact --base")
	if resolve < 0 || imported < 0 || impactAt < 0 {
		t.Fatalf("missing a step: resolve=%d scip import=%d impact=%d", resolve, imported, impactAt)
	}
	if resolve >= imported || imported >= impactAt {
		t.Errorf("scip import must run after resolve and before impact (resolve=%d import=%d impact=%d)",
			resolve, imported, impactAt)
	}
	if !strings.Contains(text, "GRAPHLUME_ACCURACY: auto") {
		t.Error("GRAPHLUME_ACCURACY must default to auto at the workflow level")
	}
	for _, name := range []string{"Pin Compiler Indexers", "Cache Compiler Indexers", "Import Compiler Facts"} {
		if !strings.Contains(stepBody(t, text, name), "if: env.GRAPHLUME_ACCURACY == 'auto'") {
			t.Errorf("step %q is not guarded by GRAPHLUME_ACCURACY", name)
		}
	}
	if !strings.Contains(stepBody(t, text, "Cache Compiler Indexers"), "hashFiles('graphlume-out/indexer-pins.txt')") {
		t.Error("the indexer cache must be keyed on the pinned versions and digests")
	}
	imp := stepBody(t, text, "Import Compiler Facts")
	for _, want := range []string{
		"::warning::compiler indexers unavailable",
		"::warning::scip import of",
	} {
		if !strings.Contains(imp, want) {
			t.Errorf("Import Compiler Facts must degrade with %q, not fail the gate", want)
		}
	}
	if !strings.Contains(text, "steps.setup-go.outputs.go-version") {
		t.Error("the indexer cache key must include the Go version scip-go is built with")
	}
	if strings.Contains(text, "--allow-repo-build") {
		t.Error("the gate must never run the repository's own build")
	}
}

// TestInstallationPageMatchesTheToolchainAndRelease: the installation page
// must not say "Go 1.23+" against `go 1.26.0` in go.mod, name archives of an
// outdated release, or describe install locations neither installer uses.
// Each of those is a fact stated somewhere else in the repository, so the page
// is compared with where it is stated.
func TestInstallationPageMatchesTheToolchainAndRelease(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	page := read("docs", "getting-started", "installation.md")

	// The Go version: `go 1.26.0` in go.mod is "Go 1.26".
	var goMinor string
	for _, line := range strings.Split(read("go.mod"), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "go" {
			if v := strings.SplitN(f[1], ".", 3); len(v) >= 2 {
				goMinor = v[0] + "." + v[1]
			}
		}
	}
	if goMinor == "" {
		t.Fatal("go.mod has no go directive")
	}
	if !strings.Contains(page, "Go "+goMinor) {
		t.Errorf("installation.md does not say Go %s, which is what go.mod requires", goMinor)
	}
	// And names no other.
	for i := 0; i < len(page); i++ {
		if !strings.HasPrefix(page[i:], "Go 1.") {
			continue
		}
		rest := page[i+len("Go "):]
		end := 0
		for end < len(rest) && (rest[end] == '.' || (rest[end] >= '0' && rest[end] <= '9')) {
			end++
		}
		if got := strings.TrimRight(rest[:end], "."); got != goMinor && !strings.HasPrefix(got, goMinor+".") {
			t.Errorf("installation.md names Go %s; go.mod requires Go %s", got, goMinor)
		}
	}

	// The release the archive names are written for is the one the package
	// manifests point at, which the release process updates.
	scoop := read("packaging", "scoop", "graphlume.json")
	_, after, ok := strings.Cut(scoop, `"version": "`)
	if !ok {
		t.Fatal("packaging/scoop/graphlume.json has no version")
	}
	release := "v" + after[:strings.Index(after, `"`)]
	for _, field := range strings.FieldsFunc(page, func(r rune) bool { return r == '`' || r == ' ' || r == '\n' || r == '/' }) {
		if !strings.HasPrefix(field, "graphlume_v") {
			continue
		}
		if !strings.HasPrefix(field, "graphlume_"+release+"_") {
			t.Errorf("installation.md names %s; the current release is %s", field, release)
		}
	}
	if !strings.Contains(page, "graphlume_"+release+"_linux_amd64.tar.gz") {
		t.Errorf("installation.md names no archive of the current release %s", release)
	}

	// Where each installer puts the binary.
	if sh := read("install.sh"); !strings.Contains(sh, `INSTALL_DIR="${HOME}/.local/bin"`) || !strings.Contains(page, "`~/.local/bin`") {
		t.Error("installation.md and install.sh disagree about the default install directory (~/.local/bin)")
	}
	if ps := read("install.ps1"); !strings.Contains(ps, `$env:LOCALAPPDATA\Programs\graphlume`) ||
		!strings.Contains(page, `%LOCALAPPDATA%\Programs\graphlume`) {
		t.Error(`installation.md and install.ps1 disagree about the default install directory (%LOCALAPPDATA%\Programs\graphlume)`)
	}
	for _, stale := range []string{"`graphlume.exe` and `graphlume.exe`", "alias"} {
		if strings.Contains(page, stale) {
			t.Errorf("installation.md still says %q; there is one command and one name", stale)
		}
	}
}
