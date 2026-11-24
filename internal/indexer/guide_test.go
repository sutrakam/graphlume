package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
)

func writeTree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A Visual Studio solution with no .cs file beneath it is not a C# project,
// and C/C++ and Python sources with no marker are counted.
func TestDetectSolutionAndLooseSources(t *testing.T) {
	dir := writeTree(t, "neo/doom.sln", "neo/SConstruct", "neo/a.cpp", "neo/a.h", "tools/x.py")
	projects, sources, err := detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects = %v, want none", projects)
	}
	if sources[LangCPP] != 2 || sources[LangPython] != 1 {
		t.Fatalf("sources = %v", sources)
	}
	dir = writeTree(t, "App.sln", "src/Program.cs")
	projects, err = Detect(context.Background(), dir)
	if err != nil || len(projects) != 1 || projects[0].Language != LangCSharp {
		t.Fatalf("a solution with C# beneath it: %v, %v", projects, err)
	}
}

func TestFindCBuildShallowestFirst(t *testing.T) {
	dir := writeTree(t, "neo/SConstruct", "neo/openal/docs/Makefile")
	b := FindCBuild(dir, ".")
	if b.System != "scons" || b.Dir != "neo" {
		t.Fatalf("FindCBuild = %+v", b)
	}
	if b := FindCBuild(writeTree(t, "x.c"), "."); b.System != "" {
		t.Fatalf("no build file: %+v", b)
	}
}

func TestSetupItemsKindsAndDefaults(t *testing.T) {
	plan := Plan{
		RepoRoot: writeTree(t, "CMakeLists.txt"),
		Accuracy: config.AccuracyAuto, ProvisionPolicy: config.ProvisionPrompt,
		Steps: []Step{
			{Project: Project{Language: LangPython, Dir: "."}, Pin: mustPin(t, LangPython), Indexer: SCIPPython, NeedsProvision: true},
			{Project: Project{Language: LangCSharp, Dir: "."}, Pin: mustPin(t, LangCSharp), Indexer: SCIPDotnet,
				MissingTools: []Tool{ToolDotnet}, Blocked: true},
			{Project: Project{Language: LangCPP, Dir: ".", Markers: []string{"CMakeLists.txt"}}, Pin: mustPin(t, LangCPP), Blocked: true},
		},
	}
	env := SetupEnv{GOOS: "darwin", Home: "/h", LookPath: func(n string) (string, error) {
		if n == "brew" || n == "cmake" {
			return "/x/" + n, nil
		}
		return "", errors.New("no")
	}}
	items := SetupItems(plan, env)
	byKey := map[string]SetupItem{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	if it := byKey["indexer:scip-python"]; it.Kind != SetupIndexer || !it.DefaultYes || it.Install == nil {
		t.Errorf("scip-python offer: %+v", it)
	}
	if it := byKey["toolchain:dotnet"]; it.DefaultYes || strings.Join(it.Commands[0], " ") != "brew install --cask dotnet-sdk" {
		t.Errorf(".NET SDK offer: %+v", it)
	}
	if it := byKey["indexer:scip-dotnet"]; it.After != "toolchain:dotnet" || !it.DefaultYes || it.Kind != SetupIndexer {
		t.Errorf("scip-dotnet offer: %+v", it)
	}
	var build SetupItem
	for _, it := range items {
		if it.Kind == SetupRepoBuild {
			build = it
		}
	}
	if build.DefaultYes || len(build.Prereq) != 0 || !strings.Contains(strings.Join(build.Commands[0], " "), "CMAKE_EXPORT_COMPILE_COMMANDS=ON") {
		t.Errorf("CMake offer: %+v", build)
	}
	// Off macOS no Homebrew command is offered for a toolchain.
	env.GOOS = "linux"
	for _, it := range SetupItems(plan, env) {
		if it.Key == "toolchain:dotnet" && (len(it.Commands) != 0 || !strings.Contains(it.Manual, "dotnet.microsoft.com")) {
			t.Errorf("linux .NET SDK offer: %+v", it)
		}
	}
}

func TestSetupItemsGoModDownload(t *testing.T) {
	dir := writeTree(t, "go.mod")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.22\n\nrequire rsc.io/quote v1.5.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := Plan{
		RepoRoot: dir,
		Steps: []Step{
			{Project: Project{Language: LangGo, Dir: ".", Markers: []string{"go.mod"}}},
		},
	}
	env := SetupEnv{
		GOOS: "darwin",
		LookPath: func(n string) (string, error) {
			if n == "go" {
				return "/usr/bin/go", nil
			}
			return "", errors.New("no")
		},
	}
	items := SetupItems(plan, env)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want 1 for go mod download", items)
	}
	it := items[0]
	if it.Kind != SetupFetch || !it.DefaultYes || len(it.Commands) != 1 || strings.Join(it.Commands[0], " ") != "go mod download" {
		t.Errorf("go mod download offer: %+v", it)
	}
}

func TestSetupItemsJavaDocker(t *testing.T) {
	plan := Plan{
		RepoRoot: t.TempDir(),
		Runner:   RunnerNative,
		Steps: []Step{
			{
				Project:      Project{Language: LangJava, Dir: ".", Markers: []string{"pom.xml"}},
				MissingTools: []Tool{ToolJava, ToolMaven},
			},
		},
	}
	env := SetupEnv{
		GOOS: "darwin",
		LookPath: func(n string) (string, error) {
			if n == "docker" {
				return "/usr/local/bin/docker", nil
			}
			return "", errors.New("no")
		},
	}
	items := SetupItems(plan, env)
	byKey := map[string]SetupItem{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	dockerOffer, ok := byKey["indexer:scip-java:docker"]
	if !ok {
		t.Fatalf("expected docker offer for Java, got: %+v", items)
	}
	if dockerOffer.DefaultYes {
		t.Errorf("expected DefaultYes=false for docker runner offer: %+v", dockerOffer)
	}
	if !strings.Contains(dockerOffer.Question, "Index Java in a container") {
		t.Errorf("unexpected question: %s", dockerOffer.Question)
	}
}
