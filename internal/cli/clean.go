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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sutrakam/graphlume/internal/multirepo"
)

func newCleanCmd(g *Global) *cobra.Command {
	var (
		all       bool
		agents    bool
		processes bool
		hooks     bool
		self      bool
		force     bool
		dryRun    bool
		explain   bool
	)

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Clean GraphLume indexes, caches, processes, and agent configurations",
		Long: `Removes GraphLume artifacts, caches, background daemons, and agent integrations.

DEFAULT BEHAVIOR (no flags):
  Safely cleans the current repository:
  - Removes .graphlume/ databases, logs, and locks
  - Uninstalls git hooks (.git/hooks/post-commit, post-merge, post-checkout)
  - Strips injected guidance blocks from AGENTS.md, .cursorrules, CLAUDE.md, etc.
  - Unregisters the repository from ~/.graphlume/registry.json
  - Cleans local project-scoped MCP configs (.mcp.json, .cursor/mcp.json)

COMMAND SCOPES:
  --all, -a    Full machine purge: terminates running processes, unregisters
               from all coding agents, removes ~/.graphlume/, and cleans all
               registered projects across the system
  --agents     Unregister GraphLume MCP server from all detected coding agents
               (Cursor, Claude Desktop/Code, VS Code, Zed, Antigravity, Cline, Windsurf, etc.)
  --processes  Terminate all running graphlume background processes (serve, watch)
  --hooks      Uninstall git hooks from the current repository
  --self       Remove the graphlume executable binary itself from system PATH
  --dry-run    Preview every cleanup action without modifying files or killing processes
  --force, -f  Skip interactive confirmation prompt on full cleanup
  --explain    Display detailed architectural guide of all artifacts, files, and recovery steps

EXAMPLES:
  # Reset the current repository index and git hooks
  $ graphlume clean

  # Preview a full machine cleanup without making changes
  $ graphlume clean --all --dry-run

  # Unregister GraphLume from all AI coding agents without deleting indexes
  $ graphlume clean --agents

  # Terminate all running GraphLume background servers and watchers
  $ graphlume clean --processes

  # Completely eradicate GraphLume from your system including binary
  $ graphlume clean --all --self -f

  # Read in-depth architectural explanation of all GraphLume files
  $ graphlume clean --explain`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			if explain {
				printCleanExplanation(out)
				return nil
			}

			home, _ := os.UserHomeDir()
			root := g.Config().RepoRoot

			if all {
				agents = true
				processes = true
				hooks = true
			}

			isDefaultProjectClean := !all && !agents && !processes && !hooks && !self

			// A full cleanup with nobody at a terminal to confirm it (a script,
			// an agent's shell) must be asked for explicitly: skipping the
			// prompt silently would let `clean --all` purge everything unasked.
			if (all || self) && !force && !dryRun && !isInteractiveTerminal() {
				return errors.New("refusing a full cleanup without confirmation: no terminal to ask on. " +
					"Run it again with --force, or preview it with --dry-run")
			}
			// Interactive confirmation for destructive full cleanups when not forced
			if (all || self) && !force && !dryRun && isInteractiveTerminal() {
				target := "all registered projects, agent integrations, and global state"
				if self {
					target += ", AND remove the graphlume binary"
				}
				_, _ = fmt.Fprintf(out, "Warning: You are about to perform a full GraphLume cleanup (%s).\n", target)
				_, _ = fmt.Fprintf(out, "Are you sure you want to proceed? [y/N]: ")
				scanner := bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
					if ans != "y" && ans != "yes" {
						_, _ = fmt.Fprintln(out, "Cleanup aborted.")
						return nil
					}
				} else {
					_, _ = fmt.Fprintln(out, "Cleanup aborted.")
					return nil
				}
			}

			if dryRun {
				_, _ = fmt.Fprintln(out, "[DRY RUN] Previewing cleanup actions:")
			}

			// 1. Terminate running background processes
			if processes || all {
				pids, err := killGraphlumeProcesses(dryRun)
				if err != nil {
					g.Log().Warn("kill processes", "error", err)
				}
				if len(pids) > 0 {
					verb := "Terminated"
					if dryRun {
						verb = "Would terminate"
					}
					_, _ = fmt.Fprintf(out, "✓ %s %d running GraphLume process(es) (PIDs: %v)\n", verb, len(pids), pids)
				} else if processes {
					_, _ = fmt.Fprintln(out, "• No running GraphLume processes found.")
				}
			}

			// 2. Unregister from coding agents
			if agents || all {
				agentActions, err := cleanAgentConfigs(home, root, dryRun)
				if err != nil {
					g.Log().Warn("clean agents", "error", err)
				}
				if len(agentActions) > 0 {
					verb := "Unregistered"
					if dryRun {
						verb = "Would unregister"
					}
					_, _ = fmt.Fprintf(out, "✓ %s from %d coding agent configuration(s):\n", verb, len(agentActions))
					for _, act := range agentActions {
						_, _ = fmt.Fprintf(out, "    - %s\n", act)
					}
				} else if agents {
					_, _ = fmt.Fprintln(out, "• No coding agents were currently registered with GraphLume.")
				}
			}

			// 3. Clean current project (databases, git hooks, guidance blocks).
			// Only a real project: run from ~ or / the "project" would be the
			// home directory, whose .graphlume is the global state.
			if reason := projectCleanRefusal(root, home); reason != "" && (isDefaultProjectClean || hooks) && !all {
				return fmt.Errorf("not cleaning %s: %s", root, reason)
			} else if reason != "" && all {
				_, _ = fmt.Fprintf(out, "• Skipping the current directory (%s): %s.\n", root, reason)
			} else if isDefaultProjectClean || all || hooks {
				projActions, err := cleanCurrentProject(root, home, hooks || all || isDefaultProjectClean, dryRun)
				if err != nil {
					return fmt.Errorf("clean project %s: %w", root, err)
				}
				if len(projActions) > 0 {
					verb := "Cleaned"
					if dryRun {
						verb = "Would clean"
					}
					_, _ = fmt.Fprintf(out, "✓ %s project repository (%s):\n", verb, root)
					for _, act := range projActions {
						_, _ = fmt.Fprintf(out, "    - %s\n", act)
					}
				} else if isDefaultProjectClean {
					_, _ = fmt.Fprintf(out, "• Project %s is already clean (no .graphlume/ or git hooks found).\n", root)
				}
			}

			// 4. If --all is requested, also clean all other projects tracked in the global registry
			if all && home != "" {
				if reg, err := multirepo.LoadGlobalRegistry(home); err == nil {
					for _, proj := range reg.Projects {
						if proj.RootPath != "" && multirepo.CanonicalPath(proj.RootPath) != multirepo.CanonicalPath(root) &&
							projectCleanRefusal(proj.RootPath, home) == "" {
							pActions, _ := cleanCurrentProject(proj.RootPath, "", hooks, dryRun)
							if len(pActions) > 0 {
								verb := "Cleaned"
								if dryRun {
									verb = "Would clean"
								}
								_, _ = fmt.Fprintf(out, "✓ %s registered project (%s):\n", verb, proj.RootPath)
								for _, act := range pActions {
									_, _ = fmt.Fprintf(out, "    - %s\n", act)
								}
							}
						}
					}
				}
			}

			// 5. Remove global state (~/.graphlume)
			if all {
				globalActions, err := cleanGlobalState(home, dryRun)
				if err != nil {
					return fmt.Errorf("clean global state: %w", err)
				}
				if len(globalActions) > 0 {
					verb := "Removed"
					if dryRun {
						verb = "Would remove"
					}
					_, _ = fmt.Fprintf(out, "✓ %s global control plane & cache:\n", verb)
					for _, act := range globalActions {
						_, _ = fmt.Fprintf(out, "    - %s\n", act)
					}
				}
			}

			// 6. Self-removal of the binary
			if self {
				binaryPath, err := cleanBinarySelf(dryRun)
				if err != nil {
					return fmt.Errorf("self-removal: %w", err)
				}
				verb := "Removed"
				if dryRun {
					verb = "Would remove"
				}
				_, _ = fmt.Fprintf(out, "✓ %s GraphLume binary (%s)\n", verb, binaryPath)
			}

			if dryRun {
				_, _ = fmt.Fprintln(out, "\nDry run complete. No files or processes were modified.")
			} else {
				_, _ = fmt.Fprintln(out, "\nGraphLume cleanup complete.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "machine-wide cleanup: processes, agents, ~/.graphlume, and all registered projects")
	cmd.Flags().BoolVar(&agents, "agents", false, "unregister GraphLume MCP server from all detected coding agents")
	cmd.Flags().BoolVar(&processes, "processes", false, "terminate all running GraphLume background processes")
	cmd.Flags().BoolVar(&hooks, "hooks", false, "uninstall GraphLume git hooks from current repository")
	cmd.Flags().BoolVar(&self, "self", false, "remove the graphlume executable binary itself")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip confirmation prompt on full cleanup")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show actions without making changes")
	cmd.Flags().BoolVar(&explain, "explain", false, "display in-depth architectural guide of all artifacts and recovery steps")

	return cmd
}

