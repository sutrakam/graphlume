package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability/ownership"
	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// authoredHistory is a fixed commit list with authors, newest first.
type authoredHistory []ownership.Commit

func (h authoredHistory) Commits(_ context.Context, limit int) ([]ownership.Commit, error) {
	if len(h) > limit {
		return h[:limit], nil
	}
	return h, nil
}

func withOwners(h ownership.History) fullOption { return func(d *Deps) { d.Owners = h } }

const knowledgeDayOne = int64(1_700_000_000)

// authored builds n commits by one author, one a day from the given day.
func authored(n, fromDay int, name, email string, paths ...string) []ownership.Commit {
	out := make([]ownership.Commit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ownership.Commit{
			SHA: fmt.Sprintf("%s-%d", name, fromDay+i), AuthorName: name, AuthorEmail: email,
			When: knowledgeDayOne + int64(fromDay+i)*86400, Subject: "change", Paths: paths,
		})
	}
	return out
}

// seedKnowledge indexes two directories: svc (flow.go, store.go) and web.
func seedKnowledge(ctx context.Context, reg *registry.Registry, runID int64) {
	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		panic(err)
	}
	for _, p := range []string{"svc/flow.go", "svc/store.go", "web/page.go", "tools/gen.go"} {
		f := model.File{Path: p, Language: "go", ContentHash: p, ParseStatus: "ok", SizeBytes: 100}
		if _, err := w.AddFile(ctx, &f); err != nil {
			panic(err)
		}
	}
	_ = w.Flush(ctx)
	_ = w.Close()
}

// knowledgeFixture: svc is Robin's (8 of 10), web is shared three ways, and
// tools has too few commits to say anything.
func knowledgeFixture() authoredHistory {
	var h authoredHistory
	h = append(h, authored(8, 0, "Robin Vega", "robin@example.com", "svc/flow.go", "svc/store.go")...)
	h = append(h, authored(2, 10, "Sam Okafor", "sam@example.com", "svc/flow.go")...)
	h = append(h, authored(2, 20, "Robin Vega", "robin@example.com", "web/page.go")...)
	h = append(h, authored(2, 30, "Sam Okafor", "sam@example.com", "web/page.go")...)
	h = append(h, authored(2, 40, "Ash Berg", "ash@example.com", "web/page.go")...)
	h = append(h, authored(1, 50, "Ash Berg", "ash@example.com", "tools/gen.go")...)
	return h
}

func TestListKnowledgeRisksIsOnTheFullProfileOnly(t *testing.T) {
	full := newFullServer(t, seedKnowledge)
	if !containsTool(full.srv.Tools(), "busfactor") {
		t.Error("the full profile does not expose busfactor")
	}
	standard := newFullServer(t, seedKnowledge, withProfile(config.MCPStandard))
	if containsTool(standard.srv.Tools(), "busfactor") {
		t.Error("the standard profile exposes busfactor; it is a full-profile tool")
	}
}

func containsTool(tools []string, name string) bool {
	for _, n := range tools {
		if n == name {
			return true
		}
	}
	return false
}

func TestListKnowledgeRisksRanksTheMostConcentratedFirst(t *testing.T) {
	fs := newFullServer(t, seedKnowledge, withOwners(knowledgeFixture()))
	res := callTool(t, fs.sess, "busfactor", map[string]any{})
	text := resultText(res)
	if res.IsError {
		t.Fatalf("busfactor failed:\n%s", text)
	}

	svc, web := strings.Index(text, "- directory svc"), strings.Index(text, "- directory web")
	if svc < 0 || web < 0 || svc > web {
		t.Errorf("svc (bus factor 1) should be listed above web (bus factor 2):\n%s", text)
	}
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{
		"2 of 2 measured directory(ies) listed",
		"from 17 commit(s) in the last 12 month(s) by 3 author name(s)",
		"Bus factor of 1: 1 of the 2 measured",
		"1 more had fewer than 5 commits and were not measured",
		"bus factor 1: 10 commit(s) by 2 name(s); Robin Vega 80% (last change here 2023-11-21, idle 43d), Sam Okafor 20%",
		"bus factor 2: 6 commit(s) by 3 name(s)",
		"All 17 commit(s) reachable from HEAD were read",
		"unmeasured, not low risk",
		"shallow clone", "squash-merge", "Names are never merged",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "- directory tools") {
		t.Errorf("a directory with one commit must not be listed:\n%s", text)
	}
}

