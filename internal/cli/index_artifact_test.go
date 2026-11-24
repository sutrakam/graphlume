package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
)

// These tests exercise export and pull end to end with no network: a shell
// script named gh, first on PATH, serves an artifact zip built from a real
// export of a small indexed repository.

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func skipWithoutGitOrSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

const artifactFixtureSrc = "package shop\n\nfunc Save() int { return 1 }\n\nfunc Create() int { return Save() }\n"

// indexedRepo builds a committed git repository, indexes and resolves it.
func indexedRepo(t *testing.T) string {
	t.Helper()
	root := isolate(t)
	// Resolve symlinks (macOS temp dirs) so paths compare equal everywhere.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	writeFiles(t, root, map[string]string{
		"go.mod":  "module example.com/shop\n\ngo 1.22\n",
		"shop.go": artifactFixtureSrc,
	})
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "one")
	for _, args := range [][]string{{"init"}, {"index"}, {"resolve"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return root
}

func exportTo(t *testing.T, root string, extra ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "art")
	args := append([]string{"index", "export", "--out", dir}, extra...)
	if out, err := run(t, root, args...); err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}
	return dir
}

type zipEntry struct {
	name string
	data []byte
	mode os.FileMode // 0 means a regular file
}

func buildZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipOfExport(t *testing.T, dir string, extra ...zipEntry) []byte {
	t.Helper()
	db, err := os.ReadFile(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	mf, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return buildZip(t, append([]zipEntry{{name: "index.db", data: db}, {name: "manifest.json", data: mf}}, extra...)...)
}

// fakeGH installs a gh stand-in and returns the directory it serves from.
type fakeGH struct{ dir string }

func installFakeGH(t *testing.T) *fakeGH {
	t.Helper()
	skipWithoutGitOrSh(t)
	dir := t.TempDir()
	bin := t.TempDir()
	script := `#!/bin/sh
echo "$*" >> "$FAKE_GH_DIR/calls.log"
if [ "$1" = "auth" ]; then exit "${FAKE_GH_AUTH:-0}"; fi
if [ "$1" = "api" ]; then
  case "$2" in
    */zip)
      id=${2%/zip}; id=${id##*/}
      cat "$FAKE_GH_DIR/zip-$id.zip"; exit 0 ;;
    *name=*)
      n=${2#*name=}; n=${n%%&*}
      if [ -f "$FAKE_GH_DIR/list-$n.json" ]; then cat "$FAKE_GH_DIR/list-$n.json"; else echo '{"total_count":0,"artifacts":[]}'; fi
      exit 0 ;;
  esac
fi
echo "unexpected: $*" >&2; exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil { //nolint:gosec // test stand-in must be executable
		t.Fatal(err)
	}
	t.Setenv("FAKE_GH_DIR", dir)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &fakeGH{dir: dir}
}

// serve publishes zipData as the artifact of commit sha with the given id.
func (f *fakeGH) serve(t *testing.T, sha string, id int, zipData []byte, expired bool) {
	t.Helper()
	name := indexArtifactPrefix + sha
	list := fmt.Sprintf(`{"total_count":1,"artifacts":[{"id":%d,"name":%q,"expired":%v,"created_at":"2026-01-01T00:00:00Z","workflow_run":{"head_sha":%q}}]}`,
		id, name, expired, sha)
	must(t, os.WriteFile(filepath.Join(f.dir, "list-"+name+".json"), []byte(list), 0o600))
	must(t, os.WriteFile(filepath.Join(f.dir, fmt.Sprintf("zip-%d.zip", id)), zipData, 0o600))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// cloneOf makes a second checkout of root at a different path, with no index.
func cloneOf(t *testing.T, root string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "other-place", "checkout")
	must(t, os.MkdirAll(filepath.Dir(dst), 0o755)) //nolint:gosec // test fixture
	if out, err := exec.Command("git", "clone", "-q", root, dst).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if r, err := filepath.EvalSymlinks(dst); err == nil {
		dst = r
	}
	return dst
}

func headOf(t *testing.T, root string) string { return gitRun(t, root, "rev-parse", "HEAD") }

func readManifest(t *testing.T, dir string) indexManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m indexManifest
	must(t, json.Unmarshal(raw, &m))
	return m
}

func TestIndexExportWritesManifestAndSnapshot(t *testing.T) {
	skipWithoutGitOrSh(t)
	root := indexedRepo(t)
	dir := exportTo(t, root)
	m := readManifest(t, dir)

	if m.Format != "graphlume-index-artifact/v1" {
		t.Errorf("format = %q", m.Format)
	}
	if m.Commit != headOf(t, root) {
		t.Errorf("commit = %s, want HEAD", m.Commit)
	}
	if m.Dirty {
		t.Error("a clean tree must not be recorded as dirty")
	}
	latest, _ := store.LatestVersion()
	if m.SchemaVersion != latest {
		t.Errorf("schema_version = %d, want %d", m.SchemaVersion, latest)
	}
	if m.Files < 2 || m.Symbols < 2 || m.Edges < 1 || m.CommittedRunID < 1 || m.RegistryDigest == "" || m.CreatedAt == "" || m.GraphlumeVersion == "" {
		t.Errorf("manifest is missing facts: %+v", m)
	}
	sum, size, err := hashFile(filepath.Join(dir, "index.db"))
	must(t, err)
	if sum != m.SHA256 || size != m.Size {
		t.Errorf("sha/size do not describe index.db: manifest %s/%d, file %s/%d", m.SHA256, m.Size, sum, size)
	}
	// Only the two artifact files: no -wal, no temp file.
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "index.db,manifest.json" {
		t.Errorf("export dir holds %v", names)
	}

	// Deterministic field order: the keys appear as declared.
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	last := -1
	for _, key := range []string{"format", "schema_version", "commit", "committed_run_id", "registry_digest",
		"graphlume_version", "created_at", "files", "symbols", "edges", "sha256", "size"} {
		i := bytes.Index(raw, []byte(`"`+key+`"`))
		if i < 0 || i < last {
			t.Fatalf("key %q is missing or out of order in:\n%s", key, raw)
		}
		last = i
	}
}

func TestIndexExportRefusesDirtyTreeUnlessAllowed(t *testing.T) {
	skipWithoutGitOrSh(t)
	root := indexedRepo(t)
	writeFiles(t, root, map[string]string{"shop.go": artifactFixtureSrc + "\nfunc Extra() {}\n"})

	out, err := run(t, root, "index", "export", "--out", filepath.Join(t.TempDir(), "a"))
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("want a refusal naming uncommitted changes, got %v\n%s", err, out)
	}
	dir := exportTo(t, root, "--allow-dirty")
	if !readManifest(t, dir).Dirty {
		t.Error("--allow-dirty must record dirty: true")
	}
}

func TestIndexExportRefusesAnIndexBuiltAtAnotherCommit(t *testing.T) {
	skipWithoutGitOrSh(t)
	root := indexedRepo(t)
	writeFiles(t, root, map[string]string{"more.go": "package shop\n"})
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "two")
	_, err := run(t, root, "index", "export", "--out", filepath.Join(t.TempDir(), "a"))
	if err == nil || !strings.Contains(err.Error(), "run 'graphlume index'") {
		t.Fatalf("a stale index must not be exported under HEAD's name: %v", err)
	}
}

func TestIndexExportSnapshotIsConsistentWhileWriterRuns(t *testing.T) {
	skipWithoutGitOrSh(t)
	root := indexedRepo(t)
	// Hold a write transaction open on the live index: the snapshot must
	// still come out whole, and the live -wal must not leak into the export.
	rw, err := store.Open(context.Background(), store.Options{Path: filepath.Join(root, IndexDir, IndexFile)})
	must(t, err)
	defer func() { _ = rw.Close() }()
	tx, err := rw.Begin()
	must(t, err)
	_, err = tx.Exec(`INSERT INTO index_runs (status, committed, started_unix) VALUES ('indexing', 0, 1)`)
	must(t, err)
	defer func() { _ = tx.Rollback() }()

	dir := exportTo(t, root)
	if _, err := os.Stat(filepath.Join(dir, "index.db-wal")); err == nil {
		t.Error("the export must not carry a -wal file")
	}
	db, err := openReadOnly(context.Background(), filepath.Join(dir, "index.db"))
	must(t, err)
	defer func() { _ = db.Close() }()
	var n int
	must(t, db.QueryRow(`SELECT count(*) FROM index_runs WHERE status = 'indexing'`).Scan(&n))
	if n != 0 {
		t.Errorf("an uncommitted writer's rows leaked into the snapshot (%d)", n)
	}
}

func TestIndexPullInstallsAndAnswersFromADifferentCheckoutPath(t *testing.T) {
	gh := installFakeGH(t)
	root := indexedRepo(t)
	sha := headOf(t, root)
	dir := exportTo(t, root)
	gh.serve(t, sha, 7, zipOfExport(t, dir), false)

	// The index must not hold the path it was built under, or a checkout
	// elsewhere would have an index pointing at files that are not there.
	assertNoPathStored(t, filepath.Join(dir, "index.db"), root)

	other := cloneOf(t, root)
	out, err := run(t, other, "index", "pull", "--repo", "acme/shop")
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Installed") || strings.Contains(out, "behind") {
		t.Errorf("unexpected output:\n%s", out)
	}
	callers, err := run(t, other, "callers", "Save")
	if err != nil {
		t.Fatalf("callers after pull: %v\n%s", err, callers)
	}
	if !strings.Contains(callers, "Create") {
		t.Errorf("the pulled index does not answer callers of Save:\n%s", callers)
	}
	if doc, err := run(t, other, "doctor", "--verify-index"); err != nil {
		t.Errorf("doctor --verify-index on the pulled index: %v\n%s", err, doc)
	}
	// No staging leftovers beside the index.
	entries, _ := os.ReadDir(filepath.Join(other, IndexDir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pull-") || strings.HasSuffix(e.Name(), ".bak") {
			t.Errorf("leftover %s", e.Name())
		}
	}
	// An incremental update in the new checkout works on the pulled index,
	// even though every file has a different modification time here.
	writeFiles(t, other, map[string]string{"shop.go": artifactFixtureSrc + "\nfunc Later() int { return Save() }\n"})
	if out, err := run(t, other, "index"); err != nil {
		t.Fatalf("incremental index after pull: %v\n%s", err, out)
	}
	if callers, _ := run(t, other, "callers", "Save"); !strings.Contains(callers, "Later") {
		t.Errorf("incremental update did not see the new caller:\n%s", callers)
	}
}

// assertNoPathStored scans every text cell of every table for needle.
func assertNoPathStored(t *testing.T, dbPath, needle string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: dbPath, ReadOnly: true})
	must(t, err)
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	must(t, err)
	var tables []string
	for rows.Next() {
		var n string
		must(t, rows.Scan(&n))
		tables = append(tables, n)
	}
	_ = rows.Close()
	for _, tbl := range tables {
		cols, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT name FROM pragma_table_info(%q)`, tbl))
		if err != nil {
			continue
		}
		var names []string
		for cols.Next() {
			var c string
			must(t, cols.Scan(&c))
			names = append(names, c)
		}
		_ = cols.Close()
		for _, c := range names {
			var n int
			q := fmt.Sprintf(`SELECT count(*) FROM %q WHERE instr(CAST(%q AS TEXT), ?) > 0`, tbl, c)
			if err := db.QueryRowContext(ctx, q, needle).Scan(&n); err != nil {
				continue // virtual-table internals that cannot be scanned this way
			}
			if n > 0 {
				t.Errorf("%s.%s stores the checkout path %s in %d row(s)", tbl, c, needle, n)
			}
		}
	}
}

