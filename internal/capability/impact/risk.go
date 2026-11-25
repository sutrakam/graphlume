package impact

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Risk is a score that shows its work.
//
// Spec §2.8 requires any score GraphLume emits to be explainable. That is not a
// presentation concern: a bare "Risk: HIGH" tells a developer to be careful
// without telling them what to be careful ABOUT, and the three things that
// drive it — public API, caller count, absent tests — call for three completely
// different responses. So Factors is not optional decoration attached to a
// number; the number is defined as the sum of the factors, and a factor with
// zero points is still listed because "this symbol is not exported" is an input
// the reader needs to see was considered.
//
// Certainty is deliberately separate from Level. They answer different
// questions: Level is "how much could break", Certainty is "how much should you
// trust that estimate". A high-risk score derived entirely from low-confidence
// edges and a high-risk score derived from compiler-proven ones are not the
// same finding, and collapsing them into one word would lose the distinction
// this whole package exists to preserve.
type Risk struct {
	// Level is low | moderate | high | unknown. Derived from Score by Formula,
	// except that a level which would be low is LevelUnknown when the callers
	// of the target could not be determined: see Undetermined.
	Level string `json:"level"`

	// Score is the sum of Factors, clamped to 0..100. It is computed from what
	// the index bound. When Level is LevelUnknown it is NOT a risk estimate and
	// must not be printed as one: it is the score of an evidence set known to
	// be missing the callers.
	Score int `json:"score"`

	// Undetermined is non-empty when the index cannot say who calls the
	// target: its language yields no references, or references spelled like it
	// exist and were not bound to it. It states which, with the counts.
	//
	// A level of moderate or high stays as scored when this is set, because
	// those are floors and a floor survives missing evidence. A level of low
	// does not: "few callers were found" and "few callers exist" are different
	// claims, and only the first is supported. That one becomes LevelUnknown.
	Undetermined string `json:"undetermined,omitempty"`

	// Partial is non-empty when the analysis is known to be a floor for a reason
	// other than Undetermined, and states it: the walk was cut at a bound, the
	// name occurs where no call is recorded, or callers at a low confidence were
	// left out and the count shown is zero. Level is then printed "or higher",
	// for every level and not only for moderate and high: a low that stopped at
	// a depth limit is as much a floor as a high is.
	//
	// Not in the JSON form, for the reason Plan.RiskPartial is not: the HTTP
	// API's field contract is frozen and nothing renders it. Report.Risk.Level
	// and the summaries carry it as "or higher".
	Partial string `json:"-"`

	// Factors are the inputs, in the order they were applied. Never empty.
	Factors []Factor `json:"factors"`

	// Formula states exactly how Level follows from Score, so the thresholds
	// are visible rather than buried in this file.
	Formula string `json:"formula"`

	// Certainty is how much the score itself should be trusted: high | medium |
	// low. Driven by the confidence mix of the impacted set and by whether the
	// traversal was truncated.
	Certainty string `json:"certainty"`

	// CertaintyReason gives the counts behind Certainty.
	CertaintyReason string `json:"certainty_reason"`

	// LowerBound is always true. The score is computed from a bounded traversal
	// over a partially resolved index, so it can only ever understate.
	LowerBound bool `json:"lower_bound"`
}

// Factor is one scored input with its justification.
type Factor struct {
	// Name is a stable machine-readable key (public_api, direct_callers, …).
	Name string `json:"name"`

	// Points is this factor's contribution to Score. May be negative — present
	// test coverage reduces risk — and may be zero, which is still reported.
	Points int `json:"points"`

	// Detail is the one-sentence explanation a human reads.
	Detail string `json:"detail"`

	// Evidence names the concrete fact the factor rests on.
	Evidence string `json:"evidence,omitempty"`
}

