// Package kafka is a small in-process stand-in for a Kafka client, so the
// fixture compiles and runs with no broker and no third-party dependency.
package kafka

import (
	"context"
	"errors"
	"sync"
)

// Message is one record on a topic.
type Message struct {
	Key   string
	Value []byte
}

// Writer publishes to one topic.
type Writer struct {
	Topic string
}

// ReaderConfig names the topic a reader listens to.
type ReaderConfig struct {
	Topic   string
	GroupID string
}

// Reader receives messages for one topic.
type Reader struct {
	cfg ReaderConfig
	ch  chan Message
}

var (
	mu   sync.Mutex
	subs = map[string][]chan Message{}
)

// NewReader subscribes to the configured topic.
func NewReader(cfg ReaderConfig) *Reader {
	r := &Reader{cfg: cfg, ch: make(chan Message, 64)}
	mu.Lock()
	subs[cfg.Topic] = append(subs[cfg.Topic], r.ch)
	mu.Unlock()
	return r
}

// WriteMessages delivers each message to every reader of the topic.
func (w *Writer) WriteMessages(ctx context.Context, msgs ...Message) error {
	if w.Topic == "" {
		return errors.New("kafka: writer has no topic")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	mu.Lock()
	chans := append([]chan Message(nil), subs[w.Topic]...)
	mu.Unlock()
	for _, m := range msgs {
		for _, ch := range chans {
			select {
			case ch <- m:
			default: // a slow reader drops messages; fine for a fixture
			}
		}
	}
	return nil
}

// ReadMessage blocks until a message arrives or ctx ends.
func (r *Reader) ReadMessage(ctx context.Context) (Message, error) {
	select {
	case m := <-r.ch:
		return m, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}
