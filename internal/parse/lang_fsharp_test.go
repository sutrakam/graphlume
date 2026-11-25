package parse

import (
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

const fsharpStall = "module M\n\nlet f task =\n    let a = (^T: (member GetAwaiter: unit -> ^A) (task))\n    a\n\nlet g x = x\n"

func fsharpParser(t *testing.T) *genericParser {
	t.Helper()
	entry := grammars.DetectLanguageByName("fsharp")
	if entry == nil {
		t.Skip("no F# grammar")
	}
	p := newGenericParser(*entry, defaultParserConfig)
	if p.lang == nil {
		t.Skip("F# grammar did not load")
	}
	return p
}

// TestFSharpMemberConstraintCallStallsTheParser pins a measurement, not a
// wish. A statically-resolved member call, `(^T: (member M: unit -> ^A) (x))`,
// puts gotreesitter's F# parser into a loop that consumes no token. The
// library ends it with its no-progress guard, which it reports as an
// iteration-limit stop, so the plain parse of the file is incomplete.
//
// It is NOT the work limit running out: the limit is 30 iterations per byte
// (10,000 at least) and this five-line file stops after 156 of 10,000, a
// 690-line file after 3,218 of 825,480. The stopping point does not move when
// the limit is raised to fifty million (measured), so a larger per-grammar
// factor recovers nothing. The plain parse of that 690-line file reaches the
// stop in about 140 ms (both recovery routes), so the masked retry needs no
// wall-clock allowance of its own.
//
// mask_fsharp.go reads such a file around the stall. If a library upgrade
// fixes the loop this test fails; then the mask has nothing to do: remove
// mask_fsharp.go, its entry in spanMasks and its fixture file.
func TestFSharpMemberConstraintCallStallsTheParser(t *testing.T) {
	p := fsharpParser(t)
	_, _, stop, err := p.parseTree(t.Context(), []byte(fsharpStall))
	if err != nil {
		t.Fatal(err)
	}
	if !stop.early {
		t.Fatal("the parser now reads a member constraint call to the end; remove mask_fsharp.go and its fixture")
	}
	if stop.iterations*10 >= stop.iterLimit {
		t.Fatalf("stopped after %d of %d iterations: that is the limit running out, not a stall", stop.iterations, stop.iterLimit)
	}
}

// The mask makes the same file read to the end: the definitions after the call
// are found, the file is ok, and the result says it was read through a mask.
func TestFSharpMemberConstraintCallIsReadAroundByTheMask(t *testing.T) {
	res, err := fsharpParser(t).Parse(t.Context(), "m.fsx", []byte(fsharpStall))
	if err != nil {
		t.Fatal(err)
	}
	if res.ConstructsMasked != 1 {
		t.Fatalf("ConstructsMasked = %d, want 1", res.ConstructsMasked)
	}
	if res.Status != StatusOK {
		t.Fatalf("status %s (%v), want ok", res.Status, res.Err)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "read with 1 construct masked") {
		t.Errorf("the note is %v, want it to say the file was read with 1 construct masked", res.Err)
	}
	names := kindsByName(res)
	for _, want := range []string{"M", "f", "g"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing %s; got %v", want, names)
		}
	}
}
