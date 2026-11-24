package multirepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/store"
)

// countingOpener returns an opener that tracks live (opened minus closed)
// databases and the highest value that count ever reached.
type countingOpener struct {
	opened, closed atomic.Int32
	peak           atomic.Int32
	delay          time.Duration
}

func (c *countingOpener) open(ctx context.Context, dbPath string) (*OpenedStore, error) {
	live := c.opened.Add(1) - c.closed.Load()
	for {
		p := c.peak.Load()
		if live <= p || c.peak.CompareAndSwap(p, live) {
			break
		}
	}
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return &OpenedStore{
		Source: &fakeSource{},
		RunID:  1,
		Close:  func() error { c.closed.Add(1); return nil },
	}, nil
}

func quiet(cfg PoolConfig) PoolConfig {
	cfg.DisableJanitor = true
	return cfg
}

func TestHotStorePoolAcquireAndLRUEviction(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 3, IdleEvictAfter: 10 * time.Minute}), c.open)
	defer pool.Close()
	ctx := context.Background()

	hA, err := pool.Acquire(ctx, "projA", "/db/A")
	if err != nil {
		t.Fatal(err)
	}
	hB, _ := pool.Acquire(ctx, "projB", "/db/B")
	hC, _ := pool.Acquire(ctx, "projC", "/db/C")
	if pool.ActiveCount() != 3 {
		t.Errorf("active = %d, want 3", pool.ActiveCount())
	}

	hA.Release()
	hB.Release()

	// D evicts the least recently used unpinned store (A).
	hD, err := pool.Acquire(ctx, "projD", "/db/D")
	if err != nil {
		t.Fatal(err)
	}
	defer hD.Release()
	if pool.ActiveCount() != 3 || c.closed.Load() != 1 {
		t.Errorf("active=%d closed=%d, want 3 and 1", pool.ActiveCount(), c.closed.Load())
	}

	// C is pinned, so E evicts B, not C.
	hE, err := pool.Acquire(ctx, "projE", "/db/E")
	if err != nil {
		t.Fatal(err)
	}
	defer hE.Release()
	if c.closed.Load() != 2 {
		t.Errorf("closed = %d, want 2 (B evicted)", c.closed.Load())
	}
	hC.Release()
}

func TestHotStorePoolIdleEviction(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 10, IdleEvictAfter: 5 * time.Minute}), c.open)
	defer pool.Close()

	h, err := pool.Acquire(context.Background(), "projX", "/db/X")
	if err != nil {
		t.Fatal(err)
	}
	h.Release()

	if n := pool.EvictIdle(time.Now()); n != 0 {
		t.Errorf("evicted %d, want 0", n)
	}
	if n := pool.EvictIdle(time.Now().Add(10 * time.Minute)); n != 1 || c.closed.Load() != 1 {
		t.Errorf("evicted %d (closed %d), want 1", n, c.closed.Load())
	}
}

func TestHotStorePoolJanitorEvictsAndStops(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(PoolConfig{
		MaxOpenStores:   4,
		IdleEvictAfter:  20 * time.Millisecond,
		JanitorInterval: 10 * time.Millisecond,
	}, c.open)

	h, err := pool.Acquire(context.Background(), "p", "/db/p")
	if err != nil {
		t.Fatal(err)
	}
	h.Release()

	deadline := time.Now().Add(3 * time.Second)
	for c.closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.closed.Load() != 1 {
		t.Fatalf("janitor did not evict the idle store")
	}

	done := make(chan struct{})
	go func() { _ = pool.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return: janitor goroutine leaked")
	}
}

func TestHotStorePoolConcurrency(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(DefaultPoolConfig()), c.open)
	defer pool.Close()

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				proj := fmt.Sprintf("proj-%d", i%5)
				h, err := pool.Acquire(context.Background(), proj, "/db/"+proj)
				if err != nil {
					t.Errorf("acquire %s: %v", proj, err)
					return
				}
				time.Sleep(time.Millisecond)
				h.Release()
			}
		}()
	}
	wg.Wait()
}

func TestHotStorePoolBoundHoldsUnderConcurrentDistinctAcquires(t *testing.T) {
	const max = 3
	c := &countingOpener{delay: 5 * time.Millisecond}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: max}), c.open)
	defer pool.Close()

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			proj := fmt.Sprintf("p%d", i)
			// Short pins: a caller that cannot get a slot because the others
			// are momentarily pinned gets ErrPoolExhausted and retries.
			for attempt := 0; attempt < 200; attempt++ {
				h, err := pool.Acquire(context.Background(), proj, "/db/"+proj)
				if errors.Is(err, ErrPoolExhausted) {
					time.Sleep(time.Millisecond)
					continue
				}
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				time.Sleep(time.Millisecond)
				h.Release()
				return
			}
			t.Errorf("never got a slot for %s", proj)
		}(i)
	}
	wg.Wait()

	if peak := c.peak.Load(); peak > max {
		t.Fatalf("peak live stores = %d, want <= %d", peak, max)
	}
}

