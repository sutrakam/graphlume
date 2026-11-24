package registry

import (
	"context"
	"strings"
	"testing"
)

// Plan gates for the statements `resolve` runs over the whole run.
//
// A timing test cannot guard these: CI indexes small repositories, where the
// slow plans are also fast. The plan is size-independent, so it fails on a
// fixture the moment the shape of the statement is undone.
//
// What went wrong, so the assertions below read as more than magic strings.
// `resolve` took 35 minutes on a Java repository of 770,000 symbols and
// 1.5 million edges:
//
//   - the supertype lookup (run once per chunk of types, hundreds of times)
//     started from the edges table and walked every edge of the run, because
//     the planner has no statistics and preferred it to the symbol-name index;
//   - the per-language reference counts walked the reference table in name
//     order and fetched every row from the table at random.

// explain returns EXPLAIN QUERY PLAN for query, one step per line.
func explain(t *testing.T, query string, args ...any) string {
	t.Helper()
	_, db := newTestRegistry(t)
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(detail)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	if b.Len() == 0 {
		t.Fatal("empty query plan; this test is not checking anything")
	}
	return b.String()
}

// TestSupertypeLookupStartsFromTheNamedSymbols: the lookup must begin at the
// symbols with the asked-for names and follow their edges by source id, never
// scan the edges of the run.
func TestSupertypeLookupStartsFromTheNamedSymbols(t *testing.T) {
	plan := explain(t, supertypeSQL(3), 1, 1, 1, "A", "B", "C", "EXTENDS", "IMPLEMENTS", 64)

	if !strings.Contains(plan, "SEARCH src USING COVERING INDEX idx_symbols_name (index_run_id=? AND name=?)") &&
		!strings.Contains(plan, "SEARCH src USING INDEX idx_symbols_name (index_run_id=? AND name=?)") {
		t.Errorf("the lookup does not start from idx_symbols_name:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN e") {
		t.Errorf("the lookup scans the edges table:\n%s", plan)
	}
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(line, "SEARCH e ") && !strings.Contains(line, "src_id=?") {
			t.Errorf("edges are searched without the source id, so every edge of the run is read: %q", line)
		}
	}
}

// TestLanguageReferenceCountScansTheTableOnce: the reference counts must read
// the table in order, not walk an index and fetch each row at random.
func TestLanguageReferenceCountScansTheTableOnce(t *testing.T) {
	plan := explain(t, languageRefsSQL, 1)

	if !strings.Contains(plan, "SCAN symbol_references") {
		t.Errorf("the reference table is not scanned in order:\n%s", plan)
	}
	if strings.Contains(plan, "idx_refs_") {
		t.Errorf("the reference counts walk an index and fetch rows at random:\n%s", plan)
	}
}
