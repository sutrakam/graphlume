package cli

import (
	"sort"
	"strings"
	"testing"
)

// indexedPaths lists the files the committed run holds.
func indexedPaths(t *testing.T, root string) []string {
	t.Helper()
	out, err := run(t, root, "--json", "query", "SELECT path FROM files WHERE index_run_id = :run ORDER BY path")
	if err != nil {
		t.Fatalf("query: %v\n%s", err, out)
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		// One `"path": "..."` per row, whatever envelope the rows are in.
		_, rest, ok := strings.Cut(line, `"path": "`)
		if !ok {
			continue
		}
		paths = append(paths, strings.TrimSuffix(strings.TrimRight(rest, ", "), `"`))
	}
	sort.Strings(paths)
	return paths
}

// The defect as it was met: a hidden scratch file left in the project
// directory was picked up by the next `graphlume index`. Hidden
// configuration is indexed, hidden tool state is not, and `.graphlume.yml`
// moves the line in both directions.
func TestHiddenScratchFilesAreNotIndexed(t *testing.T) {
	root := isolate(t)
	writeFiles(t, root, map[string]string{
		"main.go":                  "package main\n\nfunc main() {}\n",
		".github/workflows/ci.yml": "on: push\njobs: {}\n",
		".eslintrc.js":             "module.exports = { root: true };\n",
		".env.example":             "API_BASE=\n",
		".vscode/settings.json":    "{}\n",
	})
	if out, err := run(t, root, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := run(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	source := []string{".env.example", ".eslintrc.js", ".github/workflows/ci.yml", "main.go"}
	if got := indexedPaths(t, root); strings.Join(got, " ") != strings.Join(source, " ") {
		t.Fatalf("indexed %v, want %v", got, source)
	}

	// A tool leaves its scratch behind, and an editor its state.
	writeFiles(t, root, map[string]string{
		".syms.json":          `{"symbols": ["main"]}` + "\n",
		".idea/workspace.xml": "<project/>\n",
	})
	out, err := run(t, root, "index")
	if err != nil || !strings.Contains(out, "is up to date") {
		t.Errorf("re-index after scratch appeared: %v\n%s", err, out)
	}
	if got := indexedPaths(t, root); strings.Join(got, " ") != strings.Join(source, " ") {
		t.Errorf("after the scratch files appeared the index holds %v, want %v", got, source)
	}

	// The project decides otherwise: its CI is not to be indexed, and that
	// particular hidden file is.
	writeFiles(t, root, map[string]string{
		".graphlume.yml": "index:\n  include: [\"**\", \".syms.json\"]\n  exclude: [\".github/**\"]\n",
	})
	if out, err := run(t, root, "index"); err != nil {
		t.Fatalf("index with .graphlume.yml: %v\n%s", err, out)
	}
	want := []string{".env.example", ".eslintrc.js", ".graphlume.yml", ".syms.json", "main.go"}
	if got := indexedPaths(t, root); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("with index.include and index.exclude set the index holds %v, want %v", got, want)
	}
}
