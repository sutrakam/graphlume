package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
)

func writeGoProject(t *testing.T, root string) {
	t.Helper()
	src := "package demo\n\nfunc A() int { return B() }\n\nfunc B() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(root, "demo.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

// An index this build creates reclaims its own free space, status says so,
// and index and resolve carry the reclaimed bytes as a JSON field.
func TestIndexReportsAutomaticCompaction(t *testing.T) {
	root := isolate(t)
	writeGoProject(t, root)
	if out, err := run(t, root, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"index", "--full", "--json"},
		{"index", "--full", "--json"},
		{"resolve", "--json", "--accuracy", "fast"},
	} {
		out, err := run(t, root, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v: not JSON: %v\n%s", args, err, out)
		}
		if _, ok := got["reclaimed_bytes"]; !ok {
			t.Errorf("%v: no reclaimed_bytes field:\n%s", args, out)
		}
		if strings.Contains(out, "Reclaimed") {
			t.Errorf("%v: --json output carries the text line:\n%s", args, out)
		}
	}

	out, err := run(t, root, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st statusReport
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	if st.Storage == nil || st.Storage.AutoVacuum != store.AutoVacuumIncremental || !st.Storage.AutoCompact {
		t.Fatalf("status storage = %+v, want incremental and automatic", st.Storage)
	}

	text, err := run(t, root, "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Free space:", "(reclaimed automatically)", "Auto-vacuum:", "incremental"} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
}

func TestAutoCompactOptOut(t *testing.T) {
	root := isolate(t)
	writeGoProject(t, root)
	if err := os.WriteFile(filepath.Join(root, ".graphlume.yml"),
		[]byte("index:\n  auto_compact: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, root, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err := run(t, root, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	var st statusReport
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	if st.Storage == nil || st.Storage.AutoCompact {
		t.Fatalf("status storage = %+v, want auto_compact false", st.Storage)
	}
	if line := freeSpaceLine(*st.Storage); !strings.Contains(line, "index.auto_compact is false") {
		t.Errorf("free space line = %q", line)
	}
}

// A database made without auto_vacuum keeps working, and the write that
// follows says nothing about space.
func TestCompactPolicyOnDatabaseWithoutAutoVacuum(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "plain.db")
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE filler(b BLOB)`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < 4096)
		INSERT INTO filler SELECT randomblob(1024) FROM c`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM filler`); err != nil {
		t.Fatal(err)
	}
	if got := (compactPolicy{enabled: true}).after(ctx, registry.New(db)); got != 0 {
		t.Fatalf("after() on auto_vacuum=none reclaimed %d", got)
	}
	var buf bytes.Buffer
	writeReclaimed(&buf, "  ", 0)
	if buf.Len() != 0 {
		t.Fatalf("nothing reclaimed, but printed %q", buf.String())
	}
	writeReclaimed(&buf, "  ", 70<<20)
	if got := buf.String(); got != "  Reclaimed 70.0 MiB of free space in the index.\n" {
		t.Fatalf("reclaimed line = %q", got)
	}
}

// An index artifact is a VACUUM INTO copy; it must keep the mode, so a
// pulled index reclaims its own space too.
func TestVacuumIntoKeepsIncrementalAutoVacuum(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src, err := openPlaceholder(ctx, filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.db")
	if _, err := src.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		t.Fatal(err)
	}
	_ = src.Close()
	cp, err := store.Open(ctx, store.Options{Path: dst, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	free, err := store.ReadFreeSpace(ctx, cp)
	if err != nil {
		t.Fatal(err)
	}
	if free.AutoVacuum != store.AutoVacuumIncremental {
		t.Fatalf("VACUUM INTO copy has auto_vacuum %q", free.AutoVacuum)
	}
}
