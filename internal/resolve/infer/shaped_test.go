package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// def builds an index entry the way the parser now writes one: a qualified
// name that states the owner, a kind, and the language of its file.
func def(id int64, kind, file, qual string, fileID int64) registry.SymbolIndexEntry {
	name := qual
	for i := len(qual) - 1; i >= 0; i-- {
		if qual[i] == '.' {
			name = qual[i+1:]
			break
		}
	}
	return registry.SymbolIndexEntry{
		ID: id, Name: name, FQN: file + ":" + qual, Kind: kind,
		FileID: fileID, FilePath: file, Language: languageOfPath(file),
	}
}

func callRef(id int64, name, kind, receiver string, line int) registry.Reference {
	return registry.Reference{ID: id, Name: name, Kind: "reference.call",
		ReceiverKind: kind, Receiver: receiver, Line: line}
}

// within gives a file one enclosing symbol with a qualified name, so `self`
// and bare calls know the type they are written in.
func within(id int64, file, qual string) []registry.EnclosingSymbol {
	return []registry.EnclosingSymbol{{ID: id, Name: qual, StartLine: 1, EndLine: 1000, FQN: file + ":" + qual}}
}

type boundTo struct {
	dst        int64
	confidence model.Confidence
	kind       model.EdgeKind
}

func edgesByLine(edges []model.Edge) map[int]boundTo {
	out := map[int]boundTo{}
	for _, e := range edges {
		out[e.Evidence.Range.Start.Line] = boundTo{e.DstID, e.Confidence, e.Kind}
	}
	return out
}

// TestQualifiedByAnImportBindsInsideThatPackageOrNowhere is the Go half of the
// defect: `errors.New(...)` bound to the package's own `New`. A qualifier that
// is an import names a package, and the call binds inside it — or, where the
// package is not in the repository, to nothing, recorded as external.
func TestQualifiedByAnImportBindsInsideThatPackageOrNowhere(t *testing.T) {
	idx := index(
		def(1, "function", "gin.go", "New", 10),
		def(2, "function", "internal/bytesconv/bytesconv.go", "StringToBytes", 11),
		def(3, "method", "errors.go", "Error.Error", 12),
	)
	layout := NewLayout(
		[]string{"go.mod", "gin.go", "errors.go", "auth.go", "internal/bytesconv/bytesconv.go"},
		[]string{"github.com/stretchr/testify"},
	)
	r := New(idx, Options{Layout: layout})

	edges, outs, st := resolveOne(t, r, registry.FileRefs{
		FileID: 20, Path: "auth.go", Language: "go",
		Imports: []registry.Import{
			{Path: "errors"},
			{Path: "github.com/gin-gonic/gin/internal/bytesconv"},
			{Path: "github.com/stretchr/testify/require"},
			{Path: "html/template", Alias: "tpl"},
		},
		Symbols: within(99, "auth.go", "BasicAuth"),
		Refs: []registry.Reference{
			callRef(1, "New", model.ReceiverName, "errors", 5),
			callRef(2, "StringToBytes", model.ReceiverName, "bytesconv", 6),
			callRef(3, "Error", model.ReceiverName, "require", 7),
			callRef(4, "New", model.ReceiverName, "tpl", 8),
			callRef(5, "New", model.ReceiverBare, "", 9),
		},
	})
	got := edgesByLine(edges)
	if len(got) != 2 {
		t.Fatalf("want two edges — the in-repository package and the bare call — got %+v", edges)
	}
	if got[6] != (boundTo{2, model.ConfidenceHigh, model.EdgeCalls}) {
		t.Errorf("bytesconv.StringToBytes bound as %+v, want symbol 2 at high", got[6])
	}
	if got[9] != (boundTo{1, model.ConfidenceHigh, model.EdgeCalls}) {
		t.Errorf("bare New() bound as %+v, want the package's own New at high", got[9])
	}
	for _, id := range []int64{1, 3, 4} {
		if o := outs[id]; o.Resolution != registry.ResolutionUnresolved || !o.External {
			t.Errorf("reference %d is %+v, want unresolved and external: its qualifier is the standard "+
				"library or a declared dependency", id, o)
		}
	}
	if st.External != 3 {
		t.Errorf("External = %d, want 3", st.External)
	}
}

