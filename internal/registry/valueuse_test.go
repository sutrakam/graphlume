package registry

import (
	"context"
	"reflect"
	"testing"
)

func TestIdentifierInCodeSkipsStringLiterals(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`p.then(handleResponse)`, true},
		{`t.Errorf("must have context when called with handleResponse", x)`, false},
		{`describe('app.handleResponse', () => {`, false},
		{`x := "a" + handleResponse`, true},
		{"msg := `handleResponse` + handleResponse", true},
		{`forget_handleResponse(handleResponse_x)`, false},
		{`"quoted \" still quoted handleResponse" ; handleResponse()`, true},
	} {
		if got := identifierInCode(tc.line, "handleResponse"); got != tc.want {
			t.Errorf("identifierInCode(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestUnrecordedMentionsLeavesOnlyWhatNoReferenceExplains(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	writeContent(t, r, run.ID, []fileSpec{
		{path: "a/app.js", lang: "javascript", hash: "u1",
			content: "import { handleResponse } from './x'\nfunction handleResponse() {}\nfunction go() { return p.then(handleResponse) }\nhandleResponse()\n"},
		{path: "b/t.js", lang: "javascript", hash: "u2",
			content: "it('handleResponse works', () => {})\nreg(handleResponse)\n"},
	}, nil)
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	// No reference rows are recorded here, so only the import, the definition
	// and the string are explained.
	got, err := r.UnrecordedMentions(ctx, run.ID, "handleResponse",
		[]DefSpan{{Path: "a/app.js", From: 2, To: 2}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Place{{"a/app.js", 3}, {"a/app.js", 4}, {"b/t.js", 2}}
	if !got.Known || !got.Complete || got.Total != 3 || !reflect.DeepEqual(got.Sample, want) {
		t.Errorf("UnrecordedMentions = %+v, want the three places %v", got, want)
	}

	none, err := r.UnrecordedMentions(ctx, run.ID, "neverWritten", nil)
	if err != nil || !none.Known || none.Total != 0 {
		t.Errorf("an absent name should be known with no places: %+v %v", none, err)
	}
	if unknown, _ := r.UnrecordedMentions(ctx, run.ID, "[]", nil); unknown.Known {
		t.Errorf("a name the tokenizer cannot index must be unknown: %+v", unknown)
	}
}

// A site the graph holds an edge for is recorded, even with no reference row
// behind it: the compiler pass records a function passed as a value as a
// REFERENCES edge only. Counting reference rows alone called those sites
// "not recorded" while references listed them.
func TestUnrecordedMentionsCountsEdgeEvidenceAsRecorded(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	app := "a/app.js"
	writeContent(t, r, run.ID, []fileSpec{
		{path: app, lang: "javascript", hash: "u1",
			content: "function handleResponse() {}\nfunction go() { return p.then(handleResponse) }\nreg(handleResponse)\n",
			syms: []symSpec{
				{fqn: app + ":handleResponse", name: "handleResponse", line: 1, kind: "function"},
				{fqn: app + ":go", name: "go", line: 2, kind: "function"},
			}},
	}, []edgeSpec{{
		srcFQN: app + ":go", dstFQN: app + ":handleResponse",
		kind: "REFERENCES", conf: "exact", source: "compiler", evFile: app, evLine: 2,
	}})
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.UnrecordedMentions(ctx, run.ID, "handleResponse", []DefSpan{{Path: app, From: 1, To: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Place{{app, 3}}; !got.Known || got.Total != 1 || !reflect.DeepEqual(got.Sample, want) {
		t.Errorf("UnrecordedMentions = %+v; want only line 3, since line 2 has a REFERENCES edge", got)
	}
}

func TestParseQualified(t *testing.T) {
	for _, tc := range []struct {
		in    string
		forms []string
		ok    bool
	}{
		{"Guard.AgainstNull", []string{"Guard.AgainstNull"}, true},
		{"Guard::AgainstNull", []string{"Guard.AgainstNull"}, true},
		{"Mailer#deliver", []string{"Mailer.deliver"}, true},
		{"Serilog.Guard.AgainstNull", []string{"Serilog.Guard.AgainstNull", "Guard.AgainstNull"}, true},
		{"AgainstNull", nil, false},
		{"src/Guard.cs:Guard.AgainstNull", nil, false},
		{".x", nil, false},
		{"a..b", nil, false},
	} {
		q, ok := ParseQualified(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseQualified(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && !reflect.DeepEqual(q.Forms(), tc.forms) {
			t.Errorf("ParseQualified(%q).Forms() = %v, want %v", tc.in, q.Forms(), tc.forms)
		}
	}
	if !MatchesFQN("src/Guard.cs:Guard.AgainstNull", "Guard.AgainstNull") ||
		MatchesFQN("src/Guard.cs:MyGuard.AgainstNull", "Guard.AgainstNull") ||
		!MatchesFQN("a.cs:Outer.Guard.AgainstNull", "Guard.AgainstNull") {
		t.Error("MatchesFQN does not match at a segment boundary")
	}
}
