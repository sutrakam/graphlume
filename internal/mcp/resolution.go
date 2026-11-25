package mcp

import (
	"context"
	"fmt"
	"sort"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/gotypes"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bindingFloor is capability.CoverageFloor: the one floor every surface that
// publishes a coverage verdict uses.
//
// It is not a second, independently-argued constant, checked against
// BindingRate while the relationship tools checked ResolutionStats.Rate
// against 0.5 — the two would disagree out loud, calling one index 37% and
// unusable in the same session as 79% and unremarkable. The argument that
// produced 0.6 was the better one and it is recorded at
// capability.CoverageFloor, which is where both surfaces read it.
//
// It is checked beside the rate and the Ambiguous/Unresolved counts (see
// resolutionSummary) so a reader who would draw the line elsewhere can.
const bindingFloor = capability.CoverageFloor

// resolutionCoverage answers "how much of this index actually resolved?".
//
// # Why a tool of its own when overview already says it
//
// The overview says it ONCE, as one line among file and symbol counts, and an
// agent reaches it at the start of a session. The question this tool answers is
// asked at a different moment and for a different reason: an impact or
// callers result has come back short, and the agent has to decide whether
// that is a fact about the repository or a fact about the index. Making it ask
// for the whole overview again to find out is how an agent stops asking.
//
// It needs only the Registry, so it registers with the plain tools rather than
// with registerCapabilities: an index whose references were never resolved is
// PRECISELY the index this tool exists to describe, and gating it on the graph
// would silence it exactly there.
func (s *Server) resolutionCoverage(ctx context.Context, req *sdk.CallToolRequest, _ emptyArgs) (*sdk.CallToolResult, *Response, error) {
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}

	// Read through capDeps, not the registry directly. ResolutionStats is a
	// GROUP BY over every reference row in the run; capability.Deps memoises it
	// for the server's lifetime, and this tool asking the same question a
	// second time in a session should cost nothing.
	rs, err := s.capDeps.ResolutionStats(ctx, runID)
	if err != nil {
		return failFrom(err, "read resolution stats: %v", err)
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary:       resolutionSummary(rs),
	}
	resp.Notes = append(resp.Notes, resolutionNotes(rs)...)

	// One row per language that holds code. The run-wide rate is an average
	// over languages that can differ completely: a repository can bind its
	// Java well and hold not one Kotlin reference, and the average of those
	// describes neither.
	if langs, err := s.capDeps.LanguageBindings(ctx, runID); err == nil {
		// A compiler index changes what a low rate means, so each row says
		// whether one read the language. Without the figures the rows stand.
		checked := map[string]registry.CheckedLanguage{}
		cov, covErr := s.deps.Registry.CompilerCoverage(ctx, runID, gotypes.Detail)
		if covErr == nil {
			for _, c := range cov {
				checked[c.Language] = c
			}
		}
		resp.Items = languageRows(langs, checked)
		resp.Languages = languageCoverage(langs, checked, covErr == nil)
		if covErr != nil {
			resp.Notes = append(resp.Notes, fmt.Sprintf(
				"What a compiler read in each language could not be counted (%v), so no language is "+
					"called compiler-checked here; that is unknown, not none.", covErr))
		}
		if len(resp.Items) > 0 {
			resp.Notes = append(resp.Notes,
				"The rows are the same figures per language. An answer about a symbol is judged on its "+
					"own language's row, not on the summary line above.")
		}
	}
	resp.Notes = append(resp.Notes, capability.LowerBoundCaveat)
	return s.reply(ctx, req, resp)
}

// Compiler tiers of a LanguageCoverage row.
const (
	// TierCompiler: a compiler read every file of the language that holds a
	// definition. The only tier whose relationships may be called exact as a
	// whole.
	TierCompiler = "compiler"
	// TierPartial: a compiler read some of the files and not others. The
	// relationships it proved are exact; the rest were read by the parser.
	TierPartial = "partial"
	// TierParsed: no compiler read any file of the language.
	TierParsed = "parsed"
	// TierUnknown: what a compiler read could not be counted.
	TierUnknown = "unknown"
)

// LanguageCoverage is one language's coverage row in numbers.
//
// # Why the share and not a yes or no
//
// A language a compiler read in one file of two hundred is not a
// compiler-checked language, and a surface that reduced "a compiler ran" to a
// badge called it one. The tier is derived from the share and only
// TierCompiler means the whole language; every other row carries the counts
// so a reader sees how much was proved.
type LanguageCoverage struct {
	// Language is the index's name for it ("typescript"); Name is the display
	// name the rows use ("TypeScript").
	Language string `json:"language"`
	Name     string `json:"name"`
	// Files holds the files with at least one definition, the population a
	// compiler could read. Definitions counts the code definitions in them.
	Files       int64 `json:"files"`
	Definitions int64 `json:"definitions"`
	// Resolved and Bindable are the reference binding counts behind the rate.
	Resolved int64 `json:"resolved"`
	Bindable int64 `json:"bindable"`
	// CompilerFiles is how many of Files a compiler read; CompilerShare is
	// CompilerFiles / Files (0 when Files is 0).
	CompilerFiles int64   `json:"compiler_files"`
	CompilerShare float64 `json:"compiler_share"`
	CompilerTool  string  `json:"compiler_tool,omitempty"`
	// Tier is one of TierCompiler, TierPartial, TierParsed, TierUnknown.
	Tier string `json:"tier"`
}

