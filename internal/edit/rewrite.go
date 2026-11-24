package edit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	afs "github.com/sutrakam/graphlume/internal/acquire/fs"
)

// This file is the editor's second kind of edit. A rename replaces one
// identifier at sites the index names (Prepare). A structural edit — moving a
// declaration — computes a file's whole new content from its syntax, so it
// needs the file's bytes first and hands back the bytes to write: Load, then
// Rewrite or Create. Both kinds end in the same Commit, with the same
// containment, re-check, lock and rollback.

// File is one file as the editor read it, or the finding that the path is
// free. Only Load and LoadDir make one that Rewrite and Create accept.
type File struct {
	// Path is relative to the project root, forward slashes, spelled as the
	// directory spells it.
	Path string
	// Data is the file's content; nil when Exists is false.
	Data []byte
	// Exists is false when nothing is at Path and its directory is a real
	// directory inside the root, so the file could be created.
	Exists bool

	rel    string // OS-native, cleaned, relative to the root
	mode   fs.FileMode
	loaded bool
}

// Hash is the content hash the index records for these bytes
// (model.File.ContentHash).
func (f File) Hash() string { return afs.HashBytes(f.Data) }

// Load reads path under every rule Prepare applies: inside the root, no
// symlinked component, a regular file, within the size limit. A path with
// nothing at it is not a problem: it comes back with Exists false, provided
// the directory it would be created in is a real directory inside the root.
// A problem is data; the error is non-nil only when the root itself cannot
// be opened.
func (e *Editor) Load(path string) (File, *Problem, error) {
	root, err := os.OpenRoot(e.root)
	if err != nil {
		return File{}, nil, fmt.Errorf("edit: open project root %s: %w", e.root, err)
	}
	defer func() { _ = root.Close() }()
	f, prob := e.load(root, path)
	return f, prob, nil
}

// LoadDir loads every regular entry of dir (relative to the root, "." for the
// root itself) whose name keep accepts, sorted by name. Subdirectories are
// skipped. An accepted entry that cannot be read safely — a symlink, a FIFO,
// a file over the limit — is a problem, not a silent omission: the caller
// asked for the whole set.
func (e *Editor) LoadDir(dir string, keep func(name string) bool) ([]File, []Problem, error) {
	root, err := os.OpenRoot(e.root)
	if err != nil {
		return nil, nil, fmt.Errorf("edit: open project root %s: %w", e.root, err)
	}
	defer func() { _ = root.Close() }()

	rel := filepath.Clean(filepath.FromSlash(dir))
	if rel != "." {
		if err := e.contain(rel); err != nil {
			return nil, []Problem{{Path: dir, Reason: "cannot be read inside the project root: " + err.Error(), Err: err}}, nil
		}
		if err := checkDirs(root, rel); err != nil {
			return nil, []Problem{{Path: dir, Reason: "cannot be read inside the project root: " + err.Error(), Err: err}}, nil
		}
	}
	entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(rel))
	if err != nil {
		return nil, []Problem{{Path: dir, Reason: "cannot be listed: " + err.Error(), Err: err}}, nil
	}
	var (
		files    []File
		problems []Problem
	)
	for _, ent := range entries {
		if ent.IsDir() || !keep(ent.Name()) {
			continue
		}
		f, prob := e.load(root, filepath.ToSlash(filepath.Join(rel, ent.Name())))
		if prob != nil {
			problems = append(problems, *prob)
			continue
		}
		if f.Exists { // removed between the listing and the read: not part of the set
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, problems, nil
}

func (e *Editor) load(root *os.Root, path string) (File, *Problem) {
	fail := func(err error) (File, *Problem) {
		return File{}, &Problem{Path: path, Reason: "cannot be read inside the project root: " + err.Error(), Err: err}
	}
	rel := filepath.Clean(filepath.FromSlash(path))
	if err := textuallyContained(rel); err != nil {
		return fail(err)
	}
	if _, err := root.Lstat(rel); errors.Is(err, fs.ErrNotExist) {
		// Nothing there. It is usable only if the directory is: contained,
		// and real all the way down.
		if dir := filepath.Dir(rel); dir != "." {
			if err := e.contain(dir); err != nil {
				return fail(err)
			}
			if err := checkDirs(root, dir); err != nil {
				return fail(err)
			}
		}
		rel = filepath.Join(onDiskSpelling(root, filepath.Dir(rel)), filepath.Base(rel))
		return File{Path: filepath.ToSlash(rel), rel: rel, loaded: true}, nil
	}
	if err := e.contain(rel); err != nil {
		return fail(err)
	}
	src, mode, err := e.readRegular(root, rel, e.maxBytes)
	if err != nil {
		return fail(err)
	}
	rel = onDiskSpelling(root, rel)
	return File{Path: filepath.ToSlash(rel), Data: src, Exists: true, rel: rel, mode: mode, loaded: true}, nil
}

// textuallyContained is the part of security.ContainedPath that needs no
// filesystem: relative, no "..", no NUL, not the root itself. It lets a path
// that does not exist yet be judged before anything is created.
func textuallyContained(rel string) error {
	switch {
	case rel == "" || rel == ".":
		return errors.New("path must name a file")
	case filepath.IsAbs(rel) || filepath.VolumeName(rel) != "":
		return errors.New("path must be relative to the project root")
	case strings.ContainsRune(rel, 0):
		return errors.New("path must not contain a NUL byte")
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return errors.New("path must not contain '..'")
		}
	}
	return nil
}

