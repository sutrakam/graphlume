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
	"github.com/sutrakam/graphlume/internal/indexer"
)

// fakeSetupRunner stands in for brew, dotnet, cmake and bear: it records what
// would run and, for cmake, writes the compilation database a configure
// would. Nothing is installed.
type fakeSetupRunner struct {
	tools map[string]bool
	ran   []string
	fail  map[string]bool
}

func (f *fakeSetupRunner) LookPath(name string) (string, error) {
	if f.tools[name] {
		return "/fake/" + name, nil
	}
	return "", errors.New("not found")
}

func (f *fakeSetupRunner) Run(_ context.Context, dir string, argv []string, _ io.Writer) error {
	f.ran = append(f.ran, strings.Join(argv, " "))
	if f.fail[argv[0]] {
		return errors.New("exit status 1")
	}
	if argv[0] == "cmake" {
		for i, a := range argv {
			if a == "-B" && i+1 < len(argv) {
				if err := os.MkdirAll(argv[i+1], 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(argv[i+1], "compile_commands.json"), []byte("[]"), 0o600)
			}
		}
	}
	if argv[0] == "bear" {
		return os.WriteFile(filepath.Join(dir, "compile_commands.json"), []byte("[]"), 0o600)
	}
	return nil
}

type setupHarness struct {
	t         *testing.T
	runner    *fakeSetupRunner
	provision []string
	cfgPath   string
}

// newSetupHarness isolates PATH, HOME and the user config, and replaces the
// terminal check, the command runner and the indexer download.
func newSetupHarness(t *testing.T, interactive bool, tools ...string) *setupHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-ins")
	}
	h := &setupHarness{t: t, runner: &fakeSetupRunner{tools: map[string]bool{}, fail: map[string]bool{}}}
	for _, tool := range tools {
		h.runner.tools[tool] = true
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\necho v22.0.0\n"), 0o700); err != nil { //nolint:gosec // test stand-in executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())
	cfgHome := t.TempDir()
	t.Setenv("GRAPHLUME_CONFIG_HOME", cfgHome)
	h.cfgPath = filepath.Join(cfgHome, "config.yml")

	oldI, oldR, oldP, oldG := setupInteractive, newSetupRunner, setupProvision, setupGOOS
	t.Cleanup(func() { setupInteractive, newSetupRunner, setupProvision, setupGOOS = oldI, oldR, oldP, oldG })
	setupInteractive = func() bool { return interactive }
	newSetupRunner = func() setupRunner { return h.runner }
	setupGOOS = "darwin"
	setupProvision = func(_ context.Context, _ indexer.Plan, ins []indexer.Install, _ indexer.Options) ([]indexer.Installed, []indexer.ProvisionFailure) {
		var done []indexer.Installed
		for _, in := range ins {
			h.provision = append(h.provision, string(in.Indexer))
			done = append(done, indexer.Installed{Indexer: in.Indexer, Version: in.Pin.Version})
		}
		return done, nil
	}
	return h
}

func (h *setupHarness) run(dir, input string, yes, allowBuild bool) string {
	h.t.Helper()
	cfg, err := config.Load(dir)
	if err != nil {
		h.t.Fatal(err)
	}
	g := &Global{cfg: cfg}
	var out bytes.Buffer
	runGuidedSetup(context.Background(), g, strings.NewReader(input), &out, yes, allowBuild)
	return out.String()
}

func pythonRepo(t *testing.T) string {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n", "app.py": "def f():\n    return 1\n"})
	return dir
}

func csharpRepo(t *testing.T) string {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"App.csproj": "<Project/>\n", "Program.cs": "class P {}\n"})
	return dir
}

func cmakeRepo(t *testing.T) string {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"CMakeLists.txt": "project(x)\n", "main.cpp": "int main() { return 0; }\n"})
	return dir
}

func goRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":  "module example.com/m\n\ngo 1.22\n\nrequire rsc.io/quote v1.5.2\n",
		"main.go": "package main\nfunc main() {}\n",
	})
	return dir
}

func TestSetupGoModDownload(t *testing.T) {
	h := newSetupHarness(t, true, "go")
	out := h.run(goRepo(t), "\n", false, false)
	mustContain(t, out, "go mod download", "[Y/n]")
	if len(h.runner.ran) != 1 || h.runner.ran[0] != "go mod download" {
		t.Fatalf("ran %v, want [go mod download]", h.runner.ran)
	}
}

// In a repository with multiple Go modules, setup asks ONCE and runs download across all of them.
func TestSetupMultiGoModDownloadBatched(t *testing.T) {
	h := newSetupHarness(t, true, "go")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"svc1/go.mod":  "module example.com/svc1\ngo 1.22\nrequire rsc.io/quote v1.5.2\n",
		"svc1/main.go": "package main\nfunc main() {}\n",
		"svc2/go.mod":  "module example.com/svc2\ngo 1.22\nrequire rsc.io/quote v1.5.2\n",
		"svc2/main.go": "package main\nfunc main() {}\n",
		"svc3/go.mod":  "module example.com/svc3\ngo 1.22\nrequire rsc.io/quote v1.5.2\n",
		"svc3/main.go": "package main\nfunc main() {}\n",
	})
	out := h.run(dir, "\n", false, false)
	mustContain(t, out, "Run `go mod download` across 3 Go modules", "built-in go/types")
	// Must run go mod download 3 times (once per module), but asked only ONCE.
	if len(h.runner.ran) != 3 {
		t.Fatalf("ran %v, want 3 go mod download executions", h.runner.ran)
	}
}

