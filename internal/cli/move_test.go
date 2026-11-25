package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability/move"
	"github.com/sutrakam/graphlume/internal/edit"
	"github.com/sutrakam/graphlume/pkg/model"
)

const (
	moveLoad = `// Package config loads settings.
package config

import (
	"os"
	"strings"
)

// Load reads the configuration file.
func Load(path string) ([]byte, error) { return os.ReadFile(path) }

// Normalize lower-cases a key and trims it.
func Normalize(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
`
	moveLoadAfter = `// Package config loads settings.
package config

import (
	"os"
)

// Load reads the configuration file.
func Load(path string) ([]byte, error) { return os.ReadFile(path) }
`
	moveKeysAfter = `package config

import "strings"

// Normalize lower-cases a key and trims it.
func Normalize(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
`
	moveKinds = "package config\n\n// Kind says where a value came from.\ntype Kind int\n"
)

// seedMoveRepo is a small Go module, indexed: config/load.go holds two
// functions, one of which is the only user of "strings".
func seedMoveRepo(t *testing.T, extra map[string]string) string {
	t.Helper()
	root := isolate(t)
	files := map[string]string{
		"go.mod":          "module example.com/shop\n\ngo 1.21\n",
		"config/load.go":  moveLoad,
		"config/kinds.go": moveKinds,
	}
	for name, body := range extra {
		files[name] = body
	}
	writeFiles(t, root, files)
	indexProject(t, root)
	return root
}

func moveJSON(t *testing.T, root string, args ...string) (moveReport, int) {
	t.Helper()
	out, err := run(t, root, append([]string{"--json", "move"}, args...)...)
	var rep moveReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil {
		t.Fatalf("move --json did not emit a report: %v (err=%v)\n%s", jerr, err, out)
	}
	return rep, exitCodeOf(err)
}

func needGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command on PATH")
	}
}

// withoutGo hides the toolchain from the command. Most of what a move
// promises is decided before any compiler is involved, and a test of those
// promises should not pay for (or depend on) a `go list` per run.
func withoutGo(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// noMoveLeftovers: nothing the move made for itself outlives it — no lock,
// no temporary file beside a source file, no scratch directory in the index
// directory.
func noMoveLeftovers(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		// An entry that vanished mid-walk (the index's journal files do) is
		// not a leftover.
		if err == nil && (strings.HasPrefix(d.Name(), ".graphlume-edit") || strings.HasPrefix(d.Name(), "move-")) {
			t.Errorf("left behind: %s", p)
		}
		return nil
	})
}

func refusalText(rep moveReport) string {
	var b strings.Builder
	for _, r := range rep.Refusals {
		b.WriteString(r.String() + "\n")
	}
	return b.String()
}

// TestMoveDryRunWritesNothing: every change is listed with its confidence,
// and no file is touched or created.
func TestMoveDryRunWritesNothing(t *testing.T) {
	withoutGo(t)
	root := seedMoveRepo(t, nil)
	before := snapshot(t, root, "config/load.go", "config/kinds.go")

	rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go", "--no-build")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a dry run that would apply; refusals:\n%s", code, refusalText(rep))
	}
	if rep.Applied || len(rep.Files) != 0 {
		t.Errorf("applied=%v files=%v, want a dry run", rep.Applied, rep.Files)
	}
	if rep.Declaration == nil || rep.Declaration.Kind != "func" || rep.Declaration.Line != 12 || rep.Declaration.EndLine != 15 {
		t.Errorf("declaration = %+v, want func at config/load.go:12-15", rep.Declaration)
	}
	var actions []string
	for _, s := range rep.Sites {
		actions = append(actions, string(s.Action))
		// No compiler ran, so nothing may call itself exact.
		if s.Confidence != model.ConfidenceHigh {
			t.Errorf("site %+v: confidence %q without a build, want high", s, s.Confidence)
		}
	}
	if got := strings.Join(actions, " "); got != "create-file add-import insert remove remove-import" {
		t.Errorf("sites = %q", got)
	}
	if rep.Build.Status != buildNotRun || !strings.Contains(rep.Build.Detail, "--no-build") {
		t.Errorf("build = %+v, want not-run because of --no-build", rep.Build)
	}
	if rep.Limits == "" {
		t.Error("the report does not state the move's limits")
	}
	assertUnchanged(t, before, snapshot(t, root, "config/load.go", "config/kinds.go"))
	if _, err := os.Stat(filepath.Join(root, "config", "keys.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a dry run created the target (stat err = %v)", err)
	}
	noMoveLeftovers(t, root)

	out, err := run(t, root, "move", "Normalize", "config/load.go", "config/keys.go", "--no-build")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"DRY RUN: nothing was written", "--apply would exit 3", "SITES", "BUILD: NOT RUN", "LIMITS:",
		"config/load.go:12-15", "remove-import"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

