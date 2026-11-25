package cluster

import (
	"context"
	"fmt"
	"sort"

	"github.com/sutrakam/graphlume/internal/analyze/metrics"
)

// Algorithm names accepted by Options.Algorithm.
const (
	AlgorithmLabelPropagation = "label_propagation"
	AlgorithmLeiden           = "leiden"
)

// eps absorbs float noise when comparing gains. The operations feeding every
// comparison run in a fixed order, so results are reproducible; eps only keeps
// two mathematically equal gains from being ordered by rounding.
const eps = 1e-12

// maxLevels bounds the aggregation depth of one Leiden run. Every level has
// strictly fewer nodes than the last, so this is a backstop, not a tuning knob.
const maxLevels = 64

type wedge struct {
	to int
	w  float64
}

// lgraph is one level of the Leiden hierarchy: an undirected weighted graph
// with neighbour lists sorted by node index. self[i] is twice the weight of the
// edges folded inside aggregate node i, so k[i] = sum of incident weights +
// self[i] and the sum of every k is 2m.
type lgraph struct {
	adj  [][]wedge
	self []float64
	k    []float64
	m2   float64
}

// buildLevelZero reads the graph the way Detect does: undirected, the weights
// of both directions summed, nodes indexed in ascending key order.
func buildLevelZero(g metrics.Graph) ([]string, lgraph) {
	keys := g.Keys()
	idx := make(map[string]int, len(keys))
	for i, k := range keys {
		idx[k] = i
	}
	weight := map[[2]int]float64{}
	for _, e := range g.Edges() {
		a, b := idx[e.From], idx[e.To]
		if a > b {
			a, b = b, a
		}
		weight[[2]int{a, b}] += float64(e.Weight)
	}
	pairs := make([][2]int, 0, len(weight))
	for p := range weight { // collected, then sorted: order never matters
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	lg := lgraph{adj: make([][]wedge, len(keys)), self: make([]float64, len(keys)), k: make([]float64, len(keys))}
	for _, p := range pairs {
		w := weight[p]
		lg.adj[p[0]] = append(lg.adj[p[0]], wedge{p[1], w})
		lg.adj[p[1]] = append(lg.adj[p[1]], wedge{p[0], w})
	}
	// Sort each list so the invariant does not rest on append order.
	for i := range lg.adj {
		sort.Slice(lg.adj[i], func(a, b int) bool { return lg.adj[i][a].to < lg.adj[i][b].to })
		for _, e := range lg.adj[i] {
			lg.k[i] += e.w
		}
		lg.m2 += lg.k[i]
	}
	return keys, lg
}

// renumber relabels to 0..c-1 in order of first appearance and returns c.
// Scanning nodes in ascending order makes label order follow smallest member.
func renumber(label []int) int {
	m := make(map[int]int, len(label))
	next := 0
	for i, l := range label {
		n, ok := m[l]
		if !ok {
			n = next
			next++
			m[l] = n
		}
		label[i] = n
	}
	return next
}

// moveNodes is the Leiden local-moving phase: a FIFO queue seeded with every
// node in ascending order; a node moves to the neighbouring community with the
// greatest strictly positive modularity gain (ties to the smaller community id)
// and its neighbours outside the new community are queued behind it.
func moveNodes(ctx context.Context, lg lgraph, comm []int, gamma float64) error {
	n := len(comm)
	tot := make([]float64, n)
	for i, c := range comm {
		tot[c] += lg.k[i]
	}
	queue := make([]int, n)
	inQ := make([]bool, n)
	for i := range queue {
		queue[i] = i
		inQ[i] = true
	}
	head, size := 0, n
	wTo := make([]float64, n)
	touched := make([]int, 0, 16)
	for pops := 0; size > 0; pops++ {
		if pops&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("detecting communities: %w", err)
			}
		}
		v := queue[head]
		head = (head + 1) % n
		size--
		inQ[v] = false

		c0 := comm[v]
		touched = touched[:0]
		touched = append(touched, c0)
		for _, e := range lg.adj[v] {
			c := comm[e.to]
			if c != c0 && wTo[c] == 0 {
				touched = append(touched, c)
			}
			wTo[c] += e.w
		}
		tot[c0] -= lg.k[v]
		stay := wTo[c0] - gamma*lg.k[v]*tot[c0]/lg.m2
		best, bestGain := c0, stay
		for _, c := range touched[1:] {
			gain := wTo[c] - gamma*lg.k[v]*tot[c]/lg.m2
			if gain <= stay+eps {
				continue
			}
			if best == c0 || gain > bestGain+eps || (gain >= bestGain-eps && c < best) {
				best, bestGain = c, gain
			}
		}
		for _, c := range touched {
			wTo[c] = 0
		}
		tot[best] += lg.k[v]
		if best == c0 {
			continue
		}
		comm[v] = best
		for _, e := range lg.adj[v] {
			if comm[e.to] != best && !inQ[e.to] {
				inQ[e.to] = true
				queue[(head+size)%n] = e.to
				size++
			}
		}
	}
	return nil
}

