// Package cli assembles GraphLume's command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/observe"
	"github.com/sutrakam/graphlume/internal/store"

	"github.com/spf13/cobra"
)

// Build metadata, injected via -ldflags at release time. Spec §64.
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

// Global holds flags shared by every command.
type Global struct {
	JSON    bool
	Verbose bool
	Quiet   bool
	Project string

	cfg config.Config
	log *slog.Logger
}

// IndexDir is where GraphLume stores its registry inside a project.
const IndexDir = ".graphlume"

// IndexFile is the SQLite registry filename.
const IndexFile = "index.db"

// Config returns the resolved configuration.
func (g *Global) Config() config.Config { return g.cfg }

// Log returns the logger.
func (g *Global) Log() *slog.Logger {
	if g.log == nil {
		return slog.Default()
	}
	return g.log
}

// DBPath returns the registry path for the resolved project root.
func (g *Global) DBPath() string {
	return projectIndexPath(g.cfg.RepoRoot)
}

// NewRoot builds the root command tree.
func NewRoot() *cobra.Command {
	g := &Global{}

	root := &cobra.Command{
		Use:     "graphlume",
		Version: Version,
		Short:   "Deterministic code intelligence & change safety for AI coding agents",
		Long: `GraphLume builds a queryable knowledge graph of a software system —
symbols, references, APIs, database access, tests, configuration and Git
history — and serves it to AI coding agents over MCP.

Every relationship it reports carries a confidence level and the source
location that justifies it, so an agent can tell a compiler-proven fact from
a heuristic guess.

QUICK START:
  graphlume init --all     Initialize repository & connect all coding agents
  graphlume ui             Open interactive local web portal in browser (http://127.0.0.1:7777)
  graphlume status         Inspect index health, entity counts, and language breakdown
  graphlume blindspots     Detect code intelligence blindspots and hidden risks
  graphlume doctor         Diagnose environment & verify graph integrity
  graphlume bundle         Export sanitized diagnostic bundle for bug reports & issues`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return g.setup(cmd)
		},
	}

	pf := root.PersistentFlags()
	pf.BoolVar(&g.JSON, "json", false, "emit machine-readable JSON on stdout")
	pf.BoolVarP(&g.Verbose, "verbose", "v", false, "verbose logging")
	pf.BoolVarP(&g.Quiet, "quiet", "q", false, "suppress all logging")
	pf.StringVar(&g.Project, "project", "", "project root (default: current directory)")

	root.AddCommand(
		newInitCmd(g),
		newIndexCmd(g),
		newIndexersCmd(g),
		newResolveCmd(g),
		newSCIPCmd(g),
		newServeCmd(g),
		newWatchCmd(g),
		newAPICmd(g),
		newArchCmd(g),
		newDocsCmd(g),
		newVerifyCmd(g),
		newDiffCmd(g),
		newAgentCmd(g),
		newInstallCmd(g),
		newSetupCmd(g),
		newCleanCmd(g),
		newUninstallCmd(g),
		newWhoCmd(g),
		newImpactCmd(g),
		newPRImpactCmd(g),
		newInfraCmd(g),
		newSystemCmd(g),
		newWorkspaceCmd(g),
		newUICmd(g),
		newHooksCmd(g),
		newAutoIndexCmd(g),
		newCoverageCmd(g),
		newTraceCmd(g),
		newCompileCmd(g),
		newRenameCmd(g),
		newMoveCmd(g),
		newBenchCmd(g),
		newConfigCmd(g),
		newMigrateCmd(g),
		newDepsCmd(g),
		newDBCmd(g),
		newSearchCmd(g),
		newSymbolCmd(g),
		newReferencesCmd(g),
		newCallersCmd(g),
		newCalleesCmd(g),
		newExploreCmd(g),
		newContextCmd(g),
		newStatusCmd(g),
		newDoctorCmd(g),
		newBundleCmd(g),
		newAccuracyCmd(g),
		newVersionCmd(g),
		newLicensesCmd(g),
		newLanguagesCmd(g),
		newExportCmd(g),
		newPackCmd(g),
		newPruneCmd(g),
		newDiffMinimizeCmd(g),
		newMissedCmd(g),
		newPlanChangeCmd(g),
		newEventsCmd(g),
		newReportCmd(),
		newToolCmd(g),
		newStatsCmd(g),
		newDeadCodeCmd(g),
		newHotspotsCmd(g),
		newBusFactorCmd(g),
		newCommunitiesCmd(g),
		newPathCmd(g),
		newTourCmd(g),
		newLessonCmd(g),
		newMentionsCmd(g),
		newImplementationsCmd(g),
		newTestsCmd(g),
		newQueryCmd(g),
		newSBOMCmd(g),
		newVulnsCmd(g),
		newCICmd(g),
		newPolicyCmd(g),
	)
	applyCommandGroups(root)
	return root
}

func (g *Global) setup(cmd *cobra.Command) error {
	if err := checkLimitFlags(cmd); err != nil {
		return err
	}
	// Diagnostics stay out of what a person reads unless they ask for them:
	// commands print their own progress and results.
	level := "warn"
	if g.Verbose {
		level = "debug"
	}
	g.log = observe.New(observe.Options{
		Level: level,
		JSON:  g.JSON,
		Quiet: g.Quiet,
	})

	root := g.Project
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determine working directory: %w", err)
		}
		root = wd
		if servesAnIndex[cmd.Name()] {
			// An agent started from a user-level config runs this in its
			// working folder, which may be below the project root.
			if found := nearestIndexedRoot(wd); found != "" {
				root = found
			}
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve project root %q: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("project root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project root %q is not a directory", abs)
	}
	if projectCommands[cmd.Name()] {
		if err := refuseBroadRoot(abs); err != nil {
			return err
		}
	}

	cfg, err := config.Load(abs)
	if err != nil {
		return err
	}
	if cfg.ProjectName == "" {
		cfg.ProjectName = filepath.Base(abs)
	}
	g.cfg = cfg
	return nil
}

