package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/pipeline"
)

// The tests in this file pin what a new user sees on a first run. Each one is
// a defect that reached a person running `graphlume init --all`.

// TestProjectCommandsRefuseAHomeDirectory: run in ~, init walked every folder
// in the home directory (macOS asked for Music and Photos access), registered
// the home directory with every agent and wrote AGENTS.md into it.
func TestProjectCommandsRefuseAHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, args := range [][]string{
		{"init", "--all", "--yes"}, {"init"}, {"setup", "--no-index"}, {"install", "--dry-run"},
		{"index"}, {"resolve"}, {"watch"},
	} {
		root := NewRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"--project", home, "--quiet"}, args...))
		err := root.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), "your home directory, not a project") {
			t.Errorf("%v in the home directory: err = %v, want a refusal", args, err)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("a refused command wrote %s into the home directory", e.Name())
	}
}

func TestRefuseBroadRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(project, 0o750); err != nil {
		t.Fatal(err)
	}
	fsRoot := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		fsRoot = filepath.VolumeName(home) + `\`
	}
	for _, tc := range []struct {
		root   string
		refuse bool
	}{
		{home, true},
		{filepath.Dir(home), true},
		{fsRoot, true},
		{project, false},
		{filepath.Join(home, "work"), false},
	} {
		err := refuseBroadRoot(tc.root)
		if (err != nil) != tc.refuse {
			t.Errorf("refuseBroadRoot(%s) = %v, refuse = %v", tc.root, err, tc.refuse)
		}
	}
	// Commands that only read an existing index, or clean up, are not refused.
	for _, name := range []string{"clean", "uninstall", "status", "ui", "serve", "doctor"} {
		if projectCommands[name] {
			t.Errorf("%s must keep working from a home directory", name)
		}
	}
}

type scriptedRunner struct {
	calls []string
	fail  map[string]bool
}

func (r *scriptedRunner) LookPath(name string) (string, error) { return name, nil }

func (r *scriptedRunner) Run(_ context.Context, dir string, argv []string, out io.Writer) error {
	r.calls = append(r.calls, filepath.Base(dir))
	if r.fail[filepath.Base(dir)] {
		_, _ = io.WriteString(out, "go: downloading something\ngo: go.mod requires go >= 9.9\n")
		return errors.New("exit status 1")
	}
	return nil
}

// TestSetupInManyDirectoriesPrintsOneProgressLineEach: preparing 1,213 Go
// modules printed "$ go mod download" 1,213 times, never saying which module,
// and the first module that failed stopped all the others.
func TestSetupInManyDirectoriesPrintsOneProgressLineEach(t *testing.T) {
	root := t.TempDir()
	var dirs []string
	for _, d := range []string{"a", "b", "c"} {
		dirs = append(dirs, filepath.Join(root, d))
	}
	run := &scriptedRunner{fail: map[string]bool{"b": true}}
	var out bytes.Buffer
	g := &Global{}
	g.cfg.RepoRoot = root
	s := &guidedSetup{ctx: context.Background(), g: g, cfg: g.cfg, out: &out, run: run}
	if err := s.runEach(dirs, [][]string{{"go", "mod", "download"}}); err != nil {
		t.Fatalf("one failing directory of three must not fail the step: %v", err)
	}
	text := out.String()
	if n := strings.Count(text, "$ go mod download"); n != 1 {
		t.Errorf("the command is printed %d times, want once:\n%s", n, text)
	}
	for _, want := range []string{"(in each of 3 directories)", "[1/3] a", "[2/3] b", "[3/3] c",
		"1 of 3 directories could not be prepared", "go.mod requires go >= 9.9"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Join(run.calls, ",") != "a,b,c" {
		t.Errorf("ran in %v, want every directory despite the failure in b", run.calls)
	}
}

// TestGoRunsWithoutDownloadingAToolchain: a go.mod asking for a newer Go made
// `go mod download` fetch a whole toolchain that the offer never mentioned.
func TestGoRunsWithoutDownloadingAToolchain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in go is a shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"GOTOOLCHAIN=$GOTOOLCHAIN\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o700); err != nil { //nolint:gosec // a test stand-in
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GOTOOLCHAIN", "")
	var out bytes.Buffer
	if err := (execSetupRunner{}).Run(context.Background(), t.TempDir(), []string{"go", "mod", "download"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "GOTOOLCHAIN=local" {
		t.Errorf("go ran with %q, want GOTOOLCHAIN=local", got)
	}
	t.Setenv("GOTOOLCHAIN", "go1.99.0")
	out.Reset()
	if err := (execSetupRunner{}).Run(context.Background(), t.TempDir(), []string{"go", "version"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "GOTOOLCHAIN=go1.99.0" {
		t.Errorf("a GOTOOLCHAIN the user set was replaced: %q", got)
	}
}

// TestUserLevelRegistrationNamesNoProject: after `init --all` in six projects,
// ~/.claude.json pointed at the sixth, so an agent in any of the other five
// read the wrong index. Project files keep their project; shared files name none.
func TestUserLevelRegistrationNamesNoProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var roots []string
	for i := 0; i < 3; i++ {
		root := filepath.Join(t.TempDir(), "project")
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
		spec := agentServerSpec{Name: agentMCPName, Command: "graphlume",
			Args: []string{"--project", root, "serve", "--profile", "compact", "--audit-log", filepath.Join(root, IndexDir, "audit.jsonl")}}
		// createAll: write the configs whether or not Claude Code is
		// installed, so the test does not depend on this machine's PATH.
		if _, err := installClients(home, root, spec, true, false, false, []string{"claude-code"}); err != nil {
			t.Fatal(err)
		}
	}
	user, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if strings.Contains(string(user), root) {
			t.Errorf("~/.claude.json names project %s:\n%s", root, user)
		}
	}
	if !strings.Contains(string(user), `"other"`) || !strings.Contains(string(user), `"serve"`) {
		t.Errorf("~/.claude.json lost a server or the GraphLume entry:\n%s", user)
	}
	for _, root := range roots {
		proj, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
		if err != nil {
			t.Fatalf("no project registration for %s: %v", root, err)
		}
		if !strings.Contains(string(proj), root) {
			t.Errorf("%s/.mcp.json does not name its own project:\n%s", root, proj)
		}
	}
}

func TestServeFindsTheIndexAboveItsWorkingFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "code", "app")
	deep := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(filepath.Join(root, IndexDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectIndexPath(root), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := nearestIndexedRoot(deep); got != root {
		t.Errorf("nearestIndexedRoot(%s) = %q, want %q", deep, got, root)
	}
	if got := nearestIndexedRoot(filepath.Join(home, "elsewhere")); got != "" {
		t.Errorf("a folder outside any project found %q", got)
	}
}

// TestCompilerSummaryIsOneBlockPerIndexer: a monorepo of six TypeScript apps
// printed "1 files covered, 0 calls proven, ..." six times.
func TestCompilerSummaryIsOneBlockPerIndexer(t *testing.T) {
	var o pipeline.CompilerIndexOutcome
	for _, d := range []string{"web0", "web1", "web2"} {
		s := pipeline.CompilerIndexStep{Indexer: "scip-typescript", Version: "0.3.14", Language: "typescript", Dir: d}
		s.Import.Ingest.DocumentsMatched = 2
		s.Import.Promote.Promoted = 1
		o.Steps = append(o.Steps, s)
	}
	var out bytes.Buffer
	renderCompilerIndexers(&out, config.AccuracyAuto, o)
	text := out.String()
	if n := strings.Count(text, "files covered"); n != 1 {
		t.Errorf("the totals line is printed %d times, want once:\n%s", n, text)
	}
	for _, want := range []string{"read 3 projects (web0, web1, web2)", "6 files covered, 3 calls proven"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
}

// TestInitAllRepairsADamagedIndex: a damaged index had no repair path short of
// deleting it by hand. init --all moves it aside, keeps it, and rebuilds.
func TestInitAllRepairsADamagedIndex(t *testing.T) {
	root := isolate(t)
	if _, err := run(t, root, "init"); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(root, IndexDir, IndexFile)
	if err := os.WriteFile(db, []byte("not a database at all, just bytes on disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, root, "init")
	if err != nil {
		t.Fatalf("init on a damaged index: %v\n%s", err, out)
	}
	if !strings.Contains(out, "was damaged") {
		t.Errorf("init did not say the index was damaged:\n%s", out)
	}
	backups, _ := filepath.Glob(db + ".damaged-*")
	if len(backups) != 1 {
		t.Fatalf("want one backup of the damaged file, found %v", backups)
	}
	if _, err := run(t, root, "status"); err != nil {
		t.Errorf("status after the repair: %v", err)
	}
}

// TestSetupFailureInEveryDirectoryIsReportedOnce: when every directory failed,
// "2 of 2 directories could not be prepared" was printed and then the caller's
// "Failed: ..." said the same thing again.
func TestSetupFailureInEveryDirectoryIsReportedOnce(t *testing.T) {
	root := t.TempDir()
	dirs := []string{filepath.Join(root, "a"), filepath.Join(root, "b")}
	run := &scriptedRunner{fail: map[string]bool{"a": true, "b": true}}
	var out bytes.Buffer
	g := &Global{}
	g.cfg.RepoRoot = root
	s := &guidedSetup{ctx: context.Background(), g: g, cfg: g.cfg, out: &out, run: run}
	if err := s.runEach(dirs, [][]string{{"go", "mod", "download"}}); err == nil {
		t.Fatal("every directory failed, yet the step reported success")
	}
	if strings.Contains(out.String(), "could not be prepared") {
		t.Errorf("the failure is summarised here and again by the caller:\n%s", out.String())
	}
}
