package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkWalkWithAmbiguousExtensions times a walk of a tree in which a large
// share of the files need their content looked at to be identified, which is
// where the cost of language detection shows: it runs once for every file.
//
// The tree is the unfavourable case on purpose. Two thirds of it is C headers
// with nothing C++ in them, each of which makes every `.h` rule scan its whole
// window and find nothing; a real repository has far fewer of those per file
// walked. The files are a few kilobytes, like the headers of the projects the
// walk was measured on (git, redis).
//
// What to watch is the gap between the two sub-benchmarks: "plain" walks the
// same number of files of the same size under an extension detection settles
// without reading anything.
func BenchmarkWalkWithAmbiguousExtensions(b *testing.B) {
	const files = 1500
	body := strings.Repeat("int add_one(int value); /* a prototype and nothing else */\n", 60)

	for _, bc := range []struct {
		name string
		ext  func(i int) string
	}{
		{"plain", func(int) string { return ".c" }},
		{"ambiguous", func(i int) string {
			switch i % 6 {
			case 0:
				return ".c"
			case 1:
				return ".m"
			default:
				return ".h"
			}
		}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			root := b.TempDir()
			for i := 0; i < files; i++ {
				dir := filepath.Join(root, fmt.Sprintf("pkg%02d", i%25))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					b.Fatal(err)
				}
				name := filepath.Join(dir, fmt.Sprintf("file%04d%s", i, bc.ext(i)))
				if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			w, err := NewWalker(root, minimalCfg())
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for b.Loop() {
				found, err := w.Walk(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if len(found) != files {
					b.Fatalf("walked %d files, want %d", len(found), files)
				}
			}
		})
	}
}
