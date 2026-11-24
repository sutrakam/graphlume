package mcp

// How far an answer can be trusted, computed in one place.
//
// THE DEFECT THIS FILE PREVENTS. An answer could carry two independent
// judgements of itself. The coverage gate looked at how much of the index was
// bound and could print "COVERAGE TOO LOW ... Do not present what follows as
// an answer". The verdict looked at the confidence tier of each row and could
// print "verdict: strong ... Act on these". Neither read the other, so one
// answer said both, and an agent follows the shorter, more confident sentence.
//
// There is one input, Reliability, and one function, OverallSignal, that
// turns it and the rows into the single word the answer leads with. The
// verdict is graded from it (verdict.go), the first line of the answer opens
// with it (Response.lead), and the coverage notes are rendered from the same
// value, so the three cannot disagree.

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/registry"
)

// ReliabilityLevel is how far the index supports the answer it is attached to.
type ReliabilityLevel string

const (
	// ReliabilityGood: nothing the index knows of undermines the answer.
	ReliabilityGood ReliabilityLevel = "good"

	// ReliabilityIncomplete: the rows are sound, and the index itself holds
	// evidence that some are missing: references spelled like the symbol that
	// were never bound to it, or a kind of relationship the language declares
	// in a way the index does not read.
	ReliabilityIncomplete ReliabilityLevel = "incomplete"

	// ReliabilityUnverified: the index holds evidence that some ROWS are bound
	// to the wrong definition. A call is recorded as made on something that
	// cannot reach the definition (a Go function called through a value), so
	// the binding that produced the row is wrong.
	ReliabilityUnverified ReliabilityLevel = "unverified"

	// ReliabilityLow: most of what could have been bound in the symbol's
	// language was not. The answer is a minority report.
	ReliabilityLow ReliabilityLevel = "low"

	// ReliabilityNone: nothing can be concluded. The symbol's language yields
	// no references at all, or none has been resolved.
	ReliabilityNone ReliabilityLevel = "none"
)

// rank orders levels, best first, so two can be combined by taking the worse.
func (l ReliabilityLevel) rank() int {
	switch l {
	case ReliabilityGood:
		return 4
	case ReliabilityIncomplete:
		return 3
	case ReliabilityUnverified:
		return 2
	case ReliabilityLow:
		return 1
	default:
		return 0
	}
}

// Reliability is the index's own account of how far to trust one answer.
//
// Set by every tool whose answer is a statement about relationships, through
// coverage.reliability, and read by Server.reply. A response without one makes
// no relationship claim (a search hit, a file outline) and is not graded
// against coverage.
type Reliability struct {
	Level ReliabilityLevel `json:"level"`

	// Lead is the clause the first line of the answer opens with when Level is
	// not good. It is a complete sentence and must be safe to read alone.
	Lead string `json:"lead,omitempty"`

	// Reasons are the same finding at full length, with every count.
	Reasons []string `json:"reasons,omitempty"`
}

// Lead words. Each is the first word of an answer's first line.
const (
	leadUnknown    = "UNKNOWN"
	leadUnreliable = "UNRELIABLE"
	leadIncomplete = "INCOMPLETE"
	leadUnverified = "UNVERIFIED"
	leadAmbiguous  = "AMBIGUOUS"
)

// ---------------------------------------------------------------- coverage

// coverageFloor is the binding rate below which a relationship answer stops
// being an answer.
//
// It is capability.CoverageFloor, not a number of its own. This gate must not
// carry its own 0.5 against ResolutionStats.Rate while coverage
// used 0.6 against BindingRate, so the two tools would report different coverage
// for one index in one session — see capability.CoverageFloor for the whole
// argument and for why BindingRate is the published rate.
//
// The number is a judgement rather than a measurement, which is exactly why
// the response states the RATE and its denominator as well as the verdict: a
// reader who would draw the line elsewhere can see the input and decide for
// himself.
const coverageFloor = capability.CoverageFloor

// question names what an answer is about, for the sentence that says it could
// not be determined.
type question string

const (
	aboutCallers   question = "callers"
	aboutCallees   question = "what it calls"
	aboutHierarchy question = "its subtypes or supertypes"
	aboutRoutes    question = "routes through it"
	aboutTests     question = "the tests that reach it"
	aboutContext   question = "what the task touches"
)

