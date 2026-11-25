package parse

import "testing"

func TestBashForWithoutInDoesNotCostTheRestOfTheScript(t *testing.T) {
	src := "#!/bin/sh\nmain() {\n  for arg do\n    echo \"$arg\"\n  done\n}\nafter() {\n  echo hi\n}\n"
	res := parse(t, "run.sh", src)
	if res.Status != StatusOK {
		t.Fatalf("status %v with %d error nodes", res.Status, res.ErrorNodes)
	}
	line := 0
	for _, s := range res.Symbols {
		if s.Name == "after" {
			line = s.Range.Start.Line
		}
	}
	if line != 7 {
		t.Errorf("after is defined at line %d, want 7", line)
	}
}

func TestBashMaskLeavesNormalLoops(t *testing.T) {
	for name, src := range map[string]string{
		"with in":      "for arg in a b c; do echo $arg; done\n",
		"c style":      "for ((i=0;i<3;i++)); do echo $i; done\n",
		"short name":   "for i do echo $i; done\n",
		"in a comment": "# for arg do\n",
	} {
		if spans := findBashForWithoutIn([]byte(src)); len(spans) != 0 {
			t.Errorf("%s: %d span(s), want none", name, len(spans))
		}
	}
	spans := findBashForWithoutIn([]byte("  for arg do\n"))
	if len(spans) != 1 || string(spans[0].repl) != "while :;do" || spans[0].start != 2 {
		t.Errorf("a loop without in: %+v", spans)
	}
}

func TestBashParamMessageDoesNotCostTheFunction(t *testing.T) {
	src := "#!/bin/bash\nmint() {\n    local role=\"${1:?mint: ROLE is required (ADMIN|CENTRAL|FE|AM)}\"\n    echo \"$role\"\n}\nafter() {\n    echo hi\n}\n"
	res := parse(t, "lib.sh", src)
	if res.Status != StatusOK {
		t.Fatalf("status %v with %d error nodes", res.Status, res.ErrorNodes)
	}
	found := false
	for _, s := range res.Symbols {
		found = found || s.Name == "after"
	}
	if !found {
		t.Error("the function after the expansion was lost")
	}
}

func TestBashParamMessageMaskStaysInsideTheMessage(t *testing.T) {
	for name, src := range map[string]string{
		"no message":      "x=${1:-default (a|b)}\n",
		"plain":           "echo ${name} | grep x\n",
		"in a comment":    "# ${1:?use (a|b)}\n",
		"after a comment": "echo hi # ${1:?use (a|b)}\n",
	} {
		if spans := findBashParamMessages([]byte(src)); len(spans) != 0 {
			t.Errorf("%s: %d span(s), want none", name, len(spans))
		}
	}
	src := []byte("x=${1:?a (b|c)} | cat\n")
	spans := findBashParamMessages(src)
	if len(spans) != 3 {
		t.Fatalf("want the ( | ) of the message only, got %+v", spans)
	}
	for _, sp := range spans {
		if sp.start < 7 || sp.start > 13 {
			t.Errorf("a character outside the message was masked: %d", sp.start)
		}
	}
}