// languageCoverage is languageRows as numbers, in the same order. known is
// false when the compiler figures could not be read: every tier is then
// TierUnknown, never TierParsed, because a failed count is not a zero.
func languageCoverage(langs map[string]registry.LanguageBinding, checked map[string]registry.CheckedLanguage, known bool) []LanguageCoverage {
	names := sortedCodeLanguages(langs)
	out := make([]LanguageCoverage, 0, len(names))
	for _, name := range names {
		l := langs[name]
		row := LanguageCoverage{
			Language: name, Name: capability.DisplayLanguage(name),
			Files: l.Files, Definitions: l.Definitions,
			Resolved: l.Resolved, Bindable: l.Bindable(),
		}
		c, ok := checked[name]
		if ok && c.Files > 0 {
			// The compiler figures count files holding a definition, which is
			// the population that matters; LanguageBinding counts every file.
			row.Files = c.Files
		}
		switch {
		case !known:
			row.Tier = TierUnknown
		case !ok || c.Checked == 0:
			row.Tier = TierParsed
		default:
			row.CompilerFiles = c.Checked
			row.CompilerTool = c.Tool
			if row.Files > 0 {
				row.CompilerShare = float64(c.Checked) / float64(row.Files)
			}
			row.Tier = TierPartial
			if c.Checked >= row.Files {
				row.Tier = TierCompiler
			}
		}
		out = append(out, row)
	}
	return out
}

// sortedCodeLanguages lists the languages with definitions, most definitions
// first: the order of the coverage rows.
func sortedCodeLanguages(langs map[string]registry.LanguageBinding) []string {
	names := make([]string, 0, len(langs))
	for name, l := range langs {
		if l.Definitions > 0 {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := langs[names[i]], langs[names[j]]
		if a.Definitions != b.Definitions {
			return a.Definitions > b.Definitions
		}
		return names[i] < names[j]
	})
	return names
}

// languageRows renders each language's binding outcome, largest first.
//
// The rows carry no confidence tier: they are counts, and grading counts would
// put a verdict on a coverage report.
//
// checked holds what a compiler read in each language. A language below the
// floor that a compiler read is not one to install an indexer for: the
// relationships the compiler proved are exact, and the rate counts only the
// references left to name matching. The row says so instead of calling the
// whole language unreliable.
func languageRows(langs map[string]registry.LanguageBinding, checked map[string]registry.CheckedLanguage) []Item {
	names := sortedCodeLanguages(langs)
	items := make([]Item, 0, len(names))
	for _, name := range names {
		l := langs[name]
		display := capability.DisplayLanguage(name)
		if display == "" {
			display = "(no language recorded)"
		}
		var why string
		switch {
		case !l.ExtractsReferences():
			why = fmt.Sprintf("%d file(s); %s, so nothing about callers in it can be concluded",
				l.Files, capability.NoReferencesClause(name, l.Definitions))
		case l.Total == 0:
			why = fmt.Sprintf("%d definition(s) in %d file(s); %d relationship(s) recorded, none from "+
				"reference resolution", l.Definitions, l.Files, l.Relationships)
		case l.Pending == l.Total:
			why = fmt.Sprintf("%d definition(s) in %d file(s); all %d reference(s) are pending — run "+
				"'graphlume resolve'", l.Definitions, l.Files, l.Total)
		default:
			why = fmt.Sprintf("%d definition(s) in %d file(s); resolved %d of %d references that name "+
				"something this index defines (%.1f%%), %d ambiguous, %d matched no definition here",
				l.Definitions, l.Files, l.Resolved, l.Bindable(), 100*l.BindingRate(), l.Ambiguous, l.Unresolved)
			if l.Pending > 0 {
				why += fmt.Sprintf(", %d pending", l.Pending)
			}
			if l.BindingRate() < bindingFloor {
				if c, ok := checked[name]; ok && c.Checked > 0 {
					tool := c.Tool
					if tool == "" {
						tool = "a compiler"
					}
					why += fmt.Sprintf("; compiler-checked by %s in %d of %d file(s): the relationships "+
						"it proved are exact, and the references it left to name matching bind at a rate below the floor",
						tool, c.Checked, c.Files)
				} else {
					why += "; below the floor, so relationship answers in this language are unreliable"
				}
			} else if c, ok := checked[name]; ok && c.Checked > 0 && c.Checked < c.Files {
				// Above the floor a compiler that read only part of the
				// language is still worth saying: "a compiler ran" is not
				// "the language is compiler-checked".
				why += fmt.Sprintf("; compiler-checked in %d of %d file(s)", c.Checked, c.Files)
			}
		}
		if l.ExtractsReferences() && l.Hierarchy == 0 {
			why += "; no extends or implements relationship is recorded for it"
		}
		items = append(items, Item{Kind: "language", Name: display, Why: why})
	}
	return items
}

