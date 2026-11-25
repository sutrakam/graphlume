package registry

import (
	"context"
	"testing"
)

func TestFileSizesReturnsOnlyFilesTheRunHolds(t *testing.T) {
	r, runID, _ := structureFixture(t)

	got, err := r.FileSizes(context.Background(), runID, []string{"a/a.go", "b/b.go", "gone/deleted.go", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got["a/a.go"] != 10 || got["b/b.go"] != 5 {
		t.Errorf("sizes = %v, want a/a.go=10 and b/b.go=5", got)
	}
	// Absent, not zero: a directory and a deleted file are not empty files.
	for _, missing := range []string{"gone/deleted.go", "a"} {
		if _, ok := got[missing]; ok {
			t.Errorf("%q is not a file in the run and must be absent from the result", missing)
		}
	}
}

func TestFileSizesIsScopedToTheRun(t *testing.T) {
	r, runID, _ := structureFixture(t)

	got, err := r.FileSizes(context.Background(), runID+1000, []string{"a/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("another run's id returned %v; sizes must come from the named run only", got)
	}
}

// More paths than one IN list holds must still all be answered.
func TestFileSizesChunksLargePathLists(t *testing.T) {
	r, runID, _ := structureFixture(t)

	paths := make([]string, 0, fileSizeChunk*2+2)
	for i := 0; i < fileSizeChunk*2; i++ {
		paths = append(paths, "missing/"+string(rune('a'+i%26))+".go")
	}
	paths = append(paths, "a/a.go", "b/b.go")

	got, err := r.FileSizes(context.Background(), runID, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d files, want the 2 that exist", len(got))
	}
}

func TestFileSizesOfNoPathsIsEmpty(t *testing.T) {
	r, runID, _ := structureFixture(t)
	got, err := r.FileSizes(context.Background(), runID, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("FileSizes(nil) = %v, %v; want an empty map and no error", got, err)
	}
}