// refine builds the refined partition: every node starts alone and may merge
// only into a well-connected refined community inside its own community of
// comm. It takes the best merge, not a random one, so the result is a function
// of the input alone. Each refined community grows by edges, so it is
// connected by construction.
func refine(lg lgraph, comm []int, gamma float64) []int {
	n := len(comm)
	ref := make([]int, n)
	ext := make([]float64, n) // weight from refined community to the rest of its community
	kr := make([]float64, n)  // total degree of refined community
	single := make([]bool, n) // still alone
	kc := make([]float64, n)  // total degree of each community
	wTo := make([]float64, n)
	for i := 0; i < n; i++ {
		ref[i] = i
		single[i] = true
		kr[i] = lg.k[i]
		kc[comm[i]] += lg.k[i]
		for _, e := range lg.adj[i] {
			if comm[e.to] == comm[i] {
				ext[i] += e.w
			}
		}
	}
	touched := make([]int, 0, 16)
	for v := 0; v < n; v++ {
		if !single[v] {
			continue
		}
		c := comm[v]
		if ext[v] < gamma*lg.k[v]*(kc[c]-lg.k[v])/lg.m2-eps {
			continue
		}
		touched = touched[:0]
		for _, e := range lg.adj[v] {
			if comm[e.to] != c {
				continue
			}
			r := ref[e.to]
			if wTo[r] == 0 {
				touched = append(touched, r)
			}
			wTo[r] += e.w
		}
		best, bestGain := -1, 0.0
		for _, r := range touched {
			if ext[r] < gamma*kr[r]*(kc[c]-kr[r])/lg.m2-eps {
				continue
			}
			gain := wTo[r] - gamma*lg.k[v]*kr[r]/lg.m2
			if gain < -eps {
				continue
			}
			if best < 0 || gain > bestGain+eps || (gain >= bestGain-eps && r < best) {
				best, bestGain = r, gain
			}
		}
		if best >= 0 {
			ext[best] += ext[v] - 2*wTo[best]
			kr[best] += lg.k[v]
			ref[v] = best
			single[best] = false
			single[v] = false
		}
		for _, r := range touched {
			wTo[r] = 0
		}
	}
	return ref
}

// aggregate collapses each refined community into one node. agg maps old node
// to new node, numbered by smallest member.
func aggregate(lg lgraph, ref []int) (lgraph, []int, int) {
	n := len(ref)
	agg := make([]int, n)
	copy(agg, ref)
	m := renumber(agg)
	members := make([][]int, m)
	for v := 0; v < n; v++ {
		members[agg[v]] = append(members[agg[v]], v)
	}
	out := lgraph{adj: make([][]wedge, m), self: make([]float64, m), k: make([]float64, m), m2: lg.m2}
	acc := make([]float64, m)
	seen := make([]bool, m)
	var touched []int
	for a := 0; a < m; a++ {
		touched = touched[:0]
		for _, v := range members[a] {
			out.self[a] += lg.self[v]
			out.k[a] += lg.k[v]
			for _, e := range lg.adj[v] {
				b := agg[e.to]
				if b == a {
					out.self[a] += e.w
					continue
				}
				if !seen[b] {
					seen[b] = true
					touched = append(touched, b)
				}
				acc[b] += e.w
			}
		}
		sort.Ints(touched)
		for _, b := range touched {
			out.adj[a] = append(out.adj[a], wedge{b, acc[b]})
			acc[b], seen[b] = 0, false
		}
	}
	return out, agg, m
}

