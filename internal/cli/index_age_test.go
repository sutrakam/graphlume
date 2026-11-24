package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/store"
)

// ageRun rewrites the committed run's timestamps to a day ago, which is what
// an index built yesterday holds.
func ageRun(t *testing.T, root string) int64 {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(root, IndexDir, IndexFile)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	then := time.Now().Add(-24 * time.Hour).Unix()
	if _, err := db.ExecContext(ctx,
		`UPDATE index_runs SET started_unix = ?, finished_unix = ? WHERE committed = 1`, then, then+5); err != nil {
		t.Fatal(err)
	}
	return then
}

func lastIndexed(t *testing.T, root string) time.Time {
	t.Helper()
	raw, err := run(t, root, "--json", "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, raw)
	}
	var st struct {
		LastIndexed string `json:"last_indexed"`
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("status --json: %v\n%s", err, raw)
	}
	at, err := time.Parse(time.RFC3339, st.LastIndexed)
	if err != nil {
		t.Fatalf("last_indexed %q: %v", st.LastIndexed, err)
	}
	return at
}

func provenanceAge(t *testing.T, root string) int64 {
	t.Helper()
	raw, err := run(t, root, "--json", "tool", "call", "overview")
	if err != nil {
		t.Fatalf("tool call: %v\n%s", err, raw)
	}
	var out struct {
		Result struct {
			Provenance struct {
				AgeSeconds int64 `json:"age_seconds"`
			} `json:"provenance"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("tool call --json: %v\n%s", err, raw)
	}
	return out.Result.Provenance.AgeSeconds
}

// The index age never moved after the first full run: `status` printed that
// run's time as "Last indexed" and every tool answer's provenance counted its
// age from it, however recently `graphlume index` had brought the index up to
// date. Both must follow the update, whether or not the update found anything
// to change: a pass that looked at every file and found nothing has made the
// index exactly as current as one that found something.
func TestAnIncrementalIndexMovesTheIndexAge(t *testing.T) {
	root := queryFixture(t)
	built := ageRun(t, root)

	if at := lastIndexed(t, root); at.Unix() > built+60 {
		t.Fatalf("fixture: last_indexed = %v, want about a day ago", at)
	}
	if age := provenanceAge(t, root); age < 23*3600 {
		t.Fatalf("fixture: provenance age = %ds, want about a day", age)
	}

	for _, step := range []struct {
		name string
		edit func()
		want string
	}{
		{"nothing changed", func() {}, "is up to date"},
		{"one file edited", func() {
			path := filepath.Join(root, "svc", "unrelated.go")
			body, err := os.ReadFile(path) //nolint:gosec // test fixture inside t.TempDir
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(body, "\nfunc Extra() {}\n"...), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "Modified:  1"},
	} {
		ageRun(t, root)
		step.edit()
		before := time.Now().Add(-2 * time.Second)

		out, err := run(t, root, "index")
		if err != nil || !strings.Contains(out, step.want) {
			t.Fatalf("%s: index: %v\n%s", step.name, err, out)
		}
		if at := lastIndexed(t, root); at.Before(before) {
			t.Errorf("%s: status last_indexed = %v after an update at about %v", step.name, at, before)
		}
		if age := provenanceAge(t, root); age > 60 {
			t.Errorf("%s: provenance age_seconds = %d after an update just now", step.name, age)
		}
	}
}

// --rehash and --full answer different questions and are refused together; on
// its own --rehash is an ordinary update that happens to read every file.
func TestIndexRehashIsAnIncrementalUpdate(t *testing.T) {
	root := queryFixture(t)
	out, err := run(t, root, "index", "--rehash")
	if err != nil || !strings.Contains(out, "is up to date") {
		t.Fatalf("index --rehash: %v\n%s", err, out)
	}
	if _, err := run(t, root, "index", "--full", "--rehash"); err == nil {
		t.Error("--full --rehash was accepted")
	}
}
