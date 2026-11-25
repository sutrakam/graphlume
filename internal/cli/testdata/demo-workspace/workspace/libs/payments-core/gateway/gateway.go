// Package gateway defines what a card processor must do for us.
package gateway

import (
	"context"

	core "example.com/payments-platform/libs/payments-core"
)

// ChargeRequest asks a processor to take money.
type ChargeRequest struct {
	PaymentID string
	Amount    core.Money
}

// ChargeResult is the processor's answer.
type ChargeResult struct {
	Reference string
	Approved  bool
}

// Gateway is implemented once per processor.
type Gateway interface {
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
	Void(ctx context.Context, reference string) error
}