func TestIndexPullRefusesWhatDoesNotMatchTheManifest(t *testing.T) {
	gh := installFakeGH(t)
	root := indexedRepo(t)
	sha := headOf(t, root)
	dir := exportTo(t, root)
	good, _ := os.ReadFile(filepath.Join(dir, "index.db"))
	mfRaw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))

	flipped := append([]byte(nil), good...)
	flipped[len(flipped)-1] ^= 0xff

	var m map[string]any
	must(t, json.Unmarshal(mfRaw, &m))
	// A manifest naming a schema other than the database's own.
	latest, _ := store.LatestVersion()
	m["schema_version"] = latest + 1
	badSchema, _ := json.Marshal(m)
	delete(m, "schema_version")

	for _, tc := range []struct {
		name string
		zip  []byte
		want string
	}{
		{"checksum", buildZip(t, zipEntry{name: "index.db", data: flipped}, zipEntry{name: "manifest.json", data: mfRaw}), "SHA-256 mismatch"},
		{"size", buildZip(t, zipEntry{name: "index.db", data: append(append([]byte(nil), good...), 0)}, zipEntry{name: "manifest.json", data: mfRaw}), "bytes but the manifest says"},
		{"manifest schema", buildZip(t, zipEntry{name: "index.db", data: good}, zipEntry{name: "manifest.json", data: badSchema}), fmt.Sprintf("manifest says schema v%d", latest+1)},
		{"not a zip", []byte("not a zip"), "not a valid zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh.serve(t, sha, 7, tc.zip, false)
			other := cloneOf(t, root)
			out, err := run(t, other, "index", "pull", "--repo", "acme/shop")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v\n%s", tc.want, err, out)
			}
			if fileExists(filepath.Join(other, IndexDir, IndexFile)) {
				t.Error("a refused artifact must not leave an index behind")
			}
		})
	}
}

