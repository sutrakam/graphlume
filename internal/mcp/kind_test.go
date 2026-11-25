package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// constructorOnly is google/benchmark's shape: the class BenchmarkReporter was
// not indexed, a constructor named like it was, and a class with a method.
func constructorOnly(b *indexBuilder) {
	b.file("src/reporter.cc", "cpp", 80,
		method("BenchmarkReporter", 33, 36, 0).ownedBy("BenchmarkReporter"))
	b.file("src/widget.cc", "cpp", 80,
		typ("Widget", model.KindClass, 1, 40),
		method("draw", 5, 9, 0).ownedBy("Widget"))
	b.file("src/use.cc", "cpp", 40, fn("build", 1, 8))
	b.edge(symKey("src/use.cc", "build", 1), symKey("src/widget.cc", "Widget", 1),
		model.EdgeInstantiates, model.ConfidenceHigh, "src/use.cc", 4)
	b.wellBound("cpp", ".cc")
}

// TestAQuestionAboutTypesIsNotAnsweredZeroForAMethod: implementations
// said "0 implementing" about a constructor because the name selected it.
func TestAQuestionAboutTypesIsNotAnsweredZeroForAMethod(t *testing.T) {
	fs := serverFor(t, constructorOnly)

	text := mustAnswer(t, fs, "implementations", map[string]any{"name": "BenchmarkReporter"})
	first := firstLine(text)
	for _, want := range []string{leadUnknown + " — ", `no type named "BenchmarkReporter" is indexed`,
		"a method at src/reporter.cc:33", "none has an extends or implements relationship recorded"} {
		if !strings.Contains(first, want) {
			t.Errorf("first line does not say %q: %q", want, first)
		}
	}
	if strings.Contains(first, "0 implementing") || !strings.Contains(text, "outcome: "+outcomeNoSuchKind) {
		t.Errorf("a method's zero was answered for the type:\n%s", text)
	}
	if rowCount(text) != 0 {
		t.Errorf("the refusal carries rows a client could offer as results:\n%s", text)
	}

	// The caller choosing a definition by its place is honoured: it is theirs.
	chosen := mustAnswer(t, fs, "implementations",
		map[string]any{"name": "BenchmarkReporter", "path": "src/reporter.cc", "line": 33})
	if strings.Contains(firstLine(chosen), "no type named") {
		t.Errorf("an explicit path and line were refused: %q", firstLine(chosen))
	}
}

// TestACallableQuestionAboutAClassStillCountsTheConstructions: a class has
// callers in the sense that matters, and callers says what constructs it.
// What a class calls is a different matter: callees refuses.
func TestACallableQuestionAboutAClassStillCountsTheConstructions(t *testing.T) {
	fs := serverFor(t, constructorOnly)

	callers := mustAnswer(t, fs, "callers", map[string]any{"name": "Widget"})
	if strings.Contains(firstLine(callers), "no function or method") || !strings.Contains(callers, "construct") {
		t.Errorf("callers on a class lost the constructions:\n%s", callers)
	}

	callees := mustAnswer(t, fs, "callees", map[string]any{"name": "Widget"})
	if first := firstLine(callees); !strings.Contains(first, `no function or method named "Widget" is indexed`) ||
		!strings.Contains(first, "a class at src/widget.cc:1") {
		t.Errorf("callees on a class answered for it: %q", first)
	}
}

// TestAnOverriddenMethodStillHasImplementations: implementations is about
// types, and a method that something overrides has implementations too.
func TestAnOverriddenMethodStillHasImplementations(t *testing.T) {
	fs := serverFor(t, func(b *indexBuilder) {
		b.file("a/Base.java", "java", 30, typ("Base", model.KindClass, 1, 20), method("run", 5, 9, 0).ownedBy("Base"))
		b.file("a/Sub.java", "java", 30, typ("Sub", model.KindClass, 1, 20), method("run", 5, 9, 0).ownedBy("Sub"))
		b.edge(symKey("a/Sub.java", "run", 5), symKey("a/Base.java", "run", 5),
			model.EdgeExtends, model.ConfidenceHigh, "a/Sub.java", 5)
		b.wellBound("java", ".java")
	})
	text := mustAnswer(t, fs, "implementations", map[string]any{"name": "run", "path": "Base.java"})
	if strings.Contains(firstLine(text), "no type named") {
		t.Errorf("a method with overrides was refused as not a type: %q", firstLine(text))
	}
}
