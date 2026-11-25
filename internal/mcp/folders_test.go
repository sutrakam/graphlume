package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// seedFolders builds a small workspace:
//
//	svc/api/       go.mod, handler.go (Handle), handler_test.go (TestHandle)
//	svc/api/core/  core.go (Run)
//	lib/           lib.go (Util, Helper)
//	web/           app.ts (render)
//
// Handle -> Run (exact), Handle -> Util (exact), Run -> Util (medium),
// render -> Handle (low), TestHandle -> Handle (exact).
func seedFolders(ctx context.Context, reg *registry.Registry, runID int64) {
	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		panic(err)
	}
	file := func(p, lang string) int64 {
		f := model.File{Path: p, Language: lang, ContentHash: "h" + p, ParseStatus: "ok", SizeBytes: 10}
		id, _ := w.AddFile(ctx, &f)
		return id
	}
	mod := file("svc/api/go.mod", "gomod")
	handler := file("svc/api/handler.go", "go")
	htest := file("svc/api/handler_test.go", "go")
	core := file("svc/api/core/core.go", "go")
	lib := file("lib/lib.go", "go")
	web := file("web/app.ts", "typescript")
	_ = mod
	sym := func(fid int64, name string, line int) int64 {
		s := model.Symbol{
			FileID: fid, Kind: model.KindFunction, Name: name, FQN: name, Exported: true,
			Range:      model.Range{Start: model.Position{Line: line}, End: model.Position{Line: line + 3}},
			Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
		}
		id, _ := w.AddSymbol(ctx, &s)
		return id
	}
	handle := sym(handler, "Handle", 1)
	testHandle := sym(htest, "TestHandle", 1)
	run := sym(core, "Run", 1)
	util := sym(lib, "Util", 1)
	sym(lib, "Helper", 10)
	render := sym(web, "render", 1)
	_ = w.Flush(ctx)
	_ = w.Close()

	edge := func(src, dst int64, file string, conf model.Confidence) model.Edge {
		return model.Edge{SrcID: src, DstID: dst, Kind: model.EdgeCalls, Confidence: conf,
			Evidence: model.Evidence{FilePath: file, Range: model.Range{Start: model.Position{Line: 2}},
				Source: srcFor(conf), Detail: "test"}}
	}
	if _, err := reg.AddResolvedEdges(ctx, runID, []model.Edge{
		edge(handle, run, "svc/api/handler.go", model.ConfidenceExact),
		edge(handle, util, "svc/api/handler.go", model.ConfidenceExact),
		edge(run, util, "svc/api/core/core.go", model.ConfidenceMedium),
		edge(render, handle, "web/app.ts", model.ConfidenceLow),
		edge(testHandle, handle, "svc/api/handler_test.go", model.ConfidenceExact),
	}); err != nil {
		panic(err)
	}
}

// srcFor is the evidence source an edge of the tier may carry: only a compiler
// may claim exact.
func srcFor(c model.Confidence) model.SourceType {
	if c == model.ConfidenceExact {
		return model.SourceCompiler
	}
	return model.SourceSourceCode
}

func structureCall(t *testing.T, fs fullServer, args map[string]any) (folderReport, string, bool) {
	t.Helper()
	res := callTool(t, fs.sess, "structure", args)
	var rep folderReport
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &rep); err != nil {
			t.Fatalf("structured answer does not decode: %v\n%s", err, raw)
		}
	}
	return rep, resultText(res), res.IsError
}

func folderByPath(rep folderReport, p string) *folderRow {
	for i := range rep.Folders {
		if rep.Folders[i].Path == p {
			return &rep.Folders[i]
		}
	}
	return nil
}