// TestSelfSuperAndStatedTypeBindToMembers pins the three rules that need the
// qualified names shape.go writes: a member of the enclosing type, a member of
// the type the source states, and — for `super` — an ancestor's member and
// never the method the call is written in.
func TestSelfSuperAndStatedTypeBindToMembers(t *testing.T) {
	idx := index(
		def(1, "method", "a/Parser.java", "Parser.peek", 10),
		def(2, "method", "a/Parser.java", "Parser.clone", 10),
		def(3, "method", "a/JsonReader.java", "JsonReader.peek", 11),
		def(4, "class", "a/JsonReader.java", "JsonReader", 11),
		def(5, "method", "a/Other.java", "Other.peek", 12),
		def(6, "class", "a/Parser.java", "Parser", 10),
	)
	r := New(idx, Options{})

	edges, outs, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: "a/Parser.java", Language: "java",
		Symbols: within(2, "a/Parser.java", "Parser.clone"),
		Refs: []registry.Reference{
			callRef(1, "peek", model.ReceiverSelf, "this", 5),
			callRef(2, "peek", model.ReceiverType, "JsonReader", 6),
			callRef(3, "clone", model.ReceiverSuper, "super", 7),
			callRef(4, "peek", model.ReceiverType, "List", 8),
			callRef(5, "peek", model.ReceiverBare, "", 9),
		},
	})
	got := edgesByLine(edges)
	if got[5] != (boundTo{1, model.ConfidenceHigh, model.EdgeCalls}) {
		t.Errorf("this.peek() bound as %+v, want Parser.peek at high", got[5])
	}
	if got[6] != (boundTo{3, model.ConfidenceHigh, model.EdgeCalls}) {
		t.Errorf("reader.peek() with reader a JsonReader bound as %+v, want JsonReader.peek at high", got[6])
	}
	if e, ok := got[7]; ok {
		t.Errorf("super.clone() is bound to %+v; an ancestor's member is the only answer and none is known", e)
	}
	if e, ok := got[8]; ok {
		t.Errorf("a call on a List — a type this repository does not define — is bound to %+v", e)
	}
	if outs[4].Resolution != registry.ResolutionUnresolved {
		t.Errorf("the call on List is %q, want unresolved", outs[4].Resolution)
	}
	if got[9] != (boundTo{1, model.ConfidenceHigh, model.EdgeCalls}) {
		t.Errorf("bare peek() in a Java method bound as %+v, want the enclosing type's peek", got[9])
	}
}

// TestUnknownReceiverNeverBindsToAFreeFunctionOrItself: the rule every `value`,
// `expr` and unidentified `name` ends in. Members only, never the function
// the call is written in, and a name the language's own library declares is
// left alone however unique it is here.
func TestUnknownReceiverNeverBindsToAFreeFunctionOrItself(t *testing.T) {
	idx := index(
		def(1, "function", "crates/a/src/lib.rs", "Wrapper.is_match", 10),
		def(2, "function", "crates/a/src/lib.rs", "line_number", 10),
		def(3, "function", "crates/b/src/lib.rs", "Builder.line_number", 11),
		def(4, "function", "crates/b/src/lib.rs", "Thing.clone", 11),
		def(5, "function", "crates/b/src/lib.rs", "helper", 11),
	)
	r := New(idx, Options{})

	// Inside Wrapper.is_match: `self.inner.is_match(x)` on an inner value of
	// unknown type must not come back to Wrapper.is_match.
	edges, _, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: "crates/a/src/lib.rs", Language: "rust",
		Symbols: within(1, "crates/a/src/lib.rs", "Wrapper.is_match"),
		Refs: []registry.Reference{
			callRef(1, "is_match", model.ReceiverPath, "self.inner", 5),
			callRef(2, "line_number", model.ReceiverExpr, model.OpaqueReceiver, 6),
			callRef(3, "clone", model.ReceiverValue, "matcher", 7),
			callRef(4, "helper", model.ReceiverValue, "x", 8),
		},
	})
	got := edgesByLine(edges)
	if e, ok := got[5]; ok {
		t.Errorf("self.inner.is_match() inside is_match is bound to %+v: a self-loop on a name match", e)
	}
	if got[6] != (boundTo{3, model.ConfidenceLow, model.EdgeCalls}) {
		t.Errorf("a call on an unnameable expression bound as %+v, want the one member of that name, at low "+
			"and never the free function line_number", got[6])
	}
	if e, ok := got[7]; ok {
		t.Errorf("matcher.clone() is bound to %+v; every Rust value has a clone, and one in the "+
			"repository is not evidence", e)
	}
	if e, ok := got[8]; ok {
		t.Errorf("x.helper() is bound to %+v; a call made on a value cannot reach a free function", e)
	}
}

