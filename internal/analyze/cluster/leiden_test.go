package cluster

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/metrics"
	"github.com/sutrakam/graphlume/pkg/model"
)

func leidenOpts() Options {
	o := DefaultOptions()
	o.Algorithm = AlgorithmLeiden
	return o
}

func mkEdge(f, t string, w int) metrics.Edge {
	return metrics.Edge{From: f, To: t, Kind: model.EdgeCalls, Confidence: model.ConfidenceHigh, Weight: w}
}

func asMap(as []Assignment) map[string]string {
	m := map[string]string{}
	for _, a := range as {
		m[a.Key] = a.ClusterID
	}
	return m
}

// planted builds `groups` groups of `size` nodes: dense inside, noisy between.
func planted(seed int64, groups, size int, pIn, pOut float64, shuffle bool) metrics.Graph {
	r := rand.New(rand.NewSource(seed))
	var nodes []metrics.Node
	var edges []metrics.Edge
	name := func(g, i int) string { return fmt.Sprintf("g%02d/n%03d", g, i) }
	for g := 0; g < groups; g++ {
		for i := 0; i < size; i++ {
			nodes = append(nodes, metrics.Node{Key: name(g, i)})
		}
	}
	for g := 0; g < groups; g++ {
		for i := 0; i < size; i++ {
			for h := g; h < groups; h++ {
				for j := 0; j < size; j++ {
					if h == g && j <= i {
						continue
					}
					p := pOut
					if h == g {
						p = pIn
					}
					if r.Float64() < p {
						edges = append(edges, mkEdge(name(g, i), name(h, j), 1+r.Intn(3)))
					}
				}
			}
		}
	}
	if shuffle {
		r2 := rand.New(rand.NewSource(seed + 99))
		r2.Shuffle(len(nodes), func(i, j int) { nodes[i], nodes[j] = nodes[j], nodes[i] })
		r2.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
		for i := range edges {
			if r2.Intn(2) == 0 {
				edges[i].From, edges[i].To = edges[i].To, edges[i].From
			}
		}
	}
	return metrics.NewGraph(nodes, edges)
}

func TestLeidenSeparatesTwoCliques(t *testing.T) {
	got, err := Detect(context.Background(), twoCliques(), leidenOpts())
	if err != nil {
		t.Fatal(err)
	}
	m := asMap(got)
	if m["a1"] != m["a2"] || m["a2"] != m["a3"] || m["b1"] != m["b2"] || m["b2"] != m["b3"] {
		t.Errorf("cliques split: %v", m)
	}
	if m["a1"] == m["b1"] {
		t.Errorf("cliques merged: %v", m)
	}
	if m["a2"] != "a1" || m["b3"] != "b1" {
		t.Errorf("cluster id is not the smallest member key: %v", m)
	}
}

func TestLeidenIsDeterministicAcrossInputOrder(t *testing.T) {
	var want []Assignment
	for i := 0; i < 6; i++ {
		g := planted(7, 4, 12, 0.6, 0.05, i > 0)
		for rep := 0; rep < 2; rep++ {
			got, err := Detect(context.Background(), g, leidenOpts())
			if err != nil {
				t.Fatal(err)
			}
			if want == nil {
				want = got
			} else if !reflect.DeepEqual(want, got) {
				t.Fatalf("shuffle %d rep %d differs", i, rep)
			}
		}
	}
}

// connected reports whether every community induces a connected subgraph.
func connected(g metrics.Graph, as []Assignment) (bool, string) {
	of := asMap(as)
	adj := map[string][]string{}
	for _, e := range g.Edges() {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	members := map[string][]string{}
	for _, a := range as {
		members[a.ClusterID] = append(members[a.ClusterID], a.Key)
	}
	for id, ms := range members {
		seen := map[string]bool{ms[0]: true}
		stack := []string{ms[0]}
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, u := range adj[v] {
				if !seen[u] && of[u] == id {
					seen[u] = true
					stack = append(stack, u)
				}
			}
		}
		if len(seen) != len(ms) {
			return false, id
		}
	}
	return true, ""
}

func TestLeidenCommunitiesAreConnected(t *testing.T) {
	// Bridge scenario: two triangles joined through a bridge node x. Louvain
	// style moving can pull x's neighbours away and leave a community split.
	var nodes []metrics.Node
	for _, k := range []string{"a1", "a2", "a3", "b1", "b2", "b3", "x", "y"} {
		nodes = append(nodes, metrics.Node{Key: k})
	}
	edges := []metrics.Edge{
		mkEdge("a1", "a2", 5), mkEdge("a2", "a3", 5), mkEdge("a3", "a1", 5),
		mkEdge("b1", "b2", 5), mkEdge("b2", "b3", 5), mkEdge("b3", "b1", 5),
		mkEdge("a1", "x", 3), mkEdge("x", "b1", 3), mkEdge("x", "y", 1), mkEdge("y", "a2", 1),
	}
	graphs := []metrics.Graph{metrics.NewGraph(nodes, edges)}
	for s := int64(1); s <= 8; s++ {
		graphs = append(graphs, planted(s, 5, 10, 0.4, 0.08, true))
		// Sparse graphs break into many weakly attached pieces.
		graphs = append(graphs, planted(s, 6, 14, 0.12, 0.01, true))
	}
	for i, g := range graphs {
		got, err := Detect(context.Background(), g, leidenOpts())
		if err != nil {
			t.Fatal(err)
		}
		if ok, id := connected(g, got); !ok {
			t.Errorf("graph %d: community %s is disconnected", i, id)
		}
	}
}

func TestLeidenModularityAtLeastLabelPropagation(t *testing.T) {
	for s := int64(1); s <= 5; s++ {
		g := planted(s, 6, 15, 0.35, 0.06, false)
		lp, err := Detect(context.Background(), g, DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		ld, err := Detect(context.Background(), g, leidenOpts())
		if err != nil {
			t.Fatal(err)
		}
		qlp, qld := Modularity(g, lp, 1), Modularity(g, ld, 1)
		t.Logf("seed %d: label propagation %.4f, leiden %.4f", s, qlp, qld)
		if qld < qlp-1e-9 {
			t.Errorf("seed %d: leiden modularity %.4f < label propagation %.4f", s, qld, qlp)
		}
	}
}

func TestLeidenBoundsAndCancellation(t *testing.T) {
	g := planted(3, 4, 10, 0.5, 0.05, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Detect(ctx, g, leidenOpts()); err == nil {
		t.Error("cancelled context: want error")
	}
	o := leidenOpts()
	o.MaxIterations = 1
	if _, err := Detect(context.Background(), g, o); err != nil {
		t.Errorf("one iteration: %v", err)
	}
	o.Algorithm = "nope"
	if _, err := Detect(context.Background(), g, o); err == nil {
		t.Error("unknown algorithm: want error")
	}
	if got, err := Detect(context.Background(), metrics.NewGraph(nil, nil), leidenOpts()); err != nil || got != nil {
		t.Errorf("empty graph: %v %v", got, err)
	}
}