// checkDirs refuses dir unless every component of it is a real directory: no
// symlink, junction or file on the way.
func checkDirs(root *os.Root, dir string) error {
	parts := strings.Split(dir, string(filepath.Separator))
	for i := range parts {
		name := filepath.Join(parts[:i+1]...)
		fi, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("lstat %s: %w", name, err)
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; symlinked paths are never edited", name)
		case fi.Mode().Type() != fs.ModeDir:
			return fmt.Errorf("%s is not a plain directory", name)
		}
	}
	return nil
}

// Rewrite prepares f, which must exist, to hold after. Nothing is written
// until Commit, which first re-checks that the file still holds exactly the
// bytes Load read.
func (e *Editor) Rewrite(f File, after []byte) (Prepared, *Problem) {
	fail := func(reason string) (Prepared, *Problem) {
		return Prepared{}, &Problem{Path: f.Path, Reason: reason}
	}
	switch {
	case !f.loaded:
		return fail("was not read by this editor")
	case !f.Exists:
		return fail("does not exist; it can only be created")
	case f.mode&0o200 == 0:
		// As in Prepare: a read-only file is its owner saying "do not touch".
		return fail("file is read-only; make it writable first if it should be edited")
	case bytes.Equal(f.Data, after):
		return fail("the edit changes nothing")
	case int64(len(after)) > e.maxBytes:
		return fail(fmt.Sprintf("the edit would make it %d bytes, over the %d-byte limit", len(after), e.maxBytes))
	}
	return Prepared{Path: f.Path, Replaced: 1, rel: f.rel, before: f.Data, after: after, mode: f.mode}, nil
}

// Create prepares the creation of f, which Load found absent, holding content
// with like's permissions (a new file beside the one its text came from
// should be as readable and writable as that one). Commit refuses if anything
// has appeared at the path in the meantime; it never overwrites.
func (e *Editor) Create(f File, content []byte, like File) (Prepared, *Problem) {
	fail := func(reason string) (Prepared, *Problem) {
		return Prepared{}, &Problem{Path: f.Path, Reason: reason}
	}
	switch {
	case !f.loaded:
		return fail("was not checked by this editor")
	case f.Exists:
		return fail("already exists; it can only be rewritten")
	case int64(len(content)) > e.maxBytes:
		return fail(fmt.Sprintf("the new file would be %d bytes, over the %d-byte limit", len(content), e.maxBytes))
	}
	mode := like.mode.Perm()
	if !like.loaded || !like.Exists || mode&0o200 == 0 {
		mode = 0o644
	}
	return Prepared{Path: f.Path, Replaced: 1, rel: f.rel, after: content, mode: mode, create: true}, nil
}

// create makes rel, which must not exist, holding data. created reports
// whether the file is in place, which can be true alongside an error from
// what follows the link.
//
// The temp file is linked to its final name rather than renamed over it:
// link(2) fails if the name exists, so a file that appeared after the last
// check is never overwritten. Where the filesystem has no hard links the
// rename is used, behind the same absent-check a rewrite's content re-check
// provides.
func (e *Editor) create(root *os.Root, path, rel string, data []byte, mode fs.FileMode) (created bool, err error) {
	if err := textuallyContained(rel); err != nil {
		return false, err
	}
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := e.contain(dir); err != nil {
			return false, err
		}
		if err := checkDirs(root, dir); err != nil {
			return false, err
		}
	}
	tmp, err := writeTemp(root, dir, data, mode)
	if err != nil {
		return false, err
	}
	// The temp name is always removed: after a link it is a second name for
	// the new file, after a rename it is already gone.
	defer func() {
		if rerr := root.Remove(tmp); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temp file %s: %w", tmp, rerr))
		}
	}()
	if err = e.injected(stageTempWritten, rel); err != nil {
		return false, err
	}
	if _, lerr := root.Lstat(rel); !errors.Is(lerr, fs.ErrNotExist) {
		return false, fmt.Errorf("%w: %s did not exist when this edit was planned and does now", ErrStale, path)
	}
	if err = e.injected(stageBeforeRename, rel); err != nil {
		return false, err
	}
	switch lerr := root.Link(tmp, rel); {
	case lerr == nil:
	case errors.Is(lerr, fs.ErrExist):
		return false, fmt.Errorf("%w: %s did not exist when this edit was planned and does now", ErrStale, path)
	default:
		// No hard links on this filesystem, or an error this platform does
		// not report as "exists": look once more, then rename.
		if _, serr := root.Lstat(rel); !errors.Is(serr, fs.ErrNotExist) {
			return false, fmt.Errorf("%w: %s did not exist when this edit was planned and does now", ErrStale, path)
		}
		if rerr := root.Rename(tmp, rel); rerr != nil {
			return false, fmt.Errorf("create %s: %w", path, errors.Join(lerr, rerr))
		}
	}
	if serr := syncDir(root, dir); serr != nil {
		return true, serr
	}
	return true, nil
}

// uncreate removes a file create made, but only while it still holds exactly
// the bytes that were written: a file someone has edited since is theirs.
func (e *Editor) uncreate(root *os.Root, path, rel string, wrote []byte) error {
	cur, _, err := e.readRegular(root, rel, int64(len(wrote)))
	if errors.Is(err, errTooLarge) || (err == nil && !bytes.Equal(cur, wrote)) {
		return fmt.Errorf("%w: %s no longer holds the bytes this edit wrote", ErrStale, path)
	}
	if err != nil {
		return fmt.Errorf("re-read before removal: %w", err)
	}
	if err := root.Remove(rel); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return syncDir(root, filepath.Dir(rel))
}
