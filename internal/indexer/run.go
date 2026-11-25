package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/errs"
)

// Execution is the outcome of running one indexer over one project.
type Execution struct {
	Project Project
	Indexer Indexer

	// IndexPath is the .scip file, ready for scip.Ingester.IngestFile. Empty
	// when the run failed.
	IndexPath string

	// Bytes is the size of that file. Zero bytes with exit code 0 is a real
	// failure mode for these tools and the caller should treat it as one.
	Bytes int64

	Argv     []string
	ExitCode int
	Duration time.Duration
	TimedOut bool

	// Diagnostic is a bounded excerpt of the tool's output, kept only for a
	// failed run. It may contain fragments of source code, so it belongs in a
	// message shown to the user who asked for the index — never in a log file
	// and never in errs.Fields.
	Diagnostic string
}

// Executor runs indexers.
type Executor struct {
	Runner Runner
	Audit  AuditSink

	// MountLocalArtifacts gives a containerised build the invoking user's
	// artifact cache. Off unless user config asked for it; see
	// config.UserConfig for what it costs.
	MountLocalArtifacts bool

	// Timeout bounds one indexer. This is config.Config.IndexerTimeout: a
	// Gradle build on a cold cache genuinely takes minutes, and the user is the
	// only one who knows how many they are willing to wait.
	Timeout time.Duration

	// RunnerKind is "native" or "docker".
	RunnerKind string

	// AllowRepoBuild is the user's opt-in to running the repository's own
	// build. For Rust it decides whether build scripts and procedural macros
	// are switched off.
	AllowRepoBuild bool

	// NodeHeapMB configures maximum old space size in MB for Node-based indexers (scip-python, scip-typescript).
	NodeHeapMB int
}

// OutputDir creates the one directory this package writes to.
//
// It is under os.TempDir and owned by this process. Nothing GraphLume produces
// is written into the repository, so indexing a repository never dirties a
// working tree or, worse, overwrites a file in it. (A Java build still writes
// its own target/ — that is Maven's doing and refusing it would mean refusing
// exact Java altogether.)
func OutputDir() (string, error) {
	dir, err := os.MkdirTemp("", "graphlume-scip-")
	if err != nil {
		return "", errs.Wrap(errs.KindInternal, "indexer.OutputDir", err)
	}
	return dir, nil
}

