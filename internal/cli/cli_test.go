package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
)

// run executes the CLI in-process against an isolated project root.
func run(t *testing.T, projectRoot string, args ...string) (stdout string, err error) {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	full := append([]string{"--project", projectRoot, "--quiet"}, args...)
	root.SetArgs(full)
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

// isolate gives each test its own project root and user-config home so tests
// never read the developer's real ~/.config/graphlume.
func isolate(t *testing.T) string {
	t.Helper()
	t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())
	return t.TempDir()
}

func TestInitCreatesIndex(t *testing.T) {
	root := isolate(t)

	out, err := run(t, root, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	dbPath := filepath.Join(root, IndexDir, IndexFile)
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("index not created: %v", err)
	}

	// The registry maps the whole codebase; committing it would be a leak.
	ignore, err := os.ReadFile(filepath.Join(root, IndexDir, ".gitignore"))
	if err != nil {
		t.Fatalf("index dir must self-ignore: %v", err)
	}
	if strings.TrimSpace(string(ignore)) != "*" {
		t.Errorf(".graphlume/.gitignore = %q, want \"*\"", ignore)
	}
}

// TestInitNeverClobbers: a second init keeps the index and says it is already
// set up (people run it again to check their setup); only --force recreates it.
func TestInitNeverClobbers(t *testing.T) {
	root := isolate(t)
	if _, err := run(t, root, "init"); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(root, IndexDir, IndexFile)
	before, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, root, "init")
	if err != nil {
		t.Fatalf("a second init must succeed: %v", err)
	}
	if !strings.Contains(out, "already set up") {
		t.Errorf("a second init must say the project is already set up:\n%s", out)
	}
	after, err := os.Stat(db)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("a second init without --force replaced the index")
	}
	if _, err := run(t, root, "init", "--force"); err != nil {
		t.Fatalf("init --force should succeed: %v", err)
	}
}

func TestStatusJSONIsMachineReadable(t *testing.T) {
	root := isolate(t)
	if _, err := run(t, root, "init"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, root, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("status --json emitted non-JSON on stdout: %v\n%s", err, out)
	}
	if got["index_status"] != "empty" {
		t.Errorf("index_status = %v, want empty", got["index_status"])
	}
	if got["schema_version"].(float64) < 1 {
		t.Errorf("schema_version = %v, want >= 1", got["schema_version"])
	}
}

func TestStatusOnMissingIndex(t *testing.T) {
	root := isolate(t)
	out, err := run(t, root, "status")
	if err != nil {
		t.Fatalf("status on missing index should not error: %v", err)
	}
	if !strings.Contains(out, "ABSENT") {
		t.Errorf("expected ABSENT, got:\n%s", out)
	}
}

func TestDoctorPassesOnFreshIndex(t *testing.T) {
	root := isolate(t)
	if _, err := run(t, root, "init"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, root, "doctor", "--verify-index")
	if err != nil {
		t.Fatalf("doctor failed on a fresh index: %v\n%s", err, out)
	}
	for _, want := range []string{"fts5", "dangling edges", "unproven exact edges"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}

// TestDoctorDetectsDanglingEdges proves the invariant gate can actually fail.
//
// A check that has only ever been observed passing is not evidence of
// anything. This injects a real violation and requires doctor to catch it.
func TestDoctorDetectsDanglingEdges(t *testing.T) {
	root := isolate(t)
	if _, err := run(t, root, "init"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(root, IndexDir, IndexFile)})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, db, ctx)

	// Foreign keys would normally block this, so drop the constraint's effect
	// for one statement to simulate corruption arriving another way (a bad
	// migration, a restored backup, a bug in a future writer).
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO edges(index_run_id, src_id, dst_id, kind, confidence, source)
		VALUES(1, 1, 9999, 'CALLS', 'high', 'source_code')`); err != nil {
		t.Fatalf("seed dangling edge: %v", err)
	}
	db.Close()

	out, err := run(t, root, "doctor", "--verify-index")
	if err == nil {
		t.Fatalf("doctor did not fail on a dangling edge:\n%s", out)
	}
	if !strings.Contains(out, "dangling edges") || !strings.Contains(out, "FAIL") {
		t.Errorf("expected a dangling-edge FAIL, got:\n%s", out)
	}
	t.Logf("gate correctly failed: %v", err)
}

// TestDoctorDetectsUnprovenExactEdge guards the product's central promise: an
// edge may not claim compiler certainty unless a compiler produced it.
func TestDoctorDetectsUnprovenExactEdge(t *testing.T) {
	root := isolate(t)
	if _, err := run(t, root, "init"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(root, IndexDir, IndexFile)})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, db, ctx)

	// Layer 1: the storage CHECK must reject the corrupt write outright.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO edges(index_run_id, src_id, dst_id, kind, confidence, source)
		VALUES(1, 1, 2, 'CALLS', 'exact', 'inferred')`); err == nil {
		t.Fatal("storage accepted confidence=exact with source=inferred")
	}

	// A legitimate exact edge must still be insertable...
	if _, err := db.ExecContext(ctx, `
		INSERT INTO edges(index_run_id, src_id, dst_id, kind, confidence, source)
		VALUES(1, 1, 2, 'CALLS', 'exact', 'compiler')`); err != nil {
		t.Fatalf("legitimate compiler-backed exact edge rejected: %v", err)
	}
	// ...and must not be downgradable into a false claim afterwards.
	updateErr := func() error {
		_, err := db.ExecContext(ctx,
			`UPDATE edges SET source = 'inferred' WHERE confidence = 'exact'`)
		return err
	}()
	if updateErr == nil {
		// The CHECK let it through, so layer 2 (the doctor gate) must catch it.
		db.Close()
		out, err := run(t, root, "doctor", "--verify-index")
		if err == nil {
			t.Fatalf("neither the CHECK nor doctor flagged an unproven exact edge:\n%s", out)
		}
		if !strings.Contains(out, "unproven exact edges") {
			t.Errorf("expected an unproven-exact-edge failure, got:\n%s", out)
		}
		t.Logf("doctor gate caught it: %v", err)
		return
	}
	db.Close()
	t.Logf("storage CHECK rejected the downgrade outright (stronger than the doctor gate): %v", updateErr)
}