// errInvalidArgument marks an error as the caller's argument being wrong.
var errInvalidArgument = errors.New("invalid argument")

// checkLimitFlags enforces one rule for every command's -n/--limit: when the
// flag is given it must be a positive number. Zero would otherwise mean "the default"
// in one command and "no limit" in another, and a negative number would be passed
// to a query as it is. An unset flag keeps the command's own default.
func checkLimitFlags(cmd *cobra.Command) error {
	f := cmd.Flags().Lookup("limit")
	if f == nil || !f.Changed || f.Value.Type() != "int" {
		return nil
	}
	n, err := strconv.Atoi(f.Value.String())
	if err != nil || n < 1 {
		return fmt.Errorf("%w: --limit must be positive, got %s (omit it for the default)", errInvalidArgument, f.Value.String())
	}
	return nil
}

// jsonError is the object a failing command prints on stdout under --json.
type jsonError struct {
	Error jsonErrorBody `json:"error"`
}

type jsonErrorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// writeJSONError prints err as a JSON object so a consumer reading stdout gets
// a document, not empty input.
func writeJSONError(w io.Writer, err error) {
	code := "error"
	if errors.Is(err, errInvalidArgument) {
		code = "invalid_argument"
	}
	_ = emitJSON(w, jsonError{Error: jsonErrorBody{Message: err.Error(), Code: code}})
}

// ExitCodeError is an error that carries the process exit code it wants.
//
// It exists so a command whose exit code IS its output — `verify`, whose four
// dispositions a CI job branches on — can express that without calling os.Exit
// from inside RunE. A command that exits the process directly cannot be tested
// for the one behaviour that matters about it, and the codes would then be the
// only untested part of a gate whose entire job is to fail correctly.
type ExitCodeError struct {
	Code int
	// Silent is set when the command already printed its report. The code is
	// the message at that point, and an "error:" line beneath a rendered report
	// reads as a malfunction rather than a verdict.
	Silent bool
	Err    error
}

func (e *ExitCodeError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

func (e *ExitCodeError) Unwrap() error { return e.Err }

// Execute runs the CLI, returning a process exit code.
func Execute(ctx context.Context) int {
	return executeRoot(ctx, NewRoot(), os.Stdout, os.Stderr)
}

// executeRoot runs root and reports its error as text on stderr and, under
// --json, as a JSON object on stdout too, so a consumer parsing stdout is
// never handed empty input.
func executeRoot(ctx context.Context, root *cobra.Command, stdout, stderr io.Writer) int {
	if err := root.ExecuteContext(ctx); err != nil {
		// A corrupt index surfaces as a bare driver error from wherever the
		// first query ran; say what it means and how to recover.
		err = store.ExplainCorrupt(err)
		asJSON, _ := root.PersistentFlags().GetBool("json")
		var ec *ExitCodeError
		if errors.As(err, &ec) {
			if !ec.Silent {
				fmt.Fprintln(stderr, "error:", err)
				if asJSON {
					writeJSONError(stdout, err)
				}
			}
			return ec.Code
		}
		fmt.Fprintln(stderr, "error:", err)
		if asJSON {
			writeJSONError(stdout, err)
		}
		return 1
	}
	return 0
}

func newVersionCmd(g *Global) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build metadata",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"version":    Version,
					"commit":     Commit,
					"build_time": BuildTime,
					"go_os":      osName(),
					"go_arch":    archName(),
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "graphlume %s (commit %s, built %s, %s/%s)\n",
				Version, Commit, BuildTime, osName(), archName())
			return nil
		},
	}
}

// projectCommands are the commands that walk, index or register the project
// root. Run in a home directory they would read every folder in it (on macOS
// that asks for Music, Photos and the rest) and register the whole home
// directory with every agent, so they refuse a root that cannot be a project.
var projectCommands = map[string]bool{
	"init": true, "setup": true, "install": true, "index": true,
	"resolve": true, "watch": true, "auto-index": true,
}

// refuseBroadRoot reports an error when root is a home directory, the
// directory that holds home directories, or a filesystem root.
func refuseBroadRoot(root string) error {
	clean := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	r := clean(root)
	why := ""
	if filepath.Dir(r) == r {
		why = "the root of the filesystem"
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		h := clean(home)
		switch r {
		case h:
			why = "your home directory"
		case filepath.Dir(h):
			why = "the folder that holds home directories"
		}
	}
	if why == "" {
		return nil
	}
	return fmt.Errorf("%s is %s, not a project: GraphLume indexes one repository at a time. "+
		"cd into a project and run the command there, or pass --project <path to the project>", r, why)
}

// errInterrupted ends a command the user stopped with Ctrl-C. It has already
// said what it stopped, so no "error:" line follows; 130 is the shell's code
// for a process ended by SIGINT.
var errInterrupted = &ExitCodeError{Code: 130, Silent: true}

// servesAnIndex are the commands an agent or a person starts from anywhere
// inside a project, which therefore look upward for its index.
var servesAnIndex = map[string]bool{"serve": true, "ui": true}

// nearestIndexedRoot is the closest directory at or above dir that has a
// GraphLume index, or "" when none does before the home directory or the
// filesystem root.
func nearestIndexedRoot(dir string) string {
	home, _ := os.UserHomeDir()
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if refuseBroadRoot(d) != nil || (home != "" && d == filepath.Clean(home)) {
			return ""
		}
		if _, err := os.Stat(projectIndexPath(d)); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}
