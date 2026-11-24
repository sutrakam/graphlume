package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// indexBuilder writes a small index through the real registry, in the shape
// the answer tests need: definitions with exact lines, references with a
// recorded outcome and qualifier, and edges at known evidence sites.
//
// Everything is collected first and written by seed, because the registry
// writes symbols through a Writer and outcomes and edges after it is closed.
type indexBuilder struct {
	files []fileDef
	refs  []refDef
	edges []edgeDef
}

type fileDef struct {
	path, lang, text string
	syms             []symDef
}

type symDef struct {
	name       string
	kind       model.EntityKind
	start, end int
	params     int // -1 for unknown
	// owner is the type the member is declared in, which the real indexer puts
	// in the qualified name (`path:Guard.AgainstNull`). Empty for a top-level
	// definition.
	owner string
}

// ownedBy returns s declared in owner.
func (s symDef) ownedBy(owner string) symDef { s.owner = owner; return s }

type refDef struct {
	path, name, qualifier string
	// kind is what the call was made on, a model.Receiver* constant; empty
	// for a reference that records none.
	kind    string
	line    int
	outcome registry.Resolution
	// target is "path:name@line" of the definition a resolved reference is
	// bound to; empty for an unbound one.
	target string
	// refKind is the tag the extractor gave it: "reference.call" when empty,
	// "reference.implementation" or "reference.extends" for a supertype.
	refKind string
}

type edgeDef struct {
	src, dst string // "path:name@line"
	kind     model.EdgeKind
	conf     model.Confidence
	evFile   string
	evLine   int
}

func cmpOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// superRef records a reference that names a supertype, as `impl T for X` or
// `class C(T)` does, with what resolution made of it.
func (b *indexBuilder) superRef(path, name string, line int, kind string, outcome registry.Resolution, target string) {
	b.refs = append(b.refs, refDef{path: path, name: name, line: line, outcome: outcome, target: target, refKind: kind})
}

func symKey(path, name string, line int) string { return fmt.Sprintf("%s:%s@%d", path, name, line) }

// file adds a source file. Its text has one line per number up to lines, so
// FileExcerpt returns something recognisable for any line.
func (b *indexBuilder) file(path, lang string, lines int, syms ...symDef) {
	var text strings.Builder
	for i := 1; i <= lines; i++ {
		// No path in the text: a path holds the names it files, and a
		// fixture that said "ui/cn.go" on every line would mention the symbol
		// "cn" 20 times, which no real source file does.
		fmt.Fprintf(&text, "line %d of the file\n", i)
	}
	b.files = append(b.files, fileDef{path: path, lang: lang, text: text.String(), syms: syms})
}

// fileText adds a source file with the text given, for a test about what the
// text says: a name used where no call is recorded.
func (b *indexBuilder) fileText(path, lang, text string, syms ...symDef) {
	b.files = append(b.files, fileDef{path: path, lang: lang, text: text, syms: syms})
}

func fn(name string, start, end int) symDef {
	return symDef{name: name, kind: model.KindFunction, start: start, end: end, params: -1}
}

func method(name string, start, end, params int) symDef {
	return symDef{name: name, kind: model.KindMethod, start: start, end: end, params: params}
}

func typ(name string, kind model.EntityKind, start, end int) symDef {
	return symDef{name: name, kind: kind, start: start, end: end, params: -1}
}

func (b *indexBuilder) ref(path, name, qualifier string, line int, outcome registry.Resolution, target string) {
	b.refs = append(b.refs, refDef{path: path, name: name, qualifier: qualifier, line: line, outcome: outcome, target: target})
}

func (b *indexBuilder) edge(src, dst string, kind model.EdgeKind, conf model.Confidence, evFile string, evLine int) {
	b.edges = append(b.edges, edgeDef{src: src, dst: dst, kind: kind, conf: conf, evFile: evFile, evLine: evLine})
}

// call records a resolved call from caller to callee at evFile:evLine: the
// bound reference AND the edge it produces, which is what the resolver writes
// for a call inside an extracted function.
func (b *indexBuilder) call(caller, callee, calleeName, qualifier, evFile string, evLine int, conf model.Confidence) {
	b.callOn(caller, callee, calleeName, "", qualifier, evFile, evLine, conf)
}

// callOn is call for a call that records what it was made on.
func (b *indexBuilder) callOn(caller, callee, calleeName, kind, qualifier, evFile string, evLine int, conf model.Confidence) {
	b.refs = append(b.refs, refDef{path: evFile, name: calleeName, qualifier: qualifier, kind: kind, line: evLine,
		outcome: registry.ResolutionResolved, target: callee})
	b.edge(caller, callee, model.EdgeCalls, conf, evFile, evLine)
}