// Scoring weights. Stated as named constants rather than inline numbers so the
// model is auditable in one place — and so a future change to it is a visible
// diff rather than a tweak buried in an expression.
const (
	pointsExported       = 25 // public API: callers outside this repo are not in the index
	pointsPerDirect      = 5  // per direct caller …
	maxPointsDirect      = 25 // … capped, because 40 callers is not 8x worse than 5
	pointsIndirectDiv    = 5  // one point per 5 indirect symbols …
	maxPointsIndirect    = 20 // … capped for the same reason
	pointsNoTests        = 20 // nothing observed to cover this symbol
	pointsHasTests       = -10
	pointsTruncated      = 10 // the true blast radius is larger than measured
	pointsDynamicUnknown = 10 // reachable through dispatch we cannot see
	pointsDispatchReach  = 10 // implementations a call to the target can land on

	// pointsConventionTestsOnly sits between pointsNoTests and pointsHasTests
	// because that is exactly what convention-only coverage is: a test was found
	// by its NAME or its FILE, and no recorded edge says it exercises the
	// symbol. Scoring it as covered would let a filename discharge the risk.
	pointsConventionTestsOnly = 10

	riskModerateAt = 25 // Score >= this is moderate
	riskHighAt     = 60 // Score >= this is high
)

// Risk levels. LevelUnknown is a first-class outcome, not an error: it is what
// a score computed without the target's callers is called.
const (
	LevelLow      = "low"
	LevelModerate = "moderate"
	LevelHigh     = "high"
	LevelUnknown  = "unknown"
)

// LevelRank orders levels for "which of these is the riskiest". Unknown sits
// above low and below moderate: an undetermined symbol is a better candidate
// for a reader's attention than one known to be low, and a worse one than a
// symbol with evidence of real reach.
func LevelRank(level string) int {
	switch level {
	case LevelHigh:
		return 3
	case LevelModerate:
		return 2
	case LevelUnknown:
		return 1
	default:
		return 0
	}
}

// riskFormula is the one statement of how Level follows from Score. Shared by
// the report-level and the symbol-level scorer so the two cannot drift into
// meaning different things by the same word.
func riskFormula() string {
	return fmt.Sprintf(
		"score = sum(factors), clamped to 0..100; level = low below %d, moderate below %d, high at or above %d; "+
			"a level that would be low is reported as unknown when the callers could not be determined",
		riskModerateAt, riskHighAt, riskHighAt)
}

func finishRisk(risk *Risk) {
	for _, f := range risk.Factors {
		risk.Score += f.Points
	}
	if risk.Score < 0 {
		risk.Score = 0
	}
	if risk.Score > 100 {
		risk.Score = 100
	}
	switch {
	case risk.Score >= riskHighAt:
		risk.Level = LevelHigh
	case risk.Score >= riskModerateAt:
		risk.Level = LevelModerate
	default:
		risk.Level = LevelLow
	}
}

// markUndetermined records that the callers could not be determined and
// withdraws a low level, which is the one level that claim would be read as
// reassurance from.
func markUndetermined(risk *Risk, reason string) {
	risk.Undetermined = reason
	if risk.Level == LevelLow {
		risk.Level = LevelUnknown
	}
}

// exportFactor is the public-API input, or the admission that the index does
// not know it.
//
// The index's exported flag is trusted in the languages listed by
// capability.ExportedKnown and nowhere else. Outside them, stating "unexported,
// so its blast radius is bounded by this repository" would turn a guess about
// the first letter of a name into a claim that no external caller exists.
func exportFactor(subject, fqn, kind, language, filePath string, exported bool) Factor {
	if !capability.ExportedKnown(language, filePath) {
		lang := capability.DisplayLanguage(language)
		if lang == "" {
			lang = "this language"
		}
		return Factor{
			Name: "public_api", Points: 0,
			Detail: fmt.Sprintf("Whether the %s is exported is UNKNOWN. The index does not read the visibility "+
				"rules of %s, so it cannot say whether callers outside this repository exist. No points are "+
				"added, and none should be read as subtracted.", subject, lang),
			Evidence: fmt.Sprintf("the exported flag is not trusted for %s", lang),
		}
	}
	if exported {
		return Factor{
			Name: "public_api", Points: pointsExported,
			Detail: fmt.Sprintf("The %s is exported. Callers outside this repository are not indexed, "+
				"so the reported blast radius excludes them entirely.", subject),
			Evidence: fmt.Sprintf("%s is exported (%s)", fqn, kind),
		}
	}
	return Factor{
		Name: "public_api", Points: 0,
		Detail:   fmt.Sprintf("The %s is unexported, so its blast radius is bounded by this repository.", subject),
		Evidence: fmt.Sprintf("%s is not exported", fqn),
	}
}

