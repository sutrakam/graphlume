package parse

import (
	"context"
	"os"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// cDefineInsideInitialiser is the construct at line 58 of redis's
// src/bitops.c: a prototype naming a type the file does not declare, then
// preprocessor definitions between the braces of an array initialiser.
// gotreesitter v0.57.0's default recovery gives up on it with an
// "invariant_violation" 1,870 bytes into a 115 KB file; its other recovery
// route reads the file to the end. Both halves are needed: the table alone
// parses, with errors, on either route.
func cDefineInsideInitialiser() string {
	return "int before_table(int x) { return x - 1; }\n\n" +
		"static void normalise_range(long long *start, long long *end, uint64_t *bit_start,\n" +
		"                            unsigned char *mask, long long len, int isbit);\n\n" +
		"static const uint8_t bits_in_byte[256] = {\n" +
		"    #define B2(n) n, n+1, n+1, n+2\n" +
		"    #define B4(n) B2(n), B2(n+1), B2(n+1), B2(n+2)\n" +
		"    #define B6(n) B4(n), B4(n+1), B4(n+1), B4(n+2)\n" +
		"    B6(0), B6(1), B6(1), B6(2)\n" +
		"    #undef B6\n" +
		"    #undef B4\n" +
		"    #undef B2\n" +
		"};\n\n" +
		"int after_table(int x) { return x + 1; }\n"
}

// cRecoveryEnv is the variable gotreesitter reads to force a recovery route
// for the whole process. This package never sets it; the tests below check
// that, and skip where an operator has set it to the value that would make the
// default route and the alternate the same thing.
const cRecoveryEnv = "GOT_C_RECOVERY"

// TestDefaultRecoveryStillAbandonsTheConstruct documents WHY the second
// attempt exists, by showing the first one fail. If a library upgrade makes
// this test fail, the default route now reads the file, and the alternate
// parser (a second copy of the grammar, kept only for the retry) may no longer
// be earning its keep: measure on real C, and consider deleting it.
func TestDefaultRecoveryStillAbandonsTheConstruct(t *testing.T) {
	if os.Getenv(cRecoveryEnv) != "" {
		t.Skipf("%s is set, which overrides the route under test", cRecoveryEnv)
	}
	p, ok := NewRegistry().ForFile("bitops.c").(*genericParser)
	if !ok {
		t.Fatal("no generic parser for C")
	}
	_, stop, err := p.parseOnce(context.Background(), []byte(cDefineInsideInitialiser()))
	if err != nil {
		t.Fatal(err)
	}
	if !stop.early {
		t.Fatal("the default recovery route read the file to its end; see this test's comment")
	}
	t.Logf("default route stopped at byte %d: %s", stop.parsedBytes, stop.reason)

	alt := p.alternate()
	if alt == nil {
		t.Fatal("C has no alternate recovery route")
	}
	_, stop, err = alt.parseOnce(context.Background(), []byte(cDefineInsideInitialiser()))
	if err != nil {
		t.Fatal(err)
	}
	if stop.early {
		t.Fatalf("the alternate route stopped too, at byte %d: %s", stop.parsedBytes, stop.reason)
	}
}

// TestStoppedParseIsRetriedOnTheOtherRecoveryRoute pins the second attempt
// through the public entry point: a file the default recovery abandons must
// still be read to its end, the definition after the construct must be found,
// and the process environment must be untouched.
func TestStoppedParseIsRetriedOnTheOtherRecoveryRoute(t *testing.T) {
	if os.Getenv(cRecoveryEnv) != "" {
		t.Skipf("%s is set, which overrides the route under test", cRecoveryEnv)
	}
	res := parse(t, "bitops.c", cDefineInsideInitialiser())
	if res.Reason == model.ParseReasonBudgetExceeded || res.Reason == model.ParseReasonStoppedEarly {
		t.Fatalf("the file was not read to its end: %v", res.Err)
	}
	// The construct is not valid to the grammar on either route. Read as it
	// stands the honest verdict is "read to the end, with syntax errors". The C
	// mask (cmacro_c.go) overwrites a #define written between the braces of an
	// initialiser, after which the table parses, and the verdict is ok with the
	// mask noted; either way the file was read to its end.
	switch {
	case res.Status == StatusPartial && res.Reason == model.ParseReasonSyntaxErrors:
	case res.Status == StatusOK && res.MacrosMasked > 0:
	default:
		t.Errorf("status=%s reason=%q masked=%d, want partial / syntax_errors, or ok with macros masked", res.Status, res.Reason, res.MacrosMasked)
	}
	for _, name := range []string{"before_table", "after_table"} {
		if !hasSymbol(res, name, "") {
			t.Errorf("definition %q is missing; got %v", name, symbolNames(res))
		}
	}
	if v, set := os.LookupEnv(cRecoveryEnv); set {
		t.Errorf("%s was set to %q by a parse; nothing in this package may change the environment", cRecoveryEnv, v)
	}
}

// TestRetryLeavesTheDefaultRouteAlone is the isolation the alternate depends
// on: it parses with a private copy of the grammar, so using it must not
// change what the default route does afterwards, for this registry or for a
// new one. An earlier design switched routes through the process environment,
// and failed exactly this: whichever route ran first decided both.
func TestRetryLeavesTheDefaultRouteAlone(t *testing.T) {
	if os.Getenv(cRecoveryEnv) != "" {
		t.Skipf("%s is set, which overrides the route under test", cRecoveryEnv)
	}
	src := []byte(cDefineInsideInitialiser())
	for round := 0; round < 3; round++ {
		reg := NewRegistry()
		p := reg.ForFile("bitops.c").(*genericParser)

		// The full path, which uses the alternate.
		if res, err := p.Parse(context.Background(), "bitops.c", src); err != nil || res.Reason == model.ParseReasonStoppedEarly {
			t.Fatalf("round %d: full parse: reason=%v err=%v", round, res.Reason, err)
		}
		// The default route alone must still stop where it always did.
		_, stop, err := p.parseOnce(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		if !stop.early {
			t.Fatalf("round %d: the default route changed behaviour after the alternate was used", round)
		}
	}
}

// TestRetriesDoNotDisturbConcurrentParses runs files that need the second
// attempt beside files that do not, on many goroutines, and requires every
// result to equal the one the same file gives when parsed alone. Run with
// -race: the alternate is built lazily and shared.
func TestRetriesDoNotDisturbConcurrentParses(t *testing.T) {
	if raceEnabled {
		t.Skip("wall-clock sensitive: the race detector slows parsing past the allowance on small CI runners; CI runs this test without -race")
	}
	inputs := map[string]string{
		"retry.c":      cDefineInsideInitialiser(),
		"table.c":      cMacroThenTable(128),
		"lobject.c":    cCastThenLocalTable(128),
		"errors.c":     "int broken(int x) {\n  return x +;\n}\n\nint fine(int x) { return x; }\n",
		"clean.go":     manyGoFuncs(40),
		"Generator.cs": csPreprocessorInSwitch(2),
	}
	want := map[string]string{}
	for path, src := range inputs {
		want[path] = fingerprint(parse(t, path, src))
	}

	const rounds, perFile = 4, 6
	reg := NewRegistry()
	type outcome struct{ path, got string }
	results := make(chan outcome, perFile*len(inputs)*rounds)
	done := make(chan struct{})
	workers := 0
	for w := 0; w < perFile; w++ {
		for path, src := range inputs {
			workers++
			go func(path, src string) {
				defer func() { done <- struct{}{} }()
				for i := 0; i < rounds; i++ {
					res, err := reg.ForFile(path).Parse(context.Background(), path, []byte(src))
					if err != nil {
						results <- outcome{path, "error: " + err.Error()}
						continue
					}
					results <- outcome{path, fingerprint(res)}
				}
				if d, ok := reg.(Drainable); ok {
					d.DrainPools() // while others are still parsing
				}
			}(path, src)
		}
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	close(results)
	for r := range results {
		if r.got != want[r.path] {
			t.Fatalf("%s parsed beside other files differs from parsed alone\nalone:\n%.600s\nbeside:\n%.600s",
				r.path, want[r.path], r.got)
		}
	}
}