// TestMoveApplyWithoutABuildExitsThree: the files are written, and the exit
// code says no compiler checked them.
func TestMoveApplyWithoutABuildExitsThree(t *testing.T) {
	withoutGo(t)
	root := seedMoveRepo(t, nil)

	rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go", "--no-build", "--apply")
	if code != moveExitUnverified {
		t.Fatalf("exit = %d, want %d; refusals:\n%s", code, moveExitUnverified, refusalText(rep))
	}
	if !rep.Applied || rep.Created != "config/keys.go" || strings.Join(rep.Files, " ") != "config/keys.go config/load.go" {
		t.Errorf("applied=%v created=%q files=%v", rep.Applied, rep.Created, rep.Files)
	}
	got := snapshot(t, root, "config/load.go", "config/keys.go")
	if got["config/load.go"] != moveLoadAfter {
		t.Errorf("config/load.go:\n%s", got["config/load.go"])
	}
	if got["config/keys.go"] != moveKeysAfter {
		t.Errorf("config/keys.go:\n%s", got["config/keys.go"])
	}
	noMoveLeftovers(t, root)

	// The index now describes files that no longer exist as indexed: a
	// second move must be refused until it is refreshed.
	rep, code = moveJSON(t, root, "Load", "config/load.go", "config/keys.go", "--no-build")
	if code != moveExitRefused || !strings.Contains(refusalText(rep), "changed since it was indexed") ||
		!strings.Contains(refusalText(rep), "not in the index") {
		t.Errorf("exit = %d, refusals:\n%s\nwant both files refused as not what was indexed", code, refusalText(rep))
	}
}

// TestMoveApplyBuildsBeforeAndAfterWriting: with go on PATH the planned files
// are compiled before anything is written and the package is built again
// afterwards; only then is a site exact and the exit code 0.
func TestMoveApplyBuildsBeforeAndAfterWriting(t *testing.T) {
	needGo(t)
	root := seedMoveRepo(t, nil)

	rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go")
	if code != 0 || rep.Build.Status != buildPass || rep.Build.Command != "go build ." {
		t.Fatalf("dry run: exit=%d build=%+v refusals:\n%s", code, rep.Build, refusalText(rep))
	}
	for _, s := range rep.Sites {
		if s.Confidence != model.ConfidenceExact {
			t.Errorf("site %+v: confidence %q after a passing build, want exact", s, s.Confidence)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "config", "keys.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the build check of a dry run created the target (stat err = %v)", err)
	}
	noMoveLeftovers(t, root)

	rep, code = moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go", "--apply")
	if code != 0 || !rep.Applied || rep.Build.Status != buildPass || !strings.Contains(rep.Build.Detail, "after writing") {
		t.Fatalf("apply: exit=%d applied=%v build=%+v refusals:\n%s", code, rep.Applied, rep.Build, refusalText(rep))
	}
	got := snapshot(t, root, "config/load.go", "config/keys.go")
	if got["config/load.go"] != moveLoadAfter || got["config/keys.go"] != moveKeysAfter {
		t.Errorf("config/load.go:\n%s\nconfig/keys.go:\n%s", got["config/load.go"], got["config/keys.go"])
	}
	noMoveLeftovers(t, root)

	// And back, once the index is refreshed: the target exists this time.
	for _, step := range []string{"index", "resolve"} {
		if out, err := run(t, root, step); err != nil {
			t.Fatalf("%s: %v\n%s", step, err, out)
		}
	}
	rep, code = moveJSON(t, root, "Normalize", "config/keys.go", "config/load.go", "--apply", "--no-build")
	if code != moveExitUnverified || !rep.Applied || rep.Created != "" {
		t.Fatalf("move back: exit=%d applied=%v created=%q refusals:\n%s", code, rep.Applied, rep.Created, refusalText(rep))
	}
	got = snapshot(t, root, "config/load.go", "config/keys.go")
	if got["config/load.go"] != moveLoad {
		t.Errorf("config/load.go was not restored byte for byte:\n%s", got["config/load.go"])
	}
	if got["config/keys.go"] != "package config\n" {
		t.Errorf("config/keys.go = %q, want only the package clause", got["config/keys.go"])
	}
}

