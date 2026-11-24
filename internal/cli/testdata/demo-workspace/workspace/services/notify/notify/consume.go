// Package notify tells customers what happened to their payments.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	core "example.com/payments-platform/libs/payments-core"
	"example.com/payments-platform/libs/payments-core/kafka"
)

// Mailer sends one message to a customer.
type Mailer interface {
	Send(ctx context.Context, subject, body string) error
}

// LogMailer writes messages to the log instead of sending them.
type LogMailer struct{}

// Send logs the message.
func (LogMailer) Send(_ context.Context, subject, body string) error {
	log.Printf("notify: %s: %s", subject, body)
	return nil
}

// Consumer turns payment events into messages.
type Consumer struct {
	mailer Mailer
}

// NewConsumer returns a Consumer that sends through m.
func NewConsumer(m Mailer) *Consumer { return &Consumer{mailer: m} }

// Run reads payment.captured until ctx ends.
func (c *Consumer) Run(ctx context.Context) error {
	r := kafka.NewReader(kafka.ReaderConfig{
		Topic:   "payment.captured",
		GroupID: "notify",
	})
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			return err
		}
		if err := c.Handle(ctx, msg.Value); err != nil {
			log.Printf("notify: %v", err)
		}
	}
}

// Handle sends the receipt for one captured payment.
func (c *Consumer) Handle(ctx context.Context, value []byte) error {
	var p core.Payment
	if err := json.Unmarshal(value, &p); err != nil {
		return fmt.Errorf("decode payment: %w", err)
	}
	return c.mailer.Send(ctx, "Payment received", fmt.Sprintf("We received %s for payment %s.", p.Amount, p.ID))
}