// Run executes one indexer and returns the index it produced.
//
// The repository directory crosses into the subprocess as Command.Dir, never as
// an argument. Every argument below is either a compile-time constant or a path
// this package constructed inside its own temp directory.
func (e *Executor) Run(ctx context.Context, inst Installed, p Project, repoRoot, outDir string) (Execution, error) {
	if !filepath.IsAbs(repoRoot) || !filepath.IsAbs(outDir) {
		return Execution{}, errs.New(errs.KindInvalid, "indexer.Run",
			"repository root and output directory must be absolute")
	}
	rel, err := sanitizeRel(p.Dir)
	if err != nil {
		return Execution{}, errs.Wrap(errs.KindInvalid, "indexer.Run", err)
	}
	projectDir := repoRoot
	if rel != "" {
		projectDir = filepath.Join(repoRoot, filepath.FromSlash(rel))
	}
	indexPath := filepath.Join(outDir, slugFor(p)+".scip")

	cmd, err := e.command(inst, p, rel, projectDir, repoRoot, outDir, indexPath)
	if err != nil {
		return Execution{}, err
	}
	cmd.Timeout = e.timeout()

	res, runErr := e.Runner.Run(ctx, cmd)
	recordErr(e.Audit, ActionIndex, AuditRecord{
		Indexer: inst.Indexer, Language: p.Language, Result: res,
	}, runErr)
	work := filepath.Join(outDir, slugFor(p))
	for attempt := 0; attempt < 3; attempt++ {
		retry, ok := fallbackCommand(inst, e.RunnerKind, cmd, res, runErr, work, repoRoot)
		if !ok {
			break
		}
		cmd = retry
		res, runErr = e.Runner.Run(ctx, cmd)
		recordErr(e.Audit, ActionIndex, AuditRecord{
			Indexer: inst.Indexer, Language: p.Language, Result: res,
		}, runErr)
	}

	ex := Execution{
		Project:  p,
		Indexer:  inst.Indexer,
		Argv:     res.Argv,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
		TimedOut: res.TimedOut,
	}
	if runErr != nil {
		ex.Diagnostic = res.Diagnostic()
		// A timeout unwraps to context.DeadlineExceeded, which errs classifies
		// as KindCancelled — a run we stopped, not a fault to report as a bug.
		return ex, errs.Wrap(errs.KindOf(runErr), "indexer.Run",
			fmt.Errorf("%s on %s: %w", inst.Indexer, p, runErr)).
			WithField("language", string(p.Language)).
			WithField("project_dir", p.Dir)
	}
	if res.ExitCode != 0 {
		ex.Diagnostic = res.Diagnostic()
		if hint := failureHint(inst.Indexer, ex.Diagnostic); hint != "" {
			ex.Diagnostic = hint + "\n" + ex.Diagnostic
		}
		if res.ExitCode == 137 {
			diag := "process terminated by Out-Of-Memory / SIGKILL (exit 137); consider increasing accuracy.indexers.node_heap_mb"
			if ex.Diagnostic != "" {
				ex.Diagnostic = diag + "\n" + ex.Diagnostic
			} else {
				ex.Diagnostic = diag
			}
		}
		// KindUnsupported: the tool ran and could not index this project. In
		// `auto` the caller degrades to heuristics; in `exact` it must fail.
		// Either way it is not a GraphLume bug and not retryable.
		return ex, errs.Wrap(errs.KindUnsupported, "indexer.Run",
			fmt.Errorf("%s exited %d for %s", inst.Indexer, res.ExitCode, p)).
			WithField("language", string(p.Language)).
			WithField("project_dir", p.Dir).
			WithField("exit_code", res.ExitCode)
	}

	if inst.Indexer == SCIPRuby {
		// scip-ruby writes index.scip in its working directory (projectDir).
		defaultPath := filepath.Join(projectDir, "index.scip")
		if _, err := os.Stat(defaultPath); err == nil && defaultPath != indexPath {
			if err := moveOrCopy(defaultPath, indexPath); err != nil {
				return ex, errs.Wrap(errs.KindInternal, "indexer.Run", err)
			}
		}
	}

	info, statErr := os.Stat(indexPath)
	if statErr != nil {
		// Exit 0 and no output. Reporting success here would hand the caller a
		// path that does not exist, and a later "file not found" would look
		// like a GraphLume bug rather than an indexer that produced nothing.
		ex.Diagnostic = res.Diagnostic()
		return ex, errs.Wrap(errs.KindUnsupported, "indexer.Run",
			fmt.Errorf("%s reported success but wrote no index for %s", inst.Indexer, p)).
			WithField("language", string(p.Language)).
			WithField("project_dir", p.Dir)
	}
	if info.Size() == 0 {
		ex.Diagnostic = res.Diagnostic()
		return ex, errs.Wrap(errs.KindUnsupported, "indexer.Run",
			fmt.Errorf("%s reported success but wrote an empty (0-byte) index for %s", inst.Indexer, p)).
			WithField("language", string(p.Language)).
			WithField("project_dir", p.Dir)
	}
	// Exit 0 and an index with no documents. This is not a hypothetical: it is
	// how scip-go reports a module go/packages could not load, and it reached
	// import as "none of the index's 0 definition(s) matched a symbol in this
	// repository" — a message that blames the repository. See scipcontent.go.
	//
	// A file we cannot parse is reported as itself rather than as empty, and is
	// not treated as a reason to reject an index the tool says it wrote.
	hasDocs, scanErr := scipHasDocuments(indexPath)
	if scanErr != nil {
		ex.Diagnostic = res.Diagnostic()
		return ex, errs.Wrap(errs.KindUnsupported, "indexer.Run",
			fmt.Errorf("%s wrote an unreadable index for %s: %w", inst.Indexer, p, scanErr)).
			WithField("language", string(p.Language)).
			WithField("project_dir", p.Dir)
	}
	if !hasDocs {
		ex.Diagnostic = res.Diagnostic()
		return ex, emptyIndexError(inst.Indexer, p)
	}

	ex.IndexPath = indexPath
	ex.Bytes = info.Size()
	return ex, nil
}

func (e *Executor) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return DefaultTimeout
}