// TestMoveRefusesFilesThatAreNotWhatWasIndexed: a file edited since indexing,
// and a file the index has never seen.
func TestMoveRefusesFilesThatAreNotWhatWasIndexed(t *testing.T) {
	withoutGo(t)
	root := seedMoveRepo(t, nil)
	edited := moveLoad + "\n// edited after indexing\n"
	writeFiles(t, root, map[string]string{"config/load.go": edited, "config/fresh.go": "package config\n"})

	for _, apply := range []bool{false, true} {
		args := []string{"Normalize", "config/load.go", "config/fresh.go", "--no-build"}
		if apply {
			args = append(args, "--apply")
		}
		rep, code := moveJSON(t, root, args...)
		if code != moveExitRefused {
			t.Fatalf("apply=%v: exit = %d, want %d", apply, code, moveExitRefused)
		}
		text := refusalText(rep)
		for _, want := range []string{"config/load.go  changed since it was indexed", "config/fresh.go  file is not in the index", "graphlume index"} {
			if !strings.Contains(text, want) {
				t.Errorf("apply=%v: refusals do not mention %q:\n%s", apply, want, text)
			}
		}
		if len(rep.Sites) != 0 {
			t.Errorf("apply=%v: a refused move lists sites: %+v", apply, rep.Sites)
		}
	}
	got := snapshot(t, root, "config/load.go", "config/fresh.go")
	if got["config/load.go"] != edited || got["config/fresh.go"] != "package config\n" {
		t.Error("a refused move wrote a file")
	}
}

