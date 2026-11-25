package risk

import "context"

// Flagger records and clears fraud flags.
type Flagger struct {
	db DB
}

// NewFlagger returns a Flagger over db.
func NewFlagger(db DB) *Flagger { return &Flagger{db: db} }

// Flag opens a fraud flag on a payment.
func (f *Flagger) Flag(ctx context.Context, paymentID, reason string) error {
	_, err := f.db.ExecContext(ctx,
		`INSERT INTO fraud_flags (payment_id, reason) VALUES ($1, $2)`, paymentID, reason)
	return err
}

// Clear closes every open flag on a payment.
func (f *Flagger) Clear(ctx context.Context, paymentID string) error {
	_, err := f.db.ExecContext(ctx,
		`UPDATE fraud_flags SET cleared_at = now() WHERE payment_id = $1 AND cleared_at IS NULL`, paymentID)
	return err
}
