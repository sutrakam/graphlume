package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"

	"github.com/spf13/cobra"
)

// # Tours
//
// A tour is an order to read the code in: where requests enter, and what each
// entry point then calls. It is built from the graph alone, so it is
// deterministic, offline and needs no model, and every line of it is a
// relationship the index holds at the confidence asked for. An agent that wants
// prose can narrate the structure; the structure is what stays true.
//
// Entry points are HTTP routes with a bound handler and the `main` functions.
// The stops are ordered by how much each reaches, so the entry points that
// explain the most come first, then by path so the order is the same every run.

// tourSource is the part of the registry a tour reads.
type tourSource interface {
	EndpointReport(ctx context.Context, runID int64) ([]registry.Endpoint, error)
	OutgoingEdges(ctx context.Context, runID, symbolID int64, limit int, filter registry.EdgeFilter) ([]registry.EdgeHit, error)
	SymbolByID(ctx context.Context, runID, id int64) (registry.SymbolHit, bool, error)
	FindSymbolsByName(ctx context.Context, runID int64, name, kind string, limit int) ([]registry.SymbolHit, error)
}

// tourOptions bound a tour.
type tourOptions struct {
	Stops         int
	Depth         int
	Width         int
	MinConfidence model.Confidence
}

// tourStep is one thing an entry point reaches.
type tourStep struct {
	Name       string `json:"name"`
	Depth      int    `json:"depth"`
	Confidence string `json:"confidence"`
	CalledAt   string `json:"called_at"`
}

// tourStop is one entry point and what it reaches.
type tourStop struct {
	Entry  string     `json:"entry"`
	Where  string     `json:"where"`
	Routes []string   `json:"routes,omitempty"`
	Steps  []tourStep `json:"steps"`
}

// buildTour reads the entry points and what each reaches.
func buildTour(ctx context.Context, src tourSource, runID int64, opts tourOptions) ([]tourStop, error) {
	type entry struct {
		id     int64
		routes []string
	}
	entries := map[int64]*entry{}
	var order []int64
	add := func(id int64, route string) {
		e, ok := entries[id]
		if !ok {
			e = &entry{id: id}
			entries[id] = e
			order = append(order, id)
		}
		if route != "" {
			e.routes = append(e.routes, route)
		}
	}

	endpoints, err := src.EndpointReport(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("tour: read routes: %w", err)
	}
	for _, ep := range endpoints {
		if ep.HandlerID != 0 {
			add(ep.HandlerID, ep.Signature)
		}
	}
	mains, err := src.FindSymbolsByName(ctx, runID, "main", "function", 20)
	if err != nil {
		return nil, fmt.Errorf("tour: read main: %w", err)
	}
	for _, m := range mains {
		add(m.ID, "")
	}

	filter := registry.EdgeFilter{Kinds: []string{string(model.EdgeCalls)}, MinConfidence: opts.MinConfidence}

	// The stops are ordered by how much each entry point reaches, and a
	// service with two thousand routes is two thousand walks of up to thirty
	// queries each, for the few stops shown: most of a 3-second tour on a
	// 26,000-file repository. So the walks share one cache of the callees read
	// for each symbol, and an entry point is walked only while it could still
	// make the list: one grouped count of the calls leaving each entry point
	// bounds how much it can reach, the entry points are walked from the
	// highest bound down, and the walk stops when no remaining bound can beat
	// the last stop kept. The tour is the one walking every entry point gives.
	counter, _ := src.(tourCounter)
	src = &tourEdgeCache{tourSource: src, edges: map[int64][]registry.EdgeHit{}}

	syms := make([]registry.SymbolHit, len(order))
	known := make([]bool, len(order))
	errs := make([]error, len(order))
	tourParallel(len(order), func(i int) {
		syms[i], known[i], errs[i] = src.SymbolByID(ctx, runID, order[i])
	})
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	type candidate struct {
		i     int
		bound int
		where string
	}
	var counts map[int64]int
	if counter != nil && opts.Stops > 0 {
		counts, err = counter.OutgoingEdgeCounts(ctx, runID, order, filter)
		if err != nil {
			return nil, fmt.Errorf("tour: count calls: %w", err)
		}
	}
	var cands []candidate
	for i, id := range order {
		if !known[i] {
			continue
		}
		c := candidate{i: i, bound: math.MaxInt, where: fmt.Sprintf("%s:%d", syms[i].FilePath, syms[i].StartLine)}
		if counts != nil {
			c.bound = tourReachBound(counts[id], opts)
		}
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].bound != cands[b].bound {
			return cands[a].bound > cands[b].bound
		}
		if cands[a].where != cands[b].where {
			return cands[a].where < cands[b].where
		}
		return cands[a].i < cands[b].i
	})

	type ranked struct {
		i    int
		stop tourStop
	}
	// before is the order of the finished tour: most steps, then path, then
	// the order the entry points were found in.
	before := func(a, b ranked) bool {
		if len(a.stop.Steps) != len(b.stop.Steps) {
			return len(a.stop.Steps) > len(b.stop.Steps)
		}
		if a.stop.Where != b.stop.Where {
			return a.stop.Where < b.stop.Where
		}
		return a.i < b.i
	}
	var kept []ranked
	// beaten says no candidate from c on can enter a full list.
	beaten := func(c candidate) bool {
		if opts.Stops <= 0 || len(kept) < opts.Stops {
			return false
		}
		last := kept[opts.Stops-1]
		n := len(last.stop.Steps)
		if c.bound != n {
			return c.bound < n
		}
		if c.where != last.stop.Where {
			return c.where > last.stop.Where
		}
		return c.i > last.i
	}
	const batch = tourWorkers * 4
	for lo := 0; lo < len(cands); {
		var take []candidate
		for ; lo < len(cands) && len(take) < batch; lo++ {
			if beaten(cands[lo]) {
				lo = len(cands)
				break
			}
			take = append(take, cands[lo])
		}
		found := make([]*tourStop, len(take))
		werrs := make([]error, len(take))
		tourParallel(len(take), func(k int) {
			c := take[k]
			found[k], werrs[k] = tourStopFor(ctx, src, runID, syms[c.i], order[c.i], entries[order[c.i]].routes, filter, opts)
		})
		for k, c := range take {
			if werrs[k] != nil {
				return nil, werrs[k]
			}
			if found[k] != nil {
				kept = append(kept, ranked{i: c.i, stop: *found[k]})
			}
		}
		sort.SliceStable(kept, func(a, b int) bool { return before(kept[a], kept[b]) })
	}

	stops := make([]tourStop, 0, len(kept))
	for _, r := range kept {
		stops = append(stops, r.stop)
	}
	if opts.Stops > 0 && len(stops) > opts.Stops {
		stops = stops[:opts.Stops]
	}
	return stops, nil
}

