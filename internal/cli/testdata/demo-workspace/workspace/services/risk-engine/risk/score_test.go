package risk

import "testing"

func TestRate(t *testing.T) {
	if got := rate(1_000, 0); got != 0 {
		t.Fatalf("small payment: got %d", got)
	}
	if got := rate(900_000, 12); got != 100 {
		t.Fatalf("large payment, busy hour: got %d", got)
	}
}