// resolutionSummary states the rate and, in the same sentence, everything the
// rate's denominator left out.
//
// # Which of the two rates is published, and why
//
// BindingRate, not Rate. registry.ResolutionStats documents both and is unusually
// explicit about the difference: Rate divides by EVERY reference, including the
// ones that matched zero candidates in this index, which are overwhelmingly
// calls into third-party libraries. A project that imports heavily therefore
// scores badly on Rate no matter how well GraphLume resolved it, so Rate mostly
// measures a dependency surface while reading like a defect rate — and an agent
// that reads "46%" concludes the index is broken when the index is fine.
// BindingRate divides by the references that name something this index actually
// defines, which is the population any improvement to binding could ever move.
//
// # The exclusion is only honest because the excluded are printed
//
// ResolutionStats.Bindable states the condition on which those references may be
// excluded: "Callers must print Unresolved beside this rate, never instead of
// it." An unresolved reference is not proof of anything about a library — it can
// equally be a definition in this repository that the extractor never emitted —
// so this summary names the unresolved count, the ambiguous count and the total
// alongside the percentage. A reader who would draw the denominator differently
// has every number needed to do so. This is also the wording internal/cli's
// bindingCaveat and overview already use, so the CLI and the MCP
// surface cannot drift into publishing two different coverage numbers.
func resolutionSummary(rs registry.ResolutionStats) string {
	switch {
	case rs.Total == 0:
		return "No reference data is recorded for this index, so resolution coverage cannot be stated."
	case rs.Pending == rs.Total:
		return fmt.Sprintf("%d reference(s) were extracted and none resolved — the call graph "+
			"has not been built.", rs.Total)
	}

	by := ""
	if rs.ByCompiler > 0 {
		by = fmt.Sprintf(", %d of them by an imported compiler index", rs.ByCompiler)
	}
	pending := ""
	if rs.Pending > 0 {
		pending = fmt.Sprintf("; %d reference(s) are still pending", rs.Pending)
	}
	return fmt.Sprintf(
		"resolved %d of %d references that name something this index defines (%.1f%%)%s; "+
			"%d ambiguous, %d matched no definition here at all (mostly calls into libraries), "+
			"%d references in total%s.",
		rs.Resolved, rs.Bindable(), 100*rs.BindingRate(), by,
		rs.Ambiguous, rs.Unresolved, rs.Total, pending)
}

// resolutionNotes says what to DO about the number above.
//
// A coverage figure with no next step is a number an agent can only worry
// about. Each branch below names the one command that moves it, and the
// low-coverage branch says what a low figure does to every OTHER answer this
// server gives — which is the part that changes behaviour.
func resolutionNotes(rs registry.ResolutionStats) []string {
	switch {
	case rs.Total == 0:
		return []string{
			"Nothing has been resolved because nothing was extracted. Run 'graphlume index' " +
				"followed by 'graphlume resolve'. Until then an empty callers or " +
				"impact result says nothing about the code.",
		}
	case rs.Pending == rs.Total:
		return []string{
			"The references exist but none has been bound to a definition, so callers, " +
				"references and impact will return nothing regardless of the code. " +
				"Run 'graphlume resolve'.",
		}
	}

	notes := []string{
		"AMBIGUOUS means the name matched several definitions here and GraphLume refused to " +
			"guess between them. UNRESOLVED means it matched none, which usually indicates a " +
			"library call and sometimes a definition this index failed to cover — those are " +
			"different problems with different fixes, which is why they are counted apart.",
	}
	if rs.Pending > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d reference(s) have not been resolved since the last index — run 'graphlume resolve' "+
				"so this figure describes the whole index.", rs.Pending))
	}
	if rs.BindingRate() < bindingFloor {
		notes = append(notes, fmt.Sprintf(
			"COVERAGE TOO LOW — fewer than %.0f%% of bindable references are bound (%.0f%%). "+
				"Relationship answers from this index are a minority report: treat callers, "+
				"references and impact results as leads to confirm with search, "+
				"not as answers.", 100*bindingFloor, 100*rs.BindingRate()))
	}
	return notes
}
