package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `resolve` never opts in to running code the repository chooses: an installed
// scip-clang is left alone until the user passes --allow-repo-build on the
// command line, whatever the repository contains.
func TestResolveDoesNotRunScipClangUnlessTheUserOptsIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in indexer is a shell script")
	}
	root := isolate(t)
	writeFiles(t, root, map[string]string{
		"compile_commands.json": `[{"directory":"/x","command":"c++ -c a.cpp","file":"a.cpp"}]`,
		"a.cpp":                 "int add(int a, int b) { return a + b; }\n",
	})
	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	bins, log := t.TempDir(), filepath.Join(t.TempDir(), "ran.log")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo scip-clang 0.3.2; exit 0; fi\necho ran >> \"" + log + "\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bins, "scip-clang"), []byte(script), 0o755); err != nil { //nolint:gosec // G306: a test executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bins)

	out, err := runLoud(t, root, "resolve")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if !strings.Contains(collapse(out), "re-run with --allow-repo-build") {
		t.Errorf("resolve does not say the C/C++ indexer needs the opt-in:\n%s", out)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("resolve ran scip-clang without --allow-repo-build")
	}

	if out, err := runLoud(t, root, "resolve", "--allow-repo-build"); err != nil {
		t.Fatalf("resolve --allow-repo-build: %v\n%s", err, out)
	}
	if _, err := os.Stat(log); err != nil {
		t.Error("scip-clang did not run with --allow-repo-build")
	}
}