func TestHotStorePoolAllPinnedReturnsExhausted(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 2}), c.open)
	defer pool.Close()
	ctx := context.Background()

	h1, _ := pool.Acquire(ctx, "a", "/db/a")
	h2, _ := pool.Acquire(ctx, "b", "/db/b")
	if _, err := pool.Acquire(ctx, "c", "/db/c"); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("err = %v, want ErrPoolExhausted", err)
	}
	if c.opened.Load() != 2 {
		t.Errorf("opened %d stores, want 2: the over-limit open must not proceed", c.opened.Load())
	}
	h1.Release()
	h3, err := pool.Acquire(ctx, "c", "/db/c")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	h3.Release()
	h2.Release()
}

func TestHotStorePoolCloseWithLivePinsDefersClose(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 3}), c.open)
	ctx := context.Background()

	pinned, _ := pool.Acquire(ctx, "a", "/db/a")
	idle, _ := pool.Acquire(ctx, "b", "/db/b")
	idle.Release()

	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if c.closed.Load() != 1 {
		t.Fatalf("closed = %d after Close, want 1: the pinned store must stay open", c.closed.Load())
	}
	pinned.Release()
	if c.closed.Load() != 2 {
		t.Fatalf("closed = %d after last Release, want 2", c.closed.Load())
	}
	if _, err := pool.Acquire(ctx, "a", "/db/a"); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("err = %v, want ErrPoolClosed", err)
	}
}

func TestHotStorePoolReregisteredPathGetsNewHandle(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 4}), c.open)
	defer pool.Close()
	ctx := context.Background()

	old, _ := pool.Acquire(ctx, "svc", "/db/old")
	moved, err := pool.Acquire(ctx, "svc", "/db/new")
	if err != nil {
		t.Fatal(err)
	}
	if moved.DBPath != "/db/new" || moved.Source == old.Source {
		t.Fatalf("got path %s / same source %v, want a fresh handle on the new path",
			moved.DBPath, moved.Source == old.Source)
	}
	if c.closed.Load() != 0 {
		t.Error("the old store was closed while still pinned")
	}
	old.Release()
	if c.closed.Load() != 1 {
		t.Errorf("closed = %d after releasing the replaced handle, want 1", c.closed.Load())
	}
	moved.Release()
}

func TestHotStorePoolTwoIDsSharingPathKeepIndependentEntries(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 4}), c.open)
	defer pool.Close()
	ctx := context.Background()

	a, _ := pool.Acquire(ctx, "a", "/db/shared")
	b, _ := pool.Acquire(ctx, "b", "/db/shared")
	a.Release()
	b.Release()
	pool.EvictIdle(time.Now().Add(time.Hour * 24))

	if pool.ActiveCount() != 0 || c.closed.Load() != 2 {
		t.Fatalf("active=%d closed=%d, want 0 and 2", pool.ActiveCount(), c.closed.Load())
	}
}

func TestPinnedHandleReleaseIsIdempotent(t *testing.T) {
	c := &countingOpener{}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 2}), c.open)
	defer pool.Close()
	ctx := context.Background()

	h1, _ := pool.Acquire(ctx, "a", "/db/a")
	h2, _ := pool.Acquire(ctx, "a", "/db/a") // second pin on the same entry
	copied := *h1
	h1.Release()
	h1.Release()
	copied.Release()

	// h2 still holds a pin, so the entry must not be evictable.
	if n := pool.EvictIdle(time.Now().Add(24 * time.Hour)); n != 0 {
		t.Fatalf("evicted %d while a pin is held: double Release over-decremented", n)
	}
	h2.Release()
	if n := pool.EvictIdle(time.Now().Add(24 * time.Hour)); n != 1 {
		t.Fatalf("evicted %d after the last pin, want 1", n)
	}
}

func TestHotStorePoolOpenErrorIsPerMemberAndTyped(t *testing.T) {
	opener := func(context.Context, string) (*OpenedStore, error) {
		return nil, &store.ErrSchemaNewer{Found: 99, Latest: 1}
	}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 2}), opener)
	defer pool.Close()

	_, err := pool.Acquire(context.Background(), "x", "/db/x")
	var me *MemberOpenError
	if !errors.As(err, &me) || !me.SchemaMismatch || me.ProjectID != "x" {
		t.Fatalf("err = %v, want a MemberOpenError flagged SchemaMismatch", err)
	}
	if pool.ActiveCount() != 0 {
		t.Error("a failed open left an entry behind")
	}
}

