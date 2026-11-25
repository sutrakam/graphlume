package langdetect

import (
	"strings"
	"testing"
)

func TestMaskCplusplusGuards(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// hidden and visible are substrings that must be blanked and kept.
		hidden, visible []string
	}{
		{
			name:    "ifdef block",
			in:      "int a;\n#ifdef __cplusplus\nextern \"C\" {\nnamespace n {\n#endif\nint b;\n",
			hidden:  []string{"extern", "namespace n"},
			visible: []string{"int a;", "#ifdef __cplusplus", "#endif", "int b;"},
		},
		{
			name:    "if defined, with and without parentheses",
			in:      "#if defined(__cplusplus)\nclass A;\n#endif\n# if defined __cplusplus\nclass B;\n# endif\nint c;\n",
			hidden:  []string{"class A;", "class B;"},
			visible: []string{"int c;"},
		},
		{
			name:    "the else branch is what C sees",
			in:      "#ifdef __cplusplus\n#include <cstring>\n#else\n#include <string.h>\n#endif\n",
			hidden:  []string{"<cstring>"},
			visible: []string{"<string.h>", "#else"},
		},
		{
			name:    "a version test opens a block too",
			in:      "#if __cplusplus > 199711L\n#include <cstdint>\n#else\n#include <stdint.h>\n#endif\n",
			hidden:  []string{"<cstdint>"},
			visible: []string{"<stdint.h>"},
		},
		{
			name:    "nested conditionals stay hidden until the guard closes",
			in:      "#ifdef __cplusplus\n#if defined(_MSC_VER)\nclass A;\n#else\nclass B;\n#endif\nclass C;\n#endif\nint d;\n",
			hidden:  []string{"class A;", "class B;", "class C;"},
			visible: []string{"int d;"},
		},
		{
			name:    "negated conditions hide nothing",
			in:      "#ifndef __cplusplus\ntypedef int a;\n#endif\n#if !defined(__cplusplus)\ntypedef int b;\n#endif\n",
			visible: []string{"typedef int a;", "typedef int b;"},
		},
		{
			name:    "an elif on c++ reopens the block",
			in:      "#if defined(FOO)\nint a;\n#elif defined(__cplusplus)\nclass B;\n#endif\n#ifdef __cplusplus\nclass C;\n#elif __cplusplus > 1\nclass D;\n#else\nint e;\n#endif\n",
			hidden:  []string{"class C;", "class D;"},
			visible: []string{"int a;", "int e;"},
		},
		{
			name:    "a guard cut off by the end of the window",
			in:      "int a;\n#ifdef __cplusplus\nnamespace n {\nclass Cut",
			hidden:  []string{"namespace n", "class Cut"},
			visible: []string{"int a;"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(maskCplusplusGuards([]byte(tc.in)))
			if len(got) != len(tc.in) {
				t.Fatalf("length changed from %d to %d: offsets must be preserved", len(tc.in), len(got))
			}
			if strings.Count(got, "\n") != strings.Count(tc.in, "\n") {
				t.Errorf("line structure changed:\n%s", got)
			}
			for _, h := range tc.hidden {
				if strings.Contains(got, h) {
					t.Errorf("%q should be hidden:\n%s", h, got)
				}
			}
			for _, v := range tc.visible {
				if !strings.Contains(got, v) {
					t.Errorf("%q should be visible:\n%s", v, got)
				}
			}
		})
	}

	t.Run("no guard, no copy", func(t *testing.T) {
		for _, in := range []string{
			"int f(void);\n",
			"// mentions __cplusplus in a comment only\nint f(void);\n",
			"#ifndef __cplusplus\nint f(void);\n#endif\n",
		} {
			src := []byte(in)
			got := maskCplusplusGuards(src)
			if len(got) == 0 || &got[0] != &src[0] {
				t.Errorf("%q: a header with nothing to hide was copied", in)
			}
		}
	})

	t.Run("the input is never modified", func(t *testing.T) {
		in := "#ifdef __cplusplus\nclass A;\n#endif\n"
		src := []byte(in)
		_ = maskCplusplusGuards(src)
		if string(src) != in {
			t.Errorf("the caller's buffer was written to: %q", src)
		}
	})
}
