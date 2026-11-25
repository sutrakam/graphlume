package parse

import "testing"

func TestKotlinPostfixStatementDoesNotCostTheRestOfTheFile(t *testing.T) {
	src := "fun f() {\n  var i = 0\n  while (i < 3) {\n    if (c) { i++; continue }\n    i--; g()\n  }\n}\nfun after() {}\n"
	res := parse(t, "a.kt", src)
	if res.Status != StatusOK {
		t.Fatalf("status %v with %d error nodes", res.Status, res.ErrorNodes)
	}
	line := 0
	for _, s := range res.Symbols {
		if s.Name == "after" {
			line = s.Range.Start.Line
		}
	}
	if line != 8 {
		t.Errorf("after is defined at line %d, want 8: offsets must not move", line)
	}
}

func TestKotlinCommentBeforeAbstractClass(t *testing.T) {
	// The plain grammar fails when the comment follows the package line.
	src := "package com.x.wifi\n/**\n * Shared pre-flight.\n *\n * Ported from `a.b`.\n */\nabstract class EspTouch : Base() {\n    fun run() {}\n}\n"
	res := parse(t, "a.kt", src)
	if res.Status != StatusOK {
		t.Fatalf("status %v with %d error nodes", res.Status, res.ErrorNodes)
	}
	var cls, fn int
	for _, s := range res.Symbols {
		switch s.Name {
		case "EspTouch":
			cls = s.Range.Start.Line
		case "run":
			fn = s.Range.Start.Line
		}
	}
	if cls != 7 || fn != 8 {
		t.Errorf("class at line %d, run at line %d; want 7 and 8", cls, fn)
	}
}

func TestKotlinMaskLeavesStringsCommentsAndSameLineComments(t *testing.T) {
	for name, src := range map[string]string{
		"string":      "val s = \"i++; x\"\nval r = \"\"\"a--; b\"\"\"\n",
		"comment":     "// i++; x\n/* j--; y */\n",
		"char":        "val c = ';'\n",
		"no mask":     "fun f() { i += 1; g() }\n",
		"same line":   "package p\n/** d */ abstract class A\n",
		"not a block": "package p\n// d\nabstract class A\n",
	} {
		if spans := findKotlinMasks([]byte(src)); len(spans) != 0 {
			t.Errorf("%s: %d span(s), want none: %+v", name, len(spans), spans)
		}
	}
	spans := findKotlinMasks([]byte("fun f() { i++; g() }\n"))
	if len(spans) != 1 || string(spans[0].repl) != "+1;" {
		t.Errorf("a postfix statement: %+v", spans)
	}
}
