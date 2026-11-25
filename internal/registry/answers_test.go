package registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

// answersFixture writes rows directly, because these reads are about exact
// lines, outcomes and qualifiers, and a fixture that went through the writer
// would be testing the writer's defaults instead.
type answersFixture struct {
	t     *testing.T
	db    *store.DB
	run   int64
	files map[string]int64
}

func newAnswersFixture(t *testing.T) (*Registry, *answersFixture) {
	t.Helper()
	r, db := newTestRegistry(t)
	run, err := r.BeginRun(context.Background(), "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	return r, &answersFixture{t: t, db: db, run: run.ID, files: map[string]int64{}}
}

func (f *answersFixture) exec(query string, args ...any) int64 {
	f.t.Helper()
	res, err := f.db.ExecContext(context.Background(), query, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *answersFixture) file(path, lang string) int64 {
	if id, ok := f.files[path]; ok {
		return id
	}
	id := f.exec(`INSERT INTO files (index_run_id, path, language, content_hash) VALUES (?,?,?,?)`,
		f.run, path, lang, "h-"+path)
	f.files[path] = id
	return id
}

func (f *answersFixture) sym(path, lang, name, kind string, start, end, params int) int64 {
	return f.exec(`INSERT INTO symbols (index_run_id, file_id, kind, name, fqn, start_line, end_line,
	                                    exported, confidence, source, param_count)
	               VALUES (?,?,?,?,?,?,?,0,'high','source_code',?)`,
		f.run, f.file(path, lang), kind, name, model.DefaultFQN(path, name), start, end, params)
}

func (f *answersFixture) ref(path, lang, name, qualifier string, line int, outcome Resolution, target int64) {
	f.refOn(path, lang, name, "", qualifier, line, outcome, target)
}

// refOn is ref for a reference that records what it was made on.
func (f *answersFixture) refOn(path, lang, name, kind, qualifier string, line int, outcome Resolution, target int64) {
	var bound any
	if target != 0 {
		bound = target
	}
	f.exec(`INSERT INTO symbol_references (index_run_id, file_id, name, kind, receiver, receiver_kind,
	                                       start_line, resolution, resolved_symbol_id)
	        VALUES (?,?,?,'reference.call',?,?,?,?,?)`,
		f.run, f.file(path, lang), name, qualifier, kind, line, string(outcome), bound)
}

func (f *answersFixture) edge(src, dst int64, kind, conf, evFile string, evLine int) {
	f.exec(`INSERT INTO edges (index_run_id, src_id, dst_id, kind, confidence, evidence_file,
	                           evidence_line, source)
	        VALUES (?,?,?,?,?,?,?,'source_code')`, f.run, src, dst, kind, conf, evFile, evLine)
}

func TestSymbolCandidatesTellOverloadsApart(t *testing.T) {
	r, f := newAnswersFixture(t)
	ctx := context.Background()

	f.sym("src/Gson.java", "java", "Gson", "class", 1, 100, 0)
	f.sym("src/Gson.java", "java", "fromJson", "method", 10, 19, int(model.KnownArity(2)))
	f.sym("src/Gson.java", "java", "fromJson", "method", 20, 29, int(model.KnownArity(3)))
	f.sym("src/Adapter.java", "java", "Adapter", "class", 1, 50, 0)
	f.sym("src/Adapter.java", "java", "fromJson", "method", 5, 9, 0)
	f.sym("lib/free.go", "go", "fromJson", "function", 3, 8, int(model.KnownArity(0)))

	got, total, err := r.SymbolCandidates(ctx, f.run, "fromJson", 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(got) != 4 {
		t.Fatalf("total=%d rows=%d, want 4 and 4", total, len(got))
	}
	// Path, then line: deterministic, and not ordered by a guessed export flag.
	want := []struct {
		path, lang, owner string
		line              int
		params            int // -1 unknown
	}{
		{"lib/free.go", "go", "", 3, 0},
		{"src/Adapter.java", "java", "Adapter", 5, -1},
		{"src/Gson.java", "java", "Gson", 10, 2},
		{"src/Gson.java", "java", "Gson", 20, 3},
	}
	for i, w := range want {
		c := got[i]
		if c.FilePath != w.path || c.StartLine != w.line || c.Language != w.lang || c.Owner != w.owner {
			t.Errorf("row %d = %s:%d lang=%q owner=%q, want %s:%d lang=%q owner=%q",
				i, c.FilePath, c.StartLine, c.Language, c.Owner, w.path, w.line, w.lang, w.owner)
		}
		switch {
		case w.params < 0 && c.ParamCount.Known():
			t.Errorf("row %d: parameter count is known (%d), want unknown", i, c.ParamCount.Count())
		case w.params >= 0 && (!c.ParamCount.Known() || c.ParamCount.Count() != w.params):
			t.Errorf("row %d: parameter count = %v, want %d", i, c.ParamCount, w.params)
		}
	}
	// Two overloads share one qualified name: that is why the line exists.
	if got[2].FQN != got[3].FQN {
		t.Errorf("the overloads' qualified names differ (%q, %q); this fixture no longer models them", got[2].FQN, got[3].FQN)
	}

	// The total is the count, not the page.
	page, total, err := r.SymbolCandidates(ctx, f.run, "fromJson", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(page) != 2 {
		t.Errorf("limit 2: total=%d rows=%d, want 4 and 2", total, len(page))
	}

	// By qualified name: both overloads, never just the first.
	byFQN, total, err := r.SymbolCandidatesByFQN(ctx, f.run, "src/Gson.java:fromJson", 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(byFQN) != 2 || byFQN[0].StartLine != 10 || byFQN[1].StartLine != 20 {
		t.Errorf("by qualified name: total=%d rows=%+v, want the two overloads", total, byFQN)
	}

	// Narrowed by path in the store, with a total of its own.
	in, total, err := r.SymbolCandidatesIn(ctx, f.run, "fromJson", "Adapter", 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(in) != 1 || in[0].FilePath != "src/Adapter.java" {
		t.Errorf("in path: total=%d rows=%+v", total, in)
	}
	// A path holding SQL wildcards is matched literally.
	if _, total, _ := r.SymbolCandidatesIn(ctx, f.run, "fromJson", "%", 10); total != 0 {
		t.Errorf("path %% matched %d definitions; it must be a literal", total)
	}

	if none, total, err := r.SymbolCandidates(ctx, f.run, "absent", 10); err != nil || total != 0 || none != nil {
		t.Errorf("an unknown name: rows=%v total=%d err=%v", none, total, err)
	}
}

func TestNameBindingCountsEveryOutcomeForOneName(t *testing.T) {
	r, f := newAnswersFixture(t)
	ctx := context.Background()

	target := f.sym("lib/a.go", "go", "decode", "function", 10, 20, 0)
	other := f.sym("lib/b.go", "go", "decode", "function", 10, 20, 0)
	caller := f.sym("lib/a.go", "go", "run", "function", 30, 40, 0)

	// Two calls inside a function: bound, and each has its edge.
	f.ref("lib/a.go", "go", "decode", "", 32, ResolutionResolved, target)
	f.edge(caller, target, "CALLS", "high", "lib/a.go", 32)
	f.ref("lib/a.go", "go", "decode", "", 34, ResolutionResolved, target)
	f.edge(caller, target, "CALLS", "high", "lib/a.go", 34)
	// Three at module top level: bound, and no edge records them.
	for _, line := range []int{2, 3, 4} {
		f.ref("lib/a.go", "go", "decode", "", line, ResolutionResolved, target)
	}
	f.ref("lib/b.go", "go", "decode", "", 50, ResolutionResolved, other)
	for i := 0; i < 4; i++ {
		f.ref("lib/c.go", "go", "decode", "x", 10+i, ResolutionAmbiguous, 0)
	}
	f.ref("lib/c.go", "go", "decode", "", 20, ResolutionUnresolved, 0)
	f.ref("lib/c.go", "go", "decode", "", 21, ResolutionPending, 0)
	// Another name entirely: not counted.
	f.ref("lib/c.go", "go", "encode", "", 30, ResolutionAmbiguous, 0)

	b, err := r.NameBinding(ctx, f.run, target, "decode")
	if err != nil {
		t.Fatal(err)
	}
	want := NameBinding{
		Language: "go", References: 12,
		BoundHere: 5, BoundElsewhere: 1, Ambiguous: 4, Unresolved: 1, Pending: 1,
		Definitions: 2, WithoutEdge: 3,
	}
	if b != want {
		t.Errorf("NameBinding = %+v\nwant          %+v", b, want)
	}
	if b.Unbound() != 6 {
		t.Errorf("Unbound = %d, want 6", b.Unbound())
	}

	// Seen from the other definition, the same rows split the other way.
	o, err := r.NameBinding(ctx, f.run, other, "decode")
	if err != nil {
		t.Fatal(err)
	}
	if o.BoundHere != 1 || o.BoundElsewhere != 5 || o.WithoutEdge != 1 {
		t.Errorf("from the other definition: %+v", o)
	}

	// A name nothing refers to is all zeros, with the language still read.
	lone := f.sym("lib/d.go", "go", "lonely", "function", 1, 2, 0)
	z, err := r.NameBinding(ctx, f.run, lone, "lonely")
	if err != nil {
		t.Fatal(err)
	}
	if z != (NameBinding{Language: "go", Definitions: 1}) {
		t.Errorf("an unreferenced name: %+v", z)
	}
}

func TestLanguageBindingsSeparateWhatEachLanguageYields(t *testing.T) {
	r, f := newAnswersFixture(t)
	ctx := context.Background()

	// Kotlin: definitions, and nothing that says what they use.
	f.sym("k/a.kt", "kotlin", "emit", "function", 1, 5, 0)
	f.sym("k/b.kt", "kotlin", "flush", "function", 1, 5, 0)
	// Java: references, some bound, and one inheritance edge.
	base := f.sym("j/Base.java", "java", "Base", "class", 1, 20, 0)
	impl := f.sym("j/Impl.java", "java", "Impl", "class", 1, 20, 0)
	run := f.sym("j/Impl.java", "java", "run", "method", 5, 10, 0)
	f.edge(impl, base, "EXTENDS", "high", "j/Impl.java", 1)
	f.edge(run, base, "CALLS", "high", "j/Impl.java", 6)
	f.edge(impl, run, "CONTAINS", "high", "j/Impl.java", 5) // never counted
	f.ref("j/Impl.java", "java", "x", "", 6, ResolutionResolved, base)
	f.ref("j/Impl.java", "java", "y", "", 7, ResolutionAmbiguous, 0)
	f.ref("j/Impl.java", "java", "z", "", 8, ResolutionUnresolved, 0)
	f.ref("j/Impl.java", "java", "w", "", 9, ResolutionPending, 0)
	// A file with no language and no symbols.
	f.file("README", "")

	langs, err := r.LanguageBindings(ctx, f.run)
	if err != nil {
		t.Fatal(err)
	}

	k := langs["kotlin"]
	if k.Files != 2 || k.Definitions != 2 || k.Total != 0 || k.Relationships != 0 {
		t.Errorf("kotlin = %+v", k)
	}
	if k.ExtractsReferences() {
		t.Error("a language with definitions and no reference or relationship is reported as extracting references")
	}

	j := langs["java"]
	if j.Files != 2 || j.Definitions != 3 {
		t.Errorf("java files/definitions = %d/%d, want 2/3", j.Files, j.Definitions)
	}
	if j.Total != 4 || j.Resolved != 1 || j.Ambiguous != 1 || j.Unresolved != 1 || j.Pending != 1 {
		t.Errorf("java outcomes = %+v", j.ResolutionStats)
	}
	if j.Relationships != 2 || j.Hierarchy != 1 {
		t.Errorf("java relationships=%d hierarchy=%d, want 2 and 1 (containment is not a relationship)",
			j.Relationships, j.Hierarchy)
	}
	if !j.ExtractsReferences() {
		t.Error("java has references and is reported as not extracting them")
	}
	if got := j.BindingRate(); got != 0.5 {
		t.Errorf("java binding rate = %v, want 0.5 (1 resolved of 2 bindable)", got)
	}
	if _, ok := langs[""]; !ok {
		t.Error("files with no language are missing; they must be countable, not dropped")
	}
}

func TestEdgePageIsStableAndCountsTheTotal(t *testing.T) {
	r, f := newAnswersFixture(t)
	ctx := context.Background()

	target := f.sym("t.go", "go", "target", "function", 1, 5, 0)
	const n = 23
	for i := 0; i < n; i++ {
		src := f.sym(fmt.Sprintf("c%02d.go", i), "go", fmt.Sprintf("caller%02d", i), "function", 1, 5, 0)
		conf := "high"
		if i%5 == 0 {
			conf = "low"
		}
		// Several edges share a file and line with their neighbours' order
		// undefined except by id, which is the tie the page order must break.
		f.edge(src, target, "CALLS", conf, "shared.go", 7)
	}
	other := f.sym("o.go", "go", "other", "function", 1, 5, 0)
	f.edge(other, target, "REFERENCES", "high", "o.go", 3)

	filter := EdgeFilter{Kinds: []string{"CALLS"}}
	seen := map[int64]bool{}
	var order []int64
	for offset := 0; ; offset += 5 {
		page, total, err := r.EdgePage(ctx, f.run, target, true, 5, offset, filter)
		if err != nil {
			t.Fatal(err)
		}
		if total != n {
			t.Fatalf("total = %d at offset %d, want %d", total, offset, n)
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if seen[e.SrcID] {
				t.Errorf("caller %d appears on two pages", e.SrcID)
			}
			seen[e.SrcID] = true
			order = append(order, e.SrcID)
		}
	}
	if len(seen) != n {
		t.Errorf("paging reached %d of %d edges", len(seen), n)
	}

	// One read of everything gives the same order the pages did.
	all, _, err := r.EdgePage(ctx, f.run, target, true, 100, 0, filter)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range all {
		if e.SrcID != order[i] {
			t.Fatalf("row %d differs between a single read and paged reads", i)
		}
	}
	// Strongest first.
	for i := 1; i < len(all); i++ {
		if all[i-1].Confidence == "low" && all[i].Confidence == "high" {
			t.Fatalf("a low row precedes a high one at %d", i)
		}
	}

	// The floor narrows the total as well as the rows.
	_, strong, err := r.EdgePage(ctx, f.run, target, true, 5, 0,
		EdgeFilter{Kinds: []string{"CALLS"}, MinConfidence: model.ConfidenceHigh})
	if err != nil {
		t.Fatal(err)
	}
	if strong != n-5 {
		t.Errorf("total at min high = %d, want %d", strong, n-5)
	}

	// Outgoing, and past the end.
	out, total, err := r.EdgePage(ctx, f.run, other, false, 5, 0, EdgeFilter{})
	if err != nil || total != 1 || len(out) != 1 || out[0].DstName != "target" {
		t.Errorf("outgoing: rows=%+v total=%d err=%v", out, total, err)
	}
	past, total, err := r.EdgePage(ctx, f.run, target, true, 5, 500, filter)
	if err != nil || total != n || len(past) != 0 {
		t.Errorf("past the end: rows=%d total=%d err=%v", len(past), total, err)
	}
}

func TestBoundSitesReportTheQualifierAndKindEachCallWasMadeWith(t *testing.T) {
	r, f := newAnswersFixture(t)
	ctx := context.Background()

	target := f.sym("gin.go", "go", "New", "function", 10, 20, 0)
	f.refOn("gin.go", "go", "New", model.ReceiverBare, "", 30, ResolutionResolved, target)
	f.refOn("gin.go", "go", "New", model.ReceiverName, "errors", 40, ResolutionResolved, target)
	f.refOn("a.go", "go", "New", model.ReceiverValue, "template", 5, ResolutionResolved, target)
	f.ref("a.go", "go", "New", "errors", 9, ResolutionAmbiguous, 0) // not bound: not a site

	sites, err := r.BoundSites(ctx, f.run, target, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []BoundSite{
		{Path: "a.go", Line: 5, Qualifier: "template", ReceiverKind: model.ReceiverValue},
		{Path: "gin.go", Line: 30, Qualifier: "", ReceiverKind: model.ReceiverBare},
		{Path: "gin.go", Line: 40, Qualifier: "errors", ReceiverKind: model.ReceiverName},
	}
	if len(sites) != len(want) {
		t.Fatalf("sites = %+v, want %+v", sites, want)
	}
	for i := range want {
		if sites[i] != want[i] {
			t.Errorf("site %d = %+v, want %+v", i, sites[i], want[i])
		}
	}
	if capped, _ := r.BoundSites(ctx, f.run, target, 2); len(capped) != 2 {
		t.Errorf("limit 2 returned %d sites", len(capped))
	}
}