// isInteractiveTerminal reports whether stdin is a terminal a person can
// answer on. A character-device test also accepted /dev/null.
func isInteractiveTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func killGraphlumeProcesses(dryRun bool) ([]int, error) {
	// Exact executable name only. `pgrep -f graphlume` matched any command
	// line containing the word: an editor, a shell or `go test` running in a
	// checkout named graphlume, a `tail` of its log. Those must never be
	// signalled.
	selfPID := os.Getpid()
	out, pgrepErr := exec.Command("pgrep", "-x", "graphlume").Output()
	if pgrepErr != nil {
		return nil, nil //nolint:nilerr // no match, or no pgrep: nothing to stop
	}
	var stopped []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid == selfPID || pid <= 1 {
			continue
		}
		if !isGraphlumeExecutable(pid) {
			continue
		}
		if !dryRun {
			if err := stopProcess(pid); err != nil {
				continue // not ours to signal (another user's), or already gone
			}
		}
		stopped = append(stopped, pid)
	}
	return stopped, nil
}

// isGraphlumeExecutable re-checks a pid's executable name, since pgrep's
// name can be truncated and the pid may have been reused since it listed it.
func isGraphlumeExecutable(pid int) bool {
	comm, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	name := filepath.Base(strings.TrimSpace(string(comm)))
	return name == "graphlume" || name == "graphlume.exe"
}