// command builds the argv for one indexer.
//
// Read this function as a list of what is NOT here. No --config flag pointing
// at a repository file. No project name taken from .graphlume.yml. No package
// manager install step: `npm install` and `mvn dependency:resolve` both execute
// repository-supplied code, so scip-typescript indexes whatever node_modules is
// already on disk and reports less rather than GraphLume running the repository's
// install hooks. And no interpolation anywhere — every element is appended as
// its own argv slot.
func (e *Executor) command(inst Installed, p Project, rel, projectDir, repoRoot, outDir, indexPath string) (Command, error) {
	switch inst.Indexer {
	case SCIPGo:
		return Command{
			Path: inst.Path,
			Args: []string{"--output", indexPath},
			Dir:  projectDir,
			// scip-go drives go/packages, which reads the repository's go.mod.
			// GOTOOLCHAIN must therefore be chosen by us, or a
			// `toolchain go1.99.9` line in an untrusted repository would make
			// the Go command download and execute a compiler of the
			// repository's choosing. It is also the reason Go indexing produced
			// nothing at all until now: see gotoolchain.go for the failure and
			// for why raising the pin to the project's `go` directive — and to
			// nothing else — keeps the guarantee.
			Env: goEnv("GOTOOLCHAIN=" + goIndexToolchain(projectDir)),
		}, nil

	case SCIPTypeScript:
		if e.RunnerKind == RunnerDocker {
			return e.dockerNodeCommand(inst, p, rel, repoRoot, outDir, indexPath)
		}
		// Convert any Svelte components into shadow TypeScript files so their script blocks are indexable.
		_, _ = PrepareSvelteShadow(repoRoot, projectDir, outDir)

		args := []string{"index", "--output", indexPath}
		if !p.hasMarker("tsconfig.json") {
			if dir, ok := workspaceStandIn(projectDir, repoRoot, filepath.Join(outDir, slugFor(p)+workspaceDirSuffix)); ok {
				// A workspace root that was never installed: one program
				// over every member, with each member's name mapped to its
				// source, so an import of a sibling package is followed.
				args = []string{"index", "--cwd", projectDir, "--output", indexPath, dir}
			} else {
				// Decided by a file NAME, never by its contents.
				args = append(args, "--infer-tsconfig")
			}
		}
		env := os.Environ()
		if e.NodeHeapMB > 0 {
			env = append(env, fmt.Sprintf("NODE_OPTIONS=--max-old-space-size=%d", e.NodeHeapMB))
		}
		return Command{Path: inst.Path, Args: args, Dir: projectDir, Env: env}, nil

	case SCIPPython:
		if e.RunnerKind == RunnerDocker {
			return e.dockerNodeCommand(inst, p, rel, repoRoot, outDir, indexPath)
		}
		args := []string{
			"index", "--output", indexPath,
			// Fixed literals. scip-python wants a project name and version,
			// and the tempting sources for them — package.json's "name",
			// pyproject.toml's [project], .graphlume.yml's project.name — are
			// all attacker-controlled strings that would land in an argv. The
			// name only labels the index, so a constant costs nothing.
			"--project-name", "graphlume-repository",
			"--project-version", "0.0.0",
		}
		env := os.Environ()
		hasVenvPip := false
		if venv := findPythonVenv(projectDir, repoRoot); venv != "" {
			env = setEnv(env, "VIRTUAL_ENV", venv)
			binDir := filepath.Join(venv, "bin")
			if fi, err := os.Stat(binDir); err != nil || !fi.IsDir() {
				binDir = filepath.Join(venv, "Scripts")
			}
			env = prependEnv(env, "PATH", binDir, filepath.ListSeparator)
			if exists(filepath.Join(binDir, "pip")) || exists(filepath.Join(binDir, "pip3")) || exists(filepath.Join(binDir, "pip.exe")) {
				hasVenvPip = true
			}
		}
		if pyPath := findPythonPath(projectDir, repoRoot); pyPath != "" {
			env = prependEnv(env, "PYTHONPATH", pyPath, filepath.ListSeparator)
		}
		if !hasVenvPip && !hasPip(e.Runner) {
			envFile := filepath.Join(outDir, slugFor(p)+".environment.json")
			if err := writeEmptyEnvironment(envFile); err != nil {
				return Command{}, err
			}
			args = append(args, "--environment", envFile)
		}
		if e.NodeHeapMB > 0 {
			env = append(env, fmt.Sprintf("NODE_OPTIONS=--max-old-space-size=%d", e.NodeHeapMB))
		}
		return Command{
			Path: inst.Path,
			Args: args,
			Dir:  projectDir,
			Env:  env,
		}, nil

	case SCIPClang:
		// Run from the project directory, which scip-clang's documentation
		// asks for ("the project root, not the build root"). Both arguments
		// are fixed names or a path built here; the compilation database is
		// the file whose NAME made this a runnable project.
		return Command{
			Path: inst.Path,
			Args: []string{"--compdb-path=compile_commands.json", "--index-output-path=" + indexPath},
			Dir:  projectDir,
			Env:  os.Environ(),
		}, nil

	case SCIPRustAnalyzer:
		// `rust-analyzer scip <path> --output <file>`. The path is the literal
		// "." with Dir set, so no repository-derived string is an argument.
		// In a Cargo workspace Dir is the workspace root, and one run reads
		// every member crate.
		args := []string{"scip", ".", "--output", indexPath}
		// Never let rustup fetch a toolchain a rust-toolchain file names: that
		// is a download the repository chose.
		env := setEnv(os.Environ(), "RUSTUP_AUTO_INSTALL", "0")
		if !e.AllowRepoBuild {
			// Safe by default: build scripts (build.rs) and procedural macros
			// are repository code that rust-analyzer would otherwise run while
			// loading the workspace, and cargo would fetch crates over the
			// network. A fixed configuration switches the first two off and
			// the environment keeps cargo offline.
			cfg := filepath.Join(outDir, slugFor(p)+".rust-analyzer.json")
			if err := writeRustSafeConfig(cfg); err != nil {
				return Command{}, errs.Wrap(errs.KindInternal, "indexer.Run", err)
			}
			args = append(args, "--config-path", cfg)
			env = setEnv(env, "CARGO_NET_OFFLINE", "true")
		}
		return Command{Path: inst.Path, Args: args, Dir: projectDir, Env: env}, nil

	case SCIPDotnet:
		// `scip-dotnet index --output <file>`.
		return Command{
			Path: inst.Path,
			Args: []string{"index", "--output", indexPath},
			Dir:  projectDir,
			Env:  os.Environ(),
		}, nil

	case SCIPRuby:
		// scip-ruby indexes Ruby files starting from the project directory and outputs index.scip.
		return Command{
			Path: inst.Path,
			Args: []string{"--gem-metadata", slugFor(p) + "@0.0.1", "."},
			Dir:  projectDir,
			Env:  os.Environ(),
		}, nil

	case SCIPJava:
		if e.RunnerKind == RunnerDocker {
			return e.dockerJavaCommand(inst, p, rel, repoRoot, outDir, indexPath)
		}
		// Native scip-java drives Maven or Gradle over the repository's own
		// build files. See Options.AllowRepoBuild for why reaching this point
		// requires an explicit decision, and java.go for why the argv is not
		// simply "index --output".
		return Command{
			Path: inst.Path,
			Args: javaArgs(p, indexPath),
			Dir:  projectDir,
			Env:  os.Environ(),
		}, nil
	}
	return Command{}, errs.New(errs.KindInternal, "indexer.Run",
		"no command defined for indexer "+string(inst.Indexer))
}

