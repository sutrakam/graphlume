package context

import (
	stdctx "context"
	"fmt"
	"sync"

	"github.com/sutrakam/graphlume/internal/registry"
)

// catalogueCache reuses a built catalogue across Assemble calls.
//
// WHY THIS EXISTS. Measured on a synthetic run:
//
//	10,000 symbols   catalogue 55.6 ms of 93.7 ms total   (59%)
//	94,000 symbols   catalogue dominates 1.34 s total
//
// The catalogue is derived data — two table scans plus enclosure resolution and
// index building — and it is identical for every query against the same run
// content. Rebuilding it per request means an agent asking three questions in a
// session pays for it three times, and the cost grows with the repository
// rather than with the question.
//
// WHY IT IS SAFE TO SHARE. A catalogue is immutable once built: nothing outside
// loadCatalogue writes to it, and every consumer only reads its maps. Go maps
// are safe for concurrent reads, so one instance can serve many in-flight MCP
// requests.
//
// WHY IT IS KEYED ON A FINGERPRINT AND NOT A RUN ID. A committed run is NOT
// immutable — incremental indexing updates it in place, so the same id can mean
// different content minute to minute while a developer edits. Keying on the id
// alone would serve pre-edit answers forever, which is the precise staleness
// this product exists to remove. registry.CatalogueFingerprint costs four
// indexed aggregates and is checked on every call.
//
// WHY THE REGISTRY IDENTITY IS PART OF THE KEY. A fingerprint describes CONTENT
// within one database; it does not identify the database. Two registries opened
// on different projects can hold runs with the same id and the same counts, and
// therefore the same fingerprint, while containing entirely different code. A
// content-only key would serve one project's catalogue for another project's
// query.
//
// This was not hypothetical: keying on content alone broke two tests
// immediately, because every test in this package builds its own in-memory
// database and several use fixtures of identical shape. The same collision
// would hit any process holding two projects open.
//
// A single entry is deliberate. One server serves one index; an LRU would add
// eviction policy and bookkeeping to solve a problem nobody has. Switching
// registries simply misses and rebuilds, which is correct if not optimal.
type catalogueCache struct {
	mu sync.Mutex

	// reg identifies WHICH database the cached catalogue came from. Compared by
	// pointer: two Registry values are the same store only if they are the same
	// object.
	reg    Registry
	fp     registry.Fingerprint
	cat    *catalogue
	loaded bool

	// hits and misses are for the benchmark and for `doctor`, so the cache's
	// effectiveness is observable rather than assumed.
	hits, misses int64

	flight *catFlight
}

type catFlight struct {
	done chan struct{}
	reg  Registry
	fp   registry.Fingerprint
	cat  *catalogue
	err  error
}

// get returns a catalogue for runID, building one only when the run's content
// has actually changed.
func (c *catalogueCache) get(
	ctx stdctx.Context,
	reg Registry,
	runID int64,
) (*catalogue, error) {
	fp, err := reg.CatalogueFingerprint(ctx, runID)
	if err != nil {
		// A fingerprint failure must not fail the request: fall back to
		// building fresh, which is always correct, merely slower.
		cat, lerr := loadCatalogue(ctx, reg, runID)
		return cat, lerr
	}

	c.mu.Lock()
	if c.loaded && c.reg == reg && c.fp == fp {
		c.hits++
		cat := c.cat
		c.mu.Unlock()
		return cat, nil
	}
	if c.flight != nil && c.flight.reg == reg && c.flight.fp == fp {
		fl := c.flight
		c.mu.Unlock()
		select {
		case <-fl.done:
			return fl.cat, fl.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	fl := &catFlight{done: make(chan struct{}), reg: reg, fp: fp}
	c.flight = fl
	go func() {
		var cat *catalogue
		var loadErr error
		defer func() {
			if r := recover(); r != nil {
				loadErr = fmt.Errorf("catalogue load panicked: %v", r)
			}
			c.mu.Lock()
			// Store unless a newer load has started since this one: flights
			// begin with the fingerprint current at that moment, so the most
			// recently started flight describes the newest content. Requiring
			// the fingerprint to be unchanged instead kept the first catalogue
			// forever and rebuilt on every call after the first edit.
			if loadErr == nil && cat != nil && (c.flight == fl || !c.loaded) {
				c.reg, c.fp, c.cat, c.loaded = reg, fp, cat, true
				c.misses++
			}
			fl.cat, fl.err = cat, loadErr
			if c.flight == fl {
				c.flight = nil
			}
			close(fl.done)
			c.mu.Unlock()
		}()
		loadCtx := stdctx.WithoutCancel(ctx)
		cat, loadErr = loadCatalogue(loadCtx, reg, runID)
	}()
	c.mu.Unlock()

	select {
	case <-fl.done:
		return fl.cat, fl.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// stats reports hit/miss counts.
func (c *catalogueCache) stats() (hits, misses int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}
