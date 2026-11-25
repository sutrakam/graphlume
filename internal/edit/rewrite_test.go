package edit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	afs "github.com/sutrakam/graphlume/internal/acquire/fs"
)

const (
	fromSrc  = "package app\n\nfunc Keep() {}\n\nfunc Go() {}\n"
	fromDone = "package app\n\nfunc Keep() {}\n"
	toDone   = "package app\n\nfunc Go() {}\n"
)

// loadBoth loads a.go (which must exist) and b.go.
func loadBoth(t *testing.T, ed *Editor) (a, b File) {
	t.Helper()
	a, prob, err := ed.Load("a.go")
	if err != nil || prob != nil || !a.Exists {
		t.Fatalf("Load a.go: err=%v problem=%+v exists=%v", err, prob, a.Exists)
	}
	b, prob, err = ed.Load("b.go")
	if err != nil || prob != nil {
		t.Fatalf("Load b.go: err=%v problem=%+v", err, prob)
	}
	return a, b
}

// moveOut prepares the two halves of a move from a.go into a new b.go, the
// receiving file first.
func moveOut(t *testing.T, ed *Editor) []Prepared {
	t.Helper()
	a, b := loadBoth(t, ed)
	if b.Exists {
		t.Fatal("b.go already exists")
	}
	create, prob := ed.Create(b, []byte(toDone), a)
	if prob != nil {
		t.Fatalf("Create: %+v", prob)
	}
	rewrite, prob := ed.Rewrite(a, []byte(fromDone))
	if prob != nil {
		t.Fatalf("Rewrite: %+v", prob)
	}
	return []Prepared{create, rewrite}
}

func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func TestLoadReadsAFileOrFindsThePathFree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/a.go", fromSrc)
	ed := New(root)

	f, prob, err := ed.Load("pkg/a.go")
	if err != nil || prob != nil {
		t.Fatalf("Load: err=%v problem=%+v", err, prob)
	}
	if !f.Exists || string(f.Data) != fromSrc || f.Path != "pkg/a.go" || f.Hash() != afs.HashBytes([]byte(fromSrc)) {
		t.Errorf("loaded = %+v", f)
	}

	free, prob, err := ed.Load("pkg/new.go")
	if err != nil || prob != nil {
		t.Fatalf("Load of a free path: err=%v problem=%+v", err, prob)
	}
	if free.Exists || free.Data != nil || free.Path != "pkg/new.go" {
		t.Errorf("free path = %+v, want Exists false and no data", free)
	}
	if exists(root, "pkg/new.go") {
		t.Error("Load created the file")
	}
}

func TestLoadRefusesWhatItCannotReadSafely(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/a.go", fromSrc)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "o.go"), []byte(fromSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir.go"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, path string
		opts       []Option
		reason     string
	}{
		{name: "dot-dot escape", path: "../o.go", reason: "'..'"},
		{name: "dot-dot to a path that does not exist", path: "../new.go", reason: "'..'"},
		{name: "absolute path", path: filepath.Join(outside, "o.go"), reason: "relative to the project root"},
		{name: "absolute path that does not exist", path: filepath.Join(outside, "new.go"), reason: "relative to the project root"},
		{name: "the root itself", path: ".", reason: "must name a file"},
		{name: "a directory", path: "dir.go", reason: "not a regular file"},
		{name: "a new file in a directory that does not exist", path: "nowhere/new.go", reason: "nowhere"},
		// The operating systems word this one differently (ENOTDIR on Unix,
		// "not a plain directory" from the component check elsewhere).
		{name: "a new file under a file", path: "pkg/a.go/new.go", reason: "inside the project root"},
		{name: "over the size limit", path: "pkg/a.go", opts: []Option{WithMaxFileBytes(8)}, reason: "byte limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, prob, err := New(root, tc.opts...).Load(tc.path)
			if err != nil {
				t.Fatalf("Load returned an error instead of a problem: %v", err)
			}
			if prob == nil {
				t.Fatalf("Load accepted %q: %+v", tc.path, f)
			}
			if !strings.Contains(prob.Reason, tc.reason) {
				t.Errorf("reason %q does not mention %q", prob.Reason, tc.reason)
			}
		})
	}
}

