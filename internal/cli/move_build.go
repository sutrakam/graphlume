package cli

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/capability/move"
	"github.com/sutrakam/graphlume/internal/indexer"
)

// goTool is the user's Go toolchain, used for two things a move cannot get
// from syntax: the name of the package an import path declares, and whether
// the package compiles.
//
// It runs `go` in the package's own directory with the user's environment,
// so it builds what the user's own `go build` builds, with one change:
// GOPROXY=off, so that nothing is ever downloaded. A module that is not
// already on this machine makes the build fail, which is reported, rather
// than reaching the network.
type goTool struct {
	runner indexer.Runner
	path   string // the go command, absolute
	dir    string // the package directory, absolute, symlinks resolved
	env    []string
}

// goTimeout bounds one go invocation.
const goTimeout = 5 * time.Minute

// newGoTool finds go on PATH. It returns nil when there is none: the move is
// then planned from syntax alone and no build checks it.
func newGoTool(root, relDir string) *goTool {
	runner := indexer.ExecRunner{}
	goPath, err := runner.LookPath("go")
	if err != nil {
		return nil
	}
	// The directory is resolved and handed to go as both its working
	// directory and $PWD, so the paths go derives for the package's files are
	// character for character the keys written into the overlay. An overlay
	// keyed by another spelling of the same directory is silently not applied.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	dir := filepath.Join(root, filepath.FromSlash(relDir))
	return &goTool{runner: runner, path: goPath, dir: dir, env: withEnv(os.Environ(), "PWD="+dir, "GOPROXY=off")}
}

// withEnv returns base with each key=value in kv set, replacing any existing
// entry for that key.
func withEnv(base []string, kv ...string) []string {
	set := make(map[string]bool, len(kv))
	for _, e := range kv {
		k, _, _ := strings.Cut(e, "=")
		set[k] = true
	}
	out := make([]string, 0, len(base)+len(kv))
	for _, e := range base {
		if k, _, _ := strings.Cut(e, "="); !set[k] {
			out = append(out, e)
		}
	}
	return append(out, kv...)
}

func (t *goTool) run(ctx stdctx.Context, args ...string) (indexer.Result, error) {
	return t.runner.Run(ctx, indexer.Command{Path: t.path, Args: args, Dir: t.dir, Env: t.env, Timeout: goTimeout})
}

// packageNames asks `go list` for the package name of each import path. A
// path it cannot answer for (a module not downloaded, a directory outside any
// module) is simply absent from the result, and the move treats its name as
// unknown.
func (t *goTool) packageNames(ctx stdctx.Context, paths []string) map[string]string {
	if len(paths) == 0 {
		return nil
	}
	// "--" ends the flags: an import path is repository text and must never
	// be read as an option.
	args := append([]string{"list", "-e", "-f", "{{.ImportPath}}\t{{.Name}}", "--"}, paths...)
	res, err := t.run(ctx, args...)
	if err != nil {
		return nil
	}
	asked := make(map[string]bool, len(paths))
	for _, p := range paths {
		asked[p] = true
	}
	out := make(map[string]string)
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		p, name, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if ok && name != "" && asked[p] {
			out[p] = name
		}
	}
	return out
}

// build compiles the package: `go build` for ordinary files, `go test -c`
// (compile and link the test binary, run nothing) when the files are test
// files. overlay, when not empty, is a -overlay file. scratch receives the
// test binary, which is the only thing either command writes.
func (t *goTool) build(ctx stdctx.Context, tests bool, overlay, scratch string) (ok bool, diagnostic string, err error) {
	args := []string{"build", "-o", os.DevNull}
	if tests {
		args = []string{"test", "-c", "-o", filepath.Join(scratch, "package.test")}
	}
	if overlay != "" {
		args = append(args, "-overlay", overlay)
	}
	res, err := t.run(ctx, append(args, ".")...)
	if err != nil {
		return false, "", err
	}
	return res.ExitCode == 0, res.Diagnostic(), nil
}

// buildCommand is the build as a user would type it.
func buildCommand(tests bool) string {
	if tests {
		return "go test -c ."
	}
	return "go build ."
}

// moveVerdict is the compiler's answer about a planned move.
type moveVerdict struct {
	build moveBuild
	// refusal, when not empty, is why the move must not be made.
	refusal string
	// recheck builds the package from the files on disk; it is run after
	// they are written, and a failure undoes the write.
	recheck func(stdctx.Context) error
}

