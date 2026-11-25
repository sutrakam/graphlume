package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// hostInlineResultCharCap is the size, in characters, above which an MCP host
// stops delivering a tool result inline and instead writes it to a file for the
// agent to Read back.
//
// It is a property of the host, not of GraphLume, which is why it appears here
// as a named constant to test against rather than as a tunable.
const hostInlineResultCharCap = 25000

// TestDefaultLimitsFitHostInlineToolResultCap pins the one property of
// DefaultLimits that is not a matter of taste.
//
// If you are here because this test failed after you raised MaxTotalBytes:
// raising it past the host cap does not give agents bigger answers, it makes
// every large answer cost more. Past the cap the host spills the tool result to
// a file and the agent issues a Read to retrieve it, so GraphLume pays for the
// file read and the cache write it was built to avoid. The correct fix is
// almost never a larger budget; it is a narrower query or a smaller excerpt.
//
// The only legitimate reason to change this test is evidence that the host's
// inline cap itself moved. Update hostInlineResultCharCap with that evidence
// first, then the limit — never the other way round.
func TestDefaultLimitsFitHostInlineToolResultCap(t *testing.T) {
	got := DefaultLimits().MaxTotalBytes

	if got >= hostInlineResultCharCap {
		t.Fatalf("DefaultLimits().MaxTotalBytes = %d, want < %d (host externalises "+
			"larger tool results to a file the agent must Read back, reintroducing "+
			"the read and cache-write cost GraphLume exists to eliminate)",
			got, hostInlineResultCharCap)
	}

	if got <= 0 {
		t.Fatalf("DefaultLimits().MaxTotalBytes = %d, want a positive bound; "+
			"a non-positive value disables the total-size check in Bound entirely", got)
	}
}

// TestBoundEnforcesTotalBytesAgainstOversubscribedItemBudget covers the
// interaction between the per-item limits and the whole-response limit.
//
// MaxItems x MaxExcerptBytes (50 x 2000 = 100000) deliberately oversubscribes
// MaxTotalBytes. That is not a misconfiguration: the per-item limits exist to
// stop any single excerpt from dominating a response, while MaxTotalBytes is
// what actually bounds the response. The arrangement is only safe because Bound
// applies the total after each item, so this test holds it to that.
func TestBoundEnforcesTotalBytesAgainstOversubscribedItemBudget(t *testing.T) {
	l := DefaultLimits()

	if l.MaxItems*l.MaxExcerptBytes <= l.MaxTotalBytes {
		t.Skip("per-item budgets no longer oversubscribe the total; this test guards the opposite case")
	}

	r := &Response{Summary: "oversubscribed"}
	for i := 0; i < l.MaxItems; i++ {
		r.Items = append(r.Items, Item{
			Kind:    "func",
			Path:    "a.go",
			Excerpt: strings.Repeat("x", l.MaxExcerptBytes),
		})
	}

	r.Bound(l)

	if got := len(r.RenderText()); got > l.MaxTotalBytes {
		t.Fatalf("bounded response renders to %d bytes, want <= %d",
			got, l.MaxTotalBytes)
	}
	if !r.Truncated {
		t.Fatal("response was cut but Truncated is false; an agent would read a partial answer as complete")
	}
	if r.TruncatedReason == "" {
		t.Fatal("Truncated set without a reason; the agent cannot tell whether to narrow the query")
	}
}

