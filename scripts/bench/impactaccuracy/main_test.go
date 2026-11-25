//go:build bench

package main

import (
	"reflect"
	"testing"
)

func TestParseHunksReadsParentSideRanges(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -10,3 +10,5 @@ func A() {
-x
@@ -40 +42 @@
-y
+z
@@ -50,0 +53,2 @@ func B() {
+new
`
	got := parseHunks(diff)
	want := []hunk{
		{Start: 10, Count: 3, Added: 5},
		{Start: 40, Count: 1, Added: 1},
		{Start: 50, Count: 0, Added: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseHunks = %+v, want %+v", got, want)
	}
	if n := changedLines(got); n != 12 {
		t.Fatalf("changedLines = %d, want 12", n)
	}
}

func TestSeedSymbolsKeepsTheInnermostSymbol(t *testing.T) {
	syms := []symbol{
		{FQN: "pkg.Class", Kind: "class", StartLine: 1, EndLine: 100},
		{FQN: "pkg.Class.method", Kind: "method", StartLine: 10, EndLine: 20},
		{FQN: "pkg.Class.other", Kind: "method", StartLine: 30, EndLine: 40},
		{FQN: "pkg.free", Kind: "function", StartLine: 110, EndLine: 120},
		{FQN: "app.go#GET /users", Kind: "endpoint", StartLine: 115, EndLine: 115},
	}

	// A change inside a method seeds the method and not the class around it.
	got := seedSymbols(syms, []hunk{{Start: 12, Count: 2}}, false)
	if want := []string{"pkg.Class.method"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inside a method: got %v, want %v", got, want)
	}

	// A change to the class body outside every method seeds the class.
	got = seedSymbols(syms, []hunk{{Start: 25, Count: 1}}, false)
	if want := []string{"pkg.Class"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("class body: got %v, want %v", got, want)
	}

	// A change between two top-level symbols seeds nothing.
	if got = seedSymbols(syms, []hunk{{Start: 105, Count: 1}}, false); len(got) != 0 {
		t.Fatalf("between symbols: got %v, want none", got)
	}

	// A route registered inside a function does not displace the function.
	got = seedSymbols(syms, []hunk{{Start: 115, Count: 1}}, false)
	if want := []string{"pkg.free"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("route inside a function: got %v, want %v", got, want)
	}

	// A deleted file seeds every code definition it held.
	if got = seedSymbols(syms, nil, true); len(got) != 4 {
		t.Fatalf("deleted file: got %v, want the four definitions", got)
	}
}

func TestInsertionAfterTheLastLineIsOutsideTheSymbol(t *testing.T) {
	s := symbol{FQN: "f", StartLine: 10, EndLine: 20}
	if !s.touchedBy(hunk{Start: 15, Count: 0}) {
		t.Fatal("an insertion after line 15 is inside a symbol spanning 10-20")
	}
	if s.touchedBy(hunk{Start: 20, Count: 0}) {
		t.Fatal("an insertion after line 20 is outside a symbol ending at 20")
	}
	if s.touchedBy(hunk{Start: 9, Count: 0}) {
		t.Fatal("an insertion after line 9 is before a symbol starting at 10")
	}
}

func TestScoreCountsFilesOnce(t *testing.T) {
	predicted := map[string]bool{"a": true, "b": true, "c": true}
	truth := map[string]bool{"b": true, "d": true}
	got := score(predicted, truth)
	want := counts{Predicted: 3, TP: 1, FP: 2, FN: 1}
	if got != want {
		t.Fatalf("score = %+v, want %+v", got, want)
	}
	if p := got.precision(); p < 0.333 || p > 0.334 {
		t.Fatalf("precision = %v", p)
	}
	if r := got.recall(); r != 0.5 {
		t.Fatalf("recall = %v", r)
	}
	if f := got.f1(); f < 0.399 || f > 0.401 {
		t.Fatalf("f1 = %v", f)
	}
	if (counts{}).f1() != 0 {
		t.Fatal("an empty tally must score 0, not NaN")
	}
}

func TestScopesAreNested(t *testing.T) {
	for _, tc := range []struct {
		scope, kind string
		want        bool
	}{
		{"direct", kindDirect, true},
		{"direct", kindIndirect, false},
		{"direct+indirect", kindIndirect, true},
		{"direct+indirect", kindDispatch, false},
		{"all", kindDispatch, true},
	} {
		if got := inScope(tc.scope, tc.kind); got != tc.want {
			t.Errorf("inScope(%q, %q) = %v, want %v", tc.scope, tc.kind, got, tc.want)
		}
	}
}

func TestIsTestPath(t *testing.T) {
	tests := []string{
		"tests/test_basic.py", "pkg/store/store_test.go", "src/app.spec.ts",
		"src/app.test.tsx", "gson/src/test/java/com/google/gson/GsonTest.java",
		"lib/FooTests.cs", "tests/conftest.py", "integration/hello/e2e/app.ts",
		"src/__tests__/a.js", "module/BarIT.java",
	}
	for _, p := range tests {
		if !isTestPath(p) {
			t.Errorf("isTestPath(%q) = false, want true", p)
		}
	}
	code := []string{
		"src/flask/testing.py", "internal/store/store.go", "src/contest.ts",
		"gson/src/main/java/com/google/gson/Gson.java", "pkg/latest/api.go",
		"src/Attest.java", "lib/protest.rb",
	}
	for _, p := range code {
		if isTestPath(p) {
			t.Errorf("isTestPath(%q) = true, want false", p)
		}
	}
}