// coverage is what the server knows about how far to trust the graph for a
// relationship question about one symbol.
//
// It is judged at three widths, narrowest first, because each wider one was
// caught describing the wrong thing:
//
//   - THE NAME. References spelled like the symbol and what became of each
//     (Binding). A repository can bind most of its references and none of the
//     ones naming this method.
//   - THE LANGUAGE. Resolution over the files of the symbol's own language
//     (Lang). A Kotlin symbol in a repository with a few Java samples was being
//     described by the Java files' rate, and told to run a command that had
//     already run.
//   - THE RUN. The whole index (Run), used only when the language is unknown.
type coverage struct {
	// Language is the symbol's language as a reader spells it, named because
	// coverage is uneven across languages. Empty when unknown.
	Language string

	// Rate, Bindable, Unresolved, Ambiguous, Pending and Total are the figures
	// the verdict was reached on: the symbol's language when HasLang, the run
	// otherwise. See capability.CoverageFloor for why this rate.
	Rate       float64
	Bindable   int64
	Unresolved int64
	Ambiguous  int64
	Pending    int64
	Total      int64

	// HasLang is true when the figures above describe one language.
	HasLang bool
	// Lang is that language's share of the index. Zero when !HasLang.
	Lang registry.LanguageBinding

	// Binding is the name-level evidence. Known is false for an answer that is
	// not about one symbol's callers.
	Binding capability.TargetBinding

	// About is what the answer claims, for the sentence saying it cannot.
	About question

	// Structural is non-empty when the symbol is a type whose language lets
	// other types satisfy it without saying so, and holds the sentence.
	Structural string

	// NoHierarchy is true when the answer is about type relationships and the
	// index records none at all for the symbol's language.
	NoHierarchy bool

	// Misbound is non-empty when some call sites bound to the symbol were made
	// on something that cannot reach it, and holds the sentence. See
	// boundSiteSet.conflict.
	Misbound string

	// Elsewhere is the text check for a function passed as a value: the name of
	// the symbol, or of one on the path to it, occurs in code the index records
	// no reference for. Zero when the check was not made or found nothing.
	Elsewhere capability.ValueUse

	// Unlinked is non-empty when the index holds subtypes of the type that are
	// not in the list: supertype references that became no edge, and lines that
	// read as a declaration naming it and are recorded as nothing. It holds the
	// sentence, and UnlinkedDetail the same finding at full length.
	Unlinked       string
	UnlinkedDetail string

	// FloorEmpty is non-empty when the number of callers the answer shows is
	// zero only because the confidence floor kept some out, and holds the
	// sentence. A bare zero there reads as "nothing calls it".
	FloorEmpty string

	// Known is false when the index holds no resolution outcomes to judge — no
	// references at all, or none resolved yet. Distinct from a LOW rate: one
	// says GraphLume looked and found little, the other says it never looked.
	Known bool

	// Extracts is false when the symbol's language yields no reference and no
	// relationship in this index: its calls are not read at all.
	Extracts bool

	// Reliable is true when the rate clears the floor. It says nothing about
	// the name-level evidence; see level.
	Reliable bool
}

// coverageOf judges run-wide stats for a file path. It is the constructor for
// an answer with no symbol behind it, and for a symbol whose language the
// index could not name.
func coverageOf(rs registry.ResolutionStats, filePath string) coverage {
	c := coverage{Language: languageOf(filePath), About: aboutCallers, Extracts: true}
	c.setStats(rs)
	return c
}

func (c *coverage) setStats(rs registry.ResolutionStats) {
	c.Rate = rs.BindingRate()
	c.Bindable = rs.Bindable()
	c.Unresolved = rs.Unresolved
	c.Ambiguous = rs.Ambiguous
	c.Pending = rs.Pending
	c.Total = rs.Total
	c.Known = rs.Total > 0 && rs.Pending < rs.Total
	// A language whose every reference matched nothing here has bound nothing
	// and has nothing bindable; BindingRate reports 0 for it and that is the
	// truthful reading, so it is unreliable rather than vacuously perfect.
	c.Reliable = c.Known && (c.Rate >= coverageFloor)
}

