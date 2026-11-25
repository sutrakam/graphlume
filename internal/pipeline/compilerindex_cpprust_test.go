package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	scippb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/indexer"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

// These tests run the C/C++ and Rust paths end to end with STAND-INS: small
// shell scripts, written into the test's temp directory and put first on PATH,
// that write a hand-made .scip file where the real tool would. Nothing here
// runs scip-clang or rust-analyzer. What they establish is that GraphLume
// plans, runs under its trust rules, caches, imports and proves from an index
// of the shape these tools are documented to write; whether the real tools
// write that shape is not something this file can show. The symbol strings are
// written from the SCIP grammar and the tools' documented conventions
// (`cxx . . $ ...` for scip-clang, `rust-analyzer cargo <crate> <version>
// impl#[Type]method().` for rust-analyzer) and are NOT copied from a real run.

func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in indexers are shell scripts")
	}
}

// standIn writes an executable named name into dir. banner is its --version
// answer; copyFlag says how it is told where to write the index: either a
// "--flag=" prefix or, when it starts with a space, a flag whose value is the
// next argument. It logs "<cwd> <args>" to log on every indexing run, then
// copies fixture to the output path.
func standIn(t *testing.T, dir, name, banner, outFlag, fixture, log string) {
	t.Helper()
	var find string
	if strings.HasPrefix(outFlag, " ") {
		find = `prev=""; for a in "$@"; do if [ "$prev" = "` + strings.TrimSpace(outFlag) + `" ]; then out="$a"; fi; prev="$a"; done`
	} else {
		find = `for a in "$@"; do case "$a" in ` + outFlag + `*) out="${a#` + outFlag + `}";; esac; done`
	}
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo "` + banner + `"; exit 0; fi` + "\n" +
		`echo "$PWD $*" >> "` + log + `"` + "\n" +
		`out=""` + "\n" + find + "\n" +
		`/bin/cp "` + fixture + `" "$out"` + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil { //nolint:gosec // G306: a test executable
		t.Fatal(err)
	}
}

// onlyPath makes dir the whole PATH for the test: the real machine may have
// either tool installed (this one has a rustup proxy for rust-analyzer), and a
// test about a tool being absent must not find it.
func onlyPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func writeScip(t *testing.T, path string, tool string, docs ...*scippb.Document) {
	t.Helper()
	raw, err := proto.Marshal(&scippb.Index{
		Metadata:  &scippb.Metadata{ProjectRoot: "file:///build/repo", ToolInfo: &scippb.ToolInfo{Name: tool, Version: "0.0.1"}},
		Documents: docs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func defOcc(sym string, line, col, endCol, bodyStart, bodyEnd int32) *scippb.Occurrence {
	o := &scippb.Occurrence{Symbol: sym, SymbolRoles: int32(scippb.SymbolRole_Definition)}
	o.SetSourceRange(scippb.Range{Start: scippb.Position{Line: line, Character: col}, End: scippb.Position{Line: line, Character: endCol}})
	o.SetEnclosingSourceRange(scippb.Range{Start: scippb.Position{Line: bodyStart}, End: scippb.Position{Line: bodyEnd, Character: 1}})
	return o
}

func refOcc(sym string, line, col, endCol int32) *scippb.Occurrence {
	o := &scippb.Occurrence{Symbol: sym}
	o.SetSourceRange(scippb.Range{Start: scippb.Position{Line: line, Character: col}, End: scippb.Position{Line: line, Character: endCol}})
	return o
}

// nativeRepo writes files on disk and indexes into the registry the symbols and
// references a parser would have recorded for the one source file of the
// project: a type with two methods and a function that calls both, one of them
// a name the standard library also has.
type nativeFixture struct {
	root  string
	reg   *registry.Registry
	runID int64
	db    *store.DB
	ids   map[string]int64
}

func nativeRepo(t *testing.T, srcPath, lang string, onDisk map[string]string, extraFiles ...model.File) nativeFixture {
	t.Helper()
	root := t.TempDir()
	for p, content := range onDisk {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil { //nolint:gosec // G306: test fixture
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := registry.New(db)
	run, err := reg.BeginRun(ctx, "auto", "cafe")
	if err != nil {
		t.Fatal(err)
	}
	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	fid, err := w.AddFile(ctx, &model.File{Path: srcPath, Language: lang, ContentHash: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range extraFiles {
		if _, err := w.AddFile(ctx, &f); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]int64{}
	// Lines are 1-indexed, as the parser records them.
	for _, s := range []struct {
		name       string
		kind       model.EntityKind
		start, end int
	}{
		{"Circle", model.KindClass, 1, 4},
		{"size", model.KindMethod, 2, 2},
		{"area", model.KindMethod, 3, 3},
		{"total", model.KindFunction, 5, 9},
	} {
		id, err := w.AddSymbol(ctx, &model.Symbol{
			FileID: fid, Kind: s.kind, Name: s.name, FQN: srcPath + ":" + s.name,
			Range:      model.Range{Start: model.Position{Line: s.start}, End: model.Position{Line: s.end}},
			Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[s.name] = id
	}
	for _, r := range []struct {
		name string
		line int
	}{{"size", 7}, {"area", 8}} {
		if err := w.AddReference(ctx, fid, registry.Reference{
			Name: r.name, Kind: "reference.call", Line: r.line, Col: 4, ArgCount: model.ArityUnknown,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reg.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE symbol_references SET resolution = 'unresolved'`); err != nil {
		t.Fatal(err)
	}
	return nativeFixture{root: root, reg: reg, runID: run.ID, db: db, ids: ids}
}

