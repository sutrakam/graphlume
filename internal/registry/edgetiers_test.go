package registry

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// TestReferenceEdgeTiersCountsConstructorEdges: the resolver counts an
// INSTANTIATES edge among the edges it writes, so the total read back from the
// table has to include it. While it did not, `graphlume resolve` printed
// "Edges: 25917 (26056 written by this pass)", more written than exist.
func TestReferenceEdgeTiersCountsConstructorEdges(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)

	run, err := r.BeginRun(ctx, "auto", "rev")
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}
	const from = "src/main/java/com/pay/model/Payment.java"
	const to = "src/main/java/com/pay/billing/Invoice.java"
	edge := func(kind model.EdgeKind) edgeSpec {
		return edgeSpec{
			srcFQN: model.DefaultFQN(from, "Payment"), dstFQN: model.DefaultFQN(to, "Invoice"),
			kind: kind, conf: model.ConfidenceHigh, source: model.SourceSourceCode,
			evFile: from, evLine: 12,
		}
	}
	files := []fileSpec{classFile("model", "Payment"), classFile("billing", "Invoice")}
	writeContent(t, r, run.ID, files, []edgeSpec{edge(model.EdgeCalls), edge(model.EdgeInstantiates)})

	tiers, total, err := r.ReferenceEdgeTiers(ctx, run.ID)
	if err != nil {
		t.Fatalf("tiers: %v", err)
	}
	if total != 2 || tiers[model.ConfidenceHigh] != 2 {
		t.Errorf("total=%d tiers=%v, want the CALLS and the INSTANTIATES edge counted", total, tiers)
	}
}