// stopProcess sends SIGTERM, waits up to two seconds for the process to exit,
// then sends SIGKILL. It waits in this process: a timer would never fire,
// because the CLI exits as soon as clean returns.
func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return proc.Kill() // Windows has no SIGTERM for another process
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sigErr := proc.Signal(syscall.Signal(0)); sigErr != nil {
			return nil //nolint:nilerr // the process has exited, which is what was waited for
		}
		time.Sleep(100 * time.Millisecond)
	}
	if isGraphlumeExecutable(pid) { // still the same program, not a reused pid
		return proc.Kill()
	}
	return nil
}

func cleanAgentConfigs(home, root string, dryRun bool) ([]string, error) {
	var actions []string
	clients := mcpClients(home, root)
	for _, c := range clients {
		if _, err := os.Stat(c.Path); err != nil {
			continue
		}
		data, err := os.ReadFile(c.Path)
		if err != nil {
			continue
		}
		unmerged, removed, err := unmergeClientConfig(c, data, agentMCPName)
		if err != nil || !removed {
			continue
		}
		if !dryRun {
			if err := writeFileAtomic(c.Path, unmerged); err != nil {
				return actions, fmt.Errorf("write %s: %w", c.Path, err)
			}
		}
		actions = append(actions, fmt.Sprintf("%s (%s)", c.Name, c.Path))
	}
	return actions, nil
}

func cleanCurrentProject(root, home string, doHooks, dryRun bool) ([]string, error) {
	var actions []string
	// 1. Remove database and index directories
	for _, dir := range []string{IndexDir} {
		p := filepath.Join(root, dir)
		if _, err := os.Stat(p); err == nil {
			if !dryRun {
				if err := os.RemoveAll(p); err != nil {
					return actions, fmt.Errorf("remove %s: %w", p, err)
				}
			}
			actions = append(actions, fmt.Sprintf("Removed %s directory", dir))
		}
	}
	// 2. Remove git hooks. A dry run only looks: removing them here was the
	// one change a preview made.
	if doHooks {
		had := false
		if dryRun {
			had = hasManagedHooks(root)
		} else {
			had, _ = UninstallCommitHook(root)
		}
		if had {
			actions = append(actions, "Uninstalled git hooks (.git/hooks/)")
		}
	}
	// 3. Clean guidance blocks from AGENTS.md, CLAUDE.md, .cursorrules, etc.
	guidanceActions, _ := cleanProjectGuidance(root, dryRun)
	actions = append(actions, guidanceActions...)

	// 4. Unregister from global control plane registry
	if home != "" {
		if reg, err := multirepo.LoadGlobalRegistry(home); err == nil {
			if projectRegistered(reg, root) {
				if !dryRun {
					_ = multirepo.UnregisterProject(context.Background(), home, root)
				}
				actions = append(actions, "Removed the project from ~/.graphlume/registry.json")
			}
		}
	}
	// 5. Clean project-scoped MCP configs (.mcp.json, .cursor/mcp.json)
	clients := mcpClients(home, root)
	for _, c := range clients {
		if isProjectScoped(c, root) {
			if _, err := os.Stat(c.Path); err == nil {
				data, err := os.ReadFile(c.Path)
				if err == nil {
					unmerged, removed, err := unmergeClientConfig(c, data, agentMCPName)
					if err == nil && removed {
						if !dryRun {
							if err := writeFileAtomic(c.Path, unmerged); err != nil {
								return actions, fmt.Errorf("write %s: %w", c.Path, err)
							}
						}
						actions = append(actions, fmt.Sprintf("Removed MCP registration from %s", c.Path))
					}
				}
			}
		}
	}
	return actions, nil
}