// tourWorkers is how many entry points are walked at once.
const tourWorkers = 4

// tourCounter is the optional part of a tourSource that counts the
// relationships leaving many symbols in one read. Without it every entry
// point is walked.
type tourCounter interface {
	OutgoingEdgeCounts(ctx context.Context, runID int64, ids []int64, filter registry.EdgeFilter) (map[int64]int, error)
}

// tourReachBound is the most steps a walk from a symbol with calls outgoing
// relationships can take: at most Width of them at the first level, and at
// most Width more from each symbol at every level below.
func tourReachBound(calls int, opts tourOptions) int {
	first := min(calls, opts.Width)
	total, level := 0, first
	for d := 1; d <= opts.Depth; d++ {
		if level > math.MaxInt/2-total {
			return math.MaxInt
		}
		total += level
		if d < opts.Depth {
			if opts.Width > 0 && level > math.MaxInt/opts.Width {
				return math.MaxInt
			}
			level *= opts.Width
		}
	}
	return total
}

// tourParallel runs f for 0..n-1 on tourWorkers goroutines.
func tourParallel(n int, f func(i int)) {
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(tourWorkers, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				f(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// tourStopFor builds the stop for one entry point, or nil when it says nothing.
func tourStopFor(ctx context.Context, src tourSource, runID int64, sym registry.SymbolHit, id int64, routes []string, filter registry.EdgeFilter, opts tourOptions) (*tourStop, error) {
	steps, err := tourSteps(ctx, src, runID, id, filter, opts)
	if err != nil {
		return nil, err
	}
	if len(steps) == 0 && len(routes) == 0 {
		return nil, nil // a main that calls nothing the index bound says nothing
	}
	routes = append([]string(nil), routes...)
	sort.Strings(routes)
	return &tourStop{
		Entry:  sym.Name,
		Where:  fmt.Sprintf("%s:%d", sym.FilePath, sym.StartLine),
		Routes: routes,
		Steps:  steps,
	}, nil
}

// tourEdgeCache remembers the callees read for each symbol during one tour.
// Every read of one tour asks with the same limit and filter, so the symbol
// alone is the key.
type tourEdgeCache struct {
	tourSource
	mu    sync.Mutex
	edges map[int64][]registry.EdgeHit
}

func (c *tourEdgeCache) OutgoingEdges(ctx context.Context, runID, symbolID int64, limit int, filter registry.EdgeFilter) ([]registry.EdgeHit, error) {
	c.mu.Lock()
	e, ok := c.edges[symbolID]
	c.mu.Unlock()
	if ok {
		return e, nil
	}
	e, err := c.tourSource.OutgoingEdges(ctx, runID, symbolID, limit, filter)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.edges[symbolID] = e
	c.mu.Unlock()
	return e, nil
}

// tourSteps walks outwards from an entry point, breadth first, to opts.Depth,
// taking at most opts.Width callees of each and every symbol once.
func tourSteps(ctx context.Context, src tourSource, runID, from int64, filter registry.EdgeFilter, opts tourOptions) ([]tourStep, error) {
	seen := map[int64]struct{}{from: {}}
	frontier := []int64{from}
	var steps []tourStep
	for depth := 1; depth <= opts.Depth && len(frontier) > 0; depth++ {
		var next []int64
		for _, id := range frontier {
			edges, err := src.OutgoingEdges(ctx, runID, id, opts.Width*2, filter)
			if err != nil {
				return nil, err
			}
			taken := 0
			for _, e := range edges {
				if taken >= opts.Width {
					break
				}
				if _, dup := seen[e.DstID]; dup {
					continue
				}
				seen[e.DstID] = struct{}{}
				taken++
				steps = append(steps, tourStep{
					Name: e.DstName, Depth: depth, Confidence: e.Confidence,
					CalledAt: fmt.Sprintf("%s:%d", e.EvidenceFile, e.EvidenceLine),
				})
				next = append(next, e.DstID)
			}
		}
		frontier = next
	}
	return steps, nil
}

// writeTour prints a tour as numbered stops.
func writeTour(w io.Writer, stops []tourStop) {
	if len(stops) == 0 {
		fmt.Fprintln(w, "No entry points found: the index holds no route with a bound handler and no main function that calls anything it bound.")
		return
	}
	for i, s := range stops {
		fmt.Fprintf(w, "%d. %s  (%s)\n", i+1, s.Entry, s.Where)
		for _, r := range s.Routes {
			fmt.Fprintf(w, "   serves %s\n", r)
		}
		for _, st := range s.Steps {
			fmt.Fprintf(w, "   %s-> %s  [%s, called at %s]\n", strings.Repeat("  ", st.Depth-1), st.Name, st.Confidence, st.CalledAt)
		}
		fmt.Fprintln(w)
	}
}

func newTourCmd(g *Global) *cobra.Command {
	var opts tourOptions
	var minConfidence string
	cmd := &cobra.Command{
		Use:   "tour",
		Short: "An order to read the code in: entry points, then what each calls",
		Long: `Builds a walkthrough from the graph: the HTTP routes with a bound handler and
the main functions, each followed by the calls it makes, breadth first.

Entry points that reach the most come first, then by path, so the order is the
same every run. Every line is a relationship the index holds at or above
--min-confidence (high by default); nothing is guessed and no model is used.

  graphlume tour
  graphlume tour --depth 3 --limit 5 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			opts.MinConfidence = model.Confidence(minConfidence)
			if !opts.MinConfidence.Valid() {
				return fmt.Errorf("--min-confidence %q: want exact, high, medium or low", minConfidence)
			}
			if opts.Depth < 1 || opts.Width < 1 {
				return fmt.Errorf("--depth and --width must be at least 1")
			}
			s, err := openQuery(ctx, g)
			if err != nil {
				return err
			}
			defer s.close()
			stops, err := buildTour(ctx, s.reg, s.runID, opts)
			if err != nil {
				return err
			}
			if g.JSON {
				if stops == nil {
					stops = []tourStop{}
				}
				return emitJSON(cmd.OutOrStdout(), map[string]any{"index_revision": s.runID, "stops": stops})
			}
			writeTour(cmd.OutOrStdout(), stops)
			return nil
		},
	}
	cmd.Flags().IntVar(&opts.Stops, "limit", 8, "maximum entry points to show")
	cmd.Flags().IntVar(&opts.Depth, "depth", 2, "how many calls deep to follow from each entry point")
	cmd.Flags().IntVar(&opts.Width, "width", 5, "most callees to follow from each symbol")
	cmd.Flags().StringVar(&minConfidence, "min-confidence", "high", "lowest confidence to follow: exact, high, medium, low")
	return cmd
}
