package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// ---------------------------------------------------------------- B: a type declared with the target

func hierarchyBase(b *indexBuilder, lang, ext, target string) {
	b.fileText("src/base"+ext, lang, "// "+target+" lives here\n", typ(target, model.KindInterface, 1, 1))
	// One recorded subtype, so the language is known to hold hierarchy: without
	// it the answer is UNKNOWN and says nothing about lines at all.
	b.file("src/seed"+ext, lang, 3, typ("Seed", model.KindClass, 1, 3))
	b.edge(symKey("src/seed"+ext, "Seed", 1), symKey("src/base"+ext, target, 1),
		model.EdgeExtends, model.ConfidenceHigh, "src/seed"+ext, 1)
	b.wellBound(lang, ext)
}

func TestAGenericConstraintAWildcardAndAForwardDeclarationAreNotSubtypes(t *testing.T) {
	cases := []struct {
		name   string
		target string
		build  func(*indexBuilder)
	}{
		{"typescript generic constraint", "Preset", func(b *indexBuilder) {
			hierarchyBase(b, "typescript", ".ts", "Preset")
			b.fileText("src/picker.ts", "typescript", strings.Join([]string{
				"export const select = <TPreset extends Preset>(p: TPreset) => p",
				"export function pick<TPreset extends Preset>(p: TPreset) {", "  return p", "}", ""}, "\n"), fn("select", 1, 1), fn("pick", 2, 4))
			b.edge(symKey("src/picker.ts", "pick", 2), symKey("src/base.ts", "Preset", 1), model.EdgeCalls, model.ConfidenceHigh, "src/picker.ts", 3)
		}},
		{"java wildcard bound", "TypeName", func(b *indexBuilder) {
			hierarchyBase(b, "java", ".java", "TypeName")
			b.fileText("src/MethodSpec.java", "java", "class MethodSpec {\n  void f(List<? extends TypeName> xs) {}\n  <T extends TypeName> T g() { return null; }\n}\n",
				typ("MethodSpec", model.KindClass, 1, 4))
		}},
		{"c++ forward declaration", "BenchmarkReporter", func(b *indexBuilder) {
			hierarchyBase(b, "cpp", ".h", "BenchmarkReporter")
			b.fileText("include/types.h", "cpp", "class BenchmarkReporter;\nstruct Other {\n  friend class BenchmarkReporter;\n};\n", typ("Other", model.KindClass, 2, 4))
		}},
		{"c# primary constructor parameter", "ILogEventSink", func(b *indexBuilder) {
			hierarchyBase(b, "c_sharp", ".cs", "ILogEventSink")
			b.fileText("src/Listener.cs", "c_sharp", "internal class Delegating(ILogEventSink sink) : IListener\n{\n}\nclass Box<T> where T : ILogEventSink\n{\n}\n",
				typ("Delegating", model.KindClass, 1, 3), typ("Box", model.KindClass, 4, 6))
		}},
		{"documentation", "ParamType", func(b *indexBuilder) {
			hierarchyBase(b, "python", ".py", "ParamType")
			b.fileText("docs/parameter-types.md", "markdown", "```python\nclass EnvVarType(ParamType[str]):\n    pass\n```\n")
			b.fileText("docs/index.rst", "rst", "class Doc(ParamType):\n")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := serverFor(t, tc.build)
			text := mustAnswer(t, fs, "implementations", map[string]any{"name": tc.target})
			first := firstLine(text)
			if strings.Contains(first, "read as a type declaration") || strings.Contains(first, "supertype list names it") ||
				strings.Contains(text, "declaration-like lines") {
				t.Errorf("a line that declares no subtype was counted as one: %q", first)
			}
		})
	}
}

// TestASubtypeDeclarationTheExtractorMissedStillFires: for each family of
// language, a class that names the target in its supertype list and left no
// reference behind is counted, in a source file, and a documentation page that
// shows the same line beside it is not.
func TestASubtypeDeclarationTheExtractorMissedStillFires(t *testing.T) {
	for _, tc := range []struct {
		lang, ext, line, target string
	}{
		{"typescript", ".ts", "export class Wide extends Preset {}", "Preset"},
		{"java", ".java", "final class Wide extends TypeName {}", "TypeName"},
		{"cpp", ".h", "class Wide : public BenchmarkReporter {};", "BenchmarkReporter"},
		{"c_sharp", ".cs", "internal sealed class Wide : Base, ILogEventSink { }", "ILogEventSink"},
		{"python", ".py", "class Wide(Base, ParamType):", "ParamType"},
		{"rust", ".rs", "impl Matcher for Wide {}", "Matcher"},
		{"kotlin", ".kt", "class Wide : JsonAdapter<String>() {}", "JsonAdapter"},
		{"ruby", ".rb", "class Wide < BadRequest", "BadRequest"},
		{"php", ".php", "final class Wide extends Base implements HandlerInterface {}", "HandlerInterface"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			fs := serverFor(t, func(b *indexBuilder) {
				b.fileText("src/base"+tc.ext, tc.lang, tc.line+"\n// "+tc.target+"\n", typ(tc.target, model.KindInterface, 2, 2))
				// One recorded subtype, so the language is known to hold hierarchy.
				b.file("src/seed"+tc.ext, tc.lang, 3, typ("Seed", model.KindClass, 1, 3))
				b.edge(symKey("src/seed"+tc.ext, "Seed", 1), symKey("src/base"+tc.ext, tc.target, 2),
					model.EdgeExtends, model.ConfidenceHigh, "src/seed"+tc.ext, 1)
				b.fileText("docs/page.md", "markdown", tc.line+"\n")
				b.wellBound(tc.lang, tc.ext)
			})
			text := mustAnswer(t, fs, "implementations", map[string]any{"name": tc.target})
			first := firstLine(text)
			if !strings.Contains(first, "1 other line(s) declare a named type whose supertype list names it and are not recorded as a subtype") {
				t.Errorf("a %s class missing from the index was not counted: %q", tc.lang, first)
			}
			if strings.Contains(text, "docs/page.md") {
				t.Errorf("the documentation page was counted:\n%s", text)
			}
		})
	}
}
