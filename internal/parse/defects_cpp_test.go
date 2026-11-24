package parse

import (
	"strings"
	"testing"
)

func TestCppDeclarationConstructs(t *testing.T) {
	res := parse(t, "a.cc", `void use(int n) {
  MutexLock l(mu);
  BenchmarkInstance instance(a, b);
  ValueUnion buff{n};
  ns::Thing t(3);
  Box<int> bx(4);
  std::string s("x");
  std::vector<int> v(3);
  int x(3);
  double d{1.0};
  Foo f(int);
  Foo h();
  Foo i;
  Foo k, m(2);
  const Foo cf(5);
  auto q(3);
  Foo bad(ns::x);
  TCHAR hostname[COUNT] = {0};
  Foo* ptr(make());
  Foo& ref(other);
}

Outer o(global_thing);
struct S { Member m(arg); };
`)
	var got []string
	for _, r := range res.References {
		if r.Kind == "reference.call.new" {
			got = append(got, r.Name+"@"+strings.TrimSpace(string(rune('0'+r.Range.Start.Line/10))+string(rune('0'+r.Range.Start.Line%10))))
		}
	}
	// Foo f(int); Foo h(); std types, built-in types, auto, and a declaration
	// outside a function body that may be a function (Outer o(global_thing);
	// Member m(arg);) are not constructions.
	wantList(t, "constructions", got, []string{
		"MutexLock@02", "BenchmarkInstance@03", "ValueUnion@04", "Thing@05", "Box@06", "Foo@14", "Foo@15",
	})
}

func TestCppTestMacrosAreNamedByTheirArguments(t *testing.T) {
	res := parse(t, "t.cc", `TEST(Suite, Name) { run(); }
TEST_F(ns::Fixture, Other) {}
TYPED_TEST(Typed, Works) {}
BENCHMARK_F(BM_Fixture, Bench)(benchmark::State& state) {}
REGISTER_THING(widget) { setup(); }
MY_MACRO(a, b) {}
int real(int x) { return x; }
class K { public: K() {} API() {} };
`)
	var names []string
	for _, s := range res.Symbols {
		names = append(names, s.Name)
	}
	wantList(t, "symbols", names, []string{
		"Suite.Name", "ns::Fixture.Other", "Typed.Works", "BM_Fixture.Bench", "real", "K", "K", "API",
	})
}

func TestCppDestructorsAndOperators(t *testing.T) {
	res := parse(t, "comp.cc", `class Comparator {
public:
  ~Comparator() {}
  bool operator==(const Comparator& other) const { return true; }
};

Comparator::~Comparator() {}
bool Comparator::operator==(const Comparator& other) const { return true; }
`)
	var got []string
	for _, s := range res.Symbols {
		got = append(got, string(s.Kind)+" "+s.Name)
	}
	wantList(t, "symbols", got, []string{
		"class Comparator",
		"method ~Comparator",
		"method operator==",
		"method ~Comparator",
		"method operator==",
	})
}
