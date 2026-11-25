package capability

import (
	"context"
	"errors"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
)

// TestResolveTargetAcceptsAnOwnerAndAMember is the CLI and library side of
// `Guard.AgainstNull`: the name was answered "not found" while the bare member
// resolved.
func TestResolveTargetAcceptsAnOwnerAndAMember(t *testing.T) {
	t.Parallel()
	reg := &fakeRegistry{symbols: map[string][]registry.SymbolIndexEntry{
		"Close": {
			entry(1, "Close", "io/reader.go:Reader.Close", "io/reader.go"),
			entry(2, "Close", "io/writer.go:Writer.Close", "io/writer.go"),
		},
	}}
	deps := NewDeps(reg, fakeGraph{})

	for _, name := range []string{"Reader.Close", "Reader::Close", "Reader#Close", "pkg.Reader.Close"} {
		tgt, err := ResolveTarget(context.Background(), deps, 1, name)
		if err != nil {
			t.Errorf("ResolveTarget(%q): %v", name, err)
			continue
		}
		if tgt.SymbolID != 1 {
			t.Errorf("ResolveTarget(%q) chose symbol %d, want the Reader's (1)", name, tgt.SymbolID)
		}
	}
	if _, err := ResolveTarget(context.Background(), deps, 1, "Buffer.Close"); !errors.Is(err, ErrTargetNotFound) {
		t.Errorf("an owner that does not declare the member resolved: %v", err)
	}
	var amb *AmbiguousTargetError
	if _, err := ResolveTarget(context.Background(), deps, 1, "Close"); !errors.As(err, &amb) {
		t.Errorf("the bare member must stay ambiguous: %v", err)
	}
}