// scoreRisk computes the risk block from a finished report.
//
// It is a pure function of the report on purpose: everything it uses is already
// visible to the reader, so there is no hidden input that could make the score
// unreproducible from what was printed.
func scoreRisk(r Report) Risk {
	risk := Risk{LowerBound: true, Formula: riskFormula()}

	// What the index could not determine about the target's callers, if
	// anything. Read first because two factors below change their wording on
	// it, and the level changes on it at the end.
	undetermined, isUndetermined := r.Binding.Undetermined()

	// 1. Public API.
	risk.Factors = append(risk.Factors, exportFactor("target", r.Target.FQN, r.Target.Kind,
		r.Target.Language, r.Target.FilePath, r.Target.Exported))

	// 2. Direct callers.
	direct := len(r.Direct)
	dp := direct * pointsPerDirect
	if dp > maxPointsDirect {
		dp = maxPointsDirect
	}
	risk.Factors = append(risk.Factors, Factor{
		Name: "direct_callers", Points: dp,
		Detail: fmt.Sprintf("%d symbol(s) reference the target directly (depth 1). "+
			"Capped at %d points: caller count grows faster than risk does.", direct, maxPointsDirect),
		Evidence: fmt.Sprintf("%d direct impacted symbols in this report", direct),
	})

	// 3. Indirect reach.
	indirect := len(r.Indirect)
	ip := indirect / pointsIndirectDiv
	if ip > maxPointsIndirect {
		ip = maxPointsIndirect
	}
	risk.Factors = append(risk.Factors, Factor{
		Name: "indirect_reach", Points: ip,
		Detail: fmt.Sprintf("%d symbol(s) are reachable at depth 2 or more. "+
			"Weighted far below direct callers because a long path is a weaker claim.", indirect),
		Evidence: fmt.Sprintf("%d indirect impacted symbols in this report", indirect),
	})

	// 4. Observed test coverage. This is coverage OBSERVED IN THE GRAPH — a
	// test symbol that reaches the target — not a coverage report. It is
	// therefore evidence that something exercises the symbol, never evidence
	// that the symbol is well tested.
	tests, example := countTests(r)
	if tests > 0 {
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: pointsHasTests,
			Detail: fmt.Sprintf("%d of the impacted symbols are test code, so a change here is at least "+
				"partly guarded. This is reachability, not a coverage measurement.", tests),
			Evidence: "e.g. " + example,
		})
	} else if r.ValueUse.Any() {
		// Not "no test reaches it" either. A test that reaches the target through
		// a function passed as a value leaves no edge, and the name occurs where
		// such a use would be.
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: 0,
			Detail: "Whether any test reaches this target is NOT KNOWN: the name of the target, or of a symbol " +
				"on its path, occurs in code that is not recorded as a call, and a function passed as a value " +
				"is reached by its callers without an edge. No points are added for missing tests, because " +
				"none were shown to be missing.",
			Evidence: "0 impacted symbols classified as tests, and the name occurs where no call is recorded",
		})
	} else if isUndetermined {
		// Not "no test reaches it". A test that calls the target through a
		// reference the index could not bind is a test this walk cannot see,
		// and the same gap that hides the callers hides the tests among them.
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: 0,
			Detail: "Whether any test reaches this target could NOT be determined: the callers themselves " +
				"could not be (see unbound_references). No points are added for missing tests, because " +
				"none were shown to be missing.",
			Evidence: "0 impacted symbols classified as tests, among callers that could not be determined",
		})
	} else {
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: pointsNoTests,
			Detail: "No test symbol was found reaching this target through the relationships this index " +
				"bound. Either it is untested, or its tests reach it in a way the index does not record " +
				"(an unresolved call, interface or trait dispatch, table-driven or generated tests, a " +
				"separate test repository).",
			Evidence: "0 impacted symbols classified as tests",
		})
	}

	// 5. Truncation.
	if r.Truncated {
		risk.Factors = append(risk.Factors, Factor{
			Name: "truncated_traversal", Points: pointsTruncated,
			Detail: "The traversal hit a bound, so the counts above are floors. " +
				"The score is raised because an unmeasured region is a risk, not an absence of one.",
			Evidence: r.TruncatedReason,
		})
	}

	// 6. Dynamic dispatch.
	if u, ok := findUnknown(r, UnknownDynamicDispatch); ok {
		risk.Factors = append(risk.Factors, Factor{
			Name: "dynamic_dispatch", Points: pointsDynamicUnknown,
			Detail: "The target participates in a subtype relationship, so calls can reach it through a " +
				"supertype without any edge recording that they do.",
			Evidence: u.Detail,
		})
	}

	// 7. Dispatch expansion. Scored separately from factor 6: that one fires
	// when the TARGET implements something and the callers of the supertype are
	// unknown, this one when something implements the TARGET and those
	// implementations were expanded. A symbol can have both, and they are
	// different reasons to be careful.
	if n := len(r.Dispatch); n > 0 {
		risk.Factors = append(risk.Factors, Factor{
			Name: "dispatch_reach", Points: pointsDispatchReach,
			Detail: fmt.Sprintf("%d symbol(s) are reachable only by dispatching through %d recorded "+
				"implementation(s) of the target. A change to the target's contract lands on all of them, "+
				"and no call site records which one it takes.", n, len(r.DispatchThrough)),
			Evidence: dispatchEvidence(r),
		})
	}

	// 8. Callers that could not be determined. Listed with zero points like
	// every considered input, and acted on below: it does not move the score,
	// it withdraws the claim a low score would make.
	if isUndetermined {
		risk.Factors = append(risk.Factors, Factor{
			Name: "unbound_references", Points: 0,
			Detail: "The callers of this target could not be determined (the NOT VISIBLE note " +
				"unbound_references_to_name says why). The score is computed without them, which is why " +
				"a level that would be low is reported as unknown.",
			Evidence: fmt.Sprintf("%d reference(s) spelled %q; %d bound to this definition",
				r.Binding.References, r.Target.Name, r.Binding.BoundHere),
		})
	}

	finishRisk(&risk)
	if isUndetermined {
		markUndetermined(&risk, undetermined)
	}
	risk.Partial = partialReason(r)
	risk.Certainty, risk.CertaintyReason = certainty(r)
	return risk
}

