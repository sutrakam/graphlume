package indexer

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/config"
)

// This file turns a Plan into the offers `graphlume init` walks a user
// through: for each language with no compiler proof, what is missing, what
// would be downloaded or run, where it comes from and roughly how big it is.
//
// Everything an offer says or runs comes from the tables below and from the
// Plan, which itself reads only marker file NAMES from the repository. The one
// repository-derived value that reaches a command is a directory, passed as
// the working directory, never as an argument.

// SetupKind classifies an offer by what saying yes costs.
type SetupKind string

const (
	// SetupIndexer is a pinned, checksum-verified indexer GraphLume installs
	// itself. Small, and the only kind whose default answer is yes.
	SetupIndexer SetupKind = "indexer"
	// SetupToolchain is a language toolchain (the .NET SDK, Node.js, a JDK,
	// Maven, Gradle). Large, installed system-wide; default no.
	SetupToolchain SetupKind = "toolchain"
	// SetupUserIndexer is something GraphLume has no pinned release of,
	// installed by the tool's own installer, or only reported. Default no.
	SetupUserIndexer SetupKind = "user-indexer"
	// SetupRepoBuild runs the repository's own build configuration to produce
	// compile_commands.json for scip-clang. It executes code the repository
	// chooses; default no, and never without an explicit yes or
	// --allow-repo-build.
	SetupRepoBuild SetupKind = "repo-build"
	// SetupFetch fetches module dependencies over the network without running
	// repository code (`go mod download`). Default yes.
	SetupFetch SetupKind = "fetch"
)

// SetupItem is one yes/no offer.
type SetupItem struct {
	// Key identifies the offer in accuracy.indexers.declined, so a declined
	// offer is not made again. Toolchains and indexers are machine-wide keys
	// ("toolchain:dotnet"); a repository build names its directory.
	Key  string
	Kind SetupKind

	// Language is who benefits, for display ("C#", "Python").
	Language string
	// Question is what the user is asked, without the [Y/n].
	Question string

	Missing  string
	Download string
	Source   string
	Size     string

	// Prereq are toolchain installs a repository build needs first (bear,
	// cmake), run only after an interactive yes: never from a flag alone.
	Prereq [][]string
	// Commands run on yes, in order. Empty means GraphLume cannot run it here
	// and Manual is printed instead.
	Commands [][]string
	// Dir is the absolute working directory for Commands (repository builds).
	Dir string
	// Dirs, when non-empty, runs Commands across each of these directories in order.
	Dirs []string
	// Manual is the command or URL to show, and to print when not run.
	Manual string
	// Notes are printed under the offer.
	Notes []string

	// Install is set for SetupIndexer.
	Install *Install

	// After names the Key of a toolchain this offer depends on: it is made
	// only once that toolchain is present.
	After string

	// PathAdd is a directory to put on PATH for this run, before the
	// commands run (Homebrew's keg-only rustup), so that a command finds what
	// the one before it installed and the resolve that follows finds the tool.
	PathAdd string

	// CopyFrom and CopyTo move a compilation database a build tree wrote into
	// the project directory, where detection looks for it.
	CopyFrom, CopyTo string

	DefaultYes bool
}

// SetupEnv is what offers depend on beyond the plan.
type SetupEnv struct {
	// GOOS selects Homebrew commands (darwin) or official download pages.
	GOOS string
	// LookPath finds a helper (brew, bear, cmake, scons). Nil means none.
	LookPath func(string) (string, error)
	// Home is the user's home directory.
	Home string
}

func (e SetupEnv) has(name string) bool {
	if e.LookPath == nil {
		return false
	}
	_, err := e.LookPath(name)
	return err == nil
}

// brew reports whether Homebrew commands should be offered.
func (e SetupEnv) brew() bool { return e.GOOS == "darwin" && e.has("brew") }

// LanguageLabel is a language as people write it.
func LanguageLabel(l Language) string {
	switch l {
	case LangGo:
		return "Go"
	case LangTypeScript:
		return "TypeScript/JavaScript"
	case LangPython:
		return "Python"
	case LangJava:
		return "Java"
	case LangCPP:
		return "C/C++"
	case LangRust:
		return "Rust"
	case LangCSharp:
		return "C#"
	case LangRuby:
		return "Ruby"
	}
	return string(l)
}

