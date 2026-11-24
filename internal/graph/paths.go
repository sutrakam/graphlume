package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/sutrakam/graphlume/pkg/model"
)

// DefaultMaxPaths bounds Paths when the caller passes no limit. An agent asking
// "how does A reach B" wants a handful of concrete routes, not an enumeration.
const DefaultMaxPaths = 10

// pathsNodeCap bounds how much of the graph the search may materialise.
//
// Paths keeps a predecessor list per discovered node, so unlike Traverse its
// memory is a function of the explored region rather than the returned paths.
// The cap is what stops "how does A reach B" from becoming a whole-graph load
// when B is unreachable and the frontier just keeps growing.
const pathsNodeCap = 100_000

// Paths returns up to maxPaths shortest routes from `from` to `to`, each a
// slice of symbol ids beginning with from and ending with to.
//
// Only SHORTEST paths are returned. This is a bound, not an oversight: the
// number of distinct paths between two nodes in a dense code graph is
// exponential, so any complete answer is unusable, and the most direct routes
// are what actually explains a relationship. A longer, more circuitous route
// that happens to exist will not appear.
//
// An unreachable target is not an error; it returns no paths.
//
// The search is the same level-synchronous expansion Traverse uses — one query
// per depth, not one per node — with the predecessors of each newly discovered
// node recorded. Enumeration then walks that layered DAG backwards from `to`.
// Because every node in it was discovered by BFS, every backward step reaches a
// strictly shallower node and every walk terminates at `from`: the enumeration
// never backtracks out of a dead end, so producing maxPaths routes costs
// O(maxPaths x depth) rather than an exponential search.
func (g *Graph) Paths(ctx context.Context, from, to int64, maxPaths, maxDepth int) ([][]int64, error) {
	return g.PathsMatching(ctx, from, to, maxPaths, maxDepth, nil, "", nil)
}

// PathsMatching returns up to maxPaths shortest directed routes from `from` to `to`,
// filtered by edge kinds, minimum confidence, and excluded nodes.
func (g *Graph) PathsMatching(ctx context.Context, from, to int64, maxPaths, maxDepth int, kinds []model.EdgeKind, minConf model.Confidence, exclude func(id int64) bool) ([][]int64, error) {
	return g.PathsMatchingVia(ctx, from, 0, to, maxPaths, maxDepth, kinds, minConf, exclude)
}

