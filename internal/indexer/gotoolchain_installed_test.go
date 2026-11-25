package indexer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The tests below pin the toolchain a scip-go run uses relative to the Go the
// user has installed. Running with GraphLume's own release made every machine
// with an older Go download a whole toolchain that nobody had been told about.

func TestGoRunToolchainUsesTheInstalledGo(t *testing.T) {
	t.Parallel()
	installed := goVersion{1, 24, 2}
	for _, tc := range []struct{ mod, want string }{
		{"module x\n\ngo 1.21\n", "local"},
		{"module x\n\ngo 1.24.2\n", "local"},
		{"module x\n\ngo 1.25.3\n", "go1.25.3"},
		{"module x\n\ngo 1.21\n\ntoolchain go1.99.9\n", "local"}, // the toolchain line is never obeyed
		{"module x\ngo $(whoami)\n", "local"},
		{"", "local"},
	} {
		dir := writeRepo(t, map[string]string{"go.mod": tc.mod})
		if got := goRunToolchain(dir, installed); got != tc.want {
			t.Errorf("go.mod %q with Go 1.24.2 installed: GOTOOLCHAIN=%q, want %q", tc.mod, got, tc.want)
		}
	}
}

func TestInstalledGoReadsTheVersionBanner(t *testing.T) {
	t.Parallel()
	tc := Toolchain{Tools: map[Tool]ToolStatus{ToolGo: {Tool: ToolGo, Present: true, Version: "go version go1.24.2 darwin/arm64"}}}
	if v, ok := installedGo(tc); !ok || v.String() != "go1.24.2" {
		t.Errorf("installedGo = %v, %v; want go1.24.2", v, ok)
	}
	if _, ok := installedGo(Toolchain{}); ok {
		t.Error("no Go on PATH must read as unknown")
	}
}

func TestGoDownloadsNamesEveryReleaseBeforeIndexing(t *testing.T) {
	t.Parallel()
	root := writeRepo(t, map[string]string{
		"old/go.mod":   "module old\n\ngo 1.21\n",
		"newA/go.mod":  "module a\n\ngo 1.25.3\n",
		"newB/go.mod":  "module b\n\ngo 1.25.3\n",
		"newer/go.mod": "module c\n\ngo 1.26\n",
	})
	plan := Plan{
		RepoRoot:  root,
		Toolchain: Toolchain{Tools: map[Tool]ToolStatus{ToolGo: {Present: true, Version: "go version go1.24.2 linux/amd64"}}},
	}
	for _, d := range []string{"old", "newA", "newB", "newer"} {
		plan.Projects = append(plan.Projects, Project{Language: LangGo, Dir: d})
	}
	got := GoDownloads(plan)
	if len(got) != 2 || got[0].Release != "go1.25.3" || len(got[0].Projects) != 2 || got[1].Release != "go1.26" {
		t.Fatalf("GoDownloads = %+v; want go1.25.3 for newA and newB, then go1.26", got)
	}
	if got[0].Installed != "go1.24.2" {
		t.Errorf("installed = %q", got[0].Installed)
	}
	e := &Executor{InstalledGo: "go1.24.2"}
	if v := e.goToolchain(filepath.Join(root, "old")); v != "local" {
		t.Errorf("an old module runs with %q, want the installed Go (local)", v)
	}
}

// TestGoProbeAsksAboutTheInstalledGo: run inside a project whose go.mod wants
// a newer Go, `go version` switched toolchains and failed, so the installed
// release read as unknown and the download that followed went unannounced.
func TestGoProbeAsksAboutTheInstalledGo(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("go")
	var seen Command
	r.handle("go", func(c Command) (Result, error) {
		seen = c
		return Result{Stdout: []byte("go version go1.24.2 darwin/arm64\n")}, nil
	})
	st := probeOne(context.Background(), r, nil, ToolGo)
	if st.Version == "" || !strings.Contains(st.Version, "go1.24.2") {
		t.Fatalf("version = %q", st.Version)
	}
	if !containsEnv(seen.Env, "GOTOOLCHAIN=local") {
		t.Errorf("go version ran without GOTOOLCHAIN=local: %v", seen.Env)
	}
	if seen.Dir == "" {
		t.Error("go version ran in the project directory, where its go.mod can switch the toolchain")
	}
}

func containsEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
