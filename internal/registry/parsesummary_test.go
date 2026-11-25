package registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// summaryRows is one file of every kind the parse status distinguishes,
// including the rows an older build wrote.
func summaryRows() []model.File {
	row := func(path, lang string, size int64, status string, reason model.ParseReason, detail string) model.File {
		return model.File{
			Path: path, Language: lang, ContentHash: "h-" + path, SizeBytes: size,
			ParseStatus: status, ParseError: model.FormatParseError(reason, detail),
		}
	}
	return []model.File{
		row("a/clean.go", "go", 100, model.ParseStatusOK, model.ParseReasonNone, ""),
		row("a/doc.go", "go", 40, model.ParseStatusOK, model.ParseReasonNone, ""),
		row("b/macro.c", "c", 900, model.ParseStatusPartial, model.ParseReasonSyntaxErrors, "2 error nodes and 0 missing tokens, first at line 9"),
		row("b/stopped.c", "c", 5000, model.ParseStatusPartial, model.ParseReasonBudgetExceeded, "parser stopped at byte 10 of 5000 (line 2) on its iteration limit (30 of 30)"),
		row("b/abandoned.c", "c", 700, model.ParseStatusPartial, model.ParseReasonStoppedEarly, `parser stopped at byte 10 of 700 (line 2) on parser stop reason "accepted_prefix"`),
		row("c/broken.rb", "ruby", 300, model.ParseStatusFailed, model.ParseReasonNone, "parser panicked: index out of range"),
		row("d/report.pl", "perl", 200, model.ParseStatusSkipped, model.ParseReasonTextOnly, `no definition query for "perl"`),
		row("d/notes.md", "markdown", 250, model.ParseStatusSkipped, model.ParseReasonTextOnly, `no definition query for "markdown"`),
		row("d/README.md", "markdown", 260, model.ParseStatusSkipped, model.ParseReasonTextOnly, `no definition query for "markdown"`),
		row("e/huge.c", "c", 9_000_000, model.ParseStatusSkipped, model.ParseReasonTooLarge, "9000000 bytes, over the 2097152 byte limit (index.max_file_bytes)"),
		row("e/big.json", "json", 3_000_000, model.ParseStatusSkipped, model.ParseReasonTooLarge, "3000000 bytes, over the 2097152 byte limit (index.max_file_bytes)"),
		row("f/LICENSE", "", 1000, model.ParseStatusSkipped, model.ParseReasonNoGrammar, "no grammar for this file type"),
		row("f/logo.png", "", 5000, model.ParseStatusSkipped, model.ParseReasonBinary, ""),
		row("f/other.png", "", 5000, model.ParseStatusSkipped, model.ParseReasonBinary, ""),
		// Written by an older build: partial meant "nothing was extracted".
		{Path: "g/old_constants.go", Language: "go", ContentHash: "h-old1", SizeBytes: 60, ParseStatus: model.ParseStatusPartial},
		{Path: "g/old.md", Language: "markdown", ContentHash: "h-old2", SizeBytes: 70, ParseStatus: model.ParseStatusSkipped, ParseError: `no tags query available for "markdown"`},
		{Path: "g/old_big.c", Language: "c", ContentHash: "h-old3", SizeBytes: 300000, ParseStatus: model.ParseStatusSkipped, ParseError: "source is 300000 bytes, over the 262144 limit"},
	}
}

