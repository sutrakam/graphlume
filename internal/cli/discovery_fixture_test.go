package cli

import (
	stdctx "context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// fixtureIndex builds the public demo-workspace fixture with its own script into a
// temporary directory and indexes it.
func fixtureIndex(t *testing.T) string {
	t.Helper()
	for _, bin := range []string{"git", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("the fixture script needs %s", bin)
		}
	}
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(file), "testdata", "demo-workspace", "make-workspace.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("fixture script not found: %v", err)
	}
	t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "ws")
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(root)); err == nil {
		root = filepath.Join(resolved, "ws")
	}
	if out, err := exec.Command("bash", script, root).CombinedOutput(); err != nil { //nolint:gosec // the repository's own fixture script
		t.Fatalf("make-workspace: %v\n%s", err, out)
	}
	if out, err := run(t, root, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := run(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	return root
}

type fixtureAnswer struct {
	Text    string          `json:"text"`
	Result  json.RawMessage `json:"result"`
	IsError bool            `json:"is_error"`
}

type fixturePage struct {
	Offset     int `json:"offset"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	Returned   int `json:"returned"`
	NextOffset int `json:"next_offset"`
}

type fixtureResult struct {
	Summary string       `json:"summary"`
	Page    *fixturePage `json:"page"`
	Items   []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Path string `json:"path"`
		Why  string `json:"why"`
	} `json:"items"`
}

func callFixtureTool(t *testing.T, root string, args ...string) (fixtureAnswer, fixtureResult) {
	t.Helper()
	raw, err := run(t, root, append([]string{"--json", "tool", "call"}, args...)...)
	var a fixtureAnswer
	if jerr := json.Unmarshal([]byte(raw), &a); jerr != nil {
		t.Fatalf("%v: %v / %v\n%s", args, err, jerr, raw)
	}
	if a.IsError {
		t.Fatalf("%v failed:\n%s", args, a.Text)
	}
	var r fixtureResult
	_ = json.Unmarshal(a.Result, &r)
	return a, r
}

func fixtureRegistry(t *testing.T, root string) (*registry.Registry, int64) {
	t.Helper()
	ctx := stdctx.Background()
	db, err := openReadOnly(ctx, projectIndexPath(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var runID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM index_runs WHERE committed = 1`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	return registry.New(db), runID
}

// noCounts hides OutgoingEdgeCounts, so buildTour walks every entry point.
type noCounts struct{ tourSource }

func TestDiscoveryToolsOnTheDemoWorkspace(t *testing.T) {
	root := fixtureIndex(t)
	ctx := stdctx.Background()

	t.Run("endpoints pages and filters", func(t *testing.T) {
		_, all := callFixtureTool(t, root, "endpoints")
		if all.Page == nil || all.Page.Total < 3 || all.Page.Total != len(all.Items) {
			t.Fatalf("default answer: page %+v, %d items", all.Page, len(all.Items))
		}
		_, p := callFixtureTool(t, root, "endpoints", "limit=2", "offset=1")
		if p.Page.Total != all.Page.Total || len(p.Items) != 2 || p.Page.NextOffset != 3 {
			t.Fatalf("page: %+v %d items", p.Page, len(p.Items))
		}
		if p.Items[0].Name != all.Items[1].Name || p.Items[1].Name != all.Items[2].Name {
			t.Errorf("offset 1 does not continue the default order")
		}
		dir := strings.SplitAfterN(all.Items[0].Path, "/", 3)
		within := dir[0] + dir[1]
		_, w := callFixtureTool(t, root, "endpoints", "within="+within)
		for _, it := range w.Items {
			if !strings.HasPrefix(it.Path, within) {
				t.Errorf("within %s kept %s", within, it.Path)
			}
		}
		if len(w.Items) == 0 {
			t.Errorf("within %s kept nothing", within)
		}
		word := strings.ToUpper(strings.Fields(all.Items[0].Name)[1])
		_, q := callFixtureTool(t, root, "endpoints", "query="+word)
		if len(q.Items) == 0 {
			t.Errorf("query %q matched nothing", word)
		}
		for _, it := range q.Items {
			if !strings.Contains(strings.ToLower(it.Name+" "+it.Why), strings.ToLower(word)) {
				t.Errorf("query %q kept %s", word, it.Name)
			}
		}
	})

	t.Run("tables pages and filters", func(t *testing.T) {
		_, all := callFixtureTool(t, root, "tables")
		if all.Page == nil || all.Page.Total == 0 || all.Page.Total != len(all.Items) {
			t.Fatalf("default answer: page %+v, %d items", all.Page, len(all.Items))
		}
		_, p := callFixtureTool(t, root, "tables", "limit=1")
		if len(p.Items) != 1 || p.Page.NextOffset != 1 || p.Page.Total != all.Page.Total {
			t.Errorf("limit=1: %+v", p.Page)
		}
		name := all.Items[0].Name
		_, q := callFixtureTool(t, root, "tables", "query="+strings.ToUpper(name[:3]))
		found := false
		for _, it := range q.Items {
			found = found || it.Name == name
			if !strings.Contains(it.Name, name[:3]) {
				t.Errorf("query kept %s", it.Name)
			}
		}
		if !found {
			t.Errorf("query %q lost %s", name[:3], name)
		}
		_, none := callFixtureTool(t, root, "tables", "within=no/such/folder/")
		if len(none.Items) != 0 {
			t.Errorf("a folder with no code kept %d tables", len(none.Items))
		}
	})

	t.Run("config lists keys without a key", func(t *testing.T) {
		_, all := callFixtureTool(t, root, "config")
		if all.Page == nil || all.Page.Total == 0 {
			t.Fatalf("listing: %+v", all.Page)
		}
		reg, runID := fixtureRegistry(t, root)
		keys, err := reg.ConfigReport(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if all.Page.Total != len(keys) {
			t.Errorf("listing total %d, report holds %d keys", all.Page.Total, len(keys))
		}
		seen := map[string]bool{}
		for _, it := range all.Items {
			st := strings.SplitN(it.Why, ":", 2)[0]
			seen[st] = true
			if it.Kind != "config_key" {
				t.Errorf("kind %q", it.Kind)
			}
		}
		for _, st := range []string{"undeclared", "unread", "declared"} {
			_, f := callFixtureTool(t, root, "config", "status="+st, "limit=200")
			for _, it := range f.Items {
				if !strings.HasPrefix(it.Why, st+":") {
					t.Errorf("status=%s kept %s (%s)", st, it.Name, it.Why)
				}
			}
		}
		_, und := callFixtureTool(t, root, "config", "status=undeclared")
		if len(und.Items) == 0 {
			t.Fatal("the fixture reads a key it declares nowhere")
		}
		// The single-key lookup is unchanged.
		_, one := callFixtureTool(t, root, "config", "key="+und.Items[0].Name)
		if len(one.Items) != 1 || !strings.Contains(one.Summary, "not declared anywhere") || one.Page != nil {
			t.Errorf("key lookup: %+v", one)
		}
	})

	t.Run("events answers what the command prints", func(t *testing.T) {
		a, _ := callFixtureTool(t, root, "events")
		var got struct {
			Total  int               `json:"total"`
			Topics []json.RawMessage `json:"topics"`
		}
		if err := json.Unmarshal(a.Result, &got); err != nil {
			t.Fatal(err)
		}
		raw, err := run(t, root, "--json", "events")
		if err != nil {
			t.Fatal(err)
		}
		var want []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		if got.Total == 0 || got.Total != len(want) || len(got.Topics) != len(want) {
			t.Fatalf("tool lists %d of %d topics, command %d", len(got.Topics), got.Total, len(want))
		}
		for i := range want {
			var g, w any
			_ = json.Unmarshal(got.Topics[i], &g)
			_ = json.Unmarshal(want[i], &w)
			if !reflect.DeepEqual(g, w) {
				t.Errorf("topic %d differs:\n%s\n%s", i, got.Topics[i], want[i])
			}
		}
	})

	t.Run("tour pruning walks to the same tour", func(t *testing.T) {
		reg, runID := fixtureRegistry(t, root)
		for _, o := range []tourOptions{
			{Stops: 8, Depth: 2, Width: 5, MinConfidence: model.ConfidenceHigh},
			{Stops: 1, Depth: 2, Width: 1, MinConfidence: model.ConfidenceLow},
			{Stops: 3, Depth: 3, Width: 2, MinConfidence: model.ConfidenceMedium},
			{Stops: 50, Depth: 1, Width: 20, MinConfidence: model.ConfidenceLow},
		} {
			pruned, err := buildTour(ctx, reg, runID, o)
			if err != nil {
				t.Fatal(err)
			}
			full, err := buildTour(ctx, noCounts{reg}, runID, o)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pruned, full) {
				t.Errorf("%+v: pruned tour differs\n%+v\n%+v", o, pruned, full)
			}
			if len(full) == 0 {
				t.Errorf("%+v: the fixture has entry points", o)
			}
		}
		// The tool lays the tour out as the command does.
		a, _ := callFixtureTool(t, root, "tour")
		text, err := run(t, root, "tour")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(a.Text, strings.TrimRight(text, "\n")) {
			t.Errorf("tool text differs from the command's:\n%s\n---\n%s", a.Text, text)
		}
	})

	t.Run("blindspot reads match the reports they replace", func(t *testing.T) {
		reg, runID := fixtureRegistry(t, root)
		keys, err := reg.ConfigReport(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		var want []registry.ConfigKeyUsage
		for _, k := range keys {
			if !k.Declared && k.Readers > 0 {
				want = append(want, k)
			}
		}
		got, err := reg.UndeclaredConfigReads(ctx, runID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("undeclared reads\n got %+v\nwant %+v", got, want)
		}
		if one, _ := reg.UndeclaredConfigReads(ctx, runID, 1); len(one) != 1 || one[0] != want[0] {
			t.Errorf("limit 1: %+v", one)
		}

		// The reordered queries return what the queries they replaced did.
		db, err := openReadOnly(ctx, projectIndexPath(root))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		ids := func(q string, args ...any) []int64 {
			rows, err := db.QueryContext(ctx, q, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			var out []int64
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				out = append(out, id)
			}
			return out
		}
		for _, limit := range []int{1, 5, 50, 100000} {
			oldRefs := ids(`SELECT sr.id FROM symbol_references sr JOIN files f ON f.id = sr.file_id
				 WHERE sr.index_run_id = ? AND sr.resolution IN ('unresolved', 'ambiguous')
				 ORDER BY f.path, sr.start_line, sr.start_col, sr.id LIMIT ?`, runID, limit)
			refs, err := reg.UnresolvedReferences(ctx, runID, limit)
			if err != nil {
				t.Fatal(err)
			}
			var newRefs []int64
			for _, r := range refs {
				newRefs = append(newRefs, r.RefID)
			}
			if !reflect.DeepEqual(newRefs, oldRefs) {
				t.Errorf("unresolved references, limit %d: %v, want %v", limit, newRefs, oldRefs)
			}
			oldFan := ids(`SELECT s.id FROM symbols s JOIN files f ON f.id = s.file_id
				  CROSS JOIN edges e INDEXED BY idx_edges_dst ON e.index_run_id = s.index_run_id AND e.dst_id = s.id
				 WHERE s.index_run_id = ? AND s.exported = 1 AND s.kind IN ('function','method')
				 GROUP BY s.id HAVING COUNT(*) >= ? ORDER BY COUNT(*) DESC, s.id LIMIT ?`, runID, 2, limit)
			fan, err := reg.HighFanInSymbols(ctx, runID, 2, limit)
			if err != nil {
				t.Fatal(err)
			}
			var newFan []int64
			for _, f := range fan {
				newFan = append(newFan, f.ID)
			}
			if !reflect.DeepEqual(newFan, oldFan) {
				t.Errorf("high fan-in, limit %d: %v, want %v", limit, newFan, oldFan)
			}
		}
	})
}
