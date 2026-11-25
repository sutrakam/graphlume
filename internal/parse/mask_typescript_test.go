package parse

import "testing"

func TestTSUsingAsAVariableDoesNotCostTheRestOfTheFile(t *testing.T) {
	for name, src := range map[string]string{
		"top level":  "const using = files.filter((f) => f)\nconsole.log(using.length)\nexport function after() { return 1 }\n",
		"in a block": "function f() {\n  const using = 1\n  return using\n}\nexport function after() { return 2 }\n",
		"tsx":        "export const A = () => { const using = [1]; return using.length }\nexport function after() { return 3 }\n",
	} {
		path := "a.ts"
		if name == "tsx" {
			path = "a.tsx"
		}
		res := parse(t, path, src)
		if res.Status != StatusOK {
			t.Errorf("%s: status %v with %d error nodes", name, res.Status, res.ErrorNodes)
		}
		found := false
		for _, s := range res.Symbols {
			if s.Name == "after" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the definition after the variable was lost", name)
		}
	}
}

func TestTSUsingMaskLeavesDeclarationsCommentsAndStrings(t *testing.T) {
	for name, src := range map[string]string{
		"declaration":       "using conn = open()\n",
		"await declaration": "async function f() { await using conn = open() }\n",
		"for declaration":   "for (using x of xs) {}\n",
		"comment":           "// using.x is fine here\n/* using\n.y */\n",
		"string":            "const s = 'using.length'\nconst t = \"using)\"\n",
		"template":          "const t = `we are using.it and ${a}`\n",
		"longer word":       "const reusing = 1\nconst usingIt = reusing + usingIt\n",
	} {
		if spans := findTSUsingIdentifiers([]byte(src)); len(spans) != 0 {
			t.Errorf("%s: %d span(s) masked, want none", name, len(spans))
		}
	}
	spans := findTSUsingIdentifiers([]byte("return using\n"))
	if len(spans) != 1 || spans[0].start != 7 || string(spans[0].repl) != "usinG" {
		t.Errorf("a plain use: %+v", spans)
	}
}