func TestMoveRefusesFilesItCannotReadOrWriteSafely(t *testing.T) {
	withoutGo(t)
	root := seedMoveRepo(t, map[string]string{"config/ro.go": "package config\n\nfunc RO() {}\n"})
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package config\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("a source that does not exist", func(t *testing.T) {
		rep, code := moveJSON(t, root, "X", "config/missing.go", "config/keys.go")
		if code != moveExitRefused || !strings.Contains(refusalText(rep), "config/missing.go  no such file") {
			t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
		}
	})
	t.Run("a path outside the project root", func(t *testing.T) {
		rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/../../escape.go")
		if code != moveExitRefused || !strings.Contains(refusalText(rep), "another directory") {
			t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
		}
		rep, code = moveJSON(t, root, "Normalize", "../load.go", "../keys.go")
		if code != moveExitRefused || !strings.Contains(refusalText(rep), "'..'") {
			t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
		}
		rep, code = moveJSON(t, root, "Normalize", outside, filepath.Join(filepath.Dir(outside), "b.go"))
		if code != moveExitRefused || !strings.Contains(refusalText(rep), "relative to the project root") {
			t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
		}
		if b, _ := os.ReadFile(outside); string(b) != "package config\n" {
			t.Error("a file outside the root was touched")
		}
	})
	t.Run("a read-only source", func(t *testing.T) {
		p := filepath.Join(root, "config", "ro.go")
		if err := os.Chmod(p, 0o444); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
		// Refused by the dry run too: it reports what --apply would do.
		rep, code := moveJSON(t, root, "RO", "config/ro.go", "config/keys.go", "--no-build")
		if code != moveExitRefused || !strings.Contains(refusalText(rep), "read-only") {
			t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
		}
	})
	t.Run("a symlinked target", func(t *testing.T) {
		link := filepath.Join(root, "config", "link.go")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("cannot create a symlink here: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/link.go", "--no-build", "--apply")
		if code != moveExitRefused || !strings.Contains(refusalText(rep), "symlink") {
			t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
		}
		if b, _ := os.ReadFile(outside); string(b) != "package config\n" {
			t.Error("the move wrote through a symlink to a file outside the root")
		}
	})
	t.Run("a symlinked file in the package", func(t *testing.T) {
		link := filepath.Join(root, "config", "zlinked.go")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("cannot create a symlink here: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go", "--no-build")
		if code != moveExitRefused || !strings.Contains(refusalText(rep), "config/zlinked.go  a file of the package") {
			t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
		}
	})
}

// TestMoveRefusesWhatItCannotMakeSafe: a sample of the planner's refusals
// through the command, each exiting 2 with nothing written.
func TestMoveRefusesWhatItCannotMakeSafe(t *testing.T) {
	withoutGo(t)
	root := seedMoveRepo(t, map[string]string{
		"config/start.go": "package config\n\nfunc init() {}\n\nconst (\n\tA = iota\n\tB\n)\n\nfunc (k Kind) String() string { return \"\" }\n",
		"tools/gen.py":    "def gen():\n    pass\n",
		"other/other.go":  "package other\n",
	})
	before := snapshot(t, root, "config/load.go", "config/start.go", "config/kinds.go", "tools/gen.py", "other/other.go")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"gen", "tools/gen.py", "tools/lib.py"}, "moves Go declarations only"},
		{[]string{"Normalize", "config/load.go", "other/other.go"}, "in another package"},
		{[]string{"Kind.String", "config/start.go", "other/other.go"}, "cannot leave the package of its receiver type"},
		{[]string{"init", "config/start.go", "config/kinds.go"}, "init function cannot be moved"},
		{[]string{"B", "config/start.go", "config/kinds.go"}, "iota numbers the members"},
		{[]string{"String", "config/start.go", "config/kinds.go"}, "move Kind.String"},
		{[]string{"Kind", "config/kinds.go", "config/load.go", "extra"}, "accepts 3 arg(s)"},
		{[]string{"Normalize", "config/load.go", "config/kinds_windows.go"}, "only under GOOS=windows"},
		{[]string{"Nope", "config/load.go", "config/kinds.go"}, "no top-level declaration named Nope"},
		{[]string{"Normalize", "config/load.go", "config/load.go"}, "same file"},
	} {
		out, err := run(t, root, append(append([]string{"move"}, tc.args...), "--apply", "--no-build")...)
		if err == nil || !strings.Contains(out+err.Error(), tc.want) {
			t.Errorf("move %v: err=%v, want a refusal mentioning %q\n%s", tc.args, err, tc.want, out)
			continue
		}
		if strings.Contains(tc.want, "arg(s)") {
			continue // a usage error, not a refusal
		}
		if code := exitCodeOf(err); code != moveExitRefused || !strings.Contains(out, "REFUSED: nothing was written") {
			t.Errorf("move %v: exit = %d, want %d and a REFUSED report\n%s", tc.args, code, moveExitRefused, out)
		}
	}
	assertUnchanged(t, before, snapshot(t, root, "config/load.go", "config/start.go", "config/kinds.go", "tools/gen.py", "other/other.go"))
	noMoveLeftovers(t, root)
}