// toolchainOffer is how one missing toolchain is obtained.
type toolchainOffer struct {
	name string
	brew []string
	url  string
	size string
	note string
}

// toolchainOffers is the closed set of toolchains setup can offer. Sizes are
// approximate and stated as such.
var toolchainOffers = map[Tool]toolchainOffer{
	ToolNode: {
		name: "Node.js (node and npm)", brew: []string{"brew", "install", "node"},
		url: "https://nodejs.org/en/download", size: "about 30 MB download, about 100 MB installed",
	},
	ToolDotnet: {
		name: "the .NET SDK (dotnet)", brew: []string{"brew", "install", "--cask", "dotnet-sdk"},
		url: "https://dotnet.microsoft.com/download", size: "about 220 MB download, about 700 MB installed",
	},
	ToolJava: {
		name: "a JDK (java)", brew: []string{"brew", "install", "--cask", "temurin"},
		url: "https://adoptium.net", size: "about 190 MB download, about 330 MB installed",
		note: "Java is then indexed only with --allow-repo-build, because scip-java runs the project's own Maven or Gradle build, or with accuracy.indexers.runner: docker.",
	},
	ToolMaven: {
		name: "Maven (mvn)", brew: []string{"brew", "install", "maven"},
		url: "https://maven.apache.org/download.cgi", size: "about 10 MB, plus a JDK",
		note: "Java is then indexed only with --allow-repo-build, because scip-java runs the project's own Maven build, or with accuracy.indexers.runner: docker.",
	},
	ToolGradle: {
		name: "Gradle", brew: []string{"brew", "install", "gradle"},
		url: "https://gradle.org/install/", size: "about 140 MB, plus a JDK",
		note: "Java is then indexed only with --allow-repo-build, because scip-java runs the project's own Gradle build, or with accuracy.indexers.runner: docker.",
	},
	ToolGo: {
		name: "Go (go)", brew: []string{"brew", "install", "go"},
		url: "https://go.dev/dl/", size: "about 60 MB download, about 250 MB installed",
	},
}

// indexerSize is the approximate installed size of a pinned indexer.
var indexerSize = map[Indexer]string{
	SCIPPython:     "about 20 MB installed",
	SCIPTypeScript: "about 25 MB installed, with the TypeScript compiler it uses",
	SCIPGo:         "about 15 MB installed",
	SCIPDotnet:     "about 160 MB installed (a 57 MB download)",
	SCIPRuby:       "about 20 MB installed",
}

// LooseLanguage is a language with source files and no project to index them
// with a compiler.
type LooseLanguage struct {
	Language Language
	Files    int
	// Dir is where the build lives (C/C++), or "." when unknown.
	Dir string
	// Why says why there is no project, in a sentence.
	Why string
}

// Unplanned lists languages with source files but no detected project. A
// handful of Python scripts with no pyproject.toml, or a C++ tree built with
// SCons, used to be absent from `indexers list` altogether; this is the row
// that says they exist and what a compiler pass for them needs.
func (p Plan) Unplanned() []LooseLanguage {
	has := map[Language]bool{}
	for _, pr := range p.Projects {
		has[pr.Language] = true
	}
	var out []LooseLanguage
	for _, l := range []Language{LangCPP, LangCSharp, LangPython} {
		n := p.Sources[l]
		if n == 0 || has[l] {
			continue
		}
		ll := LooseLanguage{Language: l, Files: n, Dir: "."}
		switch l {
		case LangPython:
			ll.Why = "no pyproject.toml, setup.py, setup.cfg, requirements.txt or Pipfile, so there is no Python project " +
				"for scip-python to index; add one at the root of the Python code to plan it"
		case LangCSharp:
			ll.Why = "no .csproj, so there is no .NET project for scip-dotnet to load"
		case LangCPP:
			b := FindCBuild(p.RepoRoot, ".")
			ll.Dir = b.Dir
			ll.Why = "no compile_commands.json, which scip-clang reads"
		}
		out = append(out, ll)
	}
	return out
}

// CBuild is the build system found for C or C++ code.
type CBuild struct {
	// System is cmake, meson, scons, make, or "" when none was found.
	System string
	// Dir is the directory holding the build file, relative to the repository
	// root, slash-separated.
	Dir string
	// File is the build file's name.
	File string
}

