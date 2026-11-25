package registry

import (
	"context"
	"fmt"
	"testing"
)

func TestFuzzyScoreOrdersTheKindsOfResemblance(t *testing.T) {
	q := "transaction"
	same := FuzzyScore(q, "Transaction")
	prefix := FuzzyScore(q, "TransactionService")
	contains := FuzzyScore(q, "NewTransactionService")
	typo := FuzzyScore("transactoin", "Transaction") // two letters swapped
	subseq := FuzzyScore("trnsctn", "Transaction")
	if !(same > prefix && prefix > contains && contains > subseq) {
		t.Errorf("order: same %.2f prefix %.2f contains %.2f subseq %.2f", same, prefix, contains, subseq)
	}
	if typo < fuzzyMinScore {
		t.Errorf("a swapped pair of letters scored %.2f, below the %.2f floor", typo, fuzzyMinScore)
	}
	if got := FuzzyScore("zebra", "Transaction"); got >= fuzzyMinScore {
		t.Errorf("unrelated names scored %.2f", got)
	}
	if got := FuzzyScore("", "x"); got != 0 {
		t.Errorf("empty query scored %.2f", got)
	}
}

func TestFuzzySymbolsFindsAMisspelledName(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	stmts := []string{`INSERT INTO index_runs(id, status, committed, started_unix) VALUES(1,'ready',1,0)`,
		`INSERT INTO files(id, index_run_id, path, language, content_hash) VALUES(1,1,'a.go','go','h')`}
	for i, n := range []string{"TransactionService", "TransactionRepo", "PaymentGateway", "Transaction", "UserService"} {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO symbols(id, index_run_id, file_id, kind, name, fqn, start_line, exported, confidence, source)
			VALUES(%d,1,1,'type','%s','a.go:%s',%d,1,'high','tree-sitter')`, i+1, n, n, i+1))
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	got, err := r.FuzzySymbols(ctx, 1, "Transactoin", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Name != "Transaction" {
		t.Fatalf("misspelling should find Transaction first: %+v", got)
	}
	for _, h := range got {
		if h.Name == "PaymentGateway" || h.Name == "UserService" {
			t.Errorf("unrelated name suggested: %s", h.Name)
		}
	}

	// A short query takes the names it starts, and a LIKE metacharacter is plain.
	short, err := r.FuzzySymbols(ctx, 1, "Us", "", 5)
	if err != nil || len(short) != 1 || short[0].Name != "UserService" {
		t.Errorf("short query: %+v, %v", short, err)
	}
	none, err := r.FuzzySymbols(ctx, 1, "%", "", 5)
	if err != nil || len(none) != 0 {
		t.Errorf("a percent sign matched %d names: %v", len(none), err)
	}
}
