package capability

import (
	"context"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/registry"
)

// slowText is a registry whose text index never answers in time.
type slowText struct {
	*fakeRegistry
}

func (slowText) UnrecordedMentions(ctx context.Context, _ int64, _ string, _ []registry.DefSpan) (registry.UnrecordedMentions, error) {
	<-ctx.Done()
	return registry.UnrecordedMentions{}, ctx.Err()
}

func (slowText) IncomingEdgeCounts(context.Context, int64, []int64) (map[int64]int, error) {
	return nil, nil
}

// TestTheUsedAsAValueCheckStopsAtItsTimeBudget: a check that outlives its
// budget returns what it has and says it stopped, and is not an error. A
// caller's own cancellation is still an error.
func TestTheUsedAsAValueCheckStopsAtItsTimeBudget(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{symbols: map[string][]registry.SymbolIndexEntry{
		"handle": {{ID: 1, Name: "handle", FQN: "a.go:handle", Kind: "function", FilePath: "a.go"}},
	}}
	deps := NewDeps(slowText{reg}, fakeGraph{})
	tgt := Target{SymbolID: 1, Name: "handle", FQN: "a.go:handle", Kind: "function", FilePath: "a.go", Line: 1}

	start := time.Now()
	vu, err := deps.ValueUse(context.Background(), 1, tgt, nil, nil)
	if err != nil {
		t.Fatalf("a check that ran out of time is an answer, not an error: %v", err)
	}
	if !vu.OutOfTime || vu.Checked != 0 || vu.Any() {
		t.Errorf("ValueUse = %+v, want out of time with nothing checked", vu)
	}
	if took := time.Since(start); took > TextBudget+2*time.Second {
		t.Errorf("the check took %s, far over its %s budget", took, TextBudget)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := deps.ValueUse(ctx, 1, tgt, nil, nil); err == nil {
		t.Error("the caller's own cancellation was swallowed as if it were the time budget")
	}
}

// TestAConstructorIsNeverCheckedForUseAsAValue: its name is its type's.
func TestAConstructorIsNeverCheckedForUseAsAValue(t *testing.T) {
	t.Parallel()
	for fqn, want := range map[string]bool{
		"src/Config.cs:LoggerConfiguration.LoggerConfiguration": true,
		"src/Guard.cs:Guard.AgainstNull":                        false,
		"lib/app.js:respond":                                    false,
		"a/b.rs:CommandSet.new":                                 false,
	} {
		if got := constructorLike(fqn); got != want {
			t.Errorf("constructorLike(%q) = %v, want %v", fqn, got, want)
		}
	}
}
