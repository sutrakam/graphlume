package fs

import (
	"reflect"
	"sort"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
)

// hiddenTree is one project with both kinds of hidden entry in it: the
// configuration a repository commits, and the state editors and tools leave
// beside the code.
var hiddenTree = map[string]string{
	"main.go":     "package main\n",
	"src/app.go":  "package src\n",
	"src/data.go": "package src\n",

	// Source: automation and configuration.
	".github/workflows/ci.yml": "on: push\n",
	".gitlab-ci.yml":           "stages: [test]\n",
	".eslintrc.js":             "module.exports = {}\n",
	".env.example":             "API_BASE=\n",
	".editorconfig":            "root = true\n",
	".mcp.json":                "{}\n",
	"src/.eslintrc.json":       "{}\n",
	".devcontainer/setup.sh":   "#!/bin/sh\n",

	// Scratch: editor and tool state.
	".syms.json":                     `{"symbols": []}` + "\n",
	".DS_Store":                      "x",
	".main.go.swp":                   "x",
	".#main.go":                      "x",
	".idea/workspace.xml":            "<project/>\n",
	".vscode/settings.json":          "{}\n",
	".venv/lib/site.py":              "x = 1\n",
	".pytest_cache/v/cache/lastfail": "{}\n",
	"src/.cache/gen.go":              "package gen\n",
	".claude/worktrees/copy/main.go": "package main\n",
	".claude/.state.json":            "{}\n",
	".github/.cache/state.json":      "{}\n",
	"sub/.git/config":                "[core]\n",
	"sub/keep.go":                    "package sub\n",
}

func walkPaths(t *testing.T, cfg config.Config) []string {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, hiddenTree)
	var out []string
	for _, d := range walkTree(t, root, cfg) {
		out = append(out, d.Path)
	}
	sort.Strings(out)
	return out
}

var hiddenSource = []string{
	".devcontainer/setup.sh",
	".editorconfig",
	".env.example",
	".eslintrc.js",
	".github/workflows/ci.yml",
	".gitlab-ci.yml",
	".mcp.json",
	"main.go",
	"src/.eslintrc.json",
	"src/app.go",
	"src/data.go",
	"sub/keep.go",
}

// The defect: a `.syms.json` a tool left in the project directory was indexed
// on the next run. Hidden configuration is source and stays; hidden state is
// not and goes; and the object store is refused at any depth.
func TestHiddenScratchIsNotIndexedAndHiddenConfigurationIs(t *testing.T) {
	t.Parallel()
	got := walkPaths(t, config.Defaults())
	if !reflect.DeepEqual(got, hiddenSource) {
		t.Errorf("walk found\n  %v\nwant\n  %v", got, hiddenSource)
	}
}

// index.exclude removes more, hidden or not, as it always has.
func TestIndexExcludeRemovesHiddenSource(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Exclude = append(cfg.Exclude, ".github/**", ".*.yml", "**/.eslintrc.*")
	got := walkPaths(t, cfg)
	want := []string{
		".devcontainer/setup.sh", ".editorconfig", ".env.example", ".mcp.json",
		"main.go", "src/app.go", "src/data.go", "sub/keep.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("walk found\n  %v\nwant\n  %v", got, want)
	}
}

// index.include brings a hidden name back only when a pattern component with a
// dot of its own matches it. `**` does not, which is why the default include
// does not undo the rule; and `.claude/**` reaches what is visible inside
// .claude, not the hidden state further down.
//
// Naming a hidden directory opens it. What is visible inside is then matched
// against the whole include list like any other file, so with `**` in the
// list `**/.cache/*.go` brings in every visible file under a .cache, not only
// the Go ones.
func TestIndexIncludeBringsBackOnlyTheHiddenNamesItNames(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Include = []string{"**", ".syms.json", ".claude/**", "**/.cache/*.go"}
	got := walkPaths(t, cfg)
	want := append([]string{
		".claude/worktrees/copy/main.go",
		".github/.cache/state.json",
		".syms.json",
		"src/.cache/gen.go",
	}, hiddenSource...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("walk found\n  %v\nwant\n  %v", got, want)
	}
}