// depthLimit finds the limit in a truncation reason of the form graph writes:
// "depth limit 3 reached, and 4 node(s) at the limit were not expanded".
var depthLimit = regexp.MustCompile(`depth limit (\d+)`)

// partialReason says why the level is a floor, apart from callers that could
// not be determined. Empty when it is not.
func partialReason(r Report) string {
	var parts []string
	if r.Truncated {
		if m := depthLimit.FindStringSubmatch(r.TruncatedReason); m != nil {
			parts = append(parts, "the analysis was cut at depth "+m[1])
		} else if r.MaxDepth > 0 {
			parts = append(parts, fmt.Sprintf("the analysis was cut at a bound within depth %d", r.MaxDepth))
		} else {
			parts = append(parts, "the analysis was cut at a bound")
		}
	}
	if r.ValueUse.Any() {
		parts = append(parts, "the name occurs where no call is recorded, so a function passed as a value "+
			"may reach the target")
	}
	if r.LowConfidenceHiddenDirect > 0 && len(r.Direct) == 0 {
		parts = append(parts, fmt.Sprintf("%d caller(s) at low confidence were excluded", r.LowConfidenceHiddenDirect))
	}
	return strings.Join(parts, "; ")
}

func dispatchEvidence(r Report) string {
	if len(r.DispatchThrough) == 0 {
		return ""
	}
	names := make([]string, 0, 3)
	for i, s := range r.DispatchThrough {
		if i == 3 {
			names = append(names, fmt.Sprintf("… and %d more", len(r.DispatchThrough)-3))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s)", s.Name, s.FilePath))
	}
	return "implementations: " + strings.Join(names, ", ")
}

