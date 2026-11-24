package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	core "example.com/payments-platform/libs/payments-core"
	"example.com/payments-platform/services/checkout-api/internal/gateway"
	"example.com/payments-platform/services/checkout-api/internal/payments"
	"example.com/payments-platform/services/ledger/ledger"
)

type memStore struct{ p core.Payment }

func (m *memStore) Create(context.Context, core.Payment) error { return nil }
func (m *memStore) Update(_ context.Context, p core.Payment) error {
	m.p = p
	return nil
}
func (m *memStore) Get(context.Context, string) (core.Payment, error) { return m.p, nil }
func (m *memStore) LookupKey(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (m *memStore) InsertKey(context.Context, string, string) error { return nil }

type nopDB struct{}

func (nopDB) ExecContext(context.Context, string, ...any) (sql.Result, error) { return nil, nil }
func (nopDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("nopDB: no queries")
}

func TestCaptureRoute_201(t *testing.T) {
	st := &memStore{p: core.Payment{ID: "pay_1", Amount: core.Money{Cents: 900, Currency: "EUR"}, Status: core.StatusAuthorised}}
	svc := payments.NewService(st, gateway.Sandbox{}, ledger.New(nopDB{}), payments.NewNotifier())
	srv := httptest.NewServer(NewRouter(svc, nopDB{}))
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/payments/pay_1/capture", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", res.StatusCode)
	}
}
