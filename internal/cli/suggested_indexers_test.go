package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A language with many files and no indexer installed is said, not silent: one
// line naming the language, what it would add and the command, in `resolve`
// and in `status`, and under suggested_indexers in JSON. It installs nothing,
// and accuracy fast and --quiet keep it out of the text.
func TestMissingIndexerIsNamedAfterResolveAndInStatus(t *testing.T) {
	root := isolate(t)
	files := map[string]string{"Cargo.toml": "[package]\nname = \"geo\"\nversion = \"0.1.0\"\n"}
	for i := range 11 {
		files[fmt.Sprintf("src/m%02d.rs", i)] = fmt.Sprintf("pub fn f%02d() -> u32 { %d }\n", i, i)
	}
	// A few files of a second language, under the threshold: no line for it.
	for i := range 3 {
		files[fmt.Sprintf("web/p%d.py", i)] = fmt.Sprintf("def g%d():\n    return %d\n", i, i)
	}
	writeFiles(t, root, files)
	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// Neither indexer can be on PATH, whatever this machine has installed.
	t.Setenv("PATH", t.TempDir())

	text, err := runLoud(t, root, "resolve")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, text)
	}
	flat := collapse(text)
	for _, want := range []string{"Suggested:", "rust: 11 files with definitions", "no rust-analyzer installed",
		"compiler-proven callers and references", "rustup component add rust-analyzer"} {
		if !strings.Contains(flat, want) {
			t.Errorf("resolve output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(flat, "python:") && strings.Contains(flat, "no scip-python installed") {
		t.Errorf("three Python files are under the threshold and must not be suggested:\n%s", text)
	}

	status, err := runLoud(t, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, status)
	}
	if !strings.Contains(collapse(status), "no rust-analyzer installed") {
		t.Errorf("status does not name the missing indexer:\n%s", status)
	}

	raw, err := run(t, root, "--json", "resolve")
	if err != nil {
		t.Fatalf("resolve --json: %v\n%s", err, raw)
	}
	var rep struct {
		Suggested []struct {
			Language string `json:"language"`
			Indexer  string `json:"indexer"`
			Files    int    `json:"files"`
			Command  string `json:"command"`
			Pinned   bool   `json:"pinned"`
		} `json:"suggested_indexers"`
	}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("resolve --json: %v\n%s", err, raw)
	}
	if len(rep.Suggested) != 1 || rep.Suggested[0].Language != "rust" || rep.Suggested[0].Files != 11 ||
		rep.Suggested[0].Pinned || rep.Suggested[0].Command != "rustup component add rust-analyzer" {
		t.Errorf("suggested_indexers = %+v, want the one Rust entry", rep.Suggested)
	}

	quiet, err := run(t, root, "--quiet", "resolve")
	if err != nil {
		t.Fatalf("resolve --quiet: %v\n%s", err, quiet)
	}
	if strings.Contains(quiet, "Suggested:") {
		t.Errorf("--quiet printed the suggestion:\n%s", quiet)
	}

	fast, err := run(t, root, "--json", "resolve", "--accuracy", "fast")
	if err != nil {
		t.Fatalf("resolve --accuracy fast: %v\n%s", err, fast)
	}
	var fastRep struct {
		Suggested []any `json:"suggested_indexers"`
	}
	if err := json.Unmarshal([]byte(fast), &fastRep); err != nil {
		t.Fatal(err)
	}
	if fastRep.Suggested == nil || len(fastRep.Suggested) != 0 {
		t.Errorf("accuracy fast: suggested_indexers = %v, want an empty list", fastRep.Suggested)
	}
}

// runLoud is run without --quiet, which the shared helper always passes.
func runLoud(t *testing.T, projectRoot string, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--project", projectRoot}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}
