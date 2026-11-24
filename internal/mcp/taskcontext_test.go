package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func TestTaskContextParseMinConfidenceHelper(t *testing.T) {
	t.Run("empty or whitespace defaults to medium", func(t *testing.T) {
		for _, raw := range []string{"", "   ", "\t\n"} {
			got, err := parseTaskContextMinConfidence(raw)
			if err != nil {
				t.Fatalf("parseTaskContextMinConfidence(%q): unexpected error: %v", raw, err)
			}
			if got != model.ConfidenceMedium {
				t.Errorf("parseTaskContextMinConfidence(%q) = %q, want %q", raw, got, model.ConfidenceMedium)
			}
		}
	})

	t.Run("explicit low is preserved", func(t *testing.T) {
		for _, raw := range []string{"low", "LOW", "  low  "} {
			got, err := parseTaskContextMinConfidence(raw)
			if err != nil {
				t.Fatalf("parseTaskContextMinConfidence(%q): unexpected error: %v", raw, err)
			}
			if got != model.ConfidenceLow {
				t.Errorf("parseTaskContextMinConfidence(%q) = %q, want %q", raw, got, model.ConfidenceLow)
			}
		}
	})

	t.Run("accepts every other valid tier", func(t *testing.T) {
		cases := map[string]model.Confidence{
			"medium": model.ConfidenceMedium,
			"high":   model.ConfidenceHigh,
			"exact":  model.ConfidenceExact,
		}
		for raw, want := range cases {
			got, err := parseTaskContextMinConfidence(raw)
			if err != nil {
				t.Errorf("parseTaskContextMinConfidence(%q): unexpected error: %v", raw, err)
				continue
			}
			if got != want {
				t.Errorf("parseTaskContextMinConfidence(%q) = %q, want %q", raw, got, want)
			}
		}
	})

	t.Run("rejects unknown tier", func(t *testing.T) {
		if _, err := parseTaskContextMinConfidence("probable"); err == nil {
			t.Fatal("expected error for unknown tier, got nil")
		}
	})
}

func seedTaskContextWithTiers(ctx context.Context, reg *registry.Registry, runID int64) {
	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		panic(err)
	}
	defer w.Close()

	svcFile := model.File{Path: "pkg/service.go", Language: "go", ContentHash: "hsvc", ParseStatus: "ok"}
	svcFileID, _ := w.AddFile(ctx, &svcFile)
	svcSym := model.Symbol{
		FileID: svcFileID, Kind: model.KindClass, Name: "Service", FQN: "pkg.Service", Exported: true,
		Range:      model.Range{Start: model.Position{Line: 1}, End: model.Position{Line: 10}},
		Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
	}
	svcID, _ := w.AddSymbol(ctx, &svcSym)

	callerStrongFile := model.File{Path: "pkg/strong.go", Language: "go", ContentHash: "hstr", ParseStatus: "ok"}
	strongFileID, _ := w.AddFile(ctx, &callerStrongFile)
	strongSym := model.Symbol{
		FileID: strongFileID, Kind: model.KindFunction, Name: "StrongCaller", FQN: "pkg.StrongCaller", Exported: true,
		Range:      model.Range{Start: model.Position{Line: 1}, End: model.Position{Line: 5}},
		Confidence: model.ConfidenceExact, Source: model.SourceCompiler,
	}
	strongID, _ := w.AddSymbol(ctx, &strongSym)

	callerWeakFile := model.File{Path: "pkg/weak.go", Language: "go", ContentHash: "hweak", ParseStatus: "ok"}
	weakFileID, _ := w.AddFile(ctx, &callerWeakFile)
	weakSym := model.Symbol{
		FileID: weakFileID, Kind: model.KindFunction, Name: "WeakCaller", FQN: "pkg.WeakCaller", Exported: true,
		Range:      model.Range{Start: model.Position{Line: 1}, End: model.Position{Line: 5}},
		Confidence: model.ConfidenceLow, Source: model.SourceInferred,
	}
	weakID, _ := w.AddSymbol(ctx, &weakSym)

	edges := []model.Edge{
		{
			SrcID: strongID, DstID: svcID, Kind: model.EdgeCalls, Confidence: model.ConfidenceExact,
			Evidence: model.Evidence{FilePath: "pkg/strong.go", Range: model.Range{Start: model.Position{Line: 2}}, Source: model.SourceCompiler},
		},
		{
			SrcID: weakID, DstID: svcID, Kind: model.EdgeCalls, Confidence: model.ConfidenceLow,
			Evidence: model.Evidence{FilePath: "pkg/weak.go", Range: model.Range{Start: model.Position{Line: 2}}, Source: model.SourceInferred},
		},
	}

	_ = w.IndexFileText(ctx, svcFileID, svcFile.Path, "package pkg\ntype Service struct{}")
	_ = w.IndexFileText(ctx, strongFileID, callerStrongFile.Path, "package pkg\nfunc StrongCaller() { Service{} }")
	_ = w.IndexFileText(ctx, weakFileID, callerWeakFile.Path, "package pkg\nfunc WeakCaller() { Service{} }")
	_ = w.Flush(ctx)
	_ = w.Close()

	if _, err := reg.AddResolvedEdges(ctx, runID, edges); err != nil {
		panic(err)
	}
}

func TestTaskContextDefaultsToMediumConfidence(t *testing.T) {
	sess := newGraphTestServer(t, seedTaskContextWithTiers)

	task := "update the Service"

	// 1. Calling without min_confidence: defaults to medium, suppressing low tier.
	resDefault := resultText(callTool(t, sess, "context", map[string]any{
		"task": task,
	}))

	// 2. Calling with explicit min_confidence="medium": must produce the exact same text.
	resMedium := resultText(callTool(t, sess, "context", map[string]any{
		"task":           task,
		"min_confidence": "medium",
	}))

	if resDefault != resMedium {
		t.Errorf("omitting min_confidence differs from min_confidence='medium':\ndefault:\n%s\nmedium:\n%s",
			resDefault, resMedium)
	}

	// 3. Low tier callers should NOT be included in default medium floor.
	if strings.Contains(resDefault, "WeakCaller") {
		t.Errorf("default context should suppress low-tier WeakCaller, got:\n%s", resDefault)
	}

	// Strong caller and Service should still be present.
	if !strings.Contains(resDefault, "StrongCaller") {
		t.Errorf("default context should keep StrongCaller, got:\n%s", resDefault)
	}
	if !strings.Contains(resDefault, "Service") {
		t.Errorf("default context should keep Service, got:\n%s", resDefault)
	}

	// 4. Calling with explicit min_confidence="low": must include low tier callers.
	resLow := resultText(callTool(t, sess, "context", map[string]any{
		"task":           task,
		"min_confidence": "low",
	}))

	if !strings.Contains(resLow, "WeakCaller") {
		t.Errorf("min_confidence='low' should include low-tier WeakCaller, got:\n%s", resLow)
	}

	// 5. Default medium response discloses omitted lower-confidence results.
	if !strings.Contains(resDefault, "lower-confidence result(s) were omitted") {
		t.Errorf("default context should report omitted low-confidence results, got:\n%s", resDefault)
	}
}

func TestTaskContextInvalidMinConfidence(t *testing.T) {
	sess := newGraphTestServer(t, seedMixedConfidence)
	res := resultText(callTool(t, sess, "context", map[string]any{
		"task":           "change the login flow",
		"min_confidence": "bogus",
	}))
	if !strings.Contains(res, "unknown confidence") {
		t.Errorf("expected 'unknown confidence' error message, got:\n%s", res)
	}
}
