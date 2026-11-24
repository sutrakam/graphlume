// Package ledger is the double-entry book of record. Every captured payment
// becomes one debit and one credit.
package ledger

import (
	"context"
	"database/sql"

	core "example.com/payments-platform/libs/payments-core"
)

// DB is the part of *sql.DB the ledger uses.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Ledger writes and reads ledger_entries.
type Ledger struct {
	db DB
}

// New returns a Ledger over db.
func New(db DB) *Ledger { return &Ledger{db: db} }

// Post books a captured payment: debit receivables, credit revenue.
func (l *Ledger) Post(ctx context.Context, p core.Payment) error {
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO ledger_entries (payment_id, account, direction, amount_cents, currency)
		 VALUES ($1, 'receivables', 'debit', $2, $3), ($1, 'revenue', 'credit', $2, $3)`,
		p.ID, p.Amount.Cents, p.Amount.Currency)
	return err
}

// Reverse books the opposite entries for a refunded payment. A payment with an
// open fraud flag is never reversed here; it goes to manual review.
func (l *Ledger) Reverse(ctx context.Context, p core.Payment) error {
	frozen, err := l.Frozen(ctx, p.ID)
	if err != nil {
		return err
	}
	if frozen {
		return ErrFrozen
	}
	_, err = l.db.ExecContext(ctx,
		`INSERT INTO ledger_entries (payment_id, account, direction, amount_cents, currency)
		 VALUES ($1, 'receivables', 'credit', $2, $3), ($1, 'revenue', 'debit', $2, $3)`,
		p.ID, p.Amount.Cents, p.Amount.Currency)
	return err
}
