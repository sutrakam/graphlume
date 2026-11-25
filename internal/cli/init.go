package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/store"
)

// newInitCmd initializes GraphLume for the repository and configures coding agents.
func newInitCmd(g *Global) *cobra.Command {
	var (
		force        bool
		withAll      bool
		withIndex    bool
		withAgents   bool
		withRules    bool
		withIndexers bool
		assumeYes    bool
		allowBuild   bool
		localOnly    bool
		profileFlag  string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize GraphLume and configure coding agents for this repository",
		Long: `Initializes GraphLume for the current project:
1. Applies the SQLite registry schema to .graphlume/index.db.
2. Auto-registers GraphLume in detected coding agents (when --agents or --all is passed).
3. Injects agent guidance into AGENTS.md (when --rules or --all is passed).
4. Walks you through the compiler indexers the detected languages need, before indexing (when
   --indexers or --all is passed, and at a terminal before --index). Compilers prove most
   relationships; without one a language's answers are graded, not proven. For each language
   that has none, it says what is missing, what would be downloaded, from where and about how
   big, then asks:
     [Y/n]  a small pinned, checksum-verified indexer (scip-python, scip-typescript)
     [y/N]  a toolchain (the .NET SDK, Node.js, a JDK, Maven, Gradle), installed with Homebrew
            on macOS when you say yes, otherwise its official download page is printed
     [y/N]  generating compile_commands.json for C/C++ (CMake configure, or the build under
            bear), which runs the repository's own build scripts
   A "no" is remembered in your user config (accuracy.indexers.declined), never in the
   repository; 'graphlume indexers install --ask-again' forgets those answers.
   Without a terminal nothing is asked and nothing is installed: the plan is printed. --yes
   accepts only the [Y/n] offers; a repository build also needs --allow-repo-build; a toolchain
   is never installed without a yes typed at a terminal. A failure is reported and indexing
   always goes on.
5. Performs initial indexing and reference resolution (when --index or --all is passed).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if withAll {
				withIndex = true
				withAgents = true
				withRules = true
				withIndexers = true
			}
			ctx := cmd.Context()
			if g.cfg.RepoRoot == "" {
				if g.Project != "" {
					g.cfg.RepoRoot = g.Project
				} else {
					g.cfg.RepoRoot, _ = os.Getwd()
				}
				if g.cfg.ProjectName == "" {
					g.cfg.ProjectName = filepath.Base(g.cfg.RepoRoot)
				}
			}
			cfg := g.Config()
			dir := filepath.Join(cfg.RepoRoot, IndexDir)
			primary := filepath.Join(dir, IndexFile)

			existed := false
			if _, err := os.Stat(primary); err == nil && !force {
				// Running init again is how people check their setup, so it
				// reuses the index rather than failing. A damaged index is
				// moved aside and rebuilt; nothing is deleted.
				if moved, err := setAsideIfCorrupt(ctx, primary); err != nil {
					return err
				} else if moved != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The index at %s was damaged. It was moved to %s, and a new one is built now.\n", primary, moved)
				} else {
					existed = true
					if !withIndex && !withAgents && !withRules && !withIndexers {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "GraphLume is already set up for %q (index at %s).\n"+
							"  Update the index:   graphlume index\n  Connect agents:     graphlume init --all\n  Start over:         graphlume init --force\n",
							cfg.ProjectName, primary)
						return nil
					}
				}
			} else if err == nil && force {
				if err := os.Remove(primary); err != nil {
					return fmt.Errorf("remove existing index: %w", err)
				}
				_ = os.Remove(primary + "-wal")
				_ = os.Remove(primary + "-shm")
				g.Log().Warn("removed existing index", "path", primary)
			}

			if err := os.MkdirAll(dir, 0o750); err != nil {
				return fmt.Errorf("create %s: %w", dir, err)
			}
			if err := writeIfAbsent(filepath.Join(dir, ".gitignore"), "*\n"); err != nil {
				return err
			}

			db, err := store.Open(ctx, store.Options{Path: primary})
			if err != nil {
				return err
			}

			if err := store.VerifyFTS5(ctx, db.DB); err != nil {
				_ = db.Close()
				return err
			}
			applied, err := store.Migrate(ctx, db.DB, time.Now().Unix)
			if err != nil {
				_ = db.Close()
				return fmt.Errorf("apply schema: %w", err)
			}
			version, err := store.SchemaVersion(ctx, db.DB)
			if err != nil {
				_ = db.Close()
				return err
			}
			_ = db.Close()

			if home, err := os.UserHomeDir(); err == nil {
				lockPath := filepath.Join(dir, "reindex.lock")
				if err := multirepo.RegisterProject(ctx, home, multirepo.ProjectEntry{
					Name:          cfg.ProjectName,
					RootPath:      cfg.RepoRoot,
					DBPath:        primary,
					LockPath:      lockPath,
					Mode:          "single_repo",
					SchemaVersion: version,
					WatchPolicy:   "auto_lru",
				}); err != nil {
					// The index is usable without it; only cross-project
					// discovery loses this project, and the user should know.
					g.Log().Warn("could not add the project to ~/.graphlume/registry.json", "error", err)
				}
			}

			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), map[string]any{
					"project":            cfg.ProjectName,
					"index_path":         primary,
					"schema_version":     version,
					"migrations_applied": applied,
					"accuracy_mode":      cfg.Accuracy,
				})
			}

			out := cmd.OutOrStdout()
			ui := NewUI(out)
			if existed {
				_, _ = fmt.Fprintf(out, "Using the existing GraphLume index for %q (schema v%d); it is updated below.\n", cfg.ProjectName, version)
			} else {
				_, _ = fmt.Fprintf(out, "Initialized GraphLume for %q (schema v%d, %d %s applied)\n", cfg.ProjectName, version, len(applied),
					plural(len(applied), "migration", "migrations"))
			}
			if !hasProjectFiles(cfg.RepoRoot) {
				_, _ = fmt.Fprintf(out, "No source files in %s yet, so the index will be empty. Run graphlume index after you add code.\n", cfg.RepoRoot)
			}

			if withAgents {
				home, err := os.UserHomeDir()
				if err == nil {
					serverArgs := []string{"--project", cfg.RepoRoot, "serve"}
					if profileFlag != "" {
						p := config.MCPProfile(profileFlag)
						if p.Valid() {
							serverArgs = append(serverArgs, "--profile", profileFlag)
						} else {
							serverArgs = append(serverArgs, "--profile", "compact")
						}
					} else {
						serverArgs = append(serverArgs, "--profile", "compact")
					}
					serverArgs = append(serverArgs, "--audit-log", filepath.Join(cfg.RepoRoot, IndexDir, "audit.jsonl"))
					spec := agentServerSpec{
						Name:    agentMCPName,
						Command: "graphlume",
						Args:    serverArgs,
					}
					results, _ := installClients(home, cfg.RepoRoot, spec, false, false, localOnly, nil)
					if g.Verbose {
						written := printInstallResults(out, results, false)
						if written > 0 {
							ui.Success("Registered GraphLume MCP server in detected coding agents.")
						}
					} else {
						printInstallSummary(out, results)
					}
				}
			}

			if withRules {
				if err := injectAgentGuidance(cmd, cfg.RepoRoot, false, false); err != nil {
					g.Log().Warn("inject agent guidance", "error", err)
				} else {
					ui.Success("Injected code intelligence guidance into AGENTS.md and instruction files.")
				}
			}

			// The guided setup runs whenever indexers would be installed, and
			// is offered at a terminal before a plain --index too. Without a
			// terminal it prints the plan and installs only what --yes covers.
			setupRan := false
			if withIndexers || (withIndex && setupInteractive()) {
				runGuidedSetup(ctx, g, cmd.InOrStdin(), out, assumeYes, allowBuild)
				setupRan = true
				if ctx.Err() != nil {
					return errInterrupted
				}
			}

			if withIndex {
				_, _ = fmt.Fprintln(out)
				ui.Header("Indexing codebase files")
				idxCmd := newIndexCmd(g)
				idxCmd.Annotations = map[string]string{"caller": "init"}
				idxCmd.SetContext(ctx)
				idxCmd.SetOut(out)
				idxCmd.SetErr(cmd.ErrOrStderr())
				if err := idxCmd.RunE(idxCmd, nil); err != nil {
					return fmt.Errorf("initial index: %w", err)
				}

				_, _ = fmt.Fprintln(out)
				ui.Header("Resolving cross-file reference graph")
				resCmd := newResolveCmd(g)
				if allowBuild {
					_ = resCmd.Flags().Set("allow-repo-build", "true")
				}
				resCmd.SetContext(ctx)
				resCmd.SetOut(out)
				resCmd.SetErr(cmd.ErrOrStderr())
				if err := resCmd.RunE(resCmd, nil); err != nil {
					return fmt.Errorf("initial resolve: %w", err)
				}
			}

			if !setupRan {
				setupHints(ctx, g, out)
			}

			if !insideGitRepository(cfg.RepoRoot) {
				_, _ = fmt.Fprintf(out, "\nNote: %s is not a git repository, so answers that read history (owners, hotspots, bus-factor, impact over commits) are unavailable.\n", cfg.RepoRoot)
			}
			if !withIndex {
				ui.NextSteps(cfg.ProjectName)
			} else {
				ui.Header("GraphLume is ready!")
				ui.NextSteps(cfg.ProjectName)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "recreate the index if it already exists")
	cmd.Flags().BoolVar(&withAll, "all", false, "run full onboarding wizard (guided compiler indexer setup, index, resolve, register agents, inject rules)")
	cmd.Flags().BoolVar(&withIndex, "index", false, "run initial indexing and symbol resolution passes")
	cmd.Flags().BoolVar(&withAgents, "agents", false, "auto-register MCP server with detected coding agents")
	cmd.Flags().BoolVar(&withRules, "rules", false, "inject agent guidance into AGENTS.md / .cursorrules")
	cmd.Flags().BoolVar(&withIndexers, "indexers", false, "walk through the compiler indexers detected languages need, before indexing (implied by --all); says what each download is and asks first")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "without a terminal, accept the default-yes offers: only the small pinned, checksum-verified indexers; never a toolchain or a repository build")
	cmd.Flags().BoolVar(&allowBuild, "allow-repo-build", false, "let setup generate compile_commands.json by running the repository's own build, and let resolve run indexers that execute code the repository chooses")
	cmd.Flags().BoolVar(&localOnly, "local", false, "write only project-scoped configs (.mcp.json, .cursor/mcp.json, ...)")
	cmd.Flags().BoolVar(&localOnly, "project-scope", false, "alias for --local")
	cmd.Flags().StringVar(&profileFlag, "profile", "compact", "MCP profile to configure for agents (compact, minimal, standard, full)")
	return cmd
}

// setAsideIfCorrupt moves a damaged index, with its WAL and shared-memory
// files, to a dated backup beside it and returns the backup's path. An index
// that opens and passes SQLite's quick check is left alone ("" is returned).
func setAsideIfCorrupt(ctx context.Context, primary string) (string, error) {
	damaged := false
	db, err := store.Open(ctx, store.Options{Path: primary, ReadOnly: true})
	if err != nil {
		damaged = true
	} else {
		var verdict string
		if err := db.DB.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&verdict); err != nil || verdict != "ok" {
			damaged = true
		}
		_ = db.Close()
	}
	if !damaged {
		return "", nil
	}
	backup := primary + ".damaged-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(primary, backup); err != nil {
		return "", fmt.Errorf("move the damaged index aside: %w", err)
	}
	for _, ext := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(primary + ext); err == nil {
			_ = os.Rename(primary+ext, backup+ext)
		}
	}
	return backup, nil
}

// hasProjectFiles reports whether root holds anything besides what GraphLume
// and the agents it configures write there.
func hasProjectFiles(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return true
	}
	own := map[string]bool{".git": true, IndexDir: true, "AGENTS.md": true, "CLAUDE.md": true, ".mcp.json": true,
		".cursor": true, ".vscode": true, ".graphlume.yml": true, ".gitignore": true}
	for _, e := range entries {
		if !own[e.Name()] {
			return true
		}
	}
	return false
}

// insideGitRepository reports whether root or a directory above it has .git.
func insideGitRepository(root string) bool {
	for dir := root; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil { //nolint:gosec // G703: the project root and its parents, joined with a fixed name
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}