// Reach is how a symbol came to be in a blast radius. It is the same
// three-way distinction Report makes between Direct, Indirect and Dispatch,
// named so a per-symbol score can carry it.
type Reach string

const (
	ReachDirect   Reach = "direct"
	ReachIndirect Reach = "indirect"
	ReachDispatch Reach = "dispatch"
)

// Coverage is what is known about tests for one reached symbol.
type Coverage string

const (
	// CoverageProven: a recorded edge chain runs from a test symbol to this one.
	CoverageProven Coverage = "proven_by_graph"

	// CoverageConventionOnly: a test was found by naming convention or by
	// sibling file, and no edge says it exercises this symbol.
	CoverageConventionOnly Coverage = "convention_only"

	// CoverageNone: no signal matched. Not proof of absence — see the caveats on
	// any report carrying this.
	CoverageNone Coverage = "none"

	// CoverageNotExamined: the symbol is in the blast radius but coverage was
	// not looked up for it, because the examination budget ran out.
	CoverageNotExamined Coverage = "not_examined"
)

// ReachedSymbol is one symbol in a blast radius, with the facts the risk model
// scores. It is the input to ScoreReachedSymbol.
type ReachedSymbol struct {
	Symbol   capability.Symbol
	Reach    Reach
	Depth    int
	Coverage Coverage

	// TestCount is how many tests were discovered for the symbol, whatever the
	// signal. Reported in the factor's evidence, never as points on its own.
	TestCount int

	// Confidence is the path minimum that reached the symbol.
	Confidence model.Confidence
}

