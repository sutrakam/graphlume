package model

import "strings"

// The four values files.parse_status can hold.
//
// They describe what the PARSER did with the file, not what was extracted from
// it. That distinction is the point of this file: for a long time "ok" meant
// "at least one symbol or reference was recorded" and "partial" meant "none
// were", so a file with 150 syntax errors was ok and a valid file holding only
// constants was partial.
//
//	ok       the parser read the whole file and reported no syntax error
//	partial  a syntax tree was built, but it is not a clean reading of the whole
//	         file: the parser reported syntax errors, or it stopped before the
//	         end because the file exceeded the parse budget
//	failed   no usable syntax tree
//	skipped  no syntax tree was attempted
//
// Every status other than ok carries a [ParseReason] at the start of
// File.ParseError, so "why" is a stable token and not a sentence to be matched.
//
// The column's CHECK constraint (migration 0001) admits exactly these four
// values, which is why the finer distinctions live in the reason rather than in
// more statuses: widening a CHECK in SQLite means rebuilding a table that nine
// others reference.
const (
	ParseStatusOK      = "ok"
	ParseStatusPartial = "partial"
	ParseStatusFailed  = "failed"
	ParseStatusSkipped = "skipped"
)

// ParseReason is the stable token that says why a file is not [ParseStatusOK].
//
// It is stored as the leading word of files.parse_error, followed by ": " and
// free text for a person, e.g.
//
//	syntax_errors: 12 error nodes and 1 missing token, first at line 5962
//
// The token is the contract; the text after it may be reworded.
type ParseReason string

const (
	// ParseReasonNone is the reason of a file that parsed cleanly.
	ParseReasonNone ParseReason = ""

	// ParseReasonSyntaxErrors (status partial): the parser reached the end of
	// the file and reported error nodes or missing tokens. Definitions inside
	// the regions it could not read may be absent.
	ParseReasonSyntaxErrors ParseReason = "syntax_errors"

	// ParseReasonBudgetExceeded (status partial): the parse was stopped before
	// the end of the file. What was read is indexed; the rest is missing.
	ParseReasonBudgetExceeded ParseReason = "budget_exceeded"

	// ParseReasonStoppedEarly (status partial): the parser itself gave up
	// before the end of the file, for a reason that is not one of the budgets
	// (it accepted a prefix, or met an internal inconsistency). The effect on
	// the index is the same as a budget stop: the rest of the file is missing.
	ParseReasonStoppedEarly ParseReason = "stopped_early"

	// ParseReasonTextOnly (status skipped): the file's grammar has no
	// definition query, so no symbols can be extracted. Its text IS indexed
	// for full-text search.
	ParseReasonTextOnly ParseReason = "text_only"

	// ParseReasonGrammarUnavailable (status skipped): the grammar failed to
	// load or rejected its query. The file's text is indexed for search.
	ParseReasonGrammarUnavailable ParseReason = "grammar_unavailable"

	// ParseReasonNoGrammar (status skipped): no grammar claims the file type.
	// The pipeline stores this one as a sentence beginning "no grammar", not
	// as the token; ParseReasonOf reads both.
	ParseReasonNoGrammar ParseReason = "no_grammar"

	// ParseReasonTooLarge (status skipped): the file is over the size limit.
	ParseReasonTooLarge ParseReason = "too_large"

	// ParseReasonEmpty (status skipped): the file has no bytes.
	ParseReasonEmpty ParseReason = "empty"

	// Discovery's reasons (status skipped). The spellings are the ones
	// internal/acquire/fs has always written.
	ParseReasonBinary        ParseReason = "binary"
	ParseReasonSymlink       ParseReason = "symlink"
	ParseReasonSymlinkEscape ParseReason = "symlink_escapes_root"
	ParseReasonIrregular     ParseReason = "irregular"
	ParseReasonUnreadable    ParseReason = "unreadable"

	// ParseReasonLegacy marks a row an older build wrote under the old meaning
	// of partial: "parsed, but no symbol or reference was recorded". It says
	// nothing about syntax errors or budgets. See [File.ParseReason].
	ParseReasonLegacy ParseReason = "legacy_no_definitions"

	// ParseReasonUnspecified is a non-ok row whose text names no known reason:
	// every failed file, and anything written by a build this one cannot read.
	ParseReasonUnspecified ParseReason = "unspecified"
)

// knownParseReasons is the set a stored token is checked against, so free text
// that happens to contain a colon is never mistaken for a reason.
var knownParseReasons = map[ParseReason]bool{
	ParseReasonSyntaxErrors:       true,
	ParseReasonBudgetExceeded:     true,
	ParseReasonStoppedEarly:       true,
	ParseReasonTextOnly:           true,
	ParseReasonGrammarUnavailable: true,
	ParseReasonNoGrammar:          true,
	ParseReasonTooLarge:           true,
	ParseReasonEmpty:              true,
	ParseReasonBinary:             true,
	ParseReasonSymlink:            true,
	ParseReasonSymlinkEscape:      true,
	ParseReasonIrregular:          true,
	ParseReasonUnreadable:         true,
}

// FormatParseError builds the text stored in files.parse_error: the reason
// token, then the detail for a person.
func FormatParseError(reason ParseReason, detail string) string {
	detail = strings.TrimSpace(detail)
	switch {
	case reason == ParseReasonNone:
		return detail
	case detail == "":
		return string(reason)
	default:
		return string(reason) + ": " + detail
	}
}

