package registry

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// ---------------------------------------------------------------- subtype declarations

func TestSubtypeLinesPerLanguage(t *testing.T) {
	type tc struct {
		name  string
		lang  string
		lines []string // the last line holds the mention
		want  declKind
	}
	for _, c := range []tc{
		// ---- TypeScript / JavaScript
		{"ts class extends", "typescript", []string{"export class Foo extends Preset {"}, declNamed},
		{"ts class implements", "typescript", []string{"class Foo extends Base implements A, Preset {"}, declNamed},
		{"ts interface extends", "typescript", []string{"interface Foo<T> extends Preset<T> {"}, declNamed},
		{"ts generic constraint", "tsx", []string{"export const f = <TPreset extends Preset>(p: TPreset) => p"}, declNone},
		{"ts class generic constraint", "typescript", []string{"class Foo<T extends Preset> extends Base {"}, declNone},
		{"ts type argument of another supertype", "typescript", []string{"class Foo extends Base<Preset> {"}, declNone},
		{"ts conditional type", "typescript", []string{"type X<T> = T extends Preset ? 1 : 2"}, declNone},
		{"ts parameter", "typescript", []string{"class Foo { constructor(p: Preset) {} }"}, declNone},
		{"ts own declaration", "typescript", []string{"export interface Preset {"}, declNone},
		{"js anonymous class expression", "javascript", []string{"  return class extends Preset {"}, declAnonymous},
		{"ts multi-line extends", "typescript", []string{"export class Foo", "  extends Preset"}, declNamed},
		{"ts multi-line implements list", "typescript", []string{"export class Foo", "  implements Other,", "    Preset {"}, declNamed},
		// ---- Java
		{"java extends", "java", []string{"public final class Wild extends TypeName {"}, declNamed},
		{"java implements", "java", []string{"class A implements B, TypeName {"}, declNamed},
		{"java wildcard bound", "java", []string{"  void f(List<? extends TypeName> xs) {"}, declNone},
		{"java type parameter bound", "java", []string{"  class Box<T extends TypeName> {"}, declNone},
		{"java generic supertype argument", "java", []string{"class A extends Base<TypeName> {"}, declNone},
		{"java permits", "java", []string{"sealed class A permits TypeName {"}, declNone},
		{"java method with extends in params", "java", []string{"  static <T extends TypeName> T f() {"}, declNone},
		{"java anonymous", "java", []string{"    return new TypeName() {"}, declAnonymous},
		{"java anonymous generic", "java", []string{"    return new JsonAdapter<Node>(a, b) {"}, declAnonymous},
		{"java record", "java", []string{"record R(int x) implements TypeName {"}, declNamed},
		{"java record component", "java", []string{"record R(TypeName x) implements Other {"}, declNone},
		// ---- Kotlin
		{"kotlin supertype", "kotlin", []string{"class A : JsonAdapter<T>() {"}, declNamed},
		{"kotlin interface list", "kotlin", []string{"class A(val x: Int) : Base(), JsonAdapter<T> {"}, declNamed},
		{"kotlin primary constructor parameter", "kotlin", []string{"class A(val x: JsonAdapter<T>) : Base() {"}, declNone},
		{"kotlin type parameter bound", "kotlin", []string{"class A<T : JsonAdapter<T>> : Base() {"}, declNone},
		{"kotlin anonymous object", "kotlin", []string{"  return object : JsonAdapter<Boolean?>() {"}, declAnonymous},
		{"kotlin where clause", "kotlin", []string{"class A<T> : Base() where T : JsonAdapter<T> {"}, declNone},
		// ---- Scala
		{"scala case class with", "scala", []string{"case class A(x: Int) extends Base with Checker {"}, declNamed},
		{"scala with", "scala", []string{"class A extends Base with Checker {"}, declNamed},
		{"scala trait", "scala", []string{"trait A extends Checker"}, declNamed},
		{"scala type bound", "scala", []string{"class A[T <: Checker] extends Base {"}, declNone},
		// ---- C#
		{"c# interface list", "c_sharp", []string{"internal sealed class Sink : Base, ILogEventSink"}, declNamed},
		{"c# primary constructor parameter", "c_sharp", []string{"class Delegating(ILogEventSink sink) : IListener"}, declNone},
		{"c# where constraint", "c_sharp", []string{"class Box<T> : Base where T : ILogEventSink"}, declNone},
		{"c# generic constraint on a method", "c_sharp", []string{"  void M<T>() where T : ILogEventSink"}, declNone},
		{"c# multi-line base list", "c_sharp", []string{"class Sink", "    : ILogEventSink"}, declNamed},
		{"c# record", "c_sharp", []string{"record R(int X) : ILogEventSink;"}, declNamed},
		// ---- C / C++
		{"c++ public base", "cpp", []string{"class Console : public BenchmarkReporter {"}, declNamed},
		{"c++ second base", "cpp", []string{"struct A : Base, protected BenchmarkReporter {"}, declNamed},
		{"c++ forward declaration", "cpp", []string{"class BenchmarkReporter;"}, declNone},
		{"c++ friend declaration", "cpp", []string{"  friend class BenchmarkReporter;"}, declNone},
		{"c++ template parameter", "cpp", []string{"template <class BenchmarkReporter>"}, declNone},
		{"c++ template argument of a base", "cpp", []string{"class A : public Base<BenchmarkReporter> {"}, declNone},
		{"c++ own declaration", "cpp", []string{"class BenchmarkReporter {"}, declNone},
		{"c++ constructor initializer", "cpp", []string{"  A() : BenchmarkReporter() {}"}, declNone},
		{"c++ template class", "cpp", []string{"template <class T> class A : public BenchmarkReporter {"}, declNamed},
		{"objective-c interface", "objc", []string{"@interface Foo : Reporter"}, declNamed},
		// ---- Rust
		{"rust impl for", "rust", []string{"impl Matcher for Glob {"}, declNamed},
		{"rust impl generic for", "rust", []string{"impl<T: Clone> Matcher for Wrapper<T> {"}, declNamed},
		{"rust path", "rust", []string{"impl crate::patterns::Matcher for Glob {"}, declNamed},
		{"rust inherent impl", "rust", []string{"impl Matcher {"}, declNone},
		{"rust bound", "rust", []string{"impl<T: Matcher> Display for Wrapper<T> {"}, declNone},
		{"rust where bound", "rust", []string{"impl<T> Display for Wrapper<T> where T: Matcher {"}, declNone},
		{"rust supertrait", "rust", []string{"pub trait Sub: Clone + Matcher {"}, declNamed},
		{"rust trait generic bound", "rust", []string{"pub trait Sub<T: Matcher> {"}, declNone},
		{"rust fn bound", "rust", []string{"fn run<T: Matcher>(m: T) {"}, declNone},
		// ---- Python
		{"python base", "python", []string{"class Choice(ParamType):"}, declNamed},
		{"python subscripted base", "python", []string{"class Choice(ParamType[str]):"}, declNamed},
		{"python dotted base", "python", []string{"class Choice(click.ParamType):"}, declNamed},
		{"python second base", "python", []string{"class Choice(Base, ParamType, metaclass=Meta):"}, declNamed},
		{"python metaclass keyword", "python", []string{"class Choice(Base, metaclass=ParamType):"}, declNone},
		{"python multi-line base list", "python", []string{"class Choice(", "    Base,", "    ParamType,"}, declNamed},
		{"python annotation", "python", []string{"def convert(value) -> ParamType:"}, declNone},
		{"python isinstance", "python", []string{"    if issubclass(guessed, ParamType):"}, declNone},
		{"python a class that closed earlier", "python", []string{"class A(Base):", "    x = ParamType"}, declNone},
		// ---- Ruby
		{"ruby superclass", "ruby", []string{"class Sub < BadRequest"}, declNamed},
		{"ruby scoped superclass", "ruby", []string{"class Sub < Rack::BadRequest"}, declNamed},
		{"ruby include", "ruby", []string{"  include BadRequest"}, declNamed},
		{"ruby mention", "ruby", []string{"  raise BadRequest, 'x'"}, declNone},
		{"ruby own class", "ruby", []string{"class BadRequest < StandardError"}, declNone},
		{"ruby Class.new", "ruby", []string{"  k = Class.new(BadRequest)"}, declAnonymous},
		// ---- PHP
		{"php implements", "php", []string{"final class Stream extends Base implements HandlerInterface"}, declNamed},
		{"php interface extends", "php", []string{"interface Sub extends HandlerInterface"}, declNamed},
		{"php namespaced", "php", []string{"class A extends \\Monolog\\Handler\\HandlerInterface {"}, declNamed},
		{"php parameter", "php", []string{"    public function __construct(HandlerInterface $h)"}, declNone},
		{"php anonymous", "php", []string{"        $h = new class extends HandlerInterface {"}, declAnonymous},
		// ---- Swift
		{"swift conformance", "swift", []string{"final class A: Base, Reporter {"}, declNamed},
		{"swift extension", "swift", []string{"extension A: Reporter {"}, declNamed},
		{"swift where clause", "swift", []string{"extension Array where Element: Reporter {"}, declNone},
		// ---- languages with no pattern
		{"go struct embedding is a reference, not text", "go", []string{"type A struct { Reporter }"}, declNone},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := mentionedType(c.lines[len(c.lines)-1], c.lang)
			if got := classifySubtypeLine(c.lines, len(c.lines)-1, name, c.lang); got != c.want {
				t.Errorf("classify(%q as %s, target %s) = %v, want %v", c.lines, c.lang, name, got, c.want)
			}
		})
	}
}