func TestStructureRollsFilesUpToFolders(t *testing.T) {
	fs := newFullServer(t, seedFolders)
	rep, text, isErr := structureCall(t, fs, map[string]any{"owners": false})
	if isErr {
		t.Fatalf("structure failed:\n%s", text)
	}
	if rep.Root != "." {
		t.Errorf("root = %q, want .", rep.Root)
	}
	svc := folderByPath(rep, "svc")
	if svc == nil {
		t.Fatalf("no svc folder: %+v", rep.Folders)
	}
	if svc.Files != 4 || svc.Symbols != 3 {
		t.Errorf("svc holds %d files and %d symbols; want 4 and 3 (subfolders folded in)", svc.Files, svc.Symbols)
	}
	if svc.CallsOut != 2 {
		// Handle->Util and Run->Util leave svc. The test file's call stays inside it.
		t.Errorf("svc calls_out = %d, want 2", svc.CallsOut)
	}
	if svc.CallsIn != 1 {
		t.Errorf("svc calls_in = %d, want 1 (the web call)", svc.CallsIn)
	}
	if svc.Language != "go" {
		t.Errorf("svc language = %q", svc.Language)
	}
	lib := folderByPath(rep, "lib")
	if lib == nil || lib.CallsIn != 2 || lib.CallsOut != 0 {
		t.Errorf("lib = %+v, want 2 in and 0 out", lib)
	}

	var svcLib *folderEdge
	for i, e := range rep.Edges {
		if e.From == "svc" && e.To == "lib" {
			svcLib = &rep.Edges[i]
		}
		if e.From == e.To {
			t.Errorf("an edge from %q to itself: %+v", e.From, e)
		}
	}
	if svcLib == nil || svcLib.Calls != 2 || svcLib.Exact != 1 || svcLib.Medium != 1 {
		t.Errorf("svc -> lib = %+v, want 2 calls, 1 exact, 1 medium", svcLib)
	}
	for _, want := range []string{"| folder |", "svc", "lib", "Strongest relationships"} {
		if !strings.Contains(text, want) {
			t.Errorf("text is missing %q:\n%s", want, text)
		}
	}
	if rep.IndexRevision == 0 || rep.Provenance == nil {
		t.Errorf("the answer carries no index revision or provenance: %+v", rep)
	}
}

func TestStructureMarksModulesAndTestedShare(t *testing.T) {
	fs := newFullServer(t, seedFolders)
	rep, _, _ := structureCall(t, fs, map[string]any{"path": "svc", "depth": 2, "owners": false})
	api := folderByPath(rep, "svc/api")
	if api == nil {
		t.Fatalf("no svc/api: %+v", rep.Folders)
	}
	if api.Kind != "module" {
		t.Errorf("svc/api holds a go.mod and is %q, not a module", api.Kind)
	}
	core := folderByPath(rep, "svc/api/core")
	if core == nil || core.Kind != "folder" {
		t.Errorf("svc/api/core = %+v, want a plain folder", core)
	}
	// handler.go is reached by handler_test.go; the test file itself is not
	// counted as code to be tested. core.go is not reached by any test.
	if api.TestedPct == nil || *api.TestedPct != 100 {
		t.Errorf("svc/api tested_pct = %v, want 100", api.TestedPct)
	}
	if core.TestedPct == nil || *core.TestedPct != 0 {
		t.Errorf("svc/api/core tested_pct = %v, want 0", core.TestedPct)
	}
	if rep.Root != "svc" {
		t.Errorf("root = %q", rep.Root)
	}
	// Run is called from svc/api, which is another folder at this depth.
	if core.CallsIn != 1 {
		t.Errorf("core calls_in = %d, want 1", core.CallsIn)
	}
}

func TestStructureHonoursTheConfidenceFloor(t *testing.T) {
	fs := newFullServer(t, seedFolders)
	rep, text, _ := structureCall(t, fs, map[string]any{"min_confidence": "exact", "owners": false})
	for _, e := range rep.Edges {
		if e.High+e.Medium+e.Low != 0 {
			t.Errorf("edge %+v passed an exact floor", e)
		}
	}
	web := folderByPath(rep, "web")
	if web == nil || web.CallsOut != 0 {
		t.Errorf("web = %+v: its only call is low confidence", web)
	}
	if !strings.Contains(text, `below confidence "exact"`) {
		t.Errorf("the floor is not stated:\n%s", text)
	}
}