// projectRegistered reports whether the global registry holds root itself.
func projectRegistered(reg *multirepo.GlobalRegistry, root string) bool {
	canon := multirepo.CanonicalPath(root)
	for _, p := range reg.Projects {
		if multirepo.CanonicalPath(p.RootPath) == canon {
			return true
		}
	}
	return false
}

// hasManagedHooks reports whether any hook GraphLume manages carries its
// block, without changing anything.
func hasManagedHooks(root string) bool {
	dir, err := hooksDir(root)
	if err != nil {
		return false
	}
	for _, name := range managedHookNames {
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the project's own hooks directory
		if err != nil {
			continue
		}
		if _, had := stripBlock(string(data)); had {
			return true
		}
	}
	return false
}

func stripDelimited(content, openTag, closeTag string) (string, bool) {
	start := strings.Index(content, openTag)
	if start < 0 {
		return content, false
	}
	end := strings.Index(content, closeTag)
	if end < 0 || end < start {
		return content, false
	}
	end += len(closeTag)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	result := content[:start] + content[end:]
	return result, true
}

func cleanProjectGuidance(root string, dryRun bool) ([]string, error) {
	var actions []string
	candidates := append([]string{"AGENTS.md", "CONVENTIONS.md"}, guidanceUpdate...)
	for _, name := range candidates {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path) //nolint:gosec // G304: a fixed guidance file name under the project root
		if err != nil {
			continue
		}
		s := string(data)
		cleaned, hadAgent := stripDelimited(s, agentGuidanceOpen, agentGuidanceClose)
		cleaned, hadCli := stripDelimited(cleaned, cliGuidanceOpen, cliGuidanceClose)
		if !hadAgent && !hadCli {
			continue
		}
		if strings.TrimSpace(cleaned) == "" {
			if !dryRun {
				_ = os.Remove(path)
			}
			actions = append(actions, fmt.Sprintf("Removed %s (contained only GraphLume guidance)", name))
		} else {
			if !dryRun {
				// Only the block goes: the rest of the file keeps its own
				// whitespace, so a git-tracked file shows just that change.
				if err := writeFileAtomic(path, []byte(cleaned)); err != nil {
					return actions, fmt.Errorf("write %s: %w", path, err)
				}
			}
			actions = append(actions, fmt.Sprintf("Removed GraphLume guidance block from %s", name))
		}
	}
	return actions, nil
}

func cleanGlobalState(home string, dryRun bool) ([]string, error) {
	var actions []string
	for _, dir := range []string{multirepo.GlobalRegistryDir} {
		p := filepath.Join(home, dir)
		if _, err := os.Stat(p); err == nil {
			if !dryRun {
				if err := os.RemoveAll(p); err != nil {
					return actions, fmt.Errorf("remove %s: %w", p, err)
				}
			}
			actions = append(actions, fmt.Sprintf("Removed ~/%s", dir))
		}
	}
	return actions, nil
}

func cleanBinarySelf(dryRun bool) (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = resolved
	}
	if pm := packageManagerOf(execPath); pm != "" {
		return execPath, fmt.Errorf("%s was installed by %s; remove it with %s so the package manager's records stay consistent",
			execPath, pm, packageManagerUninstall[pm])
	}
	if !dryRun {
		if err := os.Remove(execPath); err != nil {
			return execPath, fmt.Errorf("remove binary %s: %w", execPath, err)
		}
	}
	return execPath, nil
}