// coverageFor judges coverage for an answer about the definition named by
// language (the index's name for it) and filePath.
//
// A read that fails is not swallowed: the answer then says coverage could not
// be read, which is a reason to distrust it, where staying silent would make
// a failed read look like a well-resolved index.
func (s *Server) coverageFor(ctx context.Context, runID int64, language, filePath string,
	binding capability.TargetBinding, about question) coverage {
	c := coverage{Language: capability.DisplayLanguage(language), About: about, Binding: binding, Extracts: true}
	if c.Language == "" {
		c.Language = languageOf(filePath)
	}

	run, err := s.capDeps.ResolutionStats(ctx, runID)
	if err != nil {
		// Zero stats read as "no reference data", which is the safe verdict.
		return c
	}
	c.setStats(run)

	langs, err := s.capDeps.LanguageBindings(ctx, runID)
	if err != nil || langs == nil || strings.TrimSpace(language) == "" {
		return c
	}
	lb := capability.LanguageBindingFor(langs, language)
	if lb.Files == 0 {
		return c
	}
	c.HasLang, c.Lang = true, lb
	c.setStats(lb.ResolutionStats)
	c.Extracts = lb.Definitions == 0 || lb.ExtractsReferences()
	if !c.Extracts {
		c.Known, c.Reliable = false, false
	} else if lb.Total == 0 {
		// Relationships exist with no reference rows behind them (a compiler
		// index supplied them). There is no heuristic binding to distrust.
		c.Known, c.Reliable = true, true
	}
	return c
}

// requireHierarchy makes the judgement about TYPE relationships rather than
// calls.
//
// A language can bind its calls well and record no inheritance at all: the
// index holds no extends or implements relationship for any Ruby, C#, Rust,
// C++ or PHP type. For such a language "0 implementing it" is a statement
// about what the index reads, and it is reported as unknown, with the count
// that shows it.
func (c *coverage) requireHierarchy() {
	if !c.HasLang || c.Lang.Hierarchy > 0 || c.Lang.Definitions == 0 {
		return
	}
	c.NoHierarchy = true
}

// scope names the code the rate describes, for the caveat sentence.
func (c coverage) scope() string {
	if c.Language == "" {
		return "this repository"
	}
	return c.Language
}

// nameAccounted is true when the index accounts for every reference to this
// symbol's NAME: at least one exists, and each is bound (to this definition or
// to another of the same name), none ambiguous, unresolved or pending, none
// bound from outside an extracted function, and the text check found no place
// the name occurs that no reference explains.
//
// It is the narrowest of the three widths and, where it holds, the one that
// decides. koa's isStream has five callers, exactly right, and every reference
// spelled `isStream` is bound to it; the answer opened "UNRELIABLE - only 59%
// of the JavaScript references that name something this index defines are
// bound" because the REST of the language resolves badly, and told the reader
// not to present five correct callers as an answer. A symbol whose own
// references are all accounted for is determined however badly its neighbours
// resolved (TargetBinding.Undetermined says the same).
func (c coverage) nameAccounted() bool {
	return c.Binding.Known && c.Binding.References > 0 && c.Binding.Unlisted() == 0 && !c.Elsewhere.Any()
}

// rateIsOverridden is true when the language-wide rate is below the floor and
// the name-level accounting is what the grade rests on instead.
func (c coverage) rateIsOverridden() bool {
	return c.Extracts && c.Known && !c.NoHierarchy && !c.Reliable && c.nameAccounted()
}

// level is the one judgement every surface reads.
func (c coverage) level() ReliabilityLevel {
	switch {
	case !c.Extracts, !c.Known, c.NoHierarchy:
		return ReliabilityNone
	case !c.Reliable && !c.nameAccounted():
		return ReliabilityLow
	case c.Misbound != "":
		return ReliabilityUnverified
	case c.Binding.Known && c.Binding.Unlisted() > 0, c.Structural != "", c.Elsewhere.Any(), c.FloorEmpty != "", c.Unlinked != "":
		return ReliabilityIncomplete
	default:
		return ReliabilityGood
	}
}

// notExtracted is the clause saying the symbol's language yields nothing to
// answer from. For a question about calls it is the sentence an agent most
// needs verbatim; for a question about types it must not say "calls", because
// a reader would conclude that types are a separate, working, matter.
//
// It is said of the LANGUAGE only when the index holds enough of it to say so
// (capability.MinDefinitionsToGeneralise). A handful of definitions with no
// reference among them is reported as exactly that.
func (c coverage) notExtracted() string {
	if !capability.GeneralisesToLanguage(c.Lang.Definitions) {
		return fmt.Sprintf("found no reference in any %s file of this index", c.scope())
	}
	if c.About == aboutHierarchy {
		return fmt.Sprintf("does not extract calls or type relationships from %s", c.scope())
	}
	return fmt.Sprintf("does not extract calls from %s", c.scope())
}

// refWord is "Kotlin references" or "references", so a sentence about one
// language says so and a sentence about the run does not pretend to.
func (c coverage) refWord() string {
	if c.HasLang && c.Language != "" {
		return c.Language + " references"
	}
	return "references"
}