// TestCallsBindOnlyToCodeInACompatibleLanguage: the candidate filter. A call
// cannot denote a configuration key, a column or a dependency line, and it
// cannot cross into a language its own cannot call.
func TestCallsBindOnlyToCodeInACompatibleLanguage(t *testing.T) {
	entry := func(id int64, kind, file, lang string) registry.SymbolIndexEntry {
		return registry.SymbolIndexEntry{ID: id, Name: "text", FQN: file + ":text", Kind: kind,
			FileID: id, FilePath: file, Language: lang, Exported: true}
	}
	idx := index(
		entry(1, "column", "schema.prisma", "prisma"),
		entry(2, "configuration", "site.yaml", "yaml"),
		entry(3, "dependency", "package.json", "json"),
		entry(4, "function", "tool.py", "python"),
		entry(5, "field", "model.ts", "typescript"),
	)
	r := New(idx, Options{})
	edges, outs, st := resolveOne(t, r, registry.FileRefs{
		FileID: 50, Path: "handler.ts", Language: "typescript",
		Symbols: enclose(60, "handle"),
		Refs:    []registry.Reference{callRef(1, "text", model.ReceiverBare, "", 5)},
	})
	if len(edges) != 0 {
		t.Fatalf("a TypeScript call is bound to %+v", edges)
	}
	if outs[1].Resolution != registry.ResolutionUnresolved {
		t.Errorf("resolution = %q, want unresolved: nothing callable in a language TypeScript can call", outs[1].Resolution)
	}
	if st.CrossLanguageDropped != 5 {
		t.Errorf("CrossLanguageDropped = %d, want all 5 candidates counted", st.CrossLanguageDropped)
	}

	for _, pair := range []struct {
		from, to string
		want     bool
	}{
		{"typescript", "javascript", true}, {"tsx", "vue", true}, {"c", "cpp", true},
		{"java", "kotlin", true}, {"go", "go", true}, {"lua", "lua", true},
		{"go", "python", false}, {"c", "python", false}, {"typescript", "prisma", false},
		{"python", "yaml", false}, {"lua", "go", false},
	} {
		spec := NewSpecs().For(pair.from)
		c := registry.SymbolIndexEntry{Kind: "function", Language: pair.to}
		if got := spec.canBind(c, model.EdgeCalls); got != pair.want {
			t.Errorf("a call in %s to a function in %s: bindable = %v, want %v", pair.from, pair.to, got, pair.want)
		}
	}
}

// TestConstructorCallIsAnInstantiation: a call bound to a type is recorded as
// INSTANTIATES, in the languages where calling a type constructs it, and is
// not a candidate at all in the languages where it does not.
func TestConstructorCallIsAnInstantiation(t *testing.T) {
	idx := index(
		def(1, "class", "shop/models.py", "Account", 10),
		def(2, "class", "Shop/Account.cs", "Account", 11),
	)
	r := New(idx, Options{})

	edges, _, st := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: "shop/models.py", Language: "python",
		Symbols: enclose(60, "open_account"),
		Refs:    []registry.Reference{callRef(1, "Account", model.ReceiverBare, "", 5)},
	})
	if len(edges) != 1 || edges[0].Kind != model.EdgeInstantiates || edges[0].DstID != 1 {
		t.Fatalf("Account(...) in Python produced %+v, want one INSTANTIATES edge to the class", edges)
	}
	if st.Instantiations != 1 {
		t.Errorf("Instantiations = %d, want 1", st.Instantiations)
	}

	edges, _, _ = resolveOne(t, r, registry.FileRefs{
		FileID: 11, Path: "Shop/Account.cs", Language: "c_sharp",
		Symbols: enclose(61, "Open"),
		Refs:    []registry.Reference{callRef(1, "Account", model.ReceiverBare, "", 5)},
	})
	if len(edges) != 0 {
		t.Errorf("a bare Account(...) in C# is bound to %+v; C# constructs with `new`, and a call "+
			"cannot denote a type", edges)
	}
}

// TestRowWithNoRecordedShapeIsCappedAtLow: a row with no receiver_kind
// recorded says nothing about what a call was made on, so whatever the name tiers
// answer for it is the least this package can claim.
func TestRowWithNoRecordedShapeIsCappedAtLow(t *testing.T) {
	idx := index(def(1, "function", "a.go", "Save", 10))
	r := New(idx, Options{})
	edges, _, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: "a.go", Language: "go",
		Symbols: enclose(60, "Run"),
		Refs:    []registry.Reference{{ID: 1, Name: "Save", Kind: "reference.call", Line: 5}},
	})
	if len(edges) != 1 || edges[0].Confidence != model.ConfidenceLow {
		t.Fatalf("a call with no recorded shape produced %+v, want one edge at low", edges)
	}
}

