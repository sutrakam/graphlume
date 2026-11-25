package indexer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
)

// An npm indexer installs into a prefix GraphLume owns, so it neither needs
// write access to npm's global prefix nor depends on that prefix being on PATH.
func TestNPMInstallUsesAPrefixGraphLumeOwns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	const payload = "the expected package"
	r := newFakeRunner("npm")
	fakeNPM(r, payload)
	pin := npmPin(sha256Of(payload))
	r.handle("npm", withInstallSideEffect(r, payload, pin))
	p := &Provisioner{Runner: r, Policy: config.ProvisionAuto, Audit: &MemoryAudit{}}
	if _, err := p.Ensure(context.Background(), pin); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, c := range r.calls() {
		if !contains(c.Args, "install") {
			continue
		}
		i := indexOf(c.Args, "--prefix")
		if i < 0 || i+1 >= len(c.Args) || c.Args[i+1] != NPMPrefixDir() {
			t.Fatalf("npm install was not given GraphLume's prefix: %v", c.Argv())
		}
		if !strings.HasSuffix(NPMPrefixDir(), filepath.Join(".graphlume", "npm")) {
			t.Fatalf("unexpected prefix %q", NPMPrefixDir())
		}
		return
	}
	t.Fatal("no npm install call recorded")
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// A binary in the npm prefix is found even though that directory is not on
// PATH: it is what makes "installed but not on PATH" impossible.
func TestLookPathFindsAnIndexerInTheNPMPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix layout")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	bin := npmBinDir()
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(bin, "scip-python")
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o700); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	got, err := (ExecRunner{}).LookPath("scip-python")
	if err != nil || got != want {
		t.Fatalf("LookPath = %q, %v; want %q", got, err, want)
	}
}

// The plan names each download once, with the exact artifact and its source,
// and leaves out Go, which the built-in type checker proves.
func TestPlanInstallsNamesWhatWillBeFetched(t *testing.T) {
	plan := Plan{Steps: []Step{
		{Project: Project{Language: LangPython}, Indexer: SCIPPython, Pin: mustPin(t, LangPython), NeedsProvision: true},
		{Project: Project{Language: LangPython, Dir: "b"}, Indexer: SCIPPython, Pin: mustPin(t, LangPython), NeedsProvision: true},
		{Project: Project{Language: LangGo}, Indexer: SCIPGo, Pin: mustPin(t, LangGo), NeedsProvision: true},
		{Project: Project{Language: LangTypeScript}, Indexer: SCIPTypeScript, Pin: mustPin(t, LangTypeScript)},
		{Project: Project{Language: LangJava}, Indexer: SCIPJava, Pin: mustPin(t, LangJava), NeedsProvision: true, Blocked: true},
	}}
	got := plan.Installs(true)
	if len(got) != 1 || got[0].Indexer != SCIPPython {
		t.Fatalf("Installs(skipGo) = %+v, want only scip-python once", got)
	}
	line := got[0].String()
	for _, want := range []string{"scip-python", "@sourcegraph/scip-python@", "npm registry"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q does not mention %q", line, want)
		}
	}
	if all := plan.Installs(false); len(all) != 2 {
		t.Errorf("Installs(false) = %d entries, want python and go", len(all))
	}
}

func mustPin(t *testing.T, l Language) Pin {
	t.Helper()
	p, ok := PinFor(l)
	if !ok {
		t.Fatalf("no pin for %s", l)
	}
	return p
}

// A failed install is reported and the others still run: an indexer that
// cannot be fetched never stops the onboarding.
func TestProvisionReportsFailuresAndContinues(t *testing.T) {
	r := newFakeRunner() // no npm, no go: every install fails
	plan := Plan{ProvisionPolicy: config.ProvisionAuto}
	installs := []Install{
		{Indexer: SCIPPython, Language: LangPython, Pin: mustPin(t, LangPython)},
		{Indexer: SCIPTypeScript, Language: LangTypeScript, Pin: mustPin(t, LangTypeScript)},
	}
	done, failed := Provision(context.Background(), plan, installs, Options{Runner: r})
	if len(done) != 0 || len(failed) != 2 {
		t.Fatalf("done=%d failed=%d, want 0 and 2", len(done), len(failed))
	}
	for _, f := range failed {
		if f.Err == nil {
			t.Errorf("%s failure has no reason", f.Install.Indexer)
		}
	}
}