// `.*` asks for every hidden name, which is a walk with no hidden-name filtering. Two
// names stay out whatever is asked: the object store and this tool's own
// index.
func TestNoIncludeCanReachTheObjectStore(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Include = []string{"**", ".*", ".git/**", "**/.git/**"}
	for _, p := range walkPaths(t, cfg) {
		if p == "sub/.git/config" {
			t.Error("an include pattern brought a .git directory into the index")
		}
	}
	got := walkPaths(t, cfg)
	for _, want := range []string{".syms.json", ".idea/workspace.xml", ".venv/lib/site.py", ".claude/.state.json"} {
		found := false
		for _, p := range got {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("include \".*\" did not bring back %s; got %v", want, got)
		}
	}
}

// The rule by name, so a new entry in one of the lists is a one-line test.
func TestHiddenScratchByName(t *testing.T) {
	t.Parallel()
	type entry struct {
		name    string
		dir     bool
		scratch bool
	}
	for _, e := range []entry{
		// Directories are scratch unless listed.
		{".github", true, false}, {".gitlab", true, false}, {".circleci", true, false},
		{".devcontainer", true, false}, {".husky", true, false}, {".cargo", true, false},
		{".venv", true, true}, {".idea", true, true}, {".vscode", true, true},
		{".cache", true, true}, {".next", true, true}, {".terraform", true, true},
		{".claude", true, true}, {".tox", true, true}, {".gradle", true, true},

		// Files are source unless recognisably scratch.
		{".env", false, false}, {".env.production", false, false}, {".npmrc", false, false},
		{".gitignore", false, false}, {".gitattributes", false, false}, {".dockerignore", false, false},
		{".eslintrc", false, false}, {".eslintrc.js", false, false}, {".eslintrc.json", false, false},
		{".prettierrc.json", false, false}, {".babelrc.json", false, false}, {".c8rc.json", false, false},
		{".travis.yml", false, false}, {".golangci.yml", false, false}, {".pre-commit-config.yaml", false, false},
		{".rustfmt.toml", false, false}, {".flake8", false, false}, {".clang-format", false, false},
		{".tool-versions", false, false}, {".terraform.lock.hcl", false, false},
		{".dependency-cruiser.js", false, false}, {".mcp.json", false, false}, {".cspell.json", false, false},

		{".syms.json", false, true}, {".report.json", false, true}, {".package-lock.json", false, true},
		{".DS_Store", false, true}, {".coverage", false, true}, {".eslintcache", false, true},
		{".main.go.swp", false, true}, {".#main.go", false, true}, {"._main.go", false, true},
		{".notes.md~", false, true}, {".server.log", false, true}, {".app.pid", false, true},
		{".state.db", false, true}, {".npmrc.bak", false, true}, {".trace.jsonl", false, true},
	} {
		if got := hiddenScratch(e.name, e.dir); got != e.scratch {
			t.Errorf("hiddenScratch(%q, dir=%v) = %v, want %v", e.name, e.dir, got, e.scratch)
		}
	}
}

// The watcher asks about paths it has not walked to. Every hidden component
// has to pass, or a watch lands on a virtual environment because the question
// was about a file three levels inside it.
func TestIgnorePredicateAppliesTheHiddenRuleToEveryComponent(t *testing.T) {
	t.Parallel()
	w, err := NewWalker(t.TempDir(), config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	ignore := w.IgnorePredicate()
	for _, tc := range []struct {
		path    string
		dir     bool
		ignored bool
	}{
		{".venv", true, true},
		{".venv/lib/python3/site.py", false, true},
		{".claude/worktrees/copy/internal", true, true},
		{"src/.cache", true, true},
		{".syms.json", false, true},
		{"sub/.git", true, true},
		{"sub/.git/objects/ab", true, true},

		{".github", true, false},
		{".github/workflows", true, false},
		{".github/workflows/ci.yml", false, false},
		{".eslintrc.js", false, false},
		{"src/.eslintrc.json", false, false},
		{"src", true, false},
		{"src/app.go", false, false},
	} {
		if got := ignore(tc.path, tc.dir); got != tc.ignored {
			t.Errorf("ignore(%q, dir=%v) = %v, want %v", tc.path, tc.dir, got, tc.ignored)
		}
	}
}
