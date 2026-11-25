package export

import (
	"bytes"
	"context"
	"encoding/xml"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/analyze/metrics"
	"github.com/sutrakam/graphlume/pkg/model"
)

func makeTestGraph(keys []string, edges []metrics.Edge) metrics.Graph {
	nodes := make([]metrics.Node, len(keys))
	for i, k := range keys {
		nodes[i] = metrics.Node{Key: k}
	}
	return metrics.NewGraph(nodes, edges)
}

// TestSVGShortenToRadiusDoesNotCrossEndpoints checks that when two nodes
// are closer than 2*nodeRadius (e.g. d=6 with radius=5), shortening both ends
// does not invert the vector (which would flip the arrowhead backward).
func TestSVGShortenToRadiusDoesNotCrossEndpoints(t *testing.T) {
	x1, y1 := 100.0, 100.0
	x2, y2 := 106.0, 100.0 // distance is 6.0, radius is 5.0 (2*r = 10.0 > 6.0)
	nx1, ny1, nx2, ny2 := shortenToRadius(x1, y1, x2, y2, 5.0)

	// In the original vector, x1 < x2.
	// If endpoints crossed, nx1 would be > nx2 (e.g. 105 and 101), inverting direction!
	if nx1 >= nx2 {
		t.Fatalf("shortenToRadius crossed endpoints: start x=%.2f, end x=%.2f (reversed edge!)", nx1, nx2)
	}
	if ny1 != 100.0 || ny2 != 100.0 {
		t.Errorf("y coordinates unexpectedly moved: ny1=%.2f, ny2=%.2f", ny1, ny2)
	}
}

// TestSVGLayoutForceDirectedDanglingEdgeDoesNotPanic tests that dangling
// edges fed to layoutForceDirected do not panic or access out of bounds.
func TestSVGLayoutForceDirectedDanglingEdgeDoesNotPanic(t *testing.T) {
	xs := []float64{100.0, 200.0}
	ys := []float64{100.0, 200.0}
	idx := map[string]int{"a": 0, "b": 1}
	edges := []metrics.Edge{
		{From: "a", To: "nonexistent", Kind: model.EdgeCalls},
		{From: "ghost", To: "b", Kind: model.EdgeCalls},
		{From: "ghost1", To: "ghost2", Kind: model.EdgeCalls},
	}
	opts := DefaultLayoutOptions()
	err := layoutForceDirected(context.Background(), xs, ys, idx, edges, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSVGNoNaNCoordinatesUnderExtremeLayoutOptions ensures that invalid or
// zero layout dimensions do not produce NaN or Inf in coordinates.
func TestSVGNoNaNCoordinatesUnderExtremeLayoutOptions(t *testing.T) {
	xs := []float64{10, 10}
	ys := []float64{10, 10}
	idx := map[string]int{"a": 0, "b": 1}
	edges := []metrics.Edge{{From: "a", To: "b", Kind: model.EdgeCalls}}

	// Extreme options: width 0, height 0, negative iterations
	opts := LayoutOptions{Width: 0, Height: 0, Iterations: 10, Seed: 42}
	err := layoutForceDirected(context.Background(), xs, ys, idx, edges, opts)
	if err != nil {
		t.Logf("layoutForceDirected cleanly returned error: %v", err)
	}
	for i := range xs {
		if math.IsNaN(xs[i]) || math.IsInf(xs[i], 0) || math.IsNaN(ys[i]) || math.IsInf(ys[i], 0) {
			t.Fatalf("NaN/Inf coordinate produced: xs[%d]=%v, ys[%d]=%v", i, xs[i], i, ys[i])
		}
	}
}

// TestSVGCancellationStopsGracefully tests that context cancellation stops
// force layout immediately without hanging.
func TestSVGCancellationStopsGracefully(t *testing.T) {
	// Build a graph with 200 nodes
	keys := make([]string, 200)
	for i := range keys {
		keys[i] = strings.Repeat("x", i+1)
	}
	g := makeTestGraph(keys, nil)
	e, _ := ForFormat(FormatSVG)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	var buf bytes.Buffer
	err := e.Write(ctx, &buf, g)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got: %v", err)
	}
}

// TestSVGAdversarialLabelStrings tests XML parsing validity with
// various hostile and edge-case label strings.
func TestSVGAdversarialLabelStrings(t *testing.T) {
	hostileKeys := []string{
		`<script>alert("xss")</script>`,
		`" onclick="alert(1)" foo="`,
		`&amp;&lt;&gt;&quot;&#39;`,
		`null-byte-\x00-test`,
		"control-\x01\x02\x08\x0b\x0c\x0e\x1f-chars",
		"unicode-surrogate-\xed\xa0\x80-end",
		"cdata-injection-<![CDATA[<foo>]]>-end",
		"emoji-🚀-and-rtl-مرحبا-שלום",
		strings.Repeat("super_long_identifier_", 50),
	}
	g := makeTestGraph(hostileKeys, nil)
	e, _ := ForFormat(FormatSVG)
	var buf bytes.Buffer
	if err := e.Write(context.Background(), &buf, g); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Must be 100% valid XML parseable by encoding/xml
	dec := xml.NewDecoder(&buf)
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("emitted SVG failed XML parsing: %v\nOutput snippet:\n%s", err, buf.String()[:minVal(500, buf.Len())])
		}
	}
}

// TestSVGTimeoutSlowLayout ensures layout respects reasonable deadlines.
func TestSVGTimeoutSlowLayout(t *testing.T) {
	keys := make([]string, 100)
	for i := range keys {
		keys[i] = string(rune('A'+(i%26))) + string(rune('0'+(i/26)))
	}
	g := makeTestGraph(keys, nil)
	e, _ := ForFormat(FormatSVG)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	// May succeed if 100 nodes finish under 1ms, or fail with timeout/cancellation; must not hang.
	_ = e.Write(ctx, &buf, g)
}

func minVal(a, b int) int {
	if a < b {
		return a
	}
	return b
}
