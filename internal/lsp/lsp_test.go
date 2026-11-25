package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/indexer"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

func TestFramingRoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	var buf bytes.Buffer

	if err := WriteMessage(&buf, payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	reader := bufio.NewReader(&buf)
	got, err := ReadMessage(reader)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if !bytes.Equal(got, payload) {
		t.Fatalf("got %s, want %s", got, payload)
	}
}

func TestParseLocations(t *testing.T) {
	t.Parallel()

	// 1. Single Location
	singleJSON := json.RawMessage(`{"uri":"file:///a/b.go","range":{"start":{"line":10,"character":5},"end":{"line":10,"character":10}}}`)
	locs, err := ParseLocations(singleJSON)
	if err != nil {
		t.Fatalf("Parse single: %v", err)
	}
	if len(locs) != 1 || locs[0].Range.Start.Line != 10 {
		t.Fatalf("unexpected single: %+v", locs)
	}

	// 2. Multiple Locations
	multiJSON := json.RawMessage(`[{"uri":"file:///a/b.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":5}}}]`)
	locs, err = ParseLocations(multiJSON)
	if err != nil {
		t.Fatalf("Parse multi: %v", err)
	}
	if len(locs) != 1 || locs[0].Range.Start.Line != 1 {
		t.Fatalf("unexpected multi: %+v", locs)
	}

	// 3. LocationLink (LSP 3.14+)
	linkJSON := json.RawMessage(`[{
		"targetUri":"file:///a/b.go",
		"targetRange":{"start":{"line":20,"character":0},"end":{"line":25,"character":0}},
		"targetSelectionRange":{"start":{"line":21,"character":4},"end":{"line":21,"character":12}}
	}]`)
	locs, err = ParseLocations(linkJSON)
	if err != nil {
		t.Fatalf("Parse link: %v", err)
	}
	if len(locs) != 1 || locs[0].Range.Start.Line != 21 || locs[0].Range.Start.Character != 4 {
		t.Fatalf("unexpected link: %+v", locs)
	}
}