func cppDocument() *scippb.Document {
	const (
		circle = "cxx . . $ geo/Circle#"
		size   = "cxx . . $ geo/Circle#size(a1b2c3)."
		area   = "cxx . . $ geo/Circle#area(d4e5f6)."
		total  = "cxx . . $ geo/total(0a1b2c)."
		// The standard library's symbol in the same unnamed package.
		stdSize = "cxx . . $ std/vector#size(9f8e7d)."
	)
	return &scippb.Document{
		RelativePath: "src/geo.cpp", Language: "cpp",
		PositionEncoding: scippb.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Symbols: []*scippb.SymbolInformation{
			{Symbol: circle, Kind: scippb.SymbolInformation_Struct, DisplayName: "Circle"},
			{Symbol: size, Kind: scippb.SymbolInformation_Method, DisplayName: "size"},
			{Symbol: area, Kind: scippb.SymbolInformation_Method, DisplayName: "area"},
			{Symbol: total, Kind: scippb.SymbolInformation_Function, DisplayName: "total"},
		},
		Occurrences: []*scippb.Occurrence{
			defOcc(circle, 0, 7, 13, 0, 3),
			defOcc(size, 1, 6, 10, 1, 1),
			defOcc(area, 2, 9, 13, 2, 2),
			defOcc(total, 4, 7, 12, 4, 8),
			refOcc(stdSize, 6, 4, 8), // v.size(): the standard library's, not Circle::size
			refOcc(area, 7, 11, 15),  // c.area()
		},
	}
}

const cppSource = "struct Circle {\n  int size() { return 1; }\n  double area() { return 3.0; }\n};\n" +
	"double total(std::vector<int>& v) {\n  Circle c;\n  v.size();\n  return c.area();\n}\n"

func allowAll() config.Config {
	cfg := autoConfig()
	return cfg
}