// cBuildFiles are checked in this order within one directory.
var cBuildFiles = []struct{ file, system string }{
	{"CMakeLists.txt", "cmake"},
	{"meson.build", "meson"},
	{"SConstruct", "scons"},
	{"GNUmakefile", "make"},
	{"Makefile", "make"},
	{"makefile", "make"},
}

// cBuildMaxDepth bounds the search below the starting directory.
const cBuildMaxDepth = 2

// FindCBuild finds the shallowest C or C++ build file at or below dir (relative
// to repoRoot), breadth first and at most two levels down. Like Detect it
// reads names only, never a file's content.
func FindCBuild(repoRoot, dir string) CBuild {
	if dir == "" {
		dir = "."
	}
	level := []string{dir}
	for depth := 0; depth <= cBuildMaxDepth && len(level) > 0; depth++ {
		var next []string
		for _, d := range level {
			abs := filepath.Join(repoRoot, filepath.FromSlash(d))
			for _, f := range cBuildFiles {
				if fi, err := os.Lstat(filepath.Join(abs, f.file)); err == nil && fi.Mode().IsRegular() {
					return CBuild{System: f.system, Dir: d, File: f.file}
				}
			}
			entries, err := os.ReadDir(abs)
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() || skipDirs[name] || strings.HasPrefix(name, ".") {
					continue
				}
				next = append(next, path.Join(d, name))
			}
		}
		sort.Strings(next)
		level = next
	}
	return CBuild{Dir: dir}
}

// SetupItems lists the offers for a plan, in the order they are asked:
// language by language, a toolchain before the indexer that needs it.
func SetupItems(plan Plan, env SetupEnv) []SetupItem {
	if plan.Accuracy == config.AccuracyFast {
		return nil
	}
	var items []SetupItem
	seen := map[string]bool{}
	add := func(it SetupItem) {
		if !seen[it.Key] {
			seen[it.Key] = true
			items = append(items, it)
		}
	}
	canProvision := plan.ProvisionPolicy != config.ProvisionNever

	var goModDirs []string
	for _, s := range plan.Steps {
		if s.Excluded {
			continue
		}
		if s.Project.Language == LangGo && s.Project.hasMarker("go.mod") {
			modFile := filepath.Join(plan.RepoRoot, filepath.FromSlash(s.Project.Dir), "go.mod")
			if hasGoModDependencies(modFile) {
				goModDirs = append(goModDirs, s.Project.Dir)
			}
			continue
		}
	}
	if len(goModDirs) > 0 {
		if env.has("go") {
			add(goModDownloadItem(plan.RepoRoot, goModDirs))
		} else {
			if it, ok := toolchainItem(ToolGo, LangGo, env); ok {
				add(it)
			}
		}
	}

	for _, s := range plan.Steps {
		if s.Excluded {
			continue
		}
		if s.Project.Language == LangGo {
			continue
		}
		if s.Project.Language == LangJava && env.has("docker") && plan.Runner != RunnerDocker {
			add(SetupItem{
				Key:      "indexer:scip-java:docker",
				Kind:     SetupUserIndexer,
				Language: LanguageLabel(LangJava),
				Question: "Index Java in a container (repo build runs isolated)?",
				Missing:  "accuracy.indexers.runner is native; container runner isolates the build",
				Download: "sourcegraph/scip-java pinned image",
				Manual:   "graphlume config set accuracy.indexers.runner docker",
				Notes: []string{
					"scip-java runs in a container with the repository mounted read-only",
					"requires no host JDK, Maven or Gradle",
				},
				DefaultYes: false,
			})
		}
		lang := s.Project.Language
		switch {
		case len(s.MissingTools) > 0:
			for _, t := range s.MissingTools {
				if t == ToolCargo && lang == LangRust {
					add(rustToolchainItem(env, true))
					continue
				}
				if it, ok := toolchainItem(t, lang, env); ok {
					add(it)
				}
			}
			after := "toolchain:" + string(s.MissingTools[0])
			if len(s.MissingTools) > 1 {
				// Java with neither a JDK nor Maven: the follow-up waits for
				// all of them, and only the first is checked here; the plan
				// is computed again after any install.
				after = ""
			}
			if after == "" {
				continue
			}
			switch {
			case (lang == LangTypeScript || lang == LangPython) && canProvision && s.Pin.verifiable() == nil &&
				s.Pin.Channel == ChannelNPM:
				add(indexerItem(s.Pin, lang, after))
			case lang == LangCSharp && canProvision && s.Pin.verifiable() == nil:
				add(indexerItem(s.Pin, lang, after))
			}
		case s.Blocked && s.NeedsProvision && lang == LangCSharp:
			// The run itself needs --allow-repo-build; installing the pinned
			// indexer runs nothing of the repository's.
			if canProvision && s.Pin.verifiable() == nil {
				add(indexerItem(s.Pin, lang, ""))
			}
		case s.Blocked && s.NeedsProvision && s.Pin.Channel == ChannelUser:
			if lang == LangRust {
				add(rustToolchainItem(env, false))
			}
		case s.Blocked && lang == LangCPP && !s.Project.hasMarker("compile_commands.json"):
			add(cppBuildItem(plan.RepoRoot, s.Project.Dir, env))
		case !s.Blocked && s.NeedsProvision && canProvision:
			add(indexerItem(s.Pin, lang, ""))
		}
	}
	for _, l := range plan.Unplanned() {
		if l.Language == LangCPP {
			add(cppBuildItem(plan.RepoRoot, l.Dir, env))
			continue
		}
		// Nothing to install or run: the code has no project a compiler
		// indexer could load. Said once, so the closing line naming the
		// language as graded is not a surprise.
		pin, _ := PinFor(l.Language)
		add(SetupItem{
			Key:      "info:" + string(l.Language),
			Kind:     SetupUserIndexer,
			Language: LanguageLabel(l.Language),
			Missing:  fmt.Sprintf("a project for %s to load (%d %s, %s)", pin.Indexer, l.Files, filesWord(l.Files), "no project file"),
			Notes:    []string{strings.ToUpper(l.Why[:1]) + l.Why[1:] + "."},
		})
	}
	return items
}

