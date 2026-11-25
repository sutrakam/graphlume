package parse

import (
	"strings"
	"testing"
)

// maskedC returns the C source with every kernel-style candidate applied.
func maskedC(src string) string {
	return string(maskCandidates([]byte(src), findMacroCandidates([]byte(src), "c")))
}

func TestCMaskRewrites(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // the text after masking; "_" stands for a space in the expectation
	}{
		{"attribute word between type and name", "static int __init f(void) { }", "static int        f(void) { }"},
		{"attribute word after a pointer star type", "void __iomem *base;", "void         *base;"},
		{"attribute word in front of the type", "static __always_inline bool f(void) { }", "static                 bool f(void) { }"},
		{"annotation after the declarator", "static int table[4] __read_mostly;", "static int table[4]              ;"},
		{"annotation after a parameter list", "int f(int a) __must_hold(lk) { }", "int f(int a)                 { }"},
		{"a double-underscore type is a type", "static __u32 f(__be32 a) { }", "static __u32 f(__be32 a) { }"},
		{"a double-underscore function is a function", "int __helper(int a);\nint g(void) { return __helper(1); }", "int __helper(int a);\nint g(void) { return __helper(1); }"},
		{"a call after a block is a call", "void f(void) { if (x) { } __release(y); }", "void f(void) { if (x) { } __release(y); }"},
		{"type as a macro argument", "p = container_of(q, struct foo, m);", "p = container_of(q, __________, m);"},
		{"type with keywords", "DEFINE_PER_CPU(unsigned long, n);", "DEFINE_PER_CPU(_____________, n);"},
		{"sizeof keeps its type", "int n = sizeof(struct foo);", "int n = sizeof(struct foo);"},
		{"a prototype keeps void", "int f(void);", "int f(void);"},
		{"a macro after a string", "asm(\"1:\\n\" EX_TABLE(0b, 1b) : : );", "asm(\"1:\\n\"                  : : );"},
		{"a macro before a string", "printk(KERN_INFO \"up\\n\");", "printk(          \"up\\n\");"},
		{"extern C keeps its declaration", "extern \"C\" void f(void);", "extern \"C\" void f(void);"},
		{"a name that starts with digits", "f(cmd, 10000baseT_Full);", "f(cmd, _0000baseT_Full);"},
		{"numbers stay numbers", "x = 0x1fUL + 1e5 + 10ull + 0b101 + 3f;", "x = 0x1fUL + 1e5 + 10ull + 0b101 + 3f;"},
		{"a double-underscore function after an annotation", "void __printf(2, 3)\n__log(const char *f)\n{\n}", "void               \n__log(const char *f)\n{\n}"},
		{"two annotations in a row", "int f(int a) __acquires(x) __releases(y) { }", "int f(int a)                             { }"},
		{"asm_inline", "void f(void) { asm_inline volatile(\"nop\"); }", "void f(void) { asm        volatile(\"nop\"); }"},
		{"a comment inside a continued define", "#define M(x) \\\n  return 0; /* ok */ \\\n  end\n", "#define M(x) \\\n  return 0;          \\\n  end\n"},
		{"storage words before a declaration macro", "static DEFINE_LOCK(lk);", "       DEFINE_LOCK(lk);"},
		{"a statement macro with a block", "void f(void) {\n  each_item(p, h) {\n  }\n}", "void f(void) {\n  each_item(p, h);{\n  }\n}"},
		{"a macro function with parameter pairs (the types are overwritten as arguments of a call)", "SYSCALL_DEFINE2(poke, int, fd, long, val)\n{\n}", "void sys_poke(        ___  fd, ____  val)\n{\n}"},
		{"a macro function without parameters", "BPF_CALL_0(zero)\n{\n}", "void zero()     \n{\n}"},
		{"a test macro", "TEST_F(widget, resize)\n{\n}", "void widget_resize()  \n{\n}"},
		{"a macro at depth is not a function", "void f(void) {\n  SOME_MACRO(a) {\n  }\n}", "void f(void) {\n  SOME_MACRO(a);{\n  }\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maskedC(tc.src)
			if len(got) != len(tc.src) {
				t.Fatalf("length changed: %d -> %d", len(tc.src), len(got))
			}
			if strings.Count(got, "\n") != strings.Count(tc.src, "\n") {
				t.Fatalf("lines changed")
			}
			if got != tc.want {
				t.Fatalf("masked\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// The kernel rules apply to C only: C++ and Objective-C are masked as before.
func TestCMaskIsCOnly(t *testing.T) {
	src := "static int __init f(void) { }\nSYSCALL_DEFINE1(a, int, b)\n{\n}\n"
	for _, lang := range []string{"cpp", "objc"} {
		for _, c := range findMacroCandidates([]byte(src), lang) {
			if c.repl != nil || strings.HasPrefix(src[c.start:c.end], "__") {
				t.Errorf("%s: kernel-style candidate %q", lang, src[c.start:c.end])
			}
		}
	}
}