// TestHotStorePoolDetectsReindexOnAcquire re-indexes the member's database
// (a new committed run, as another process's two-phase swap would leave) and
// checks the next Acquire reports it rather than returning the cached run.
func TestHotStorePoolDetectsReindexOnAcquire(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "member.db")
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE index_runs (id INTEGER PRIMARY KEY, committed INTEGER NOT NULL, mutation_seq INTEGER NOT NULL DEFAULT 0);
		 INSERT INTO index_runs (id, committed, mutation_seq) VALUES (1, 1, 0);`); err != nil {
		t.Fatal(err)
	}

	opener := func(ctx context.Context, _ string) (*OpenedStore, error) {
		probe, err := NewRunProbe(ctx, db.DB)
		if err != nil {
			return nil, err
		}
		run, seq, err := probe(ctx)
		if err != nil {
			return nil, err
		}
		return &OpenedStore{Source: &fakeSource{}, RunID: run, MutationSeq: seq, Probe: probe}, nil
	}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 2}), opener)
	defer pool.Close()

	h, err := pool.Acquire(ctx, "m", path)
	if err != nil || h.RunID != 1 || h.RunChanged {
		t.Fatalf("first acquire: %+v, %v", h, err)
	}
	h.Release()

	// Same run, higher mutation_seq: still a change.
	if _, err := db.ExecContext(ctx, `UPDATE index_runs SET mutation_seq = 5 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	h, err = pool.Acquire(ctx, "m", path)
	if err != nil || !h.RunChanged || h.RunID != 1 {
		t.Fatalf("mutation_seq bump not reported: %+v, %v", h, err)
	}
	h.Release()

	// Full re-index: old run replaced by a new committed run.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM index_runs WHERE id = 1; INSERT INTO index_runs (id, committed, mutation_seq) VALUES (2, 1, 0);`); err != nil {
		t.Fatal(err)
	}
	h, err = pool.Acquire(ctx, "m", path)
	if err != nil {
		t.Fatal(err)
	}
	if !h.RunChanged || h.RunID != 2 || h.PreviousRunID != 1 {
		t.Fatalf("re-index not reported: RunID=%d changed=%v prev=%d", h.RunID, h.RunChanged, h.PreviousRunID)
	}
	h.Release()

	h, _ = pool.Acquire(ctx, "m", path)
	if h.RunChanged || h.RunID != 2 {
		t.Errorf("a second acquire must see the refreshed run: %+v", h)
	}
	h.Release()
}

// A rebuilt index (init --force) is a new file at the same path. The cached
// handle reads the deleted file, so its probe would never see the change; the
// pool must notice the file itself was replaced and open the new one, while a
// caller still holding the old handle keeps it until Release.
func TestHotStorePoolReopensIndexReplacedOnDisk(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "member.db")
	create := func() {
		db, err := store.Open(ctx, store.Options{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.ExecContext(ctx,
			`CREATE TABLE index_runs (id INTEGER PRIMARY KEY, committed INTEGER NOT NULL, mutation_seq INTEGER NOT NULL DEFAULT 0);
			 INSERT INTO index_runs (id, committed, mutation_seq) VALUES (1, 1, 0);`); err != nil {
			t.Fatal(err)
		}
	}
	create()

	var opens, closes atomic.Int32
	opener := func(ctx context.Context, p string) (*OpenedStore, error) {
		db, err := store.Open(ctx, store.Options{Path: p})
		if err != nil {
			return nil, err
		}
		probe, err := NewRunProbe(ctx, db.DB)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		run, seq, err := probe(ctx)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		opens.Add(1)
		return &OpenedStore{Source: &fakeSource{}, RunID: run, MutationSeq: seq, Probe: probe,
			Close: func() error { closes.Add(1); return db.Close() }}, nil
	}
	pool := NewHotStorePool(quiet(PoolConfig{MaxOpenStores: 2}), opener)
	defer pool.Close()

	old, err := pool.Acquire(ctx, "m", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	create()

	fresh, err := pool.Acquire(ctx, "m", path)
	if err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 2 {
		t.Fatalf("opens = %d, want 2: the replaced file was served from the old handle", opens.Load())
	}
	if closes.Load() != 0 {
		t.Fatal("the old database was closed while still pinned")
	}
	old.Release()
	if closes.Load() != 1 {
		t.Fatalf("closes = %d after the old pin's release, want 1", closes.Load())
	}
	fresh.Release()

	again, err := pool.Acquire(ctx, "m", path)
	if err != nil {
		t.Fatal(err)
	}
	again.Release()
	if opens.Load() != 2 {
		t.Fatalf("an unchanged file was reopened: opens = %d", opens.Load())
	}
}
