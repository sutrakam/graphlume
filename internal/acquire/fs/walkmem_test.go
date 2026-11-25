package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWalkStreamHoldsLessThanWalk measures what discovery keeps alive on a
// large tree: Walk returns every Discovered at once, WalkStream hands each to
// a callback and keeps none. Opt-in (RC_WALK_MEM=<files>), because writing a
// six-figure tree is slow; the figures it logs are the evidence behind
// pipeline.StreamingThreshold.
func TestWalkStreamHoldsLessThanWalk(t *testing.T) {
	n := 0
	if v := os.Getenv("RC_WALK_MEM"); v != "" {
		_, _ = fmt.Sscan(v, &n)
	}
	if n <= 0 {
		t.Skip("set RC_WALK_MEM=<file count> to measure")
	}
	root := t.TempDir()
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%03d", i%500), fmt.Sprintf("sub%02d", (i/500)%20))
		if i < 500*20 {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file_%07d.go", i)), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := NewWalker(root, minimalCfg())
	if err != nil {
		t.Fatal(err)
	}

	live := func() uint64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}

	base := live()
	all, err := w.Walk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	held := live() - base
	// KeepAlive after the measurement: without it the compiler may treat the
	// slice's backing array as dead once only its length is used later, and
	// the GC inside live() frees exactly what is being measured.
	runtime.KeepAlive(all)
	got := len(all)

	base = live()
	var streamed int
	var peak uint64
	err = w.WalkStream(context.Background(), func(Discovered) error {
		streamed++
		if streamed%20000 == 0 {
			if h := live(); h > base && h-base > peak {
				peak = h - base
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamed != got {
		t.Fatalf("WalkStream found %d files, Walk %d", streamed, got)
	}
	t.Logf("%d files: Walk holds %.1f MB (%.0f B/file); WalkStream peak while walking %.1f MB",
		got, float64(held)/1e6, float64(held)/float64(got), float64(peak)/1e6)
	if peak >= held {
		t.Errorf("streaming held %d bytes, no less than Walk's %d", peak, held)
	}
}
