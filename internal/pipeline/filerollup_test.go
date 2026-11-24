package pipeline

import (
	"context"
	"fmt"
	"sort"
	"testing"
)

// rollupFixture is a small Python tree whose files call one another across
// directories, so the run has edges between files, and a few files that
// nothing touches, so an update of one file is a small share of the run.
func rollupFixture() map[string]string {
	files := map[string]string{
		"core/util.py":  "def helper():\n    return 1\n\ndef other():\n    return 2\n",
		"core/model.py": "from core.util import helper\n\nclass Model:\n    def save(self):\n        return helper()\n",
		"api/handlers.py": "from core.model import Model\nfrom core.util import helper, other\n\n" +
			"def get():\n    m = Model()\n    m.save()\n    return helper()\n\ndef put():\n    return other()\n",
		"api/routes.py":   "from api.handlers import get, put\n\ndef wire():\n    get()\n    put()\n",
		"jobs/nightly.py": "from core.util import other\n\ndef run():\n    return other()\n",
	}
	for i := 0; i < 40; i++ {
		files[fmt.Sprintf("misc/m%02d.py", i)] = fmt.Sprintf("def f%02d():\n    return %d\n", i, i)
	}
	return files
}

// rollupDiff returns how the stored rollups differ from the live grouping
// over edges and symbols, one line per disagreeing row; nil when they agree.
func (h *harness) rollupDiff() []string {
	h.t.Helper()
	ctx := context.Background()
	read := func(q string) map[string]bool {
		rows, err := h.db.QueryContext(ctx, q, h.runID)
		if err != nil {
			h.t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := map[string]bool{}
		for rows.Next() {
			var a, b, k, c string
			var n int64
			if err := rows.Scan(&a, &b, &k, &c, &n); err != nil {
				h.t.Fatal(err)
			}
			out[fmt.Sprintf("%s -> %s %s %s x%d", a, b, k, c, n)] = true
		}
		if err := rows.Err(); err != nil {
			h.t.Fatal(err)
		}
		return out
	}
	live := read(`
		SELECT sf.path, df.path, e.kind, e.confidence, count(*)
		  FROM edges e
		  JOIN symbols s ON s.id = e.src_id JOIN files sf ON sf.id = s.file_id
		  JOIN symbols d ON d.id = e.dst_id JOIN files df ON df.id = d.file_id
		 WHERE e.index_run_id = ? AND sf.id <> df.id
		 GROUP BY sf.path, df.path, e.kind, e.confidence
		UNION ALL
		SELECT f.path, '', 'symbols', '', count(*)
		  FROM symbols s JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ?1 GROUP BY f.path`)
	kept := read(`
		SELECT sf.path, df.path, r.kind, r.confidence, r.n
		  FROM file_edges r JOIN files sf ON sf.id = r.src_file_id JOIN files df ON df.id = r.dst_file_id
		 WHERE r.index_run_id = ?
		UNION ALL
		SELECT f.path, '', 'symbols', '', r.n
		  FROM file_symbols r JOIN files f ON f.id = r.file_id
		 WHERE r.index_run_id = ?1`)
	var diff []string
	for k := range live {
		if !kept[k] {
			diff = append(diff, "missing: "+k)
		}
	}
	for k := range kept {
		if !live[k] {
			diff = append(diff, "stale:   "+k)
		}
	}
	var dirty int
	if err := h.db.QueryRowContext(ctx,
		`SELECT count(*) FROM file_rollup_dirty WHERE index_run_id = ?`, h.runID).Scan(&dirty); err != nil {
		h.t.Fatal(err)
	}
	if dirty != 0 {
		diff = append(diff, fmt.Sprintf("%d touched files left unprocessed", dirty))
	}
	sort.Strings(diff)
	return diff
}

func (h *harness) crossFileRollupRows() int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM file_edges WHERE index_run_id = ?`, h.runID).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// plantRollupRow stores a rollup row no edge stands for, between two files
// nothing in the test touches. A scoped update leaves it alone and a full
// rewrite removes it, which is how the test tells which of the two ran.
func (h *harness) plantRollupRow() {
	h.t.Helper()
	if _, err := h.db.ExecContext(context.Background(), `
		INSERT INTO file_edges (index_run_id, src_file_id, dst_file_id, kind, confidence, n)
		SELECT ?1, (SELECT id FROM files WHERE index_run_id = ?1 AND path = 'misc/m00.py'),
		           (SELECT id FROM files WHERE index_run_id = ?1 AND path = 'misc/m01.py'),
		       'PLANTED', 'low', 1`, h.runID); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) plantedRowSurvived() bool {
	h.t.Helper()
	var n int
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM file_edges WHERE index_run_id = ? AND kind = 'PLANTED'`, h.runID).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	if n > 0 {
		if _, err := h.db.ExecContext(context.Background(),
			`DELETE FROM file_edges WHERE kind = 'PLANTED'`); err != nil {
			h.t.Fatal(err)
		}
	}
	return n > 0
}