func TestGoImportName(t *testing.T) {
	for path, want := range map[string]string{
		"errors":                                 "errors",
		"net/http":                               "http",
		"github.com/stretchr/testify/require":    "require",
		"github.com/go-playground/validator/v10": "validator",
		"gopkg.in/yaml.v3":                       "yaml",
		"github.com/mattn/go-isatty":             "isatty",
	} {
		if got := goImportName(path); got != want {
			t.Errorf("goImportName(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestLayoutAnswersWhereAPackageIs pins the directory index the external
// verdict rests on.
func TestLayoutAnswersWhereAPackageIs(t *testing.T) {
	l := NewLayout([]string{
		"go/go.mod", "go/shop.go", "go/web/web.go",
		"src/flask/__init__.py", "src/flask/json/__init__.py", "src/flask/cli.py",
		"java/src/main/java/com/shop/Parser.java",
	}, []string{"github.com/stretchr/testify"})

	if !l.HasDirUnderModule([]string{"web"}) {
		t.Error("go/web is not seen as a directory under the module rooted at go/")
	}
	if l.HasDirUnderModule([]string{"shop"}) {
		t.Error("java/.../com/shop was taken for a Go package directory; it is under no module root")
	}
	if !l.IsModuleRoot([]string{"go"}) || l.IsModuleRoot(nil) {
		t.Error("the module root is go/, where go.mod is, and not the repository root")
	}
	if !l.HasDirEnding([]string{"com", "shop"}) || l.HasDirEnding([]string{"org", "shop"}) {
		t.Error("HasDirEnding does not match a package path by its tail")
	}
	if l.HasTopLevelDir([]string{"json"}) {
		t.Error("src/flask/json was taken for the top-level module `json`; flask is a package, so it is flask.json")
	}
	if !l.HasTopLevelDir([]string{"flask"}) {
		t.Error("src/flask is not seen as the top-level package flask")
	}
	if !l.DeclaresDependency("github.com/stretchr/testify/require") || l.DeclaresDependency("github.com/other/mod") {
		t.Error("DeclaresDependency does not match an import beneath a declared module")
	}
	var none *Layout
	if none.Known() || none.HasDirEnding([]string{"x"}) || none.DeclaresDependency("x") {
		t.Error("a nil Layout must answer unknown to everything")
	}
}

// TestQualifierMustFitTheTypeItNames: `fs::DirEntry` is not this workspace's
// DirEntry, and `Rack::Response` is not Sinatra::Response.
func TestQualifierMustFitTheTypeItNames(t *testing.T) {
	rust := NewSpecs().For("rust")
	ruby := NewSpecs().For("ruby")
	dirEntry := typeRef{name: "DirEntry", filePath: "crates/ignore/src/walk.rs", qual: "DirEntry"}
	response := typeRef{name: "Response", filePath: "lib/sinatra/base.rb", qual: "Sinatra.Response"}
	for _, tc := range []struct {
		spec LanguageSpec
		segs []string
		t    typeRef
		want bool
	}{
		{rust, []string{"DirEntry"}, dirEntry, true},
		{rust, []string{"ignore", "DirEntry"}, dirEntry, true},
		{rust, []string{"crate", "DirEntry"}, dirEntry, true},
		{rust, []string{"fs", "DirEntry"}, dirEntry, false},
		{ruby, []string{"Sinatra", "Response"}, response, true},
		{ruby, []string{"Response"}, response, true},
		{ruby, []string{"Rack", "Response"}, response, false},
	} {
		if got := qualifierFits(tc.spec, tc.segs, tc.t); got != tc.want {
			t.Errorf("%s: %v against %s: fits = %v, want %v", tc.spec.Name, tc.segs, tc.t.qual, got, tc.want)
		}
	}
}

// TestEveryLanguageHasItsCallTraits: a spec's Calls is filled from the one
// table, for a named language and for one nothing was ever written about.
func TestEveryLanguageHasItsCallTraits(t *testing.T) {
	specs := NewSpecs()
	for _, name := range specs.Names() {
		if len(specs.For(name).Calls.Family) == 0 {
			t.Errorf("%s has no call family; it could bind a call to nothing, itself included", name)
		}
	}
	unknown := specs.For("fortran")
	if len(unknown.Calls.Family) != 1 || unknown.Calls.Family[0] != "fortran" {
		t.Errorf("a language in no family calls only itself, got %v", unknown.Calls.Family)
	}
	if got := unknown.Calls.Measured.unknownReceiver(); got != model.ConfidenceLow {
		t.Errorf("an unmeasured language's unknown-receiver rule is %s, want low", got)
	}
}