// ParseReasonOf reads the reason out of a stored (parse_status, parse_error)
// pair.
//
// OLD ROWS. An index written before the statuses were redefined is read as
// follows, without a migration:
//
//   - partial with no error text can only be an old row, because every partial
//     row written since carries a reason. It reads as [ParseReasonLegacy]: the
//     file parsed and nothing was extracted, which is not a parse problem.
//   - ok cannot be told apart from a new ok row. An old ok file may contain
//     syntax errors that were never recorded. Re-indexing rewrites every row,
//     and a new build re-parses every file on its first index (see
//     pipeline.Diff), so this ambiguity lasts only until then.
//   - the prose reasons older builds wrote ("no grammar for this file type",
//     "no tags query available for …", "source is N bytes, over the …") map to
//     the reason they describe. A file with no tags query was NOT text-indexed
//     by those builds, and still reads as text_only: the row says what kind of
//     file it is; whether its text is in the search index depends on the build
//     that wrote it.
func ParseReasonOf(status, parseError string) ParseReason {
	if status == ParseStatusOK || status == "" {
		return ParseReasonNone
	}
	text := strings.TrimSpace(parseError)
	if text == "" {
		if status == ParseStatusPartial {
			return ParseReasonLegacy
		}
		if status == ParseStatusSkipped {
			// Older builds recorded an empty file as skipped with no text.
			return ParseReasonEmpty
		}
		return ParseReasonUnspecified
	}

	token := text
	if i := strings.IndexByte(text, ':'); i >= 0 {
		token = text[:i]
	}
	if r := ParseReason(token); knownParseReasons[r] {
		return r
	}

	switch {
	case strings.HasPrefix(strings.ToLower(text), "no grammar"):
		// The pipeline's own wording for a file type nothing claims. Matched
		// loosely on purpose: the sentence after these two words has been
		// reworded before and the reason has not changed.
		return ParseReasonNoGrammar
	case strings.HasPrefix(text, "no tags query available"):
		return ParseReasonTextOnly
	case strings.HasPrefix(text, "source is ") && strings.Contains(text, " limit"):
		return ParseReasonTooLarge
	case strings.HasPrefix(text, "grammar ") && strings.HasSuffix(text, "failed to load"),
		strings.HasPrefix(text, "tags query for "):
		return ParseReasonGrammarUnavailable
	}
	return ParseReasonUnspecified
}

// ParseReason is the reason this file is not ok. See [ParseReasonOf].
func (f File) ParseReason() ParseReason {
	return ParseReasonOf(f.ParseStatus, f.ParseError)
}

// ParseIncomplete reports whether indexing work on this file was abandoned:
// the parse failed outright, or it was stopped before the end of the file.
//
// This, and only this, is what makes an index run degraded. A file the parser
// read to the end is not abandoned work, whatever it did or did not contain,
// and neither is a file that was never going to be parsed.
func (f File) ParseIncomplete() bool {
	return ParseAbandoned(f.ParseStatus, f.ParseError)
}

// ParseAbandoned is [File.ParseIncomplete] for a bare status pair.
func ParseAbandoned(status, parseError string) bool {
	switch status {
	case ParseStatusFailed:
		return true
	case ParseStatusPartial:
		switch ParseReasonOf(status, parseError) {
		case ParseReasonBudgetExceeded, ParseReasonStoppedEarly:
			return true
		}
	}
	return false
}

// TextIndexed reports whether a file with this status has its text in the
// full-text index.
//
// Every file a syntax tree was attempted for does, and so does a file whose
// grammar yields no definitions: the answer to "why is this function name not
// found" must not be "its language has no query".
func TextIndexed(status, parseError string) bool {
	if status != ParseStatusSkipped {
		return true
	}
	switch ParseReasonOf(status, parseError) {
	case ParseReasonTextOnly, ParseReasonGrammarUnavailable:
		return true
	}
	return false
}

// SQLParseAbandoned is the WHERE-clause form of [ParseAbandoned], for the
// counts that must be taken in SQL. column is the files table alias prefix,
// e.g. "" or "f.".
//
// Kept beside the Go predicate so the two cannot drift: a run is degraded when
// this matches at least one row, and a reader explaining the run must name
// exactly those rows.
func SQLParseAbandoned(prefix string) string {
	return "(" + prefix + "parse_status = 'failed' OR (" + prefix + "parse_status = 'partial' AND (" +
		prefix + "parse_error LIKE 'budget_exceeded%' OR " + prefix + "parse_error LIKE 'stopped_early%')))"
}

// ParseOutcomeLabel is the short, human name of a (status, reason) pair, used
// wherever a file's parse result is shown to a person.
func ParseOutcomeLabel(status, parseError string) string {
	reason := ParseReasonOf(status, parseError)
	switch status {
	case ParseStatusOK, "":
		return "clean parse"
	case ParseStatusPartial:
		switch reason {
		case ParseReasonSyntaxErrors:
			return "parsed with syntax errors"
		case ParseReasonBudgetExceeded:
			return "incomplete: parse budget exceeded"
		case ParseReasonStoppedEarly:
			return "incomplete: parser stopped early"
		case ParseReasonLegacy:
			return "no definitions found (recorded by an older version)"
		}
		return "partial"
	case ParseStatusFailed:
		return "failed"
	case ParseStatusSkipped:
		switch reason {
		case ParseReasonTextOnly:
			return "text only, no definitions"
		case ParseReasonGrammarUnavailable:
			return "text only, grammar unavailable"
		case ParseReasonTooLarge:
			return "skipped: over the size limit"
		case ParseReasonNoGrammar:
			return "skipped: no grammar"
		case ParseReasonEmpty:
			return "skipped: empty"
		case ParseReasonUnspecified:
			return "skipped"
		}
		return "skipped: " + string(reason)
	}
	return status
}