// runMockServer simulates an LSP server over r/w pipes.
func runMockServer(r io.Reader, w io.Writer) {
	br := bufio.NewReader(r)
	for {
		msg, err := ReadMessage(br)
		if err != nil {
			return
		}

		var req Request
		if err := json.Unmarshal(msg, &req); err != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			resp := Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{"capabilities":{"definitionProvider":true}}`),
			}
			data, _ := json.Marshal(resp)
			_ = WriteMessage(w, data)

		case "textDocument/definition":
			resp := Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`[{"uri":"file:///workspace/lib.cpp","range":{"start":{"line":15,"character":4},"end":{"line":15,"character":10}}}]`),
			}
			data, _ := json.Marshal(resp)
			_ = WriteMessage(w, data)

		case "shutdown":
			resp := Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`null`),
			}
			data, _ := json.Marshal(resp)
			_ = WriteMessage(w, data)

		case "exit":
			return
		}
	}
}

func TestMockLSPClientSession(t *testing.T) {
	t.Parallel()

	// Client write -> Server read
	c2sR, c2sW := io.Pipe()
	// Server write -> Client read
	s2cR, s2cW := io.Pipe()

	go runMockServer(c2sR, s2cW)

	client := &Client{
		stdin:      c2sW,
		serverName: "mock-lsp",
		pending:    make(map[int64]chan *Response),
		done:       make(chan struct{}),
	}
	go client.readLoop(bufio.NewReader(s2cR))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Initialize
	initRes, err := client.Initialize(ctx, "/workspace")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initRes == nil {
		t.Fatal("nil initialize result")
	}

	// 2. Definition
	locs, err := client.Definition(ctx, "/workspace/main.cpp", 10, 5)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
	if locs[0].Range.Start.Line != 15 {
		t.Fatalf("expected line 15, got %d", locs[0].Range.Start.Line)
	}

	// 3. Close
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestProbeServerKnownLanguages(t *testing.T) {
	langs := []indexer.Language{
		indexer.LangCPP,
		indexer.LangRust,
		indexer.LangGo,
		indexer.LangPython,
		indexer.LangJava,
		indexer.LangCSharp,
		indexer.LangTypeScript,
	}

	for _, l := range langs {
		spec, path, found := ProbeServer(l)
		if found {
			if spec.Binary == "" || path == "" {
				t.Errorf("language %s reported found but empty path: %+v", l, spec)
			}
		}
	}
}

func TestFilePathURIConversions(t *testing.T) {
	t.Parallel()

	tmp := filepath.Join(os.TempDir(), "test_file.rs")
	uri := FilePathToURI(tmp)
	if !bytes.HasPrefix([]byte(uri), []byte("file:///")) {
		t.Fatalf("expected file:/// prefix, got %s", uri)
	}

	back := URIToFilePath(uri)
	cleanTmp := filepath.Clean(tmp)
	cleanBack := filepath.Clean(back)
	if cleanTmp != cleanBack {
		t.Fatalf("got %s, want %s", cleanBack, cleanTmp)
	}
}

func TestRunLSPBridgeNoSupportedFiles(t *testing.T) {
	ctx := context.Background()
	reg, cleanup := setupTestRegistry(t)
	defer cleanup()

	runID := createTestRun(t, reg)

	out := RunLSPBridge(ctx, reg, runID, BridgeOptions{Root: os.TempDir()})
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if out.Note == "" && len(out.Steps) == 0 {
		t.Fatalf("expected note or steps, got %+v", out)
	}
}

func TestLSPBridgeUpgradeEdge(t *testing.T) {
	ctx := context.Background()
	reg, cleanup := setupTestRegistry(t)
	defer cleanup()

	run, err := reg.BeginRun(ctx, "auto", "rev1")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	runID := run.ID

	// Add files and symbols
	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	fileID, err := w.AddFile(ctx, &model.File{
		Path:        "src/main.cpp",
		Language:    "cpp",
		ContentHash: "hash-main",
		SizeBytes:   100,
		ModTimeUnix: time.Now().UnixNano(),
		ParseStatus: "ok",
	})
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	srcSymID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     fileID,
		Kind:       model.KindFunction,
		Name:       "main",
		FQN:        "src/main.cpp#main",
		Range:      model.Range{Start: model.Position{Line: 5, Col: 0}, End: model.Position{Line: 12, Col: 0}},
		Exported:   true,
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
	})
	if err != nil {
		t.Fatalf("AddSymbol: %v", err)
	}

	libFileID, err := w.AddFile(ctx, &model.File{
		Path:        "src/lib.cpp",
		Language:    "cpp",
		ContentHash: "hash-lib",
		SizeBytes:   100,
		ModTimeUnix: time.Now().UnixNano(),
		ParseStatus: "ok",
	})
	if err != nil {
		t.Fatalf("AddFile lib: %v", err)
	}

	dstSymID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     libFileID,
		Kind:       model.KindFunction,
		Name:       "compute",
		FQN:        "src/lib.cpp#compute",
		Range:      model.Range{Start: model.Position{Line: 20, Col: 0}, End: model.Position{Line: 25, Col: 0}},
		Exported:   true,
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
	})
	if err != nil {
		t.Fatalf("AddSymbol compute: %v", err)
	}

	// Add heuristic edge
	_, err = w.AddEdge(ctx, &model.Edge{
		SrcID:      srcSymID,
		DstID:      dstSymID,
		Kind:       model.EdgeCalls,
		Confidence: model.ConfidenceHigh,
		Evidence: model.Evidence{
			FilePath: "src/main.cpp",
			Range:    model.Range{Start: model.Position{Line: 8, Col: 4}},
			Source:   model.SourceSourceCode,
		},
	})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	_ = w.Close()

	if err := reg.CommitRun(ctx, runID); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	// Verify initial edge has ConfidenceHigh
	edges, err := reg.Edges(ctx, runID)
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	if len(edges) != 1 || edges[0].Confidence != model.ConfidenceHigh {
		t.Fatalf("expected 1 edge at ConfidenceHigh, got %+v", edges)
	}

	// Now upgrade edge via reg.UpgradeEdges with compiler exact fact
	exactEdge := model.Edge{
		SrcID:      srcSymID,
		DstID:      dstSymID,
		Kind:       model.EdgeCalls,
		Confidence: model.ConfidenceExact,
		Evidence: model.Evidence{
			FilePath: "src/main.cpp",
			Range:    model.Range{Start: model.Position{Line: 8, Col: 4}},
			Source:   model.SourceCompiler,
			Detail:   "lsp: clangd",
		},
	}

	st, err := reg.UpgradeEdges(ctx, runID, []model.Edge{exactEdge})
	if err != nil {
		t.Fatalf("UpgradeEdges: %v", err)
	}
	if st.Upgraded != 1 {
		t.Fatalf("expected 1 upgraded, got %+v", st)
	}

	// Verify upgraded edge
	upgradedEdges, err := reg.Edges(ctx, runID)
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	if len(upgradedEdges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(upgradedEdges))
	}
	if upgradedEdges[0].Confidence != model.ConfidenceExact {
		t.Errorf("got confidence %v, want exact", upgradedEdges[0].Confidence)
	}
	if upgradedEdges[0].Evidence.Source != model.SourceCompiler {
		t.Errorf("got source %v, want compiler", upgradedEdges[0].Evidence.Source)
	}
	if upgradedEdges[0].Evidence.Detail != "lsp: clangd" {
		t.Errorf("got detail %v, want lsp: clangd", upgradedEdges[0].Evidence.Detail)
	}
}

func setupTestRegistry(t *testing.T) (*registry.Registry, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	reg := registry.New(db)
	return reg, func() { _ = db.Close() }
}

func createTestRun(t *testing.T, reg *registry.Registry) int64 {
	t.Helper()
	ctx := context.Background()
	run, err := reg.BeginRun(ctx, "auto", "rev1")
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}
	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	_ = w.Close()
	if err := reg.CommitRun(ctx, run.ID); err != nil {
		t.Fatalf("commit run: %v", err)
	}
	return run.ID
}

func TestLSPRequiresAllowRepoBuild(t *testing.T) {
	ctx := context.Background()
	reg, cleanup := setupTestRegistry(t)
	defer cleanup()

	run, err := reg.BeginRun(ctx, "auto", "rev1")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}

	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	_, _ = w.AddFile(ctx, &model.File{
		Path:        "main.cpp",
		Language:    "cpp",
		ContentHash: "hash-main",
		ParseStatus: "ok",
	})
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	_ = w.Close()
	if err := reg.CommitRun(ctx, run.ID); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	// Without AllowRepoBuild, cpp should be skipped
	out := RunLSPBridge(ctx, reg, run.ID, BridgeOptions{
		Root:           t.TempDir(),
		AllowRepoBuild: false,
	})
	if len(out.Steps) == 0 {
		t.Fatalf("expected step for cpp, got none")
	}
	if out.Steps[0].Ran {
		t.Errorf("step should not run when AllowRepoBuild=false")
	}
	if out.Steps[0].SkippedReason != "requires --allow-repo-build" {
		t.Errorf("expected skipped reason 'requires --allow-repo-build', got: %s", out.Steps[0].SkippedReason)
	}
}
