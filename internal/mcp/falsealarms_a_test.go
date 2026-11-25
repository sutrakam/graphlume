package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// These tests hold the text-based warnings to the standard a warning must meet
// to be worth reading: each fires when its evidence is real and only then. A
// second measurement on fourteen projects found each of them firing on a
// correct answer; every case below is one of those, and beside each is the case
// that must KEEP firing, because a check that never fires is no better.

// ---------------------------------------------------------------- A: a function passed as a value

// goFuncMentionedInProse is cobra's CompErrorln: one caller, correct, and the
// name written on a Markdown page and in a workflow file.
func goFuncMentionedInProse(b *indexBuilder) {
	b.fileText("completions.go", "go", strings.Join([]string{
		"package cobra",
		"func CompErrorln(s string) {}",
		"func complete() {",
		"	CompErrorln(\"x\")",
		"}",
		"",
	}, "\n"), fn("CompErrorln", 2, 2), fn("complete", 3, 5))
	b.call(symKey("completions.go", "complete", 3), symKey("completions.go", "CompErrorln", 2),
		"CompErrorln", "", "completions.go", 4, model.ConfidenceHigh)
	b.fileText("site/content/completions/_index.md", "markdown", "Use CompErrorln(msg) to print.\n    CompErrorln\n")
	b.fileText("CHANGELOG.md", "markdown", "- added CompErrorln\n")
	b.fileText(".github/workflows/ci.yml", "yaml", "steps:\n  - run: CompErrorln\n")
	b.wellBound("go", ".go")
}

func TestAMarkdownOrWorkflowLineIsNotAFunctionPassedAsAValue(t *testing.T) {
	fs := serverFor(t, goFuncMentionedInProse)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"callers", map[string]any{"name": "CompErrorln"}},
		{"impact", map[string]any{"symbol": "CompErrorln"}},
		{"tests", map[string]any{"symbol": "CompErrorln"}},
		{"plan", map[string]any{"symbol": "CompErrorln"}},
	} {
		text := mustAnswer(t, fs, tc.tool, tc.args)
		first := firstLine(text)
		if strings.Contains(first, "passed as a value") || strings.HasPrefix(first, leadIncomplete) {
			t.Errorf("%s doubts a correct answer because a Markdown or YAML file says the name: %q", tc.tool, first)
		}
		if strings.Contains(text, "_index.md") || strings.Contains(text, "ci.yml") || strings.Contains(text, "CHANGELOG.md") {
			t.Errorf("%s names a prose or configuration file as a place the function may be passed:\n%s", tc.tool, text)
		}
	}
}

// passedByNameBesideProse is the same shape with a real use: only the source
// file's place is counted, whatever the documentation says.
func passedByNameBesideProse(b *indexBuilder) {
	passedByName(b)
	b.fileText("README.md", "markdown", "Pass handleResponse to then.\n")
	b.fileText("docs/app.rst", "rst", "handleResponse is passed by name.\n")
}

func TestARealValueUseStillFiresAndProseBesideItIsNotCounted(t *testing.T) {
	fs := serverFor(t, passedByNameBesideProse)
	callers := mustAnswer(t, fs, "callers", map[string]any{"name": "handleResponse"})
	first := firstLine(callers)
	if !strings.HasPrefix(first, leadIncomplete+" — ") ||
		!strings.Contains(first, "its name appears in 1 other place(s) that are not recorded as calls: it may be passed as a value") {
		t.Errorf("a function passed by name, with documentation beside it, lost its warning or counted the prose: %q", first)
	}
	if strings.Contains(callers, "README.md") {
		t.Errorf("a README is named as a place the function is passed:\n%s", callers)
	}
}

// importListAndPrototype are hono's mergePath (three import-list lines) and
// jq's jv_parser_free (the extra occurrence is its prototype in a header).
func TestAnImportListAndAPrototypeAreNotFunctionsPassedAsValues(t *testing.T) {
	fs := serverFor(t, func(b *indexBuilder) {
		b.fileText("src/utils/url.ts", "typescript", "export const mergePath = (a: string, b: string) => a + b\n", fn("mergePath", 1, 1))
		b.fileText("src/client.ts", "typescript", strings.Join([]string{
			"import {", "  buildSearchParams,", "  mergePath,", "} from './utils'", "export const f = () => mergePath('a', 'b')", ""}, "\n"),
			fn("f", 5, 5))
		b.call(symKey("src/client.ts", "f", 5), symKey("src/utils/url.ts", "mergePath", 1),
			"mergePath", "", "src/client.ts", 5, model.ConfidenceHigh)
		b.fileText("src/hono.ts", "typescript", "import { getPath, mergePath } from './utils/url'\n")
		b.fileText("src/lib.rs", "rust", "use crate::a::{\n    mergePath,\n};\n")
		b.wellBound("typescript", ".ts")

		b.fileText("src/jv_parse.c", "c", "#include \"jv.h\"\nvoid jv_parser_free(struct jv_parser* p) {\n  free(p);\n}\nvoid f(void) {\n  jv_parser_free(0);\n}\n",
			fn("jv_parser_free", 2, 4), fn("f", 5, 7))
		b.fileText("src/jv.h", "c", "jv jv_parse(const char*);\nvoid jv_parser_free(jv_parser*);\n")
		b.call(symKey("src/jv_parse.c", "f", 5), symKey("src/jv_parse.c", "jv_parser_free", 2),
			"jv_parser_free", "", "src/jv_parse.c", 6, model.ConfidenceHigh)
		b.wellBound("c", ".c")
	})
	for _, name := range []string{"mergePath", "jv_parser_free"} {
		first := firstLine(mustAnswer(t, fs, "callers", map[string]any{"name": name}))
		if strings.Contains(first, "passed as a value") || strings.HasPrefix(first, leadIncomplete) {
			t.Errorf("%s: an import list or a prototype was counted as a use: %q", name, first)
		}
	}
}
