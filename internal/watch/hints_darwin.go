//go:build darwin

package watch

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
)

// kqueue on directories only. One descriptor per watched directory, opened
// O_EVTONLY so that holding it does not stop the volume being unmounted, and
// registered for the three things that change a directory's listing.
//
// Standard library syscall, not golang.org/x/sys: everything needed is there
// for darwin, and this adds no direct dependency.

// kqWake is how often the reader returns from kevent with nothing to report,
// which is what lets close() stop it without a second descriptor to wake it.
const kqWake = 250 * time.Millisecond

type kqHints struct {
	kq int

	mu     sync.Mutex
	byPath map[string]int
	byFD   map[int]string
	dirty  map[string]struct{}

	signal chan struct{}
	done   chan struct{}
	wg     sync.WaitGroup
}

func newDirHints() (dirHints, error) {
	kq, err := syscall.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("watch: create kqueue: %w", err)
	}
	syscall.CloseOnExec(kq)
	h := &kqHints{
		kq:     kq,
		byPath: make(map[string]int),
		byFD:   make(map[int]string),
		dirty:  make(map[string]struct{}),
		signal: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	h.wg.Add(1)
	go h.read()
	return h, nil
}

func (h *kqHints) add(dir string) error {
	h.mu.Lock()
	_, known := h.byPath[dir]
	h.mu.Unlock()
	if known {
		return nil
	}
	fd, err := syscall.Open(dir, syscall.O_EVTONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
			// One descriptor per watched directory is all this backend uses,
			// so running out means the tree has more directories than the
			// process may hold descriptors.
			return fmt.Errorf("out of file descriptors after %d watched directories "+
				"(raise the limit with `ulimit -n`, or exclude directories in .graphlume.yml): %w",
				h.count(), err)
		}
		return err
	}
	ev := syscall.Kevent_t{
		Ident:  uint64(fd), //nolint:gosec // G115: a descriptor is non-negative
		Filter: syscall.EVFILT_VNODE,
		Flags:  syscall.EV_ADD | syscall.EV_CLEAR,
		Fflags: syscall.NOTE_WRITE | syscall.NOTE_DELETE | syscall.NOTE_RENAME,
	}
	if _, err := syscall.Kevent(h.kq, []syscall.Kevent_t{ev}, nil, nil); err != nil {
		_ = syscall.Close(fd)
		return fmt.Errorf("register %q with kqueue: %w", dir, err)
	}
	h.mu.Lock()
	h.byPath[dir], h.byFD[fd] = fd, dir
	h.mu.Unlock()
	return nil
}

func (h *kqHints) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byPath)
}

func (h *kqHints) remove(dir string) {
	h.mu.Lock()
	fd, ok := h.byPath[dir]
	if ok {
		delete(h.byPath, dir)
		delete(h.byFD, fd)
	}
	delete(h.dirty, dir)
	h.mu.Unlock()
	if ok {
		// Closing the descriptor removes its registration from the queue.
		_ = syscall.Close(fd)
	}
}

func (h *kqHints) changed() <-chan struct{} { return h.signal }

func (h *kqHints) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.dirty))
	for d := range h.dirty {
		out = append(out, d)
	}
	clear(h.dirty)
	return out
}

func (h *kqHints) read() {
	defer h.wg.Done()
	events := make([]syscall.Kevent_t, 128)
	timeout := syscall.NsecToTimespec(int64(kqWake))
	for {
		select {
		case <-h.done:
			return
		default:
		}
		n, err := syscall.Kevent(h.kq, nil, events, &timeout)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return // the queue was closed underneath us
		}
		if n == 0 {
			continue
		}
		h.mu.Lock()
		for _, ev := range events[:n] {
			if dir, ok := h.byFD[int(ev.Ident)]; ok { //nolint:gosec // G115: the descriptor registered above
				h.dirty[dir] = struct{}{}
			}
		}
		h.mu.Unlock()
		select {
		case h.signal <- struct{}{}:
		default: // a signal is already waiting; take() will return these too
		}
	}
}

func (h *kqHints) close() error {
	close(h.done)
	h.wg.Wait()
	h.mu.Lock()
	for fd := range h.byFD {
		_ = syscall.Close(fd)
	}
	h.byFD, h.byPath = map[int]string{}, map[string]int{}
	h.mu.Unlock()
	return syscall.Close(h.kq)
}

// newDefaultBackend is the backend a Watcher uses when a test has not supplied
// one. On macOS that is the directory scanner with kqueue hints: see scan.go
// for why fsnotify's own kqueue backend is not used here.
func newDefaultBackend(poll time.Duration) (backend, error) {
	hints, err := newDirHints()
	if err != nil {
		return nil, err
	}
	return newScanBackend(hints, poll), nil
}
