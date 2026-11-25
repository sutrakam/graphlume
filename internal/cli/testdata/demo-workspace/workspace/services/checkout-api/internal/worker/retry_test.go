package worker

import (
	"context"
	"testing"
	"time"

	core "example.com/payments-platform/libs/payments-core"
)

type fakeCapturer struct{ keys []string }

func (f *fakeCapturer) Capture(_ context.Context, id, key string) (core.Payment, error) {
	f.keys = append(f.keys, key)
	return core.Payment{ID: id}, nil
}

type fixedPending []string

func (f fixedPending) Pending(context.Context) ([]string, error) { return f, nil }

func TestRetryWorker_Backoff(t *testing.T) {
	svc := &fakeCapturer{}
	w := NewRetryWorker(svc, fixedPending{"pay_1"}, 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	if len(svc.keys) < 2 || svc.keys[0] != "retry-pay_1" {
		t.Fatalf("keys=%v", svc.keys)
	}
}
