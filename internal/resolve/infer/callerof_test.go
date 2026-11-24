package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
)

// TestCallerOfPrefersTheEnclosingDefinition: a line inside a definition is that
// definition's, even in a file that has a synthetic caller; a line outside every
// definition is the file's, and a file with no synthetic caller yields none.
func TestCallerOfPrefersTheEnclosingDefinition(t *testing.T) {
	syms := []registry.EnclosingSymbol{
		{ID: 1, Name: "helper", StartLine: 10, EndLine: 20},
		{ID: 2, Name: "single", StartLine: 25},
	}
	with := registry.FileRefs{Symbols: syms, FileCaller: 99}
	without := registry.FileRefs{Symbols: syms}
	for _, c := range []struct {
		line   int
		fr     registry.FileRefs
		wantID int64
		wantOK bool
	}{
		{12, with, 1, true},
		{25, with, 2, true},
		{26, with, 99, true},
		{3, with, 99, true},
		{30, with, 99, true},
		{30, without, 0, false},
		{12, without, 1, true},
	} {
		id, ok := callerOf(c.fr, c.line)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("callerOf(line %d, caller %d) = %d, %v; want %d, %v",
				c.line, c.fr.FileCaller, id, ok, c.wantID, c.wantOK)
		}
	}
	// What encloses a line, for the owner of `self`, never names the file.
	if id, ok := enclosing(syms, 30); ok {
		t.Errorf("enclosing found %d outside every definition", id)
	}
}