// dockerJavaCommand builds the container invocation.
//
// The container is the security feature: the repository's build system executes
// inside it, so a malicious Maven plugin gets a container filesystem rather than
// the developer's home directory. That property is only real if the flags below
// hold, which is why each is spelled out rather than inherited from a helper:
//
//   - The repository is mounted read-write, because Maven and Gradle write
//     target/ and .gradle/ and fail without it. This is the one concession, and
//     it is bounded to the repository the user asked to index.
//   - The output directory is a separate mount, so the index cannot be written
//     into the tree.
//   - --rm leaves nothing behind, and the image is always referenced by digest.
//   - No --privileged, no docker socket, no --network host.
//   - The container runs as the invoking uid so the .scip file is readable
//     afterwards and is not left root-owned in the user's temp directory.
func (e *Executor) dockerJavaCommand(inst Installed, p Project, rel, repoRoot, outDir, indexPath string) (Command, error) {
	docker, err := e.Runner.LookPath(string(ToolDocker))
	if err != nil {
		return Command{}, errs.Wrap(errs.KindUnsupported, "indexer.Run",
			fmt.Errorf("runner is docker but docker is unavailable: %w", err))
	}
	if inst.Image == "" {
		return Command{}, errs.New(errs.KindInvalid, "indexer.Run",
			"docker runner requires a pinned image digest")
	}

	const workMount, outMount = "/work", "/out"
	workdir := workMount
	if rel != "" {
		// rel has been through sanitizeRel: no "..", no leading dash, no
		// absolute path. It is a single argv element either way.
		workdir = workMount + "/" + rel
	}

	args := []string{
		"run", "--rm",
		"-v", repoRoot + ":" + workMount,
		"-v", outDir + ":" + outMount,
		"-w", workdir,
	}
	if uid := currentUser(); uid != "" {
		args = append(args, "-u", uid)
		// A numeric uid has no passwd entry inside the image, so HOME resolves
		// to "/" and every tool that wants a cache directory fails on it.
		// scip-java dies immediately: coursier tries to create /.cache and
		// gets AccessDeniedException before the build starts, which surfaced
		// only as "indexer failed".
		//
		// The output mount is already writable by this uid — it has to be, the
		// index is written there — so it is the one directory guaranteed to
		// work. Caches land beside the index and are discarded with it, which
		// costs a cold dependency download per run and is the right trade for
		// a container whose whole point is leaving no trace on the host.
		args = append(args, "-e", "HOME="+outMount)

		// HOME is not what a JVM reads. Its `user.home` property comes from
		// the passwd entry for the running uid, and a numeric uid the image
		// has never heard of resolves it to "?". scip-java's wrapper fetches
		// its own JDK through `coursier java`, coursier is a JVM program, and
		// its cache root is `${user.home}/.cache/coursier` — so on the first
		// repository Java was measured on, 611 MB of Temurin landed in a
		// directory literally named `?` at the root of the indexed
		// repository: untracked, not gitignored, and outside every mount
		// above. The output mount is the home the shell was just given, so
		// the JVM gets the same one.
		//
		// JDK_JAVA_OPTIONS rather than JAVA_TOOL_OPTIONS: every JDK 9+
		// launcher reads it, and it does not displace the JAVA_TOOL_OPTIONS
		// the image sets for container memory limits — which this runner
		// would otherwise have to know and repeat to keep.
		args = append(args, "-e", "JDK_JAVA_OPTIONS=-Duser.home="+outMount)

		// The local artifact cache, when the user has asked for it.
		//
		// Without this a private dependency cannot resolve inside the
		// container and Java indexing is impossible for exactly the
		// repositories most worth indexing — the ones with internal libraries.
		// Mounted at the HOME the container was just given, which is where
		// Maven looks.
		if e.MountLocalArtifacts {
			if home, err := os.UserHomeDir(); err == nil {
				if m2 := filepath.Join(home, ".m2"); dirExists(m2) {
					args = append(args, "-v", m2+":"+outMount+"/.m2")
				}
			}
		}
	}
	args = append(args, inst.Image)
	args = append(args, inst.DockerEntryOr("scip-java")...)
	args = append(args, javaArgs(p, outMount+"/"+filepath.Base(indexPath))...)

	return Command{Path: docker, Args: args, Env: os.Environ()}, nil
}

// DockerEntryOr returns the in-image argv prefix, defaulting to the binary name.
func (i Installed) DockerEntryOr(def string) []string {
	if pin, ok := PinFor(languageOf(i.Indexer)); ok && len(pin.DockerEntry) > 0 {
		return pin.DockerEntry
	}
	return []string{def}
}

func languageOf(ix Indexer) Language {
	for _, p := range append(append([]Pin(nil), pins...), userPins...) {
		if p.Indexer == ix {
			return p.Language
		}
	}
	return ""
}