func TestStructureWithoutSubfoldersCountsDirectFilesOnly(t *testing.T) {
	fs := newFullServer(t, seedFolders)
	rep, _, _ := structureCall(t, fs, map[string]any{"path": "svc", "depth": 2, "include_subfolders": false, "owners": false})
	api := folderByPath(rep, "svc/api")
	if api == nil || api.Files != 3 {
		t.Errorf("svc/api = %+v, want its 3 own files", api)
	}
	// depth 1 and no subfolders: svc itself has no files of its own.
	rep, _, _ = structureCall(t, fs, map[string]any{"include_subfolders": false, "owners": false})
	if svc := folderByPath(rep, "svc"); svc != nil {
		t.Errorf("svc has no files of its own but is listed: %+v", svc)
	}
	if lib := folderByPath(rep, "lib"); lib == nil || lib.Files != 1 {
		t.Errorf("lib = %+v", lib)
	}
}

func TestStructurePagesAndSaysSo(t *testing.T) {
	fs := newFullServer(t, seedFolders)
	first, text, _ := structureCall(t, fs, map[string]any{"limit": 1, "owners": false})
	if len(first.Folders) != 1 || !first.Truncated || first.NextOffset == nil || first.Total != 3 {
		t.Fatalf("page 1 = %+v", first)
	}
	if !strings.Contains(text, "next page at offset") {
		t.Errorf("the text does not say there is more:\n%s", text)
	}
	second, _, _ := structureCall(t, fs, map[string]any{"limit": 5, "offset": *first.NextOffset, "owners": false})
	if len(second.Folders) != 2 || second.Truncated {
		t.Errorf("page 2 = %+v", second)
	}
	if second.Folders[0].Path == first.Folders[0].Path {
		t.Error("the second page repeats the first")
	}
}

func TestStructureRejectsBadArguments(t *testing.T) {
	fs := newFullServer(t, seedFolders)
	for name, args := range map[string]map[string]any{
		"negative depth":  {"depth": -1},
		"negative limit":  {"limit": -2},
		"bad floor":       {"min_confidence": "hgih"},
		"path above root": {"path": "../etc"},
	} {
		if _, _, isErr := structureCall(t, fs, args); !isErr {
			t.Errorf("%s: want an error result", name)
		}
	}
	// An unknown folder is an answer, not a failure.
	rep, text, isErr := structureCall(t, fs, map[string]any{"path": "nope"})
	if isErr || len(rep.Folders) != 0 || !strings.Contains(text, "Nothing is indexed") {
		t.Errorf("unknown folder: err=%v folders=%d\n%s", isErr, len(rep.Folders), text)
	}
}

func TestStructureRiskOrdersBusyUntestedFoldersFirst(t *testing.T) {
	maxIn := int64(100)
	zero, full := 0, 100
	busyUntested := folderRisk(folderRow{Symbols: 10, CallsIn: 100, TestedPct: &zero}, maxIn)
	busyTested := folderRisk(folderRow{Symbols: 10, CallsIn: 100, TestedPct: &full}, maxIn)
	quietTested := folderRisk(folderRow{Symbols: 10, CallsIn: 1, TestedPct: &full}, maxIn)
	empty := folderRisk(folderRow{CallsIn: 100}, maxIn)
	if busyUntested != "high" || busyTested == "high" || quietTested != "low" || empty != "low" {
		t.Errorf("risk grades: busy untested %s, busy tested %s, quiet tested %s, no definitions %s",
			busyUntested, busyTested, quietTested, empty)
	}
}

func TestCleanFolderNormalisesPaths(t *testing.T) {
	for in, want := range map[string]string{"": "", ".": "", "./": "", "/": "", "svc/": "svc", "./svc/api/": "svc/api", `svc\api`: "svc/api"} {
		if got, ok := cleanFolder(in); !ok || got != want {
			t.Errorf("cleanFolder(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := cleanFolder("../x"); ok {
		t.Error("a path above the root was accepted")
	}
}
