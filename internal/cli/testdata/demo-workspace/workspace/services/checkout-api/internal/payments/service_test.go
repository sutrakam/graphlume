package payments

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	core "example.com/payments-platform/libs/payments-core"
	"example.com/payments-platform/services/checkout-api/internal/gateway"
	"example.com/payments-platform/services/ledger/ledger"
)

type memStore struct {
	payments map[string]core.Payment
	keys     map[string]string
}

func newMemStore() *memStore {
	return &memStore{payments: map[string]core.Payment{}, keys: map[string]string{}}
}

func (m *memStore) Create(_ context.Context, p core.Payment) error { m.payments[p.ID] = p; return nil }
func (m *memStore) Update(_ context.Context, p core.Payment) error { m.payments[p.ID] = p; return nil }
func (m *memStore) Get(_ context.Context, id string) (core.Payment, error) {
	if p, ok := m.payments[id]; ok {
		return p, nil
	}
	return core.Payment{}, core.ErrNotFound
}
func (m *memStore) LookupKey(_ context.Context, key string) (string, bool, error) {
	id, ok := m.keys[key]
	return id, ok, nil
}
func (m *memStore) InsertKey(_ context.Context, key, id string) error { m.keys[key] = id; return nil }

// countingDB counts the statements the ledger runs, so a test can tell how
// many times a capture reached it.
type countingDB struct{ execs int }

func (c *countingDB) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	c.execs++
	return nil, nil
}

func (c *countingDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("countingDB: no queries")
}

func newTestService(t *testing.T) (*PaymentService, *countingDB) {
	t.Helper()
	st, db := newMemStore(), &countingDB{}
	st.payments["pay_1"] = core.Payment{
		ID: "pay_1", Amount: core.Money{Cents: 4200, Currency: "EUR"}, Status: core.StatusAuthorised,
	}
	return NewService(st, gateway.Sandbox{}, ledger.New(db), NewNotifier()), db
}

func TestCapture_Happy(t *testing.T) {
	svc, db := newTestService(t)
	p, err := svc.Capture(context.Background(), "pay_1", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != core.StatusCaptured || db.execs != 1 {
		t.Fatalf("status=%s ledger posts=%d", p.Status, db.execs)
	}
}

func TestCapture_Idempotent(t *testing.T) {
	svc, db := newTestService(t)
	for i := 0; i < 2; i++ {
		if _, err := svc.Capture(context.Background(), "pay_1", "key-1"); err != nil {
			t.Fatal(err)
		}
	}
	if db.execs != 1 {
		t.Fatalf("posted to the ledger %d times, want once", db.execs)
	}
}
