package util

// Counter shares the method name Name with the shapes but implements nothing.
type Counter struct{ n int }

func (c *Counter) Name() string { return "counter" }

func (c *Counter) Inc() *Counter { c.n++; return c }

// Make returns a Counter so a call can be made on a call result.
func Make() *Counter { return &Counter{} }

// Map is generic.
func Map[T, U any](xs []T, f func(T) U) []U {
	var out []U
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

// Stack is a generic type with a method.
type Stack[T any] struct{ items []T }

func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }

// registry is initialised by a call: a use in a package-level initialiser.
var registry = Make()

var handler = func() string { return registry.Name() }
