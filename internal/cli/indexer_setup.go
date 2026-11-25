package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/indexer"
)

// The guided setup `graphlume init` runs before indexing: for each language no
// compiler can check yet, it says what is missing, what would be downloaded or
// run, where it comes from and roughly how big it is, and asks.
//
// Defaults follow cost. A small pinned, checksum-verified indexer defaults to
// yes. A toolchain (the .NET SDK, Node.js, a JDK, Maven, Gradle) and anything
// that runs the repository's own build default to no. Without a terminal
// nothing is asked and nothing is installed: the plan is printed, --yes
// accepts the default-yes offers only, and --allow-repo-build is what lets a
// repository build run. A toolchain is never installed without a typed yes.
//
// A declined offer is remembered in the user config
// (accuracy.indexers.declined) and not made again until
// `graphlume indexers install --ask-again`. Nothing is ever written into the
// repository's configuration, and nothing here can stop the onboarding.

// setupRunner runs the commands an accepted offer names. The real one streams
// their output to the terminal: a toolchain install takes minutes, and a
// silent one looks hung.
type setupRunner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, dir string, argv []string, out io.Writer) error
}

type execSetupRunner struct{}

func (execSetupRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (execSetupRunner) Run(ctx context.Context, dir string, argv []string, out io.Writer) error {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	c := exec.CommandContext(ctx, bin, argv[1:]...) //nolint:gosec // argv comes from indexer.SetupItems' compile-time tables
	c.Dir = dir
	c.Stdout, c.Stderr = out, out
	c.Stdin = nil
	return c.Run()
}

// Seams for tests: whether a person is at the terminal, and what runs.
var (
	setupInteractive                    = isInteractiveTerminal
	newSetupRunner   func() setupRunner = func() setupRunner { return execSetupRunner{} }
	setupProvision                      = indexer.Provision
	setupGOOS                           = runtime.GOOS
)

// guidedSetup carries one run of the setup.
type guidedSetup struct {
	ctx         context.Context
	g           *Global
	cfg         config.Config
	out         io.Writer
	in          *bufio.Reader
	interactive bool
	yes         bool
	allowBuild  bool
	run         setupRunner
	declined    map[string]bool
	configPath  string
	// present marks a toolchain offer as satisfied in this run, so the
	// indexer that depends on it is offered next.
	present map[string]bool
	// installed counts what this run installed or generated.
	installed int
}

// runGuidedSetup walks the user through the offers for this repository. It
// returns whether anything was installed or generated, so the caller knows
// the plan changed.
func runGuidedSetup(ctx context.Context, g *Global, in io.Reader, out io.Writer, yes, allowBuild bool) bool {
	cfg := g.Config()
	plan, err := indexer.NewPlanWith(ctx, cfg.RepoRoot, cfg, indexer.Options{AllowRepoBuild: allowBuild})
	if err != nil {
		_, _ = fmt.Fprintf(out, "\nCould not plan compiler indexers (%v); indexing without them.\n", err)
		return false
	}
	run := newSetupRunner()
	home, _ := os.UserHomeDir()
	items := indexer.SetupItems(plan, indexer.SetupEnv{GOOS: setupGOOS, LookPath: run.LookPath, Home: home})
	if len(items) == 0 {
		return false
	}
	s := &guidedSetup{
		ctx: ctx, g: g, cfg: cfg, out: out, in: bufio.NewReader(in),
		interactive: setupInteractive(), yes: yes, allowBuild: allowBuild,
		run: run, declined: map[string]bool{}, present: map[string]bool{},
	}
	s.configPath, _ = config.UserConfigPath()
	for _, k := range cfg.IndexerDeclined {
		s.declined[k] = true
	}
	s.walk(items)
	return s.installed > 0
}

func (s *guidedSetup) printf(format string, a ...any) { _, _ = fmt.Fprintf(s.out, format, a...) }

func (s *guidedSetup) walk(items []indexer.SetupItem) {
	s.printf("\nCompiler indexers prove most relationships in a language; without one, its answers are graded, not proven.\n")
	if s.interactive {
		s.printf("For each language below, GraphLume says what is missing and asks before it installs or runs anything.\n")
	} else {
		s.printf("No terminal to ask, so this is the plan only.")
		switch {
		case s.yes && s.allowBuild:
			s.printf(" --yes installs the pinned indexers; --allow-repo-build runs the C/C++ build steps.\n")
		case s.yes:
			s.printf(" --yes installs the pinned indexers, whose default is yes; nothing else.\n")
		case s.allowBuild:
			s.printf(" --allow-repo-build runs the C/C++ build steps; nothing is installed.\n")
		default:
			s.printf(" Nothing is installed: re-run with --yes to install the pinned indexers, whose default is yes.\n")
		}
	}
	for _, it := range items {
		if it.After != "" && !s.present[it.After] {
			continue
		}
		if s.declined[it.Key] {
			s.printf("\n%s: skipped %s; you declined it before (graphlume indexers install --ask-again asks again).\n",
				it.Language, firstWordLower(it.Missing))
			continue
		}
		s.offer(it)
	}
	s.close()
}

func firstWordLower(s string) string {
	if i := strings.Index(s, ","); i > 0 {
		return s[:i]
	}
	return s
}

// describe prints what an offer is about.
func (s *guidedSetup) describe(it indexer.SetupItem) {
	if it.Kind == indexer.SetupFetch {
		s.printf("\n%s: dependencies needed for full type-checking (built-in go/types).\n", it.Language)
	} else {
		s.printf("\n%s: no compiler indexer can run yet.\n", it.Language)
	}
	row := func(k, v string) {
		if v != "" {
			s.printf("    %-9s %s\n", k+":", v)
		}
	}
	row("Missing", it.Missing)
	row("Download", it.Download)
	row("From", it.Source)
	row("Size", it.Size)
	if it.Kind == indexer.SetupIndexer && it.Install != nil {
		row("Command", "installs "+it.Install.Ref+" into "+it.Install.Where())
	} else {
		row("Command", it.Manual)
	}
	for _, n := range it.Notes {
		s.printf("    %s\n", n)
	}
}

// decide returns whether to act on an offer.
func (s *guidedSetup) decide(it indexer.SetupItem) bool {
	if s.yes && (it.Kind == indexer.SetupIndexer || it.Kind == indexer.SetupFetch) {
		return true
	}
	if s.allowBuild && it.Kind == indexer.SetupRepoBuild && len(it.Prereq) == 0 {
		return true
	}
	if s.interactive {
		ok, answered := s.ask(it.Question, it.DefaultYes)
		if !ok && answered {
			s.remember(it.Key)
		}
		return ok
	}
	switch it.Kind {
	case indexer.SetupIndexer, indexer.SetupFetch:
		if s.yes {
			return true
		}
		s.printf("    Not run: no terminal to ask. --yes accepts it.\n")
	case indexer.SetupRepoBuild:
		if s.allowBuild && len(it.Prereq) == 0 {
			return true
		}
		if s.allowBuild {
			s.printf("    Not run: its build helpers must be installed first, and a toolchain is never installed without a yes at a terminal.\n")
		} else {
			s.printf("    Not run: a repository build needs a yes at a terminal, or --allow-repo-build.\n")
		}
	default:
		s.printf("    Not installed: a toolchain is installed only after a yes at a terminal. To do it yourself: %s\n", it.Manual)
	}
	return false
}

// ask prints the question and reads one answer. answered is false when input
// ended before an answer, which is not remembered as a decline.
func (s *guidedSetup) ask(q string, def bool) (yes bool, answered bool) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		s.printf("  %s %s ", q, hint)
		line, err := s.in.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if err != nil && ans == "" {
			s.printf("\n")
			s.interactive = false
			return false, false
		}
		switch ans {
		case "":
			return def, true
		case "y", "yes":
			return true, true
		case "n", "no":
			return false, true
		}
		s.printf("  Please answer y or n.\n")
	}
}