// PathsMatchingVia returns up to maxPaths shortest directed routes from `from` to `to` optionally through `via`,
// filtered by edge kinds, minimum confidence, and excluded nodes.
func (g *Graph) PathsMatchingVia(
	ctx context.Context,
	from, via, to int64,
	maxPaths, maxDepth int,
	kinds []model.EdgeKind,
	minConf model.Confidence,
	exclude func(id int64) bool,
) ([][]int64, error) {
	if maxPaths <= 0 {
		maxPaths = DefaultMaxPaths
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}

	if via != 0 && via != from && via != to {
		// 1. Search from -> via
		p1, err := g.PathsMatchingVia(ctx, from, 0, via, maxPaths, maxDepth, kinds, minConf, exclude)
		if err != nil || len(p1) == 0 {
			return nil, err
		}

		// 2. Search via -> to
		p2, err := g.PathsMatchingVia(ctx, via, 0, to, maxPaths, maxDepth, kinds, minConf, exclude)
		if err != nil || len(p2) == 0 {
			return nil, err
		}

		// Combine p1 and p2 into valid paths without cycles
		var combined [][]int64
		for _, path1 := range p1 {
			seen := make(map[int64]bool, len(path1))
			for _, id := range path1 {
				seen[id] = true
			}
			for _, path2 := range p2 {
				hasCycle := false
				for _, id := range path2[1:] {
					if seen[id] {
						hasCycle = true
						break
					}
				}
				if hasCycle {
					continue
				}
				totalLen := len(path1) + len(path2) - 1
				if totalLen-1 > maxDepth {
					continue
				}
				full := make([]int64, 0, totalLen)
				full = append(full, path1...)
				full = append(full, path2[1:]...)
				combined = append(combined, full)
				if len(combined) >= maxPaths {
					return combined, nil
				}
			}
		}
		return combined, nil
	}

	if from == to {
		return [][]int64{{from}}, nil
	}

	runID, err := g.run(ctx)
	if err != nil {
		return nil, err
	}

	depthOf := map[int64]int{from: 0}
	preds := make(map[int64][]int64)
	frontier := []int64{from}
	found := false

	for depth := 1; depth <= maxDepth && len(frontier) > 0 && !found; depth++ {
		var next []int64

		visit := func(h hop) error {
			if exclude != nil && exclude(h.to) {
				return nil
			}
			if d, ok := depthOf[h.to]; ok {
				if d == depth {
					preds[h.to] = append(preds[h.to], h.from)
				}
				return nil
			}
			if len(depthOf) >= pathsNodeCap {
				return errNodeCap
			}
			depthOf[h.to] = depth
			preds[h.to] = append(preds[h.to], h.from)
			next = append(next, h.to)
			if h.to == to {
				found = true
			}
			return nil
		}

		err := g.forEachHop(ctx, runID, frontier, kinds, minConf, dirOut, visit)
		if errors.Is(err, errNodeCap) {
			break
		}
		if err != nil {
			return nil, err
		}
		if g.afterLevel != nil {
			g.afterLevel(depth)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frontier = next
	}

	if !found {
		return nil, nil
	}
	return enumeratePaths(preds, from, to, maxPaths), nil
}

func enumeratePaths(preds map[int64][]int64, from, to int64, maxPaths int) [][]int64 {
	// Parallel edges (two kinds between the same pair) record the same
	// predecessor twice and would otherwise yield duplicate paths. Sorting also
	// fixes the output order, which would otherwise depend on row order.
	for node, list := range preds {
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
		preds[node] = compactSorted(list)
	}

	out := make([][]int64, 0, maxPaths)
	reversed := []int64{to}

	var walk func(node int64)
	walk = func(node int64) {
		if len(out) >= maxPaths {
			return
		}
		if node == from {
			path := make([]int64, len(reversed))
			for i, id := range reversed {
				path[len(reversed)-1-i] = id
			}
			out = append(out, path)
			return
		}
		for _, p := range preds[node] {
			reversed = append(reversed, p)
			walk(p)
			reversed = reversed[:len(reversed)-1]
			if len(out) >= maxPaths {
				return
			}
		}
	}
	walk(to)
	return out
}

func compactSorted(ids []int64) []int64 {
	if len(ids) < 2 {
		return ids
	}
	n := 1
	for i := 1; i < len(ids); i++ {
		if ids[i] != ids[n-1] {
			ids[n] = ids[i]
			n++
		}
	}
	return ids[:n]
}

// MaxWaypoints bounds how many intermediate symbols one path query may name.
// Each waypoint is one more bounded search, so the bound keeps the query's
// cost a small multiple of a single path search.
const MaxWaypoints = 4

// PathsThrough returns up to maxPaths directed routes from `from` to `to` that
// pass through every waypoint, in order. Each leg is a shortest-path search
// with the same kinds, confidence floor and exclusions; legs are joined only
// where the result revisits no symbol, and the whole route must fit maxDepth.
// With no waypoints it is PathsMatching.
func (g *Graph) PathsThrough(
	ctx context.Context,
	from int64, waypoints []int64, to int64,
	maxPaths, maxDepth int,
	kinds []model.EdgeKind,
	minConf model.Confidence,
	exclude func(id int64) bool,
) ([][]int64, error) {
	if len(waypoints) > MaxWaypoints {
		return nil, fmt.Errorf("graph: %d waypoints, at most %d", len(waypoints), MaxWaypoints)
	}
	if maxPaths <= 0 {
		maxPaths = DefaultMaxPaths
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	stops := append(append([]int64{from}, waypoints...), to)
	routes := [][]int64{{from}}
	for i := 1; i < len(stops); i++ {
		if stops[i] == stops[i-1] {
			continue
		}
		leg, err := g.PathsMatchingVia(ctx, stops[i-1], 0, stops[i], maxPaths, maxDepth, kinds, minConf, exclude)
		if err != nil || len(leg) == 0 {
			return nil, err
		}
		var next [][]int64
		for _, r := range routes {
			seen := make(map[int64]bool, len(r))
			for _, id := range r {
				seen[id] = true
			}
			for _, l := range leg {
				if len(r)+len(l)-2 > maxDepth {
					continue
				}
				cycle := false
				for _, id := range l[1:] {
					if seen[id] {
						cycle = true
						break
					}
				}
				if cycle {
					continue
				}
				joined := make([]int64, 0, len(r)+len(l)-1)
				joined = append(append(joined, r...), l[1:]...)
				next = append(next, joined)
				if len(next) >= maxPaths {
					break
				}
			}
			if len(next) >= maxPaths {
				break
			}
		}
		if len(next) == 0 {
			return nil, nil
		}
		routes = next
	}
	return routes, nil
}
