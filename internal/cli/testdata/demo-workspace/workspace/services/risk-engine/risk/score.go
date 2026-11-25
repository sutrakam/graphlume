// Package risk scores payments and records the ones that look fraudulent.
package risk

import (
	"context"
	"database/sql"

	core "example.com/payments-platform/libs/payments-core"
)

// DB is the part of *sql.DB the risk engine uses.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Scorer rates a payment from 0 (safe) to 100.
type Scorer struct {
	db DB
}

// NewScorer returns a Scorer over db.
func NewScorer(db DB) *Scorer { return &Scorer{db: db} }

// Score rates the amount against the customer's recent payments.
func (s *Scorer) Score(ctx context.Context, p core.Payment) (int, error) {
	recent, err := s.recentCount(ctx, p.ID)
	if err != nil {
		return 0, err
	}
	return rate(p.Amount.Cents, recent), nil
}

func (s *Scorer) recentCount(ctx context.Context, paymentID string) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM payments WHERE created_at > now() - interval '1 hour' AND id <> $1`, paymentID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

func rate(cents int64, recent int) int {
	score := recent * 5
	if cents > 500_000 {
		score += 40
	}
	if score > 100 {
		score = 100
	}
	return score
}