// resign rewrites the database in an export and updates its manifest so the
// checksum matches: the attack is then in the database, not the transport.
func resign(t *testing.T, dir string, mutate func(db *store.DB)) {
	t.Helper()
	path := filepath.Join(dir, "index.db")
	db, err := store.Open(context.Background(), store.Options{Path: path})
	must(t, err)
	mutate(db)
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	_, _ = db.Exec(`PRAGMA journal_mode = DELETE`)
	_ = db.Close()
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	m := readManifest(t, dir)
	m.SHA256, m.Size, _ = hashFileOrFail(t, path)
	body, _ := json.Marshal(m)
	must(t, os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0o600))
}

func hashFileOrFail(t *testing.T, p string) (string, int64, error) {
	t.Helper()
	s, n, err := hashFile(p)
	must(t, err)
	return s, n, nil
}

func TestIndexPullRefusesSchemaMismatchWithATypedError(t *testing.T) {
	gh := installFakeGH(t)
	root := indexedRepo(t)
	sha := headOf(t, root)
	latest, _ := store.LatestVersion()

	t.Run("newer", func(t *testing.T) {
		dir := exportTo(t, root)
		resign(t, dir, func(db *store.DB) {
			_, err := db.Exec(`INSERT INTO schema_migrations (version, name, checksum, applied_unix) VALUES (?, 'future', 'x', 1)`, latest+5)
			must(t, err)
		})
		m := readManifest(t, dir)
		m.SchemaVersion = latest + 5
		body, _ := json.Marshal(m)
		must(t, os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0o600))
		gh.serve(t, sha, 7, zipOfExport(t, dir), false)
		other := cloneOf(t, root)
		_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
		var newer *store.ErrSchemaNewer
		if !errors.As(err, &newer) {
			t.Fatalf("want ErrSchemaNewer, got %v", err)
		}
		if !strings.Contains(err.Error(), "upgrade graphlume") {
			t.Errorf("the message must name the fix: %v", err)
		}
		if fileExists(filepath.Join(other, IndexDir, IndexFile)) {
			t.Error("nothing may be installed")
		}
	})
	t.Run("outdated", func(t *testing.T) {
		// CI exported with this build; the puller runs the next release.
		dir := exportTo(t, root)
		gh.serve(t, sha, 7, zipOfExport(t, dir), false)
		other := cloneOf(t, root)
		shipNextMigration(t)
		_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
		var older *store.ErrSchemaOutdated
		if !errors.As(err, &older) {
			t.Fatalf("want ErrSchemaOutdated, got %v", err)
		}
		if !strings.Contains(err.Error(), "re-run") {
			t.Errorf("the message must name the fix: %v", err)
		}
	})
}

