package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sutrakam/graphlume/internal/acquire/fs"
	"github.com/sutrakam/graphlume/internal/pipeline"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/watch"
	"github.com/sutrakam/graphlume/pkg/model"

	"github.com/spf13/cobra"
)

func newWatchCmd(g *Global) *cobra.Command {
	var debounce time.Duration

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Keep the index current as files change",
		Long: `Watches the repository and updates the index incrementally as you edit.

Runs until interrupted. Editors produce bursts of filesystem events for a
single save, so changes are debounced and coalesced before any reparsing
happens.

It holds one watch per directory and none per file. On Linux and Windows the
kernel reports every change through those. On macOS the kernel reports a file
created, deleted or renamed, which is how most editors save, at once; a file
rewritten in place is found by a sweep of file sizes and times every two
seconds or so (longer on a tree that takes long to sweep), so such an edit can
take that long to be noticed.

The index stays queryable throughout: each file is updated in its own
transaction, so an agent querying concurrently always sees a file either
entirely before or entirely after a change.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWatch(cmd, g, debounce)
		},
	}

	cmd.Flags().DurationVar(&debounce, "debounce", 300*time.Millisecond,
		"quiet period before reacting to a burst of edits")
	return cmd
}

func runWatch(cmd *cobra.Command, g *Global, debounce time.Duration) error {
	return watchLoop(cmd.Context(), g, debounce, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// watchLoop is the watch command's body, also run inside `serve --watch`.
// out receives what a person at a terminal reads and errOut the start-up
// progress; serve passes io.Discard for both, because on stdio its stdout is
// the MCP transport and any stray byte there corrupts the protocol.
func watchLoop(ctx context.Context, g *Global, debounce time.Duration, out, errOut io.Writer) error {
	cfg := g.Config()
	log := g.Log()

	dbPath := g.DBPath()
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no index at %s — run 'graphlume init' first", dbPath)
	}

	db, err := openWritable(ctx, g, dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	reg := registry.New(db)
	current, err := reg.CurrentRun(ctx)
	if err != nil {
		if errors.Is(err, registry.ErrNoCommittedRun) {
			return fmt.Errorf("no committed index — run 'graphlume index' first")
		}
		return fmt.Errorf("read current index revision: %w", err)
	}

	walker, err := fs.NewWalker(cfg.RepoRoot, cfg)
	if err != nil {
		return fmt.Errorf("prepare discovery: %w", err)
	}

	// Watch first, reconcile second.
	//
	// The other order loses edits in the gap between the two:
	// the reconcile runs, then registering the tree takes 17
	// seconds to over a minute on a 7,100-file repository, and a file saved in
	// that time is neither in the reconcile that has already finished nor
	// seen by a watch that is not yet placed. Registering first means every
	// directory is covered from some moment during start-up, and the
	// reconcile that follows reads the tree as it is after all of them are.
	//
	// The context goes in so Ctrl-C during start-up stops it between two
	// directories instead of after the last one, and progress is printed so
	// that a slow start says it is still starting.
	started := time.Now()
	progress := func(dirs int) {
		if !g.JSON && !g.Quiet {
			fmt.Fprintf(errOut, "watching %d directories...\n", dirs)
		}
	}
	w, err := watch.New(cfg.RepoRoot, watch.Options{
		Ignore:   walker.IgnorePredicate(),
		Debounce: debounce,
		Context:  ctx,
		Progress: progress,
	})
	if err != nil {
		if ctx.Err() != nil {
			// Interrupted while starting. Nothing was changed, so there is
			// nothing to report beyond having stopped.
			if !g.JSON {
				_, _ = fmt.Fprintln(out, "\nStopped before watching began.")
			}
			return nil
		}
		return fmt.Errorf("start watcher: %w", err)
	}
	defer func() { _ = w.Close() }()

	// Drain errors in background so a long lockedRefresh or lock wait never blocks
	// the watcher loop when w.Errors() buffer fills.
	go func() {
		for err := range w.Errors() {
			log.Warn("watcher error", "error", err)
		}
	}()

	// Files change while the watcher is not running (a branch switch, a pull,
	// an editor save between sessions) and while it was starting. Starting
	// from a stale index would leave those edits invisible until the affected
	// file happened to be touched again.
	lockPath := filepath.Join(filepath.Dir(dbPath), "reindex.lock")
	if stats, err := lockedRefresh(ctx, lockPath, reg, current, walker, cfg.RepoRoot, g.compactPolicy()); err != nil {
		if ctx.Err() != nil {
			if !g.JSON {
				_, _ = fmt.Fprintln(out, "\nStopped before watching began.")
			}
			return nil
		}
		return fmt.Errorf("initial reconcile: %w", err)
	} else if changed := stats.Added + stats.Modified + stats.Deleted; changed > 0 {
		log.Info("reconciled changes made while not watching",
			"added", stats.Added, "modified", stats.Modified, "deleted", stats.Deleted)
	}

	if !g.JSON {
		fmt.Fprintf(out,
			"Watching %s: %d directories (revision %d), ready in %s. Press Ctrl-C to stop.\n",
			cfg.ProjectName, w.WatchedDirs(), current.ID, time.Since(started).Round(10*time.Millisecond))
	}

	var stoppedPrinted bool
	printStopped := func() {
		if !g.JSON && !stoppedPrinted {
			stoppedPrinted = true
			_, _ = fmt.Fprintln(out, "\nStopped.")
		}
	}
	defer func() {
		if ctx.Err() != nil {
			printStopped()
		}
	}()

	pendingPath := filepath.Join(filepath.Dir(lockPath), "reindex.pending")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// SIGINT/SIGTERM. Nothing to flush: every incremental pass has
			// already committed per file, so stopping here cannot lose or
			// corrupt work.
			printStopped()
			return nil

		case <-ticker.C:
			if _, err := os.Stat(pendingPath); err == nil {
				stats, err := lockedRefresh(ctx, lockPath, reg, current, walker, cfg.RepoRoot, g.compactPolicy())
				if err == nil {
					_ = os.Remove(pendingPath)
					changed := stats.Added + stats.Modified + stats.Deleted
					if changed > 0 {
						log.Info("index updated from pending reindex trigger",
							"added", stats.Added,
							"modified", stats.Modified,
							"deleted", stats.Deleted,
							"symbols", stats.Symbols)
					}
				}
			}

		case batch, ok := <-w.Changes():
			if !ok {
				if ctx.Err() != nil {
					printStopped()
				}
				return nil
			}
			if len(batch) == 0 {
				continue
			}
			start := time.Now()

			// Re-diff rather than trusting the event batch. Events say a path
			// was touched, not that its content differs — editors rewrite
			// files unchanged, and formatters run on save. Hashing keeps the
			// no-op case cheap and, more importantly, correct.
			stats, err := lockedRefresh(ctx, lockPath, reg, current, walker, cfg.RepoRoot, g.compactPolicy())
			if errors.Is(err, fs.ErrLocked) {
				// A hook's pass held the index for longer than the wait. The
				// next batch, or the server's own check, picks these edits up:
				// both compare the whole tree, not this batch.
				log.Warn("another index update held the lock; these edits will be picked up next", "events", len(batch))
				continue
			}
			if err != nil {
				if ctx.Err() != nil {
					printStopped()
					return nil
				}
				log.Error("incremental update failed; index left unchanged", "error", err)
				continue
			}

			changed := stats.Added + stats.Modified + stats.Deleted
			if changed == 0 {
				// Touched but semantically identical. Common enough that
				// logging it at info would be noise.
				log.Debug("filesystem events produced no content change", "events", len(batch))
				continue
			}
			log.Info("index updated",
				"added", stats.Added,
				"modified", stats.Modified,
				"deleted", stats.Deleted,
				"symbols", stats.Symbols,
				"took", time.Since(start).Round(time.Millisecond))
		}
	}
}

// watchLockWait is how long a watcher pass waits for another writer (a git
// hook's background pass, a server's edit check) to finish with the index.
const watchLockWait = 2 * time.Minute

// lockedRefresh is refreshIndex under the index's write lock, the one git
// hooks and the server's edit check take, so the three never write at once.
func lockedRefresh(ctx context.Context, lockPath string, reg *registry.Registry, current *model.IndexRun,
	walker *fs.Walker, root string, compact compactPolicy) (pipeline.IncrementalStats, error) {
	lock, err := lockWithin(ctx, lockPath, watchLockWait)
	if err != nil {
		return pipeline.IncrementalStats{}, err
	}
	defer func() { _ = lock.Unlock() }()

	// Re-read current run under the lock: an external 'graphlume index' may have
	// committed a new run and retired the previous one while we were watching.
	run, err := reg.CurrentRun(ctx)
	if err != nil {
		if errors.Is(err, registry.ErrNoCommittedRun) && current != nil {
			run = current
		} else {
			return pipeline.IncrementalStats{}, fmt.Errorf("read current index revision: %w", err)
		}
	}
	stats, reclaimed, err := refreshIndex(ctx, reg, run, walker, root, false, compact)
	if reclaimed > 0 && compact.log != nil {
		compact.log.Info("reclaimed free space in the index", "size", humanBytes(reclaimed))
	}
	if err == nil {
		_ = os.Remove(filepath.Join(filepath.Dir(lockPath), "reindex.pending"))
	}
	return stats, err
}
