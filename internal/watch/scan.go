package watch

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// A watcher backend that holds one descriptor per DIRECTORY and none per file.
//
// WHY IT EXISTS. On macOS and the BSDs the kernel interface is kqueue, and
// kqueue reports a write to a file only to someone holding that file open.
// fsnotify therefore opens every file in every watched directory. On django
// (7,100 files) that is 8,200 descriptors and as many open calls, made one at
// a time before `graphlume watch` can say it is watching: 17 s on a loaded
// machine, over a minute on a busier one, uninterruptible, and a repository
// with more files than the descriptor limit could not be watched at all.
// fsnotify has no option to watch a directory without its files (AddWith
// takes a buffer size, for Windows, and nothing else that is exported).
//
// WHAT THIS DOES INSTEAD. It remembers what is in each watched directory:
// every entry's name, kind, size and modification time. Two things make it
// look again.
//
//   - A hint from the kernel that a directory changed. kqueue on the directory
//     itself fires when an entry is created, deleted or renamed, which is
//     every save an editor makes by writing a temporary file and renaming it,
//     and every checkout. One descriptor per directory buys that.
//   - A sweep of every watched directory on a timer. A file rewritten IN PLACE
//     changes no directory, so no hint arrives; the sweep finds it by its size
//     and modification time. This is the cost of not holding the file open,
//     and it is stated rather than hidden: an in-place write is reported
//     within one sweep interval, not at once.
//
// Looking again is one ReadDir and one lstat per entry, compared with what was
// remembered, and what differs is emitted as the same create, write and remove
// events fsnotify would have sent. Nothing above this file can tell the
// difference, except by how soon an in-place write arrives.
//
// THE SWEEP INTERVAL adapts so that sweeping costs at most about a twentieth
// of one core: it is never shorter than Options.PollInterval (two seconds by
// default) and never shorter than twenty times as long as the last sweep took.
//
// WHAT THE DESCRIPTOR LIMIT IMPLIES. One descriptor per watched directory:
// the bound is on directories (Options.MaxWatchDirs, 8,192 by default, under
// the usual per-process limit of 10,240), not on directories
// plus files.

// DefaultPollInterval is the shortest gap between two sweeps of the scanning
// backend, and so the longest an in-place write waits to be noticed on a tree
// small enough to sweep quickly.
const DefaultPollInterval = 2 * time.Second

// sweepDutyFactor is how many times longer than a sweep the gap after it must
// be: twenty keeps sweeping to about five percent of one core.
const sweepDutyFactor = 20

// maxPollInterval caps the adaptive gap, so a very large or very slow tree is
// still looked at.
const maxPollInterval = 30 * time.Second

// dirHints tells the scanner that a directory changed, without saying how.
// Nil means there is no such source and the sweep is the only way anything is
// noticed.
type dirHints interface {
	add(dir string) error
	remove(dir string)
	// changed is signalled after one or more directories have been marked;
	// take returns and clears them.
	changed() <-chan struct{}
	take() []string
	close() error
}

// sig is what the scanner remembers about one directory entry.
type sig struct {
	dir   bool
	size  int64
	modNs int64
}

type scanBackend struct {
	hints dirHints
	poll  time.Duration

	mu   sync.Mutex
	dirs map[string]map[string]sig

	events chan fsnotify.Event
	errs   chan error
	done   chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
	cerr   error
}

func newScanBackend(hints dirHints, poll time.Duration) *scanBackend {
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	b := &scanBackend{
		hints:  hints,
		poll:   poll,
		dirs:   make(map[string]map[string]sig),
		events: make(chan fsnotify.Event, 256),
		errs:   make(chan error, errorBuffer),
		done:   make(chan struct{}),
	}
	b.wg.Add(1)
	go b.run()
	return b
}

func (b *scanBackend) Events() <-chan fsnotify.Event { return b.events }
func (b *scanBackend) Errors() <-chan error          { return b.errs }

// Add starts watching one directory. Adding a directory that is already
// watched is a no-op, and in particular does not forget what was remembered:
// the watcher re-adds directories as a repair for a fault this backend does
// not have, and a fresh snapshot there would swallow a change made in between.
func (b *scanBackend) Add(name string) error {
	name = filepath.Clean(name)
	b.mu.Lock()
	_, known := b.dirs[name]
	b.mu.Unlock()
	if known {
		return nil
	}
	// Hint first, snapshot second: a change between the two is then reported
	// by the hint and found by comparing with the snapshot, where the other
	// order would lose it.
	if b.hints != nil {
		if err := b.hints.add(name); err != nil {
			return err
		}
	}
	snap, err := snapshot(name)
	if err != nil {
		if b.hints != nil {
			b.hints.remove(name)
		}
		return err
	}
	b.mu.Lock()
	b.dirs[name] = snap
	b.mu.Unlock()
	return nil
}

func (b *scanBackend) Remove(name string) error {
	name = filepath.Clean(name)
	b.mu.Lock()
	_, known := b.dirs[name]
	delete(b.dirs, name)
	b.mu.Unlock()
	if !known {
		return fsnotify.ErrNonExistentWatch
	}
	if b.hints != nil {
		b.hints.remove(name)
	}
	return nil
}

func (b *scanBackend) Close() error {
	b.once.Do(func() {
		close(b.done)
		b.wg.Wait()
		if b.hints != nil {
			b.cerr = b.hints.close()
		}
		close(b.events)
		close(b.errs)
	})
	return b.cerr
}

// watchedCount is how many directories are remembered, for tests.
func (b *scanBackend) watchedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.dirs)
}