func (b *indexBuilder) seed(ctx context.Context, reg *registry.Registry, runID int64) {
	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		panic(err)
	}
	fileIDs := map[string]int64{}
	symIDs := map[string]int64{}
	for _, f := range b.files {
		mf := model.File{Path: f.path, Language: f.lang, ContentHash: "h-" + f.path, ParseStatus: "ok"}
		id, err := w.AddFile(ctx, &mf)
		if err != nil {
			panic(err)
		}
		fileIDs[f.path] = id
		if err := w.IndexFileText(ctx, id, f.path, f.text); err != nil {
			panic(err)
		}
		for _, s := range f.syms {
			fqnName := s.name
			if s.owner != "" {
				fqnName = s.owner + "." + s.name
			}
			ms := model.Symbol{
				FileID: id, Kind: s.kind, Name: s.name, FQN: model.DefaultFQN(f.path, fqnName),
				Range:      model.Range{Start: model.Position{Line: s.start}, End: model.Position{Line: s.end}},
				Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
			}
			if s.params >= 0 {
				ms.ParamCount = model.KnownArity(s.params)
			}
			sid, err := w.AddSymbol(ctx, &ms)
			if err != nil {
				panic(err)
			}
			symIDs[symKey(f.path, s.name, s.start)] = sid
		}
	}
	for _, r := range b.refs {
		if err := w.AddReference(ctx, fileIDs[r.path], registry.Reference{
			Name: r.name, Kind: cmpOr(r.refKind, "reference.call"), Receiver: r.qualifier, ReceiverKind: r.kind, Line: r.line,
		}); err != nil {
			panic(err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		panic(err)
	}
	_ = w.Close()

	// Reference ids are assigned in insertion order from 1 in a fresh store.
	outcomes := make([]registry.RefOutcome, 0, len(b.refs))
	for i, r := range b.refs {
		o := registry.RefOutcome{RefID: int64(i + 1), Resolution: r.outcome}
		if r.target != "" {
			id, ok := symIDs[r.target]
			if !ok {
				panic("fixture: no symbol " + r.target)
			}
			o.SymbolID = id
		}
		outcomes = append(outcomes, o)
	}
	if err := reg.MarkResolutions(ctx, outcomes); err != nil {
		panic(err)
	}

	edges := make([]model.Edge, 0, len(b.edges))
	for _, e := range b.edges {
		src, ok1 := symIDs[e.src]
		dst, ok2 := symIDs[e.dst]
		if !ok1 || !ok2 {
			panic("fixture: edge names an unknown symbol: " + e.src + " -> " + e.dst)
		}
		source := model.SourceSourceCode
		if e.conf == model.ConfidenceExact {
			source = model.SourceCompiler
		}
		edges = append(edges, model.Edge{
			SrcID: src, DstID: dst, Kind: e.kind, Confidence: e.conf,
			Evidence: model.Evidence{
				FilePath: e.evFile,
				Range:    model.Range{Start: model.Position{Line: e.evLine}},
				Source:   source,
			},
		})
	}
	if _, err := reg.AddResolvedEdges(ctx, runID, edges); err != nil {
		panic(err)
	}
}

// answer calls a tool and returns its text, failing the test when the call
// itself could not be made.
func answer(t *testing.T, sess *sdk.ClientSession, tool string, args map[string]any) (string, *sdk.CallToolResult) {
	t.Helper()
	res := callTool(t, sess, tool, args)
	return resultText(res), res
}

// firstLine is the summary: the line under the SYSTEM METADATA banner, which
// is the line an agent acts on.
func firstLine(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return ""
	}
	return lines[1]
}

// rowCount is how many result rows an answer carries.
func rowCount(text string) int {
	n := 0
	inRows := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "=== "+sectionInferred):
			inRows = true
		case strings.HasPrefix(line, "=== "):
			inRows = false
		case inRows && strings.HasPrefix(line, "- "):
			n++
		}
	}
	return n
}

// wellBound adds enough bound references in lang that its binding rate clears
// the coverage floor, so a test about something else is not also a test of the
// low-coverage gate.
func (b *indexBuilder) wellBound(lang, ext string) {
	path := "pad/pad" + ext
	b.file(path, lang, 40, fn("padTarget", 1, 2))
	for i := 0; i < 20; i++ {
		b.ref(path, "padTarget", "", 10+i, registry.ResolutionResolved, symKey(path, "padTarget", 1))
	}
	b.edge(symKey(path, "padTarget", 1), symKey(path, "padTarget", 1), model.EdgeCalls, model.ConfidenceHigh, path, 10)
}