// TestLoadRefusesSymlinkedPaths: a symlinked file, and a path that would be
// created through a symlinked directory, whether the link leaves the root or
// not.
func TestLoadRefusesSymlinkedPaths(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "real/a.go", fromSrc)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "o.go"), []byte(fromSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(root, "real", "a.go"), filepath.Join(root, "link.go"))
	symlinkOrSkip(t, filepath.Join(root, "real"), filepath.Join(root, "inside"))
	symlinkOrSkip(t, outside, filepath.Join(root, "out"))

	for _, path := range []string{"link.go", "inside/a.go", "inside/new.go", "out/o.go", "out/new.go"} {
		f, prob, err := New(root).Load(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if prob == nil {
			t.Errorf("%s was accepted: %+v", path, f)
		}
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 1 {
		t.Errorf("the directory outside the root was touched: %v", ents)
	}
}

func TestLoadDirListsTheKeptFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/b.go", "package app\n")
	writeFile(t, root, "pkg/a.go", fromSrc)
	writeFile(t, root, "pkg/notes.txt", "x")
	writeFile(t, root, "pkg/sub/c.go", "package sub\n")
	isGo := func(name string) bool { return strings.HasSuffix(name, ".go") }

	files, problems, err := New(root).LoadDir("pkg", isGo)
	if err != nil || len(problems) != 0 {
		t.Fatalf("LoadDir: err=%v problems=%+v", err, problems)
	}
	if len(files) != 2 || files[0].Path != "pkg/a.go" || files[1].Path != "pkg/b.go" || string(files[0].Data) != fromSrc {
		t.Errorf("files = %+v, want pkg/a.go and pkg/b.go in order", files)
	}

	top, problems, err := New(root).LoadDir(".", isGo)
	if err != nil || len(problems) != 0 || len(top) != 0 {
		t.Errorf("LoadDir(.): files=%+v problems=%+v err=%v, want nothing", top, problems, err)
	}

	if _, problems, _ := New(root).LoadDir("../elsewhere", isGo); len(problems) != 1 {
		t.Errorf("an escaping directory gave problems %+v, want one", problems)
	}
	if _, problems, _ := New(root).LoadDir("missing", isGo); len(problems) != 1 {
		t.Errorf("a missing directory gave problems %+v, want one", problems)
	}
}

// TestLoadDirReportsAKeptEntryItCannotRead: a symlinked Go file is part of
// the package as far as the build is concerned, so leaving it out silently
// would hand the caller an incomplete set.
func TestLoadDirReportsAKeptEntryItCannotRead(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/a.go", fromSrc)
	outside := filepath.Join(t.TempDir(), "o.go")
	if err := os.WriteFile(outside, []byte(fromSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(root, "pkg", "linked.go"))

	files, problems, err := New(root).LoadDir("pkg", func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(problems) != 1 || problems[0].Path != "pkg/linked.go" {
		t.Errorf("files=%+v problems=%+v, want a.go read and linked.go a problem", files, problems)
	}
}

func TestRewriteAndCreateCommitTogether(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "a.go", fromSrc)
	ed := New(root)
	prepared := moveOut(t, ed)
	if readFile(t, root, "a.go") != fromSrc || exists(root, "b.go") {
		t.Fatal("preparing wrote to disk")
	}

	if err := ed.Commit(ctx, prepared); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := readFile(t, root, "a.go"); got != fromDone {
		t.Errorf("a.go = %q", got)
	}
	if got := readFile(t, root, "b.go"); got != toDone {
		t.Errorf("b.go = %q", got)
	}
	if runtime.GOOS != "windows" {
		// writeFile makes a.go 0640; the new file beside it gets the same.
		if fi, err := os.Stat(filepath.Join(root, "b.go")); err != nil || fi.Mode().Perm() != 0o640 {
			t.Errorf("b.go mode = %v (%v), want a.go's 0640", fi.Mode(), err)
		}
	}
	noTempFiles(t, root)
}

func TestRewriteAndCreateRefuse(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", fromSrc)
	ed := New(root)
	a, b := loadBoth(t, ed)

	if _, prob := ed.Rewrite(b, []byte(toDone)); prob == nil || !strings.Contains(prob.Reason, "does not exist") {
		t.Errorf("Rewrite of an absent file: %+v", prob)
	}
	if _, prob := ed.Create(a, []byte(toDone), a); prob == nil || !strings.Contains(prob.Reason, "already exists") {
		t.Errorf("Create over an existing file: %+v", prob)
	}
	if _, prob := ed.Rewrite(a, []byte(fromSrc)); prob == nil || !strings.Contains(prob.Reason, "changes nothing") {
		t.Errorf("Rewrite to the same bytes: %+v", prob)
	}
	if _, prob := ed.Rewrite(File{Path: "a.go", Exists: true, Data: []byte(fromSrc)}, []byte(fromDone)); prob == nil {
		t.Error("Rewrite accepted a File the editor never loaded")
	}
	if _, prob := ed.Create(File{Path: "c.go"}, []byte(toDone), a); prob == nil {
		t.Error("Create accepted a File the editor never checked")
	}

	small := New(root, WithMaxFileBytes(int64(len(fromSrc))))
	sa, sb := loadBoth(t, small)
	if _, prob := small.Rewrite(sa, []byte(fromSrc+"// longer\n")); prob == nil || !strings.Contains(prob.Reason, "byte limit") {
		t.Errorf("Rewrite past the limit: %+v", prob)
	}
	if _, prob := small.Create(sb, []byte(fromSrc+"// longer\n"), sa); prob == nil || !strings.Contains(prob.Reason, "byte limit") {
		t.Errorf("Create past the limit: %+v", prob)
	}
}

func TestRewriteRefusesAReadOnlyFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", fromSrc)
	p := filepath.Join(root, "a.go")
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	ed := New(root)
	a, _ := loadBoth(t, ed)
	if _, prob := ed.Rewrite(a, []byte(fromDone)); prob == nil || !strings.Contains(prob.Reason, "read-only") {
		t.Fatalf("problem = %+v, want read-only", prob)
	}
}