// leidenRun is one pass of the algorithm from the given starting partition. It
// returns the partition of the original nodes.
func leidenRun(ctx context.Context, lg0 lgraph, start []int, gamma float64) ([]int, error) {
	n0 := len(start)
	nodeOf := make([]int, n0)
	for i := range nodeOf {
		nodeOf[i] = i
	}
	lg := lg0
	comm := append([]int(nil), start...)
	for level := 0; level < maxLevels; level++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("detecting communities: %w", err)
		}
		if err := moveNodes(ctx, lg, comm, gamma); err != nil {
			return nil, err
		}
		n := len(comm)
		if renumber(append([]int(nil), comm...)) == n {
			break // every community is a single node: nothing left to merge
		}
		ref := refine(lg, comm, gamma)
		next, agg, m := aggregate(lg, ref)
		if m == n {
			break // refinement merged nothing, so aggregating would loop
		}
		nextComm := make([]int, m)
		for v := 0; v < n; v++ {
			nextComm[agg[v]] = comm[v]
		}
		renumber(nextComm)
		for i := range nodeOf {
			nodeOf[i] = agg[nodeOf[i]]
		}
		lg, comm = next, nextComm
	}
	out := make([]int, n0)
	for i := range out {
		out[i] = comm[nodeOf[i]]
	}
	return out, nil
}

// splitDisconnected gives each connected piece of a community its own label.
// Leiden's refinement makes this a no-op at convergence; it is kept as a
// guarantee for runs stopped by the iteration bound. Splitting a community
// with no edges between its pieces cannot lower modularity.
func splitDisconnected(lg lgraph, label []int) []int {
	n := len(label)
	out := make([]int, n)
	for i := range out {
		out[i] = -1
	}
	next := 0
	var stack []int
	for s := 0; s < n; s++ {
		if out[s] >= 0 {
			continue
		}
		out[s] = next
		stack = append(stack[:0], s)
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, e := range lg.adj[v] {
				if out[e.to] < 0 && label[e.to] == label[s] {
					out[e.to] = next
					stack = append(stack, e.to)
				}
			}
		}
		next++
	}
	return out
}

// detectLeiden partitions by the Leiden algorithm (Traag, Waltman and van Eck,
// 2019), maximising modularity at resolution gamma, with every choice the
// published algorithm leaves to chance fixed by the total order on keys.
func detectLeiden(ctx context.Context, g metrics.Graph, opts Options) ([]Assignment, error) {
	gamma := opts.Resolution
	if gamma == 0 {
		gamma = 1
	}
	if gamma < 0 {
		return nil, fmt.Errorf("cluster resolution must be positive, got %v", gamma)
	}
	maxIter := opts.MaxIterations
	if maxIter == 0 {
		maxIter = 10
	}
	if maxIter < 0 {
		return nil, fmt.Errorf("cluster max iterations must be positive, got %d", maxIter)
	}
	keys, lg := buildLevelZero(g)
	if len(keys) == 0 {
		return nil, nil
	}
	label := make([]int, len(keys))
	for i := range label {
		label[i] = i
	}
	if lg.m2 > 0 {
		for it := 0; it < maxIter; it++ {
			next, err := leidenRun(ctx, lg, label, gamma)
			if err != nil {
				return nil, err
			}
			renumber(next)
			same := true
			for i := range next {
				if next[i] != label[i] {
					same = false
					break
				}
			}
			label = next
			if same {
				break
			}
		}
		label = splitDisconnected(lg, label)
	}
	smallest := map[int]int{}
	for i, l := range label {
		if _, ok := smallest[l]; !ok {
			smallest[l] = i // ascending scan: first member is the smallest key
		}
	}
	out := make([]Assignment, len(keys))
	for i, k := range keys {
		out[i] = Assignment{Key: k, ClusterID: keys[smallest[label[i]]]}
	}
	return out, nil
}

// Modularity scores a partition of g, read as undirected and weighted, at
// resolution gamma (1 is standard modularity): the sum over communities of
// the weight inside it, less gamma times the share expected by chance. Nodes
// absent from the assignments are treated as singletons. It is 0 for a graph
// with no edges.
func Modularity(g metrics.Graph, assignments []Assignment, gamma float64) float64 {
	of := make(map[string]string, len(assignments))
	for _, a := range assignments {
		of[a.Key] = a.ClusterID
	}
	in := map[string]float64{}
	deg := map[string]float64{}
	var m2 float64
	for _, e := range g.Edges() {
		ca, ok := of[e.From]
		if !ok {
			ca = e.From
		}
		cb, ok := of[e.To]
		if !ok {
			cb = e.To
		}
		w := float64(e.Weight)
		deg[ca] += w
		deg[cb] += w
		m2 += 2 * w
		if ca == cb {
			in[ca] += 2 * w
		}
	}
	if m2 == 0 {
		return 0
	}
	ids := make([]string, 0, len(deg))
	for id := range deg {
		ids = append(ids, id)
	}
	sort.Strings(ids) // fixed summation order
	var q float64
	for _, id := range ids {
		q += in[id]/m2 - gamma*(deg[id]/m2)*(deg[id]/m2)
	}
	return q
}