func toolchainItem(t Tool, lang Language, env SetupEnv) (SetupItem, bool) {
	o, ok := toolchainOffers[t]
	if !ok {
		return SetupItem{}, false
	}
	it := SetupItem{
		Key:      "toolchain:" + string(t),
		Kind:     SetupToolchain,
		Language: LanguageLabel(lang),
		Question: "Install " + o.name + " now?",
		Missing:  o.name + ", which the " + LanguageLabel(lang) + " compiler indexer needs",
		Download: o.name,
		Size:     o.size,
	}
	if o.brew != nil && env.brew() {
		it.Commands = [][]string{o.brew}
		it.Manual = strings.Join(o.brew, " ")
		it.Source = "Homebrew (" + o.url + " is the official download)"
	} else {
		it.Manual = "download and install it from " + o.url
		it.Source = o.url
	}
	if o.note != "" {
		it.Notes = append(it.Notes, o.note)
	}
	return it, true
}

func indexerItem(pin Pin, lang Language, after string) SetupItem {
	it := pinnedIndexerItem(pin, lang, after)
	if lang == LangCSharp {
		it.Notes = append(it.Notes,
			"scip-dotnet "+pin.Version+" runs on the .NET 10 runtime.",
			"C# is then indexed only with --allow-repo-build, because scip-dotnet loads the project through MSBuild "+
				"and restores its NuGet packages, which runs code the repository defines.")
	}
	return it
}

func pinnedIndexerItem(pin Pin, lang Language, after string) SetupItem {
	in := Install{Indexer: pin.Indexer, Language: lang, Ref: pin.Ref(), Source: pin.Source(), Pin: pin}
	size := indexerSize[pin.Indexer]
	if size == "" {
		size = "small"
	}
	return SetupItem{
		Key:        "indexer:" + string(pin.Indexer),
		Kind:       SetupIndexer,
		Language:   LanguageLabel(lang),
		Question:   "Install " + string(pin.Indexer) + " " + pin.Version + "?",
		Missing:    string(pin.Indexer) + ", the " + LanguageLabel(lang) + " compiler indexer",
		Download:   pin.Ref() + " (pinned version)",
		Source:     pin.Source(),
		Size:       size,
		Manual:     "graphlume indexers install",
		Install:    &in,
		After:      after,
		DefaultYes: true,
	}
}