// TestCommitUndoesTheCreationWhenTheSecondWriteFails: the new file is in
// place when the source's write fails, and must be gone afterwards.
func TestCommitUndoesTheCreationWhenTheSecondWriteFails(t *testing.T) {
	for _, stage := range []faultStage{stageTempWritten, stageBeforeRename} {
		t.Run(string(stage), func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "a.go", fromSrc)
			ed := New(root)
			prepared := moveOut(t, ed)
			boom := errors.New("disk full")
			var created bool
			ed.fault = func(s faultStage, rel string) error {
				if rel == "a.go" && s == stage {
					created = exists(root, "b.go")
					return boom
				}
				return nil
			}
			err := ed.Commit(context.Background(), prepared)
			if !errors.Is(err, boom) || errors.Is(err, ErrRollbackIncomplete) {
				t.Fatalf("Commit err = %v, want the injected failure and a complete rollback", err)
			}
			if !created {
				t.Fatal("b.go was not created before a.go failed; the rollback was not exercised")
			}
			if exists(root, "b.go") {
				t.Error("the created file was left behind")
			}
			if got := readFile(t, root, "a.go"); got != fromSrc {
				t.Errorf("a.go changed: %q", got)
			}
			noTempFiles(t, root)
		})
	}
}

// TestCommitFailsCleanlyWhenTheCreationItselfFails: nothing was written, and
// no temporary file or half-made target is left.
func TestCommitFailsCleanlyWhenTheCreationItselfFails(t *testing.T) {
	for _, stage := range []faultStage{stageTempWritten, stageBeforeRename} {
		t.Run(string(stage), func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "a.go", fromSrc)
			ed := New(root)
			prepared := moveOut(t, ed)
			boom := errors.New("disk full")
			ed.fault = func(s faultStage, rel string) error {
				if rel == "b.go" && s == stage {
					return boom
				}
				return nil
			}
			if err := ed.Commit(context.Background(), prepared); !errors.Is(err, boom) {
				t.Fatalf("Commit err = %v, want the injected failure", err)
			}
			if exists(root, "b.go") || readFile(t, root, "a.go") != fromSrc {
				t.Error("a failed creation left something behind")
			}
			noTempFiles(t, root)
		})
	}
}

// TestCommitNeverOverwritesAFileThatAppeared: the path was free when the
// edit was planned. Whatever is there now is someone's file.
func TestCommitNeverOverwritesAFileThatAppeared(t *testing.T) {
	const theirs = "package app\n\n// someone else's work\n"
	t.Run("before the commit", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "a.go", fromSrc)
		ed := New(root)
		prepared := moveOut(t, ed)
		writeFile(t, root, "b.go", theirs)
		if err := ed.Commit(context.Background(), prepared); !errors.Is(err, ErrStale) {
			t.Fatalf("Commit err = %v, want ErrStale", err)
		}
		if readFile(t, root, "b.go") != theirs || readFile(t, root, "a.go") != fromSrc {
			t.Error("a file was overwritten")
		}
		noTempFiles(t, root)
	})
	t.Run("between the last check and the link", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "a.go", fromSrc)
		ed := New(root)
		prepared := moveOut(t, ed)
		ed.fault = func(s faultStage, rel string) error {
			if rel == "b.go" && s == stageBeforeRename {
				writeFile(t, root, "b.go", theirs)
			}
			return nil
		}
		err := ed.Commit(context.Background(), prepared)
		if err == nil {
			t.Fatal("Commit succeeded over a file that appeared")
		}
		if got := readFile(t, root, "b.go"); got != theirs {
			// Only a filesystem without hard links can get here: the rename
			// fallback has no atomic no-replace. Say so rather than hide it.
			t.Errorf("b.go was overwritten: %q", got)
		}
		if readFile(t, root, "a.go") != fromSrc {
			t.Error("a.go was changed")
		}
		noTempFiles(t, root)
	})
}