// TestRenderedLengthNeverExceedsTheCap is the assertion the whole size budget
// exists to support: whatever a caller hands Bound, what the host measures fits
// inline.
//
// Bound must count what the host counts. Were it to
// count only the summary plus the excerpts while the host counts the rendered
// text — section headers, the untrusted preamble, the notes, a provenance line
// per item and a banner per excerpt would all go uncounted. A response reported as
// 24,008 bytes would render at 27,270, and if item metadata had no length limit
// the shortfall would have no ceiling: no value of MaxTotalBytes could fix it.
//
// The cases below are chosen to attack the budget from three directions — many
// small items (overhead dominates), few large ones (excerpts dominate), and
// metadata far beyond any plausible real value (the unbounded axis).
func TestRenderedLengthNeverExceedsTheCap(t *testing.T) {
	l := DefaultLimits()

	cases := []struct {
		name    string
		items   int
		name_   int
		path    int
		why     int
		excerpt int
	}{
		{"ordinary metadata, full excerpts", l.MaxItems, 20, 45, 60, l.MaxExcerptBytes},
		{"many items, short excerpts", l.MaxItems, 20, 45, 60, 300},
		{"long metadata, short excerpts", l.MaxItems, 400, 600, 2000, 300},
		{"metadata only, no excerpts", l.MaxItems, 400, 600, 2000, 0},
		{"over the item cap", l.MaxItems * 4, 60, 120, 200, 800},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Response{
				Summary:       "measured",
				IndexRevision: 42,
				IndexStatus:   "fresh",
				Notes:         []string{strings.Repeat("n", 200)},
			}
			for i := 0; i < tc.items; i++ {
				r.Items = append(r.Items, Item{
					Kind:       "func",
					Name:       strings.Repeat("N", tc.name_),
					Path:       strings.Repeat("p", tc.path),
					Line:       123456,
					Confidence: "certain",
					Source:     "compiler",
					Why:        strings.Repeat("w", tc.why),
					Excerpt:    strings.Repeat("x", tc.excerpt),
				})
			}

			r.Bound(l)
			rendered := len(r.RenderText())

			t.Logf("items kept=%d rendered=%d (budget %d, host cap %d)",
				len(r.Items), rendered, l.MaxTotalBytes, hostInlineResultCharCap)

			if rendered >= hostInlineResultCharCap {
				t.Fatalf("rendered response is %d chars, want < %d: the host will spill "+
					"this to a file and the agent will Read it back, paying the cost "+
					"GraphLume exists to remove", rendered, hostInlineResultCharCap)
			}
			if rendered > l.MaxTotalBytes {
				t.Fatalf("rendered response is %d chars, over its own budget of %d: Bound "+
					"is measuring something other than what RenderText emits",
					rendered, l.MaxTotalBytes)
			}
		})
	}
}

// TestBoundKeepsWhatItCanFit guards the other direction: the bound must not be
// so eager that it starves an ordinary answer. A response that fits should keep
// every item and say nothing about truncation.
func TestBoundKeepsWhatItCanFit(t *testing.T) {
	l := DefaultLimits()

	r := &Response{Summary: "small", IndexRevision: 1, IndexStatus: "fresh"}
	for i := 0; i < 5; i++ {
		r.Items = append(r.Items, Item{
			Kind: "func", Name: "Handler", Path: "internal/api/handler.go", Line: 88,
			Confidence: "high", Source: "inference", Why: "explicit import",
			Excerpt: "func Handler() {}\n",
		})
	}

	r.Bound(l)

	if len(r.Items) != 5 {
		t.Fatalf("kept %d of 5 items that comfortably fit; the bound is over-trimming", len(r.Items))
	}
	if r.Truncated {
		t.Fatalf("Truncated set on a response that fits: %q", r.TruncatedReason)
	}
}

// TestItemMetadataIsClipped pins the field bounds that make the total a
// guarantee rather than an estimate.
func TestItemMetadataIsClipped(t *testing.T) {
	l := DefaultLimits()

	r := &Response{Summary: "clip", Items: []Item{{
		Kind: "func",
		Name: strings.Repeat("N", 5000),
		Path: strings.Repeat("p", 5000),
		Why:  strings.Repeat("w", 5000),
	}}}

	r.Bound(l)

	if len(r.Items) == 0 {
		t.Fatal("the only item was dropped; one item with long metadata should be clipped, not discarded")
	}
	it := r.Items[0]
	if len(it.Name) > l.MaxNameBytes {
		t.Errorf("Name is %d bytes, want <= %d", len(it.Name), l.MaxNameBytes)
	}
	if len(it.Path) > l.MaxPathBytes {
		t.Errorf("Path is %d bytes, want <= %d", len(it.Path), l.MaxPathBytes)
	}
	if len(it.Why) > l.MaxWhyBytes {
		t.Errorf("Why is %d bytes, want <= %d", len(it.Why), l.MaxWhyBytes)
	}
	if !utf8.ValidString(it.Name) || !utf8.ValidString(it.Path) || !utf8.ValidString(it.Why) {
		t.Error("clipping produced invalid UTF-8; JSON encoding would corrupt the field")
	}
}

