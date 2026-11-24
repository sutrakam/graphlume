package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/acquire/workspace"
	"github.com/sutrakam/graphlume/internal/multirepo"
)

// newWorkspaceCmd creates the `graphlume workspace` parent command.
func newWorkspaceCmd(g *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Inspect multi-project workspaces and global registry",
		Long: `Inspects monorepo and polyrepo workspaces, detects service partitions,
lists projects registered across the local machine, and records and queries the
contracts that link services to each other ('sync' and 'links').`,
	}

	cmd.AddCommand(newWorkspaceDetectCmd(g))
	cmd.AddCommand(newWorkspaceListCmd(g))
	cmd.AddCommand(newWorkspaceSyncCmd(g))
	cmd.AddCommand(newWorkspaceLinksCmd(g))
	// Persistent so `sync` and `links` cannot disagree about which database a
	// workspace lives in.
	cmd.PersistentFlags().String("workspace-db", "",
		"the workspace database (default <workspace root>/.graphlume/workspace.db)")
	return cmd
}

func newWorkspaceDetectCmd(g *Global) *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Detect whether the current repository root is a partitioned multi-service workspace",
		Long: `Scans the current project root to classify topology:
  - single_repo: one codebase with standard layout
  - explicit_manifest: defined via .graphlume/workspace.yaml
  - polyrepo: multiple child directories containing independent .git repositories
  - monorepo: multiple services detected via go.work, package.json, or service folders`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := g.Config().RepoRoot
			topo, err := workspace.DetectWorkspace(root)
			if err != nil {
				return fmt.Errorf("detect workspace %s: %w", root, err)
			}

			out := cmd.OutOrStdout()
			if g.JSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(topo)
			}

			if !topo.IsWorkspace {
				_, _ = fmt.Fprintf(out, "Topology: Single repository\nRoot: %s\n", topo.Root)
				return nil
			}

			_, _ = fmt.Fprintf(out, "Topology: Partitioned Workspace (%s)\n", topo.Type)
			_, _ = fmt.Fprintf(out, "Root:     %s\n\n", topo.Root)
			_, _ = fmt.Fprintf(out, "Detected %d service partition(s):\n", len(topo.Services))

			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "  SERVICE\tROLE\tPATH\tMANIFEST")
			for _, s := range topo.Services {
				manifest := s.Manifest
				if manifest == "" {
					manifest = "—"
				}
				_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", s.ID, s.Role, s.RelPath, manifest)
			}
			return w.Flush()
		},
	}
}

func newWorkspaceListCmd(g *Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all repositories registered in ~/.graphlume/registry.json",
		Long:  `Displays all projects currently tracked in the global control plane.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve user home: %w", err)
			}

			reg, err := multirepo.LoadGlobalRegistry(home)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if g.JSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(reg)
			}

			if len(reg.Projects) == 0 {
				_, _ = fmt.Fprintln(out, "No projects registered in global registry (~/.graphlume/registry.json).")
				_, _ = fmt.Fprintln(out, "Run 'graphlume init' or 'graphlume setup' in a project to register it.")
				return nil
			}

			_, _ = fmt.Fprintf(out, "Registered Projects (%d):\n\n", len(reg.Projects))
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "  ID\tMODE\tSTATUS\tROOT PATH")
			for _, p := range reg.Projects {
				status := "indexed"
				if p.LastIndexedAt == "" {
					status = "not indexed"
				}
				cleanPath := p.RootPath
				if rel, err := filepath.Rel(home, p.RootPath); err == nil && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, "..") {
					cleanPath = "~/" + rel
				}
				_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", p.ID, p.Mode, status, cleanPath)
			}
			return w.Flush()
		},
	}
}