func TestSetupGoModDownloadNonInteractive(t *testing.T) {
	h := newSetupHarness(t, false, "go")
	out := h.run(goRepo(t), "", true, false)
	if len(h.runner.ran) != 1 || h.runner.ran[0] != "go mod download" {
		t.Fatalf("--yes ran %v, want [go mod download]\n%s", h.runner.ran, out)
	}
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// A pinned, verified indexer is offered with a default of yes: Enter installs.
func TestSetupPinnedIndexerDefaultsToYes(t *testing.T) {
	h := newSetupHarness(t, true)
	out := h.run(pythonRepo(t), "\n", false, false)
	mustContain(t, out, "Missing:", "scip-python", "From:", "npm registry", "Size:", "[Y/n]", "Installed scip-python")
	if len(h.provision) != 1 || h.provision[0] != "scip-python" {
		t.Fatalf("provisioned %v, want scip-python", h.provision)
	}
}

// A toolchain defaults to no: Enter installs nothing and the answer is
// remembered in the user config, not in the repository.
func TestSetupToolchainDefaultsToNoAndIsRemembered(t *testing.T) {
	h := newSetupHarness(t, true, "brew")
	dir := csharpRepo(t)
	out := h.run(dir, "\n", false, false)
	mustContain(t, out, "the .NET SDK (dotnet)", "brew install --cask dotnet-sdk", "about 220 MB", "[y/N]", "Remembered in")
	if len(h.runner.ran) != 0 {
		t.Fatalf("ran %v after a default no", h.runner.ran)
	}
	b, err := os.ReadFile(h.cfgPath)
	if err != nil || !strings.Contains(string(b), "toolchain:dotnet") {
		t.Fatalf("decline not remembered in user config: %v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, config.RepoConfigName)); err == nil {
		t.Fatal("setup wrote into the repository's config")
	}

	// Asked again later: not asked, and nothing runs.
	h.runner.ran = nil
	out = h.run(dir, "y\ny\n", false, false)
	mustContain(t, out, "you declined it before", "--ask-again")
	if strings.Contains(out, "[y/N]") || len(h.runner.ran) != 0 {
		t.Fatalf("a remembered decline was asked again (ran %v):\n%s", h.runner.ran, out)
	}

	// --ask-again forgets it.
	cmd := newIndexersInstallCmd(&Global{})
	cmd.SetArgs([]string{"--ask-again"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	mustContain(t, buf.String(), "Forgot 1 declined setup answer")
	out = h.run(dir, "\n", false, false)
	mustContain(t, out, "[y/N]")
}

// Yes to the toolchain runs the Homebrew command, then offers the indexer that
// depends on it, and provisions it.
func TestSetupToolchainYesThenDependentIndexer(t *testing.T) {
	h := newSetupHarness(t, true, "brew")
	out := h.run(csharpRepo(t), "y\ny\n", false, false)
	want := []string{"brew install --cask dotnet-sdk"}
	if strings.Join(h.runner.ran, "|") != strings.Join(want, "|") {
		t.Fatalf("ran %v, want %v\n%s", h.runner.ran, want, out)
	}
	if len(h.provision) != 1 || h.provision[0] != "scip-dotnet" {
		t.Errorf("provisioned %v, want [scip-dotnet]", h.provision)
	}
	mustContain(t, out, "Installed scip-dotnet 0.2.16")
}

// Without Homebrew a toolchain is not installed: its official page is printed.
func TestSetupToolchainWithoutBrewPrintsURL(t *testing.T) {
	h := newSetupHarness(t, true)
	out := h.run(csharpRepo(t), "y\n", false, false)
	mustContain(t, out, "https://dotnet.microsoft.com/download", "To install it yourself")
	if len(h.runner.ran) != 0 {
		t.Fatalf("ran %v without Homebrew", h.runner.ran)
	}
}

// Without a terminal: the plan is printed and nothing is installed.
func TestSetupNonInteractivePlanOnly(t *testing.T) {
	h := newSetupHarness(t, false, "brew", "cmake")
	for _, dir := range []string{pythonRepo(t), csharpRepo(t), cmakeRepo(t)} {
		out := h.run(dir, "", false, false)
		mustContain(t, out, "plan only", "Nothing is installed")
	}
	if len(h.provision) != 0 || len(h.runner.ran) != 0 {
		t.Fatalf("non-interactive setup acted: provisioned %v, ran %v", h.provision, h.runner.ran)
	}
	if _, err := os.Stat(h.cfgPath); err == nil {
		t.Fatal("a non-interactive run remembered an answer nobody gave")
	}
}

// --yes accepts the default-yes offers only: the pinned indexer, never a
// toolchain and never a repository build.
func TestSetupYesScope(t *testing.T) {
	h := newSetupHarness(t, false, "brew", "cmake")
	h.run(pythonRepo(t), "", true, false)
	if len(h.provision) != 1 {
		t.Fatalf("--yes did not install the pinned indexer: %v", h.provision)
	}
	out := h.run(csharpRepo(t), "", true, false)
	mustContain(t, out, "a toolchain is installed only after a yes at a terminal")
	out = h.run(cmakeRepo(t), "", true, false)
	mustContain(t, out, "--allow-repo-build")
	if len(h.runner.ran) != 0 {
		t.Fatalf("--yes ran %v", h.runner.ran)
	}
}

// When interactive=true and yes=true, --yes takes effect without asking or reading stdin.
func TestSetupInteractiveWithYesFlagDoesNotPrompt(t *testing.T) {
	h := newSetupHarness(t, true)
	// Empty input string: if it prompted, it would read EOF and fail/not install.
	out := h.run(pythonRepo(t), "", true, false)
	mustContain(t, out, "Installed scip-python")
	if len(h.provision) != 1 || h.provision[0] != "scip-python" {
		t.Fatalf("provisioned %v, want scip-python", h.provision)
	}
}

// A repository build runs only on an explicit yes or --allow-repo-build, and
// --allow-repo-build never installs the helpers it needs.
func TestSetupRepoBuildGate(t *testing.T) {
	h := newSetupHarness(t, true, "cmake")
	dir := cmakeRepo(t)
	out := h.run(dir, "\n", false, false)
	mustContain(t, out, "runs the repository's own build scripts", "[y/N]")
	if len(h.runner.ran) != 0 {
		t.Fatalf("default no ran %v", h.runner.ran)
	}

	h2 := newSetupHarness(t, false, "cmake")
	dir2 := cmakeRepo(t)
	h2.run(dir2, "", false, true)
	if len(h2.runner.ran) != 1 || !strings.Contains(h2.runner.ran[0], "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON") {
		t.Fatalf("--allow-repo-build ran %v", h2.runner.ran)
	}
	if _, err := os.Stat(filepath.Join(dir2, "compile_commands.json")); err != nil {
		t.Fatalf("compile_commands.json not copied beside the project: %v", err)
	}

	h3 := newSetupHarness(t, false, "brew")
	out = h3.run(cmakeRepo(t), "", false, true)
	mustContain(t, out, "never installed without a yes")
	if len(h3.runner.ran) != 0 {
		t.Fatalf("--allow-repo-build installed a helper: %v", h3.runner.ran)
	}
}

// A failed install is reported and setup goes on; it never returns an error.
func TestSetupFailureContinues(t *testing.T) {
	h := newSetupHarness(t, true, "brew")
	h.runner.fail["brew"] = true
	out := h.run(csharpRepo(t), "y\n", false, false)
	mustContain(t, out, "Failed:", "graded edges only", "stays graded")
}

// The closing line names the languages that stay graded.
func TestSetupClosingLine(t *testing.T) {
	h := newSetupHarness(t, true)
	out := h.run(csharpRepo(t), "\n", false, false)
	mustContain(t, out, "C# stays graded, not proven", "graphlume indexers list shows this again")
}

func runIndexersList(t *testing.T, dir string) string {
	t.Helper()
	cfg := config.Defaults()
	cfg.RepoRoot = dir
	cmd := newIndexersListCmd(&Global{cfg: cfg})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A C++ tree built with SCons, a Visual Studio solution and a few Python
// scripts with no project file: the shape of a game engine repository.
func TestIndexersListRowsForUnplannedLanguages(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"neo/SConstruct": "", "neo/doom.sln": "", "neo/game/Game.cpp": "", "neo/game/Game.h": "",
		"tools/a.py": "", "tools/b.py": "",
	})
	out := runIndexersList(t, dir)
	mustContain(t, out,
		"cpp", "scip-clang", "blocked: 2 files, no compile_commands.json", "neo",
		"python", "not planned: 2 files, no project",
		"needs compile_commands.json from a working build", "--allow-repo-build", "SCons", "bear -- scons")
	if strings.Contains(out, "Toolchain:") {
		t.Errorf("printed an empty Toolchain line:\n%s", out)
	}
	if strings.Contains(out, "csharp") {
		t.Errorf("a C++ solution file was planned as C#:\n%s", out)
	}
}

// scip-dotnet's row says the .NET SDK comes first.
func TestIndexersListDotnetNeedsSDK(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	out := runIndexersList(t, csharpRepo(t))
	mustContain(t, out, "blocked: needs dotnet", ".NET SDK first", "dotnet tool install --global scip-dotnet")
}

// A CMake project gets the configure command in the C/C++ note.
func TestIndexersListCMakeNote(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	out := runIndexersList(t, cmakeRepo(t))
	mustContain(t, out, "CMake", "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON")
}