// currentUser renders "uid:gid" for `docker run -u`, or "" where that is
// meaningless. On Windows Getuid returns -1 and containers do not take numeric
// ids, so the flag is simply omitted there.
func currentUser() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 || gid < 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", uid, gid)
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// dockerNodeCommand builds the container invocation for Node-based indexers (scip-python, scip-typescript).
func (e *Executor) dockerNodeCommand(inst Installed, p Project, rel, repoRoot, outDir, indexPath string) (Command, error) {
	docker, err := e.Runner.LookPath(string(ToolDocker))
	if err != nil {
		return Command{}, errs.Wrap(errs.KindUnsupported, "indexer.Run",
			fmt.Errorf("runner is docker but docker is unavailable: %w", err))
	}
	if inst.Image == "" {
		return Command{}, errs.New(errs.KindInvalid, "indexer.Run",
			"docker runner requires a pinned image digest")
	}

	const workMount, outMount = "/work", "/out"
	workdir := workMount
	if rel != "" {
		workdir = workMount + "/" + rel
	}

	args := []string{
		"run", "--rm",
		"-v", repoRoot + ":" + workMount,
		"-v", outDir + ":" + outMount,
		"-w", workdir,
	}
	if uid := currentUser(); uid != "" {
		args = append(args, "-u", uid)
		args = append(args, "-e", "HOME="+outMount)
	}
	if e.NodeHeapMB > 0 {
		args = append(args, "-e", fmt.Sprintf("NODE_OPTIONS=--max-old-space-size=%d", e.NodeHeapMB))
	}

	args = append(args, inst.Image)
	args = append(args, inst.DockerEntryOr("npx")...)

	outIndex := outMount + "/" + filepath.Base(indexPath)
	switch p.Language {
	case LangTypeScript:
		args = append(args, "index", "--output", outIndex)
		if !p.hasMarker("tsconfig.json") {
			args = append(args, "--infer-tsconfig")
		}
	case LangPython:
		args = append(args, "index", "--output", outIndex,
			"--project-name", "graphlume-repository",
			"--project-version", "0.0.0",
		)
		args = append(args, "--environment", "[]")
	}

	return Command{Path: docker, Args: args, Env: os.Environ()}, nil
}

// findPythonVenv locates a virtual environment for projectDir, checking
// projectDir/.venv, projectDir/venv, OS-specific variants, sibling services in monorepos, and repoRoot.
func findPythonVenv(projectDir, repoRoot string) string {
	for _, c := range venvCandidates(projectDir) {
		if isVenv(c) {
			return c
		}
	}

	// Sibling services in monorepos (e.g. backend/python/*/.venv)
	parent := filepath.Dir(projectDir)
	if parent != "" && parent != "." && parent != repoRoot {
		if entries, err := os.ReadDir(parent); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					for _, c := range venvCandidates(filepath.Join(parent, e.Name())) {
						if isVenv(c) {
							return c
						}
					}
				}
			}
		}
	}

	// Repository root fallback
	if repoRoot != "" && repoRoot != projectDir {
		for _, c := range venvCandidates(repoRoot) {
			if isVenv(c) {
				return c
			}
		}
	}
	return ""
}

func venvCandidates(dir string) []string {
	if dir == "" {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	// Platform hints first so host-native venvs take priority over cross-platform checkouts
	switch runtime.GOOS {
	case "darwin":
		add(filepath.Join(dir, ".venv-mac"))
		add(filepath.Join(dir, ".venv-darwin"))
		add(filepath.Join(dir, "venv-mac"))
	case "linux":
		add(filepath.Join(dir, ".venv-linux"))
		add(filepath.Join(dir, "venv-linux"))
	case "windows":
		add(filepath.Join(dir, ".venv-win"))
		add(filepath.Join(dir, "venv-win"))
	}

	for _, name := range []string{".venv", "venv", ".env", "env", ".virtualenv"} {
		add(filepath.Join(dir, name))
	}

	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				n := e.Name()
				if strings.HasPrefix(n, ".venv") || strings.HasPrefix(n, "venv") {
					add(filepath.Join(dir, n))
				}
			}
		}
	}
	return out
}

func isVenv(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	bin := filepath.Join(dir, "bin")
	scripts := filepath.Join(dir, "Scripts")
	hasBin := exists(bin) || exists(scripts)
	if !hasBin {
		return false
	}
	// If a python executable exists in bin/Scripts, verify it resolves to a real target (not a dangling symlink).
	for _, sub := range []string{"bin", "Scripts"} {
		for _, name := range []string{"python", "python3", "python.exe"} {
			p := filepath.Join(dir, sub, name)
			if _, err := os.Lstat(p); err == nil {
				if _, err := os.Stat(p); err != nil {
					return false // Dangling symlink
				}
				return true
			}
		}
	}
	return true
}

// findPythonPath locates shared service or library directories to add to PYTHONPATH.
func findPythonPath(projectDir, repoRoot string) string {
	var paths []string
	addIfDir := func(p string) {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			paths = append(paths, p)
		}
	}

	// Check sibling directories in monorepo
	parent := filepath.Dir(projectDir)
	if parent != "" && parent != "." && parent != repoRoot {
		if entries, err := os.ReadDir(parent); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				name := strings.ToLower(e.Name())
				if strings.HasPrefix(name, "shared") || strings.HasPrefix(name, "common") || strings.HasPrefix(name, "lib") {
					full := filepath.Join(parent, e.Name())
					if full != projectDir {
						addIfDir(full)
					}
				}
			}
		}
	}

	// Check repoRoot
	if repoRoot != "" && repoRoot != projectDir {
		for _, name := range []string{"shared", "common", "lib", "shared-service"} {
			full := filepath.Join(repoRoot, name)
			if full != projectDir {
				addIfDir(full)
			}
		}
	}

	if len(paths) == 0 {
		return ""
	}
	return strings.Join(paths, string(filepath.ListSeparator))
}

