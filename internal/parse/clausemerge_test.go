package parse

import (
	"fmt"
	"strings"
	"testing"
)

// symbolSpans lists a file's function symbols as "name@start-end".
func symbolSpans(t *testing.T, file, src string) string {
	t.Helper()
	var out []string
	for _, s := range parse(t, file, src).Symbols {
		if s.Kind == "function" || s.Kind == "method" {
			out = append(out, fmt.Sprintf("%s@%d-%d", s.Name, s.Range.Start.Line, s.Range.End.Line))
		}
	}
	return strings.Join(out, " ")
}

// A function written as several clauses is one symbol from the first clause to
// the last. Different arities are different functions, and a clause that is
// not adjacent to the earlier ones is not merged into them.
func TestClausesOfOneFunctionAreOneSymbol(t *testing.T) {
	cases := []struct {
		name, file, src, want string
	}{
		{
			name: "elixir_consecutive_clauses",
			file: "a.ex",
			src: `defmodule A do
  def f(0), do: 1
  def f(n) when n > 0 do
    n * f(n - 1)
  end
  def g(), do: 2
end`,
			want: "f@2-5 g@6-6",
		},
		{
			// A bodiless head with defaults is f/2 and the clauses after it are
			// f/2; a later f/1 is another function.
			name: "elixir_default_head_and_arity",
			file: "b.ex",
			src: `defmodule B do
  def f(a, b \\ 1)
  def f(a, 0), do: a
  def f(a, b), do: a + b
  def f(a), do: a
end`,
			want: "f@2-4 f@5-5",
		},
		{
			name: "elixir_not_adjacent",
			file: "c.ex",
			src: `defmodule C do
  def f(1), do: 1
  def g(1), do: 1
  def f(2), do: 2
end`,
			want: "f@2-2 g@3-3 f@4-4",
		},
		{
			// The same name in two modules is two functions, whatever the
			// clauses around them.
			name: "elixir_other_owner",
			file: "d.ex",
			src: `defmodule D do
  def f(1), do: 1
end
defmodule E do
  def f(1), do: 1
end`,
			want: "f@2-2 f@5-5",
		},
		{
			name: "elixir_def_and_defp_same_arity",
			file: "e.ex",
			src: `defmodule F do
  def f(1), do: 1
  defp f(2), do: 2
end`,
			want: "f@2-3",
		},
		{
			name: "erlang_clauses_and_arity",
			file: "m.erl",
			src: `-module(m).
f(0) -> 1;
f(N) when N > 0 -> N * f(N - 1).

g() -> ok.

f(A, B) -> A + B.
f(A, B, C) -> A + B + C.
`,
			want: "f@2-3 g@5-5 f@7-7 f@8-8",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := symbolSpans(t, tc.file, tc.src); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// A language that did not register for the merge keeps one symbol per
// definition: Julia's methods of one name are different methods.
func TestClauseMergeIsPerLanguage(t *testing.T) {
	got := symbolSpans(t, "a.jl", "f(x::Int) = 1\nf(x::String) = 2\n")
	if strings.Count(got, "f@") != 2 {
		t.Errorf("julia methods of one name must stay separate, got %q", got)
	}
}
