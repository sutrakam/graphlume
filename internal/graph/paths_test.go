package graph

import (
	"context"
	"reflect"
	"testing"
)

func TestPathsLinearChain(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(2, 3), e(3, 4)})

	got, err := g.Paths(context.Background(), 1, 4, 10, 5)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	want := [][]int64{{1, 2, 3, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestPathsDiamondReturnsBothRoutes(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(1, 3), e(2, 4), e(3, 4)})

	got, err := g.Paths(context.Background(), 1, 4, 10, 5)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	want := [][]int64{{1, 2, 4}, {1, 3, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestPathsRespectsMaxPaths(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(1, 3), e(1, 4), e(2, 5), e(3, 5), e(4, 5)})

	got, err := g.Paths(context.Background(), 1, 5, 2, 5)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d paths, want the requested 2", len(got))
	}
}

func TestPathsRespectsMaxDepth(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(2, 3), e(3, 4)})

	got, err := g.Paths(context.Background(), 1, 4, 10, 2)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("paths = %v, want none: 4 is three hops away", got)
	}
}

func TestPathsUnreachable(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(10, 11)})

	got, err := g.Paths(context.Background(), 1, 11, 10, 5)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("paths = %v, want none", got)
	}
}

func TestPathsSameNode(t *testing.T) {
	g := build(t, []tedge{e(1, 2)})

	got, err := g.Paths(context.Background(), 1, 1, 10, 5)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if !reflect.DeepEqual(got, [][]int64{{1}}) {
		t.Fatalf("paths = %v, want [[1]]", got)
	}
}

// TestPathsCycleTerminates: the backward walk over the layered predecessor DAG
// must not be able to loop, however cyclic the underlying graph is.
func TestPathsCycleTerminates(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(2, 3), e(3, 1), e(3, 4), e(4, 2)})

	got, err := g.Paths(context.Background(), 1, 4, 10, 10)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	want := [][]int64{{1, 2, 3, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

// TestPathsIgnoresLongerRoutes documents the shortest-paths-only bound
// explicitly, so a caller reading the test knows what is missing.
func TestPathsIgnoresLongerRoutes(t *testing.T) {
	// 1 -> 4 directly, and 1 -> 2 -> 3 -> 4 the long way round.
	g := build(t, []tedge{e(1, 4), e(1, 2), e(2, 3), e(3, 4)})

	got, err := g.Paths(context.Background(), 1, 4, 10, 5)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	want := [][]int64{{1, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want only the shortest route %v", got, want)
	}
}

// TestPathsParallelEdgesDoNotDuplicate: two edge kinds between the same pair
// describe one route, not two.
func TestPathsParallelEdgesDoNotDuplicate(t *testing.T) {
	g := build(t, []tedge{
		e(1, 2),
		e(1, 2).withKind("REFERENCES"),
		e(2, 3),
	})

	got, err := g.Paths(context.Background(), 1, 3, 10, 5)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	want := [][]int64{{1, 2, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestPathsCancellationMidWalk(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(2, 3), e(3, 4), e(4, 5)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.afterLevel = func(depth int) {
		if depth == 1 {
			cancel()
		}
	}

	if _, err := g.Paths(ctx, 1, 5, 10, 10); err == nil {
		t.Fatal("want a context error, got nil")
	}
}

func TestPathsMatchingExclusion(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(1, 3), e(2, 4), e(3, 4)})

	got, err := g.PathsMatching(context.Background(), 1, 4, 10, 5, nil, "", func(id int64) bool {
		return id == 2
	})
	if err != nil {
		t.Fatalf("paths matching: %v", err)
	}
	want := [][]int64{{1, 3, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths matching = %v, want %v", got, want)
	}
}

// Two diamonds in series: 1 -> {2,3} -> 4 -> {5,6} -> 7. Waypoints pick one
// branch of each, in order; a waypoint order the graph cannot honour, or a
// route longer than maxDepth, yields nothing.
func TestPathsThroughWaypointsInOrder(t *testing.T) {
	g := build(t, []tedge{e(1, 2), e(1, 3), e(2, 4), e(3, 4), e(4, 5), e(4, 6), e(5, 7), e(6, 7)})
	ctx := context.Background()

	got, err := g.PathsThrough(ctx, 1, []int64{3, 6}, 7, 10, 10, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]int64{{1, 3, 4, 6, 7}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("through 3 then 6 = %v, want %v", got, want)
	}

	got, err = g.PathsThrough(ctx, 1, []int64{6, 3}, 7, 10, 10, nil, "", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("waypoints in an order the graph cannot follow = %v (%v), want none", got, err)
	}

	got, err = g.PathsThrough(ctx, 1, []int64{3, 6}, 7, 10, 3, nil, "", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("a 4-hop route under maxDepth 3 = %v (%v), want none", got, err)
	}

	got, err = g.PathsThrough(ctx, 1, nil, 7, 10, 10, nil, "", nil)
	if err != nil || len(got) != 4 {
		t.Fatalf("no waypoints: %d routes (%v), want all 4 shortest", len(got), err)
	}

	if _, err := g.PathsThrough(ctx, 1, []int64{2, 3, 4, 5, 6}, 7, 10, 10, nil, "", nil); err == nil {
		t.Error("more than MaxWaypoints was accepted")
	}
}
