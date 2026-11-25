package cli

import (
	"github.com/spf13/cobra"
)

// newUninstallCmd is the one command a user needs to remove GraphLume from a
// machine. It is `clean --all` under the name people look for, with the same
// confirmation: it asks on a terminal, and without one it refuses unless
// --force is given.
func newUninstallCmd(g *Global) *cobra.Command {
	var self, force, dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove GraphLume from this machine (same as 'clean --all')",
		Long: `Removes GraphLume from this machine:
  - stops running graphlume processes (serve, watch)
  - unregisters the MCP server from every coding agent it was added to
  - removes git hooks, injected guidance and .graphlume/ from every registered project
  - removes ~/.graphlume (the project registry and downloaded indexers)

The binary itself stays unless --self is given. A binary installed by
Homebrew, Scoop or WinGet is left for that package manager to remove.

Preview everything first with --dry-run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clean := newCleanCmd(g)
			clean.SetContext(cmd.Context())
			clean.SetOut(cmd.OutOrStdout())
			clean.SetErr(cmd.ErrOrStderr())
			clean.SetIn(cmd.InOrStdin())
			args := []string{"--all"}
			if self {
				args = append(args, "--self")
			}
			if force {
				args = append(args, "--force")
			}
			if dryRun {
				args = append(args, "--dry-run")
			}
			if err := clean.ParseFlags(args); err != nil {
				return err
			}
			return clean.RunE(clean, nil)
		},
	}
	cmd.Flags().BoolVar(&self, "self", false, "also remove the graphlume binary")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "do not ask for confirmation (required without a terminal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed without changing anything")
	return cmd
}
