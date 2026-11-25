package model

import "testing"

func TestParseReasonOf(t *testing.T) {
	cases := []struct {
		name, status, text string
		want               ParseReason
	}{
		// What the current build writes.
		{"clean", ParseStatusOK, "", ParseReasonNone},
		{"syntax errors", ParseStatusPartial, "syntax_errors: 12 error nodes and 1 missing token, first at line 5962", ParseReasonSyntaxErrors},
		{"budget", ParseStatusPartial, "budget_exceeded: parser stopped at byte 10 of 900 (line 2) on its iteration limit", ParseReasonBudgetExceeded},
		{"stopped early", ParseStatusPartial, `stopped_early: parser stopped at byte 10 of 900 (line 2) on parser stop reason "accepted_prefix"`, ParseReasonStoppedEarly},
		{"text only", ParseStatusSkipped, `text_only: no definition query for "perl"; text is indexed for search, no symbols are extracted`, ParseReasonTextOnly},
		{"grammar unavailable", ParseStatusSkipped, `grammar_unavailable: grammar "x" failed to load`, ParseReasonGrammarUnavailable},
		{"no grammar", ParseStatusSkipped, "no_grammar: no grammar for this file type", ParseReasonNoGrammar},
		{"too large", ParseStatusSkipped, "too_large: 300000 bytes, over the 262144 byte limit (index.max_file_bytes)", ParseReasonTooLarge},
		{"empty", ParseStatusSkipped, "empty", ParseReasonEmpty},
		{"binary, a bare token", ParseStatusSkipped, "binary", ParseReasonBinary},
		{"symlink escape", ParseStatusSkipped, "symlink_escapes_root", ParseReasonSymlinkEscape},
		{"failed", ParseStatusFailed, "parser panicked: index out of range", ParseReasonUnspecified},
		{"failed, text with a colon that is not a reason", ParseStatusFailed, "read: permission denied", ParseReasonUnspecified},

		// What older builds wrote, read without a migration.
		{"old partial: no definitions were found", ParseStatusPartial, "", ParseReasonLegacy},
		{"old skipped empty file", ParseStatusSkipped, "", ParseReasonEmpty},
		{"no grammar, as the pipeline words it", ParseStatusSkipped, "no grammar for this file type", ParseReasonNoGrammar},
		{"no grammar, reworded", ParseStatusSkipped, "No grammar claims files named like this", ParseReasonNoGrammar},
		{"old no tags query", ParseStatusSkipped, `no tags query available for "markdown"`, ParseReasonTextOnly},
		{"old parse cap", ParseStatusSkipped, "source is 300000 bytes, over the 262144 limit", ParseReasonTooLarge},
		{"old grammar failure", ParseStatusSkipped, `grammar "x" failed to load`, ParseReasonGrammarUnavailable},
		{"old query rejection", ParseStatusSkipped, `tags query for "x" rejected: bad node`, ParseReasonGrammarUnavailable},
		{"old timeout", ParseStatusFailed, "parse exceeded 5s: context deadline exceeded", ParseReasonUnspecified},
		{"old bare too_large", ParseStatusSkipped, "too_large", ParseReasonTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseReasonOf(tc.status, tc.text); got != tc.want {
				t.Errorf("ParseReasonOf(%q, %q) = %q, want %q", tc.status, tc.text, got, tc.want)
			}
			f := File{ParseStatus: tc.status, ParseError: tc.text}
			if f.ParseReason() != tc.want {
				t.Errorf("File.ParseReason() = %q, want %q", f.ParseReason(), tc.want)
			}
		})
	}
}

// TestFormatParseErrorRoundTrips checks that every reason this build can write
// is read back as itself, with and without detail.
func TestFormatParseErrorRoundTrips(t *testing.T) {
	statusOf := map[ParseReason]string{
		ParseReasonSyntaxErrors:       ParseStatusPartial,
		ParseReasonBudgetExceeded:     ParseStatusPartial,
		ParseReasonStoppedEarly:       ParseStatusPartial,
		ParseReasonTextOnly:           ParseStatusSkipped,
		ParseReasonGrammarUnavailable: ParseStatusSkipped,
		ParseReasonNoGrammar:          ParseStatusSkipped,
		ParseReasonTooLarge:           ParseStatusSkipped,
		ParseReasonEmpty:              ParseStatusSkipped,
		ParseReasonBinary:             ParseStatusSkipped,
		ParseReasonSymlink:            ParseStatusSkipped,
		ParseReasonSymlinkEscape:      ParseStatusSkipped,
		ParseReasonIrregular:          ParseStatusSkipped,
		ParseReasonUnreadable:         ParseStatusSkipped,
	}
	if len(statusOf) != len(knownParseReasons) {
		t.Fatalf("this test covers %d reasons, the package knows %d: add the new one here", len(statusOf), len(knownParseReasons))
	}
	for reason, status := range statusOf {
		for _, detail := range []string{"", "some detail: with a colon in it"} {
			text := FormatParseError(reason, detail)
			if got := ParseReasonOf(status, text); got != reason {
				t.Errorf("FormatParseError(%q, %q) = %q reads back as %q", reason, detail, text, got)
			}
		}
	}
	if got := FormatParseError(ParseReasonNone, "free text"); got != "free text" {
		t.Errorf("no reason must leave the detail alone, got %q", got)
	}
}

// TestOnlyAbandonedWorkDegrades is the rule for what makes a run degraded.
func TestOnlyAbandonedWorkDegrades(t *testing.T) {
	cases := []struct {
		status, text string
		abandoned    bool
		searchable   bool
	}{
		{ParseStatusOK, "", false, true},
		{ParseStatusPartial, "syntax_errors: 3 error nodes and 0 missing tokens, first at line 4", false, true},
		{ParseStatusPartial, "budget_exceeded: parser stopped at byte 1 of 2", true, true},
		{ParseStatusPartial, "stopped_early: parser stopped at byte 1 of 2", true, true},
		{ParseStatusPartial, "", false, true}, // an old row: "no definitions found"
		{ParseStatusFailed, "parser panicked: x", true, true},
		{ParseStatusSkipped, "text_only: no definition query", false, true},
		{ParseStatusSkipped, "grammar_unavailable: failed to load", false, true},
		{ParseStatusSkipped, "too_large: 5 bytes, over the 4 byte limit", false, false},
		{ParseStatusSkipped, "no_grammar: no grammar for this file type", false, false},
		{ParseStatusSkipped, "binary", false, false},
	}
	for _, tc := range cases {
		if got := ParseAbandoned(tc.status, tc.text); got != tc.abandoned {
			t.Errorf("ParseAbandoned(%q, %q) = %v, want %v", tc.status, tc.text, got, tc.abandoned)
		}
		if got := (File{ParseStatus: tc.status, ParseError: tc.text}).ParseIncomplete(); got != tc.abandoned {
			t.Errorf("File.ParseIncomplete(%q, %q) = %v, want %v", tc.status, tc.text, got, tc.abandoned)
		}
		if got := TextIndexed(tc.status, tc.text); got != tc.searchable {
			t.Errorf("TextIndexed(%q, %q) = %v, want %v", tc.status, tc.text, got, tc.searchable)
		}
		if ParseOutcomeLabel(tc.status, tc.text) == "" {
			t.Errorf("no label for (%q, %q)", tc.status, tc.text)
		}
	}
}
