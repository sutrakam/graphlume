package pipeline

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// A Python value initialised from a call binds through the return annotation
// of that call's definition, and only then: an unannotated producer, a variable
// bound twice, or one reassigned from another call gives no return type.
func TestPythonValueTypedByAnnotatedReturn(t *testing.T) {
	atLeastHigh := model.ConfidenceHigh
	got := langEdges(t, map[string]string{
		"app/service.py": "class Service:\n    def compose(self, x):\n        return x\n\n\n" +
			"def make_service() -> Service:\n    return Service()\n\n\n" +
			"def untyped():\n    return Service()\n",
		"app/use.py": "from app.service import make_service, untyped, Service\n\n\n" +
			"def annotated():\n    svc = make_service()\n    svc.compose(2)\n\n\n" +
			"def unannotated():\n    svc = untyped()\n    svc.compose(3)\n\n\n" +
			"def rebound():\n    svc = make_service()\n    svc = Service()\n    svc.compose(4)\n",
	})
	requireEdge(t, got, "app/use.py:annotated", "app/service.py:Service.compose", atLeastHigh)
	// A name-only edge may remain (it did before this rule); what must not
	// appear is a typed one, since the variable's class is not known there.
	notTyped(t, got, "app/use.py:unannotated", "app/service.py:Service.compose")
	notTyped(t, got, "app/use.py:rebound", "app/service.py:Service.compose")
}

// notTyped fails on an edge at high or above, which would claim a type.
func notTyped(t *testing.T, got []langEdge, src, dst string) {
	t.Helper()
	for _, e := range got {
		if strings.HasSuffix(e.Src, src) && strings.HasSuffix(e.Dst, dst) && e.Confidence.AtLeast(model.ConfidenceHigh) {
			t.Errorf("typed edge %s -> %s (%s: %s) with no return type to justify it", e.Src, e.Dst, e.Confidence, e.Reason)
		}
	}
}
