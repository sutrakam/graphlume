package multirepo

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sutrakam/graphlume/internal/store"
)

var (
	// ErrProjectNotFound is returned when a requested project is absent from the pool.
	ErrProjectNotFound = errors.New("project not found in pool")
	// ErrPoolClosed is returned when operations are attempted on a closed pool.
	ErrPoolClosed = errors.New("store pool is closed")
	// ErrPoolExhausted is returned when every slot is held by a pinned store,
	// so nothing can be evicted to make room.
	//
	// WHY FAIL INSTEAD OF WAIT. A pin lasts as long as the caller keeps its
	// handle, and a single request commonly pins several members and then asks
	// for one more. If Acquire blocked until a pin drained, two such requests
	// could each hold half the slots and wait on the other forever. Failing
	// fast turns that deadlock into an error the caller can report ("too many
	// members in one query"), which is the only outcome that always terminates.
	ErrPoolExhausted = errors.New("store pool exhausted: every slot is pinned by an active query")
	// ErrNoCommittedRun is returned when a member's index holds no committed run.
	ErrNoCommittedRun = errors.New("member index has no committed run")
)

// MemberOpenError reports that one member could not be opened or read. It is a
// per-member error on purpose: a schema mismatch in one repository must be
// reported against that repository, not abort the other members of the system.
type MemberOpenError struct {
	ProjectID string
	DBPath    string
	// SchemaMismatch is set when the index was written by a different schema
	// than this build reads (older or newer). Re-indexing, or upgrading, is the
	// fix, and callers word the message accordingly.
	SchemaMismatch bool
	Err            error
}

func (e *MemberOpenError) Error() string {
	return fmt.Sprintf("open store for %s (%s): %v", e.ProjectID, e.DBPath, e.Err)
}

// Unwrap exposes the cause so errors.As reaches store.ErrSchemaNewer and friends.
func (e *MemberOpenError) Unwrap() error { return e.Err }

func newMemberOpenError(projectID, dbPath string, err error) *MemberOpenError {
	var outdated *store.ErrSchemaOutdated
	var newer *store.ErrSchemaNewer
	return &MemberOpenError{
		ProjectID:      projectID,
		DBPath:         dbPath,
		SchemaMismatch: errors.As(err, &outdated) || errors.As(err, &newer),
		Err:            err,
	}
}

// PoolConfig configures the bounded LRU store cache.
//
// There is deliberately no byte budget: the pool cannot measure what a SQLite
// handle costs (the page cache and mmap are per connection and grow with use),
// so a budget built on a guessed constant would be a number that looks like a
// guarantee and is not one. The slot count is the real bound.
type PoolConfig struct {
	MaxOpenStores  int           // default: 6 read-only SQLite handles
	IdleEvictAfter time.Duration // default: 15m

	// JanitorInterval is how often idle stores are evicted in the background.
	// Defaults to a quarter of IdleEvictAfter, at least one second.
	JanitorInterval time.Duration
	// DisableJanitor turns the background eviction off; callers then call
	// EvictIdle themselves.
	DisableJanitor bool
}

// DefaultPoolConfig returns production-safe defaults for the hot store pool.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenStores:  6,
		IdleEvictAfter: 15 * time.Minute,
	}
}

// OpenedStore is what a [StoreOpener] hands back for one member.
type OpenedStore struct {
	Source MemberSource
	// RunID is the committed run at open time and MutationSeq its mutation
	// counter (0 when the schema has none).
	RunID       int64
	MutationSeq int64
	// Close releases the underlying database. May be nil.
	Close func() error
	// Probe cheaply re-reads the committed run and mutation counter. Nil
	// disables staleness detection for this member. See [NewRunProbe].
	Probe func(ctx context.Context) (runID, mutationSeq int64, err error)
}

