package impact

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

func rows(n int, c model.Confidence) []Impacted {
	out := make([]Impacted, n)
	for i := range out {
		out[i].Confidence = c
		out[i].Depth = 1
	}
	return out
}

// A walk that stopped at a bound is incomplete, so certainty is never high,
// but rows that all came through high or exact edges are not "low": that read
// as a contradiction next to the sentence counting them as strong.
func TestTruncatedCertaintyIsCappedNotErased(t *testing.T) {
	strong := Report{Direct: rows(11, model.ConfidenceExact), Indirect: rows(45, model.ConfidenceHigh), Truncated: true}
	level, reason := certaintyOfMix(strong)
	if level != "medium" {
		t.Fatalf("all-strong truncated walk: level %q, want medium", level)
	}
	if !strings.HasPrefix(reason, "The traversal stopped at a bound") {
		t.Fatalf("reason does not lead with the truncation: %q", reason)
	}

	weak := Report{Direct: rows(2, model.ConfidenceHigh), Indirect: rows(8, model.ConfidenceLow), Truncated: true}
	if level, _ := certaintyOfMix(weak); level != "low" {
		t.Fatalf("weak-majority truncated walk: level %q, want low", level)
	}

	if level, _ := certaintyOfMix(Report{Truncated: true}); level != "low" {
		t.Fatalf("empty truncated walk: level %q, want low", level)
	}
}