func TestIndexPullRefusesPlantedTriggersAndBrokenGraphs(t *testing.T) {
	gh := installFakeGH(t)
	root := indexedRepo(t)
	sha := headOf(t, root)

	cases := []struct {
		name   string
		mutate func(db *store.DB)
		want   string
	}{
		{"trigger", func(db *store.DB) {
			_, err := db.Exec(`CREATE TRIGGER evil AFTER INSERT ON files BEGIN SELECT 1; END`)
			must(t, err)
		}, "trigger evil"},
		{"extra table", func(db *store.DB) {
			_, err := db.Exec(`CREATE TABLE loot (x)`)
			must(t, err)
		}, "table loot"},
		{"dangling edge", func(db *store.DB) {
			_, err := db.Exec(`PRAGMA foreign_keys = OFF`)
			must(t, err)
			_, err = db.Exec(`DELETE FROM symbols WHERE id = (SELECT dst_id FROM edges LIMIT 1)`)
			must(t, err)
		}, "graph invariants"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := exportTo(t, root)
			resign(t, dir, tc.mutate)
			// Counts in the manifest are the gate for the dangling case, so
			// refresh them to isolate the invariant check.
			if tc.name == "dangling edge" {
				db, _ := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "index.db"), ReadOnly: true})
				m := readManifest(t, dir)
				_ = db.QueryRow(`SELECT count(*) FROM symbols`).Scan(&m.Symbols)
				_ = db.Close()
				body, _ := json.Marshal(m)
				must(t, os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0o600))
			}
			gh.serve(t, sha, 7, zipOfExport(t, dir), false)
			other := cloneOf(t, root)
			_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if fileExists(filepath.Join(other, IndexDir, IndexFile)) {
				t.Error("nothing may be installed")
			}
		})
	}
}