// TestMoveApplyRefusesWhileAnotherEditHoldsTheLock: one edit per project
// root, whichever command makes it.
func TestMoveApplyRefusesWhileAnotherEditHoldsTheLock(t *testing.T) {
	withoutGo(t)
	root := seedMoveRepo(t, nil)
	before := snapshot(t, root, "config/load.go")
	if err := os.WriteFile(filepath.Join(root, ".graphlume-edit.lock"), []byte("pid=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, root, "move", "Normalize", "config/load.go", "config/keys.go", "--apply", "--no-build")
	if code := exitCodeOf(err); code != moveExitRefused {
		t.Fatalf("exit = %d, want %d\n%s", code, moveExitRefused, out)
	}
	for _, want := range []string{"REFUSED: nothing was written", "another edit is in progress"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	assertUnchanged(t, before, snapshot(t, root, "config/load.go"))
	if _, err := os.Stat(filepath.Join(root, "config", "keys.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the target was created under someone else's lock (stat err = %v)", err)
	}
}

// TestMoveWithoutAToolchain: no go on PATH. The move is planned from syntax
// and says no build checked it; an import whose package name only the go
// command could supply stops the move rather than being guessed.
func TestMoveWithoutAToolchain(t *testing.T) {
	root := seedMoveRepo(t, map[string]string{
		"config/yaml.go": "package config\n\nimport \"gopkg.in/yaml.v3\"\n\nfunc Encode(v any) ([]byte, error) { return yaml.Marshal(v) }\n\nfunc Plain() {}\n",
	})
	withoutGo(t)

	rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go")
	if code != 0 || rep.Build.Status != buildNotRun || !strings.Contains(rep.Build.Detail, "no go command on PATH") {
		t.Errorf("exit=%d build=%+v refusals:\n%s", code, rep.Build, refusalText(rep))
	}
	rep, code = moveJSON(t, root, "Encode", "config/yaml.go", "config/keys.go")
	if code != moveExitRefused || !strings.Contains(refusalText(rep), `package name of "gopkg.in/yaml.v3" is not known`) {
		t.Errorf("exit=%d refusals:\n%s", code, refusalText(rep))
	}
	rep, code = moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go", "--apply")
	if code != moveExitUnverified || !rep.Applied {
		t.Errorf("apply: exit=%d applied=%v, want %d and applied", code, rep.Applied, moveExitUnverified)
	}
	out, _ := run(t, root, "move", "Plain", "config/yaml.go", "config/kinds.go")
	if !strings.Contains(out, "BUILD: NOT RUN") || !strings.Contains(out, "Sites are `high`, not `exact`") {
		t.Errorf("the text report does not say the sites are unchecked:\n%s", out)
	}
}

// TestMoveRefusesWhenThePackageDoesNotBuild: a build that already fails
// cannot vouch for the move, so the move is refused, and the user is told
// how to accept a parse-only check instead.
func TestMoveRefusesWhenThePackageDoesNotBuild(t *testing.T) {
	needGo(t)
	root := seedMoveRepo(t, map[string]string{"config/broken.go": "package config\n\nvar broken int = \"not a number\"\n"})
	before := snapshot(t, root, "config/load.go")

	rep, code := moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go", "--apply")
	if code != moveExitRefused {
		t.Fatalf("exit = %d, want %d", code, moveExitRefused)
	}
	text := refusalText(rep)
	for _, want := range []string{"does not build as it stands", "--no-build", "broken.go"} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, text)
		}
	}
	assertUnchanged(t, before, snapshot(t, root, "config/load.go"))
	noMoveLeftovers(t, root)

	if _, code := moveJSON(t, root, "Normalize", "config/load.go", "config/keys.go", "--no-build"); code != 0 {
		t.Errorf("--no-build: exit = %d, want 0", code)
	}
}

