package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// kind returns how Diff classified one path.
func kind(t *testing.T, changes []FileChange, path string) FileChange {
	t.Helper()
	for _, c := range changes {
		if c.Path == path {
			return c
		}
	}
	t.Fatalf("%s is not in the diff", path)
	return FileChange{}
}

// restamp rewrites a file with new content and puts its modification time
// back to what the index recorded, to the second.
func (h *harness) restamp(rel, body string) {
	h.t.Helper()
	files, err := h.reg.Files(context.Background(), h.runID)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, f := range files {
		if f.Path != rel {
			continue
		}
		h.write(rel, body)
		at := time.Unix(f.ModTimeUnix, 0)
		if err := os.Chtimes(filepath.Join(h.root, filepath.FromSlash(rel)), at, at); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	h.t.Fatalf("%s is not indexed", rel)
}

// THE CORRECTNESS RULE, at the level a user meets it: a file whose content
// changed is never reported unchanged, including the hardest ordinary case, a
// same-size edit landing in the same second as the save the index recorded.
//
// Nothing here waits, and nothing depends on how fast the machine is. The run
// is recorded as having begun in the very second the file was last written,
// which is the save-then-index a watcher performs all day. The file is then
// rewritten at the same size and its modification time forced back onto the
// recorded second, so that size and time are identical on every filesystem.
// The diff must still call it modified, because a time that close to the pass
// that recorded it is not believed (rule 4 in internal/acquire/fs/trust.go).
// On Windows that rule is the only one there is, so this is also the test
// that it is enough.
func TestASameSizeEditInTheSameSecondIsNeverMissed(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{"a.go": "package p\n\nfunc One() int { return 1 }\n"})
	files, err := h.reg.Files(ctx, h.runID)
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, %v", files, err)
	}
	if err := h.reg.MarkUpdated(ctx, h.runID, files[0].ModTimeUnix); err != nil {
		t.Fatal(err)
	}
	h.restamp("a.go", "package p\n\nfunc Two() int { return 2 }\n")

	changes, err := Diff(ctx, h.reg, h.runID, h.walker, harnessBuild)
	if err != nil {
		t.Fatal(err)
	}
	if c := kind(t, changes, "a.go"); c.Kind != ChangeModified {
		t.Fatalf("a.go classified %q: a same-size edit in the second it was indexed was missed", c.Kind)
	}
	h.sync()
	if names := h.symbolNames(); names["Two"] == "" || names["One"] != "" {
		t.Errorf("symbols after the edit = %v, want Two and not One", names)
	}
}

// What the saving rests on, and its escape hatch.
//
// The run is marked as verified by a pass that began well after the file was
// last written, which is the state of any index a minute after `graphlume
// index`. The file's content is then replaced behind the index's back, with
// its size and modification time left exactly as recorded and, because the
// pass is dated in the future, an inode change time that also looks settled.
// That is the one arrangement the rule cannot see through, constructed on
// purpose: it shows the file really is not opened, and that Rehash opens it.
func TestSettledFilesAreNotReadAndRehashReadsThem(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{"a.go": "package p\n\nfunc One() int { return 1 }\n"})
	h.restamp("a.go", "package p\n\nfunc Two() int { return 2 }\n")

	// As if a later pass had begun ten seconds from now.
	if err := h.reg.MarkUpdated(ctx, h.runID, time.Now().Unix()+10); err != nil {
		t.Fatal(err)
	}

	trusted, err := Diff(ctx, h.reg, h.runID, h.walker, harnessBuild)
	if err != nil {
		t.Fatal(err)
	}
	if c := kind(t, trusted, "a.go"); c.Kind != ChangeSame {
		t.Fatalf("a.go classified %q: a file with settled, matching size and time was read", c.Kind)
	}

	rehashed, err := DiffWith(ctx, h.reg, h.runID, h.walker, harnessBuild, DiffOptions{Rehash: true})
	if err != nil {
		t.Fatal(err)
	}
	if c := kind(t, rehashed, "a.go"); c.Kind != ChangeModified {
		t.Fatalf("a.go classified %q with Rehash: every file must be read", c.Kind)
	}
}

// A different build re-parses everything whatever the times say. Trusting a
// recorded hash there would bring back the stale-analysis bug the
// build-crossing rule exists for.
func TestADifferentBuildIsNeverTrustedByTimes(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{"a.go": "package p\n\nfunc One() {}\n"})
	if err := h.reg.MarkUpdated(ctx, h.runID, time.Now().Unix()+10); err != nil {
		t.Fatal(err)
	}
	changes, err := Diff(ctx, h.reg, h.runID, h.walker, "another-build")
	if err != nil {
		t.Fatal(err)
	}
	if c := kind(t, changes, "a.go"); c.Kind != ChangeModified {
		t.Fatalf("a.go classified %q under a different build, want modified", c.Kind)
	}
}

