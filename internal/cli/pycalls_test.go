package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Python call-resolution ground truth, fixtures/pycalls.
//
// Every interesting call site in that fixture says, in a comment on its own
// line, which definition it reaches — or that it is dynamic and nothing may
// claim it at `high`. This test indexes a copy, resolves it, and scores every
// marked call. See fixtures/pycalls/README.md for the markers and for what each
// file is there to catch.
//
// It asserts three things, in decreasing order of how much they matter:
//
//  1. NOTHING is wrong at `high`. The fixture is small enough that a single
//     wrong edge is a regression with a name, and `high` is the tier a consumer
//     is told to act on.
//  2. No dynamic call is claimed at `high`. A call through a parameter, a
//     reassigned variable or a dispatch table has no static answer, and a
//     confident one is a guess.
//  3. The calls that ARE found stay found. The counts are pinned, so a change
//     that silently drops a shape fails here rather than in a recall figure
//     somebody reads a month later. Raising a pin is how an improvement is
//     recorded; lowering one needs a reason in the commit.

var (
	pyDefMarker  = regexp.MustCompile(`#\s*def\s+(\S+)\s*$`)
	pyCallMarker = regexp.MustCompile(`#\s*call\s+(\S+)\s*->\s*(\S+)\s*$`)
)

type pyCallSite struct {
	file   string
	line   int
	name   string
	target string // a def marker's ID, or "?" for a dynamic call
}

type pyDefSite struct {
	file string
	line int
}

// readPyCallMarkers collects the fixture's hand-written ground truth.
func readPyCallMarkers(t *testing.T, root string) (map[string]pyDefSite, []pyCallSite) {
	t.Helper()
	defs := map[string]pyDefSite{}
	var calls []pyCallSite
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == IndexDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".py") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		f, err := os.Open(p) //nolint:gosec // a fixture copy under t.TempDir
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for line := 1; sc.Scan(); line++ {
			text := sc.Text()
			if m := pyDefMarker.FindStringSubmatch(text); m != nil {
				if prev, dup := defs[m[1]]; dup {
					t.Fatalf("def marker %q at %s:%d repeats %s:%d", m[1], rel, line, prev.file, prev.line)
				}
				defs[m[1]] = pyDefSite{file: rel, line: line}
			}
			if m := pyCallMarker.FindStringSubmatch(text); m != nil {
				calls = append(calls, pyCallSite{file: rel, line: line, name: m[1], target: m[2]})
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c.target == "?" {
			continue
		}
		if _, ok := defs[c.target]; !ok {
			t.Fatalf("%s:%d names target %q, which no def marker declares", c.file, c.line, c.target)
		}
	}
	return defs, calls
}

