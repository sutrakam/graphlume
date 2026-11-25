package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/indexer"
	"github.com/sutrakam/graphlume/internal/pipeline"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/gotypes"
)

// compilerIndexersJSON is the machine-readable form of the compiler indexer
// block of `resolve`.
func compilerIndexersJSON(mode config.AccuracyMode, o pipeline.CompilerIndexOutcome) map[string]any {
	if mode == config.AccuracyFast {
		return map[string]any{"ran": false, "reason": "accuracy fast"}
	}
	steps := make([]map[string]any, 0, len(o.Steps))
	for _, s := range o.Steps {
		m := map[string]any{
			"language": s.Language, "dir": s.Dir, "indexer": s.Indexer,
		}
		switch {
		case s.Skipped != "":
			m["skipped"] = s.Skipped
			if len(s.Diagnostic) > 0 {
				m["diagnostic"] = s.Diagnostic
			}
		default:
			m["version"], m["output"] = s.Version, s.Output
			if len(s.Notes) > 0 {
				m["notes"] = s.Notes
			}
			m["ran"], m["reused"] = s.Ran, s.Reused
			m["run_ms"], m["import_ms"] = s.RunTime.Milliseconds(), s.ImportTime.Milliseconds()
			if s.Err != nil {
				m["error"] = s.Err.Error()
			} else {
				m["files_covered"] = s.Import.Ingest.DocumentsMatched
				m["calls_proven"] = s.Import.Promote.Promoted
				m["edges_raised_to_exact"] = s.Import.Edges.Upgraded
				m["edges_new"] = s.Import.Edges.Inserted
				m["relations_attributed_to_top_level"] = s.Import.Reattributed
				m["references_settled"] = s.Import.Resolutions.Bound
				m["references_corrected"] = s.Import.Resolutions.Corrected
			}
		}
		steps = append(steps, m)
	}
	out := map[string]any{
		"steps": steps, "files_covered": o.Proof.Files,
		"references_ruled_out": o.Proof.RuledOut, "references_unbound": o.Proof.Unbound,
		"heuristic_calls_withdrawn": o.Withdrawn,
		"nested_projects_folded":    o.Folded,
	}
	if o.Note != "" {
		out["note"] = o.Note
	}
	if o.Err != nil {
		out["error"] = o.Err.Error()
	}
	return out
}

// renderCompilerIndexers prints one block per indexer. Nothing is printed when
// no project has a compiler indexer to report on, so a repository of Go alone
// (read by the built-in type check, printed separately) sees no empty heading.
func renderCompilerIndexers(out io.Writer, mode config.AccuracyMode, o pipeline.CompilerIndexOutcome) {
	if mode == config.AccuracyFast || (len(o.Steps) == 0 && o.Folded == 0 && o.Err == nil) {
		return
	}
	// A tool that is not installed is the same sentence for every project of
	// its language: say it once, with how many projects it covers.
	sameSkip := map[string]int{}
	for _, s := range o.Steps {
		if s.Skipped != "" {
			sameSkip[s.Indexer+"\x00"+s.Language+"\x00"+s.Skipped]++
		}
	}
	printedSkip := map[string]bool{}
	// Several projects that one indexer read without error are one block, with
	// totals, rather than the same two lines once per project.
	type okGroup struct {
		steps []pipeline.CompilerIndexStep
	}
	okGroups := map[string]*okGroup{}
	for _, s := range o.Steps {
		if s.Skipped == "" && s.Err == nil {
			k := s.Indexer + "\x00" + s.Language
			if okGroups[k] == nil {
				okGroups[k] = &okGroup{}
			}
			okGroups[k].steps = append(okGroups[k].steps, s)
		}
	}
	printedOK := map[string]bool{}
	for _, s := range o.Steps {
		if g := okGroups[s.Indexer+"\x00"+s.Language]; s.Skipped == "" && s.Err == nil && g != nil && len(g.steps) > 1 {
			k := s.Indexer + "\x00" + s.Language
			if printedOK[k] {
				continue
			}
			printedOK[k] = true
			renderCompilerGroup(out, g.steps)
			continue
		}
		label := fmt.Sprintf("%s at %s", s.Indexer, dirLabel(s.Dir))
		if s.Version != "" {
			label = fmt.Sprintf("%s %s at %s", s.Indexer, s.Version, dirLabel(s.Dir))
		}
		switch {
		case s.Skipped != "":
			key := s.Indexer + "\x00" + s.Language + "\x00" + s.Skipped
			if printedSkip[key] {
				continue
			}
			printedSkip[key] = true
			more := ""
			if n := sameSkip[key]; n > 1 {
				more = fmt.Sprintf(" (the same for %d projects)", n)
			}
			fmt.Fprintf(out, "  %-12s %s (%s): not run: %s%s\n", "Compiler:", s.Indexer, s.Language, s.Skipped, more)
			for _, l := range s.Diagnostic {
				fmt.Fprintf(out, "               %s\n", l)
			}
		case s.Err != nil:
			fmt.Fprintf(out, "  %-12s %s (%s): %s, but its index was NOT imported: %v\n", "Compiler:",
				label, s.Language, ranOrReused(s), shortErr(s.Err))
		default:
			fmt.Fprintf(out, "  %-12s %s (%s): %s; imported in %s\n", "Compiler:",
				label, s.Language, ranOrReused(s), dur(s.ImportTime))
			fmt.Fprintf(out, "               %d files covered, %d calls proven, %d edges raised to exact, %d new\n",
				s.Import.Ingest.DocumentsMatched, s.Import.Promote.Promoted,
				s.Import.Edges.Upgraded, s.Import.Edges.Inserted)
			if s.Import.Reattributed > 0 || s.Import.Resolutions.Corrected > 0 {
				fmt.Fprintf(out, "               %d relations outside any indexed definition attached to a top level or an enclosing definition, %d references moved to the definition the compiler named\n",
					s.Import.Reattributed, s.Import.Resolutions.Corrected)
			}
			for _, n := range s.Notes {
				fmt.Fprintf(out, "               note: %s\n", n)
			}
		}
	}
	if o.Folded > 0 {
		fmt.Fprintf(out, "  %-12s %d nested Python project(s) are read by the run of the project above them\n",
			"Compiler:", o.Folded)
	}
	if o.Err != nil {
		fmt.Fprintf(out, "  %-12s the compiler pass stopped: %v\n", "Compiler:", o.Err)
	}
	if o.Imported() > 0 {
		fmt.Fprintf(out, "               %d files read by a compiler indexer; %d references it resolved outside this "+
			"repository (%d of them bound by name and unbound), %d heuristic calls it proved false\n",
			o.Proof.Files, o.Proof.RuledOut, o.Proof.Unbound, o.Withdrawn)
	}
}