func prependEnv(env []string, key, val string, sep rune) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			old := strings.TrimPrefix(entry, prefix)
			if old == "" {
				env[i] = prefix + val
			} else {
				env[i] = prefix + val + string(sep) + old
			}
			return env
		}
	}
	return append(env, prefix+val)
}

func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

func hasPip(r Runner) bool {
	if r == nil {
		return false
	}
	if _, err := r.LookPath("pip"); err == nil {
		return true
	}
	if _, err := r.LookPath("pip3"); err == nil {
		return true
	}
	return false
}

// fallbackCommand returns the next command to try when an indexer failed in
// a way that has a known, safe workaround, and false otherwise. Run applies at
// most two, so a project can need both.
//
//   - Node ran out of heap (exit 134 or its "heap out of memory" message). A
//     large service needs more than Node's default; the retry raises the
//     limit to largeNodeHeapMB.
//   - scip-python's package discovery failed. It runs a helper script against
//     pyproject.toml, and a pyproject.toml without a [project] table (common
//     with other build tools) makes it exit non-zero. `--environment` names a
//     file listing the installed packages; an empty list skips discovery. The
//     project's own code is still indexed in full; only links into
//     third-party packages are lost, and this index cannot bind those anyway.
//   - scip-typescript found a tsconfig.json that only extends or references a
//     file a framework generates on its first build (.nuxt/tsconfig.json,
//     .svelte-kit/tsconfig.json), absent from a fresh checkout. Generating it
//     means running the project's own build tooling, which indexing never
//     does. Instead the retry points scip-typescript at a tsconfig GraphLume
//     writes beside its own output: every .ts, .tsx and .js file of the
//     project, with the path aliases these frameworks define. Document paths
//     stay relative to the project, because --cwd is the project. The same
//     retry covers a tsconfig that extends a workspace package which is not
//     installed (TS6053) and an inferred-tsconfig walk that crashed on a
//     dangling symbolic link.
//
// work is the per-project base path under the output directory that these
// workarounds write their files beside. repoRoot bounds the search for the
// workspace the stand-in tsconfig maps (see tsworkspace.go); empty means the
// project directory alone.
func fallbackCommand(inst Installed, kind string, cmd Command, res Result, runErr error, work, repoRoot string) (Command, bool) {
	if runErr != nil || res.ExitCode == 0 || res.TimedOut || kind == RunnerDocker {
		return Command{}, false
	}
	if inst.Indexer != SCIPPython && inst.Indexer != SCIPTypeScript {
		return Command{}, false
	}
	if nodeOutOfMemory(res) && nodeHeapMB(cmd.Env) < largeNodeHeapMB {
		retry := cmd
		retry.Env = setEnv(append([]string(nil), cmd.Env...), "NODE_OPTIONS", fmt.Sprintf("--max-old-space-size=%d", largeNodeHeapMB))
		return retry, true
	}
	if inst.Indexer == SCIPTypeScript && isWorkspaceStandIn(cmd.Args) && !nodeOutOfMemory(res) {
		// The workspace stand-in failed for a reason of its own. The inferred
		// tsconfig is what ran before it existed, so it is the one retry.
		retry := cmd
		retry.Args = []string{"index", "--output", argAfter(cmd.Args, "--output"), "--infer-tsconfig"}
		return retry, true
	}
	if inst.Indexer == SCIPTypeScript && !hasArg(cmd.Args, "--cwd") && tsconfigUnusable(res) {
		dir := work + "-tsconfig"
		if err := writeStandInTsconfig(dir, cmd.Dir, repoRoot); err != nil {
			return Command{}, false
		}
		retry := cmd
		retry.Args = []string{"index", "--cwd", cmd.Dir, "--output", argAfter(cmd.Args, "--output"), dir}
		return retry, true
	}
	envFile := work + ".environment.json"
	if inst.Indexer == SCIPPython && !hasArg(cmd.Args, "--environment") {
		if err := writeEmptyEnvironment(envFile); err != nil {
			return Command{}, false
		}
		retry := cmd
		retry.Args = append(append([]string(nil), cmd.Args...), "--environment", envFile)
		return retry, true
	}
	return Command{}, false
}

// tsconfigUnusable reports whether scip-typescript failed for want of a usable
// tsconfig rather than because of the code it was asked to read, which a
// tsconfig GraphLume writes can work around:
//
//   - a tsconfig it extends or references is missing: a generated one
//     ("missing tsconfig.json"), or one a workspace package provides through
//     node_modules, which a checkout that was never installed does not have
//     (TS6053 "File '@scope/tsconfig/base.json' not found"), or one a
//     framework writes on its first run (TS5083 "Cannot read file
//     '.nitro/types/tsconfig.json'");
//   - its inferred-tsconfig walk crashed on an entry it could not stat, such
//     as a dangling symbolic link (inferTsconfig.js, ENOENT).
func tsconfigUnusable(res Result) bool {
	out := string(res.Stderr) + "\n" + string(res.Stdout)
	switch {
	case strings.Contains(out, "(missing tsconfig.json)"):
		return true
	case strings.Contains(out, "TS6053") && strings.Contains(out, "' not found"):
		return true
	case strings.Contains(out, "TS5083") && strings.Contains(out, "Cannot read file") && strings.Contains(out, "tsconfig"):
		return true
	case strings.Contains(out, "inferTsconfig.js") && strings.Contains(out, "ENOENT"):
		return true
	}
	return false
}

