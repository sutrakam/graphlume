package cli

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The `resolve` summary must not print per-tier counts of resolved REFERENCES under
// "Edges:", where they sum to the resolved references and not to the edge
// count on the line above (sinatra: 2,564 / 367 / 1 under "Edges: 2342", with
// 2,342 edge rows in the table and none of them low). And it must not call every
// unbound reference "calls outside this repository", which on redis is wrong
// for at least 6,806 calls to redis's own functions.
//
// The fixture makes the two numbers differ: Twice calls Save two times in one
// expression, which is two resolved references and one edge.
func TestResolveSummaryCountsReferencesAndEdgesSeparately(t *testing.T) {
	root := isolate(t)
	writeFiles(t, root, map[string]string{
		"svc/repo.go": "package svc\n\ntype Repo struct{}\n\nfunc (r *Repo) Save(id string) error { return nil }\n",
		"svc/pay.go": "package svc\n\ntype Pay struct{ repo *Repo }\n\n" +
			"func (p *Pay) Settle(id string) error { return p.repo.Save(id) }\n\n" +
			"func (p *Pay) Refund(id string) error { return p.repo.Save(id) }\n\n" +
			"func (p *Pay) Twice(id string) error { return either(p.repo.Save(id), p.repo.Save(id)) }\n\n" +
			"func either(a, b error) error { return a }\n\n" +
			"func (p *Pay) Log() { println(\"paid\") }\n",
	})
	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	raw, err := run(t, root, "--json", "resolve")
	if err != nil {
		t.Fatalf("resolve --json: %v\n%s", err, raw)
	}
	var rep struct {
		Resolved      int64            `json:"resolved"`
		Unresolved    int64            `json:"unresolved"`
		Edges         int64            `json:"edges"`
		ByConfidence  map[string]int64 `json:"by_confidence"`
		EdgesTotal    int64            `json:"edges_total"`
		EdgesByConfid map[string]int64 `json:"edges_by_confidence"`
	}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("resolve --json: %v\n%s", err, raw)
	}
	sum := func(m map[string]int64) (n int64) {
		for _, v := range m {
			n += v
		}
		return n
	}
	if rep.Resolved <= rep.EdgesTotal {
		t.Fatalf("fixture: %d resolved references and %d edges; the test needs more references than edges",
			rep.Resolved, rep.EdgesTotal)
	}
	if got := sum(rep.ByConfidence); got != rep.Resolved {
		t.Errorf("by_confidence sums to %d; it counts resolved references, of which there are %d", got, rep.Resolved)
	}
	if got := sum(rep.EdgesByConfid); got != rep.EdgesTotal {
		t.Errorf("edges_by_confidence sums to %d, edges_total is %d", got, rep.EdgesTotal)
	}

	// The edge rows themselves.
	q, err := run(t, root, "--json", "query",
		"SELECT confidence, count(*) AS n FROM edges WHERE index_run_id = :run AND kind = 'CALLS' GROUP BY confidence")
	if err != nil {
		t.Fatalf("query: %v\n%s", err, q)
	}
	var rows int64
	for _, m := range regexp.MustCompile(`"n":\s*"?(\d+)`).FindAllStringSubmatch(q, -1) {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		rows += n
	}
	if rows != rep.EdgesTotal {
		t.Errorf("edges_total = %d, the table holds %d CALLS edges\n%s", rep.EdgesTotal, rows, q)
	}

	// The text form: a second run binds nothing new and must still describe
	// the graph that is there, not print "Edges: 0".
	for pass := 1; pass <= 2; pass++ {
		if out, err := run(t, root, "index", "--full"); pass == 1 && err != nil {
			t.Fatalf("index --full: %v\n%s", err, out)
		}
		text, err := run(t, root, "resolve")
		if err != nil {
			t.Fatalf("resolve: %v\n%s", err, text)
		}
		flat := collapse(text)
		if !strings.Contains(flat, "Edges: "+strconv.FormatInt(rep.EdgesTotal, 10)+" ") {
			t.Errorf("pass %d: the Edges line does not give the %d edge rows:\n%s", pass, rep.EdgesTotal, text)
		}
		if strings.Contains(flat, "calls outside this repository") {
			t.Errorf("pass %d: unresolved references are still called calls outside the repository:\n%s", pass, text)
		}
		if !strings.Contains(flat, "no definition of that name is indexed") {
			t.Errorf("pass %d: the Unresolved line does not say what unresolved is:\n%s", pass, text)
		}
		// Whatever is listed under Edges must add up to it.
		after := text[strings.Index(text, "  Edges:"):]
		lines := strings.Split(after, "\n")
		var tiers int64
		for _, l := range lines[1:] {
			m := regexp.MustCompile(`^    (exact|high|medium|low):\s+(\d+)$`).FindStringSubmatch(l)
			if m == nil {
				break
			}
			n, _ := strconv.ParseInt(m[2], 10, 64)
			tiers += n
		}
		if tiers != rep.EdgesTotal {
			t.Errorf("pass %d: the tiers under Edges sum to %d, not %d:\n%s", pass, tiers, rep.EdgesTotal, text)
		}
	}
}
