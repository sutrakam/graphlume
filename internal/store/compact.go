package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Automatic compaction.
//
// An index is rewritten in place: a full index publishes a new revision and
// deletes the previous one, an incremental update replaces the rows of the
// files that changed, and resolve rewrites references and edges. SQLite keeps
// the pages those deletes free inside the file, for later writes to reuse, so
// without help the file only ever grows. A database this build creates uses
// auto_vacuum=INCREMENTAL (newDatabasePragma), which lets the free pages be
// moved to the end of the file and cut off a chunk at a time, while readers
// keep reading.
//
// Compact is called after each write that can free pages, and does nothing
// unless the free space is worth a write of its own: at least
// CompactMinFreeBytes AND at least CompactMinFreeFraction of the file. Below
// that the pages are cheaper to leave for the next write to reuse.

const (
	// CompactMinFreeBytes is the least free space Compact reclaims.
	CompactMinFreeBytes int64 = 64 << 20

	// CompactMinFreeFraction is the least share of the file that must be
	// free before Compact reclaims it.
	CompactMinFreeFraction = 0.20

	// compactChunkPages is how many pages one incremental_vacuum statement
	// frees. Each statement is its own short write transaction, so a reader
	// or a waiting writer is never held behind the whole reclaim.
	compactChunkPages = 2048

	// compactBudget bounds the time one Compact spends freeing pages. What
	// is left over stays free and is reclaimed after a later write, so a
	// watch session is never held up for long by a large reclaim.
	compactBudget = 3 * time.Second
)

// Auto-vacuum modes as `PRAGMA auto_vacuum` reports them.
const (
	AutoVacuumNone        = "none"
	AutoVacuumFull        = "full"
	AutoVacuumIncremental = "incremental"
)

// FreeSpace describes the pages of an index database that hold nothing.
type FreeSpace struct {
	// AutoVacuum is the database's mode: none, full or incremental.
	AutoVacuum string `json:"auto_vacuum"`
	PageSize   int64  `json:"page_size"`
	PageCount  int64  `json:"page_count"`
	FreePages  int64  `json:"free_pages"`
}

// FreeBytes is the space the free pages occupy.
func (f FreeSpace) FreeBytes() int64 { return f.FreePages * f.PageSize }

// FileBytes is the size of the database itself, without its -wal file.
func (f FreeSpace) FileBytes() int64 { return f.PageCount * f.PageSize }

// Reclaimable reports whether Compact can return free pages to the file
// system: the database was created with auto_vacuum=INCREMENTAL.
func (f FreeSpace) Reclaimable() bool { return f.AutoVacuum == AutoVacuumIncremental }

// WorthReclaiming reports whether the free space passes both thresholds.
func (f FreeSpace) WorthReclaiming() bool {
	return f.worth(CompactMinFreeBytes, CompactMinFreeFraction)
}

func (f FreeSpace) worth(minBytes int64, minFraction float64) bool {
	if f.FreeBytes() < minBytes || f.PageCount == 0 {
		return false
	}
	return float64(f.FreePages) >= minFraction*float64(f.PageCount)
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ReadFreeSpace reads the auto-vacuum mode and the page counts. It only
// reads, so it works on a read-only open.
func ReadFreeSpace(ctx context.Context, q rowQueryer) (FreeSpace, error) {
	var f FreeSpace
	var mode int
	for _, p := range []struct {
		pragma string
		dst    any
	}{
		{"PRAGMA auto_vacuum", &mode},
		{"PRAGMA page_size", &f.PageSize},
		{"PRAGMA page_count", &f.PageCount},
		{"PRAGMA freelist_count", &f.FreePages},
	} {
		if err := q.QueryRowContext(ctx, p.pragma).Scan(p.dst); err != nil {
			return FreeSpace{}, fmt.Errorf("%s: %w", p.pragma, err)
		}
	}
	switch mode {
	case 1:
		f.AutoVacuum = AutoVacuumFull
	case 2:
		f.AutoVacuum = AutoVacuumIncremental
	default:
		f.AutoVacuum = AutoVacuumNone
	}
	return f, nil
}

// CompactResult says what one Compact did.
type CompactResult struct {
	// ReclaimedBytes is the space returned to the file system; zero when
	// Compact found nothing worth reclaiming.
	ReclaimedBytes int64
	// Checkpoint is the write-ahead log checkpoint that followed: "truncate",
	// "passive", or "" when none ran.
	Checkpoint string
}

// Compact returns the index's free pages to the file system when they are
// worth it (see WorthReclaiming), then checkpoints the write-ahead log so the
// -wal file does not keep the size the main file gave up.
//
// It does nothing, and returns no error, for a database without
// auto_vacuum=INCREMENTAL (one created by hand, or by another tool) and for
// an in-memory database.
//
// The caller must hold the index's write lock, as every writer does: Compact
// writes. Readers are never blocked: each chunk is a short transaction under
// WAL, and the checkpoint does not wait for a reader (see checkpointWAL).
func Compact(ctx context.Context, db *DB) (CompactResult, error) {
	return compact(ctx, db, CompactMinFreeBytes, CompactMinFreeFraction)
}

func compact(ctx context.Context, db *DB, minBytes int64, minFraction float64) (CompactResult, error) {
	var res CompactResult
	if db == nil || db.path == "" {
		return res, nil
	}
	before, err := ReadFreeSpace(ctx, db)
	if err != nil {
		return res, err
	}
	if !before.Reclaimable() || !before.worth(minBytes, minFraction) {
		return res, nil
	}

	deadline := time.Now().Add(compactBudget)
	remaining := before.FreePages
	for remaining > 0 && time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			break
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", compactChunkPages)); err != nil {
			return res, fmt.Errorf("reclaim free pages: %w", err)
		}
		var n int64
		if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&n); err != nil {
			return res, fmt.Errorf("reclaim free pages: %w", err)
		}
		if n >= remaining {
			break // nothing moved; never spin
		}
		remaining = n
	}
	res.ReclaimedBytes = (before.FreePages - remaining) * before.PageSize
	if res.ReclaimedBytes > 0 {
		res.Checkpoint, err = checkpointWAL(context.WithoutCancel(ctx), db)
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// checkpointWAL copies the write-ahead log into the main file and empties it.
//
// TRUNCATE also cuts the -wal file to zero bytes, but it needs every reader to
// be past the log's end. With the connection's busy timeout in force a
// reader (the MCP server, the portal) would hold the writer for up to that
// timeout, so it runs with the timeout at zero: when a reader is in the way it
// reports busy at once, and a PASSIVE checkpoint copies what it can instead.
// The log is then reused from the start by later writes rather than growing.
func checkpointWAL(ctx context.Context, db *DB) (string, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		return "", err
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", db.busyMillis))
	}()

	var busy, logPages, done int
	err = conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &done)
	if err == nil && busy == 0 {
		return "truncate", nil
	}
	if err != nil && !isBusy(err) {
		return "", fmt.Errorf("checkpoint: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logPages, &done); err != nil {
		return "", fmt.Errorf("checkpoint: %w", err)
	}
	return "passive", nil
}

func isBusy(err error) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		// SQLITE_BUSY and SQLITE_LOCKED, with or without an extended code.
		c := coded.Code() & 0xff
		return c == 5 || c == 6
	}
	return false
}