// TestMoveSaysWhenThisPlatformDoesNotBuildTheFiles: both files are built
// only for an operating system this is not, so the package builds whether or
// not the move is right. That must not be reported as a passing build —
// whether the constraint is in the file's name or in its //go:build line.
func TestMoveSaysWhenThisPlatformDoesNotBuildTheFiles(t *testing.T) {
	needGo(t)
	other := "plan9"
	if runtime.GOOS == "plan9" {
		other = "linux"
	}
	const body = "package config\n\nfunc Sys() {}\n\nfunc Other() {}\n"
	for _, tc := range []struct{ name, from, to, src string }{
		{"by file name", "config/sys_" + other + ".go", "config/more_" + other + ".go", body},
		{"by build line", "config/sys.go", "config/more.go", "//go:build " + other + "\n\n" + body},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := seedMoveRepo(t, map[string]string{tc.from: tc.src})
			rep, code := moveJSON(t, root, "Sys", tc.from, tc.to, "--apply")
			if code != moveExitUnverified || !rep.Applied {
				t.Fatalf("exit=%d applied=%v, want %d and applied; refusals:\n%s", code, rep.Applied, moveExitUnverified, refusalText(rep))
			}
			want := "this platform (" + runtime.GOOS + "/" + runtime.GOARCH + ") does not build that file"
			if rep.Build.Status != buildNotRun || !strings.Contains(rep.Build.Detail, want) {
				t.Errorf("build = %+v, want not-run because this platform does not build the file", rep.Build)
			}
			for _, s := range rep.Sites {
				if s.Confidence != model.ConfidenceHigh {
					t.Errorf("site %+v claims %q though nothing compiled it", s, s.Confidence)
				}
			}
			if got := snapshot(t, root, tc.to)[tc.to]; !strings.Contains(got, "func Sys() {}") {
				t.Errorf("%s:\n%s", tc.to, got)
			}
		})
	}
}

// TestMoveBetweenTestFilesCompilesTheTests: `go build` never reads a
// _test.go file, so the check is `go test -c`, which compiles them and runs
// nothing.
func TestMoveBetweenTestFilesCompilesTheTests(t *testing.T) {
	needGo(t)
	if testing.Short() {
		t.Skip("links a test binary")
	}
	root := seedMoveRepo(t, map[string]string{
		"config/load_test.go": "package config\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestLoad(t *testing.T) {}\n\n" +
			"func helper(t *testing.T) string {\n\tt.Helper()\n\treturn strings.Repeat(\"x\", 2)\n}\n",
	})
	rep, code := moveJSON(t, root, "helper", "config/load_test.go", "config/helpers_test.go", "--apply")
	if code != 0 || rep.Build.Status != buildPass || rep.Build.Command != "go test -c ." {
		t.Fatalf("exit=%d build=%+v refusals:\n%s", code, rep.Build, refusalText(rep))
	}
	got := snapshot(t, root, "config/load_test.go", "config/helpers_test.go")
	if strings.Contains(got["config/load_test.go"], "strings") || !strings.Contains(got["config/helpers_test.go"], "\"strings\"") {
		t.Errorf("load_test.go:\n%s\nhelpers_test.go:\n%s", got["config/load_test.go"], got["config/helpers_test.go"])
	}
	noMoveLeftovers(t, root)
}