// The file rollups equal the live grouping over edges and symbols after a
// full index, after resolve, after a small incremental change (applied file
// by file) and after a large one (rewritten whole).
func TestFileRollupMatchesTheLiveGrouping(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, rollupFixture())
	if d := h.rollupDiff(); d != nil {
		t.Fatalf("after index, the rollup disagrees with the edges:\n%v", d)
	}

	// As `graphlume resolve` does: marked for a full sum first, so the
	// triggers record nothing while it writes, then summed once every pass
	// has written.
	if err := h.reg.MarkFileRollupStale(ctx, h.runID); err != nil {
		t.Fatal(err)
	}
	h.resolveAll()
	var recorded int
	if err := h.db.QueryRowContext(ctx,
		`SELECT count(*) FROM file_rollup_dirty WHERE index_run_id = ? AND file_id <> 0`, h.runID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Errorf("resolve recorded %d touched files while the run was marked for a full sum; want none", recorded)
	}
	if err := h.reg.RefreshFileRollup(ctx, h.runID); err != nil {
		t.Fatal(err)
	}
	if d := h.rollupDiff(); d != nil {
		t.Fatalf("after resolve, the rollup disagrees with the edges:\n%v", d)
	}
	if h.crossFileRollupRows() == 0 {
		t.Fatal("after resolve the rollup holds no row: the fixture has no cross-file edges to test with")
	}

	// A small change: one call retargeted, one file deleted, one added.
	h.plantRollupRow()
	h.write("api/routes.py", "from api.handlers import put\nfrom core.util import helper\n\ndef wire():\n    put()\n    helper()\n")
	h.remove("jobs/nightly.py")
	h.write("jobs/weekly.py", "from core.model import Model\n\ndef run():\n    return Model().save()\n")
	h.syncResolving()
	if !h.plantedRowSurvived() {
		t.Error("a three-file change rewrote the whole rollup; want only the touched files' rows")
	}
	if d := h.rollupDiff(); d != nil {
		t.Fatalf("after a small incremental change, the rollup disagrees with the edges:\n%v", d)
	}

	// A definition every other file calls, changed: most of the run is
	// touched, and the rollup is rewritten whole.
	h.plantRollupRow()
	h.write("core/util.py", "def helper():\n    return 3\n\ndef other():\n    return helper()\n")
	for i := 0; i < 20; i++ {
		h.write(fmt.Sprintf("misc/m%02d.py", i+2), fmt.Sprintf("from core.util import helper\n\ndef f%02d():\n    return helper()\n", i))
	}
	h.syncResolving()
	if h.plantedRowSurvived() {
		t.Error("a change to most files kept a row no edge stands for; want a full rewrite")
	}
	if d := h.rollupDiff(); d != nil {
		t.Fatalf("after a large incremental change, the rollup disagrees with the edges:\n%v", d)
	}

	// A resolve that marked the run and never finished: the next update,
	// however small, sums the run whole.
	h.plantRollupRow()
	if err := h.reg.MarkFileRollupStale(ctx, h.runID); err != nil {
		t.Fatal(err)
	}
	h.write("api/routes.py", "from api.handlers import get\n\ndef wire():\n    get()\n")
	h.syncResolving()
	if h.plantedRowSurvived() {
		t.Error("an update after an unfinished resolve kept a row no edge stands for; want a full rewrite")
	}
	if d := h.rollupDiff(); d != nil {
		t.Fatalf("after an unfinished resolve, the rollup disagrees with the edges:\n%v", d)
	}
}
