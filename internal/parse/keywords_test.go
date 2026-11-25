package parse

import "testing"

// TestKeywordNamesAreNeverDefinitions pins the set per language: the reserved
// words are refused, and the words a language does allow as names are kept.
func TestKeywordNamesAreNeverDefinitions(t *testing.T) {
	refused := map[string][]string{
		"c":       {"if", "switch", "while", "return", "sizeof"},
		"cpp":     {"if", "catch", "new", "return"},
		"c_sharp": {"if", "return", "foreach", "lock", "using"},
		"java":    {"if", "synchronized", "new"},
		"go":      {"if", "for", "func", "range"},
		"rust":    {"if", "match", "loop", "fn"},
		"python":  {"if", "elif", "lambda", "return"},
		"kotlin":  {"if", "when", "fun"},
		"scala":   {"if", "match", "def"},
		"make":    {"if", "else", "endif", "ifeq", "ifneq", "ifdef", "ifndef", "include", "define", "endef", "export", "unexport", "override", "vpath"},
	}
	for lang, words := range refused {
		for _, w := range words {
			if !isKeywordName(lang, w) {
				t.Errorf("%s: %q should be refused as a definition name", lang, w)
			}
		}
	}
	kept := map[string][]string{
		"c_sharp":    {"get", "set", "value", "var", "Write", "If"},
		"java":       {"get", "record", "var", "Return"},
		"javascript": {"if", "delete", "catch", "default"}, // legal member names in JS: no set
		"typescript": {"if", "delete"},
		"ruby":       {"if", "end", "class"},
		"rust":       {"new", "lock", "select"},
		"cpp":        {"range", "begin", "end", "lock"},
		"go":         {"select2", "Range"},
	}
	for lang, words := range kept {
		for _, w := range words {
			if isKeywordName(lang, w) {
				t.Errorf("%s: %q is a legal name and must be kept", lang, w)
			}
		}
	}
}

// Return types and calls written in a region the C# grammar could not read were
// recorded as methods. The interface here wraps its default bodies in #if, the
// shape serilog uses; whatever the recovery produces, no method may be named
// after a keyword.
func TestCSharpRecoveryNeverNamesAKeyword(t *testing.T) {
	res := parse(t, "x.cs", `#if FEATURE_DEFAULT_INTERFACE
using System.Diagnostics;
#endif

namespace Serilog;

public interface ILogger
{
    ILogger ForContext(IEnumerable<ILogEventEnricher> enrichers)
#if FEATURE_DEFAULT_INTERFACE
    {
        if (enrichers == null!)
            return this;
        return ForContext(new SafeAggregateEnricher(enrichers));
    }
#else
        ;
#endif
}
`)
	for _, s := range res.Symbols {
		if isKeywordName("c_sharp", s.Name) {
			t.Errorf("symbol named after keyword: %s at line %d", s.Name, s.Range.Start.Line)
		}
	}
}