// TestMoveBuildCheckCatchesWhatThePlanMissed: the planner is meant to refuse
// anything that would not compile, so no real move reaches this path. The
// check is driven directly with a plan that drops a function still in use,
// and with go itself as the judge.
func TestMoveBuildCheckCatchesWhatThePlanMissed(t *testing.T) {
	needGo(t)
	root := seedMoveRepo(t, map[string]string{
		"config/use.go": "package config\n\nfunc Use() string { return Normalize(\"x\") }\n",
	})
	tool := newGoTool(root, "config")
	if tool == nil {
		t.Fatal("go is on PATH but newGoTool found none")
	}
	scratch, cleanup, err := moveScratch(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	ctx := context.Background()
	bad := move.Plan{SourceAfter: []byte(moveLoadAfter), TargetAfter: []byte("package config\n")}
	verdict, err := tool.verify(ctx, bad, "config/load.go", "config/keys.go", scratch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(verdict.refusal, "would not with the move applied") || !strings.Contains(verdict.refusal, "Normalize") {
		t.Errorf("verdict = %+v, want a refusal quoting the compiler", verdict)
	}

	good := move.Plan{SourceAfter: []byte(moveLoadAfter), TargetAfter: []byte(moveKeysAfter)}
	verdict, err = tool.verify(ctx, good, "config/load.go", "config/keys.go", scratch)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.refusal != "" || verdict.build.Status != buildPass || verdict.recheck == nil {
		t.Errorf("verdict = %+v, want a pass with a recheck", verdict)
	}
	// Nothing was written: the recheck builds the files on disk, which still
	// hold the declaration, and passes.
	if err := verdict.recheck(ctx); err != nil {
		t.Errorf("recheck of the untouched tree: %v", err)
	}
	if got := snapshot(t, root, "config/load.go"); got["config/load.go"] != moveLoad {
		t.Error("the build check wrote a source file")
	}
}

// TestGoToolAsksForPackageNames: the names come from the go command, never
// from the shape of the path.
func TestGoToolAsksForPackageNames(t *testing.T) {
	needGo(t)
	root := seedMoveRepo(t, map[string]string{"internal/odd-name/x.go": "package sensible\n"})
	tool := newGoTool(root, "config")
	names := tool.packageNames(context.Background(),
		[]string{"fmt", "math/rand/v2", "example.com/shop/internal/odd-name", "example.com/not/here", "-toolexec=evil"})
	if names["fmt"] != "fmt" || names["math/rand/v2"] != "rand" {
		t.Errorf("standard library names = %v", names)
	}
	if names["example.com/shop/internal/odd-name"] != "sensible" {
		t.Errorf("a package named differently from its directory: %v", names)
	}
	if _, ok := names["example.com/not/here"]; ok {
		t.Errorf("a package that does not exist was given a name: %v", names)
	}
}

// TestMoveCommitOutcome: every failure that left the files as they were is a
// refusal (2); only a rollback that could not restore is an error.
func TestMoveCommitOutcome(t *testing.T) {
	unlock := fmt.Errorf("%w .graphlume-edit.lock (delete it by hand): permission denied", edit.ErrLockRelease)
	for _, tc := range []struct {
		name    string
		err     error
		applied bool
		code    int
		want    string
	}{
		{"written", nil, true, 0, ""},
		{"written, lock left behind", errors.Join(nil, unlock), true, 0, "release lock"},
		{"locked", fmt.Errorf("%w (held)", edit.ErrLocked), false, moveExitRefused, "another edit is in progress"},
		{"stale", fmt.Errorf("edit: write a.go: %w: a.go changed", edit.ErrStale), false, moveExitRefused,
			"changed while the move was being written"},
		{"the build failed after writing", fmt.Errorf("%w: %w", edit.ErrCheckFailed, errors.New("go build . failed")), false,
			moveExitRefused, "both were restored"},
		{"the build failed and a file could not be restored",
			errors.Join(fmt.Errorf("%w: boom", edit.ErrCheckFailed), edit.ErrRollbackIncomplete), false, 1, "rollback was incomplete"},
		{"io, rolled back", errors.New("edit: write a.go: disk full"), false, moveExitRefused,
			"every file already written was restored"},
		{"rollback incomplete", errors.Join(errors.New("edit: write b.go: disk full"), edit.ErrRollbackIncomplete), false, 1,
			"rollback was incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applied, code, msg := moveCommitOutcome(tc.err)
			if applied != tc.applied || code != tc.code || !strings.Contains(msg, tc.want) {
				t.Errorf("moveCommitOutcome = %v %d %q, want %v %d and %q", applied, code, msg, tc.applied, tc.code, tc.want)
			}
		})
	}
}
