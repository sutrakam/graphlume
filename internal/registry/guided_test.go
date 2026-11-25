package registry

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// seedDefinitions writes `copies` files each defining `name`, plus one file
// that does not, into a committed run, and returns the registry and run.
func seedDefinitions(t *testing.T, name string, copies int) (*Registry, int64) {
	t.Helper()
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	stmts := []string{`INSERT INTO index_runs(id, status, committed, started_unix) VALUES(1,'ready',1,0)`}
	for i := 1; i <= copies; i++ {
		stmts = append(stmts,
			fmt.Sprintf(`INSERT INTO files(id, index_run_id, path, language, content_hash) VALUES(%d,1,'pkg%d/types.ts','typescript','h%d')`, i, i, i),
			fmt.Sprintf(`INSERT INTO symbols(id, index_run_id, file_id, kind, name, fqn, start_line, exported, confidence, source)
			             VALUES(%d,1,%d,'interface','%s','pkg%d/types.ts:%s',%d,1,'high','tree-sitter')`, i, i, name, i, name, i))
	}
	stmts = append(stmts,
		fmt.Sprintf(`INSERT INTO files(id, index_run_id, path, language, content_hash) VALUES(%d,1,'other.ts','typescript','x')`, copies+1))
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return r, 1
}

// TestInFilesReturnsOnlyTheDefinitionsInThoseFiles: the name has 30
// definitions and the cache would cut the list at 10; asking for two files
// returns exactly their definitions, wherever they sort.
func TestInFilesReturnsOnlyTheDefinitionsInThoseFiles(t *testing.T) {
	ctx := context.Background()
	r, run := seedDefinitions(t, "IDisposable", 30)
	c := NewCandidateCache(r, run, 0, 10)

	got, err := c.InFiles(ctx, []string{"IDisposable", "Absent"}, []int64{25, 3, 31})
	if err != nil {
		t.Fatal(err)
	}
	if len(got["IDisposable"]) != 2 || got["IDisposable"][0].FileID != 3 || got["IDisposable"][1].FileID != 25 {
		t.Fatalf("IDisposable in files 3, 25, 31 = %+v, want the definitions in files 3 and 25 in id order", got["IDisposable"])
	}
	if _, ok := got["Absent"]; ok {
		t.Errorf("a name with no definition appeared: %+v", got["Absent"])
	}
	if e := got["IDisposable"][1]; e.FilePath != "pkg25/types.ts" || e.Kind != "interface" || !e.Exported || e.Language != "typescript" {
		t.Errorf("the row is incomplete: %+v", e)
	}

	files, err := c.Files(ctx)
	if err != nil || len(files) != 31 || files[0].Path != "pkg1/types.ts" {
		t.Errorf("Files = %d rows (err %v), want 31 in id order", len(files), err)
	}
	if c.PerNameLimit() != 10 {
		t.Errorf("PerNameLimit = %d, want 10", c.PerNameLimit())
	}
}

// TestInFilesUsesTheFileNameIndex pins the plan: the lookup must be a probe per
// file and not a scan of the name's definitions or of the file's symbols.
func TestInFilesUsesTheFileNameIndex(t *testing.T) {
	ctx := context.Background()
	r, _ := seedDefinitions(t, "IDisposable", 5)
	rows, err := r.db.QueryContext(ctx, `EXPLAIN QUERY PLAN
		SELECT s.id FROM symbols s INDEXED BY idx_symbols_file_name JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = 1 AND s.name IN ('a','b') AND s.file_id IN (1,2,3)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "idx_symbols_file_name") {
		t.Errorf("plan does not use idx_symbols_file_name:\n%s", strings.Join(plan, "\n"))
	}
}
