package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/multirepo"
)

func TestCleanCommandDefault(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	projDir := t.TempDir()
	idxDir := filepath.Join(projDir, IndexDir)
	if err := os.MkdirAll(idxDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxDir, IndexFile), []byte("dummy sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Register in global registry
	_ = multirepo.RegisterProject(context.Background(), fakeHome, multirepo.ProjectEntry{
		ID:       "proj",
		RootPath: projDir,
		DBPath:   filepath.Join(idxDir, IndexFile),
	})

	g := &Global{cfg: config.Config{RepoRoot: projDir}}
	cmd := newCleanCmd(g)
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("clean run: %v", err)
	}

	// .graphlume should be deleted
	if _, err := os.Stat(idxDir); err == nil {
		t.Errorf("expected %s to be deleted", idxDir)
	}

	// Should be unregistered from global registry
	reg, _ := multirepo.LoadGlobalRegistry(fakeHome)
	if len(reg.Projects) != 0 {
		t.Errorf("expected 0 projects in registry, got %d", len(reg.Projects))
	}

	if !strings.Contains(out.String(), "Removed .graphlume directory") {
		t.Errorf("unexpected output: %s", out.String())
	}
}

func TestCleanCommandDryRun(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	projDir := t.TempDir()
	idxDir := filepath.Join(projDir, IndexDir)
	if err := os.MkdirAll(idxDir, 0o750); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(idxDir, IndexFile)
	if err := os.WriteFile(dbFile, []byte("dummy sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}

	g := &Global{cfg: config.Config{RepoRoot: projDir}}
	cmd := newCleanCmd(g)
	cmd.SetArgs([]string{"--dry-run"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("clean dry-run: %v", err)
	}

	// File should STILL exist after dry-run
	if _, err := os.Stat(dbFile); err != nil {
		t.Errorf("expected %s to still exist after dry-run", dbFile)
	}

	if !strings.Contains(out.String(), "[DRY RUN]") || !strings.Contains(out.String(), "Would clean") {
		t.Errorf("unexpected dry-run output: %s", out.String())
	}
}

func TestUnmergeClientConfig(t *testing.T) {
	jsonConfig := []byte(`{
  "mcpServers": {
    "other-tool": {
      "command": "other"
    },
    "graphlume": {
      "command": "graphlume",
      "args": ["serve"]
    }
  }
}`)

	client := mcpClient{
		Format: fmtJSONMap,
		Key:    "mcpServers",
	}

	unmerged, removed, err := unmergeClientConfig(client, jsonConfig, "graphlume")
	if err != nil {
		t.Fatalf("unmerge error: %v", err)
	}
	if !removed {
		t.Fatalf("expected removed=true")
	}
	s := string(unmerged)
	if strings.Contains(s, "graphlume") {
		t.Errorf("expected graphlume removed from JSON, got: %s", s)
	}
	if !strings.Contains(s, "other-tool") {
		t.Errorf("expected other-tool preserved in JSON, got: %s", s)
	}
}

func TestCleanExplain(t *testing.T) {
	g := &Global{}
	cmd := newCleanCmd(g)
	cmd.SetArgs([]string{"--explain"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("clean --explain: %v", err)
	}
	if !strings.Contains(out.String(), "GRAPHLUME CLEANUP & UNINSTALLATION MANUAL") {
		t.Errorf("expected manual header in --explain, got:\n%s", out.String())
	}
}

func TestCleanGuidanceStripping(t *testing.T) {
	projDir := t.TempDir()

	// 1. File that only has guidance (AGENTS.md) should be deleted
	agentsPath := filepath.Join(projDir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("<!-- graphlume:guidance -->\nGraphLume guidance text\n<!-- /graphlume:guidance -->\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. File that has other content (CLAUDE.md) should preserve user content
	claudePath := filepath.Join(projDir, "CLAUDE.md")
	userContent := "# My Custom Claude Instructions\n\nDon't edit without review.\n\n"
	fullClaude := userContent + "<!-- graphlume:guidance -->\nGraphLume rules\n<!-- /graphlume:guidance -->\n"
	if err := os.WriteFile(claudePath, []byte(fullClaude), 0o600); err != nil {
		t.Fatal(err)
	}

	actions, err := cleanProjectGuidance(projDir, false)
	if err != nil {
		t.Fatalf("clean guidance error: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %d: %v", len(actions), actions)
	}

	// AGENTS.md should be deleted
	if _, err := os.Stat(agentsPath); err == nil {
		t.Errorf("expected AGENTS.md to be deleted")
	}

	// CLAUDE.md should survive with userContent
	remaining, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("expected CLAUDE.md to exist: %v", err)
	}
	if strings.Contains(string(remaining), "graphlume:guidance") {
		t.Errorf("graphlume guidance block should be removed, got: %s", string(remaining))
	}
	if !strings.Contains(string(remaining), "My Custom Claude Instructions") {
		t.Errorf("user content was lost: %s", string(remaining))
	}
}

func TestProjectCleanRefusesHomeRootAndNonProjects(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "work", "svc")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(home, "notes")
	if err := os.MkdirAll(plain, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		root string
		ok   bool
	}{
		{home, false},               // ~/.graphlume is global state
		{filepath.Dir(home), false}, // contains home
		{string(filepath.Separator), false},
		{plain, false}, // neither indexed nor a repository
		{proj, true},
	} {
		if got := projectCleanRefusal(tc.root, home); (got == "") != tc.ok {
			t.Errorf("projectCleanRefusal(%s) = %q; want ok=%v", tc.root, got, tc.ok)
		}
	}
}

func TestGlobalAgentConfigsAreNeverProjectScoped(t *testing.T) {
	home := t.TempDir()
	for _, c := range mcpClients(home, home) {
		if isProjectScoped(c, home) && !strings.HasSuffix(c.Name, projectScopedSuffix) {
			t.Errorf("%s (%s) counted as project-scoped with the project at ~", c.Name, c.Path)
		}
	}
	proj := filepath.Join(home, "svc")
	scoped := 0
	for _, c := range mcpClients(home, proj) {
		if isProjectScoped(c, proj) {
			scoped++
			if !strings.HasPrefix(c.Path, proj) {
				t.Errorf("%s is project-scoped but lives at %s", c.Name, c.Path)
			}
		}
	}
	if scoped == 0 {
		t.Error("no project-scoped client found for a project; the suffix rule matches nothing")
	}
}

func TestPackageManagerInstallsAreNotSelfRemoved(t *testing.T) {
	for path, want := range map[string]string{
		"/opt/homebrew/Cellar/graphlume/1.4.0/bin/graphlume":    "Homebrew",
		"/home/linuxbrew/.linuxbrew/bin/graphlume":              "Homebrew",
		`C:\Users\u\scoop\apps\graphlume\current\graphlume.exe`: "",
		"/usr/local/bin/graphlume":                              "",
	} {
		if got := packageManagerOf(filepath.FromSlash(path)); want != "" && got != want {
			t.Errorf("packageManagerOf(%s) = %q, want %q", path, got, want)
		} else if want == "" && path == "/usr/local/bin/graphlume" && got != "" {
			t.Errorf("a direct install was attributed to %s", got)
		}
	}
}

func TestWriteFileAtomicKeepsMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if string(b) != "new" || fi.Mode().Perm() != 0o644 {
		t.Errorf("content %q mode %v; want new, 0644", b, fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestFullCleanRefusesWithoutATerminal(t *testing.T) {
	root := t.TempDir()
	out, err := run(t, root, "clean", "--all")
	if err == nil || !strings.Contains(out+err.Error(), "--force") {
		t.Fatalf("clean --all without a terminal ran: %v\n%s", err, out)
	}
	out, err = run(t, root, "uninstall")
	if err == nil || !strings.Contains(out+err.Error(), "--force") {
		t.Fatalf("uninstall without a terminal ran: %v\n%s", err, out)
	}
}

// A dry run changes nothing: not the hooks, not the global registry. Both must
// survive the preview, which reports "would".
func TestCleanDryRunKeepsHooksAndRegistry(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, IndexDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCommitHook(root, "graphlume"); err != nil {
		t.Fatal(err)
	}
	if err := multirepo.RegisterProject(context.Background(), home, multirepo.ProjectEntry{RootPath: root}); err != nil {
		t.Fatal(err)
	}

	actions, err := cleanCurrentProject(root, home, true, true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(actions, "\n")
	if !strings.Contains(joined, "git hooks") || !strings.Contains(joined, "registry.json") {
		t.Errorf("dry run did not report hooks and registry: %q", joined)
	}
	if !hasManagedHooks(root) {
		t.Error("dry run removed the git hooks")
	}
	reg, err := multirepo.LoadGlobalRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if !projectRegistered(reg, root) {
		t.Error("dry run unregistered the project")
	}

	if _, err := cleanCurrentProject(root, home, true, false); err != nil {
		t.Fatal(err)
	}
	if hasManagedHooks(root) {
		t.Error("clean left the git hooks")
	}
	reg, _ = multirepo.LoadGlobalRegistry(home)
	if projectRegistered(reg, root) {
		t.Error("clean left the registry entry")
	}
}