func countExactEdge(t *testing.T, f nativeFixture, src, dst int64) int {
	t.Helper()
	var n int
	if err := f.db.QueryRowContext(context.Background(), `
		SELECT count(*) FROM edges WHERE index_run_id = ? AND src_id = ? AND dst_id = ?
		   AND confidence = 'exact' AND source = 'compiler'`, f.runID, src, dst).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCPPStandInIsPlannedRunCachedAndImported(t *testing.T) {
	skipWithoutShell(t)
	ctx := context.Background()
	f := nativeRepo(t, "src/geo.cpp", "cpp", map[string]string{
		"compile_commands.json": `[{"directory":"/build/repo","command":"c++ -c src/geo.cpp","file":"src/geo.cpp"}]`,
		"src/geo.cpp":           cppSource,
	})
	bins, scratch := t.TempDir(), t.TempDir()
	fixture, logFile := filepath.Join(scratch, "cpp.scip"), filepath.Join(scratch, "runs.log")
	writeScip(t, fixture, "scip-clang", cppDocument())
	standIn(t, bins, "scip-clang", "scip-clang 0.3.2", "--index-output-path=", fixture, logFile)
	onlyPath(t, bins)

	// Without the opt-in the installed stand-in is not run: the compilers and
	// flags in compile_commands.json are the repository's to choose.
	blocked := RunCompilerIndexers(ctx, f.reg, f.runID, CompilerIndexOptions{Root: f.root, Config: allowAll()})
	if len(blocked.Steps) != 1 || !strings.Contains(blocked.Steps[0].Skipped, "indexing C/C++ runs the compilers and flags named in the repository's compile_commands.json, which executes code the repository chooses; re-run with --allow-repo-build") {
		t.Fatalf("without the opt-in: steps = %+v, want one blocked with the opt-in reason", blocked.Steps)
	}
	if _, err := os.Stat(logFile); err == nil {
		t.Fatal("scip-clang ran without --allow-repo-build")
	}

	opts := CompilerIndexOptions{Root: f.root, Config: allowAll(), AllowRepoBuild: true}
	out := RunCompilerIndexers(ctx, f.reg, f.runID, opts)
	if out.Err != nil {
		t.Fatalf("pass: %v", out.Err)
	}
	if len(out.Steps) != 1 {
		t.Fatalf("steps = %+v, want the one C/C++ project", out.Steps)
	}
	s := out.Steps[0]
	if s.Language != "cpp" || s.Indexer != "scip-clang" || !s.Ran || s.Skipped != "" || s.Err != nil {
		t.Fatalf("step = %+v, want scip-clang to have run and imported", s)
	}
	if s.Version != "scip-clang 0.3.2" {
		t.Errorf("version %q: want what the tool reports about itself", s.Version)
	}

	// How it was invoked: from the project directory, with the compilation
	// database named by its fixed name, and writing under .graphlume/scip.
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, _ := filepath.EvalSymlinks(f.root)
	want := resolvedRoot + " --compdb-path=compile_commands.json --index-output-path=" +
		filepath.Join(f.root, ".graphlume", "scip", "cpp.scip")
	if got := strings.TrimSpace(string(log)); !strings.HasSuffix(got, strings.TrimPrefix(want, resolvedRoot)) ||
		!strings.HasPrefix(got, resolvedRoot) {
		t.Errorf("invocation = %q, want %q", got, want)
	}

	// Coverage, the edge the compiler proved, and the reference it ruled out.
	if out.Proof.Files != 1 || s.Import.Ingest.DocumentsMatched != 1 {
		t.Errorf("proof %+v, import %+v: want the one file covered", out.Proof, s.Import.Ingest)
	}
	if s.Import.SymbolsMatched != 4 {
		t.Errorf("matched %d symbols, want 4 (every definition in the index)", s.Import.SymbolsMatched)
	}
	if countExactEdge(t, f, f.ids["total"], f.ids["area"]) != 1 {
		t.Error("no exact, compiler-sourced edge from total to Circle::area")
	}
	if countExactEdge(t, f, f.ids["total"], f.ids["size"]) != 0 {
		t.Error("an edge to Circle::size was written for a call the compiler resolved to std::vector::size")
	}
	if out.Proof.RuledOut != 1 {
		t.Errorf("ruled out %d references, want the one v.size() call", out.Proof.RuledOut)
	}
	b, err := f.reg.NameBinding(ctx, f.runID, f.ids["size"], "size")
	if err != nil {
		t.Fatal(err)
	}
	if b.ProvenOther != 1 {
		t.Errorf("ProvenOther = %d for size, want 1: shown by the compiler to be a use of something else (%+v)", b.ProvenOther, b)
	}
	langs, err := f.reg.CompilerCoverage(ctx, f.runID, "none")
	if err != nil {
		t.Fatal(err)
	}
	if len(langs) != 1 || langs[0].Language != "cpp" || langs[0].Checked != 1 || langs[0].Tool != "scip-clang" {
		t.Errorf("coverage = %+v, want cpp checked by scip-clang", langs)
	}

	// Cached: a second pass imports again and does not run the tool.
	second := RunCompilerIndexers(ctx, f.reg, f.runID, opts)
	if len(second.Steps) != 1 || !second.Steps[0].Reused || second.Steps[0].Ran || second.Proof.Files != 1 {
		t.Fatalf("second pass %+v, want the index reused and imported", second)
	}
	if n := strings.Count(string(mustRead(t, logFile)), "\n"); n != 1 {
		t.Errorf("scip-clang ran %d times for two passes with nothing changed, want 1", n)
	}

	// The compilation database is an input of the key: a changed flag runs it again.
	if err := os.WriteFile(filepath.Join(f.root, "compile_commands.json"),
		[]byte(`[{"directory":"/build/repo","command":"c++ -DX -c src/geo.cpp","file":"src/geo.cpp"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	third := RunCompilerIndexers(ctx, f.reg, f.runID, opts)
	if len(third.Steps) != 1 || !third.Steps[0].Ran {
		t.Fatalf("third pass %+v, want a run after compile_commands.json changed", third.Steps)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCPPWithoutACompilationDatabaseIsReportedNotAttempted(t *testing.T) {
	skipWithoutShell(t)
	f := nativeRepo(t, "src/geo.cpp", "cpp", map[string]string{
		"CMakeLists.txt": "project(geo)\n", "src/geo.cpp": cppSource,
	})
	bins, scratch := t.TempDir(), t.TempDir()
	fixture, logFile := filepath.Join(scratch, "cpp.scip"), filepath.Join(scratch, "runs.log")
	writeScip(t, fixture, "scip-clang", cppDocument())
	standIn(t, bins, "scip-clang", "scip-clang 0.3.2", "--index-output-path=", fixture, logFile)
	onlyPath(t, bins)

	out := RunCompilerIndexers(context.Background(), f.reg, f.runID, CompilerIndexOptions{Root: f.root, Config: allowAll(), AllowRepoBuild: true})
	if len(out.Steps) != 1 || out.Steps[0].Skipped == "" {
		t.Fatalf("steps = %+v, want one skipped step", out.Steps)
	}
	for _, want := range []string{"cannot run", "compile_commands.json", "CMAKE_EXPORT_COMPILE_COMMANDS"} {
		if !strings.Contains(out.Steps[0].Skipped, want) {
			t.Errorf("reason %q lacks %q", out.Steps[0].Skipped, want)
		}
	}
	if _, err := os.Stat(logFile); err == nil {
		t.Error("scip-clang was run on a project with no compilation database")
	}
}

func TestMissingCPPIndexerIsSkippedAndNeverInstalled(t *testing.T) {
	skipWithoutShell(t)
	f := nativeRepo(t, "src/geo.cpp", "cpp", map[string]string{
		"compile_commands.json": `[]`, "src/geo.cpp": cppSource,
	})
	onlyPath(t, t.TempDir()) // scip-clang is nowhere

	cfg := allowAll()
	cfg.ProvisionPolicy = config.ProvisionAuto // even a permissive policy installs nothing it cannot verify
	out := RunCompilerIndexers(context.Background(), f.reg, f.runID, CompilerIndexOptions{Root: f.root, Config: cfg, AllowRepoBuild: true})
	if len(out.Steps) != 1 || out.Steps[0].Skipped == "" || out.Imported() != 0 {
		t.Fatalf("steps = %+v, want one skipped step and nothing imported", out.Steps)
	}
	if !strings.Contains(out.Steps[0].Skipped, "scip-clang") {
		t.Errorf("reason %q does not name the missing tool", out.Steps[0].Skipped)
	}
}

const rustSource = "pub struct Circle;\nimpl Circle {\n    pub fn size(&self) -> usize { 1 }\n    pub fn area(&self) -> f64 { 3.0 }\n}\n" +
	"pub fn total(v: &Vec<u8>) -> f64 {\n    let _ = v.size();\n    Circle.area()\n}\n"

func rustDocument() *scippb.Document {
	const (
		circle  = "rust-analyzer cargo geo 0.1.0 Circle#"
		size    = "rust-analyzer cargo geo 0.1.0 impl#[Circle]size()."
		area    = "rust-analyzer cargo geo 0.1.0 impl#[Circle]area()."
		total   = "rust-analyzer cargo geo 0.1.0 total()."
		vecSize = "rust-analyzer cargo alloc 0.0.0 vec/Vec#size()."
	)
	return &scippb.Document{
		RelativePath: "src/lib.rs", Language: "rust",
		PositionEncoding: scippb.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Symbols: []*scippb.SymbolInformation{
			{Symbol: circle, Kind: scippb.SymbolInformation_Struct, DisplayName: "Circle"},
			{Symbol: size, Kind: scippb.SymbolInformation_Method, DisplayName: "size"},
			{Symbol: area, Kind: scippb.SymbolInformation_Method, DisplayName: "area"},
			{Symbol: total, Kind: scippb.SymbolInformation_Function, DisplayName: "total"},
		},
		Occurrences: []*scippb.Occurrence{
			defOcc(circle, 0, 11, 17, 0, 0),
			defOcc(size, 2, 11, 15, 2, 2),
			defOcc(area, 3, 11, 15, 3, 3),
			defOcc(total, 5, 7, 12, 5, 8),
			refOcc(vecSize, 6, 14, 18),
			refOcc(area, 7, 11, 15),
		},
	}
}

// rustFixture is the Rust repository: a single package, with the symbols laid
// out on the lines rustSource has them (the same shape as the C++ one).
func rustFixture(t *testing.T, manifest string) nativeFixture {
	t.Helper()
	return nativeRepo(t, "src/lib.rs", "rust", map[string]string{
		"Cargo.toml": manifest, "src/lib.rs": rustSource,
	}, model.File{Path: "Cargo.toml", Language: "toml", ContentHash: "c1"})
}

const singlePackage = "[package]\nname = \"geo\"\nversion = \"0.1.0\"\n"

func TestRustStandInRunsOnlyWithTheBuildOptInAndIsImported(t *testing.T) {
	skipWithoutShell(t)
	ctx := context.Background()
	f := rustFixture(t, singlePackage)
	bins, scratch := t.TempDir(), t.TempDir()
	fixture, logFile := filepath.Join(scratch, "rust.scip"), filepath.Join(scratch, "runs.log")
	writeScip(t, fixture, "rust-analyzer", rustDocument())
	standIn(t, bins, "rust-analyzer", "rust-analyzer 1.80.0", " --output", fixture, logFile)
	if err := os.WriteFile(filepath.Join(bins, "cargo"), []byte("#!/bin/sh\necho cargo 1.80.0\n"), 0o755); err != nil { //nolint:gosec // G306: a test executable
		t.Fatal(err)
	}
	onlyPath(t, bins)

	// Without the opt-in: rust-analyzer runs with safe config (no build scripts/macros, offline).
	out := RunCompilerIndexers(ctx, f.reg, f.runID, CompilerIndexOptions{Root: f.root, Config: allowAll()})
	if len(out.Steps) != 1 || !out.Steps[0].Ran || len(out.Steps[0].Notes) == 0 || !strings.Contains(out.Steps[0].Notes[0], "build scripts and procedural macros are not run") {
		t.Fatalf("without build opt-in: %+v", out.Steps)
	}
	log := strings.TrimSpace(string(mustRead(t, logFile)))
	if !strings.Contains(log, "--config-path") {
		t.Errorf("invocation %q should pass --config-path when repo build is not allowed", log)
	}

	opts := CompilerIndexOptions{Root: f.root, Config: allowAll(), AllowRepoBuild: true}
	out = RunCompilerIndexers(ctx, f.reg, f.runID, opts)
	if out.Err != nil || len(out.Steps) != 1 || !out.Steps[0].Ran || out.Steps[0].Err != nil {
		t.Fatalf("pass = %+v, want rust-analyzer to have run and imported", out)
	}
	s := out.Steps[0]
	lines := strings.Split(strings.TrimSpace(string(mustRead(t, logFile))), "\n")
	lastLog := lines[len(lines)-1]
	if !strings.HasSuffix(lastLog, " scip . --output "+filepath.Join(f.root, ".graphlume", "scip", "rust.scip")) {
		t.Errorf("invocation = %q, want `scip . --output <index>`", lastLog)
	}
	if strings.Contains(lastLog, "--config-path") {
		t.Errorf("invocation %q should not pass --config-path when repo build is allowed", lastLog)
	}
	if out.Proof.Files != 1 || s.Import.SymbolsMatched != 4 {
		t.Errorf("proof %+v, import %+v: want one file covered and four symbols matched", out.Proof, s.Import)
	}
	if countExactEdge(t, f, f.ids["total"], f.ids["area"]) != 1 {
		t.Error("no exact edge from total to Circle::area")
	}
	if out.Proof.RuledOut != 1 {
		t.Errorf("ruled out %d, want the v.size() reference resolved into alloc", out.Proof.RuledOut)
	}
	b, err := f.reg.NameBinding(ctx, f.runID, f.ids["size"], "size")
	if err != nil {
		t.Fatal(err)
	}
	if b.ProvenOther != 1 {
		t.Errorf("ProvenOther = %d, want 1 (%+v)", b.ProvenOther, b)
	}

	second := RunCompilerIndexers(ctx, f.reg, f.runID, opts)
	if len(second.Steps) != 1 || !second.Steps[0].Reused {
		t.Fatalf("second pass %+v, want the index reused", second.Steps)
	}
	if n := strings.Count(string(mustRead(t, logFile)), "\n"); n != 2 {
		t.Errorf("rust-analyzer ran %d times, want 2 (once with safe config, once with repo build allowed)", n)
	}
}

func TestRustWorkspaceRunsFromRootAndCoversMembers(t *testing.T) {
	skipWithoutShell(t)
	f := nativeRepo(t, "crates/a/src/lib.rs", "rust", map[string]string{
		"Cargo.toml":               "[workspace]\nmembers = [\"crates/a\"]\n",
		"crates/a/Cargo.toml":      singlePackage,
		"crates/a/src/lib.rs":      rustSource,
		"crates/a/src/unrelated.c": "",
	})
	bins, scratch := t.TempDir(), t.TempDir()
	fixture, logFile := filepath.Join(scratch, "rust.scip"), filepath.Join(scratch, "runs.log")
	writeScip(t, fixture, "rust-analyzer", rustDocument())
	standIn(t, bins, "rust-analyzer", "rust-analyzer 1.80.0", " --output", fixture, logFile)
	if err := os.WriteFile(filepath.Join(bins, "cargo"), []byte("#!/bin/sh\necho cargo 1.80.0\n"), 0o755); err != nil { //nolint:gosec // G306: a test executable
		t.Fatal(err)
	}
	onlyPath(t, bins)

	out := RunCompilerIndexers(context.Background(), f.reg, f.runID,
		CompilerIndexOptions{Root: f.root, Config: allowAll(), AllowRepoBuild: true})
	if len(out.Steps) != 1 {
		t.Fatalf("steps = %+v, want one step for the workspace root and none for its members", out.Steps)
	}
	if !out.Steps[0].Ran {
		t.Fatalf("step = %+v, want rust-analyzer to have run on the workspace root", out.Steps[0])
	}
	if len(out.Steps[0].Notes) == 0 || !strings.Contains(out.Steps[0].Notes[0], "Cargo workspace: one run reads the root and its 1 member crate") {
		t.Errorf("notes %v want Cargo workspace note", out.Steps[0].Notes)
	}
	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("rust-analyzer did not run on workspace: %v", err)
	}
}

func TestCargoPackageName(t *testing.T) {
	for in, want := range map[string]string{
		"[package]\nname = \"geo-types\"\n":                      "geo-types",
		"[dependencies]\nname = \"no\"\n[package]\nname='yes'\n": "yes",
		"[workspace]\nmembers = []\n":                            "",
		"[package]\nversion = \"1\"\n":                           "",
	} {
		if got := cargoPackageName(in); got != want {
			t.Errorf("cargoPackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOwnPackagesIncludesCargoPackages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(singlePackage), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ownPackages(root, []model.File{{Path: "Cargo.toml", Language: "toml"}})
	if len(got) != 1 || got[0] != [2]string{"cargo", "geo"} {
		t.Errorf("ownPackages = %v, want [[cargo geo]]", got)
	}
}

func TestNativeLanguagesHaveCacheFamilies(t *testing.T) {
	if !languageFamily(indexer.LangCPP)["cpp"] || !languageFamily(indexer.LangCPP)["c"] || !languageFamily(indexer.LangRust)["rust"] {
		t.Error("the cache key would ignore the source files of C/C++ or Rust")
	}
}

// A src-layout Python project gets a one-line analysis config naming src, a
// project with its own pyrightconfig.json is left alone, a project with no src
// directory (or a link called src) gets nothing, and what was written is
// reported so it can be removed.
func TestPythonSourceRootsAreConfiguredNotImported(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		d := filepath.Join(root, rel)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		return d
	}
	srcLayout := mk("a")
	mk("a/src")
	hasConfig := mk("b")
	mk("b/src")
	if err := os.WriteFile(filepath.Join(hasConfig, "pyrightconfig.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	mk("c") // flat layout
	linked := mk("d")
	mk("elsewhere")
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(linked, "src")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ps := []indexer.Project{
		{Language: indexer.LangPython, Dir: "a"}, {Language: indexer.LangPython, Dir: "b"},
		{Language: indexer.LangPython, Dir: "c"}, {Language: indexer.LangPython, Dir: "d"},
		{Language: indexer.LangTypeScript, Dir: "a"},
	}
	wrote := writePythonSourceRoots(root, ps)
	if len(wrote) != 1 || wrote[0] != filepath.Join(srcLayout, "pyrightconfig.json") {
		t.Fatalf("wrote %v, want only a/pyrightconfig.json", wrote)
	}
	got, err := os.ReadFile(wrote[0])
	if err != nil || string(got) != pythonRootConfig {
		t.Errorf("content = %q, %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(hasConfig, "pyrightconfig.json")); string(b) != "{}" {
		t.Errorf("a project's own config was overwritten: %q", b)
	}
}
