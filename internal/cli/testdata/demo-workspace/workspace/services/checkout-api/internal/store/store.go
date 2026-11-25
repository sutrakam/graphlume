// Package store is the only place checkout-api talks to the payments database.
package store

import (
	"context"
	"database/sql"

	core "example.com/payments-platform/libs/payments-core"
)

// DB is the part of *sql.DB the store uses.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Store reads and writes payments and idempotency keys.
type Store struct {
	db DB
}

// New returns a Store over db.
func New(db DB) *Store { return &Store{db: db} }

// Create inserts a new payment.
func (s *Store) Create(ctx context.Context, p core.Payment) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO payments (id, amount_cents, currency, status) VALUES ($1, $2, $3, $4)`,
		p.ID, p.Amount.Cents, p.Amount.Currency, p.Status)
	return err
}

// Get loads one payment.
func (s *Store) Get(ctx context.Context, id string) (core.Payment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, amount_cents, currency, status, reference FROM payments WHERE id = $1`, id)
	if err != nil {
		return core.Payment{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return core.Payment{}, core.ErrNotFound
	}
	var p core.Payment
	var ref sql.NullString
	if err := rows.Scan(&p.ID, &p.Amount.Cents, &p.Amount.Currency, &p.Status, &ref); err != nil {
		return core.Payment{}, err
	}
	p.Reference = ref.String
	return p, rows.Err()
}

// Update saves a payment's status and processor reference.
func (s *Store) Update(ctx context.Context, p core.Payment) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE payments SET status = $2, reference = $3 WHERE id = $1`, p.ID, p.Status, p.Reference)
	return err
}

// Pending lists payments whose capture still has to be retried.
func (s *Store) Pending(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM payments WHERE status = 'capture_pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// LookupKey returns the payment an idempotency key was used for.
func (s *Store) LookupKey(ctx context.Context, key string) (string, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payment_id FROM idempotency_keys WHERE key = $1`, key)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", false, rows.Err()
	}
	var id string
	if err := rows.Scan(&id); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// InsertKey remembers that key was used for a payment.
func (s *Store) InsertKey(ctx context.Context, key, paymentID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO idempotency_keys (key, payment_id) VALUES ($1, $2)`, key, paymentID)
	return err
}
