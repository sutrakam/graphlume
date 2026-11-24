package registry

import (
	"fmt"
	"strings"
	"testing"
)

// The walk below a type must reach each queued type's subtypes through
// idx_edges_dst. The planner's own choice for "edges JOIN sub" read every edge
// of the run once per queued type; an answer about a widely implemented
// interface on a large repository did not finish in 30 minutes. A plan
// regresses without any test of the RESULT noticing, so the plan is pinned.
func TestTheSubtypeWalkLooksEachTypeUpByDestination(t *testing.T) {
	_, db := newTestRegistry(t)

	rank := fmt.Sprintf(confidenceRankCase, "e.confidence")
	for _, floor := range []string{"", " AND " + rank + " >= ?"} {
		args := []any{1, 2, "IMPLEMENTS", "EXTENDS"}
		if floor != "" {
			args = append(args, 2)
		}
		args = append(args, 1, "IMPLEMENTS", "EXTENDS")
		if floor != "" {
			args = append(args, 2)
		}
		args = append(args, maxSubtypeDepth, maxSubtypeRows+1)

		plan := queryPlan(t, db, subtypeWalkSQL("?,?", rank, floor)+` SELECT COUNT(*) FROM sub`, args...)
		joined := strings.Join(plan, "\n")

		step := -1
		for i, line := range plan {
			if strings.Contains(line, "RECURSIVE STEP") {
				step = i
			}
		}
		if step < 0 || step+2 >= len(plan) {
			t.Fatalf("no recursive step in the plan:\n%s", joined)
		}
		if !strings.Contains(plan[step+1], "SCAN sub") {
			t.Errorf("the recursive step does not start from the queue:\n%s", joined)
		}
		if !strings.Contains(plan[step+2], "SEARCH e USING INDEX idx_edges_dst") {
			t.Errorf("the recursive step does not reach edges through idx_edges_dst:\n%s", joined)
		}
	}
}