func (s *guidedSetup) remember(key string) {
	s.declined[key] = true
	if s.configPath == "" {
		return
	}
	if err := config.RememberDeclined(s.configPath, key); err != nil {
		s.printf("    (could not remember this answer in %s: %v)\n", s.configPath, err)
		return
	}
	s.printf("    Remembered in %s; graphlume indexers install --ask-again asks again.\n", s.configPath)
}

// offer describes one item, asks or applies the non-interactive rule, and
// carries it out. A failure is reported and the walk goes on.
func (s *guidedSetup) offer(it indexer.SetupItem) {
	s.describe(it)
	if it.Question == "" {
		// Nothing GraphLume can run: the description is the advice.
		return
	}
	if (it.Kind == indexer.SetupToolchain || it.Kind == indexer.SetupUserIndexer) && len(it.Commands) == 0 {
		s.printf("    GraphLume cannot install this here. To install it yourself: %s\n", it.Manual)
		return
	}
	if !s.decide(it) {
		return
	}
	if err := s.carryOut(it); err != nil {
		s.printf("    Failed: %v\n", err)
		if it.Manual != "" && it.Kind != indexer.SetupIndexer {
			s.printf("    To do it yourself: %s\n", it.Manual)
		}
		s.printf("    %s is indexed without the compiler: graded edges only.\n", it.Language)
		return
	}
	s.installed++
}

