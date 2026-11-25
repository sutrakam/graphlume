package mcp

import (
	"fmt"
	"strings"
	"testing"
	"testing/quick"
)

var allLevels = []ReliabilityLevel{
	ReliabilityGood, ReliabilityIncomplete, ReliabilityUnverified, ReliabilityLow, ReliabilityNone,
}

func histogram(exact, high, medium, low int) map[string]int {
	return map[string]int{"exact": exact, "high": high, "medium": medium, "low": low}
}

// signalViolations states every rule OverallSignal must obey for one input and
// returns the ones it broke. It is the property; the two tests below feed it
// every small input and a great many random ones.
func signalViolations(counts map[string]int, level ReliabilityLevel, truncated bool) []string {
	got := OverallSignal(SignalInput{Counts: counts, Reliability: level, Truncated: truncated})
	graded := 0
	for _, n := range counts {
		graded += n
	}
	var out []string
	fail := func(format string, args ...any) {
		out = append(out, fmt.Sprintf("counts=%v level=%q truncated=%v -> %q: ", counts, level, truncated, got)+
			fmt.Sprintf(format, args...))
	}

	if graded == 0 {
		if got != VerdictEmpty {
			fail("no graded rows must be empty")
		}
		return out
	}
	if got == VerdictEmpty {
		fail("rows were returned, so the answer is not empty")
	}

	// THE RULE THE DEFECT BROKE: coverage low or unknown is never strong.
	if level == ReliabilityLow || level == ReliabilityNone {
		if got != VerdictUnreliable {
			fail("low or unknown coverage must be unreliable whatever the rows say")
		}
	}
	if level != ReliabilityGood && got.actionable() {
		fail("a verdict that says 'act on these' needs good coverage")
	}
	if truncated && got.actionable() {
		fail("rows were cut, so the set is not one to act on as the answer")
	}
	if got.actionable() && graded-counts["exact"]-counts["high"] > 0 {
		fail("an actionable verdict over rows weaker than high")
	}
	if got == VerdictProven && counts["exact"] != graded {
		fail("proven needs every row compiler-proven")
	}

	// Worse coverage never yields a more trustworthy verdict.
	if level != ReliabilityGood {
		best := OverallSignal(SignalInput{Counts: counts, Reliability: ReliabilityGood, Truncated: truncated})
		if got.actionable() && !best.actionable() {
			fail("is actionable where good coverage (%q) is not", best)
		}
	}
	// Neither does cutting rows.
	if truncated {
		whole := OverallSignal(SignalInput{Counts: counts, Reliability: level})
		if got.actionable() && !whole.actionable() {
			fail("is actionable where the uncut answer (%q) is not", whole)
		}
	}
	return out
}

// TestSignalNeverExceedsItsCoverage is the property, exhaustively, over every
// histogram of up to three rows per tier, every coverage level, cut and uncut.
func TestSignalNeverExceedsItsCoverage(t *testing.T) {
	for exact := 0; exact <= 3; exact++ {
		for high := 0; high <= 3; high++ {
			for medium := 0; medium <= 3; medium++ {
				for low := 0; low <= 3; low++ {
					for _, level := range allLevels {
						for _, truncated := range []bool{false, true} {
							for _, v := range signalViolations(histogram(exact, high, medium, low), level, truncated) {
								t.Error(v)
							}
						}
					}
				}
			}
		}
	}
}