// largeNodeHeapMB is the heap a retry gives a Node indexer that ran out of
// memory: enough for a service of about 50,000 definitions, measured at
// 6.9 GB peak resident on one.
const largeNodeHeapMB = 8192

// nodeOutOfMemory reports whether a Node indexer died for want of heap. Node
// aborts (SIGABRT) when it reaches its heap limit: a shell reports 134, and
// Go reports a process killed by a signal as -1 with no run error. Its own
// message is at the end of stderr, which the bounded capture may have cut.
func nodeOutOfMemory(res Result) bool {
	if res.ExitCode == 134 || res.ExitCode == -1 {
		return true
	}
	d := res.Diagnostic()
	return strings.Contains(d, "heap out of memory") || strings.Contains(d, "Reached heap limit")
}

// nodeHeapMB reads --max-old-space-size from NODE_OPTIONS, 0 when unset.
func nodeHeapMB(env []string) int {
	heap := 0
	for _, kv := range env {
		v, ok := strings.CutPrefix(kv, "NODE_OPTIONS=")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(v) {
			if n, ok := strings.CutPrefix(f, "--max-old-space-size="); ok {
				_, _ = fmt.Sscanf(n, "%d", &heap)
			}
		}
	}
	return heap
}

// writeEmptyEnvironment writes the package list scip-python reads with
// --environment: an empty list, so it does not run package discovery.
func writeEmptyEnvironment(path string) error {
	return os.WriteFile(path, []byte("[]\n"), 0o600)
}