// ScoreReachedSymbol scores ONE symbol in a blast radius, using the same
// factors, weights, thresholds and Formula as the report-level score.
//
// It exists so a test-gap report can rank uncovered symbols without inventing a
// second notion of risk. The two scorers necessarily read different inputs — a
// report knows caller counts, a single reached symbol knows its distance and
// its coverage — but a "high" from one means the same arithmetic as a "high"
// from the other, because both sum the constants above and both call
// finishRisk.
//
// Like every score here it shows its work: Factors is never empty, and a factor
// worth zero points is still listed so the reader can see it was considered.
func ScoreReachedSymbol(in ReachedSymbol) Risk {
	risk := Risk{LowerBound: true, Formula: riskFormula()}

	risk.Factors = append(risk.Factors, exportFactor("symbol", in.Symbol.FQN, in.Symbol.Kind,
		"", in.Symbol.FilePath, in.Symbol.Exported))

	// Proximity. Depth 1 is worth the same as the report-level cap on direct
	// callers, and each further hop divides it, because a longer path is a
	// weaker claim that this symbol is affected at all.
	depth := in.Depth
	if depth < 1 {
		depth = 1
	}
	risk.Factors = append(risk.Factors, Factor{
		Name: "proximity", Points: maxPointsDirect / depth,
		Detail: fmt.Sprintf("Reached at depth %d (%s). Closer symbols are more likely to be genuinely "+
			"affected, so distance divides the points rather than adding them.", depth, in.Reach),
		Evidence: fmt.Sprintf("%s is %d hop(s) from the changed symbol", in.Symbol.Name, depth),
	})

	if in.Reach == ReachDispatch {
		risk.Factors = append(risk.Factors, Factor{
			Name: "dispatch_reach", Points: pointsDispatchReach,
			Detail: "Reached by dispatching through a declared implementation rather than by a recorded " +
				"call. Whether a given call site takes this path cannot be determined statically, which " +
				"is a reason for caution and not a reason to discount it.",
			Evidence: fmt.Sprintf("%s was reached across an IMPLEMENTS/EXTENDS edge", in.Symbol.Name),
		})
	}

	switch in.Coverage {
	case CoverageProven:
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: pointsHasTests,
			Detail: "A recorded edge chain reaches this symbol from a test, so a change to it is at least " +
				"partly guarded. This is reachability, not a coverage measurement.",
			Evidence: fmt.Sprintf("%d test(s) discovered, at least one proven by the graph", in.TestCount),
		})
	case CoverageConventionOnly:
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: pointsConventionTestsOnly,
			Detail: "The only tests found for this symbol were matched by naming convention or by sibling " +
				"file. Nothing recorded says they exercise it, so they reduce the risk less than a proven " +
				"edge chain would.",
			Evidence: fmt.Sprintf("%d test(s) discovered, none proven by the graph", in.TestCount),
		})
	case CoverageNotExamined:
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: 0,
			Detail: "Coverage was not looked up for this symbol — the examination budget ran out before " +
				"reaching it. Scored as neither covered nor uncovered, because it is neither known.",
			Evidence: "not examined",
		})
	default:
		risk.Factors = append(risk.Factors, Factor{
			Name: "observed_tests", Points: pointsNoTests,
			Detail: "No test was found for this symbol by call graph, naming convention or sibling file. " +
				"Either it is untested, or its tests are invisible to static analysis.",
			Evidence: "0 tests discovered",
		})
	}

	finishRisk(&risk)
	risk.Certainty, risk.CertaintyReason = symbolCertainty(in)
	return risk
}

// symbolCertainty describes how much this one symbol's score should be trusted.
//
// Kept separate from the score for the same reason the report-level one is:
// "this is probably affected and definitely untested" and "this might be
// affected and is definitely untested" are different findings.
func symbolCertainty(in ReachedSymbol) (string, string) {
	base := fmt.Sprintf("reached at depth %d along a path whose weakest edge is %s",
		in.Depth, orUnrecorded(string(in.Confidence)))
	switch {
	case in.Coverage == CoverageNotExamined:
		return "low", base + ", and its coverage was never examined, so the score rests on reachability alone."
	case in.Reach == ReachDispatch:
		return "low", base + ". It was reached by dispatch, so it is possibly affected rather than " +
			"provably affected."
	case in.Confidence.AtLeast(model.ConfidenceHigh) && in.Depth == 1:
		return "high", base + ", which is the strongest claim this index makes."
	case in.Confidence.AtLeast(model.ConfidenceHigh):
		return "medium", base + "; the individual hops are strong but the chain is not depth 1."
	default:
		return "low", base + ", so the reachability itself is uncertain."
	}
}

func orUnrecorded(s string) string {
	if s == "" {
		return "unrecorded"
	}
	return s
}

// certainty describes how much the score should be trusted, from the confidence
// mix of the evidence it was computed from.
//
// Separate from the score because they can move in opposite directions: a
// symbol with 200 low-confidence callers scores high and deserves little trust,
// while one with 3 compiler-proven callers scores low and deserves a lot.
func certainty(r Report) (string, string) {
	level, reason := certaintyOfMix(r)
	if !r.ValueUse.Any() {
		return level, reason
	}
	// A function passed as a value is reached without an edge, so the set above
	// is short by an amount nothing here can size. High is a claim that the set
	// is nearly the whole of it.
	if level == "high" {
		level = "medium"
	}
	return level, reason + " Also: " + r.ValueUse.Lead() + ". A function passed as a value is invisible to the " +
		"call graph, so the callers listed may be a fraction of the real ones, and certainty is not rated high."
}

