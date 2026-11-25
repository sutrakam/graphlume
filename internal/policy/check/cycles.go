package check

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/metrics"
	"github.com/sutrakam/graphlume/internal/capability/architecture"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/policy"
	"github.com/sutrakam/graphlume/pkg/model"
)

// maxCycleViolations bounds the cycles reported per run. One change that
// closes a loop through a hub can create hundreds of distinct cycles, all
// fixed by the same edit; listing them all buries the one edge to remove.
const maxCycleViolations = 20

// cycleFloor is the weakest edge confidence the cycle graph uses. A `low`
// edge is a name match that may not exist, and a cycle through one is a lead
// at best; failing a build on it would be failing on a guess.
const cycleFloor = model.ConfidenceMedium

// cycleKinds are the relationships that make one unit depend on another. The
// rest (CONTAINS, DOCUMENTS, EXPOSES, READS, WRITES, ...) either are not
// dependencies between code units or point at tables and config, and a
// "cycle" through them is not a build- or design-time dependency cycle.
var cycleKinds = map[model.EdgeKind]bool{
	model.EdgeImports: true, model.EdgeCalls: true, model.EdgeReferences: true,
	model.EdgeInstantiates: true, model.EdgeExtends: true, model.EdgeImplements: true,
}

// pair is one directed dependency between two graph nodes (files, or
// directories at package level).
type pair struct{ from, to string }

// hop is the best evidence for one pair: the lowest (file, line), so the
// choice does not depend on edge order.
type hop struct {
	path string
	line int
	// changed is true when this evidence line is in a changed file.
	changed bool
}

// cycles implements no_new_cycles.
//
// # What "new" can honestly mean here
//
// There is exactly one index run: the one built from the checked-out head.
// There is no base index to diff against, so "a cycle that is not present at
// base" cannot be computed, and claiming it would be the failure this
// project's whole design is arranged against. Instead the rule reports every
// cycle that contains at least one dependency whose evidence line is in a
// CHANGED file.
//
// That is a sound superset of "newly created": adding a cycle requires adding
// a dependency, and a new dependency is by construction written in a changed
// file, so no cycle the change creates is missed. It is not a subset: a cycle
// that already existed and runs through a line the change merely touched (or
// re-indexed) is reported too. The message says so every time, rather than
// calling anything new. Cycles that exist elsewhere in the repository and do
// not touch the change are not reported, which is what keeps the rule usable
// on a codebase that already has some.
//
// # Levels
//
// level: file makes each indexed file a node. level: package makes each
// directory a node, which is a Go package exactly and a reasonable proxy
// elsewhere. A dependency between two symbols is a dependency between their
// files (or directories); one inside a single node is ignored.
func cycles(ctx context.Context, reg Registry, runID int64, r policy.NoNewCycles, d impact.DiffReport) ([]Violation, []string, error) {
	level := r.Level
	if level == "" {
		level = policy.LevelPackage
	}
	files, err := reg.Files(ctx, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("read files: %w", err)
	}
	symbols, err := reg.Symbols(ctx, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("read symbols: %w", err)
	}
	edges, err := reg.Edges(ctx, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("read edges: %w", err)
	}

	filePath := make(map[int64]string, len(files))
	for _, f := range files {
		filePath[f.ID] = f.Path
	}
	symFile := make(map[int64]string, len(symbols))
	for _, s := range symbols {
		symFile[s.ID] = filePath[s.FileID]
	}

	// Edges that start at a file's own top level (a script's calls, an
	// import-time call) have no symbol to start from; without them a cycle
	// closed by a top-level import would be invisible.
	type fileEdge struct {
		model.Edge
		src string
	}
	all := make([]fileEdge, 0, len(edges))
	for _, e := range edges {
		all = append(all, fileEdge{e, symFile[e.SrcID]})
	}
	if tl, ok := reg.(architecture.TopLevelSource); ok {
		top, err := tl.TopLevelEdges(ctx, runID)
		if err != nil {
			return nil, nil, fmt.Errorf("read top-level edges: %w", err)
		}
		for _, t := range top {
			all = append(all, fileEdge{t.Edge, t.CallerPath})
		}
	}

	changed := map[string]bool{}
	for _, c := range d.ChangedFiles {
		if p, ok := indexSpace(d.IndexRootPrefix, c); ok {
			changed[p] = true
		}
	}

	node := func(file string) string {
		if level == policy.LevelFile {
			return file
		}
		return path.Dir(file)
	}

	best := map[pair]hop{}
	nodes := map[string]bool{}
	for _, fe := range all {
		if !cycleKinds[fe.Kind] || !fe.Confidence.AtLeast(cycleFloor) {
			continue
		}
		dstFile, ok := symFile[fe.DstID]
		if !ok || fe.src == "" || dstFile == "" {
			continue
		}
		from, to := node(fe.src), node(dstFile)
		if from == to {
			continue
		}
		ev := fe.Evidence.FilePath
		if ev == "" {
			ev = fe.src
		}
		h := hop{path: ev, line: fe.Evidence.Range.Start.Line, changed: changed[ev]}
		k := pair{from, to}
		cur, seen := best[k]
		// A changed-file line is preferred over an unchanged one for the same
		// pair, because that is the evidence this rule is about.
		if !seen || (h.changed && !cur.changed) ||
			(h.changed == cur.changed && (h.path < cur.path || (h.path == cur.path && h.line < cur.line))) {
			best[k] = h
		}
		nodes[from], nodes[to] = true, true
	}

	var ns []metrics.Node
	for n := range nodes {
		ns = append(ns, metrics.Node{Key: n, Kind: model.KindFile})
	}
	var es []metrics.Edge
	adj := map[string][]string{}
	for k := range best {
		es = append(es, metrics.Edge{From: k.from, To: k.to, Weight: 1})
		adj[k.from] = append(adj[k.from], k.to)
	}
	for _, v := range adj {
		sort.Strings(v)
	}
	g := metrics.NewGraph(ns, es)
	comps, err := metrics.Components(ctx, g)
	if err != nil {
		return nil, nil, err
	}
	compOf := map[string]int{}
	size := map[int]int{}
	for _, c := range comps {
		size[c.ID] = len(c.Members)
		for _, m := range c.Members {
			compOf[m] = c.ID
		}
	}

	// Walk the touched edges in a fixed order so the same index always
	// yields the same violations.
	var touched []pair
	for k, h := range best {
		if h.changed && compOf[k.from] == compOf[k.to] && size[compOf[k.from]] > 1 {
			touched = append(touched, k)
		}
	}
	sort.Slice(touched, func(i, j int) bool {
		if touched[i].from != touched[j].from {
			return touched[i].from < touched[j].from
		}
		return touched[i].to < touched[j].to
	})

	sev := r.Severity.Or()
	id := policy.RuleID(policy.RuleNoNewCycles)
	seenCycle := map[string]bool{}
	var out []Violation
	total := 0
	for _, e := range touched {
		cyc := shortestPath(adj, compOf, e.to, e.from)
		if cyc == nil {
			continue // not reachable back: cannot happen inside an SCC
		}
		// cyc runs e.to ... e.from, so the cycle is e.from followed by cyc
		// without its last node (which is e.from again).
		loop := append([]string{e.from}, cyc[:len(cyc)-1]...)
		key := canonical(loop)
		if seenCycle[key] {
			continue
		}
		seenCycle[key] = true
		total++
		if len(out) >= maxCycleViolations {
			continue
		}
		out = append(out, cycleViolation(sev, id, level, loop, best, d.IndexRootPrefix))
	}

	var notes []string
	if total > len(out) {
		notes = append(notes, fmt.Sprintf("no_new_cycles: %d further cycle(s) through changed files were not listed; "+
			"the first %d are shown and breaking those dependencies usually clears the rest", total-len(out), len(out)))
	}
	return out, notes, nil
}

