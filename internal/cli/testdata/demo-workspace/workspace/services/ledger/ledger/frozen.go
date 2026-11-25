package ledger

import (
	"context"
	"errors"
)

// ErrFrozen is returned when a payment has an open fraud flag.
var ErrFrozen = errors.New("ledger: payment has an open fraud flag")

// Frozen reports whether the risk engine has flagged the payment.
func (l *Ledger) Frozen(ctx context.Context, paymentID string) (bool, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT 1 FROM fraud_flags WHERE payment_id = $1 AND cleared_at IS NULL LIMIT 1`, paymentID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}
