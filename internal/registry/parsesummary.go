package registry

import (
	"context"
	"fmt"
	"sort"

	"github.com/sutrakam/graphlume/pkg/model"
)

// ParseSummaryListLimit is how many files each named list in a ParseSummary
// holds. The counts beside the lists are always complete.
const ParseSummaryListLimit = 10

// ParseSummary says what happened to every file in a run, in the terms a
// person asks about: how many parsed cleanly, how many with errors, how many
// were cut short, how many are searchable text only, and which files were left
// out for their size.
//
// Every file is in exactly one of the counts, so they sum to Files.
type ParseSummary struct {
	Files int64 `json:"files"`

	// Clean is files the parser read to the end with no syntax error.
	Clean int64 `json:"clean"`
	// SyntaxErrors is files read to the end with error or missing nodes.
	SyntaxErrors int64 `json:"syntax_errors"`
	// Incomplete is files whose parse was stopped before the end.
	Incomplete int64 `json:"incomplete"`
	// Failed is files no usable tree was produced for.
	Failed int64 `json:"failed"`
	// TextOnly is files indexed for full-text search with no definitions,
	// because their grammar has no definition query or could not be used.
	TextOnly int64 `json:"text_only"`
	// TooLarge is files over the size limit: not parsed, not searchable.
	TooLarge int64 `json:"too_large"`
	// SkippedOther is every other file no tree was attempted for: no grammar,
	// binary, empty, symlink and the rest. OtherReasons breaks it down.
	SkippedOther int64 `json:"skipped_other"`
	// LegacyPartial is rows an older build wrote as partial under the old
	// meaning ("no symbol or reference was recorded"). They say nothing about
	// the parse; re-indexing rewrites them.
	LegacyPartial int64 `json:"legacy_partial,omitempty"`

	// TextOnlyLanguages counts the text-only files per language, most first.
	TextOnlyLanguages []LanguageCount `json:"text_only_languages,omitempty"`
	// OtherReasons counts SkippedOther per reason token, most first.
	OtherReasons []ReasonCount `json:"skipped_other_reasons,omitempty"`

	// LargestTooLarge names the largest files skipped for size, largest first.
	LargestTooLarge []FileNote `json:"largest_too_large,omitempty"`
	// IncompleteFiles and FailedFiles name the abandoned files, by path.
	IncompleteFiles []FileNote `json:"incomplete_files,omitempty"`
	FailedFiles     []FileNote `json:"failed_files,omitempty"`
}

// LanguageCount is a number of files in one language.
type LanguageCount struct {
	Language string `json:"language"`
	Files    int64  `json:"files"`
}

// ReasonCount is a number of files skipped for one reason.
type ReasonCount struct {
	Reason string `json:"reason"`
	Files  int64  `json:"files"`
}

// FileNote names one file and why it is on a list.
type FileNote struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Detail    string `json:"detail,omitempty"`
}

// Abandoned is the number of files whose indexing was given up on: failed
// outright or stopped early. It is what makes a run degraded.
func (s ParseSummary) Abandoned() int64 { return s.Failed + s.Incomplete }

// Parsed is the number of files a syntax tree was built for.
func (s ParseSummary) Parsed() int64 {
	return s.Clean + s.SyntaxErrors + s.Incomplete + s.LegacyPartial
}

