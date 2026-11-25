package mcp

import (
	"context"
	"sync"
)

// prewarmCommits is the history the warm-up reads: the window hotspots and
// busfactor read, which also covers what owners asks for.
const prewarmCommits = 500

// PrewarmIndex loads, once and in the background, what many tools read and
// would otherwise each pay for on their first call: the symbol index (the
// capability layer's name and ID tables), the in-memory graph snapshot, and
// the recent commit history. On a large index each costs seconds; loaded
// here they are shared by every later call for as long as the server runs and
// the index revision holds (each is keyed by revision and reloaded only when
// it moves).
//
// It changes no answer. Failures are ignored: the tool that needs the data
// will load it, and report the error, itself.
func (s *Server) PrewarmIndex(ctx context.Context) {
	if s == nil {
		return
	}
	runID, _, err := s.currentRun(ctx)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = s.capDeps.SymbolIndex(ctx, runID)
	}()
	if s.deps.Graph != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.deps.Graph.Warm(ctx)
		}()
	}
	if s.deps.History != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.deps.History.Commits(ctx, prewarmCommits)
		}()
	}
	wg.Wait()
}