func writeSummaryRun(t *testing.T, r *Registry, rows []model.File) int64 {
	t.Helper()
	ctx := context.Background()
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	w, err := r.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		f := rows[i]
		if _, err := w.AddFile(ctx, &f); err != nil {
			t.Fatalf("add %s: %v", f.Path, err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return run.ID
}

func TestParseSummaryAccountsForEveryFile(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	rows := summaryRows()
	runID := writeSummaryRun(t, r, rows)

	s, err := r.ParseSummary(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}

	want := ParseSummary{
		Files: int64(len(rows)), Clean: 2, SyntaxErrors: 1, Incomplete: 2, Failed: 1,
		TextOnly: 4, TooLarge: 3, SkippedOther: 3, LegacyPartial: 1,
	}
	got := ParseSummary{
		Files: s.Files, Clean: s.Clean, SyntaxErrors: s.SyntaxErrors, Incomplete: s.Incomplete, Failed: s.Failed,
		TextOnly: s.TextOnly, TooLarge: s.TooLarge, SkippedOther: s.SkippedOther, LegacyPartial: s.LegacyPartial,
	}
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("counts\n got %+v\nwant %+v", got, want)
	}
	if sum := s.Clean + s.SyntaxErrors + s.Incomplete + s.Failed + s.TextOnly + s.TooLarge + s.SkippedOther + s.LegacyPartial; sum != s.Files {
		t.Errorf("the categories sum to %d, the run has %d files: a file is in none or in two", sum, s.Files)
	}
	if s.Abandoned() != 3 {
		t.Errorf("Abandoned() = %d, want 3 (one failed, two stopped)", s.Abandoned())
	}

	if got := fmt.Sprint(s.TextOnlyLanguages); got != "[{markdown 3} {perl 1}]" {
		t.Errorf("text-only languages = %s, want markdown 3 then perl 1", got)
	}
	if got := fmt.Sprint(s.OtherReasons); got != "[{binary 2} {no_grammar 1}]" {
		t.Errorf("other skip reasons = %s, want binary 2 then no_grammar 1", got)
	}

	var large []string
	for _, n := range s.LargestTooLarge {
		large = append(large, fmt.Sprintf("%s:%d", n.Path, n.SizeBytes))
	}
	if got := fmt.Sprint(large); got != "[e/huge.c:9000000 e/big.json:3000000 g/old_big.c:300000]" {
		t.Errorf("too-large files = %s, want largest first", got)
	}
	if len(s.IncompleteFiles) != 2 || s.IncompleteFiles[0].Path != "b/abandoned.c" || s.IncompleteFiles[1].Path != "b/stopped.c" {
		t.Errorf("incomplete files = %+v, want b/abandoned.c then b/stopped.c", s.IncompleteFiles)
	}
	if len(s.FailedFiles) != 1 || s.FailedFiles[0].Path != "c/broken.rb" || s.FailedFiles[0].Detail == "" {
		t.Errorf("failed files = %+v, want c/broken.rb with its error", s.FailedFiles)
	}
}

// TestSQLAndGoAgreeOnWhatIsAbandoned holds the two forms of one rule together:
// the SQL predicate a run's status is computed with, and the Go predicate that
// explains a single file.
func TestSQLAndGoAgreeOnWhatIsAbandoned(t *testing.T) {
	ctx := context.Background()
	r, db := newTestRegistry(t)
	rows := summaryRows()
	runID := writeSummaryRun(t, r, rows)

	for _, prefix := range []string{"", "f."} {
		res, err := db.QueryContext(ctx,
			`SELECT f.path FROM files f WHERE f.index_run_id = ? AND `+model.SQLParseAbandoned(prefix), runID)
		if err != nil {
			t.Fatal(err)
		}
		inSQL := map[string]bool{}
		for res.Next() {
			var p string
			if err := res.Scan(&p); err != nil {
				t.Fatal(err)
			}
			inSQL[p] = true
		}
		_ = res.Close()

		for _, f := range rows {
			if want := model.ParseAbandoned(f.ParseStatus, f.ParseError); inSQL[f.Path] != want {
				t.Errorf("prefix %q, %s (%s / %q): SQL says abandoned=%v, Go says %v",
					prefix, f.Path, f.ParseStatus, f.ParseError, inSQL[f.Path], want)
			}
		}
	}
}

// TestRunIsDegradedOnlyByAbandonedFiles commits runs holding different mixes of
// files and checks the status each is published with.
func TestRunIsDegradedOnlyByAbandonedFiles(t *testing.T) {
	ctx := context.Background()
	all := summaryRows()
	pick := func(paths ...string) []model.File {
		var out []model.File
		for _, f := range all {
			for _, p := range paths {
				if f.Path == p {
					out = append(out, f)
				}
			}
		}
		return out
	}

	cases := []struct {
		name     string
		rows     []model.File
		status   model.IndexStatus
		failures int64
	}{
		{"clean files only", pick("a/clean.go", "a/doc.go"), model.StatusReady, 0},
		{"syntax errors, text only, too large, skipped", pick("a/clean.go", "b/macro.c", "d/report.pl", "e/huge.c", "f/LICENSE", "f/logo.png"), model.StatusReady, 0},
		{"rows from an older build", pick("a/clean.go", "g/old_constants.go", "g/old.md", "g/old_big.c"), model.StatusReady, 0},
		{"one parse stopped on its budget", pick("a/clean.go", "b/stopped.c"), model.StatusDegraded, 1},
		{"one parser gave up early", pick("a/clean.go", "b/abandoned.c"), model.StatusDegraded, 1},
		{"one file failed", pick("a/clean.go", "c/broken.rb"), model.StatusDegraded, 1},
		{"everything", all, model.StatusDegraded, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRegistry(t)
			runID := writeSummaryRun(t, r, tc.rows)
			if err := r.CommitRun(ctx, runID); err != nil {
				t.Fatal(err)
			}
			run, err := r.Run(ctx, runID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != tc.status || run.ParseFailures != tc.failures {
				t.Errorf("status=%s parse_failures=%d, want %s and %d", run.Status, run.ParseFailures, tc.status, tc.failures)
			}
		})
	}
}

// TestRefreshRunHealthFollowsTheFiles covers the incremental case: files are
// rewritten in place after the run was published, and the run's own status has
// to follow them in both directions.
func TestRefreshRunHealthFollowsTheFiles(t *testing.T) {
	ctx := context.Background()
	r, db := newTestRegistry(t)
	runID := writeSummaryRun(t, r, summaryRows())
	if err := r.CommitRun(ctx, runID); err != nil {
		t.Fatal(err)
	}
	status := func() (model.IndexStatus, int64) {
		t.Helper()
		run, err := r.Run(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		return run.Status, run.ParseFailures
	}
	if s, n := status(); s != model.StatusDegraded || n != 3 {
		t.Fatalf("published as %s with %d failures, want degraded with 3", s, n)
	}

	// The three abandoned files now parse.
	if _, err := db.ExecContext(ctx,
		`UPDATE files SET parse_status = 'ok', parse_error = NULL
		  WHERE index_run_id = ? AND path IN ('b/stopped.c','b/abandoned.c','c/broken.rb')`, runID); err != nil {
		t.Fatal(err)
	}
	if s, _ := status(); s != model.StatusDegraded {
		t.Fatalf("the run row changed without a refresh: %s", s)
	}
	if err := r.RefreshRunHealth(ctx, runID); err != nil {
		t.Fatal(err)
	}
	if s, n := status(); s != model.StatusReady || n != 0 {
		t.Errorf("after the files were fixed: %s with %d failures, want ready with 0", s, n)
	}

	// And one stops parsing again.
	if _, err := db.ExecContext(ctx,
		`UPDATE files SET parse_status = 'failed', parse_error = 'parser panicked: x'
		  WHERE index_run_id = ? AND path = 'a/clean.go'`, runID); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshRunHealth(ctx, runID); err != nil {
		t.Fatal(err)
	}
	if s, n := status(); s != model.StatusDegraded || n != 1 {
		t.Errorf("after a file failed: %s with %d failures, want degraded with 1", s, n)
	}
}