// TestSignalPropertyHoldsForRandomAnswers is the same property over inputs no
// one enumerated, including a tier this package has never heard of.
func TestSignalPropertyHoldsForRandomAnswers(t *testing.T) {
	prop := func(exact, high, medium, low, other uint8, levelIdx uint8, truncated bool) bool {
		counts := histogram(int(exact), int(high), int(medium), int(low))
		if other > 0 {
			counts["speculative"] = int(other)
		}
		violations := signalViolations(counts, allLevels[int(levelIdx)%len(allLevels)], truncated)
		for _, v := range violations {
			t.Log(v)
		}
		return len(violations) == 0
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}

// TestGoodCoverageKeepsTheOldGrades: the ceiling removes verdicts, it does not
// invent them. With nothing held against the rows they grade as they did.
func TestGoodCoverageKeepsTheOldGrades(t *testing.T) {
	cases := []struct {
		counts map[string]int
		want   VerdictStatus
	}{
		{histogram(3, 0, 0, 0), VerdictProven},
		{histogram(1, 2, 0, 0), VerdictStrong},
		{histogram(0, 4, 0, 0), VerdictStrong},
		{histogram(1, 0, 1, 2), VerdictMixed},
		{histogram(0, 0, 2, 2), VerdictWeak},
		{histogram(0, 0, 0, 0), VerdictEmpty},
	}
	for _, tc := range cases {
		if got := OverallSignal(SignalInput{Counts: tc.counts, Reliability: ReliabilityGood}); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.counts, got, tc.want)
		}
		// An absent level is a response that makes no relationship claim, and
		// is graded as good.
		if got := OverallSignal(SignalInput{Counts: tc.counts}); got != tc.want {
			t.Errorf("%v with no level: got %q, want %q", tc.counts, got, tc.want)
		}
	}
}

// actionWords are the sentences that tell an agent to go ahead.
var actionWords = []string{"Safe to act on", "Act on these", "can be relied on"}

// TestVerdictWordsNeverContradictCoverage is the same rule one layer out: the
// sentences rendered from the status. "Do not present what follows as an
// answer" and "Safe to act on" were both printed on one response.
func TestVerdictWordsNeverContradictCoverage(t *testing.T) {
	items := []Item{{Kind: "calls", Confidence: "exact"}, {Kind: "calls", Confidence: "exact"}}
	for _, level := range allLevels {
		for _, truncated := range []bool{false, true} {
			rel := &Reliability{Level: level, Lead: "a reason."}
			v := verdictFor(items, rel, truncated)
			said := v.NextStep + " " + strings.Join(v.Signals, " ")
			for _, w := range actionWords {
				if strings.Contains(said, w) && (level != ReliabilityGood || truncated) {
					t.Errorf("level=%q truncated=%v: the verdict says %q: %s", level, truncated, w, said)
				}
			}
			if (level == ReliabilityLow || level == ReliabilityNone) && !strings.Contains(said, "Do not present") {
				t.Errorf("level=%q: an unreliable verdict must say not to present the rows; got %s", level, said)
			}
		}
	}
}

// TestLeadOpensTheFirstLine: each level that is not good has a word, and the
// word is the first thing on the summary line.
func TestLeadOpensTheFirstLine(t *testing.T) {
	want := map[ReliabilityLevel]string{
		ReliabilityNone:       leadUnknown,
		ReliabilityLow:        leadUnreliable,
		ReliabilityUnverified: leadUnverified,
		ReliabilityIncomplete: leadIncomplete,
		ReliabilityGood:       "",
	}
	for level, word := range want {
		r := &Response{Summary: "3 caller(s).", Reliability: &Reliability{Level: level, Lead: "because of this."}}
		got := r.lead()
		if word == "" {
			if got != "" {
				t.Errorf("good coverage led with %q", got)
			}
			continue
		}
		if !strings.HasPrefix(got, word+" — because of this. ") {
			t.Errorf("level %q leads with %q, want it to open with %q and the reason", level, got, word)
		}
	}

	// Rows cut from a well-covered answer still make its first line unsafe to
	// read as the whole answer.
	cut := &Response{Summary: "50 caller(s).", Page: &Page{Offset: 0, Returned: 50, Total: 180, NextOffset: 50}}
	if got := cut.lead(); !strings.HasPrefix(got, leadIncomplete+" — rows 1-50 of 180 are shown. ") {
		t.Errorf("a cut answer leads with %q", got)
	}
}
