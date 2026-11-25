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
	"time"

	"github.com/sutrakam/graphlume/internal/capability/ownership"
)

// demoWorkspace builds the public demo-workspace fixture (26 commits by four
// authors) with its own script and indexes it.
func demoWorkspace(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("the fixture script needs git")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("the fixture script needs bash")
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
	for _, cmd := range []string{"init", "index"} {
		if out, err := run(t, root, cmd); err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
	}
	return root
}

type toolAnswer struct {
	Text    string          `json:"text"`
	Result  json.RawMessage `json:"result"`
	IsError bool            `json:"is_error"`
}

// historyAnswers runs every history-backed tool once and returns their
// structured results, keyed by call.
func historyAnswers(t *testing.T, root string) map[string]string {
	t.Helper()
	calls := map[string][]string{
		"hotspots":      {"hotspots"},
		"busfactor":     {"busfactor", "min_commits=1"},
		"busfactor-200": {"busfactor", "min_commits=1", "commits=200"},
		"owners":        {"owners", "symbol=Ledger"},
		"changes":       {"changes", "base=HEAD~3"},
	}
	out := map[string]string{}
	for name, args := range calls {
		raw, err := run(t, root, append([]string{"--json", "tool", "call"}, args...)...)
		var a toolAnswer
		if jerr := json.Unmarshal([]byte(raw), &a); jerr != nil {
			t.Fatalf("%s: %v / %v\n%s", name, err, jerr, raw)
		}
		if a.IsError || strings.Contains(a.Text, "No index has been built") {
			t.Fatalf("%s did not answer:\n%s", name, a.Text)
		}
		out[name] = a.Text
	}
	return out
}

// TestHistoryWalkOnTheDemoWorkspace shares one indexed fixture between the
// history-walk checks, since building it is most of their cost.
func TestHistoryWalkOnTheDemoWorkspace(t *testing.T) {
	root := demoWorkspace(t)
	t.Run("answers are unchanged", func(t *testing.T) { historyToolsAnswerTheSame(t, root) })
	t.Run("a budgeted walk continues", func(t *testing.T) { historyWalkContinuesAfterItsBudgetRunsOut(t, root) })
}

// The faster history walk (git binary, cache, continuation) must not change
// any answer. The baseline is the walk as it was: go-git, no cache file.
func historyToolsAnswerTheSame(t *testing.T, root string) {
	cache := filepath.Join(root, IndexDir, "history-cache.json")

	t.Setenv("GRAPHLUME_HISTORY_WALK", "go-git")
	_ = os.Remove(cache)
	before := historyAnswers(t, root)

	t.Setenv("GRAPHLUME_HISTORY_WALK", "")
	_ = os.Remove(cache)
	after := historyAnswers(t, root)
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("no history cache was written: %v", err)
	}
	cached := historyAnswers(t, root)

	for name, want := range before {
		if after[name] != want {
			t.Errorf("%s changed with the git-binary walk:\n--- go-git\n%s\n--- binary\n%s", name, want, after[name])
		}
		if cached[name] != want {
			t.Errorf("%s changed when answered from the history cache:\n--- go-git\n%s\n--- cached\n%s", name, want, cached[name])
		}
	}
}

// A walk stopped by its budget returns the newest commits marked as partial,
// is kept, and is continued by the next call until it is whole; the whole walk
// is then identical to one made without a budget.
func historyWalkContinuesAfterItsBudgetRunsOut(t *testing.T, root string) {
	_ = os.Remove(filepath.Join(root, IndexDir, "history-cache.json"))
	g := &Global{}
	g.cfg.RepoRoot = root
	h := openHistory(g)
	if h.repo == nil {
		t.Fatalf("no history: %s", h.note)
	}
	ctx := stdctx.Background()

	saved := historyWalkBudget
	t.Cleanup(func() { historyWalkBudget = saved })

	historyWalkBudget = time.Nanosecond
	first, err := h.Commits(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || len(first) >= 20 || !first[len(first)-1].Truncated {
		t.Fatalf("a spent budget should return a non-empty partial walk ending in a truncated commit; got %d", len(first))
	}

	// Keep calling with the tiny budget: each call reads at least one more.
	var got []ownership.Commit
	for i := 0; i < 40; i++ {
		got, err = h.Commits(ctx, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 20 || (len(got) > 0 && !got[len(got)-1].Truncated) {
			break
		}
	}

	historyWalkBudget = saved
	fresh := &gitHistory{repo: h.repo, prefix: h.prefix}
	want, err := fresh.Commits(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("continued walk (%d commits) differs from one whole walk (%d commits)", len(got), len(want))
	}
}