// pyCallsRoot copies fixtures/pycalls into a temporary project.
func pyCallsRoot(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "pycalls"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("pycalls fixture missing at %s: %v", src, err)
	}
	dst := isolate(t)
	if err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		b, err := os.ReadFile(p) //nolint:gosec // a fixture inside this repository
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestPythonCallGroundTruth(t *testing.T) {
	root := pyCallsRoot(t)
	for _, args := range [][]string{{"init"}, {"index"}, {"resolve", "--no-scip"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v on the fixture: %v\n%s", args, err, out)
		}
	}
	defs, calls := readPyCallMarkers(t, root)
	db := corpusDB(t, root)

	type edge struct {
		confidence, file, detail string
		line                     int
	}
	// A call site is answered by a CALLS edge, or — where what is called is a
	// class, and the call constructs it — by an INSTANTIATES edge. The two
	// kinds are checked against the target's own kind, so a constructor call
	// filed under CALLS, or a function call under INSTANTIATES, fails here.
	edgesAt := func(c pyCallSite) []edge {
		rows, err := db.Query(`
			SELECT e.confidence, f.path, s.start_line, COALESCE(e.detail, ''), e.kind, s.kind
			  FROM edges e
			  JOIN symbols s ON s.id = e.dst_id
			  JOIN files f ON f.id = s.file_id
			 WHERE e.kind IN ('CALLS', 'INSTANTIATES') AND e.source = 'source_code'
			   AND e.evidence_file = ? AND e.evidence_line = ? AND s.name = ?
			 ORDER BY e.id`, c.file, c.line, c.name)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []edge
		for rows.Next() {
			var e edge
			var edgeKind, targetKind string
			if err := rows.Scan(&e.confidence, &e.file, &e.line, &e.detail, &edgeKind, &targetKind); err != nil {
				t.Fatal(err)
			}
			if (targetKind == "class") != (edgeKind == "INSTANTIATES") {
				t.Errorf("%s:%d: %s edge to a %s named %s; a call to a class is an instantiation and nothing else is",
					c.file, c.line, edgeKind, targetKind, c.name)
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	right := map[string]int{}
	wrong := map[string]int{}
	var known, dynamic, missed int
	var missedAt []string
	for _, c := range calls {
		edges := edgesAt(c)
		if c.target == "?" {
			dynamic++
			for _, e := range edges {
				if e.confidence == "high" {
					t.Errorf("%s:%d: dynamic call to %s claimed at high -> %s:%d (%s)",
						c.file, c.line, c.name, e.file, e.line, e.detail)
				}
			}
			continue
		}
		known++
		if len(edges) == 0 {
			missed++
			missedAt = append(missedAt, c.file+":"+c.name+" -> "+c.target)
			continue
		}
		want := defs[c.target]
		hit := false
		for _, e := range edges {
			if e.file == want.file && e.line == want.line {
				right[e.confidence]++
				hit = true
				break
			}
		}
		if hit {
			continue
		}
		e := edges[0]
		wrong[e.confidence]++
		if e.confidence == "high" {
			t.Errorf("%s:%d: %s bound at high to %s:%d (%s); it reaches %s at %s:%d",
				c.file, c.line, c.name, e.file, e.line, e.detail, c.target, want.file, want.line)
		} else {
			t.Logf("%s:%d: %s bound at %s to %s:%d (%s); it reaches %s",
				c.file, c.line, c.name, e.confidence, e.file, e.line, e.detail, c.target)
		}
	}
	sort.Strings(missedAt)
	t.Logf("%d call sites with a known target, %d dynamic", known, dynamic)
	t.Logf("high: %d right, %d wrong; medium: %d right, %d wrong; low: %d right, %d wrong; missed %d",
		right["high"], wrong["high"], right["medium"], wrong["medium"], right["low"], wrong["low"], missed)
	for _, m := range missedAt {
		t.Logf("missed: %s", m)
	}

	// The pins. See the comment at the top of this file before changing one.
	const (
		wantKnown             = pyCallsKnown
		wantDynamic           = pyCallsDynamic
		wantRightHigh         = pyCallsRightHigh
		wantMissedMax         = pyCallsMissedMax
		wantWrongBelowHighMax = pyCallsWrongBelowHighMax
	)
	if known != wantKnown || dynamic != wantDynamic {
		t.Errorf("fixture has %d known and %d dynamic call sites, the pins say %d and %d: "+
			"a marker was added or lost", known, dynamic, wantKnown, wantDynamic)
	}
	if right["high"] < wantRightHigh {
		t.Errorf("%d call sites right at high, want at least %d", right["high"], wantRightHigh)
	}
	if missed > wantMissedMax {
		t.Errorf("%d call sites produced no edge, want at most %d", missed, wantMissedMax)
	}
	if n := wrong["medium"] + wrong["low"]; n > wantWrongBelowHighMax {
		t.Errorf("%d call site(s) wrong below high, want at most %d", n, wantWrongBelowHighMax)
	}
}

// What the fixture measured when these were last set: 55 of 57 known call
// sites right at high and none wrong there, no dynamic call claimed at high.
//
// The two it does not get are both honest. `Tree.Node().walk()` is a call on
// the result of a call, which nothing here types, and two classes in the file
// declare `walk` — so it is missed. `service.charge(...)` is called on what a
// function RETURNED, so the receiver's class is unknown; the name-only tier
// binds it to the one `charge` in an imported module, which is the subclass's
// override rather than the method that runs, and reports it at medium. That
// wrong medium edge is the fixture's one example of why an unknown receiver is
// never reported at high.
//
// Before the Python scope analysis the same fixture scored 26 right and 9
// WRONG at high, 4 of its 13 dynamic calls claimed at high, and 12 missed.
const (
	pyCallsKnown             = 57
	pyCallsDynamic           = 13
	pyCallsRightHigh         = 55
	pyCallsMissedMax         = 1
	pyCallsWrongBelowHighMax = 1
)
