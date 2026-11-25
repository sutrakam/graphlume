// Package sample exercises the ways Go declares something.
package sample

import "errors"

// Limit is a package-level constant.
const Limit = 10

var counter int

// ErrEmpty is a sentinel error.
var ErrEmpty = errors.New("empty")

// Shape is an interface.
type Shape interface {
	Area() float64
}

// Rect is a struct.
type Rect struct {
	W, H float64
}

// ID is a defined type; Alias is a true alias.
type ID int

type Alias = Rect

// Area is a method with a value receiver.
func (r Rect) Area() float64 { return r.W * r.H }

// Scale is a method with a pointer receiver.
func (r *Rect) Scale(f float64) {
	r.W *= f
	r.H *= f
}

// Map is a generic function.
func Map[T, U any](in []T, f func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

func unexported(
	a int,
	b int,
) int {
	local := a + b // a local is not a definition
	counter++
	return local + Limit
}

func use() error {
	_ = unexported(1, 2) // a call is not a definition
	_ = Map([]int{1}, func(i int) ID { return ID(i) })
	var scratch int // a var or const declared inside a function is a local
	const ceiling = 10
	var (
		first, second = 1, 2
	)
	_, _, _ = scratch, ceiling, first+second
	return ErrEmpty
}