// resolveAdvice says what, if anything, would raise a low rate.
//
// "Run 'graphlume resolve'" is not printed unconditionally, including on
// an index where resolve had just finished, which would send an agent to run a
// command whose output cannot change. Resolve binds PENDING references and
// nothing else, so it is named only when some are pending.
func (c coverage) resolveAdvice() string {
	if c.Pending > 0 {
		return fmt.Sprintf(" %d of them have not been through resolution: run 'graphlume resolve'.", c.Pending)
	}
	return " 'graphlume resolve' has already run on every one of them, so running it again will " +
		"not change this; where an indexer exists for the language, 'graphlume scip import' can."
}

// lead is the clause the answer's first line opens with. Empty when good.
func (c coverage) lead() string {
	switch c.level() {
	case ReliabilityNone:
		switch {
		case c.NoHierarchy && c.Extracts:
			return fmt.Sprintf("this index records no extends or implements relationship for any %s type; "+
				"nothing about %s can be concluded.", c.scope(), c.About)
		case !c.Extracts:
			return fmt.Sprintf("GraphLume %s; nothing about %s can be concluded.", c.notExtracted(), c.About)
		case c.Total == 0:
			return fmt.Sprintf("this index recorded no references at all; nothing about %s can be concluded.", c.About)
		default:
			return fmt.Sprintf("none of the %d %s has been resolved, so nothing about %s can be concluded; "+
				"run 'graphlume resolve'.", c.Total, c.refWord(), c.About)
		}
	case ReliabilityLow:
		if c.Bindable == 0 {
			return fmt.Sprintf("none of the %d %s matched a definition in this index.", c.Total, c.refWord())
		}
		return fmt.Sprintf("only %.0f%% of the %s that name something this index defines are bound.",
			100*c.Rate, c.refWord())
	case ReliabilityUnverified:
		return c.Misbound
	case ReliabilityIncomplete:
		// Every reason the index holds, strongest first. They were one
		// either-or before, which is how a function passed as a value could not
		// be said beside an unbound name.
		var parts []string
		if c.Binding.Known && c.Binding.Unlisted() > 0 {
			parts = append(parts, fmt.Sprintf("%d of the %d references spelled %q are not among the rows below.",
				c.Binding.Unlisted(), c.Binding.References, bindingName(c.Binding)))
		} else if c.Structural != "" {
			parts = append(parts, structuralLead(c.Language))
		}
		if c.Unlinked != "" {
			parts = append(parts, c.Unlinked)
		}
		if c.FloorEmpty != "" {
			parts = append(parts, c.FloorEmpty)
		}
		if c.Elsewhere.Any() {
			parts = append(parts, c.Elsewhere.Lead()+".")
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// structuralLead is the one-sentence form of structuralTyping for a first
// line; the full sentence, with the remedy, is in the caveat.
func structuralLead(language string) string {
	return fmt.Sprintf("%s types can satisfy this interface without declaring it, and those are not listed.", language)
}

// bindingName is the spelling the name-level counts are about. It travels on
// the coverage rather than the binding because the capability layer has no
// reason to carry a display name.
func bindingName(b capability.TargetBinding) string { return b.Name }

// rateNote states the language-wide binding rate when it was set aside because
// every reference to this symbol's name is accounted for. It is a note and
// never a lead: it describes the language, and the answer does not rest on it.
//
// It avoids the two phrases a doubtful answer is held to (see
// TestNoAnswerSaysBothDoNotUseAndActOnThese): this answer does not doubt itself.
func (c coverage) rateNote() string {
	return fmt.Sprintf("Language-wide, %.0f%% of the %d %s that name something this index defines are bound "+
		"(%d ambiguous; a further %d matched no definition here at all, out of %d in total). That rate does not "+
		"apply to this answer: all %d reference(s) spelled %q are bound, none ambiguous or unresolved. It is what to "+
		"expect of a symbol whose own references are not accounted for.",
		100*c.Rate, c.Bindable, c.refWord(), c.Ambiguous, c.Unresolved, c.Total, c.Binding.References, bindingName(c.Binding))
}

// caveat is what a doubtful answer must carry, ahead of its own results.
//
// Empty when coverage is good — silence is the reward for a well-resolved
// index, and paying tokens to say "coverage is fine" on every call would be
// the same waste this server exists to remove.
func (c coverage) caveat(subject string) []string {
	const doNot = " Do not present what follows as an answer to the question asked."

	switch c.level() {
	case ReliabilityGood:
		if c.rateIsOverridden() {
			return []string{c.rateNote()}
		}
		return nil

	case ReliabilityIncomplete, ReliabilityUnverified:
		var out []string
		if c.Misbound != "" {
			out = append(out, "SOME ROWS ARE PROBABLY NOT CALLS TO THIS DEFINITION — "+c.Misbound+
				" The rows say how each call is written; discard the ones made on a value, an instance or "+
				"an expression, and whatever was reached only through them.")
		}
		if s := c.Binding.UnlistedSentence(); s != "" {
			if c.Binding.References > 0 {
				out = append(out, "INCOMPLETE FOR THIS SYMBOL — "+s+". Any of them may be an additional dynamic use of this "+
					"definition. Proven callers are listed below; inspect with search only if dynamic coverage is required.")
			} else {
				out = append(out, "INCOMPLETE FOR THIS SYMBOL — "+s+". Any of them may be a use of this "+
					"definition. Run search for the name before concluding anything from what is absent.")
			}
		}
		if c.Structural != "" {
			out = append(out, "INCOMPLETE FOR THIS TYPE — "+c.Structural)
		}
		if c.Unlinked != "" {
			out = append(out, "INCOMPLETE FOR THIS TYPE — "+c.UnlinkedDetail)
		}
		if c.FloorEmpty != "" {
			out = append(out, "INCOMPLETE — "+c.FloorEmpty)
		}
		if c.Elsewhere.Any() {
			out = append(out, "INCOMPLETE FOR THIS SYMBOL — "+c.Elsewhere.Detail())
		}
		if c.rateIsOverridden() {
			out = append(out, c.rateNote())
		}
		return out

	case ReliabilityNone:
		var head string
		switch {
		case c.NoHierarchy && c.Extracts:
			head = fmt.Sprintf("COVERAGE UNKNOWN — %s for %s cannot be judged: this index holds %d %s "+
				"definition(s) and not one extends or implements relationship between any of them. "+
				"Either this code declares no inheritance at all or GraphLume does not record it for "+
				"%s; an empty list cannot tell the two apart.",
				subject, c.scope(), c.Lang.Definitions, c.scope(), c.scope())
		case !c.Extracts:
			head = fmt.Sprintf("COVERAGE UNKNOWN — %s for %s cannot be judged: GraphLume %s. This index "+
				"holds %d %s definition(s) in %d file(s) and not one reference or relationship that "+
				"starts in them, so nothing about %s can be concluded, and an empty result describes the "+
				"parser, not the code. Running 'graphlume resolve' again will not change this.",
				subject, c.scope(), c.notExtracted(), c.Lang.Definitions, c.scope(), c.Lang.Files, c.About)
		case c.Total == 0:
			head = fmt.Sprintf("COVERAGE UNKNOWN — %s for %s cannot be judged: this index recorded "+
				"no reference resolution at all.", subject, c.scope())
		default:
			head = fmt.Sprintf("COVERAGE UNKNOWN — %s for %s cannot be judged: all %d %s are "+
				"still pending, so the call graph has not been built. Run 'graphlume resolve'.",
				subject, c.scope(), c.Total, c.refWord())
		}
		return []string{head + doNot, capability.LowerBoundCaveat}

	default:
		// The unresolved and ambiguous counts are printed BESIDE the rate,
		// never instead of it: the rate's denominator excludes references
		// that matched no definition here, and ResolutionStats.Bindable
		// permits that exclusion only on condition they are still reported.
		head := fmt.Sprintf("COVERAGE TOO LOW — %s for %s is unreliable here: %.0f%% of the %d "+
			"%s that name something this index defines are bound to a definition "+
			"(%d ambiguous; a further %d matched no definition here at all, out of %d "+
			"%s in total).",
			subject, c.scope(), 100*c.Rate, c.Bindable, c.refWord(), c.Ambiguous, c.Unresolved, c.Total, c.refWord())
		return []string{head + c.resolveAdvice() + doNot, capability.LowerBoundCaveat}
	}
}

// saysWhyNothingIsKnown reports whether the lead already tells the reader that
// nothing can be concluded, and why. A summary that follows it need not repeat
// the reason.
func (c coverage) saysWhyNothingIsKnown() bool { return c.level() == ReliabilityNone }

// reliability is the value Server.reply grades and leads with.
func (c coverage) reliability(subject string) *Reliability {
	return &Reliability{Level: c.level(), Lead: c.lead(), Reasons: c.caveat(subject)}
}

// worse returns whichever of two reliabilities is the less trustworthy, so a
// tool that answers about two symbols (a route from one to another) is graded
// on the weaker end.
func worse(a, b *Reliability) *Reliability {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.Level.rank() < a.Level.rank():
		return b
	default:
		return a
	}
}

// structuralTyping says, for a type in a language where other types can
// satisfy it without naming it, that the declared list is not the whole list.
//
// It matters because the commonest question implementations is asked in
// Go — "what implements this interface" — is exactly the one syntax cannot
// answer: gin's Render interface has sixteen implementations and the index
// declares none of them, since none says `implements`.
func structuralTyping(language, kind string) string {
	if kind != "interface" {
		return ""
	}
	switch strings.ToLower(language) {
	case "go":
		return "A Go type implements an interface without naming it, so the implementations of this " +
			"interface are NOT listed unless a compiler index was imported ('graphlume scip import'). " +
			"Only relationships the source states (an embedded type) appear below."
	case "typescript", "tsx":
		return "TypeScript is structurally typed: an object or class that merely fits this interface " +
			"is not listed. Only declared `implements` and `extends` clauses appear below, unless a " +
			"compiler index was imported ('graphlume scip import')."
	}
	return ""
}

// extLanguage maps file extensions to the language names GraphLume indexes.
//
// The fallback for a symbol whose file row was not read: the index's own
// language column is what coverageFor uses whenever it has it. An extension
// missing here degrades to "this repository", which is accurate.
var extLanguage = map[string]string{
	".go":   "Go",
	".java": "Java",
	".kt":   "Kotlin", ".kts": "Kotlin",
	".ts": "TypeScript", ".tsx": "TypeScript",
	".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	".py": "Python", ".pyi": "Python",
	".rb": "Ruby", ".rs": "Rust", ".cs": "C#",
	".php": "PHP", ".swift": "Swift", ".scala": "Scala",
	".c": "C", ".h": "C", ".cc": "C++", ".cpp": "C++", ".hpp": "C++",
}

func languageOf(filePath string) string {
	return extLanguage[strings.ToLower(path.Ext(filePath))]
}

// ---------------------------------------------------------------- the signal

// SignalInput is everything the overall signal is computed from.
type SignalInput struct {
	// Counts is the confidence histogram of the rows the agent receives.
	Counts map[string]int

	// Reliability is the coverage judgement. Empty means good: a response that
	// makes no relationship claim is not held to relationship coverage.
	Reliability ReliabilityLevel

	// Truncated is true when rows exist that the answer does not carry.
	Truncated bool
}

// OverallSignal is the one function that says how an answer should be taken.
//
// It folds two independent axes into one word, taking the weaker: how far each
// ROW can be trusted (the confidence mix) and how far the SET can (coverage,
// truncation). The rule that matters is the ceiling, and it has no exception:
//
//	coverage none or low     -> never better than unreliable
//	rows probably misbound   -> never better than mixed
//	coverage incomplete, or
//	rows were cut            -> never better than incomplete
//
// So "strong" and "proven" are reachable only from good coverage with nothing
// cut, which is the only condition under which "act on these" is advice
// GraphLume can stand behind. TestSignalNeverExceedsItsCoverage states that
// as a property over every input.
func OverallSignal(in SignalInput) VerdictStatus {
	graded := 0
	for _, n := range in.Counts {
		graded += n
	}
	if graded == 0 {
		return VerdictEmpty
	}
	if in.Reliability == ReliabilityNone || in.Reliability == ReliabilityLow {
		return VerdictUnreliable
	}

	exact, high := in.Counts["exact"], in.Counts["high"]
	medium, low := in.Counts["medium"], in.Counts["low"]
	other := graded - exact - high - medium - low

	var rows VerdictStatus
	switch {
	case exact == graded:
		rows = VerdictProven
	case medium == 0 && low == 0 && other == 0:
		rows = VerdictStrong
	case exact == 0 && high == 0:
		rows = VerdictWeak
	default:
		rows = VerdictMixed
	}

	if !rows.actionable() {
		return rows
	}
	switch {
	case in.Reliability == ReliabilityUnverified:
		// Rows the index has evidence against are not uniformly strong,
		// whatever tier the binding gave them.
		return VerdictMixed
	case in.Reliability == ReliabilityIncomplete, in.Truncated:
		return VerdictIncomplete
	}
	return rows
}
