package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/store"
)

type setupOptions struct {
	dryRun      bool
	noIndex     bool
	noRules     bool
	hooks       bool
	local       bool
	profile     string
	onlyClients []string
}

func newSetupCmd(g *Global) *cobra.Command {
	opts := setupOptions{
		profile: "standard",
	}
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "One-stop zero-config setup: initialize, register MCP in detected agents, and index",
		Long: `One-stop zero-config setup command for developers and teams.
1. Initializes GraphLume registry and schema (.graphlume/index.db).
2. Auto-detects installed coding agents (Claude Code, Cursor, Windsurf, VS Code, Zed, Copilot, etc.) and registers the GraphLume MCP server idempotently.
3. Injects canonical agent guidance into AGENTS.md, CLAUDE.md, and .cursorrules.
4. Performs initial codebase indexing and reference resolution so the knowledge graph is queryable immediately.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetup(cmd, g, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "show actions without modifying files or config")
	cmd.Flags().BoolVar(&opts.noIndex, "no-index", false, "skip initial indexing and resolution")
	cmd.Flags().BoolVar(&opts.noRules, "no-rules", false, "skip injecting AGENTS.md / CLAUDE.md guidance")
	cmd.Flags().BoolVar(&opts.hooks, "hooks", false,
		"also add a Claude Code SessionStart hook (.claude/settings.json) that puts the GraphLume guidance in front of the agent at every session start and after compaction")
	cmd.Flags().BoolVar(&opts.local, "local", false, "write only project-scoped configs (.mcp.json, .cursor/mcp.json, ...)")
	cmd.Flags().BoolVar(&opts.local, "project-scope", false, "alias for --local")
	cmd.Flags().StringVar(&opts.profile, "profile", "standard", "MCP tool surface profile: minimal, standard, compact, frontdoor, full")
	cmd.Flags().StringSliceVar(&opts.onlyClients, "client", nil, "comma-separated client names to configure (e.g. 'Cursor,Claude Code')")
	return cmd
}

func runSetup(cmd *cobra.Command, g *Global, opts setupOptions) error {
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
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	dir := filepath.Join(cfg.RepoRoot, IndexDir)
	primary := filepath.Join(dir, IndexFile)

	var (
		version int
		applied []int
	)

	if !opts.dryRun {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		_ = writeIfAbsent(filepath.Join(dir, ".gitignore"), "*\n")

		db, err := store.Open(ctx, store.Options{Path: primary})
		if err != nil {
			return fmt.Errorf("open index db: %w", err)
		}
		_ = store.VerifyFTS5(ctx, db.DB)
		applied, err = store.Migrate(ctx, db.DB, time.Now().Unix)
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("apply schema: %w", err)
		}
		version, err = store.SchemaVersion(ctx, db.DB)
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("read schema version: %w", err)
		}
		_ = db.Close()
	}

	var installSummary []installResult
	home, err := os.UserHomeDir()
	if err == nil {
		serverArgs := []string{"--project", cfg.RepoRoot, "serve"}
		p := config.MCPProfile(opts.profile)
		if p.Valid() {
			serverArgs = append(serverArgs, "--profile", opts.profile)
		} else {
			serverArgs = append(serverArgs, "--profile", "standard")
		}
		serverArgs = append(serverArgs, "--audit-log", filepath.Join(cfg.RepoRoot, IndexDir, "audit.jsonl"))
		spec := agentServerSpec{
			Name:    agentMCPName,
			Command: "graphlume",
			Args:    serverArgs,
		}
		results, err := installClients(home, cfg.RepoRoot, spec, false, opts.dryRun, opts.local, opts.onlyClients)
		if err == nil {
			installSummary = results
		}
	}

	rulesInjected := false
	if !opts.noRules {
		if err := injectAgentGuidance(cmd, cfg.RepoRoot, opts.dryRun, false); err != nil {
			g.Log().Warn("inject agent guidance", "error", err)
		} else {
			rulesInjected = true
		}
	}

	var hookPath string
	hookChanged := false
	if opts.hooks {
		p, changed, err := installSessionHook(cfg.RepoRoot, opts.dryRun)
		if err != nil {
			return fmt.Errorf("session hook: %w", err)
		}
		hookPath, hookChanged = p, changed
	}

	if !opts.noIndex && !opts.dryRun {
		idxCmd := newIndexCmd(g)
		idxCmd.SetContext(ctx)
		idxCmd.SetOut(out)
		idxCmd.SetErr(errOut)
		if err := idxCmd.RunE(idxCmd, nil); err != nil {
			return fmt.Errorf("initial index: %w", err)
		}

		resCmd := newResolveCmd(g)
		resCmd.SetContext(ctx)
		resCmd.SetOut(out)
		resCmd.SetErr(errOut)
		if err := resCmd.RunE(resCmd, nil); err != nil {
			return fmt.Errorf("initial resolve: %w", err)
		}
	}

	if g.JSON {
		return emitJSON(out, map[string]any{
			"project":            cfg.ProjectName,
			"index_path":         primary,
			"schema_version":     version,
			"migrations_applied": applied,
			"clients":            installSummary,
			"rules_injected":     rulesInjected,
			"session_hook":       hookPath,
			"session_hook_added": hookChanged,
			"dry_run":            opts.dryRun,
		})
	}

	if opts.dryRun {
		_, _ = fmt.Fprintf(out, "[dry-run] Setup plan for %q:\n", cfg.ProjectName)
		_, _ = fmt.Fprintf(out, "  - Would initialize %s (schema v%d)\n", primary, version)
	} else {
		_, _ = fmt.Fprintf(out, "Initialized GraphLume for %q (schema v%d, %d %s applied)\n", cfg.ProjectName, version, len(applied),
			plural(len(applied), "migration", "migrations"))
	}

	if len(installSummary) > 0 {
		written := printInstallResults(out, installSummary, opts.dryRun)
		if written > 0 {
			verb := "Registered"
			if opts.dryRun {
				verb = "Would register"
			}
			_, _ = fmt.Fprintf(out, "%s GraphLume MCP server in detected coding agents.\n", verb)
		}
	}

	if rulesInjected {
		verb := "Injected"
		if opts.dryRun {
			verb = "Would inject"
		}
		_, _ = fmt.Fprintf(out, "%s code intelligence guidance into AGENTS.md and agent instruction files.\n", verb)
	}

	if opts.hooks {
		switch {
		case !hookChanged:
			_, _ = fmt.Fprintf(out, "SessionStart hook already present in %s.\n", hookPath)
		case opts.dryRun:
			_, _ = fmt.Fprintf(out, "Would add a SessionStart hook to %s.\n", hookPath)
		default:
			_, _ = fmt.Fprintf(out, "Added a SessionStart hook to %s.\n", hookPath)
		}
	}

	if !opts.dryRun {
		_, _ = fmt.Fprintln(out, "\nGraphLume is ready! Coding agents can now query your codebase via MCP.")
		_, _ = fmt.Fprintln(out, "To verify the installation, run: graphlume doctor")
	}

	return nil
}