// mentionedType picks, for the table above, the target name each line is about:
// the name that appears in the line from the set the cases use.
func mentionedType(line, lang string) string {
	for _, n := range []string{"BenchmarkReporter", "ILogEventSink", "HandlerInterface", "BadRequest", "JsonAdapter",
		"ParamType", "TypeName", "Preset", "Matcher", "Checker", "Reporter"} {
		if len(identifierOffsets(line, n)) > 0 {
			return n
		}
	}
	return ""
}

func TestUnrecordedSubtypeDeclarationsAreNamedTypesInSourceFiles(t *testing.T) {
	r, run := seedTextFiles(t, []fileSpec{
		{path: "src/types.py", lang: "python", hash: "e1", content: strings.Join([]string{
			"class ParamType:", "    pass", "", "class Choice(ParamType[str]):", "    pass", "",
			"def convert(value) -> ParamType:", "    return value", ""}, "\n"),
			syms: []symSpec{{name: "ParamType", fqn: "ParamType", kind: model.KindClass, line: 1}}},
		{path: "docs/types.md", lang: "markdown", hash: "e2", content: "```python\nclass Doc(ParamType):\n    pass\n```\n"},
		{path: "docs/types.rst", lang: "rst", hash: "e3", content: "class Doc(ParamType):\n"},
	})
	got, err := r.UnrecordedSubtypeDeclarations(context.Background(), run, "ParamType", []DefSpan{{Path: "src/types.py", From: 1, To: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Known || got.Named != 1 || got.Anonymous != 0 || !reflect.DeepEqual(got.Sample, []Place{{"src/types.py", 4}}) {
		t.Errorf("UnrecordedSubtypeDeclarations = %+v, want the one real class and none of the documentation", got)
	}
}

// TestACallRecordedOnAnAnonymousSubclassLineDoesNotExplainIt: `new Target() {`
// is recorded as an instantiation, which says nothing about a subtype.
func TestACallRecordedOnAnAnonymousSubclassLineDoesNotExplainIt(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	writeContent(t, r, run.ID, []fileSpec{
		{path: "A.java", lang: "java", hash: "f1", content: "class TypeName {}\nclass Use {\n  Object f() { return new TypeName() {\n  }; }\n  class Sub extends TypeName {}\n}\n",
			syms: []symSpec{{name: "TypeName", fqn: "TypeName", kind: model.KindClass, line: 1}}},
	}, nil)
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.UnrecordedSubtypeDeclarations(ctx, run.ID, "TypeName", []DefSpan{{Path: "A.java", From: 1, To: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Named != 1 || got.Anonymous != 1 {
		t.Errorf("want one named and one anonymous subclass, got %+v", got)
	}
}
