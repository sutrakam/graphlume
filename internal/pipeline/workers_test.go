package pipeline

import (
	"math"
	"testing"

	"github.com/sutrakam/graphlume/internal/acquire/fs"
	"github.com/sutrakam/graphlume/internal/config"
)

func TestWorkersFor(t *testing.T) {
	const (
		mib = int64(1) << 20
		gib = int64(1) << 30
	)
	for _, tc := range []struct {
		name  string
		cpus  int
		limit int64
		want  int
	}{
		// By CPU alone: half the CPUs, between 4 and 8, never more than
		// there are.
		{"one cpu", 1, 0, 1},
		{"two cpus", 2, 0, 2},
		{"three cpus", 3, 0, 3},
		{"four cpus", 4, 0, 4},
		{"eight cpus", 8, 0, 4},
		{"ten cpus", 10, 0, 5},
		{"fourteen cpus", 14, 0, 7},
		{"sixteen cpus", 16, 0, 8},
		{"sixty-four cpus", 64, 0, 8},
		{"zero cpus reported", 0, 0, 1},
		{"no limit", 64, math.MaxInt64, 8},

		// The default 768 MiB limit allows exactly the ceiling.
		{"default limit, many cpus", 64, 768 * mib, 8},
		{"default limit, ten cpus", 10, 768 * mib, 5},

		// A lower limit lowers the count, below the CPU floor if it must.
		{"512 MiB", 16, 512 * mib, 4},
		{"400 MiB", 16, 400 * mib, 2},
		{"tiny limit", 16, 100 * mib, 1},

		// A higher limit never raises it past the CPU bound.
		{"4 GiB, ten cpus", 10, 4 * gib, 5},
		{"4 GiB, many cpus", 64, 4 * gib, 8},
	} {
		if got := WorkersFor(tc.cpus, tc.limit); got != tc.want {
			t.Errorf("%s: WorkersFor(%d, %d) = %d, want %d", tc.name, tc.cpus, tc.limit, got, tc.want)
		}
	}
}

// The memory bound is what keeps a run inside the soft limit: the budget it
// sets aside never exceeds the limit, whatever the machine.
func TestWorkersForRespectsTheLimit(t *testing.T) {
	for cpus := 1; cpus <= 128; cpus++ {
		for limit := WorkerMemoryBase + WorkerMemoryBudget; limit <= 8<<30; limit += 32 << 20 {
			n := WorkersFor(cpus, limit)
			if WorkerMemoryBase+int64(n)*WorkerMemoryBudget > limit {
				t.Fatalf("WorkersFor(%d, %d) = %d: budget %d exceeds the limit",
					cpus, limit, n, WorkerMemoryBase+int64(n)*WorkerMemoryBudget)
			}
			if n < 1 || n > WorkersCeiling {
				t.Fatalf("WorkersFor(%d, %d) = %d, outside 1..%d", cpus, limit, n, WorkersCeiling)
			}
		}
	}
}

func TestExplicitWorkersOverrideTheDefault(t *testing.T) {
	w, err := fs.NewWalker(t.TempDir(), config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if r := NewRunner(w, Options{Workers: 13, MemoryLimit: 100 << 20}); r.Workers() != 13 {
		t.Fatalf("Workers() = %d, want the 13 asked for", r.Workers())
	}
	// Unset, a 100 MiB limit holds the default to one worker.
	if r := NewRunner(w, Options{MemoryLimit: 100 << 20}); r.Workers() != 1 {
		t.Fatalf("Workers() = %d under a 100 MiB limit, want 1", r.Workers())
	}
}
