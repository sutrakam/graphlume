package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
)

// The summary `graphlume index` prints, the run it stores and what `status`
// then reports are three views of one set of rows, and they disagreed: on
// Newtonsoft.Json the summary said 6,432 symbols and 43 parse failures, the
// stored run 6,745 and 48, and `status` 6,745.
//
// The fixture has what made them differ. A configuration file contributes
// symbols no parser returns (its keys), and a source file the parser gets
// nothing out of is recorded as a partial parse, which the stored run counts
// as a parse failure and the summary did not count at all.
func TestIndexSummaryMatchesTheStoredRunAndStatus(t *testing.T) {
	root := isolate(t)
	for rel, body := range map[string]string{
		"svc/ok.go":    "package svc\n\nfunc Ok() int { return 1 }\n\nfunc Also() int { return 2 }\n",
		"svc/empty.go": "package svc\n",
		"app.env":      "DATABASE_URL=postgres://localhost/app\nPAYMENT_TIMEOUT=30\n",
		".env":         "API_BASE=https://example.test\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := run(t, root, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	raw, err := run(t, root, "--json", "index")
	if err != nil {
		t.Fatalf("index: %v\n%s", err, raw)
	}
	var printed struct {
		Symbols       int64 `json:"symbols"`
		ParseFailures int64 `json:"parse_failures"`
		PartialParses int64 `json:"partial_parses"`
	}
	if err := json.Unmarshal([]byte(raw), &printed); err != nil {
		t.Fatalf("index --json: %v\n%s", err, raw)
	}

	// The stored run.
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(root, IndexDir, IndexFile), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var storedSymbols, storedFailures, rows, failedRows, partialRows int64
	if err := db.QueryRowContext(ctx, `
		SELECT r.symbols_found, r.parse_failures,
		       (SELECT count(*) FROM symbols WHERE index_run_id = r.id),
		       (SELECT count(*) FROM files WHERE index_run_id = r.id AND parse_status = 'failed'),
		       (SELECT count(*) FROM files WHERE index_run_id = r.id AND parse_status = 'partial')
		  FROM index_runs r WHERE r.committed = 1`).
		Scan(&storedSymbols, &storedFailures, &rows, &failedRows, &partialRows); err != nil {
		t.Fatal(err)
	}

	if partialRows == 0 {
		// Which files count as partial is the parser's rule and may change.
		// The comparisons below hold either way; this only says the partial
		// half of them was not exercised.
		t.Log("fixture: no file was recorded as a partial parse")
	}
	if rows <= 2 {
		t.Fatalf("fixture: the run holds %d symbols, so nothing beyond the two functions was indexed "+
			"and the symbol comparison proves nothing", rows)
	}
	if storedSymbols != rows {
		t.Fatalf("stored symbols_found = %d but the run holds %d symbol rows", storedSymbols, rows)
	}
	if printed.Symbols != storedSymbols {
		t.Errorf("index printed %d symbols; the run it stored says %d", printed.Symbols, storedSymbols)
	}
	if got := printed.ParseFailures + printed.PartialParses; got != storedFailures {
		t.Errorf("index printed %d failed + %d partial = %d; the run it stored says %d",
			printed.ParseFailures, printed.PartialParses, got, storedFailures)
	}
	if printed.ParseFailures != failedRows || printed.PartialParses != partialRows {
		t.Errorf("index printed %d failed, %d partial; the rows are %d and %d",
			printed.ParseFailures, printed.PartialParses, failedRows, partialRows)
	}

	// And `status`, the command a person runs next.
	rawStatus, err := run(t, root, "--json", "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, rawStatus)
	}
	var st struct {
		Symbols       int64 `json:"symbols"`
		ParseFailures int64 `json:"parse_failures"`
		PartialParses int64 `json:"partial_parses"`
	}
	if err := json.Unmarshal([]byte(rawStatus), &st); err != nil {
		t.Fatalf("status --json: %v\n%s", err, rawStatus)
	}
	if st.Symbols != printed.Symbols || st.ParseFailures != printed.ParseFailures || st.PartialParses != printed.PartialParses {
		t.Errorf("index printed symbols %d, failed %d, partial %d; status says %d, %d, %d",
			printed.Symbols, printed.ParseFailures, printed.PartialParses,
			st.Symbols, st.ParseFailures, st.PartialParses)
	}

	// The text form says both parse figures too.
	out, err := run(t, root, "index", "--full")
	if err != nil || !strings.Contains(collapse(out), "Symbols: "+itoa(storedSymbols)) {
		t.Errorf("index --full text summary: %v\n%s", err, out)
	}
	if partialRows > 0 && !strings.Contains(collapse(out), "Partial parses: "+itoa(partialRows)) {
		t.Errorf("index --full does not print the %d partial parse(s):\n%s", partialRows, out)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
