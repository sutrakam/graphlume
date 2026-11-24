package payments

import (
	"context"
	"encoding/json"

	core "example.com/payments-platform/libs/payments-core"
	"example.com/payments-platform/libs/payments-core/kafka"
)

// Notifier tells the rest of the platform what happened to a payment.
type Notifier struct {
	captured *kafka.Writer
	refunded *kafka.Writer
}

// NewNotifier returns a Notifier that publishes to the payment topics.
func NewNotifier() *Notifier {
	captured := &kafka.Writer{
		Topic: "payment.captured",
	}
	refunded := &kafka.Writer{
		Topic: "payment.refunded",
	}
	return &Notifier{captured: captured, refunded: refunded}
}

// Emit publishes payment.captured.
func (n *Notifier) Emit(ctx context.Context, p core.Payment) error {
	return publish(ctx, n.captured, p)
}

// EmitRefund publishes payment.refunded.
func (n *Notifier) EmitRefund(ctx context.Context, p core.Payment) error {
	return publish(ctx, n.refunded, p)
}

func publish(ctx context.Context, w *kafka.Writer, p core.Payment) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return w.WriteMessages(ctx, kafka.Message{Key: p.ID, Value: body})
}
