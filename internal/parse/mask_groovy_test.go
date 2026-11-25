package parse

import (
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

func groovyParser(t *testing.T) *genericParser {
	t.Helper()
	entry := grammars.DetectLanguageByName("groovy")
	if entry == nil {
		t.Skip("no Groovy grammar")
	}
	p := newGenericParser(*entry, defaultParserConfig)
	if p.lang == nil {
		t.Skip("Groovy grammar did not load")
	}
	return p
}

func TestGroovyOneLineDocForms(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // the comments found, as written
	}{
		{"a one-line doc comment", "class A {\n    /** the size */\n    int size\n}\n", []string{"/** the size */"}},
		{"no text", "/***/\nclass A {}\n", []string{"/***/"}},
		{"a space", "class A {\n    /** */\n}\n", []string{"/** */"}},
		{"an empty ordinary comment", "class A {\n    /**/\n}\n", []string{"/**/"}},
		{"code after it on the line", "class A {\n    /** same line */ void a() {}\n}\n", []string{"/** same line */"}},
		{"code before it on the line", "class A {\n    int a /** trailing */\n}\n", []string{"/** trailing */"}},
		{"three stars", "/*** banner */\nclass A {}\n", []string{"/*** banner */"}},
		{"ending in three stars", "/** text **/\nclass A {}\n", []string{"/** text **/"}},
		{"two on one line", "/** a */ /** b */\nclass A {}\n", []string{"/** a */", "/** b */"}},
		{"several", "class A {\n  /** a */\n  int a\n  /** b */\n  int b\n}\n", []string{"/** a */", "/** b */"}},
		{"after a line comment's line", "// note\n/** doc */\nclass A {}\n", []string{"/** doc */"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spans := findGroovyOneLineDocs([]byte(c.src))
			var got []string
			for _, s := range spans {
				// The span is the part of the comment that changes; find the comment around it.
				lo := strings.LastIndex(c.src[:s.start+3], "/*")
				if lo < 0 {
					t.Fatalf("no comment around span %d", s.start)
				}
				hi := strings.Index(c.src[lo+2:], "*/") + lo + 4
				got = append(got, c.src[lo:hi])
			}
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestGroovyMaskLeavesOrdinaryAndMultiLineCommentsAlone(t *testing.T) {
	for name, src := range map[string]string{
		"an ordinary comment":                            "/* text */\nclass A {}\n",
		"a multi-line doc comment":                       "/**\n * text\n */\nclass A {}\n",
		"a doc comment, one line of text, several lines": "/** text\n */\nclass A {}\n",
		"a multi-line doc comment holding one":           "/**\n * see /** inner */ here\n */\nclass A {}\n",
		"a line comment":                                 "// /** text */\nclass A {}\n",
		"inside an ordinary comment":                     "/* a /** b */\nclass A {}\n",
		"a double-quoted string":                         "String s = \"/** x */\"\n",
		"a single-quoted string":                         "String s = '/** x */'\n",
		"a triple-quoted string":                         "String s = '''\n/** x */\n'''\n",
		"a triple double-quoted string":                  "String s = \"\"\"\n/** x */ and /** y */\n\"\"\"\n",
		"an escaped quote":                               "String s = \"a \\\" /** x */\"\n",
		"a slashy string":                                "def r = /a\\/** x */b/\n",
		"a slashy string in a call":                      "m(/\\/** x */+/)\n",
		"a dollar-slashy string":                         "def r = $/ /** x */ /$\n",
		"a path with stars":                              "String s = '/usr/**/lib'\n",
		"a glob in a string":                             "files('src/**/*.groovy')\n",
		"division":                                       "def x = a / b ** c\n",
	} {
		if got := findGroovyOneLineDocs([]byte(src)); len(got) != 0 {
			t.Errorf("%s: %d spans in %q", name, len(got), src)
		}
	}
	// A comment after a string that holds one is still found, once.
	src := "String s = \"/** x */\"\n/** y */\nclass A {}\n"
	if got := findGroovyOneLineDocs([]byte(src)); len(got) != 1 || got[0].start != strings.Index(src, "/** y")+2 {
		t.Errorf("got %v", got)
	}
}

func TestGroovyMaskKeepsEveryOffset(t *testing.T) {
	src := "package p\r\n\r\n/** the class */\r\nclass A {\r\n    /***/\r\n    int a\r\n    /**/\r\n    /** */\r\n    /**\r\n     * several lines\r\n     */\r\n    void b() {}\r\n}\r\n"
	spans := findGroovyOneLineDocs([]byte(src))
	if len(spans) != 4 {
		t.Fatalf("spans = %d, want 4", len(spans))
	}
	out := applyMaskSpans([]byte(src), spans)
	if len(out) != len(src) {
		t.Fatalf("length %d, want %d", len(out), len(src))
	}
	for i := range out {
		if (src[i] == '\n' || src[i] == '\r') && src[i] != out[i] || (out[i] == '\n' || out[i] == '\r') && src[i] != out[i] {
			t.Fatalf("line structure changed at byte %d", i)
		}
	}
	// Every one-line doc comment is an ordinary comment now; the multi-line one is untouched.
	text := string(out)
	for _, one := range []string{"/*  the class */", "/* */", "/*  */"} {
		if !strings.Contains(text, one) {
			t.Errorf("no %q in %q", one, text)
		}
	}
	if !strings.Contains(text, "/**\r\n     * several lines\r\n     */") {
		t.Error("the multi-line doc comment changed")
	}
	if strings.Contains(text, "/**/") || strings.Contains(text, "/** ") && !strings.Contains(text, "/**\r\n") {
		t.Errorf("a one-line doc comment survived: %q", text)
	}
	// A blanked /**/ must not leave an opening that is never closed.
	if strings.Count(text, "/*") != strings.Count(text, "*/") {
		t.Errorf("comment openers and closers differ in %q", text)
	}
}

const groovyOneLine = "package p\n\nclass Flat {\n    /** the size */\n    int size\n\n    void a() {}\n\n    void b() {}\n}\n"

// The grammar never closes a one-line doc comment, so the plain parse loses
// what follows; the mask reads the methods.
func TestGroovyOneLineDocIsReadAroundByTheMask(t *testing.T) {
	p := groovyParser(t)
	plain, _, err := p.parseOnce(t.Context(), []byte(groovyOneLine))
	if err != nil {
		t.Fatal(err)
	}
	plainTags, _, _ := p.tagTree(plain, []byte(groovyOneLine))
	plainDefs := 0
	for _, tg := range plainTags {
		if isDefinitionTag(tg.Kind) && (tg.Name == "a" || tg.Name == "b") {
			plainDefs++
		}
	}
	if plainDefs != 0 {
		t.Fatalf("the plain parse now reads the methods after a one-line doc comment; remove mask_groovy.go (%d found)", plainDefs)
	}
	res, err := p.Parse(t.Context(), "Flat.groovy", []byte(groovyOneLine))
	if err != nil {
		t.Fatal(err)
	}
	if res.ConstructsMasked != 1 {
		t.Fatalf("ConstructsMasked = %d, want 1", res.ConstructsMasked)
	}
	names := kindsByName(res)
	for _, want := range []string{"Flat", "a", "b"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing %s; got %v", want, names)
		}
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "read with 1 construct masked") {
		t.Errorf("the note is %v", res.Err)
	}
}

// Guard: a masked parse that loses a definition the plain parse had, or gains
// none, is refused; one that gains and loses none is kept. The text pairs are
// chosen so that the "masked" text is the one that reads worse or better.
func TestGroovyGuardJudgesTheMaskedParse(t *testing.T) {
	p := groovyParser(t)
	good := "class A {\n    void a() {}\n    void b() {}\n}\n"
	swallowed := "class A {\n    /** x */\n    void a() {}\n    void b() {}\n}\n"
	judge := func(plainSrc, maskedSrc string) bool {
		t.Helper()
		pt, _, err := p.parseOnce(t.Context(), []byte(plainSrc))
		if err != nil {
			t.Fatal(err)
		}
		mt, _, err := p.parseOnce(t.Context(), []byte(maskedSrc))
		if err != nil {
			t.Fatal(err)
		}
		ptags, _, _ := p.tagTree(pt, []byte(plainSrc))
		mtags, _, _ := p.tagTree(mt, []byte(maskedSrc))
		mc := &maskContext{language: "groovy", plainEarly: true, swallowed: true, plainParsed: strings.Index(plainSrc, "/**")}
		return maskGuard(pt.RootNode(), ptags, mt.RootNode(), mtags, map[string]bool{}, mc)
	}
	if !judge(swallowed, good) {
		t.Error("the guard refused a mask that recovers two methods")
	}
	if judge(good, swallowed) {
		t.Error("the guard kept a mask that loses two methods")
	}
	if judge(good, good) {
		t.Error("the guard kept a mask that gains nothing")
	}
}

// A clean file, and a file whose one-line doc comment does not hurt, are parsed
// once and returned untouched.
func TestGroovyCleanFileIsParsedOnceAndUntouched(t *testing.T) {
	p := groovyParser(t)
	for name, src := range map[string]string{
		"no comments":      "class A {\n    void a() {}\n}\n",
		"a multi-line doc": "class A {\n    /**\n     * doc\n     */\n    void a() {}\n}\n",
		"in a string":      "class A {\n    String s = \"/** x */\"\n    void a() {}\n}\n",
	} {
		if got := findGroovyOneLineDocs([]byte(src)); len(got) != 0 {
			t.Fatalf("%s: spans in a file with none", name)
		}
		tree, stop, err := p.parseOnce(t.Context(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		tags, _, _ := p.tagTree(tree, []byte(src))
		if mp := p.retryMasked(t.Context(), tree, stop, []byte(src), tags); mp != nil {
			t.Errorf("%s: the file was retried", name)
		}
		res, err := p.Parse(t.Context(), "A.groovy", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if res.ConstructsMasked != 0 || res.Status != StatusOK {
			t.Errorf("%s: masked=%d status=%s (%v)", name, res.ConstructsMasked, res.Status, res.Err)
		}
	}
}

// A one-line doc comment in a file that is broken for another reason, before
// the comment, is retried; the masked reading is kept only if it gains
// definitions, and the definitions of the plain parse all survive.
func TestGroovyMaskKeepsNothingItCannotImprove(t *testing.T) {
	p := groovyParser(t)
	src := "class A {\n    void a() {}\n}\n/** last */\n"
	res, err := p.Parse(t.Context(), "A.groovy", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	names := kindsByName(res)
	if _, ok := names["a"]; !ok {
		t.Errorf("lost a: %v", names)
	}
	if res.ConstructsMasked != 0 {
		t.Errorf("ConstructsMasked = %d: the mask gains no definition here and must be refused", res.ConstructsMasked)
	}
}
