package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
)

func nativeCfg() config.Config {
	return config.Config{Accuracy: config.AccuracyAuto, ProvisionPolicy: config.ProvisionAuto, IndexerRunner: RunnerNative}
}

func TestDetectCPPAndRust(t *testing.T) {
	t.Parallel()
	root := writeRepo(t, map[string]string{
		"compile_commands.json":           "[]",
		"CMakeLists.txt":                  "",
		"lib/CMakeLists.txt":              "",   // nested in the root's CMake tree: one build
		"tools/own/compile_commands.json": "[]", // has its own database: stays
		"svc/Cargo.toml":                  "[package]\nname = \"svc\"\n",
		"meson/meson.build":               "",
	})
	ps, err := Detect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(projectDirs(ps), ",")
	want := "cpp:.,rust:svc,cpp:tools/own" // meson/ is nested in the root CMake tree
	if got != want {
		t.Errorf("projects = %s, want %s", got, want)
	}
	if !ps[0].hasMarker("compile_commands.json") {
		t.Errorf("root markers = %v, want compile_commands.json among them", ps[0].Markers)
	}
}

func TestCPPWithoutCompilationDatabaseCannotRunAndSaysWhy(t *testing.T) {
	t.Parallel()
	root := writeRepo(t, map[string]string{"CMakeLists.txt": "project(x)\n", "a.cpp": ""})
	r := newFakeRunner("scip-clang")
	plan, err := NewPlanWith(context.Background(), root, nativeCfg(), Options{Runner: r, AllowRepoBuild: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || !plan.Steps[0].Blocked {
		t.Fatalf("steps = %+v, want one blocked", plan.Steps)
	}
	for _, want := range []string{"cannot run", "no compile_commands.json", "CMAKE_EXPORT_COMPILE_COMMANDS", "cp build/compile_commands.json"} {
		if !strings.Contains(plan.Steps[0].Reason, want) {
			t.Errorf("reason lacks %q: %s", want, plan.Steps[0].Reason)
		}
	}
	for _, a := range r.argvs() {
		if strings.Contains(a, "scip-clang") {
			t.Errorf("scip-clang was asked something for a project it cannot index: %s", a)
		}
	}
}

func TestCPPIsRunnableWhenInstalledAndNeverInstalledWhenNot(t *testing.T) {
	t.Parallel()
	root := writeRepo(t, map[string]string{"compile_commands.json": "[]"})
	noOptIn, err := NewPlanWith(context.Background(), root, nativeCfg(), Options{Runner: newFakeRunner("scip-clang")})
	if err != nil {
		t.Fatal(err)
	}
	if len(noOptIn.Runnable()) != 0 || !strings.Contains(noOptIn.Steps[0].Reason,
		"indexing C/C++ runs the compilers and flags named in the repository's compile_commands.json, which executes code the repository chooses; re-run with --allow-repo-build") {
		t.Fatalf("an installed scip-clang without the opt-in: %+v, want blocked with that reason", noOptIn.Steps)
	}
	have, err := NewPlanWith(context.Background(), root, nativeCfg(),
		Options{Runner: newFakeRunner("scip-clang"), AllowRepoBuild: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(have.Runnable()) != 1 || have.Steps[0].Indexer != SCIPClang || have.Steps[0].Installed.Path == "" {
		t.Fatalf("steps = %+v, want scip-clang runnable from PATH", have.Steps)
	}

	r := newFakeRunner() // not installed, and the policy would allow an install
	miss, err := NewPlanWith(context.Background(), root, nativeCfg(), Options{Runner: r, AllowRepoBuild: true})
	if err != nil {
		t.Fatal(err)
	}
	s := miss.Steps[0]
	if !s.Blocked || !strings.Contains(s.Reason, "does not install it") || !strings.Contains(s.Reason, "scip-clang/releases") {
		t.Errorf("reason %q should say GraphLume does not install it and where to get it", s.Reason)
	}
	res, err := Execute(context.Background(), miss, Options{Runner: r, AssumeYes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Indexes) != 0 || len(r.calls()) != 0 {
		t.Errorf("Execute ran %v for a user-installed indexer that is absent", r.argvs())
	}
}

func TestRustNeedsCargoTheBuildOptInAndASinglePackage(t *testing.T) {
	t.Parallel()
	single := writeRepo(t, map[string]string{"Cargo.toml": "[package]\nname = \"a\"\n"})
	plan := func(root string, opts Options) Plan {
		p, err := NewPlanWith(context.Background(), root, nativeCfg(), opts)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	p := plan(single, Options{Runner: newFakeRunner("rust-analyzer")})
	if !strings.Contains(p.Steps[0].Reason, "cargo is not installed") {
		t.Errorf("without cargo: %q", p.Steps[0].Reason)
	}
	p = plan(single, Options{Runner: newFakeRunner("rust-analyzer", "cargo")})
	if p.Steps[0].Blocked || len(p.Steps[0].Notes) == 0 || !strings.Contains(p.Steps[0].Notes[0], "build scripts and procedural macros are not run") {
		t.Errorf("without the opt-in: %+v", p.Steps[0])
	}
	r := newFakeRunner("rust-analyzer", "cargo")
	p = plan(single, Options{Runner: r, AllowRepoBuild: true})
	if p.Steps[0].Blocked || p.Steps[0].Indexer != SCIPRustAnalyzer || len(p.Steps[0].Notes) != 0 {
		t.Errorf("with the opt-in: %+v", p.Steps[0])
	}
	// The argv, from the executor, without running anything real.
	r.install("rust-analyzer")
	r.handle("rust-analyzer", func(c Command) (Result, error) {
		if flagValue(c.Args, "--output") != "" && len(c.Args) > 0 && c.Args[0] == "scip" {
			return Result{}, os.WriteFile(flagValue(c.Args, "--output"), scipWithADocument(t), 0o600)
		}
		return Result{Stdout: []byte("rust-analyzer 1.80.0\n")}, nil
	})
	out := t.TempDir()
	res, err := Execute(context.Background(), p, Options{Runner: r, OutputDir: out, AllowRepoBuild: true})
	if err != nil || len(res.Indexes) != 1 {
		t.Fatalf("execute: %v, %+v", err, res)
	}
	var found bool
	for _, c := range r.calls() {
		if filepath.Base(c.Path) == "rust-analyzer" && len(c.Args) > 0 && c.Args[0] == "scip" {
			found = true
			if strings.Join(c.Args, " ") != "scip . --output "+filepath.Join(out, "rust.scip") || c.Dir != p.RepoRoot {
				t.Errorf("argv %v in %s, want `scip . --output <out>/rust.scip` in the project dir", c.Args, c.Dir)
			}
		}
	}
	if !found {
		t.Error("rust-analyzer scip never ran")
	}

	ws := writeRepo(t, map[string]string{
		"Cargo.toml":      "[workspace]\nmembers = [\"a\", \"b\"]\n",
		"a/Cargo.toml":    "[package]\nname = \"a\"\n",
		"b/Cargo.toml":    "[package]\nname = \"b\"\n",
		"solo/Cargo.toml": "[package]\nname = \"solo\"\n",
	})
	p = plan(ws, Options{Runner: newFakeRunner("rust-analyzer", "cargo"), AllowRepoBuild: true})
	var dirs []string
	for _, s := range p.Steps {
		dirs = append(dirs, s.Project.Dir)
	}
	if len(p.Steps) != 2 || p.Steps[0].Project.Dir != "." || p.Steps[0].Blocked || p.Steps[1].Project.Dir != "solo" {
		t.Errorf("a workspace: steps in %v = %+v, want . and solo", dirs, p.Steps)
	}
	if len(p.Steps[0].Notes) == 0 || !strings.Contains(p.Steps[0].Notes[0], "Cargo workspace: one run reads the root and its 2 member crates") {
		t.Errorf("workspace step notes = %v", p.Steps[0].Notes)
	}
}

func scipWithADocument(t *testing.T) []byte {
	t.Helper()
	// An index with one (empty) document: field 2, length 0, as helper_test.go does.
	return minimalSCIPIndex
}

func TestUserInstalledIndexersAreNotInThePinTable(t *testing.T) {
	t.Parallel()
	for _, p := range Pins() {
		if p.Channel == ChannelUser {
			t.Errorf("%s is in Pins(), the table CI keys on and GraphLume may fetch from", p.Indexer)
		}
	}
	for _, p := range UserInstalled() {
		if p.TarballSHA256 != "" || p.ImageDigest != "" || p.Version != "" || p.InstallHint == "" {
			t.Errorf("%s: a user-installed entry must carry no version or digest it cannot back, and a hint: %+v", p.Indexer, p)
		}
		if err := p.verifiable(); err == nil {
			t.Errorf("%s: provisioning would proceed on an entry with nothing to verify", p.Indexer)
		}
	}
	if _, ok := PinFor(LangCPP); !ok {
		t.Error("no entry for C/C++")
	}
	if _, ok := PinFor(LangRust); !ok {
		t.Error("no entry for Rust")
	}
	if _, ok := PinFor(LangCSharp); !ok {
		t.Error("no entry for C#")
	}
	if _, ok := PinFor(LangRuby); !ok {
		t.Error("no entry for Ruby")
	}
}

func TestRubyExecutionAndMoveIndex(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	root := writeRepo(t, map[string]string{
		"Gemfile": "source 'https://rubygems.org'\n",
		"app.rb":  "def hello; puts 'hi'; end\n",
	})
	binPath := filepath.Join(outDir, "scip-ruby")
	r := newFakeRunner(binPath, "scip-ruby")
	r.handle("scip-ruby", func(c Command) (Result, error) {
		hasMeta := false
		for i, a := range c.Args {
			if a == "--gem-metadata" && i+1 < len(c.Args) {
				hasMeta = true
				break
			}
		}
		if !hasMeta {
			return Result{ExitCode: 1, Stderr: []byte("missing --gem-metadata")}, nil
		}
		if err := os.WriteFile(filepath.Join(c.Dir, "index.scip"), minimalSCIPIndex, 0o600); err != nil {
			return Result{ExitCode: 1}, err
		}
		return Result{ExitCode: 0}, nil
	})

	ex := &Executor{Runner: r, RunnerKind: RunnerNative}

	p := Project{Language: LangRuby, Dir: "."}
	inst := Installed{Indexer: SCIPRuby, Path: binPath, Version: "0.5.1"}
	res, err := ex.Run(context.Background(), inst, p, root, outDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Bytes == 0 {
		t.Errorf("res.Bytes = 0")
	}
	if _, err := os.Stat(res.IndexPath); err != nil {
		t.Errorf("expected index at %s: %v", res.IndexPath, err)
	}
}

func TestSuggestNamesMissingIndexersAboveTheThreshold(t *testing.T) {
	t.Parallel()
	langs := []LanguageFiles{
		{"go", 500}, {"typescript", 6}, {"tsx", 5}, // 11 TypeScript-family files: over
		{"python", SuggestMinFiles - 1}, // one short
		{"cpp", 40}, {"c", 3},
		{"rust", 12}, {"markdown", 99},
	}
	r := newFakeRunner("scip-clang") // C++ is installed; everything else is not
	got := Suggest(context.Background(), r, nativeCfg(), langs)

	byLang := map[Language]Suggestion{}
	for _, s := range got {
		byLang[s.Language] = s
	}
	if len(got) != 2 {
		t.Fatalf("suggestions = %+v, want TypeScript and Rust only", got)
	}
	ts := byLang[LangTypeScript]
	if ts.Files != 11 || !ts.Pinned || ts.Command != "graphlume indexers install" || ts.Indexer != SCIPTypeScript {
		t.Errorf("typescript = %+v", ts)
	}
	rs := byLang[LangRust]
	if rs.Pinned || rs.Command != "rustup component add rust-analyzer" || rs.Files != 12 {
		t.Errorf("rust = %+v, want the tool's own install line, not a GraphLume command", rs)
	}
	for _, c := range r.calls() {
		t.Errorf("Suggest ran %v; it must only look", c.Argv())
	}

	fast := nativeCfg()
	fast.Accuracy = config.AccuracyFast
	if s := Suggest(context.Background(), r, fast, langs); len(s) != 0 {
		t.Errorf("accuracy fast suggested %+v", s)
	}
	only := nativeCfg()
	only.IndexerLanguages = []string{"python"}
	if s := Suggest(context.Background(), r, only, langs); len(s) != 0 {
		t.Errorf("a language the user excluded was suggested: %+v", s)
	}
}

func TestSuggestSaysWhyJavaIsNotInstalledByCommand(t *testing.T) {
	t.Parallel()
	got := Suggest(context.Background(), newFakeRunner(), nativeCfg(), []LanguageFiles{{"java", 30}})
	if len(got) != 1 || got[0].Pinned || !strings.Contains(got[0].Command, "runner to docker") {
		t.Errorf("java = %+v: natively the container pin is not something `indexers install` does", got)
	}
}