func certaintyOfMix(r Report) (string, string) {
	var strong, weak int
	for _, list := range [][]Impacted{r.Direct, r.Indirect} {
		for _, imp := range list {
			if imp.Confidence.AtLeast(model.ConfidenceHigh) {
				strong++
			} else {
				weak++
			}
		}
	}
	total := strong + weak

	switch {
	case total == 0 && r.Truncated:
		return "low", "No impacted symbols were found, but the traversal was truncated — absence of results " +
			"here is not evidence of absence of impact."
	case total == 0:
		if _, ok := r.Binding.Undetermined(); ok {
			return "low", "No impacted symbols were found, and that is not evidence of none: the callers " +
				"of this target could not be determined."
		}
		rate, scope := bindingRateOf(r)
		return "low", fmt.Sprintf(
			"No impacted symbols were found. With %.1f%% of %s bindable references "+
				"bound, an empty result is as likely to mean 'not indexed' as 'not used'.",
			100*rate, scope)
	}

	rate, scope := bindingRateOf(r)
	reason := fmt.Sprintf(
		"%d of %d impacted symbols were reached along a path whose weakest edge is high or exact; "+
			"%d were reached only through medium- or low-confidence edges. Reference resolution for %s: "+
			"%.1f%% of the references that name something this index defines.",
		strong, total, weak, strings.TrimSuffix(scope, "'s"), 100*rate)

	// A TRUNCATED WALK CAPS CERTAINTY; IT DOES NOT ERASE IT. Stopping at a
	// bound says the set is incomplete, so certainty is never high. It says
	// nothing against the rows that were found: rating 56 compiler-proven
	// rows "low" because a depth limit was reached read as a contradiction
	// next to the sentence that listed them, and told the agent to distrust
	// the strongest evidence the index has. Truncation is stated first, so
	// the level and its reason agree.
	if r.Truncated {
		reason = "The traversal stopped at a bound, so the impacted set is incomplete and certainty " +
			"cannot be high. Of what it explored: " + reason
		if weak*2 > total {
			return "low", reason
		}
		return "medium", reason
	}

	switch {
	case weak*2 > total:
		return "low", reason
	// THE THRESHOLD NOW MEASURES WHAT IT ALWAYS MEANT. It gates "high" on the
	// index having understood enough of the repository to trust an empty or
	// narrow impact result. Read over EVERY reference it could not do that: a
	// service that calls a lot of third-party code could never reach 0.8
	// however perfectly it bound its own, so the gate was partly a measure of
	// dependency surface. Over bindable references it is the intended test, and
	// the constant is unchanged so the intent is visible in the diff.
	case strong == total && rate >= 0.8:
		return "high", reason
	default:
		return "medium", reason
	}
}

// bindingRateOf is the binding rate the report's certainty is judged against,
// and the words naming what it covers.
//
// The target's own language when the index could say, because that is the
// population its callers are drawn from: a Kotlin symbol in a repository with a
// few Java samples was being described by the Java files' rate. The run-wide
// rate otherwise.
func bindingRateOf(r Report) (rate float64, scope string) {
	if r.Binding.Known && r.Binding.LanguageStats.Bindable() > 0 {
		return r.Binding.LanguageStats.BindingRate(),
			capability.DisplayLanguage(r.Binding.Language) + "'s"
	}
	return r.Completeness.BindingRate, "this run's"
}

func countTests(r Report) (int, string) {
	var n int
	var example string
	for _, list := range [][]Impacted{r.Direct, r.Indirect} {
		for _, imp := range list {
			if !imp.IsTest {
				continue
			}
			n++
			if example == "" {
				example = fmt.Sprintf("%s (%s)", imp.Name, imp.FilePath)
			}
		}
	}
	return n, example
}

func findUnknown(r Report, kind UnknownKind) (Unknown, bool) {
	for _, u := range r.Unknown {
		if u.Kind == kind {
			return u, true
		}
	}
	return Unknown{}, false
}
