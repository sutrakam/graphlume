package store

import (
	"context"
	"database/sql"
)

// Execer runs a statement that returns no rows.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Queryer runs a statement that returns rows.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Exec runs a raw statement. It exists for one-off scripts; application code
// should add a method to Store instead.
func Exec(ctx context.Context, db Execer, query string, args ...any) (sql.Result, error) {
	return db.ExecContext(ctx, query, args...)
}

// Query runs a raw query. See Exec.
func Query(ctx context.Context, db Queryer, query string, args ...any) (*sql.Rows, error) {
	return db.QueryContext(ctx, query, args...)
}
