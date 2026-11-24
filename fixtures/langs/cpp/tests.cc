#include <string>

TEST(Suite, Name) {
  EXPECT_EQ(1, 1);
}

TEST_F(Fixture, Other) { run(); }

TEST_P(Param, Case) {}

TYPED_TEST(Typed, Works) {}

BENCHMARK_F(BM_Fixture, Bench)(benchmark::State& state) {
  for (auto _ : state) {}
}

BENCHMARK(BM_free);

REGISTER_THING(widget) {
  setup();
}

MY_MACRO(a, b) {}

int real(int x) { return x; }

struct Widget {
  Widget() {}
  Widget(int a, int b) {}
};

class Holder {
 public:
  API_EXPORT() {}
  Holder() {}
};

void use(int n) {
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
  Foo g(bar);
  Foo h();
  Foo i;
  Foo j = Foo(1);
  Foo k, m(2);
  const Foo cf(5);
  auto q(3);
  if (Foo cond(7); cond.ok()) {}
}
