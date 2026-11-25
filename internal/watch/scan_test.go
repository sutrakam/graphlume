package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// The scanning backend with no kernel hints at all: the sweep alone has to
// find everything. That is its worst case and the one that runs the same on
// every platform, so it is the one tested here.
func newPollingBackend(t *testing.T) *scanBackend {
	t.Helper()
	b := newScanBackend(nil, 10*time.Millisecond)
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// expect reads events until one for name with op arrives.
func expect(t *testing.T, b *scanBackend, name string, op fsnotify.Op) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-b.Events():
			if !ok {
				t.Fatalf("backend closed while waiting for %s %s", op, name)
			}
			if ev.Name == name && ev.Op.Has(op) {
				return
			}
		case <-deadline:
			t.Fatalf("no %s event for %s within 10 s", op, name)
		}
	}
}

// What fsnotify's kqueue backend needed a descriptor per file to see: a file
// rewritten where it lies, which changes nothing about its directory.
func TestScanBackendSeesAnInPlaceWrite(t *testing.T) {
	t.Parallel()
	dir := tempRoot(t)
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newPollingBackend(t)
	if err := b.Add(dir); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nfunc A() {}\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	expect(t, b, file, fsnotify.Write)

	// Same size, new time: still a write.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	expect(t, b, file, fsnotify.Write)
}

func TestScanBackendSeesCreateRemoveAndRename(t *testing.T) {
	t.Parallel()
	dir := tempRoot(t)
	old := filepath.Join(dir, "old.go")
	if err := os.WriteFile(old, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newPollingBackend(t)
	if err := b.Add(dir); err != nil {
		t.Fatal(err)
	}

	created := filepath.Join(dir, "new.go")
	if err := os.WriteFile(created, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expect(t, b, created, fsnotify.Create)

	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	expect(t, b, sub, fsnotify.Create)

	// A rename is a removal and a creation, which is what the layer above
	// pairs back into a rename.
	moved := filepath.Join(dir, "moved.go")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	expect(t, b, moved, fsnotify.Create)

	if err := os.Remove(created); err != nil {
		t.Fatal(err)
	}
	expect(t, b, created, fsnotify.Remove)
}

// A directory is covered from Add, and what was already in it is not news.
func TestScanBackendReportsNothingForWhatWasAlreadyThere(t *testing.T) {
	t.Parallel()
	dir := tempRoot(t)
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := newPollingBackend(t)
	if err := b.Add(dir); err != nil {
		t.Fatal(err)
	}
	// Adding again must not forget the snapshot; see scanBackend.Add.
	if err := b.Add(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-b.Events():
		t.Errorf("an event for an unchanged directory: %v", ev)
	case <-time.After(150 * time.Millisecond): // fifteen sweeps
	}

	if err := b.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(dir); !errors.Is(err, fsnotify.ErrNonExistentWatch) {
		t.Errorf("removing an unwatched directory: err = %v", err)
	}
	if n := b.watchedCount(); n != 0 {
		t.Errorf("%d directories still watched after Remove", n)
	}
}

// A directory that is deleted stops being watched without anyone saying so.
func TestScanBackendDropsADeletedDirectory(t *testing.T) {
	t.Parallel()
	root := tempRoot(t)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b := newPollingBackend(t)
	for _, d := range []string{root, sub} {
		if err := b.Add(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(sub); err != nil {
		t.Fatal(err)
	}
	expect(t, b, sub, fsnotify.Remove)
	deadline := time.Now().Add(10 * time.Second)
	for b.watchedCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("%d directories watched, want only the root", b.watchedCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The whole watcher over the scanning backend, end to end, on every platform:
// an in-place write becomes a change batch.
func TestWatcherOverTheScanningBackend(t *testing.T) {
	t.Parallel()
	root := tempRoot(t)
	file := filepath.Join(root, "pkg", "a.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := New(root, Options{
		Debounce: 20 * time.Millisecond,
		backend:  newScanBackend(nil, 10*time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if w.WatchedDirs() != 2 {
		t.Fatalf("watching %d directories, want the root and pkg", w.WatchedDirs())
	}

	if err := os.WriteFile(file, []byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case batch := <-w.Changes():
		if len(batch) != 1 || batch[0].Path != "pkg/a.go" {
			t.Errorf("batch = %+v, want one change to pkg/a.go", batch)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no change batch for an in-place write within 10 s")
	}
}

// Start-up must be interruptible: Ctrl-C nine seconds into registering a
// 7,100-file tree must not take 51 seconds to exit for want of a context. A
// cancelled context stops it at the next directory and leaves nothing
// running.
func TestNewStopsWhenItsContextIsCancelled(t *testing.T) {
	root := tempRoot(t)
	for _, d := range []string{"a", "b", "c", "d"} {
		if err := os.MkdirAll(filepath.Join(root, d, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel from inside start-up, once two directories are registered.
	b := &hookedBackend{fakeBackend: newFakeBackend()}
	b.onAdd = func(n int) {
		if n == 2 {
			cancel()
		}
	}
	w, err := New(root, Options{Context: ctx, backend: b, Clock: NewManualClock(epoch)})
	if err == nil {
		_ = w.Close()
		t.Fatal("New finished registering the tree after its context was cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the context's", err)
	}
	if b.adds > 2 {
		t.Errorf("%d directories were registered after cancellation at 2; start-up did not stop", b.adds)
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if !closed {
		t.Error("the backend was left open by a start that was cancelled")
	}
}

// hookedBackend is the fake backend with a callback on each Add.
type hookedBackend struct {
	*fakeBackend
	adds  int
	onAdd func(n int)
}

func (h *hookedBackend) Add(name string) error {
	h.adds++
	if h.onAdd != nil {
		h.onAdd(h.adds)
	}
	return h.fakeBackend.Add(name)
}

// A slow start says it is still starting.
func TestNewReportsProgressWhileRegistering(t *testing.T) {
	root := tempRoot(t)
	for _, d := range []string{"a", "b", "c", "d", "e"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var reports []int
	w, err := New(root, Options{
		backend: newFakeBackend(), Clock: NewManualClock(epoch),
		Progress:      func(n int) { reports = append(reports, n) },
		ProgressEvery: time.Nanosecond, // every directory, for the test
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if len(reports) < 5 {
		t.Fatalf("progress was reported %d times over 6 directories: %v", len(reports), reports)
	}
	for i := 1; i < len(reports); i++ {
		if reports[i] < reports[i-1] {
			t.Errorf("progress went backwards: %v", reports)
		}
	}
	if last := reports[len(reports)-1]; last > w.WatchedDirs() {
		t.Errorf("progress reported %d directories; %d are watched", last, w.WatchedDirs())
	}
}