// The answer goes into a model's context. It must never carry an address, in
// the text or in the structured form.
func TestListKnowledgeRisksNeverReturnsAnAddress(t *testing.T) {
	h := knowledgeFixture()
	// One author whose git name IS their address, and one name sharing an
	// address with another, which the answer mentions by name only.
	h = append(h, authored(3, 60, "kit@example.org", "kit@example.org", "web/page.go")...)
	h = append(h, authored(1, 70, "R. Vega", "robin@example.com", "web/page.go")...)

	fs := newFullServer(t, seedKnowledge, withOwners(h))
	res := callTool(t, fs.sess, "busfactor", map[string]any{"min_commits": 1})
	text := resultText(res)
	if res.IsError {
		t.Fatalf("busfactor failed:\n%s", text)
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{text, string(structured)} {
		if strings.Contains(body, "@") || strings.Contains(body, "example.") {
			t.Errorf("an email address reached the answer:\n%s", body)
		}
	}
	flat := strings.Join(strings.Fields(text), " ")
	if !strings.Contains(flat, "'R. Vega' and 'Robin Vega'") || !strings.Contains(flat, "would be a guess") {
		t.Errorf("two names on one address should be named and left apart:\n%s", text)
	}
}

func TestListKnowledgeRisksHonoursItsArguments(t *testing.T) {
	fs := newFullServer(t, seedKnowledge, withOwners(knowledgeFixture()))

	one := resultText(callTool(t, fs.sess, "busfactor", map[string]any{"limit": 1}))
	if strings.Count(one, "- directory ") != 1 || !strings.Contains(one, "1 of 2 measured") ||
		!strings.Contains(strings.Join(strings.Fields(one), " "), "Pass offset=1 to read on") {
		t.Errorf("limit 1 should list one row and say how to read on:\n%s", one)
	}

	// Paging: the measured list is not capped at the server's item limit; the
	// next page is read with offset, and the total is counted.
	res := callTool(t, fs.sess, "busfactor", map[string]any{"limit": 1, "offset": 1})
	second := resultText(res)
	if strings.Count(second, "- directory ") != 1 || strings.Contains(second, "Pass offset=") {
		t.Errorf("offset 1 should list the second and last row:\n%s", second)
	}
	if strings.Contains(second, firstDirectory(one)) {
		t.Errorf("the second page repeats the first row:\n%s", second)
	}
	var page struct {
		Page *struct {
			Offset, Total, Returned, NextOffset int
		} `json:"page"`
		History *struct {
			Months  int  `json:"months"`
			Covered bool `json:"covered"`
			Commits int  `json:"commits"`
		} `json:"history_window"`
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &page); err != nil || page.Page == nil || page.Page.Total != 2 || page.Page.Offset != 1 {
		t.Errorf("page = %+v (%v), want offset 1 of 2", page.Page, err)
	}
	// The window is a period, stated in the answer for a screen to show.
	if page.History == nil || page.History.Months != 12 || !page.History.Covered || page.History.Commits == 0 {
		t.Errorf("history_window = %+v, want 12 months, covered", page.History)
	}
	if !strings.Contains(one, "in the last 12 month(s)") {
		t.Errorf("the summary does not name the period:\n%s", one)
	}

	narrowed := resultText(callTool(t, fs.sess, "busfactor", map[string]any{"path": "web/"}))
	if strings.Contains(narrowed, "- directory svc") || !strings.Contains(narrowed, "- directory web") {
		t.Errorf("path should narrow the answer to web/:\n%s", narrowed)
	}

	files := resultText(callTool(t, fs.sess, "busfactor", map[string]any{"files": true}))
	if !strings.Contains(files, "- file flow.go  svc/flow.go") || !strings.Contains(files, "measured file(s)") {
		t.Errorf("files=true should measure each file:\n%s", files)
	}

	all := resultText(callTool(t, fs.sess, "busfactor", map[string]any{"min_commits": 1}))
	if !strings.Contains(all, "- directory tools") {
		t.Errorf("min_commits=1 should measure the single-commit directory:\n%s", all)
	}

	window := resultText(callTool(t, fs.sess, "busfactor", map[string]any{"commits": 10}))
	if !strings.Contains(strings.Join(strings.Fields(window), " "), "Only the newest 10 commit(s) were read") {
		t.Errorf("a full window must say earlier history was not read:\n%s", window)
	}

	bad := callTool(t, fs.sess, "busfactor", map[string]any{"limit": -1})
	if !bad.IsError || !strings.Contains(resultText(bad), "invalid_argument") {
		t.Errorf("a negative limit must be rejected:\n%s", resultText(bad))
	}
}

// No history is a state of the world. The answer says nothing was measured,
// which is not the same as nothing being at risk.
func TestListKnowledgeRisksWithoutHistoryIsGuidance(t *testing.T) {
	fs := newFullServer(t, seedKnowledge)
	res := callTool(t, fs.sess, "busfactor", map[string]any{})
	text := strings.Join(strings.Fields(resultText(res)), " ")
	if res.IsError {
		t.Fatalf("no history is a state of the world, not a malfunction:\n%s", text)
	}
	if !strings.Contains(text, "No commit history is available") || !strings.Contains(text, "nothing was measured") {
		t.Errorf("the answer should say why nothing was measured:\n%s", text)
	}

	// A repository with no commits yet: history opens and is empty.
	empty := newFullServer(t, seedKnowledge, withOwners(authoredHistory{}))
	res = callTool(t, empty.sess, "busfactor", map[string]any{})
	if res.IsError || !strings.Contains(resultText(res), "No commit history could be read") {
		t.Errorf("an empty history should be explained:\n%s", resultText(res))
	}
}

// A long author name must not push a row past the per-item bound and take the
// figures after it with it.
func TestListKnowledgeRisksBoundsLongNames(t *testing.T) {
	long := strings.Repeat("Aurelia-Maximiliana ", 20)
	h := authoredHistory(authored(6, 0, long, "a@example.com", "svc/flow.go"))
	fs := newFullServer(t, seedKnowledge, withOwners(h))
	text := resultText(callTool(t, fs.sess, "busfactor", map[string]any{}))
	if !strings.Contains(text, "100% (last change here") {
		t.Errorf("the share and date were cut off behind a long name:\n%s", text)
	}
	if strings.Contains(text, strings.TrimSpace(long)) {
		t.Errorf("the name was not bounded:\n%s", text)
	}
}

// firstDirectory is the path of the first "- directory" row of an answer.
func firstDirectory(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "- directory ") {
			return l
		}
	}
	return "\x00"
}
