package payments

import "context"

// Idempotency makes a repeated request return the first answer.
type Idempotency struct {
	store Store
}

// Check returns the payment a key was first used for, if any.
func (i *Idempotency) Check(ctx context.Context, key string) (string, bool, error) {
	if key == "" {
		return "", false, nil
	}
	return i.store.LookupKey(ctx, key)
}

// Record remembers that key was used for a payment.
func (i *Idempotency) Record(ctx context.Context, key, paymentID string) error {
	if key == "" {
		return nil
	}
	return i.store.InsertKey(ctx, key, paymentID)
}
