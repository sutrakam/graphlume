package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/multirepo"
)

// projectIndexPath is the index file 'graphlume index' writes for the project
// at root: .graphlume/index.db. Global.DBPath and the estate commands share it
// so a member is read from the file it was written to.
func projectIndexPath(root string) string {
	return filepath.Join(root, IndexDir, IndexFile)
}

// memberIndexFile is a member's repository root and the index file indexing it
// writes. A member names its repository directory or that repository's
// .graphlume/index.db.
//
// Any other database path is refused. 'system index' indexes the repository at
// a root and the indexer writes <root>/.graphlume/index.db; for a member named
// by some other file (a copied index, ../shared/orders.db) the indexer would
// write one file and 'system' would go on reading the other, so the estate
// would report the old graph after a successful re-index.
func memberIndexFile(m multirepo.EstateMember) (root, dbPath string, err error) {
	clean := filepath.Clean(m.Index)
	if fi, statErr := os.Stat(clean); statErr == nil && fi.IsDir() {
		return clean, projectIndexPath(clean), nil
	}
	parent := filepath.Dir(clean)
	if filepath.Base(clean) == IndexFile && filepath.Base(parent) == IndexDir {
		return filepath.Dir(parent), clean, nil
	}
	if !strings.HasSuffix(clean, ".db") {
		return "", "", fmt.Errorf("index path %s does not exist; name the repository directory or its %s/%s", clean, IndexDir, IndexFile)
	}
	return "", "", fmt.Errorf("index path %s is not where 'graphlume index' writes; name the repository directory "+
		"or its %s/%s so the file indexed is the file the system graph reads", clean, IndexDir, IndexFile)
}

// newSystemIndexCmd indexes and resolves all member repositories in an estate sequentially.
func newSystemIndexCmd(g *Global) *cobra.Command {
	var (
		continueOnError bool
		skipResolve     bool
	)

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Index and resolve all member repositories in an estate sequentially",
		Long: `Indexes every member repository listed in an estate file or in --member flags.

Runs sequentially to respect the memory limit (never exceeding peak RAM) and ensure
all members have an up-to-date SQLite index and cross-file reference graph.

Example:
  graphlume system index --members ./estate.yml
  graphlume system index --member auth=./services/auth --member payments=./services/payments
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			members, err := systemMembers(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			total := len(members)

			_, _ = fmt.Fprintf(out, "Indexing %d estate member repositories sequentially...\n\n", total)

			var failed []string
			for i, m := range members {
				root, primary, err := memberIndexFile(m)
				if err != nil {
					if !continueOnError {
						return fmt.Errorf("member %q: %w", m.Name, err)
					}
					failed = append(failed, m.Name+": "+err.Error())
					continue
				}
				_, _ = fmt.Fprintf(out, "[%d/%d] Member %q (%s)...\n", i+1, total, m.Name, root)

				start := time.Now()
				// Ensure index directory exists and schema is initialized
				dir := filepath.Dir(primary)
				if _, err := os.Stat(primary); errors.Is(err, os.ErrNotExist) {
					if err := os.MkdirAll(dir, 0o750); err != nil {
						errMsg := fmt.Sprintf("create index directory: %v", err)
						if !continueOnError {
							return fmt.Errorf("member %q: %s", m.Name, errMsg)
						}
						failed = append(failed, m.Name+": "+errMsg)
						continue
					}
					db, err := openWritable(ctx, g, primary)
					if err != nil {
						errMsg := fmt.Sprintf("initialize database: %v", err)
						if !continueOnError {
							return fmt.Errorf("member %q: %s", m.Name, errMsg)
						}
						failed = append(failed, m.Name+": "+errMsg)
						continue
					}
					_ = db.Close()
				}

				memberGlobal := &Global{
					Project: root,
					Quiet:   g.Quiet,
					Verbose: g.Verbose,
					JSON:    g.JSON,
				}
				if err := memberGlobal.setup(cmd); err != nil {
					if !continueOnError {
						return fmt.Errorf("member %q: setup: %w", m.Name, err)
					}
					failed = append(failed, m.Name+": "+err.Error())
					continue
				}

				idxCmd := newIndexCmd(memberGlobal)
				idxCmd.SetContext(ctx)
				idxCmd.SetOut(out)
				idxCmd.SetErr(cmd.ErrOrStderr())
				if err := idxCmd.RunE(idxCmd, nil); err != nil {
					if !continueOnError {
						return fmt.Errorf("member %q: index: %w", m.Name, err)
					}
					failed = append(failed, m.Name+": index: "+err.Error())
					continue
				}

				if !skipResolve {
					resCmd := newResolveCmd(memberGlobal)
					resCmd.SetContext(ctx)
					resCmd.SetOut(out)
					resCmd.SetErr(cmd.ErrOrStderr())
					if err := resCmd.RunE(resCmd, nil); err != nil {
						if !continueOnError {
							return fmt.Errorf("member %q: resolve: %w", m.Name, err)
						}
						failed = append(failed, m.Name+": resolve: "+err.Error())
						continue
					}
				}

				_, _ = fmt.Fprintf(out, "✓ [%d/%d] Member %q finished in %s\n\n", i+1, total, m.Name, time.Since(start).Round(time.Millisecond))
			}

			if len(failed) > 0 {
				_, _ = fmt.Fprintf(out, "Completed with %d error(s):\n", len(failed))
				for _, f := range failed {
					_, _ = fmt.Fprintf(out, "  - %s\n", f)
				}
				return fmt.Errorf("system index: %d member(s) failed", len(failed))
			}

			_, _ = fmt.Fprintf(out, "All %d estate member repositories indexed successfully.\n", total)
			return nil
		},
	}

	cmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue indexing remaining members if one member fails")
	cmd.Flags().BoolVar(&skipResolve, "skip-resolve", false, "skip reference resolution pass")
	return cmd
}
