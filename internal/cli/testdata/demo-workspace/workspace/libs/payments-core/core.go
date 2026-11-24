// Package core holds the types every payments service shares.
package core

import (
	"errors"
	"fmt"
)

// Status is where a payment is in its life.
type Status string

const (
	StatusAuthorised     Status = "authorised"
	StatusCapturePending Status = "capture_pending"
	StatusCaptured       Status = "captured"
	StatusRefunded       Status = "refunded"
	StatusFailed         Status = "failed"
)

var (
	ErrNotFound         = errors.New("payment not found")
	ErrNotAuthorised    = errors.New("payment is not authorised")
	ErrCurrencyMismatch = errors.New("currency mismatch")
)

// Money is an amount in minor units.
type Money struct {
	Cents    int64  `json:"cents"`
	Currency string `json:"currency"`
}

func (m Money) String() string {
	return fmt.Sprintf("%d.%02d %s", m.Cents/100, m.Cents%100, m.Currency)
}

// Add returns the sum of two amounts in the same currency.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{Cents: m.Cents + o.Cents, Currency: m.Currency}, nil
}

// Payment is one customer payment.
type Payment struct {
	ID        string `json:"id"`
	Amount    Money  `json:"amount"`
	Status    Status `json:"status"`
	Reference string `json:"reference,omitempty"`
}
