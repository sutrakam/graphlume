package parse

import "testing"

func TestSwiftLongConditionListDoesNotCostTheFunction(t *testing.T) {
	for name, fn := range map[string]string{
		"three expressions":       "func nav(_ url: URL) {\n    if a == 1, b == 2, c == 3 {\n        launch(url)\n    }\n}\n",
		"guard of three":          "func nav(_ url: URL) {\n    guard a, b, c else { return }\n    launch(url)\n}\n",
		"binding then expression": "func nav(_ url: URL) {\n    if let host = url.host, flag, host != appHost {\n        launch(url)\n    }\n}\n",
		"multi-line":              "func nav(_ url: URL) {\n    if flag == true,\n       (scheme == \"http\" || scheme == \"https\"),\n       let host = url.host, host != appHost {\n        launch(url)\n    }\n}\n",
	} {
		res := parse(t, "a.swift", fn+"func after() {}\n")
		if res.Status != StatusOK {
			t.Errorf("%s: status %v with %d error nodes", name, res.Status, res.ErrorNodes)
			continue
		}
		found := false
		for _, s := range res.Symbols {
			found = found || s.Name == "after"
		}
		if !found {
			t.Errorf("%s: the definition after the statement was lost", name)
		}
	}
}

func TestSwiftConditionMaskOnlyTouchesLateExpressions(t *testing.T) {
	for name, src := range map[string]string{
		"two conditions":          "if a == 1, b == 2 {}\n",
		"last is a binding":       "if a == 1, b == 2, let c = d {}\n",
		"commas inside a call":    "if f(a, b, c, d) {}\n",
		"commas in an array":      "if [1, 2, 3, 4].isEmpty {}\n",
		"a plain statement":       "let x = foo(a, b, c)\n",
		"a string with commas":    "if s == \"a, b, c, d\" {}\n",
		"commas after the header": "if a { f(x, y, z) }\n",
	} {
		if spans := findSwiftConditionCommas([]byte(src)); len(spans) != 0 {
			t.Errorf("%s: %d span(s), want none: %+v", name, len(spans), spans)
		}
	}
	spans := findSwiftConditionCommas([]byte("if a, b, c {}\n"))
	if len(spans) != 1 || spans[0].start != 7 || string(spans[0].repl) != "&" {
		t.Errorf("a late expression: %+v", spans)
	}
}