// shortErr is an error as one line a person can read in a summary: the
// message up to its first parenthesis or sentence break, bounded. The full text
// is in the JSON output.
func shortErr(err error) string {
	msg := err.Error()
	for _, cut := range []string{" (", "\n"} {
		if i := strings.Index(msg, cut); i > 0 {
			msg = msg[:i]
		}
	}
	if len(msg) > 220 {
		msg = msg[:220] + "..."
	}
	return msg
}

func ranOrReused(s pipeline.CompilerIndexStep) string {
	if s.Reused {
		return "reused its earlier index (nothing it reads has changed)"
	}
	return "ran in " + dur(s.RunTime)
}

func dirLabel(d string) string {
	if d == "." || d == "" {
		return "the repository root"
	}
	return d
}

func dur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// suggestedIndexers names the compiler indexers that are not installed for a
// language this index has many files in (indexer.SuggestMinFiles or more with
// definitions). The count comes from the index, only the presence of a binary
// on PATH is asked of the machine, and nothing is installed. Under accuracy
// fast it is empty: that mode promises no compiler pass, so a hint to add one
// would contradict the setting.
func suggestedIndexers(ctx context.Context, reg *registry.Registry, runID int64, cfg config.Config) []indexer.Suggestion {
	if cfg.Accuracy == config.AccuracyFast {
		return []indexer.Suggestion{}
	}
	cov, err := reg.CompilerCoverage(ctx, runID, gotypes.Detail)
	if err != nil {
		return []indexer.Suggestion{}
	}
	langs := make([]indexer.LanguageFiles, 0, len(cov))
	for _, l := range cov {
		langs = append(langs, indexer.LanguageFiles{Language: l.Language, Files: l.Files})
	}
	out := indexer.Suggest(ctx, nil, cfg, langs)
	if out == nil {
		out = []indexer.Suggestion{}
	}
	return out
}

// renderSuggestedIndexers prints one line per suggestion. quiet suppresses it,
// as it does every other line that is not an answer.
func renderSuggestedIndexers(out io.Writer, quiet bool, sugg []indexer.Suggestion) {
	if quiet {
		return
	}
	for _, s := range sugg {
		fmt.Fprintf(out, "  %-12s %s: %d files with definitions, no %s installed. It would add %s. To get it: %s\n",
			"Suggested:", s.Language, s.Files, s.Indexer, s.Adds, s.Command)
	}
}

// renderCompilerGroup prints the projects one compiler indexer read without
// error as one block: which projects, then the totals.
func renderCompilerGroup(out io.Writer, steps []pipeline.CompilerIndexStep) {
	first := steps[0]
	name := first.Indexer
	if first.Version != "" {
		name += " " + first.Version
	}
	dirs := make([]string, 0, len(steps))
	var files int64
	var proven, raised, added int
	var took time.Duration
	for _, s := range steps {
		dirs = append(dirs, dirLabel(s.Dir))
		files += s.Import.Ingest.DocumentsMatched
		proven += s.Import.Promote.Promoted
		raised += s.Import.Edges.Upgraded
		added += s.Import.Edges.Inserted
		took += s.ImportTime
	}
	where := strings.Join(dirs, ", ")
	if len(dirs) > 4 {
		where = strings.Join(dirs[:4], ", ") + fmt.Sprintf(" and %d more", len(dirs)-4)
	}
	fmt.Fprintf(out, "  %-12s %s (%s): read %d projects (%s); imported in %s\n", "Compiler:",
		name, first.Language, len(steps), where, dur(took))
	fmt.Fprintf(out, "               %d files covered, %d calls proven, %d edges raised to exact, %d new\n",
		files, proven, raised, added)
}