// NewRunProbe builds a Probe over a member's index database. It checks once
// whether index_runs has a mutation_seq column so older schemas still probe
// on the run id alone.
func NewRunProbe(ctx context.Context, db *sql.DB) (func(context.Context) (int64, int64, error), error) {
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('index_runs') WHERE name = 'mutation_seq'`).Scan(&n); err != nil {
		return nil, err
	}
	query := `SELECT id, 0 FROM index_runs WHERE committed = 1 ORDER BY id DESC LIMIT 1`
	if n > 0 {
		query = `SELECT id, COALESCE(mutation_seq, 0) FROM index_runs WHERE committed = 1 ORDER BY id DESC LIMIT 1`
	}
	return func(ctx context.Context) (int64, int64, error) {
		var id, seq int64
		err := db.QueryRowContext(ctx, query).Scan(&id, &seq)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNoCommittedRun
		}
		return id, seq, err
	}, nil
}

// PinnedHandle represents an actively acquired read-only member handle.
// Callers MUST call Release() when finished. It may be copied; every copy
// shares one release, so releasing twice (or releasing two copies) unpins once.
type PinnedHandle struct {
	ProjectID string
	DBPath    string
	Source    MemberSource
	// RunID is the committed run as of this Acquire, re-read from the index.
	RunID int64
	// MutationSeq is the run's mutation counter as of the same read, so a
	// caller can record the exact (run, mutation) position it worked from, as
	// the workspace sync does. 0 when the schema has no counter.
	MutationSeq int64
	// RunChanged reports that the member was re-indexed since the pool last
	// looked (another process swapped runs). PreviousRunID is the run the pool
	// had cached. Callers surface this so an answer is not silently built from
	// a run that has since been replaced.
	RunChanged    bool
	PreviousRunID int64

	entry    *poolEntry
	pool     *HotStorePool
	released *atomic.Bool
}

// Release releases the pin on the handle, making it eligible for LRU eviction.
// It is idempotent.
func (p *PinnedHandle) Release() {
	if p == nil || p.entry == nil || p.pool == nil || p.released == nil {
		return
	}
	if !p.released.CompareAndSwap(false, true) {
		return
	}
	p.pool.releaseHandle(p.entry)
}

// poolEntry is one cached store in the LRU list.
type poolEntry struct {
	projectID string
	dbPath    string
	source    MemberSource
	runID     int64
	mutSeq    int64
	closeFn   func() error
	probe     func(ctx context.Context) (int64, int64, error)
	// file is the index file's identity when it was opened, nil when the path
	// could not be stat'ed (a test double). See replacedOnDisk.
	file     os.FileInfo
	lastUsed time.Time
	pinCount int
	// retired entries are no longer reachable by lookup (replaced, or the pool
	// is closing) and are closed when their last pin is released.
	retired bool
	elem    *list.Element
}

// StoreOpener opens a member's index at dbPath. A schema mismatch should be
// returned as the store package's typed error so it is reported per member.
type StoreOpener func(ctx context.Context, dbPath string) (*OpenedStore, error)

// flight is one open in progress, so concurrent Acquires of the same project
// share it instead of each spending a slot.
type flight struct {
	dbPath string
	done   chan struct{}
	err    error
}

// HotStorePool manages a bounded LRU cache of read-only stores.
//
// Entries are keyed by project id only. A project re-registered with a new
// path replaces its entry (the old one is closed once unpinned), and two ids
// that share a path each get their own handle: sharing one entry between ids
// made removal of one delete the other's path mapping.
type HotStorePool struct {
	mu     sync.Mutex
	cfg    PoolConfig
	opener StoreOpener
	closed bool

	byID    map[string]*poolEntry
	retired map[*poolEntry]struct{}
	lru     *list.List // front is most recently used, back is least
	flights map[string]*flight
	opening int // slots reserved by opens in flight
	closing int // slots whose database is still being closed
	// slotFreed is closed and replaced whenever a closing store finishes, so a
	// waiter can retry without polling.
	slotFreed chan struct{}

	stopJanitor chan struct{}
	janitorWG   sync.WaitGroup
}

// NewHotStorePool creates a bounded store pool and, unless disabled, starts
// the idle-eviction janitor that Close stops.
func NewHotStorePool(cfg PoolConfig, opener StoreOpener) *HotStorePool {
	if cfg.MaxOpenStores <= 0 {
		cfg.MaxOpenStores = 6
	}
	if cfg.IdleEvictAfter <= 0 {
		cfg.IdleEvictAfter = 15 * time.Minute
	}
	if cfg.JanitorInterval <= 0 {
		cfg.JanitorInterval = cfg.IdleEvictAfter / 4
		if cfg.JanitorInterval < time.Second {
			cfg.JanitorInterval = time.Second
		}
	}

	p := &HotStorePool{
		cfg:         cfg,
		opener:      opener,
		byID:        make(map[string]*poolEntry),
		retired:     make(map[*poolEntry]struct{}),
		lru:         list.New(),
		flights:     make(map[string]*flight),
		slotFreed:   make(chan struct{}),
		stopJanitor: make(chan struct{}),
	}
	if !cfg.DisableJanitor {
		p.janitorWG.Add(1)
		go p.janitor()
	}
	return p
}

func (p *HotStorePool) janitor() {
	defer p.janitorWG.Done()
	t := time.NewTicker(p.cfg.JanitorInterval)
	defer t.Stop()
	for {
		select {
		case <-p.stopJanitor:
			return
		case now := <-t.C:
			p.EvictIdle(now)
		}
	}
}

// usedLocked counts every slot that holds, or is about to hold, an open database.
func (p *HotStorePool) usedLocked() int {
	return len(p.byID) + len(p.retired) + p.opening + p.closing
}

// detachLocked removes an unpinned entry from lookup and starts its close.
// The caller finishes it with closeEntries after unlocking.
func (p *HotStorePool) detachLocked(e *poolEntry) {
	if e.elem != nil {
		p.lru.Remove(e.elem)
		e.elem = nil
	}
	if p.byID[e.projectID] == e {
		delete(p.byID, e.projectID)
	}
	p.closing++
}

// retireLocked hides a pinned entry from lookup; its last Release closes it.
func (p *HotStorePool) retireLocked(e *poolEntry) {
	if e.elem != nil {
		p.lru.Remove(e.elem)
		e.elem = nil
	}
	if p.byID[e.projectID] == e {
		delete(p.byID, e.projectID)
	}
	e.retired = true
	p.retired[e] = struct{}{}
}

// closeEntries closes databases outside p.mu: closing a SQLite handle
// checkpoints WAL and can block on disk, and holding the pool lock across it
// would stall every other Acquire.
func (p *HotStorePool) closeEntries(entries []*poolEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var errs []error
	for _, e := range entries {
		if e.closeFn != nil {
			if err := e.closeFn(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	p.mu.Lock()
	p.closing -= len(entries)
	close(p.slotFreed)
	p.slotFreed = make(chan struct{})
	p.mu.Unlock()
	return errors.Join(errs...)
}

// evictOneLocked detaches the least recently used unpinned entry.
func (p *HotStorePool) evictOneLocked() *poolEntry {
	for el := p.lru.Back(); el != nil; el = el.Prev() {
		if e := el.Value.(*poolEntry); e.pinCount == 0 {
			p.detachLocked(e)
			return e
		}
	}
	return nil
}

// makeRoomLocked evicts unpinned entries until one slot is free. When it
// cannot, exhausted distinguishes "everything is pinned or being opened" (fail)
// from "a close is still in flight" (wait for it).
func (p *HotStorePool) makeRoomLocked(victims *[]*poolEntry) (ok, exhausted bool) {
	// Victims already chosen are subtracted: the caller closes them before it
	// opens, so they free their slots by then.
	for p.usedLocked()-len(*victims) >= p.cfg.MaxOpenStores {
		v := p.evictOneLocked()
		if v == nil {
			return false, p.closing == len(*victims)
		}
		*victims = append(*victims, v)
	}
	return true, false
}

// Acquire gets or opens a read-only store for the project, pinning it until
// Released. On a cache hit it re-reads the member's committed run so a handle
// never carries a run id from before another process re-indexed.
func (p *HotStorePool) Acquire(ctx context.Context, projectID, dbPath string) (*PinnedHandle, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, ErrPoolClosed
		}

		entry := p.byID[projectID]
		if entry != nil && dbPath != "" && entry.dbPath != dbPath {
			// Re-registered at a new path: the old handle points at a
			// different database and must never be returned for this id.
			var victims []*poolEntry
			if entry.pinCount == 0 {
				p.detachLocked(entry)
				victims = append(victims, entry)
			} else {
				p.retireLocked(entry)
			}
			p.mu.Unlock()
			_ = p.closeEntries(victims)
			continue
		}
		if entry != nil {
			entry.pinCount++
			entry.lastUsed = time.Now()
			p.lru.MoveToFront(entry.elem)
			probe := entry.probe
			p.mu.Unlock()
			h, retry, err := p.finishHit(ctx, entry, probe)
			if retry {
				continue
			}
			return h, err
		}

		if dbPath == "" {
			p.mu.Unlock()
			return nil, &MemberOpenError{ProjectID: projectID, Err: errors.New("no index path registered")}
		}
		if p.opener == nil {
			p.mu.Unlock()
			return nil, errors.New("no store opener configured for pool")
		}

		if f, ok := p.flights[projectID]; ok {
			// Someone is already opening this project; share the result.
			p.mu.Unlock()
			select {
			case <-f.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if f.err != nil && f.dbPath == dbPath {
				return nil, f.err
			}
			continue
		}

		var victims []*poolEntry
		ok, exhausted := p.makeRoomLocked(&victims)
		if !ok {
			wait := p.slotFreed
			p.mu.Unlock()
			_ = p.closeEntries(victims)
			if exhausted {
				return nil, ErrPoolExhausted
			}
			// A close is in flight and will free a slot; wait for it.
			select {
			case <-wait:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		f := &flight{dbPath: dbPath, done: make(chan struct{})}
		p.flights[projectID] = f
		p.opening++
		p.mu.Unlock()
		// Victims are closed before the open starts, so the number of live
		// databases never exceeds MaxOpenStores even transiently.
		_ = p.closeEntries(victims)

		return p.openNew(ctx, projectID, dbPath, f)
	}
}

func (p *HotStorePool) openNew(ctx context.Context, projectID, dbPath string, f *flight) (*PinnedHandle, error) {
	// Stat before opening: if the file is replaced between the two, the next
	// hit sees a mismatch and reopens, which is the safe direction.
	file, _ := os.Stat(dbPath) //nolint:gosec // a registered member's own index path
	opened, err := p.opener(ctx, dbPath)
	if err == nil && (opened == nil || opened.Source == nil) {
		err = errors.New("opener returned no store")
	}

	p.mu.Lock()
	p.opening--
	delete(p.flights, projectID)
	if err != nil {
		f.err = newMemberOpenError(projectID, dbPath, err)
		close(f.done)
		p.mu.Unlock()
		return nil, f.err
	}
	if p.closed {
		f.err = ErrPoolClosed
		close(f.done)
		p.mu.Unlock()
		if opened.Close != nil {
			_ = opened.Close()
		}
		return nil, ErrPoolClosed
	}

	e := &poolEntry{
		projectID: projectID,
		dbPath:    dbPath,
		source:    opened.Source,
		runID:     opened.RunID,
		mutSeq:    opened.MutationSeq,
		closeFn:   opened.Close,
		probe:     opened.Probe,
		file:      file,
		lastUsed:  time.Now(),
		pinCount:  1,
	}
	e.elem = p.lru.PushFront(e)
	p.byID[projectID] = e
	close(f.done)

	// Re-check the bound after the insert. The reservation taken at admission
	// already guarantees it; this only trims if something else grew the pool
	// in the meantime, and never touches a pinned entry.
	var victims []*poolEntry
	for p.usedLocked()-len(victims) > p.cfg.MaxOpenStores {
		v := p.evictOneLocked()
		if v == nil {
			break
		}
		victims = append(victims, v)
	}
	p.mu.Unlock()
	_ = p.closeEntries(victims)

	return &PinnedHandle{
		ProjectID:   projectID,
		DBPath:      dbPath,
		Source:      e.source,
		RunID:       e.runID,
		MutationSeq: e.mutSeq,
		entry:       e,
		pool:        p,
		released:    new(atomic.Bool),
	}, nil
}

// finishHit probes the member's committed run after the entry has been pinned,
// outside the lock because it reads the database. retry is set when the index
// file was replaced on disk: the entry has been retired and Acquire must open
// the new file.
func (p *HotStorePool) finishHit(ctx context.Context, e *poolEntry, probe func(context.Context) (int64, int64, error)) (h *PinnedHandle, retry bool, err error) {
	if replacedOnDisk(e) {
		// init --force (or any rebuild) deletes the index and writes a new
		// file at the same path. The open handle still reads the deleted
		// file, whose committed run never changes, so the probe below would
		// report it as current forever.
		// Hide it from lookup, then drop this call's pin through the normal
		// path, which closes it once no other caller holds it.
		p.mu.Lock()
		if !e.retired && p.byID[e.projectID] == e {
			p.retireLocked(e)
		}
		p.mu.Unlock()
		p.releaseHandle(e)
		return nil, true, nil
	}
	h = &PinnedHandle{
		ProjectID: e.projectID,
		DBPath:    e.dbPath,
		Source:    e.source,
		entry:     e,
		pool:      p,
		released:  new(atomic.Bool),
	}
	if probe == nil {
		p.mu.Lock()
		h.RunID, h.MutationSeq = e.runID, e.mutSeq
		p.mu.Unlock()
		return h, false, nil
	}

	run, seq, err := probe(ctx)
	if err == nil && run == 0 {
		err = ErrNoCommittedRun
	}
	if err != nil {
		// Drop the pin and the entry: a member whose run cannot be read is
		// not one to keep serving from a cached run id.
		h.Release()
		p.mu.Lock()
		var victims []*poolEntry
		if !e.retired && p.byID[e.projectID] == e && e.pinCount == 0 {
			p.detachLocked(e)
			victims = append(victims, e)
		}
		p.mu.Unlock()
		_ = p.closeEntries(victims)
		return nil, false, newMemberOpenError(e.projectID, e.dbPath, err)
	}

	p.mu.Lock()
	if e.runID != run || e.mutSeq != seq {
		h.RunChanged = true
		h.PreviousRunID = e.runID
		e.runID, e.mutSeq = run, seq
	}
	h.RunID, h.MutationSeq = e.runID, e.mutSeq
	p.mu.Unlock()
	return h, false, nil
}

// replacedOnDisk reports whether the entry's path now names a different file
// than the one opened, or none. An entry opened without a stat (the path did
// not exist then, as with test doubles) is never reported.
func replacedOnDisk(e *poolEntry) bool {
	if e.file == nil {
		return false
	}
	now, err := os.Stat(e.dbPath) //nolint:gosec // a registered member's own index path
	return err != nil || !os.SameFile(e.file, now)
}

func (p *HotStorePool) releaseHandle(e *poolEntry) {
	p.mu.Lock()
	if e.pinCount > 0 {
		e.pinCount--
	}
	e.lastUsed = time.Now()
	var victims []*poolEntry
	if e.retired && e.pinCount == 0 {
		// Replaced, or the pool closed while pinned: this was the last user.
		delete(p.retired, e)
		p.closing++
		victims = append(victims, e)
	}
	p.mu.Unlock()
	_ = p.closeEntries(victims)
}

// EvictIdle removes entries idle longer than IdleEvictAfter that are not
// pinned. The pool's janitor calls it unless disabled.
func (p *HotStorePool) EvictIdle(now time.Time) int {
	p.mu.Lock()
	cutoff := now.Add(-p.cfg.IdleEvictAfter)
	var victims []*poolEntry
	for el := p.lru.Back(); el != nil; {
		prev := el.Prev()
		e := el.Value.(*poolEntry)
		if e.pinCount == 0 && e.lastUsed.Before(cutoff) {
			p.detachLocked(e)
			victims = append(victims, e)
		}
		el = prev
	}
	p.mu.Unlock()
	_ = p.closeEntries(victims)
	return len(victims)
}

// ActiveCount returns the number of open stores currently held, including
// replaced ones still pinned.
func (p *HotStorePool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byID) + len(p.retired)
}

// Close stops the janitor and closes every store. Stores still pinned are not
// closed under their users: they are retired and each is closed by its last
// Release. WHY NOT WAIT FOR THE PINS. Callers routinely `defer pool.Close()`
// above `defer handle.Release()`, so blocking here would deadlock the most
// ordinary usage. Close is idempotent.
func (p *HotStorePool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.janitorWG.Wait()
		return nil
	}
	p.closed = true
	close(p.stopJanitor)

	var victims []*poolEntry
	for _, e := range p.byID {
		if e.pinCount == 0 {
			p.detachLocked(e)
			victims = append(victims, e)
		} else {
			p.retireLocked(e)
		}
	}
	p.mu.Unlock()

	err := p.closeEntries(victims)
	p.janitorWG.Wait()
	return err
}