// A touched file is read once, found identical, and its new time recorded, so
// the pass after that does not read it again. Without the record, a branch
// switch and back would leave every file it touched to be re-read on every
// pass for the life of the index.
func TestATouchedFileIsRestatedNotReparsed(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{"a.go": "package p\n\nfunc One() {}\n"})

	moved := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(h.root, "a.go"), moved, moved); err != nil {
		t.Fatal(err)
	}

	changes, err := Diff(ctx, h.reg, h.runID, h.walker, harnessBuild)
	if err != nil {
		t.Fatal(err)
	}
	c := kind(t, changes, "a.go")
	if c.Kind != ChangeSame || !c.Touched || c.ModTimeUnix != moved.Unix() {
		t.Fatalf("a.go = %+v, want unchanged, touched, with the new time", c)
	}
	stats, err := Incremental(ctx, h.reg, h.runID, h.walker, changes, IncrementalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Modified != 0 || stats.Symbols != 0 || stats.Unchanged != 1 {
		t.Errorf("stats = %+v: a touched file was re-parsed", stats)
	}
	files, err := h.reg.Files(ctx, h.runID)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].ModTimeUnix != moved.Unix() {
		t.Errorf("recorded time = %d, want the file's new time %d", files[0].ModTimeUnix, moved.Unix())
	}

	again, err := Diff(ctx, h.reg, h.runID, h.walker, harnessBuild)
	if err != nil {
		t.Fatal(err)
	}
	if c := kind(t, again, "a.go"); c.Touched {
		t.Error("the new time was recorded and the file is still reported as touched")
	}
}

// Refresh is what `index` and `watch` run. A completed pass leaves the run
// saying when it happened and which build did it; otherwise the index keeps the
// times of its first full build for ever, so `status` and every tool answer
// report an index updated a minute ago as weeks old.
func TestRefreshRecordsWhenAndByWhomTheRunWasUpdated(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{"a.go": "package p\n\nfunc One() {}\n"})

	// Age the run: built long ago, by this build.
	if _, err := h.db.ExecContext(ctx,
		`UPDATE index_runs SET started_unix = 1000, finished_unix = 1010 WHERE id = ?`, h.runID); err != nil {
		t.Fatal(err)
	}
	began := time.Now().Unix()

	// Nothing changed. The pass still looked at every file, so the index is
	// as current as that look.
	stats, err := Refresh(ctx, h.reg, h.runID, h.walker, RefreshOptions{RunningAs: harnessBuild})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Unchanged != 1 || stats.Modified != 0 {
		t.Fatalf("stats = %+v, want one unchanged file", stats)
	}
	run, err := h.reg.Run(ctx, h.runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.StartedUnix < began || run.FinishedUnix < run.StartedUnix {
		t.Errorf("after an update the run says started %d, finished %d; want both at or after %d",
			run.StartedUnix, run.FinishedUnix, began)
	}

	// A pass under a new build stamps that build, or every later pass would
	// find a mismatch and re-parse the whole tree again.
	if _, err := Refresh(ctx, h.reg, h.runID, h.walker, RefreshOptions{RunningAs: "newer-build"}); err != nil {
		t.Fatal(err)
	}
	if run, err = h.reg.Run(ctx, h.runID); err != nil || run.ProducerVersion != "newer-build" {
		t.Errorf("producer = %q (%v), want the build that ran the pass", run.ProducerVersion, err)
	}
	changes, err := Diff(ctx, h.reg, h.runID, h.walker, "newer-build")
	if err != nil {
		t.Fatal(err)
	}
	if c := kind(t, changes, "a.go"); c.Kind != ChangeSame {
		t.Errorf("a.go is %q on the pass after a build-crossing one; the stamp was not recorded", c.Kind)
	}
}

// A pass that fails must not move the time later passes trust files against:
// it did not look at everything.
func TestAFailedRefreshDoesNotMoveTheRun(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "package p\n\nfunc One() {}\n"})
	before, err := h.reg.Run(context.Background(), h.runID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Refresh(ctx, h.reg, h.runID, h.walker, RefreshOptions{
		RunningAs: harnessBuild,
		Now:       func() time.Time { return time.Unix(before.StartedUnix+3600, 0) },
	}); err == nil {
		t.Fatal("a cancelled pass reported success")
	}
	after, err := h.reg.Run(context.Background(), h.runID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StartedUnix != before.StartedUnix || after.FinishedUnix != before.FinishedUnix {
		t.Errorf("a failed pass moved the run from %d/%d to %d/%d",
			before.StartedUnix, before.FinishedUnix, after.StartedUnix, after.FinishedUnix)
	}
}

// BenchmarkDiffUnchangedTree is the floor under every update: what it costs to
// learn that nothing changed. "trusting" is the default; "rehash" is what
// every pass did before, and what --rehash still does.
//
//	go test ./internal/pipeline -run '^$' -bench DiffUnchangedTree -cpuprofile cpu.prof
func BenchmarkDiffUnchangedTree(b *testing.B) {
	const files = 2000
	tree := make(map[string]string, files)
	for i := 0; i < files; i++ {
		tree[fmt.Sprintf("pkg%02d/f%04d.go", i%40, i)] = fmt.Sprintf(
			"package pkg%02d\n\n// F%d does nothing %d times.\nfunc F%d() int { return %d }\n", i%40, i, i, i, i)
	}
	h := newHarness(b, tree)
	ctx := context.Background()
	// As if the last pass began after every file was written.
	if err := h.reg.MarkUpdated(ctx, h.runID, time.Now().Unix()+10); err != nil {
		b.Fatal(err)
	}

	for _, mode := range []struct {
		name string
		opts DiffOptions
	}{{"trusting", DiffOptions{}}, {"rehash", DiffOptions{Rehash: true}}} {
		b.Run(mode.name, func(b *testing.B) {
			for b.Loop() {
				changes, err := DiffWith(ctx, h.reg, h.runID, h.walker, harnessBuild, mode.opts)
				if err != nil {
					b.Fatal(err)
				}
				if len(changes) != files {
					b.Fatalf("diff saw %d files, want %d", len(changes), files)
				}
			}
		})
	}
}
