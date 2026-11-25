package gotypes

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
)

// The answer layer recognises the type check's edges by this detail, and
// cannot import this package to read it, so the two constants must agree.
func TestTheRegistryKnowsTheTypeCheckDetail(t *testing.T) {
	if registry.GoTypesDetail != Detail {
		t.Fatalf("registry.GoTypesDetail = %q, gotypes.Detail = %q", registry.GoTypesDetail, Detail)
	}
}
