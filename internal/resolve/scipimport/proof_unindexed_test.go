package scipimport

import (
	"context"
	"testing"

	scippb "github.com/scip-code/scip/bindings/go/scip"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/scip"
	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

// These tests pin what an occurrence that resolves INSIDE the repository, to a
// symbol the index holds no definition for, settles (see Proof).
//
// The repository is TypeScript, as in proof_test.go. svc.ts defines the method
// `get`, the definition every question is about. use.ts calls things spelled
// `get` in ways whose settlement differs, one per line:
//
//	line 10  a function declared inside run(), called          -> not a use of svc's get
//	line 12  an instance of a nested class that does NOT override get, called
//	         (the compiler names the BASE method)             -> a use of svc's get
//	line 14  an instance of a nested class that OVERRIDES get, called
//	         (the compiler names the override)                -> not a use of svc's get
//	line 16  a call the compiler gave no occurrence for       -> unknown, stays counted
//	line 18  a local `const get = ...` called                 -> not a use of svc's get
//	line 20  an import binding the indexer could not resolve  -> unknown, stays counted
//	line 22  a member call whose position holds a local       -> not a bare call, unknown
//	line 24  a call of a top-level `const get = svc.get`       -> not a use of svc's get, when the
//	         resolver bound nothing (it may be an alias of it)
//	line 26  the same, bound by the resolver to svc's get     -> the binding is kept
//
// dup.ts has two indexed symbols named get that no compiler definition is
// joined to, and defines nested things spelled get too: the two sides may have
// failed to join, so nothing there is ruled out. ok.ts has an indexed get that
// IS joined to a compiler definition and a local get that shadows it: that one
// is settled.
const (
	uCoreGet    = "scip-typescript npm proj 1.0.0 `src/svc.ts`/Service#get()."
	uRun        = "scip-typescript npm proj 1.0.0 `src/use.ts`/run()."
	uInnerGet   = "scip-typescript npm proj 1.0.0 `src/use.ts`/run().get()."
	uSubGet     = "scip-typescript npm proj 1.0.0 `src/use.ts`/run().Sub#get()."
	uAliasGet   = "scip-typescript npm proj 1.0.0 `src/use.ts`/get."
	uDupRun     = "scip-typescript npm proj 1.0.0 `src/dup.ts`/dupRun()."
	uDupInner   = "scip-typescript npm proj 1.0.0 `src/dup.ts`/dupRun().get()."
	uOkRun      = "scip-typescript npm proj 1.0.0 `src/ok.ts`/okRun()."
	uOkGet      = "scip-typescript npm proj 1.0.0 `src/ok.ts`/get()."
	svcPathU    = "src/svc.ts"
	usePathU    = "src/use.ts"
	dupPathU    = "src/dup.ts"
	okPathU     = "src/ok.ts"
	declaredLoc = "local 3"
)

type unindexedRepo struct {
	reg   *registry.Registry
	runID int64
	get   int64
	db    *store.DB
}

func unindexedFixture(t *testing.T) unindexedRepo {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := registry.New(db)
	testDBs.Store(reg, db)
	run, err := reg.BeginRun(ctx, "auto", "cafe")
	if err != nil {
		t.Fatal(err)
	}
	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := func(path string) int64 {
		fid, err := w.AddFile(ctx, &model.File{Path: path, Language: "typescript", ContentHash: "h" + path})
		if err != nil {
			t.Fatal(err)
		}
		return fid
	}
	sym := func(fid int64, path, name string, kind model.EntityKind, start, end int) int64 {
		id, err := w.AddSymbol(ctx, &model.Symbol{
			FileID: fid, Kind: kind, Name: name, FQN: path + ":" + name + ":" + string(rune('a'+start)),
			Range:      model.Range{Start: model.Position{Line: start}, End: model.Position{Line: end}},
			Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	svc := file(svcPathU)
	get := sym(svc, svcPathU, "get", model.KindMethod, 6, 8)
	use := file(usePathU)
	sym(use, usePathU, "run", model.KindFunction, 4, 40)
	dup := file(dupPathU)
	sym(dup, dupPathU, "dupRun", model.KindFunction, 1, 40)
	sym(dup, dupPathU, "get", model.KindFunction, 3, 4)
	sym(dup, dupPathU, "get", model.KindFunction, 9, 10)
	ok := file(okPathU)
	sym(ok, okPathU, "okRun", model.KindFunction, 1, 40)
	sym(ok, okPathU, "get", model.KindFunction, 3, 4)

	call := func(fid int64, line int, kind string) {
		if err := w.AddReference(ctx, fid, registry.Reference{
			Name: "get", Kind: "reference.call", Line: line, Col: 8, ReceiverKind: kind, ArgCount: model.ArityUnknown,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []int{10, 12, 14, 16, 18, 20} {
		call(use, l, model.ReceiverBare)
	}
	call(use, 22, model.ReceiverName)
	call(use, 24, model.ReceiverBare)
	call(use, 26, model.ReceiverBare)
	call(dup, 12, model.ReceiverBare) // a local defined beside two indexed gets
	call(dup, 14, model.ReceiverBare) // a nested function, beside two indexed gets
	call(ok, 12, model.ReceiverBare)  // a local that shadows the one indexed get
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
	return unindexedRepo{reg: reg, runID: run.ID, get: get, db: db}
}

func localDef(sym string, line int32, name string) (*scippb.Occurrence, *scippb.SymbolInformation) {
	return def(sym, rng(line, 10, line, 13), rng(line, 0, line, 30)),
		&scippb.SymbolInformation{Symbol: sym, DisplayName: name, Kind: scippb.SymbolInformation_Variable}
}

func unindexedDocs() []*scippb.Document {
	enc := scippb.PositionEncoding_UTF16CodeUnitOffsetFromLineStart
	docs := []*scippb.Document{{
		RelativePath: svcPathU, Language: "typescript", PositionEncoding: enc,
		Symbols:     []*scippb.SymbolInformation{{Symbol: uCoreGet, Kind: scippb.SymbolInformation_Method, DisplayName: "get"}},
		Occurrences: []*scippb.Occurrence{def(uCoreGet, rng(5, 2, 5, 5), rng(5, 0, 7, 1))},
	}}

	// use.ts: lines are 0-based here, one below the registry's.
	l3def, l3info := localDef(declaredLoc, 16, "get")
	useOcc := []*scippb.Occurrence{
		def(uRun, rng(3, 9, 3, 12), rng(3, 0, 39, 1)),
		def(uInnerGet, rng(8, 9, 8, 12), rng(8, 0, 8, 40)),
		ref(uInnerGet, rng(9, 8, 9, 11)),  // line 10
		ref(uCoreGet, rng(11, 8, 11, 11)), // line 12: inherited, the base's own symbol
		def(uSubGet, rng(12, 4, 12, 7), rng(12, 0, 12, 40)),
		ref(uSubGet, rng(13, 8, 13, 11)), // line 14: an override
		// line 16: no occurrence.
		l3def,
		ref(declaredLoc, rng(17, 8, 17, 11)), // line 18: a declared local
		ref("local 4", rng(19, 8, 19, 11)),   // line 20: declared nowhere
		ref(declaredLoc, rng(21, 8, 21, 11)), // line 22: a local at a member call's column
		def(uAliasGet, rng(22, 6, 22, 9), rng(22, 0, 22, 30)),
		ref(uAliasGet, rng(23, 8, 23, 11)), // line 24
		ref(uAliasGet, rng(25, 8, 25, 11)), // line 26
	}
	docs = append(docs, &scippb.Document{
		RelativePath: usePathU, Language: "typescript", PositionEncoding: enc,
		Symbols: []*scippb.SymbolInformation{
			{Symbol: uRun, Kind: scippb.SymbolInformation_Function, DisplayName: "run"},
			{Symbol: uInnerGet, Kind: scippb.SymbolInformation_Function, DisplayName: "get"},
			{Symbol: uSubGet, Kind: scippb.SymbolInformation_Method, DisplayName: "get"},
			{Symbol: uAliasGet, Kind: scippb.SymbolInformation_Variable, DisplayName: "get"},
			l3info,
		},
		Occurrences: useOcc,
	})

	// dup.ts: two indexed gets, no compiler definition joined to either.
	dupLoc, dupInfo := localDef("local 5", 10, "get")
	docs = append(docs, &scippb.Document{
		RelativePath: dupPathU, Language: "typescript", PositionEncoding: enc,
		Symbols: []*scippb.SymbolInformation{
			{Symbol: uDupRun, Kind: scippb.SymbolInformation_Function, DisplayName: "dupRun"},
			{Symbol: uDupInner, Kind: scippb.SymbolInformation_Function, DisplayName: "get"},
			dupInfo,
		},
		Occurrences: []*scippb.Occurrence{
			def(uDupRun, rng(0, 9, 0, 14), rng(0, 0, 39, 1)),
			dupLoc,
			ref("local 5", rng(11, 8, 11, 11)), // line 12
			def(uDupInner, rng(12, 9, 12, 12), rng(12, 0, 12, 30)),
			ref(uDupInner, rng(13, 8, 13, 11)), // line 14
		},
	})

	// ok.ts: the one indexed get is joined, a local get shadows it.
	okLoc, okInfo := localDef("local 6", 10, "get")
	docs = append(docs, &scippb.Document{
		RelativePath: okPathU, Language: "typescript", PositionEncoding: enc,
		Symbols: []*scippb.SymbolInformation{
			{Symbol: uOkRun, Kind: scippb.SymbolInformation_Function, DisplayName: "okRun"},
			{Symbol: uOkGet, Kind: scippb.SymbolInformation_Function, DisplayName: "get"},
			okInfo,
		},
		Occurrences: []*scippb.Occurrence{
			def(uOkRun, rng(0, 9, 0, 14), rng(0, 0, 39, 1)),
			def(uOkGet, rng(2, 9, 2, 12), rng(2, 0, 3, 1)),
			okLoc,
			ref("local 6", rng(11, 8, 11, 11)), // line 12
		},
	})
	return docs
}

// resolutionAt reads how the reference at path:line was settled.
func resolutionAt(t *testing.T, u unindexedRepo, path string, line int) (resolution, source string) {
	t.Helper()
	if err := dbOf(u.reg).QueryRowContext(context.Background(), `
		SELECT sr.resolution, sr.resolution_source FROM symbol_references sr
		  JOIN files f ON f.id = sr.file_id
		 WHERE sr.index_run_id = ? AND f.path = ? AND sr.start_line = ?`, u.runID, path, line).
		Scan(&resolution, &source); err != nil {
		t.Fatalf("%s:%d: %v", path, line, err)
	}
	return resolution, source
}

func importUnindexed(t *testing.T, u unindexedRepo) ProofStats {
	t.Helper()
	ctx := context.Background()
	proof := NewProof()
	imp := NewImporter(u.reg, u.runID, scip.Options{RepoRoot: "/build/repo"}).Collect(proof)
	if _, err := imp.Import(ctx, scipFile(t, unindexedDocs()...)); err != nil {
		t.Fatalf("import: %v", err)
	}
	st, _, err := proof.Apply(ctx, u.reg, u.runID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return st
}

func TestProofSettlesWhatResolvesToRepositoryCodeTheIndexHasNoDefinitionFor(t *testing.T) {
	u := unindexedFixture(t)
	if _, err := dbOf(u.reg).ExecContext(context.Background(), `
		UPDATE symbol_references SET resolution = 'resolved', resolved_symbol_id = ?
		 WHERE start_line = 26 AND file_id = (SELECT id FROM files WHERE path = ?)`, u.get, usePathU); err != nil {
		t.Fatal(err)
	}
	importUnindexed(t, u)

	want := []struct {
		path   string
		line   int
		source string
		why    string
	}{
		{usePathU, 10, registry.ResolutionSourceRuledOut, "a function declared inside run() is not Service.get"},
		{usePathU, 12, "compiler", "a call that inherits Service.get reaches Service.get: bound"},
		{usePathU, 14, registry.ResolutionSourceRuledOut, "a call that reaches an override does not reach the base method"},
		{usePathU, 16, "", "no occurrence at all: unknown"},
		{usePathU, 18, registry.ResolutionSourceRuledOut, "a bare call of a local the document declares is a call of that local"},
		{usePathU, 20, "", "an import binding nothing declares says nothing"},
		{usePathU, 22, "", "a local at a member call's column is not a bare call"},
		{usePathU, 24, registry.ResolutionSourceRuledOut, "a call of a top-level variable nothing bound: not a call of get"},
		{usePathU, 26, "", "the resolver's binding of an aliasing variable is kept"},
		{dupPathU, 12, "", "an indexed symbol named get no compiler definition accounts for: the join may have failed"},
		{dupPathU, 14, "", "the same, for a nested function the index also holds no symbol for"},
		{okPathU, 12, registry.ResolutionSourceRuledOut, "the file's only indexed get is accounted for, so the local is another binding"},
	}
	for _, w := range want {
		res, src := resolutionAt(t, u, w.path, w.line)
		if src != w.source {
			t.Errorf("%s:%d resolution_source = %q (%s), want %q: %s", w.path, w.line, src, res, w.source, w.why)
		}
	}

	// The answer about Service.get counts exactly the references nothing
	// settled: no occurrence (use.ts:16), an unresolved import (use.ts:20), a
	// member call (use.ts:22) and the two guarded lines of dup.ts.
	b, err := u.reg.NameBinding(context.Background(), u.runID, u.get, "get")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Unbound(); got != 5 {
		t.Errorf("%d references still unbound, want 5: %+v", got, b)
	}
	if b.ProvenOther != 5 {
		t.Errorf("ProvenOther = %d, want 5 (use.ts 10, 14, 18, 24 and ok.ts 12)", b.ProvenOther)
	}
	if b.BoundHere != 2 {
		t.Errorf("BoundHere = %d, want 2 (the inherited call at use.ts:12 and the resolver's at use.ts:26)", b.BoundHere)
	}
}

func TestProofLocalRuleNeedsABareCallAtTheOccurrence(t *testing.T) {
	u := unindexedFixture(t)
	// The same call on use.ts:18, written against a receiver: the parser's
	// column would then be the receiver's, not the callee's.
	if _, err := dbOf(u.reg).ExecContext(context.Background(), `
		UPDATE symbol_references SET receiver_kind = 'name', receiver = 'x'
		 WHERE start_line = 18 AND file_id = (SELECT id FROM files WHERE path = ?)`, usePathU); err != nil {
		t.Fatal(err)
	}
	importUnindexed(t, u)
	if _, src := resolutionAt(t, u, usePathU, 18); src != "" {
		t.Errorf("a member call was ruled out by a local at its column: source %q", src)
	}
}

func TestProofLocalRuleNeedsTheOccurrenceToSpanTheName(t *testing.T) {
	u := unindexedFixture(t)
	// A reference of another length at that column (a longer name beginning
	// there) is not the occurrence the compiler resolved.
	if _, err := dbOf(u.reg).ExecContext(context.Background(), `
		UPDATE symbol_references SET name = 'getter'
		 WHERE start_line = 18 AND file_id = (SELECT id FROM files WHERE path = ?)`, usePathU); err != nil {
		t.Fatal(err)
	}
	importUnindexed(t, u)
	if _, src := resolutionAt(t, u, usePathU, 18); src != "" {
		t.Errorf("a reference of another length was ruled out: source %q", src)
	}
}

// TestProofUnbindsAResolverBindingOnlyWhereTheCompilerNamedAnotherDefinition: the
// resolver bound two calls to svc.ts's get by name. At use.ts:10 the compiler
// resolved the call to a function the document declares inside run(), a
// definition: the binding was a name match with another definition and goes. At
// use.ts:18 it resolved the call to a local variable, which may be an alias of
// get (`const get = svc.get`), so the resolver's binding, which may be that
// alias, stays.
func TestProofUnbindsAResolverBindingOnlyWhereTheCompilerNamedAnotherDefinition(t *testing.T) {
	u := unindexedFixture(t)
	if _, err := dbOf(u.reg).ExecContext(context.Background(), `
		UPDATE symbol_references SET resolution = 'resolved', resolved_symbol_id = ?
		 WHERE start_line IN (10, 18) AND file_id = (SELECT id FROM files WHERE path = ?)`, u.get, usePathU); err != nil {
		t.Fatal(err)
	}
	st := importUnindexed(t, u)
	if st.Unbound != 1 {
		t.Errorf("Unbound = %d, want 1 (use.ts:10)", st.Unbound)
	}
	if res, src := resolutionAt(t, u, usePathU, 10); res != "unresolved" || src != registry.ResolutionSourceRuledOut {
		t.Errorf("use.ts:10 is %q by %q, want unresolved, ruled out", res, src)
	}
	if res, src := resolutionAt(t, u, usePathU, 18); res != "resolved" || src == registry.ResolutionSourceRuledOut {
		t.Errorf("use.ts:18 is %q by %q, want the resolver's binding kept", res, src)
	}
}
