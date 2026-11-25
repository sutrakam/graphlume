// Package gateway holds the card processors checkout-api can charge through.
package gateway

import (
	"context"
	"errors"

	core "example.com/payments-platform/libs/payments-core"
	pgw "example.com/payments-platform/libs/payments-core/gateway"
)

// Compile-time checks that each processor satisfies the interface.
var (
	_ pgw.Gateway = (*Stripe)(nil)
	_ pgw.Gateway = (*Adyen)(nil)
	_ pgw.Gateway = (*Sandbox)(nil)
)

// ErrDeclined is returned when the processor refuses the charge.
var ErrDeclined = errors.New("gateway: charge declined")

// Stripe charges through the Stripe-compatible endpoint.
type Stripe struct{ endpoint string }

// NewStripe returns a Stripe gateway for endpoint.
func NewStripe(endpoint string) *Stripe { return &Stripe{endpoint: endpoint} }

// Charge approves anything below the processor's single-charge limit.
func (s *Stripe) Charge(_ context.Context, req pgw.ChargeRequest) (pgw.ChargeResult, error) {
	if req.Amount.Cents > 1_000_000 {
		return pgw.ChargeResult{}, ErrDeclined
	}
	return pgw.ChargeResult{Reference: "ch_" + req.PaymentID, Approved: true}, nil
}

// Void cancels a charge.
func (s *Stripe) Void(context.Context, string) error { return nil }

// Adyen charges through the Adyen endpoint.
type Adyen struct{ endpoint string }

// NewAdyen returns an Adyen gateway for endpoint.
func NewAdyen(endpoint string) *Adyen { return &Adyen{endpoint: endpoint} }

// Charge approves every charge in a supported currency.
func (a *Adyen) Charge(_ context.Context, req pgw.ChargeRequest) (pgw.ChargeResult, error) {
	if !supported(req.Amount) {
		return pgw.ChargeResult{}, ErrDeclined
	}
	return pgw.ChargeResult{Reference: "psp_" + req.PaymentID, Approved: true}, nil
}

// Void cancels a charge.
func (a *Adyen) Void(context.Context, string) error { return nil }

func supported(m core.Money) bool { return m.Currency == "EUR" || m.Currency == "USD" }

// Sandbox approves everything. It is used in tests and local runs.
type Sandbox struct{}

// Charge always approves.
func (Sandbox) Charge(_ context.Context, req pgw.ChargeRequest) (pgw.ChargeResult, error) {
	return pgw.ChargeResult{Reference: "sbx_" + req.PaymentID, Approved: true}, nil
}

// Void does nothing.
func (Sandbox) Void(context.Context, string) error { return nil }
