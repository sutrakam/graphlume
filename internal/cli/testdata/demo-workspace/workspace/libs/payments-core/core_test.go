package core

import "testing"

func TestMoney_Add(t *testing.T) {
	sum, err := Money{Cents: 150, Currency: "EUR"}.Add(Money{Cents: 50, Currency: "EUR"})
	if err != nil || sum.Cents != 200 {
		t.Fatalf("got %v, %v", sum, err)
	}
	if _, err := (Money{Cents: 1, Currency: "EUR"}).Add(Money{Cents: 1, Currency: "USD"}); err != ErrCurrencyMismatch {
		t.Fatalf("want currency mismatch, got %v", err)
	}
}