func cycleViolation(sev policy.Severity, id, level string, loop []string, best map[pair]hop, prefix string) Violation {
	var b strings.Builder
	for i, n := range loop {
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(n)
	}
	b.WriteString(" -> " + loop[0])

	v := Violation{Rule: policy.RuleNoNewCycles, RuleID: id, Severity: sev}
	for i := range loop {
		from, to := loop[i], loop[(i+1)%len(loop)]
		h := best[pair{from, to}]
		detail := fmt.Sprintf("%s depends on %s", from, to)
		if h.changed {
			detail += " (written in a changed file)"
			if v.Path == "" {
				v.Path, v.Line = repoPath(prefix, h.path), h.line
			}
		}
		v.Evidence = append(v.Evidence, Evidence{Path: repoPath(prefix, h.path), Line: h.line, Detail: detail})
	}
	v.Confidence = string(cycleFloor)
	v.Message = fmt.Sprintf("%s dependency cycle through the change: %s. It is reported because one of its dependencies is written in a changed file; "+
		"GraphLume has only the head index, so it cannot tell whether this cycle already existed before the change",
		level, b.String())
	return v
}

// shortestPath returns the shortest path from src to dst (inclusive of both)
// that stays inside src's component, or nil. Breadth-first over sorted
// neighbours, so the result is deterministic.
func shortestPath(adj map[string][]string, compOf map[string]int, src, dst string) []string {
	if src == dst {
		return []string{src}
	}
	prev := map[string]string{src: ""}
	queue := []string{src}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, m := range adj[n] {
			if _, seen := prev[m]; seen || compOf[m] != compOf[src] {
				continue
			}
			prev[m] = n
			if m == dst {
				var p []string
				for x := m; x != ""; x = prev[x] {
					p = append([]string{x}, p...)
				}
				return p
			}
			queue = append(queue, m)
		}
	}
	return nil
}

// canonical names a cycle independently of where it starts, so the same loop
// found from two of its changed edges is reported once.
func canonical(loop []string) string {
	min := 0
	for i, n := range loop {
		if n < loop[min] {
			min = i
		}
	}
	rot := append(append([]string(nil), loop[min:]...), loop[:min]...)
	return strings.Join(rot, "\x00")
}