// ParseSummary reads what happened to every file of a run.
//
// One pass over the files table, classified in Go by the same function every
// other reader uses (model.ParseReasonOf), so this report cannot disagree with
// what a single file's row says. The table has one row per file; on the largest
// index measured (97,000 files) the pass is tens of milliseconds.
func (r *Registry) ParseSummary(ctx context.Context, runID int64) (ParseSummary, error) {
	var s ParseSummary
	rows, err := r.db.QueryContext(ctx, `
		SELECT path, language, size_bytes, parse_status, COALESCE(parse_error,'')
		  FROM files WHERE index_run_id = ?`, runID)
	if err != nil {
		return s, fmt.Errorf("registry: parse summary for run %d: %w", runID, err)
	}
	defer func() { _ = rows.Close() }()

	textLangs := map[string]int64{}
	otherReasons := map[string]int64{}
	for rows.Next() {
		var (
			path, lang, status, perr string
			size                     int64
		)
		if err := rows.Scan(&path, &lang, &size, &status, &perr); err != nil {
			return s, fmt.Errorf("registry: parse summary for run %d: %w", runID, err)
		}
		s.Files++
		reason := model.ParseReasonOf(status, perr)
		note := FileNote{Path: path, SizeBytes: size, Detail: perr}
		switch status {
		case model.ParseStatusOK, "":
			s.Clean++
		case model.ParseStatusFailed:
			s.Failed++
			s.FailedFiles = append(s.FailedFiles, note)
		case model.ParseStatusPartial:
			switch reason {
			case model.ParseReasonBudgetExceeded, model.ParseReasonStoppedEarly:
				s.Incomplete++
				s.IncompleteFiles = append(s.IncompleteFiles, note)
			case model.ParseReasonLegacy:
				s.LegacyPartial++
			default:
				s.SyntaxErrors++
			}
		default: // skipped, and anything a newer build may add
			switch reason {
			case model.ParseReasonTextOnly, model.ParseReasonGrammarUnavailable:
				s.TextOnly++
				textLangs[lang]++
			case model.ParseReasonTooLarge:
				s.TooLarge++
				s.LargestTooLarge = append(s.LargestTooLarge, note)
			default:
				s.SkippedOther++
				otherReasons[string(reason)]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return s, fmt.Errorf("registry: parse summary for run %d: %w", runID, err)
	}

	for lang, n := range textLangs {
		s.TextOnlyLanguages = append(s.TextOnlyLanguages, LanguageCount{Language: lang, Files: n})
	}
	sort.Slice(s.TextOnlyLanguages, func(i, j int) bool {
		a, b := s.TextOnlyLanguages[i], s.TextOnlyLanguages[j]
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Language < b.Language
	})
	for reason, n := range otherReasons {
		s.OtherReasons = append(s.OtherReasons, ReasonCount{Reason: reason, Files: n})
	}
	sort.Slice(s.OtherReasons, func(i, j int) bool {
		a, b := s.OtherReasons[i], s.OtherReasons[j]
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Reason < b.Reason
	})

	s.LargestTooLarge = topNotes(s.LargestTooLarge, func(a, b FileNote) bool {
		if a.SizeBytes != b.SizeBytes {
			return a.SizeBytes > b.SizeBytes
		}
		return a.Path < b.Path
	})
	byPath := func(a, b FileNote) bool { return a.Path < b.Path }
	s.IncompleteFiles = topNotes(s.IncompleteFiles, byPath)
	s.FailedFiles = topNotes(s.FailedFiles, byPath)
	return s, nil
}

// topNotes orders a list and keeps its first ParseSummaryListLimit entries.
// The order is total (every comparison falls back to the path), so the list is
// the same on every run.
func topNotes(in []FileNote, less func(a, b FileNote) bool) []FileNote {
	sort.Slice(in, func(i, j int) bool { return less(in[i], in[j]) })
	if len(in) > ParseSummaryListLimit {
		in = in[:ParseSummaryListLimit:ParseSummaryListLimit]
	}
	return in
}

// RefreshRunHealth recomputes a committed run's status and failure count from
// the files it now holds.
//
// CommitRun writes both once, when a full index is published. An incremental
// pass then rewrites files in place, and a pass run by a NEW build rewrites
// every one of them (pipeline.Diff treats a version change as "all dirty"). The
// run row is not touched by any of that, so without this call an index stays
// "degraded" for files that now parse, and "ready" over files that no longer
// do. The case that matters: a build that counts every file it finds
// no definitions in as a parse failure, where an index built by it would
// keep saying degraded after a build that does not do so re-indexes it.
//
// Only ready and degraded are exchanged. A run in any other state is left
// alone: it is not this function's to resurrect.
func (r *Registry) RefreshRunHealth(ctx context.Context, runID int64) error {
	var abandoned int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM files WHERE index_run_id = ? AND `+model.SQLParseAbandoned(""),
		runID).Scan(&abandoned); err != nil {
		return fmt.Errorf("registry: refresh health of run %d: %w", runID, err)
	}
	status := model.StatusReady
	if abandoned > 0 {
		status = model.StatusDegraded
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE index_runs SET status = ?, parse_failures = ?
		  WHERE id = ? AND status IN ('ready','degraded')`,
		string(status), abandoned, runID); err != nil {
		return fmt.Errorf("registry: refresh health of run %d: %w", runID, err)
	}
	return nil
}
