package ledger

import (
	"context"
	"encoding/json"
	"log"

	core "example.com/payments-platform/libs/payments-core"
	"example.com/payments-platform/libs/payments-core/kafka"
)

// Consumer checks that every captured payment has ledger entries.
type Consumer struct {
	ledger *Ledger
}

// NewConsumer returns a Consumer over l.
func NewConsumer(l *Ledger) *Consumer { return &Consumer{ledger: l} }

// Run reads payment.captured until ctx ends.
func (c *Consumer) Run(ctx context.Context) error {
	r := kafka.NewReader(kafka.ReaderConfig{
		Topic:   "payment.captured",
		GroupID: "ledger",
	})
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			return err
		}
		var p core.Payment
		if err := json.Unmarshal(msg.Value, &p); err != nil {
			log.Printf("ledger: bad message %q: %v", msg.Key, err)
			continue
		}
		if err := c.Reconcile(ctx, p); err != nil {
			log.Printf("ledger: payment %s: %v", p.ID, err)
		}
	}
}

// Reconcile counts the entries booked for a payment.
func (c *Consumer) Reconcile(ctx context.Context, p core.Payment) error {
	rows, err := c.ledger.db.QueryContext(ctx,
		`SELECT id FROM ledger_entries WHERE payment_id = $1`, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if n < 2 {
		log.Printf("ledger: payment %s has %d entries, want 2", p.ID, n)
	}
	return rows.Err()
}