func (s *guidedSetup) carryOut(it indexer.SetupItem) error {
	switch it.Kind {
	case indexer.SetupIndexer:
		plan := indexer.Plan{ProvisionPolicy: s.cfg.ProvisionPolicy, Runner: s.cfg.IndexerRunner, Timeout: s.cfg.IndexerTimeout}
		if plan.ProvisionPolicy == config.ProvisionNever {
			return errors.New("accuracy.indexers.provision is never in your user config")
		}
		if plan.ProvisionPolicy == "" {
			plan.ProvisionPolicy = config.ProvisionPrompt
		}
		if plan.Runner == "" {
			plan.Runner = indexer.RunnerNative
		}
		done, failed := setupProvision(s.ctx, plan, []indexer.Install{*it.Install}, indexer.Options{
			AssumeYes: true, Logger: s.g.Log(), Audit: indexer.LogAudit{Logger: s.g.Log()},
		})
		if len(failed) > 0 {
			return fmt.Errorf("could not install %s: %w", failed[0].Install.Indexer, failed[0].Err)
		}
		for _, d := range done {
			s.printf("    Installed %s %s.\n", d.Indexer, d.Version)
		}
		return nil
	case indexer.SetupToolchain, indexer.SetupUserIndexer, indexer.SetupFetch:
		if len(it.Commands) == 0 {
			return fmt.Errorf("GraphLume cannot install this here; %s", it.Manual)
		}
		if it.PathAdd != "" {
			// Before the commands, not after: a tool a command installs into
			// a directory that is not on PATH (Homebrew's rustup) has to be
			// found by the command that follows it.
			_ = os.Setenv("PATH", it.PathAdd+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		dirs := []string{it.Dir}
		if len(it.Dirs) > 0 {
			dirs = it.Dirs
		}
		for _, d := range dirs {
			if err := s.runAll(d, it.Commands); err != nil {
				return err
			}
		}
		s.present[it.Key] = true
		s.printf("    Done.\n")
		return nil
	case indexer.SetupRepoBuild:
		if err := s.runAll(it.Dir, it.Prereq); err != nil {
			return err
		}
		if err := s.runAll(it.Dir, it.Commands); err != nil {
			return err
		}
		if it.CopyFrom != "" {
			if err := copyFile(it.CopyFrom, it.CopyTo); err != nil {
				return err
			}
		}
		target := it.CopyTo
		if target == "" {
			target = filepath.Join(it.Dir, "compile_commands.json")
		}
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("the build finished but wrote no %s", target)
		}
		s.printf("    Wrote %s.\n", target)
		return nil
	}
	return fmt.Errorf("unknown offer kind %q", it.Kind)
}

func (s *guidedSetup) runAll(dir string, cmds [][]string) error {
	for _, argv := range cmds {
		s.printf("    $ %s\n", strings.Join(argv, " "))
		if err := s.run.Run(s.ctx, dir, argv, s.out); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
	}
	return nil
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from) //nolint:gosec // a build tree GraphLume chose, under .graphlume
	if err != nil {
		return fmt.Errorf("read %s: %w", from, err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil { //nolint:gosec // G703: the project directory Detect found under the repository root, plus a fixed name
		return fmt.Errorf("write %s: %w", to, err)
	}
	return nil
}

// close prints the one line that says what stays graded.
func (s *guidedSetup) close() {
	plan, err := indexer.NewPlanWith(s.ctx, s.cfg.RepoRoot, s.cfg, indexer.Options{AllowRepoBuild: s.allowBuild})
	if err != nil {
		return
	}
	graded := indexer.GradedLanguages(plan)
	if len(graded) == 0 {
		s.printf("\nEvery detected language has a compiler indexer ready. graphlume indexers list shows this again.\n")
		return
	}
	s.printf("\n%s %s graded, not proven, until a compiler indexer runs for %s. graphlume indexers list shows this again.\n",
		joinAnd(graded), plural(len(graded), "stays", "stay"), plural(len(graded), "it", "them"))
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// setupHints is the short form for an init that does not run the guided setup
// (no terminal, and neither --indexers nor --all): one line per offer.
func setupHints(ctx context.Context, g *Global, out io.Writer) {
	cfg := g.Config()
	plan, err := indexer.NewPlan(ctx, cfg.RepoRoot, cfg)
	if err != nil {
		return
	}
	run := newSetupRunner()
	home, _ := os.UserHomeDir()
	items := indexer.SetupItems(plan, indexer.SetupEnv{GOOS: setupGOOS, LookPath: run.LookPath, Home: home})
	var lines []string
	for _, it := range items {
		if it.After != "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("  • %s: %s", it.Language, it.Missing))
	}
	if len(lines) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nCompiler indexers would prove most relationships for these languages:")
	_, _ = fmt.Fprintln(out, strings.Join(lines, "\n"))
	_, _ = fmt.Fprintln(out, "Run 'graphlume init --indexers' in a terminal to be walked through them, or see 'graphlume indexers list'.")
}