// TestRollbackLeavesACreatedFileSomeoneEdited: the file the commit created
// has been changed by the time the rollback runs. It is not deleted.
func TestRollbackLeavesACreatedFileSomeoneEdited(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", fromSrc)
	ed := New(root)
	prepared := moveOut(t, ed)
	const edited = toDone + "\n// and a human added this\n"
	ed.fault = func(s faultStage, rel string) error {
		if rel == "a.go" && s == stageBeforeRename {
			writeFile(t, root, "b.go", edited)
			return errors.New("injected")
		}
		return nil
	}
	err := ed.Commit(context.Background(), prepared)
	if !errors.Is(err, ErrRollbackIncomplete) || !strings.Contains(err.Error(), "restore b.go") {
		t.Fatalf("Commit err = %v, want an incomplete rollback naming b.go", err)
	}
	if got := readFile(t, root, "b.go"); got != edited {
		t.Errorf("the rollback deleted or changed an edited file: %q", got)
	}
	noTempFiles(t, root)
}

func TestCommitCheckedRollsBackWhenTheCheckFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "a.go", fromSrc)
	ed := New(root)
	prepared := moveOut(t, ed)

	broken := errors.New("go build: undefined: Go")
	var sawWritten, sawLock bool
	err := ed.CommitChecked(ctx, prepared, func(context.Context) error {
		sawWritten = readFile(t, root, "a.go") == fromDone && readFile(t, root, "b.go") == toDone
		sawLock = exists(root, lockName)
		return broken
	})
	if !errors.Is(err, ErrCheckFailed) || !errors.Is(err, broken) || errors.Is(err, ErrRollbackIncomplete) {
		t.Fatalf("CommitChecked err = %v, want ErrCheckFailed wrapping the check's error", err)
	}
	if !sawWritten {
		t.Error("the check did not run against the written files")
	}
	if !sawLock {
		t.Error("the check ran without the root's lock held")
	}
	if got := readFile(t, root, "a.go"); got != fromSrc {
		t.Errorf("a.go was not restored: %q", got)
	}
	if exists(root, "b.go") {
		t.Error("the created file was not removed")
	}
	noTempFiles(t, root)

	// The same plan commits once the check passes, and a nil check is Commit.
	ran := false
	if err := ed.CommitChecked(ctx, prepared, func(context.Context) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("CommitChecked with a passing check: err=%v ran=%v", err, ran)
	}
	if readFile(t, root, "a.go") != fromDone || readFile(t, root, "b.go") != toDone {
		t.Error("the files were not written")
	}
	noTempFiles(t, root)
}

// TestCommitCheckedDoesNotRunTheCheckAfterAFailedWrite: there is nothing to
// check, and the check may be expensive.
func TestCommitCheckedDoesNotRunTheCheckAfterAFailedWrite(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", fromSrc)
	ed := New(root)
	prepared := moveOut(t, ed)
	writeFile(t, root, "a.go", fromSrc+"// changed since it was loaded\n")
	ran := false
	err := ed.CommitChecked(context.Background(), prepared, func(context.Context) error { ran = true; return nil })
	if !errors.Is(err, ErrStale) || ran {
		t.Fatalf("err=%v ran=%v, want ErrStale and no check", err, ran)
	}
	if exists(root, "b.go") {
		t.Error("the created file was not removed after the source turned out stale")
	}
	noTempFiles(t, root)
}

// TestRewriteCommitRefusesAStaleFile: Rewrite carries no index hash; Commit's
// re-check against the bytes Load read is what catches a file changed since.
func TestRewriteCommitRefusesAStaleFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", fromSrc)
	writeFile(t, root, "b.go", "package app\n")
	ed := New(root)
	a, b := loadBoth(t, ed)
	pb, prob := ed.Rewrite(b, []byte(toDone))
	if prob != nil {
		t.Fatal(prob)
	}
	pa, prob := ed.Rewrite(a, []byte(fromDone))
	if prob != nil {
		t.Fatal(prob)
	}
	const human = "package app\n\n// edited by hand\n"
	writeFile(t, root, "a.go", human)
	if err := ed.Commit(context.Background(), []Prepared{pb, pa}); !errors.Is(err, ErrStale) {
		t.Fatalf("Commit err = %v, want ErrStale", err)
	}
	if readFile(t, root, "a.go") != human || readFile(t, root, "b.go") != "package app\n" {
		t.Error("a stale commit left a change behind")
	}
	noTempFiles(t, root)
}