// verify compiles the package with the planned contents in place of the two
// files, without writing either: go's -overlay substitutes them. It then
// proves that build compiled the moved code, by checking that the same build
// fails when the planned source is made uncompilable.
//
// scratch holds the overlay's files; it is inside the index directory, so
// even the check writes nothing outside the project root.
func (t *goTool) verify(ctx stdctx.Context, plan move.Plan, from, to, scratch string) (moveVerdict, error) {
	tests := plan.TestFiles
	command := buildCommand(tests)
	recheck := func(ctx stdctx.Context) error {
		ok, diag, err := t.build(ctx, tests, "", scratch)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s failed:\n%s", command, diag)
		}
		return nil
	}

	planned, err := writeOverlay(scratch, "planned", map[string][]byte{
		filepath.Join(t.dir, path.Base(from)): plan.SourceAfter,
		filepath.Join(t.dir, path.Base(to)):   plan.TargetAfter,
	})
	if err != nil {
		return moveVerdict{}, err
	}
	ok, diag, err := t.build(ctx, tests, planned, scratch)
	if err != nil {
		return moveVerdict{}, err
	}
	if !ok {
		// Is it the move, or was the package already broken?
		was, wasDiag, err := t.build(ctx, tests, "", scratch)
		if err != nil {
			return moveVerdict{}, err
		}
		if !was {
			return moveVerdict{refusal: fmt.Sprintf("the package does not build as it stands (%s), so no build can "+
				"tell whether the move breaks it. Fix the build first, or pass --no-build to accept a result "+
				"checked by parsing only:\n%s", command, wasDiag)}, nil
		}
		return moveVerdict{refusal: fmt.Sprintf("the package builds now and would not with the move applied (%s "+
			"with the planned files). Nothing was written. This is a case graphlume move did not foresee; "+
			"please report it:\n%s", command, diag)}, nil
	}

	// The control. A build passes just as well when it never compiles the
	// file: one this platform's build constraints exclude, or an overlay go
	// did not read. So the same build is run with the planned source made
	// uncompilable — the planned bytes, build constraint and all, with a
	// syntax error after them — and it must fail. If it passes, nothing
	// compiled the moved code, here or after writing, and the move is graded
	// as what it is: checked by parsing only.
	broken := append(append([]byte(nil), plan.SourceAfter...), "\n\nfunc {\n"...)
	control, err := writeOverlay(scratch, "control", map[string][]byte{
		filepath.Join(t.dir, path.Base(from)): broken,
		filepath.Join(t.dir, path.Base(to)):   plan.TargetAfter,
	})
	if err != nil {
		return moveVerdict{}, err
	}
	stillBuilds, _, err := t.build(ctx, tests, control, scratch)
	if err != nil {
		return moveVerdict{}, err
	}
	if stillBuilds {
		return moveVerdict{build: moveBuild{Status: buildNotRun, Command: command, Detail: fmt.Sprintf(
			"%s still passes when %s is replaced by a file that cannot compile, so this platform (%s/%s) does not "+
				"build that file and no build on this machine checks the moved code: the result was checked by parsing only",
			command, from, runtime.GOOS, runtime.GOARCH)}}, nil
	}
	return moveVerdict{recheck: recheck, build: moveBuild{Status: buildPass, Command: command,
		Detail: "compiled with the planned files in place of the two on disk (go's -overlay); nothing was written for it"}}, nil
}

// writeOverlay writes each content to a file under scratch and returns the
// path of a go -overlay file mapping the real paths to them.
func writeOverlay(scratch, name string, files map[string][]byte) (string, error) {
	replace := make(map[string]string, len(files))
	i := 0
	for real, content := range files {
		tmp := filepath.Join(scratch, fmt.Sprintf("%s-%d.go.txt", name, i))
		i++
		if err := os.WriteFile(tmp, content, 0o600); err != nil {
			return "", fmt.Errorf("write overlay file: %w", err)
		}
		replace[real] = tmp
	}
	raw, err := json.Marshal(map[string]map[string]string{"Replace": replace})
	if err != nil {
		return "", fmt.Errorf("encode overlay: %w", err)
	}
	overlay := filepath.Join(scratch, name+"-overlay.json")
	if err := os.WriteFile(overlay, raw, 0o600); err != nil {
		return "", fmt.Errorf("write overlay: %w", err)
	}
	return overlay, nil
}

// moveScratch makes a private directory under the index directory for the
// build check's files, and returns it with its cleanup.
func moveScratch(root string) (string, func(), error) {
	base := filepath.Join(root, IndexDir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, fmt.Errorf("create %s: %w", base, err)
	}
	dir, err := os.MkdirTemp(base, "move-")
	if err != nil {
		return "", nil, fmt.Errorf("create scratch directory: %w", err)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}