// TestClipNeverReturnsSomethingLongerThanTheOriginal pins the guarantee
// NeverWorse exists to provide: on an input just barely over max, the
// ellipsis suffix alone can outweigh a short original, so clip must fall
// back to returning s unchanged rather than a "truncated" form that is
// bigger than what it replaced.
//
// max=1 forces cut to clamp at 0 (max - len("…") is negative), so unguarded
// clip discards all of s and returns the bare ellipsis (3 bytes) in place of
// a 2-byte original — currently FAILS, proving clip returns the longer
// string.
func TestClipNeverReturnsSomethingLongerThanTheOriginal(t *testing.T) {
	full := "ab"
	got := clip(full, 1)

	if len(got) > len(full) {
		t.Fatalf("clip(%q, 1) = %q (%d bytes), longer than the %d-byte original %q; "+
			"clip must never spend more bytes to say less", full, got, len(got), len(full), full)
	}
	if got != full {
		t.Fatalf("clip(%q, 1) = %q, want the unclipped original %q since clipping would grow it",
			full, got, full)
	}
}

// TestBoundExcerptLineTruncationNeverReturnsSomethingLongerThanTheOriginal
// pins the same guarantee for the line-truncation branch of boundExcerpt: the
// "\n… (truncated)" suffix is 16 bytes, larger than many real short excerpts
// that merely exceed MaxExcerptLines — currently FAILS, proving boundExcerpt
// returns the longer string.
func TestBoundExcerptLineTruncationNeverReturnsSomethingLongerThanTheOriginal(t *testing.T) {
	full := "a\nb"
	l := Limits{MaxExcerptLines: 1}

	got := boundExcerpt(full, l)

	if len(got) > len(full) {
		t.Fatalf("boundExcerpt(%q, %+v) = %q (%d bytes), longer than the %d-byte original %q; "+
			"boundExcerpt must never spend more bytes to say less", full, l, got, len(got), len(full), full)
	}
	if got != full {
		t.Fatalf("boundExcerpt(%q, %+v) = %q, want the untruncated original %q since truncating would grow it",
			full, l, got, full)
	}
}

// TestBoundExcerptByteTruncationNeverReturnsSomethingLongerThanTheOriginal
// pins the same guarantee for the byte-truncation branch: the
// "… (truncated)" suffix (15 bytes) can outweigh what a small MaxExcerptBytes
// trimmed off a short excerpt — currently FAILS, proving boundExcerpt returns
// the longer string.
func TestBoundExcerptByteTruncationNeverReturnsSomethingLongerThanTheOriginal(t *testing.T) {
	full := "abcdef"
	l := Limits{MaxExcerptBytes: 5}

	got := boundExcerpt(full, l)

	if len(got) > len(full) {
		t.Fatalf("boundExcerpt(%q, %+v) = %q (%d bytes), longer than the %d-byte original %q; "+
			"boundExcerpt must never spend more bytes to say less", full, l, got, len(got), len(full), full)
	}
	if got != full {
		t.Fatalf("boundExcerpt(%q, %+v) = %q, want the untruncated original %q since truncating would grow it",
			full, l, got, full)
	}
}

// A confidence and source every row shares is said once, above the rows, once
// there are enough of them; a mixed answer tags each row.
func TestASharedTagIsSaidOnceAndAMixedAnswerTagsEveryRow(t *testing.T) {
	row := func(conf, src string) Item {
		return Item{Kind: "calls", Name: "f", Path: "a.go", Line: 1, Confidence: conf, Source: src}
	}
	same := &Response{Summary: "s", Items: []Item{row("exact", "compiler"), row("exact", "compiler"), row("exact", "compiler"), row("exact", "compiler"), row("exact", "compiler")}}
	out := same.RenderText()
	if got := strings.Count(out, "[confidence=exact source=compiler]"); got != 1 {
		t.Errorf("a shared tag appears %d times, want once:\n%s", got, out)
	}
	if !strings.Contains(out, "all 5 rows: [confidence=exact source=compiler]") {
		t.Errorf("the shared tag line is missing:\n%s", out)
	}

	few := &Response{Summary: "s", Items: []Item{row("exact", "compiler"), row("exact", "compiler")}}
	if got := strings.Count(few.RenderText(), "[confidence=exact source=compiler]"); got != 2 {
		t.Errorf("two rows should each carry the tag, got %d", got)
	}

	mixed := &Response{Summary: "s", Items: []Item{row("exact", "compiler"), row("exact", "compiler"), row("exact", "compiler"), row("exact", "compiler"), row("medium", "source_code")}}
	mo := mixed.RenderText()
	if strings.Contains(mo, "all 5 rows") || strings.Count(mo, "[confidence=") != 5 {
		t.Errorf("a mixed answer must tag every row:\n%s", mo)
	}
}