func printCleanExplanation(w io.Writer) {
	_, _ = fmt.Fprintln(w, `================================================================================
               GRAPHLUME CLEANUP & UNINSTALLATION MANUAL
================================================================================

OVERVIEW
  GraphLume is designed to be completely cleanable from any system without
  leaving orphaned processes, locks, or corrupted tool configurations behind.
  All state is strictly partitioned into predictable locations:

1. BACKGROUND PROCESSES & DAEMONS
   - On-demand MCP servers spawned by coding agents (e.g. 'graphlume serve')
   - Live filesystem watchers (e.g. 'graphlume watch')
   Action: Terminated with SIGTERM and fallback kill.
   Target Flag: --processes (or included in --all)

2. PROJECT CODEBASE ARTIFACTS
   - Directory: <project-root>/.graphlume/
     Contains 'index.db' (SQLite AST & symbol graph), 'workspace.db' (federated
     contracts), 'reindex.lock' (write lock), and 'audit.jsonl' (query logs).
   - Git Hooks: .git/hooks/post-commit, post-merge, post-checkout
     Managed hook blocks tagged '# >>> graphlume post-commit >>>'.
   - Agent Guidance: AGENTS.md, CLAUDE.md, .cursorrules, .windsurfrules, etc.
     Injected guidance blocks tagged '<!-- graphlume:guidance -->'. If the file
     contained only GraphLume guidance, it is deleted cleanly; otherwise, other
     content is preserved byte-for-byte.
   - Project MCP Configs: .mcp.json, .cursor/mcp.json
   Target: Cleaned by default in current project (or all projects with --all).

3. GLOBAL MACHINE STATE & CACHE
   - Directory: ~/.graphlume/
     Contains 'registry.json' (global project catalog) and 'registry.lock'.
   Target Flag: --all

4. CODING AGENT MCP CONFIGURATIONS
   - Unregisters the "graphlume" entry across all 30+ supported AI tools:
     Cursor, Claude Desktop, Claude Code, VS Code, Zed, Antigravity,
     Cline, Windsurf, Roo, Continue, Junie, mcphub.nvim, and more.
   - Preserves all other MCP servers, tokens, and settings completely untouched.
   Target Flag: --agents (or included in --all)

5. EXECUTABLE BINARY (SELF-REMOVAL)
   - Removes the 'graphlume' binary from wherever it was installed in PATH
     (e.g., ~/.local/bin/graphlume or $(go env GOPATH)/bin/graphlume).
   Target Flag: --self

HOW TO RECOVER
  If you clean a repository or machine and wish to re-enable GraphLume later:
  1. Re-install binary: go install github.com/sutrakam/graphlume/cmd/graphlume@latest
  2. In your project: graphlume init --all
================================================================================`)
}

// projectCleanRefusal says why root is not a project clean may remove things
// from, or "" when it is. The home directory, the filesystem root and their
// ancestors are never projects: ~/.graphlume is the global state, and the
// agent configs under ~ are global ones.
func projectCleanRefusal(root, home string) string {
	if strings.TrimSpace(root) == "" {
		return "no project directory"
	}
	r := multirepo.CanonicalPath(root)
	if r == string(filepath.Separator) || filepath.Dir(r) == r {
		return "it is the filesystem root"
	}
	if home != "" {
		h := multirepo.CanonicalPath(home)
		if r == h {
			return "it is your home directory, whose .graphlume is GraphLume's global state; use --all for a full cleanup"
		}
		if strings.HasPrefix(h, r+string(filepath.Separator)) {
			return "it contains your home directory"
		}
	}
	for _, marker := range []string{IndexDir, ".git"} {
		if _, err := os.Stat(filepath.Join(r, marker)); err == nil {
			return ""
		}
	}
	return "it has no .graphlume/ and is not a git repository, so there is nothing of a project to clean"
}

// writeFileAtomic replaces path's content via a temporary file and a rename
// in the same directory, keeping path's mode. Agent configs are rewritten by
// their own editors too (Claude Code writes ~/.claude.json while it runs); a
// truncate-and-write could leave either side reading half a file.
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".graphlume-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// packageManagerOf names the package manager that owns an installed binary,
// from where it lives, or "" for a direct install.
func packageManagerOf(path string) string {
	p := filepath.ToSlash(path)
	switch {
	case strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/") || strings.Contains(p, "/linuxbrew/"):
		return "Homebrew"
	case strings.Contains(strings.ToLower(p), "/scoop/apps/"):
		return "Scoop"
	case strings.Contains(strings.ToLower(p), "/winget/packages/"):
		return "WinGet"
	}
	return ""
}

var packageManagerUninstall = map[string]string{
	"Homebrew": "'brew uninstall graphlume'",
	"Scoop":    "'scoop uninstall graphlume'",
	"WinGet":   "'winget uninstall graphlume'",
}
