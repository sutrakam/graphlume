package cli

import (
	stdctx "context"
	"database/sql"
	"time"

	"github.com/sutrakam/graphlume/internal/store"
)

// openWritable opens the index for a command that may modify it, bringing the
// schema up to date first.
//
// Running migrations only in `graphlume init` would leave every other command opening
// the database and using it as-is, so upgrading the binary would leave existing users
// on their old schema forever: new tables would be missing and new indexes would
// never be created. The failure would surface far from its cause — a raw
// "no such table: table_accesses" from inside a query — so the schema is
// asserted at the single point where the database is opened.
//
// Migrate is idempotent and checksum-verified, so this is a no-op once the
// index is current.
func openWritable(ctx stdctx.Context, g *Global, path string) (*store.DB, error) {
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, err
	}
	applied, err := store.Migrate(ctx, db.DB, time.Now().Unix)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if len(applied) > 0 {
		g.Log().Info("upgraded index schema", "migrations", applied)
	}
	return db, nil
}

// openReadOnly opens the index for a command that only queries it.
//
// Read-only commands deliberately do not migrate: a query must never mutate
// the user's index, and the database is opened read-only so it could not
// anyway. They report the mismatch and name the fix instead.
func openReadOnly(ctx stdctx.Context, path string) (*store.DB, error) {
	db, err := store.Open(ctx, store.Options{Path: path, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if err := store.CheckVersion(ctx, db.DB); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// openPlaceholder creates an empty, migrated schema at path, for a server
// that has to exist before the project's index does. See serve_noindex.go.
//
// It is here, and not beside its one caller, because this file is the only
// place in the package allowed to open the store: the schema version is
// asserted at every door or it is asserted at none. The path is always in a
// temporary directory the caller removes, never in the project. Unlike
// openWritable it logs nothing, since "upgraded index schema" would describe
// an index the user does not have.
func openPlaceholder(ctx stdctx.Context, path string) (*store.DB, error) {
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, err
	}
	if _, err := store.Migrate(ctx, db.DB, time.Now().Unix); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// stdSQLNamed is sql.Named, kept here so query commands bind a named
// parameter without importing database/sql themselves: this file is the one
// door to the store in this package.
func stdSQLNamed(name string, value any) any { return sql.Named(name, value) }

// openWritableCurrent opens the index for writing WITHOUT migrating it, and
// refuses one whose schema is not this build's.
//
// For a long-running process that writes on the user's behalf in the
// background (serve's edit check): upgrading the schema there would change
// the user's index in the middle of an agent's request, unannounced. The
// mismatch is returned for the caller to report, and 'graphlume migrate'
// stays the one place an upgrade happens.
func openWritableCurrent(ctx stdctx.Context, path string) (*store.DB, error) {
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, err
	}
	if err := store.CheckVersion(ctx, db.DB); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// openMigratedMemory opens a private in-memory database at this build's
// schema, the reference a downloaded index is compared against.
func openMigratedMemory(ctx stdctx.Context) (*store.DB, error) {
	db, err := store.Open(ctx, store.Options{})
	if err != nil {
		return nil, err
	}
	if _, err := store.Migrate(ctx, db.DB, time.Now().Unix); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// checkpointForBackup folds an index's write-ahead log into its main file so a
// copy or rename of that file holds the whole index. It skips the version
// check on purpose: the caller is setting aside an index it is about to
// replace, which may be from an older or newer build, and refusing to read it
// would leave its -wal behind. Best effort; a database that does not open is
// set aside as it is.
func checkpointForBackup(ctx stdctx.Context, path string) {
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return
	}
	_, _ = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	_ = db.Close()
}
