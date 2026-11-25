package registry

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// TestDetectedBySurvivesBothWritePaths: how a file's language was decided is
// written by the full-index writer and by the in-place replace that
// incremental indexing uses, and read back by Files. Two write paths existing
// is the hazard: one prepared statement serves both, and a column added to it
// with only one caller updated fails at run time, not at compile time.
func TestDetectedBySurvivesBothWritePaths(t *testing.T) {
	ctx := context.Background()
	r, runID := seedCommitted(t)

	byPath := func() map[string]model.File {
		t.Helper()
		files, err := r.Files(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]model.File{}
		for _, f := range files {
			out[f.Path] = f
		}
		return out
	}

	// seedCommitted writes a file with no DetectedBy: the column's default.
	if got := byPath()["pkg/a.go"].DetectedBy; got != "" {
		t.Errorf("a file written without a detection method reads back %q, want empty", got)
	}

	// The in-place path.
	replaced := model.File{Path: "pkg/a.go", Language: "objc", ContentHash: "h2", DetectedBy: "content"}
	if err := r.ReplaceFile(ctx, runID, &replaced, nil, nil, AllowCommittedRun()); err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}
	added := model.File{Path: "notes/x.m", Language: "matlab", ContentHash: "h3", DetectedBy: "extension_ambiguous"}
	if err := r.ReplaceFile(ctx, runID, &added, nil, nil, AllowCommittedRun()); err != nil {
		t.Fatalf("ReplaceFile (new file): %v", err)
	}

	files := byPath()
	if got := files["pkg/a.go"]; got.DetectedBy != "content" || got.Language != "objc" {
		t.Errorf("pkg/a.go after replace: language %q by %q, want objc by content", got.Language, got.DetectedBy)
	}
	if got := files["notes/x.m"].DetectedBy; got != "extension_ambiguous" {
		t.Errorf("notes/x.m: detected_by %q, want extension_ambiguous", got)
	}

	// The full-index writer, on a fresh run.
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	w, err := r.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := model.File{Path: "bin/deploy", Language: "bash", ContentHash: "h4", DetectedBy: "shebang"}
	if _, err := w.AddFile(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	written, err := r.Files(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].DetectedBy != "shebang" {
		t.Errorf("writer path: %+v, want one file detected by shebang", written)
	}
}