// argAfter returns the value that follows a flag, or "" when it is absent.
func argAfter(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

// workspaceDirSuffix names the directory, beside an indexer's output, that
// holds the tsconfig written for an uninstalled workspace root.
const workspaceDirSuffix = "-workspace"

// isWorkspaceStandIn reports whether args are the command built by
// workspaceStandIn: the tsconfig directory it passes ends in the suffix.
func isWorkspaceStandIn(args []string) bool {
	return len(args) > 0 && hasArg(args, "--cwd") && strings.HasSuffix(args[len(args)-1], workspaceDirSuffix)
}

// workspaceStandIn writes the stand-in tsconfig for a project that is the root
// of a JavaScript or TypeScript workspace whose packages were never installed,
// and returns the directory it is in.
//
// Run from the root, one program covers every member and each member's name
// maps to its source, so the compiler sees a call in apps/web and the function
// in packages/ui it reaches as parts of one program and proves the edge in one
// index. Measured on create-t3-turbo, that root run alone proved 89.1% of the
// relationships; the per-package runs with the same map prove 77.6%, and the
// two together 93.0%, so the root run is added to the per-package runs
// rather than replacing them.
//
// It applies only where the inferred tsconfig would have been used (the root
// has no tsconfig.json of its own, so none of its settings is lost) and only
// where nothing is installed (no node_modules directory), because an installed
// workspace already resolves its members through node_modules.
func workspaceStandIn(projectDir, repoRoot, dir string) (string, bool) {
	root, patterns, ok := findWorkspaceRoot(repoRoot, projectDir)
	if !ok || filepath.Clean(root) != filepath.Clean(projectDir) || isRealDir(filepath.Join(projectDir, "node_modules")) {
		return "", false
	}
	if len(workspaceMembers(repoRoot, root, patterns)) == 0 {
		return "", false
	}
	if err := writeStandInTsconfig(dir, projectDir, repoRoot); err != nil {
		return "", false
	}
	return dir, true
}

// writeStandInTsconfig writes the tsconfig the TypeScript fallback indexes
// with: the project's own sources, JavaScript allowed, no emit, the "~" and "@"
// aliases Nuxt and Vue projects resolve imports through, the project's own
// baseUrl and paths when its tsconfig chain has them, and one path entry for
// each package of the workspace the project belongs to.
//
// The workspace entries are what an install would have linked into
// node_modules: `import { x } from "@acme/ui"` resolves to the source of
// packages/ui, so the compiler can name the definition the import reaches
// rather than leaving the reference to a guess. The project's own aliases win
// over the workspace's where both name the same key. See tsworkspace.go.
func writeStandInTsconfig(dir, projectDir, repoRoot string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	top := repoRoot
	if top == "" {
		top = projectDir
	}
	baseURL := projectDir
	paths := map[string][]string{}
	for _, alias := range []string{"~", "@", "~~", "@@"} {
		paths[alias+"/*"] = []string{filepath.Join(projectDir, "*")}
	}
	var members []workspacePackage
	if root, patterns, ok := findWorkspaceRoot(top, projectDir); ok {
		members = workspaceMembers(top, root, patterns)
		for k, v := range workspacePaths(members) {
			paths[k] = v
		}
	}
	own := readTsconfigPaths(top, projectDir, members)
	if own.BaseURL != "" {
		baseURL = own.BaseURL
	}
	for k, v := range own.Paths {
		paths[k] = v
	}
	cfg := map[string]any{
		"compilerOptions": map[string]any{
			"allowJs": true, "checkJs": false, "noEmit": true, "skipLibCheck": true,
			"target": "ESNext", "module": "ESNext", "moduleResolution": "Bundler", "jsx": "preserve",
			"baseUrl": baseURL,
			"paths":   paths,
		},
		"include": []string{
			filepath.Join(projectDir, "**", "*.ts"), filepath.Join(projectDir, "**", "*.tsx"), filepath.Join(projectDir, "**", "*.js"),
		},
		"exclude": []string{
			filepath.Join(projectDir, "node_modules"), filepath.Join(projectDir, ".nuxt"), filepath.Join(projectDir, ".output"),
			filepath.Join(projectDir, ".svelte-kit"), filepath.Join(projectDir, "dist"),
		},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tsconfig.json"), b, 0o600)
}

func hasArg(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// failureHint names the fix for an indexer failure whose cause is known from
// its output, so the report says what to do rather than only what broke.
func failureHint(ix Indexer, diag string) string {
	switch {
	case ix == SCIPTypeScript && strings.Contains(diag, ".nuxt/tsconfig.json"):
		return "the project's tsconfig extends .nuxt/tsconfig.json, which Nuxt generates: run `npx nuxi prepare` in the project, then resolve again"
	case ix == SCIPTypeScript && strings.Contains(diag, "TS6053") && strings.Contains(diag, "' not found"):
		return "the project's tsconfig extends a file that comes from a workspace package which is not installed: run the package manager's install (npm install, pnpm install or yarn install) at the workspace root, then resolve again"
	case ix == SCIPTypeScript && strings.Contains(diag, "inferTsconfig.js") && strings.Contains(diag, "ENOENT"):
		return "scip-typescript stopped on a path it could not read, usually a symbolic link whose target is missing: remove or fix the link, or add a tsconfig.json to the project, then resolve again"
	case ix == SCIPRustAnalyzer && (strings.Contains(diag, "config-path") || strings.Contains(diag, "unexpected flag")):
		return "this rust-analyzer does not accept --config-path, which GraphLume uses to switch off build scripts and procedural macros: update it (rustup component add rust-analyzer, or rustup update)"
	case ix == SCIPRustAnalyzer && strings.Contains(diag, "is not installed") && strings.Contains(diag, "toolchain"):
		return "the repository names a Rust toolchain (rust-toolchain.toml) that is not installed, and GraphLume does not let rustup fetch it: install it with rustup, then resolve again"
	case ix == SCIPRustAnalyzer && (strings.Contains(diag, "offline") || strings.Contains(diag, "failed to download") || strings.Contains(diag, "Failed to read Cargo metadata") || strings.Contains(diag, "failed to load workspace")):
		return "cargo could not load the workspace without fetching crates, which GraphLume does not do unless --allow-repo-build is given: run `cargo fetch` yourself in the repository, or re-run with --allow-repo-build"
	case ix == SCIPDotnet && strings.Contains(diag, "You must install or update .NET"):
		return "scip-dotnet needs the .NET 10 runtime: install the .NET 10 SDK, then resolve again"
	case ix == SCIPRuby && (strings.Contains(diag, "Failed to find .gemspec") || strings.Contains(diag, "gemspec")):
		return "scip-ruby needs project metadata: add a .gemspec or sorbet/config to the project"
	case ix == SCIPRuby && (strings.Contains(diag, "bundle") || strings.Contains(diag, "Bundler") || strings.Contains(diag, "Gemfile")):
		return "scip-ruby could not resolve dependencies: run bundle install if gems are missing, then resolve again"
	case ix == SCIPTypeScript && strings.Contains(diag, ".svelte-kit/tsconfig.json"):
		return "the project's tsconfig extends .svelte-kit/tsconfig.json, which SvelteKit generates: run `npx svelte-kit sync` in the project, then resolve again"
	case ix == SCIPJava && (strings.Contains(diag, "release version") || strings.Contains(diag, "UnsupportedClassVersionError") || strings.Contains(diag, "source release")):
		return "the project requires a newer JDK than this environment provides: configure a compatible JDK or use a supported version"
	case ix == SCIPJava && (strings.Contains(diag, "gradlew: not found") || strings.Contains(diag, "GradleWrapperMain")):
		return "Gradle wrapper is missing or incomplete: run `gradle wrapper` to generate gradlew and wrapper jar"
	case ix == SCIPJava && (strings.Contains(diag, "Could not resolve dependencies") || strings.Contains(diag, "offline mode") || strings.Contains(diag, "Could not transfer artifact") || strings.Contains(diag, "Non-resolvable parent POM")):
		return "build dependencies could not be resolved offline: run with network access or mount host ~/.m2 repository"
	}
	return ""
}

func moveOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	sf, err := os.Open(filepath.Clean(src)) //nolint:gosec // temporary index file move
	if err != nil {
		return err
	}
	defer func() { _ = sf.Close() }()
	df, err := os.Create(filepath.Clean(dst)) //nolint:gosec // temporary index file move
	if err != nil {
		return err
	}
	defer func() { _ = df.Close() }()
	if _, err := io.Copy(df, sf); err != nil {
		return err
	}
	_ = sf.Close()
	_ = os.Remove(src)
	return nil
}
