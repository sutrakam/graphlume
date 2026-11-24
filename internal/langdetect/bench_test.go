package langdetect

import (
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

// The benchmarks exist to keep one promise: detection runs for every file in a
// repository, so the common case has to cost about what the library's table
// lookup costs by itself. BenchmarkLibraryAlone is that baseline.

func BenchmarkLibraryAlone(b *testing.B) {
	for b.Loop() {
		_ = grammars.DetectLanguage("resolver.go")
	}
}

func BenchmarkDetectUnambiguous(b *testing.B) {
	det, _ := New(nil)
	for b.Loop() {
		if det.NeedsContent("internal/resolve/resolver.go") {
			b.Fatal("a .go file should not need its content")
		}
		_ = det.Detect("internal/resolve/resolver.go", nil, nil)
	}
}

func BenchmarkDetectHeaderWorstCase(b *testing.B) {
	// A full window of C with no marker in it: every rule runs to the end.
	head := []byte(strings.Repeat("int add(int a, int b); /* plain C, nothing else */\n", HeadBytes/52))
	det, _ := New(nil)
	b.SetBytes(int64(len(head)))
	for b.Loop() {
		_ = det.Detect("include/plain.h", head, nil)
	}
}

func BenchmarkDetectObjectiveC(b *testing.B) {
	head := []byte("#import <Foundation/Foundation.h>\n\n@interface Foo : NSObject\n@end\n")
	det, _ := New(nil)
	for b.Loop() {
		_ = det.Detect("Foo.m", head, nil)
	}
}
