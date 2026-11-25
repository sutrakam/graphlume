// Package payments is the business logic for taking and returning money.
package payments

import (
	"context"
	"os"

	core "example.com/payments-platform/libs/payments-core"
	pgw "example.com/payments-platform/libs/payments-core/gateway"
	"example.com/payments-platform/services/ledger/ledger"
)

// Store is what the service needs from the database layer.
type Store interface {
	Create(ctx context.Context, p core.Payment) error
	Get(ctx context.Context, id string) (core.Payment, error)
	Update(ctx context.Context, p core.Payment) error
	LookupKey(ctx context.Context, key string) (string, bool, error)
	InsertKey(ctx context.Context, key, paymentID string) error
}

// PaymentService creates, captures and refunds payments.
type PaymentService struct {
	store    Store
	idem     *Idempotency
	gateway  pgw.Gateway
	ledger   *ledger.Ledger
	notifier *Notifier
}

// NewService wires a PaymentService.
func NewService(st Store, gw pgw.Gateway, lg *ledger.Ledger, n *Notifier) *PaymentService {
	return &PaymentService{store: st, idem: &Idempotency{store: st}, gateway: gw, ledger: lg, notifier: n}
}

// Create records a new authorised payment.
func (s *PaymentService) Create(ctx context.Context, p core.Payment) (core.Payment, error) {
	p.Status = core.StatusAuthorised
	return p, s.store.Create(ctx, p)
}

// Get loads one payment.
func (s *PaymentService) Get(ctx context.Context, id string) (core.Payment, error) {
	return s.store.Get(ctx, id)
}

// Capture captures an authorised payment and posts it to the ledger. Calling it
// again with the same idempotency key returns the first result.
func (s *PaymentService) Capture(ctx context.Context, id, key string) (core.Payment, error) {
	if first, seen, err := s.idem.Check(ctx, key); err != nil {
		return core.Payment{}, err
	} else if seen {
		return s.store.Get(ctx, first)
	}
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return core.Payment{}, err
	}
	if p.Status != core.StatusAuthorised && p.Status != core.StatusCapturePending {
		return core.Payment{}, core.ErrNotAuthorised
	}
	res, err := s.gateway.Charge(ctx, pgw.ChargeRequest{PaymentID: p.ID, Amount: p.Amount})
	if err != nil {
		return core.Payment{}, err
	}
	p.Status, p.Reference = core.StatusCaptured, res.Reference
	if err := s.store.Update(ctx, p); err != nil {
		return core.Payment{}, err
	}
	if err := s.ledger.Post(ctx, p); err != nil {
		return core.Payment{}, err
	}
	if err := s.idem.Record(ctx, key, p.ID); err != nil {
		return core.Payment{}, err
	}
	if err := s.notifier.Emit(ctx, p); err != nil {
		return core.Payment{}, err
	}
	if os.Getenv("LEDGER_FAST_PATH") == "1" {
		return p, nil
	}
	return s.store.Get(ctx, p.ID)
}

// Refund returns a captured payment's money. It is the one write path without
// a test, which the Ownership screen should flag.
func (s *PaymentService) Refund(ctx context.Context, id string) (core.Payment, error) {
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return core.Payment{}, err
	}
	if err := s.gateway.Void(ctx, p.Reference); err != nil {
		return core.Payment{}, err
	}
	if err := s.ledger.Reverse(ctx, p); err != nil {
		return core.Payment{}, err
	}
	p.Status = core.StatusRefunded
	if err := s.store.Update(ctx, p); err != nil {
		return core.Payment{}, err
	}
	return p, s.notifier.EmitRefund(ctx, p)
}
