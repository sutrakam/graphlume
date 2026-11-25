package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
)

// The parse summary is printed by `index` (full and incremental), `status` and
// `doctor`, from one query (registry.ParseSummary) and through the one renderer
// below. Three commands describing the same files in three vocabularies is how
// "index says 0 failures, status says degraded" happened.

// parseSummaryFor reads the summary for a run through an open database.
func parseSummaryFor(ctx context.Context, db *store.DB, runID int64) (registry.ParseSummary, error) {
	return registry.New(db).ParseSummary(ctx, runID)
}

// summaryLine is one labelled row of the summary and the file names under it.
type summaryLine struct {
	Label  string
	Value  string
	Detail []string
}

// parseSummaryLines turns a summary into rows. Rows that would say "0" are
// left out, except Parsed, which is the row every reader looks for.
func parseSummaryLines(s registry.ParseSummary) []summaryLine {
	var out []summaryLine

	parsed := fmt.Sprintf("%d (%d clean", s.Parsed(), s.Clean)
	if s.SyntaxErrors > 0 {
		parsed += fmt.Sprintf(", %d with syntax errors", s.SyntaxErrors)
	}
	if s.Incomplete > 0 {
		parsed += fmt.Sprintf(", %d incomplete", s.Incomplete)
	}
	if s.LegacyPartial > 0 {
		parsed += fmt.Sprintf(", %d recorded by an older version", s.LegacyPartial)
	}
	out = append(out, summaryLine{Label: "Parsed", Value: parsed + ")"})

	if s.SyntaxErrors > 0 {
		out = append(out, summaryLine{
			Label: "Syntax errors",
			Value: fmt.Sprintf("%d %s read to the end with errors the parser recovered from; "+
				"definitions inside the unreadable regions may be missing",
				s.SyntaxErrors, files(s.SyntaxErrors)),
		})
	}
	if s.Incomplete > 0 {
		out = append(out, summaryLine{
			Label: "Incomplete",
			Value: fmt.Sprintf("%d %s whose parse was stopped early; definitions after the stop are missing",
				s.Incomplete, files(s.Incomplete)),
			Detail: noteLines(s.IncompleteFiles, s.Incomplete, true),
		})
	}
	if s.Failed > 0 {
		out = append(out, summaryLine{
			Label:  "Parse failures",
			Value:  fmt.Sprintf("%d %s with nothing extracted", s.Failed, files(s.Failed)),
			Detail: noteLines(s.FailedFiles, s.Failed, true),
		})
	}
	if s.TextOnly > 0 {
		out = append(out, summaryLine{
			Label: "Text only",
			Value: fmt.Sprintf("%d %s searchable by text, no definitions (no definition query for the language): %s",
				s.TextOnly, files(s.TextOnly), languageList(s.TextOnlyLanguages, 6)),
		})
	}
	if s.TooLarge > 0 {
		out = append(out, summaryLine{
			Label: "Too large",
			Value: fmt.Sprintf("%d %s over the size limit, not parsed and not searchable "+
				"(raise index.max_file_bytes in .graphlume.yml to include them)",
				s.TooLarge, files(s.TooLarge)),
			Detail: noteLines(s.LargestTooLarge, s.TooLarge, false),
		})
	}
	if s.SkippedOther > 0 {
		out = append(out, summaryLine{
			Label: "Skipped",
			Value: fmt.Sprintf("%d %s not indexed: %s", s.SkippedOther, files(s.SkippedOther), reasonList(s.OtherReasons)),
		})
	}
	if s.LegacyPartial > 0 {
		out = append(out, summaryLine{
			Label: "Older rows",
			Value: fmt.Sprintf("%d %s carry a status written by an older version, where \"partial\" meant "+
				"\"no definitions found\" and syntax errors were not recorded; "+
				"run 'graphlume index --full' to rewrite them",
				s.LegacyPartial, files(s.LegacyPartial)),
		})
	}
	return out
}

// writeParseSummary prints the rows, each label padded to width, with file
// names indented beneath the row they belong to.
func writeParseSummary(w io.Writer, s registry.ParseSummary, indent string, width int) {
	for _, l := range parseSummaryLines(s) {
		label := l.Label + ":"
		if pad := width - len(label); pad > 0 {
			label += strings.Repeat(" ", pad)
		}
		_, _ = fmt.Fprintf(w, "%s%s %s\n", indent, label, l.Value)
		for _, d := range l.Detail {
			_, _ = fmt.Fprintf(w, "%s%s   %s\n", indent, strings.Repeat(" ", width), d)
		}
	}
}

func files(n int64) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

func isAre(n int64) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// noteLines names the listed files. total is how many there are in all, so a
// truncated list says how many it left out. withDetail adds the stored reason,
// which for a size skip would only repeat the size.
func noteLines(notes []registry.FileNote, total int64, withDetail bool) []string {
	out := make([]string, 0, len(notes)+1)
	for _, n := range notes {
		line := fmt.Sprintf("%s (%s)", n.Path, humanBytes(n.SizeBytes))
		if withDetail && n.Detail != "" {
			line += ": " + n.Detail
		}
		out = append(out, line)
	}
	if rest := total - int64(len(notes)); rest > 0 {
		out = append(out, fmt.Sprintf("and %d more; list them with: graphlume query \"SELECT path, size_bytes, parse_error "+
			"FROM files WHERE index_run_id = :run AND parse_status <> 'ok' ORDER BY size_bytes DESC\"", rest))
	}
	return out
}

func languageList(langs []registry.LanguageCount, limit int) string {
	parts := make([]string, 0, limit+1)
	for i, l := range langs {
		if i == limit {
			parts = append(parts, fmt.Sprintf("+%d more", len(langs)-limit))
			break
		}
		name := l.Language
		if name == "" {
			name = "(unknown)"
		}
		parts = append(parts, fmt.Sprintf("%s %d", name, l.Files))
	}
	return strings.Join(parts, ", ")
}

func reasonList(reasons []registry.ReasonCount) string {
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%s %d", r.Reason, r.Files))
	}
	return strings.Join(parts, ", ")
}
