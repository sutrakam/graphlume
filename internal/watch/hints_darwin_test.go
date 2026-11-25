//go:build darwin

package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func openDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skipf("cannot list /dev/fd: %v", err)
	}
	return len(entries)
}

// The point of the scanning backend on macOS: watching a directory costs one
// descriptor, however many files are in it. fsnotify's kqueue backend opened
// one per file, which is what made start-up slow and bounded the size of
// repository that could be watched at all.
func TestMacOSWatchHoldsOneDescriptorPerDirectoryNotPerFile(t *testing.T) {
	root := tempRoot(t)
	const dirs, filesPerDir = 4, 100
	for d := 0; d < dirs; d++ {
		dir := filepath.Join(root, fmt.Sprintf("d%d", d))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < filesPerDir; f++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.go", f)), []byte("package d\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	before := openDescriptors(t)
	w, err := New(root, Options{PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	held := openDescriptors(t) - before

	if w.WatchedDirs() != dirs+1 {
		t.Fatalf("watching %d directories, want %d", w.WatchedDirs(), dirs+1)
	}
	// One per directory, plus the kqueue itself, plus slack for the runtime.
	if limit := dirs + 1 + 4; held > limit {
		t.Errorf("watching %d directories holding %d files took %d descriptors, want at most %d",
			dirs+1, dirs*filesPerDir, held, limit)
	}
}

// With the sweep an hour away, only the kernel's hint can deliver this: a
// save made the way most editors make it, by writing a temporary file and
// renaming it over the original, must arrive at once.
func TestMacOSDirectoryHintsReportAnAtomicSaveWithoutASweep(t *testing.T) {
	dir := tempRoot(t)
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hints, err := newDirHints()
	if err != nil {
		t.Fatal(err)
	}
	b := newScanBackend(hints, time.Hour)
	defer func() { _ = b.Close() }()
	if err := b.Add(dir); err != nil {
		t.Fatal(err)
	}

	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, file); err != nil {
		t.Fatal(err)
	}
	expect(t, b, file, fsnotify.Write)

	created := filepath.Join(dir, "b.go")
	if err := os.WriteFile(created, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expect(t, b, created, fsnotify.Create)
}
