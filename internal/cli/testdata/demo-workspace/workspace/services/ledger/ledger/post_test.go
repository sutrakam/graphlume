package ledger

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	core "example.com/payments-platform/libs/payments-core"
)

type fakeDB struct{ execs int }

func (f *fakeDB) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	f.execs++
	return nil, nil
}

func (f *fakeDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("fakeDB: queries are not supported")
}

func TestPost_WritesBothSides(t *testing.T) {
	db := &fakeDB{}
	p := core.Payment{ID: "pay_1", Amount: core.Money{Cents: 1250, Currency: "EUR"}}
	if err := New(db).Post(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if db.execs != 1 {
		t.Fatalf("want one insert for both entries, got %d", db.execs)
	}
}