func TestVersionJSON(t *testing.T) {
	root := isolate(t)
	out, err := run(t, root, "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("version --json invalid: %v\n%s", err, out)
	}
	for _, k := range []string{"version", "commit", "build_time", "go_os", "go_arch"} {
		if v[k] == "" {
			t.Errorf("version --json missing %q", k)
		}
	}
}

func TestUnknownProjectRootIsRejected(t *testing.T) {
	t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())
	if _, err := run(t, filepath.Join(t.TempDir(), "does-not-exist"), "status"); err == nil {
		t.Fatal("expected an error for a nonexistent project root")
	}
}

func TestDoctorVacuum(t *testing.T) {
	root := t.TempDir()
	initOut, err := run(t, root, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, initOut)
	}
	out, err := run(t, root, "doctor", "--vacuum")
	if err != nil {
		t.Fatalf("doctor --vacuum: %v\n%s", err, out)
	}
	if !strings.Contains(out, "vacuum") || !strings.Contains(out, "ok") {
		t.Errorf("expected vacuum ok in output, got:\n%s", out)
	}
}

func seed(t *testing.T, db *store.DB, ctx context.Context) {
	t.Helper()
	for _, s := range []string{
		`INSERT INTO index_runs(id, status, committed, started_unix) VALUES(1,'ready',1,0)`,
		`INSERT INTO files(id, index_run_id, path, content_hash) VALUES(1,1,'a.go','h')`,
		`INSERT INTO symbols(id, index_run_id, file_id, kind, name, fqn, confidence, source)
		 VALUES(1,1,1,'function','A','pkg.A','high','source_code')`,
		`INSERT INTO symbols(id, index_run_id, file_id, kind, name, fqn, confidence, source)
		 VALUES(2,1,1,'function','B','pkg.B','high','source_code')`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestStatusReportsStubbedImports(t *testing.T) {
	root := isolate(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, root, "init", "--index"); err != nil {
		t.Fatal(err)
	}
	scipDir := filepath.Join(root, ".graphlume", "scip")
	if err := os.MkdirAll(scipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"stubbed_imports":23,"packages_with_stubs":19,"packages":19,"files_checked":50}`
	if err := os.WriteFile(filepath.Join(scipDir, "gotypes.json"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	// Add an exact edge so ExactEdges > 0
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(root, ".graphlume", "index.db")})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.ExecContext(context.Background(), `INSERT INTO edges(id, index_run_id, src_id, dst_id, kind, confidence, source, detail, evidence_file) VALUES(1, 1, 1, 2, 'calls', 'exact', 'compiler', 'go/types (built in)', 'main.go')`)
	_ = db.Close()

	out, err := run(t, root, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "23 stubbed import(s) in 19 package(s)") {
		t.Errorf("status output lacks stubbed imports warning:\n%s", out)
	}
	jsonOut, err := run(t, root, "--json", "status")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &data); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	goTypes, ok := data["go_types"].(map[string]any)
	if !ok || goTypes["stubbed_imports"].(float64) != 23 || goTypes["packages_with_stubbed_imports"].(float64) != 19 {
		t.Errorf("unexpected go_types in status json: %v", data["go_types"])
	}
}
