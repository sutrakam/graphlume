package multirepo

import (
	"context"
	"testing"
)

// The row loops check rows.Err after iterating. A query that fails part way
// through ends rows.Next with false, exactly like a query that ran out of
// rows; without the check the caller gets a partial answer and a nil error.
//
// SQLite raises such an error at run time from a row-dependent expression:
// abs() of the smallest int64 overflows. Reading a table through a view that
// evaluates it on the last row reproduces a mid-scan failure without a fake
// driver.
const overflowOnLastRow = `abs(rowid - rowid - 9223372036854775807 - 1)`

func TestFindLinksReportsMidScanError(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)
	mustSync(t, m, svcFor("p1", 1, 0), contract("provides", "http_route", "GET /orders", "high", "heuristic"))
	mustSync(t, m, svcFor("p2", 1, 0), contract("provides", "http_route", "GET /orders", "high", "heuristic"))
	mustSync(t, m, svcFor("c", 1, 0), contract("consumes", "http_route", "GET /orders", "high", "heuristic"))
	if p := linksFrom(t, m, "c"); len(p.Links) != 2 {
		t.Fatalf("setup: links = %d, want 2", len(p.Links))
	}

	// Without its index the query scans contract_links in rowid order, which
	// is already the ORDER BY, so rows stream one at a time. With the index
	// SQLite sorts first, computing every row before returning any, and the
	// error would come from QueryContext instead of from the row loop.
	if _, err := m.db.ExecContext(ctx, `
		DROP INDEX idx_cl_src;
		ALTER TABLE contract_links RENAME TO contract_links_real;
		CREATE VIEW contract_links AS
		SELECT id, src_contract_id, dst_contract_id, src_service_id, dst_service_id, link_kind,
		       CASE WHEN id = (SELECT max(id) FROM contract_links_real)
		            THEN `+overflowOnLastRow+` ELSE confidence END AS confidence,
		       match_rule
		  FROM contract_links_real;`); err != nil {
		t.Fatal(err)
	}
	page, err := m.FindDownstreamLinks(ctx, "c", "", 10)
	if err == nil {
		t.Fatalf("mid-scan error swallowed; got a partial page of %d link(s)", len(page.Links))
	}
}

func TestMarkStaleReportsMidScanError(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)
	mustSync(t, m, svcFor("p", 1, 0), contract("provides", "http_route", "GET /orders", "high", "heuristic"))
	mustSync(t, m, svcFor("c", 1, 0), contract("consumes", "http_route", "GET /orders", "high", "heuristic"))
	m.WithRunLookup(func(context.Context, string) (int64, bool) { return 2, true })

	if _, err := m.db.ExecContext(ctx, `
		ALTER TABLE workspace_services RENAME TO workspace_services_real;
		CREATE VIEW workspace_services AS
		SELECT service_id, links_truncated,
		       CASE WHEN rowid = (SELECT max(rowid) FROM workspace_services_real)
		            THEN `+overflowOnLastRow+` ELSE committed_run_id END AS committed_run_id
		  FROM workspace_services_real;`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.FindDownstreamLinks(ctx, "c", "", 10); err == nil {
		t.Fatal("mid-scan error in the recorded-run read was swallowed")
	}
}

func TestScanContractsReportsMidScanError(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)
	mustSync(t, m, svcFor("a", 1, 0),
		contract("provides", "http_route", "GET /a", "high", "heuristic"),
		contract("provides", "http_route", "GET /b", "high", "heuristic"))

	rows, err := m.db.QueryContext(ctx, `
		SELECT id, service_id, direction, contract_kind, canonical_key,
		       CASE WHEN id = (SELECT max(id) FROM exported_contracts)
		            THEN `+overflowOnLastRow+` ELSE confidence END,
		       source, match_key, seg_count
		  FROM exported_contracts ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanContracts(rows, 0)
	if err == nil {
		t.Fatalf("mid-scan error swallowed; got %d of 2 contracts", len(got))
	}
}