// rustToolchainItem offers rust-analyzer, and the Rust toolchain under it when
// cargo is missing too. Both come from rustup, so one yes covers them: with
// rustup present, `rustup component add rust-analyzer` (after `rustup default
// stable` when there is no toolchain yet); with Homebrew, `brew install
// rustup` first; otherwise the official page is printed and nothing is run.
// It is a toolchain offer: installed system-wide, so the default is no.
func rustToolchainItem(env SetupEnv, cargoMissing bool) SetupItem {
	it := SetupItem{
		Key:      "toolchain:rust",
		Kind:     SetupToolchain,
		Language: "Rust",
		Question: "Install rust-analyzer with rustup now?",
		Missing:  "rust-analyzer, which indexes Rust",
		Download: "the rust-analyzer component for your toolchain",
		Source:   "the Rust project, through rustup",
		Size:     "about 40 MB installed",
		Notes: []string{"Rust is then indexed without running the repository's build scripts or procedural macros; " +
			"--allow-repo-build also runs them."},
	}
	if cargoMissing {
		it.Question = "Install the Rust toolchain and rust-analyzer with rustup now?"
		it.Missing = "cargo and rust-analyzer, which index Rust"
		it.Download = "the stable Rust toolchain and the rust-analyzer component"
		it.Size = "about 300 MB installed"
	}
	toolchain := []string{"rustup", "default", "stable"}
	component := []string{"rustup", "component", "add", "rust-analyzer"}
	switch {
	case env.has("rustup"):
		if cargoMissing {
			it.Commands = append(it.Commands, toolchain)
		}
		it.Commands = append(it.Commands, component)
	case env.brew():
		it.Source = "Homebrew, then the Rust project through rustup"
		it.Commands = [][]string{{"brew", "install", "rustup"}, toolchain, component}
		// Homebrew keeps rustup out of the default PATH (the formula is
		// keg-only), so the commands that follow it need its directory.
		if brewPath, err := env.LookPath("brew"); err == nil {
			it.PathAdd = filepath.Join(filepath.Dir(filepath.Dir(brewPath)), "opt", "rustup", "bin")
		}
	}
	if len(it.Commands) > 0 {
		it.Manual = joinArgv(it.Commands)
		return it
	}
	it.Source = "https://rustup.rs"
	it.Manual = "install Rust from https://rustup.rs, then run: rustup component add rust-analyzer"
	return it
}

// compDBDir is where a CMake or Meson configure writes its build tree: inside
// .graphlume, which is ignored by git and by GraphLume's own walk.
const compDBDir = ".graphlume/compdb"

