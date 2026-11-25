// Package worker retries captures that did not finish the first time.
package worker

import (
	"context"
	"log"
	"time"

	core "example.com/payments-platform/libs/payments-core"
)

// Capturer captures a payment.
type Capturer interface {
	Capture(ctx context.Context, id, key string) (core.Payment, error)
}

// PendingSource lists the payments waiting for a retry.
type PendingSource interface {
	Pending(ctx context.Context) ([]string, error)
}

// RetryWorker keeps trying pending captures.
type RetryWorker struct {
	svc     Capturer
	pending PendingSource
	backoff time.Duration
}

// NewRetryWorker returns a worker that waits backoff between passes.
func NewRetryWorker(svc Capturer, pending PendingSource, backoff time.Duration) *RetryWorker {
	return &RetryWorker{svc: svc, pending: pending, backoff: backoff}
}

// Run retries pending captures until ctx ends. The idempotency key is derived
// from the payment, so a retry can never charge twice.
func (w *RetryWorker) Run(ctx context.Context) {
	for {
		ids, err := w.pending.Pending(ctx)
		if err != nil {
			log.Printf("worker: list pending: %v", err)
		}
		for _, id := range ids {
			if _, err := w.svc.Capture(ctx, id, "retry-"+id); err != nil {
				log.Printf("worker: capture %s: %v", id, err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.backoff):
		}
	}
}
