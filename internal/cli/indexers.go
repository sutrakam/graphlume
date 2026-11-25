package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/indexer"

	"github.com/spf13/cobra"
)

func newIndexersCmd(g *Global) *cobra.Command {
	cfgPath, _ := config.UserConfigPath()
	if cfgPath == "" {
		cfgPath = "~/.config/graphlume/config.yml"
	}
	cmd := &cobra.Command{
		Use:   "indexers",
		Short: "Inspect and install compiler-backed indexers",
		Long: fmt.Sprintf(`Compiler-backed indexers produce facts a typechecker proved, which is the
only way an edge earns 'exact' confidence. Where one runs it proves most of a
language's relationships, not all of them; everything else GraphLume grades by
how sure it is.

These are third-party tools that run build systems, so GraphLume will not
install one without being asked. Which indexers may be installed, and whether
installation may happen without prompting, is controlled by your USER config
(%s) and never by the repository — a cloned repo
must not be able to decide what runs on your machine.`, cfgPath),
	}
	cmd.AddCommand(newIndexersListCmd(g), newIndexersInstallCmd(g), newIndexersPinsCmd(g))
	return cmd
}

func newIndexersListCmd(g *Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show what would run, and what is missing",
		Long: `Reports the languages detected in this project, whether the toolchain
each indexer needs is present, and what would have to be installed.

This is pure inspection: it changes nothing and installs nothing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := g.Config()

			plan, err := indexer.NewPlan(ctx, cfg.RepoRoot, cfg)
			if err != nil {
				return fmt.Errorf("plan indexers: %w", err)
			}

			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), plan)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Accuracy mode: %s\n", plan.Accuracy)
			// The toolchain line lists the tools the detected projects need.
			// A plan that needs none (C/C++ only, or nothing detected) has
			// nothing to say there, and an empty "Toolchain:" reads as a bug.
			if sum := plan.Toolchain.Summary(); sum != "" {
				fmt.Fprintf(out, "Toolchain:     %s\n", sum)
			}
			fmt.Fprintln(out)

			loose := plan.Unplanned()
			if len(plan.Projects) == 0 && len(loose) == 0 {
				fmt.Fprintln(out, "No indexable projects detected (no pom.xml, go.mod, tsconfig.json, pyproject.toml, .csproj, CMakeLists.txt …).")
				if plan.Note != "" && plan.Accuracy == config.AccuracyFast {
					fmt.Fprintf(out, "\n%s\n", plan.Note)
				}
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "LANGUAGE\tINDEXER\tSTATUS\tPATH")
			// Reasons are collected and printed under the table rather than in
			// it: they are sentences, and a sentence in a tabwriter column
			// destroys the alignment of every other row.
			var blocked []string
			seenReason := map[string]bool{}
			for _, s := range plan.Steps {
				status := "ready"
				switch {
				case len(s.MissingTools) > 0:
					// A missing toolchain is not something provisioning can
					// fix: GraphLume installs scip-java, not a JDK. Saying so
					// here is the difference between an actionable message and
					// a user running `indexers install` in a loop.
					var names []string
					for _, t := range s.MissingTools {
						names = append(names, string(t))
					}
					status = "blocked: needs " + strings.Join(names, ", ")
					if s.Reason != "" && !seenReason[s.Reason] {
						seenReason[s.Reason] = true
						blocked = append(blocked, string(s.Indexer)+": "+s.Reason)
					}
				case s.Blocked:
					// Without this case a blocked step fell through to "ready".
					// Java is the one that bites: indexing it runs the
					// repository's own Maven or Gradle build, so the planner
					// blocks it pending an explicit opt-in — and the table
					// announced the indexer as ready to use. A safety gate the
					// interface does not show is a safety gate the user works
					// around by accident.
					status = "blocked"
					if s.Reason != "" && !seenReason[s.Reason] {
						seenReason[s.Reason] = true
						blocked = append(blocked, string(s.Indexer)+": "+s.Reason)
					}
				case s.NeedsProvision:
					status = "not installed"
				case s.Installed.Preexisting:
					status = "ready (found on PATH)"
				}
				// An advisory attaches to a step that WILL run, so it cannot be
				// folded into the blocked list. It is printed with the blocked
				// reasons because both answer the same question -- what will I
				// not get, and why -- and a warning shown nowhere is a warning
				// that does not exist.
				if s.Advisory != "" && !seenReason[s.Advisory] {
					seenReason[s.Advisory] = true
					blocked = append(blocked, string(s.Indexer)+" (will run, degraded): "+s.Advisory)
					if status == "ready (found on PATH)" || status == "not installed" {
						status += ", degraded"
					}
				}
				if !s.Blocked && len(s.MissingTools) == 0 {
					for _, n := range s.Notes {
						if !seenReason[n] {
							seenReason[n] = true
							blocked = append(blocked, string(s.Indexer)+" (will run): "+n)
						}
					}
				}
				name := string(s.Indexer)
				if s.Project.Language == indexer.LangGo {
					// resolve never runs scip-go: the type checker built into
					// this binary proves Go (see pipeline.RunCompilerIndexers),
					// so naming scip-go here described a tool that does not run.
					name, status = "go/types", "built in"
					if plan.Accuracy == config.AccuracyFast {
						status = "skipped (accuracy fast)"
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Project.Language, name, status, s.Project.Dir)
			}
			// Languages with source files and no project: listed, with the
			// file count, rather than left out. Twelve Python scripts with no
			// pyproject.toml used to vanish from this table.
			for _, l := range loose {
				pin, _ := indexer.PinFor(l.Language)
				status := fmt.Sprintf("not planned: %d %s, no project", l.Files, plural(l.Files, "file", "files"))
				if l.Language == indexer.LangCPP {
					status = fmt.Sprintf("blocked: %d %s, no compile_commands.json", l.Files, plural(l.Files, "file", "files"))
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Language, pin.Indexer, status, l.Dir)
				if l.Language != indexer.LangCPP {
					blocked = append(blocked, string(pin.Indexer)+": "+l.Why)
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}

			for _, b := range blocked {
				fmt.Fprintf(out, "\n%s\n", b)
			}
			if note := cppNote(plan, loose); note != "" {
				fmt.Fprintf(out, "\n%s\n", note)
			}

			if plan.Note != "" && len(plan.Projects) > 0 {
				fmt.Fprintf(out, "\n%s\n", plan.Note)
			}
			if len(cfg.IndexerDeclined) > 0 {
				fmt.Fprintf(out, "\nDeclined during setup, not offered again: %s\n(graphlume indexers install --ask-again forgets these answers.)\n",
					strings.Join(cfg.IndexerDeclined, ", "))
			}
			return nil
		},
	}
}

// cppNote explains, once, what a compiler pass over C or C++ needs: a
// compile_commands.json from a working build, and --allow-repo-build, and how
// to produce the file for the build system this repository uses.
func cppNote(plan indexer.Plan, loose []indexer.LooseLanguage) string {
	dir := ""
	hasDB := false
	for _, p := range plan.Projects {
		if p.Language == indexer.LangCPP {
			if dir == "" {
				dir = p.Dir
			}
			for _, m := range p.Markers {
				if m == "compile_commands.json" {
					hasDB = true
				}
			}
		}
	}
	for _, l := range loose {
		if l.Language == indexer.LangCPP {
			dir = l.Dir
		}
	}
	if dir == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("scip-clang (C/C++): needs compile_commands.json from a working build of the project, and runs only " +
		"with --allow-repo-build, because it executes the compilers and flags that file names.")
	if hasDB {
		b.WriteString(" A compile_commands.json is present: graphlume resolve --allow-repo-build indexes it.")
		return b.String()
	}
	build := indexer.FindCBuild(plan.RepoRoot, dir)
	where := build.Dir
	if where == "." {
		where = "the repository root"
	}
	switch build.System {
	case "cmake":
		fmt.Fprintf(&b, " This project builds with CMake (%s): cmake -S . -B build -DCMAKE_EXPORT_COMPILE_COMMANDS=ON, "+
			"then copy build/compile_commands.json into %s.", where, where)
	case "meson":
		fmt.Fprintf(&b, " This project builds with Meson (%s): meson setup build writes build/compile_commands.json; "+
			"copy it into %s.", where, where)
	case "scons", "make":
		fmt.Fprintf(&b, " This project builds with %s (%s): run the build under bear (bear -- %s, in %s; "+
			"bear comes from your package manager, for example brew install bear).", buildLabel(build.System), where, build.System, where)
	default:
		b.WriteString(" No CMake, Meson, SCons or make build was found: generate the file with your build, for example under bear.")
	}
	b.WriteString(" graphlume init --index offers to do this at a terminal.")
	return b.String()
}

func buildLabel(system string) string {
	if system == "scons" {
		return "SCons"
	}
	return system
}

func newIndexersPinsCmd(g *Global) *cobra.Command {
	return &cobra.Command{
		Use:   "pins",
		Short: "Print the pinned indexer versions and digests",
		Long: `Prints one line per compiler-backed indexer this build of GraphLume may
install: name, version, channel, the reference it installs, and the digest it
verifies (or "-" where the channel verifies itself, as go install does against
the checksum database).

The output depends on nothing but the binary — no repository, config or
network is read — which makes it a sound CI cache key: when a pin changes, the
key changes.

  graphlume indexers pins > indexer-pins.txt`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pins := indexer.Pins()
			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), pins)
			}
			return writePins(cmd.OutOrStdout(), pins)
		},
	}
}

// writePins renders one tab-separated line per pin. Every line has five fields;
// an absent digest is "-" so a consumer splitting on tabs never sees a short row.
func writePins(w io.Writer, pins []indexer.Pin) error {
	for _, p := range pins {
		digest := p.TarballSHA256
		if p.ImageDigest != "" {
			digest = p.ImageDigest
		}
		if digest == "" {
			digest = "-"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			p.Indexer, p.Version, p.Channel, p.Ref(), digest); err != nil {
			return fmt.Errorf("write pins: %w", err)
		}
	}
	return nil
}

// prepareIndexerOutDir turns --out-dir into the absolute directory Execute
// writes .scip files into.
//
// New or empty, never "whatever is there": CI imports every *.scip in it, and a
// stale index left by an earlier run would be imported as if this run had
// produced it.
func prepareIndexerOutDir(raw string) (string, error) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("--out-dir %q: %w", raw, err)
	}
	entries, err := os.ReadDir(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Windows reports ReadDir on a regular file as "path not found", which
		// would otherwise surface as a confusing mkdir failure below.
		if fi, statErr := os.Stat(abs); statErr == nil && !fi.IsDir() {
			return "", fmt.Errorf("--out-dir %q is not a directory", raw)
		}
		if mkErr := os.MkdirAll(abs, 0o700); mkErr != nil {
			return "", fmt.Errorf("--out-dir %q: create: %w", raw, mkErr)
		}
		return abs, nil
	case err != nil:
		return "", fmt.Errorf("--out-dir %q: %w", raw, err)
	case len(entries) > 0:
		return "", fmt.Errorf("--out-dir %q is not empty; pass a new or empty directory so no earlier index is imported as this run's", raw)
	}
	return abs, nil
}

func newIndexersInstallCmd(g *Global) *cobra.Command {
	var yes bool
	var allowBuild bool
	var askAgain bool
	var outDir string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the indexers this project needs",
		Long: `Installs the compiler-backed indexers for the languages detected here.

Every download is pinned to an exact version and verified against a recorded
digest; a mismatch refuses rather than installing. An indexer with no recorded
digest is never fetched at all.

Installing requires consent: without --yes you are asked per indexer, unless
your user config sets accuracy.indexers.provision to auto.

Java is refused by default: indexing it runs the repository's own Maven or
Gradle build, which executes arbitrary code from the repository — a build
script, a plugin, or any dependency's build hook. --allow-repo-build is how you
say you have decided that is acceptable for THIS repository. Prefer
accuracy.indexers.runner=docker, which gets the same index without trusting the
repo with your machine.

--ask-again forgets the setup offers you declined during 'graphlume init'
(accuracy.indexers.declined in your user config), so the next init asks about
them again. It installs nothing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := g.Config()

			if askAgain {
				path, err := config.UserConfigPath()
				if err != nil {
					return fmt.Errorf("find user config: %w", err)
				}
				n, err := config.ForgetDeclined(path)
				if err != nil {
					return err
				}
				if g.JSON {
					return emitJSON(cmd.OutOrStdout(), map[string]any{"forgotten": n, "config": path})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Forgot %d declined setup %s in %s; graphlume init --index asks about them again.\n",
					n, plural(n, "answer", "answers"), path)
				return nil
			}

			// The opt-in has to reach NewPlanWith, not just Execute: the Java
			// block is computed while PLANNING, so passing it only to Execute
			// leaves the step already marked blocked and the flag silently
			// inert.
			plan, err := indexer.NewPlanWith(ctx, cfg.RepoRoot, cfg, indexer.Options{AllowRepoBuild: allowBuild})
			if err != nil {
				return fmt.Errorf("plan indexers: %w", err)
			}
			if len(plan.Steps) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to install: no indexable projects detected.")
				return nil
			}

			if installs := plan.Installs(false); len(installs) > 0 && !g.JSON {
				fmt.Fprintln(cmd.OutOrStdout(), "About to install:")
				fmt.Fprint(cmd.OutOrStdout(), indexer.DescribeInstalls(installs))
			}

			opts := indexer.Options{
				AssumeYes:      yes,
				AllowRepoBuild: allowBuild,
				Logger:         g.Log(),
				Audit:          indexer.LogAudit{Logger: g.Log()},
			}
			if outDir != "" {
				dir, err := prepareIndexerOutDir(outDir)
				if err != nil {
					return err
				}
				opts.OutputDir = dir
			}
			results, err := indexer.Execute(ctx, plan, opts)
			if err != nil {
				return err
			}

			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), results)
			}
			out := cmd.OutOrStdout()
			for _, e := range results.Indexes {
				// Zero bytes with a clean exit is a real failure mode for these
				// tools, so report the size rather than just the path: a 0 B
				// index that looks successful is worse than an error.
				fmt.Fprintf(out, "  %-16s %s (%s)\n", e.Indexer, e.IndexPath, humanBytes(e.Bytes))
			}
			for _, s := range results.Skipped {
				fmt.Fprintf(out, "  %-16s skipped: %s\n", s.Indexer, s.Reason)
				// The indexer's own words. Without them "indexer failed" is a
				// dead end: the cause is usually a build detail only the tool
				// saw, and it usually names its own fix.
				for _, line := range strings.Split(strings.TrimSpace(s.Diagnostic), "\n") {
					if line != "" {
						fmt.Fprintf(out, "  %-16s   %s\n", "", line)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "install without prompting (still verifies digests)")
	// Named for what it costs, not for what it unlocks. The blocked-step
	// message points here; before this flag existed it named an opt-in with no
	// CLI surface at all, so the only way past the Java gate was to edit config
	// — which is why Java shipped unmeasured.
	cmd.Flags().BoolVar(&allowBuild, "allow-repo-build", false,
		"permit indexers that execute code the repository chooses (Java build, C/C++ compile_commands.json, Rust cargo); it executes code from the repo")
	cmd.Flags().BoolVar(&askAgain, "ask-again", false,
		"forget the setup offers you declined during init, so init asks again; installs nothing")
	cmd.Flags().StringVar(&outDir, "out-dir", "",
		"write .scip files here (created if absent, must be empty) instead of a temporary directory")
	return cmd
}