func cppBuildItem(repoRoot, dir string, env SetupEnv) SetupItem {
	b := FindCBuild(repoRoot, dir)
	absDir := filepath.Join(repoRoot, filepath.FromSlash(b.Dir))
	label := b.Dir
	if label == "." {
		label = "the repository root"
	}
	it := SetupItem{
		Key:      "repo-build:" + absDir,
		Kind:     SetupRepoBuild,
		Language: "C/C++",
		Missing:  "compile_commands.json, the compilation database scip-clang reads",
		Dir:      absDir,
		Notes: []string{
			"This runs the repository's own build scripts (" + b.File + " in " + label + "), which execute code from the repository.",
		},
	}
	buildTree := filepath.Join(repoRoot, filepath.FromSlash(compDBDir))
	needTool := func(tool, formula string) {
		if env.has(tool) {
			return
		}
		if env.brew() {
			it.Prereq = append(it.Prereq, []string{"brew", "install", formula})
		} else {
			it.Notes = append(it.Notes, tool+" is not installed: install it first.")
		}
	}
	switch b.System {
	case "cmake":
		it.Question = "Run CMake's configure step to generate compile_commands.json?"
		it.Download = "nothing from GraphLume; CMake configures the project"
		needTool("cmake", "cmake")
		it.Commands = [][]string{{"cmake", "-S", ".", "-B", buildTree, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON"}}
		it.CopyFrom = filepath.Join(buildTree, "compile_commands.json")
	case "meson":
		it.Question = "Run Meson's setup step to generate compile_commands.json?"
		it.Download = "nothing from GraphLume; Meson configures the project"
		needTool("meson", "meson")
		it.Commands = [][]string{{"meson", "setup", buildTree, "."}}
		it.CopyFrom = filepath.Join(buildTree, "compile_commands.json")
	case "scons", "make":
		it.Question = "Build the project under bear to record compile_commands.json?"
		it.Download = "bear (records the compiler calls of a build), if it is missing"
		needTool("bear", "bear")
		if b.System == "scons" {
			needTool("scons", "scons")
		}
		it.Commands = [][]string{{"bear", "--", b.System}}
		it.Notes = append(it.Notes, "It performs a full build of the project, which can take a long time.")
	default:
		it.Question = ""
		it.Download = "nothing"
		it.Notes = []string{"No CMake, Meson, SCons or make build was found. Generate compile_commands.json with your " +
			"build (for example under bear) and copy it beside the code."}
		it.Manual = "see https://clang.llvm.org/docs/JSONCompilationDatabase.html"
		return it
	}
	if len(it.Prereq) > 0 && env.brew() {
		it.Source = "Homebrew, for the build helpers (" + joinArgv(it.Prereq) + ")"
		it.Size = "a few MB for each helper"
	} else {
		it.Source = "the repository's own build"
		it.Size = "no download"
	}
	it.CopyTo = filepath.Join(absDir, "compile_commands.json")
	if it.CopyFrom == "" {
		it.CopyTo = ""
	}
	cmds := append(append([][]string(nil), it.Prereq...), it.Commands...)
	it.Manual = "cd " + absDir + " && " + joinArgv(cmds)
	if it.CopyFrom != "" {
		it.Manual += " && cp " + it.CopyFrom + " " + it.CopyTo
	}
	if !env.has("scip-clang") {
		pin, _ := PinFor(LangCPP)
		it.Notes = append(it.Notes, "scip-clang itself is not installed: "+pin.InstallHint+".")
	}
	it.Notes = append(it.Notes, "Then index C/C++ with: graphlume resolve --allow-repo-build")
	return it
}

func filesWord(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

func joinArgv(cmds [][]string) string {
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = strings.Join(c, " ")
	}
	return strings.Join(parts, " && ")
}

// GradedLanguages lists, by label, the languages in the plan (and with loose
// sources) that no compiler will check as the plan stands: their answers stay
// graded rather than proven. Go is never listed, since the type checker built
// into GraphLume proves it.
func GradedLanguages(plan Plan) []string {
	proven := map[Language]bool{}
	graded := map[Language]bool{}
	for _, s := range plan.Steps {
		l := s.Project.Language
		if l == LangGo {
			continue
		}
		if !s.Blocked && !s.NeedsProvision {
			proven[l] = true
		} else {
			graded[l] = true
		}
	}
	for _, l := range plan.Unplanned() {
		graded[l.Language] = true
	}
	var out []string
	for l := range graded {
		if !proven[l] {
			out = append(out, LanguageLabel(l))
		}
	}
	sort.Strings(out)
	return out
}

func hasGoModDependencies(modFile string) bool {
	raw, ok := readLimited(modFile, maxCargoManifest)
	if !ok {
		return false
	}
	return bytes.Contains(raw, []byte("require"))
}

func goModDownloadItem(repoRoot string, projectDirs []string) SetupItem {
	var dirs []string
	for _, pd := range projectDirs {
		dirs = append(dirs, filepath.Join(repoRoot, filepath.FromSlash(pd)))
	}
	firstDir := repoRoot
	if len(dirs) > 0 {
		firstDir = dirs[0]
	}
	question := "Run `go mod download` to fetch module dependencies now?"
	if len(dirs) > 1 {
		question = fmt.Sprintf("Run `go mod download` across %d Go modules to fetch dependencies now?", len(dirs))
	}
	key := "fetch:go-mod-download"
	if len(projectDirs) == 1 {
		key += ":" + projectDirs[0]
	}
	return SetupItem{
		Key:        key,
		Kind:       SetupFetch,
		Language:   "Go",
		Question:   question,
		Missing:    "Go module dependencies (empty or incomplete module cache)",
		Download:   "dependencies declared in go.mod",
		Source:     "Go module proxy (proxy.golang.org), checked against go.sum",
		Size:       "depends on declared modules",
		Commands:   [][]string{{"go", "mod", "download"}},
		Dir:        firstDir,
		Dirs:       dirs,
		Manual:     "go mod download",
		Notes:      []string{"Fetches module dependencies without running repository code, so go/types can check packages with full fidelity instead of stubbed imports."},
		DefaultYes: true,
	}
}