// snapshot records a directory's entries.
func snapshot(dir string) (map[string]sig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sig, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue // gone between the listing and the stat: not there
		}
		out[e.Name()] = sig{dir: info.IsDir(), size: info.Size(), modNs: info.ModTime().UnixNano()}
	}
	return out, nil
}

func (b *scanBackend) run() {
	defer b.wg.Done()

	var hinted <-chan struct{}
	if b.hints != nil {
		hinted = b.hints.changed()
	}
	gap := b.poll
	timer := time.NewTimer(gap)
	defer timer.Stop()

	for {
		select {
		case <-b.done:
			return
		case <-hinted:
			for _, dir := range b.hints.take() {
				if !b.rescan(dir) {
					return
				}
			}
		case <-timer.C:
			start := time.Now()
			if !b.sweep() {
				return
			}
			gap = max(b.poll, min(sweepDutyFactor*time.Since(start), maxPollInterval))
			timer.Reset(gap)
		}
	}
}

// sweep looks at every watched directory. It reports false when the backend
// was closed while it ran.
func (b *scanBackend) sweep() bool {
	b.mu.Lock()
	dirs := make([]string, 0, len(b.dirs))
	for d := range b.dirs {
		dirs = append(dirs, d)
	}
	b.mu.Unlock()
	sort.Strings(dirs) // parents before children, and the same order every time
	for _, d := range dirs {
		select {
		case <-b.done:
			return false
		default:
		}
		// With hints, a directory whose listing changed has already been
		// rescanned, so the sweep has one thing left to find: a file whose
		// content changed where it lies. That needs a stat of each file it
		// remembers and no listing, which on a tree of small directories is
		// most of the cost. Without hints the listing is the only way to learn
		// of a new or deleted entry, so the whole directory is compared.
		look := b.rescan
		if b.hints != nil {
			look = b.restat
		}
		if !look(d) {
			return false
		}
	}
	return true
}

// restat checks the files one directory is remembered to hold for a changed
// size or modification time. Anything it cannot explain that way (a file that
// is gone, an entry that changed kind) is handed to rescan.
func (b *scanBackend) restat(dir string) bool {
	b.mu.Lock()
	old, known := b.dirs[dir]
	names := make([]string, 0, len(old))
	for name, s := range old {
		if !s.dir {
			names = append(names, name)
		}
	}
	b.mu.Unlock()
	if !known || len(names) == 0 {
		return true
	}
	sort.Strings(names)

	var out []fsnotify.Event
	changed := make(map[string]sig)
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || info.IsDir() {
			return b.rescan(dir)
		}
		now := sig{size: info.Size(), modNs: info.ModTime().UnixNano()}
		if was := old[name]; was.size != now.size || was.modNs != now.modNs {
			changed[name] = now
			out = append(out, fsnotify.Event{Name: filepath.Join(dir, name), Op: fsnotify.Write})
		}
	}
	if len(out) == 0 {
		return true
	}
	b.mu.Lock()
	if cur, still := b.dirs[dir]; still {
		// Copy on write: rescan reads the map it was handed without the lock.
		next := make(map[string]sig, len(cur))
		for k, v := range cur {
			next[k] = v
		}
		for k, v := range changed {
			if _, ok := next[k]; ok {
				next[k] = v
			}
		}
		b.dirs[dir] = next
	}
	b.mu.Unlock()
	for _, ev := range out {
		select {
		case b.events <- ev:
		case <-b.done:
			return false
		}
	}
	return true
}

// rescan compares one directory with what was remembered and emits what
// differs. It reports false when the backend was closed while it ran.
func (b *scanBackend) rescan(dir string) bool {
	b.mu.Lock()
	old, known := b.dirs[dir]
	b.mu.Unlock()
	if !known {
		return true // removed since the hint was raised
	}
	now, err := snapshot(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// The directory is gone. Its parent's listing reports that as a
			// removal; here there is only the bookkeeping to drop.
			b.mu.Lock()
			delete(b.dirs, dir)
			b.mu.Unlock()
			if b.hints != nil {
				b.hints.remove(dir)
			}
		}
		return true
	}

	var out []fsnotify.Event
	for name, s := range now {
		was, existed := old[name]
		switch {
		case !existed:
			out = append(out, fsnotify.Event{Name: filepath.Join(dir, name), Op: fsnotify.Create})
		case was.dir != s.dir:
			// A file replaced by a directory, or the reverse.
			out = append(out,
				fsnotify.Event{Name: filepath.Join(dir, name), Op: fsnotify.Remove},
				fsnotify.Event{Name: filepath.Join(dir, name), Op: fsnotify.Create})
		case !s.dir && (was.size != s.size || was.modNs != s.modNs):
			out = append(out, fsnotify.Event{Name: filepath.Join(dir, name), Op: fsnotify.Write})
		}
	}
	for name := range old {
		if _, still := now[name]; !still {
			out = append(out, fsnotify.Event{Name: filepath.Join(dir, name), Op: fsnotify.Remove})
		}
	}
	if len(out) == 0 {
		return true
	}
	// A stable order, so two runs over the same change report it the same way.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Op == fsnotify.Remove && out[j].Op != fsnotify.Remove
	})

	b.mu.Lock()
	if _, still := b.dirs[dir]; still {
		b.dirs[dir] = now
	}
	b.mu.Unlock()

	for _, ev := range out {
		select {
		case b.events <- ev:
		case <-b.done:
			return false
		}
	}
	return true
}