func TestIndexPullRefusesHostileZips(t *testing.T) {
	gh := installFakeGH(t)
	root := indexedRepo(t)
	sha := headOf(t, root)
	dir := exportTo(t, root)
	db, _ := os.ReadFile(filepath.Join(dir, "index.db"))
	mf, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	base := []zipEntry{{name: "index.db", data: db}, {name: "manifest.json", data: mf}}

	cases := []struct {
		name  string
		extra []zipEntry
		want  string
	}{
		{"dotdot", []zipEntry{{name: "../x", data: []byte("x")}}, "unexpected entry"},
		{"nested dotdot", []zipEntry{{name: "a/../../x", data: []byte("x")}}, "unexpected entry"},
		{"absolute", []zipEntry{{name: "/etc/passwd", data: []byte("x")}}, "unexpected entry"},
		{"extra", []zipEntry{{name: "extra.txt", data: []byte("x")}}, "unexpected entry"},
		{"subdir", []zipEntry{{name: "sub/index.db", data: []byte("x")}}, "unexpected entry"},
		{"duplicate", []zipEntry{{name: "index.db", data: db}}, "appears twice"},
		{"symlink", nil, "not a regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := append([]zipEntry(nil), base...)
			entries = append(entries, tc.extra...)
			if tc.name == "symlink" {
				entries = []zipEntry{{name: "index.db", data: []byte("/etc/passwd"), mode: os.ModeSymlink | 0o777}, base[1]}
			}
			gh.serve(t, sha, 7, buildZip(t, entries...), false)
			other := cloneOf(t, root)
			_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if fileExists(filepath.Join(other, IndexDir, IndexFile)) {
				t.Error("nothing may be installed")
			}
			// Nothing was written outside the staging area either.
			if fileExists(filepath.Join(filepath.Dir(other), "x")) || fileExists(filepath.Join(other, IndexDir, "x")) {
				t.Error("an entry escaped extraction")
			}
		})
	}

	t.Run("missing manifest", func(t *testing.T) {
		gh.serve(t, sha, 7, buildZip(t, base[0]), false)
		_, err := run(t, cloneOf(t, root), "index", "pull", "--repo", "acme/shop")
		if err == nil || !strings.Contains(err.Error(), "must contain both") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		old := maxArtifactDBBytes
		maxArtifactDBBytes = int64(len(db) / 2)
		defer func() { maxArtifactDBBytes = old }()
		gh.serve(t, sha, 7, buildZip(t, base...), false)
		_, err := run(t, cloneOf(t, root), "index", "pull", "--repo", "acme/shop")
		if err == nil || !strings.Contains(err.Error(), "cap") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("lying size header", func(t *testing.T) {
		// A zip may declare a small size and expand past it; the cap counts
		// the bytes actually read.
		old := maxArtifactManifestBytes
		maxArtifactManifestBytes = 64
		defer func() { maxArtifactManifestBytes = old }()
		gh.serve(t, sha, 7, buildZip(t, base...), false)
		_, err := run(t, cloneOf(t, root), "index", "pull", "--repo", "acme/shop")
		if err == nil || !strings.Contains(err.Error(), "cap") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestIndexPullKeepsAnExistingIndex(t *testing.T) {
	gh := installFakeGH(t)
	root := indexedRepo(t)
	sha := headOf(t, root)
	dir := exportTo(t, root)
	gh.serve(t, sha, 7, zipOfExport(t, dir), false)

	other := cloneOf(t, root)
	// Build a local index there first.
	for _, a := range []string{"init", "index"} {
		if out, err := run(t, other, a); err != nil {
			t.Fatalf("%s: %v\n%s", a, err, out)
		}
	}
	dbPath := filepath.Join(other, IndexDir, IndexFile)
	before, _ := os.ReadFile(dbPath)

	_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want a refusal naming --force, got %v", err)
	}
	if calls, _ := os.ReadFile(filepath.Join(gh.dir, "calls.log")); len(calls) != 0 {
		t.Errorf("the refusal must come before any network call, gh was run:\n%s", calls)
	}

	// A failing forced pull leaves the old index exactly as it was.
	gh.serve(t, sha, 7, []byte("garbage"), false)
	if _, err := run(t, other, "index", "pull", "--repo", "acme/shop", "--force"); err == nil {
		t.Fatal("a corrupt artifact must fail")
	}
	after, _ := os.ReadFile(dbPath)
	if !bytes.Equal(before, after) {
		t.Error("a failed pull changed the existing index")
	}

	// A good forced pull replaces it and removes the backup.
	gh.serve(t, sha, 7, zipOfExport(t, dir), false)
	if out, err := run(t, other, "index", "pull", "--repo", "acme/shop", "--force"); err != nil {
		t.Fatalf("forced pull: %v\n%s", err, out)
	}
	if fileExists(dbPath + ".bak") {
		t.Error("the backup must go once the new index is in place")
	}
	if out, err := run(t, other, "callers", "Save"); err != nil || !strings.Contains(out, "Create") {
		t.Fatalf("callers after forced pull: %v\n%s", err, out)
	}
}

func TestIndexPullFallsBackToAnOlderCommit(t *testing.T) {
	gh := installFakeGH(t)
	root := indexedRepo(t)
	oldSHA := headOf(t, root)
	dir := exportTo(t, root)
	gh.serve(t, oldSHA, 7, zipOfExport(t, dir), false)

	// Two more commits that CI never indexed.
	writeFiles(t, root, map[string]string{"shop.go": artifactFixtureSrc + "\nfunc Later() int { return Save() }\n"})
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "two")
	writeFiles(t, root, map[string]string{"extra.go": "package shop\n"})
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "three")
	newSHA := headOf(t, root)

	other := cloneOf(t, root)
	out, err := run(t, other, "index", "pull", "--repo", "acme/shop")
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2 commits behind") || !strings.Contains(out, shortSHA(newSHA)) || !strings.Contains(out, "graphlume index") {
		t.Errorf("the fallback must say how far behind it is:\n%s", out)
	}
	// Nothing was run automatically: the new function is not yet known.
	if callers, _ := run(t, other, "callers", "Save"); strings.Contains(callers, "Later") {
		t.Errorf("pull without --update must not index:\n%s", callers)
	}
	// The normal incremental index catches up.
	if out, err := run(t, other, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	if callers, _ := run(t, other, "callers", "Save"); !strings.Contains(callers, "Later") {
		t.Errorf("incremental index did not catch up:\n%s", callers)
	}

	// --update does it in one step.
	other2 := cloneOf(t, root)
	if out, err := run(t, other2, "index", "pull", "--repo", "acme/shop", "--update"); err != nil {
		t.Fatalf("pull --update: %v\n%s", err, out)
	}
	if callers, _ := run(t, other2, "callers", "Save"); !strings.Contains(callers, "Later") {
		t.Errorf("--update did not index:\n%s", callers)
	}

	// --lookback 1 cannot reach commit one.
	other3 := cloneOf(t, root)
	_, err = run(t, other3, "index", "pull", "--repo", "acme/shop", "--lookback", "1")
	if err == nil || !strings.Contains(err.Error(), "no index artifact") {
		t.Fatalf("lookback bound ignored: %v", err)
	}
}

func TestIndexPullGHProblemsAreExplained(t *testing.T) {
	root := indexedRepo(t)
	other := cloneOf(t, root)

	t.Run("gh missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
		if err == nil || !strings.Contains(err.Error(), "gh auth login") || !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("not logged in", func(t *testing.T) {
		installFakeGH(t)
		t.Setenv("FAKE_GH_AUTH", "1")
		_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
		if err == nil || !strings.Contains(err.Error(), "gh auth login") || !strings.Contains(err.Error(), "not logged in") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		gh := installFakeGH(t)
		gh.serve(t, headOf(t, root), 7, []byte("x"), true)
		_, err := run(t, other, "index", "pull", "--repo", "acme/shop", "--lookback", "0")
		if err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("none at all", func(t *testing.T) {
		installFakeGH(t)
		_, err := run(t, other, "index", "pull", "--repo", "acme/shop")
		if err == nil || !strings.Contains(err.Error(), "no index artifact") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("artifact from a run of another commit", func(t *testing.T) {
		gh := installFakeGH(t)
		sha := headOf(t, root)
		name := indexArtifactPrefix + sha
		list := fmt.Sprintf(`{"artifacts":[{"id":7,"name":%q,"expired":false,"workflow_run":{"head_sha":"%s"}}]}`, name, strings.Repeat("a", 40))
		must(t, os.WriteFile(filepath.Join(gh.dir, "list-"+name+".json"), []byte(list), 0o600))
		_, err := run(t, other, "index", "pull", "--repo", "acme/shop", "--lookback", "0")
		if err == nil || !strings.Contains(err.Error(), "no index artifact") {
			t.Fatalf("an artifact whose run is for another commit must be ignored: %v", err)
		}
	})
}

func TestOriginRepoParsesRemoteForms(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/shop.git":          "acme/shop@github.com",
		"https://github.com/acme/shop":              "acme/shop@github.com",
		"https://user:tok@github.com/acme/shop.git": "acme/shop@github.com",
		"git@github.com:acme/shop.git":              "acme/shop@github.com",
		"ssh://git@ghe.example.com/acme/shop.git":   "acme/shop@ghe.example.com",
	} {
		m := remoteRepo.FindStringSubmatch(url)
		if m == nil {
			t.Errorf("%s did not parse", url)
			continue
		}
		if got := m[2] + "/" + m[3] + "@" + m[1]; got != want {
			t.Errorf("%s -> %s, want %s", url, got, want)
		}
	}
}
