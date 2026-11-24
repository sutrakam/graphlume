package shapes

// Shape has two implementers, one through a value and one through a pointer.
type Shape interface {
	Area() float64
	Name() string
}

// Circle implements Shape by value.
type Circle struct{ R float64 }

func (c Circle) Area() float64 { return 3 * c.R * c.R }
func (c Circle) Name() string  { return "circle" }

// Square implements Shape through its pointer only.
type Square struct{ S float64 }

func (s *Square) Area() float64 { return s.S * s.S }
func (s *Square) Name() string  { return "square" }

// Base and Wrapper: Hello is promoted through embedding.
type Base struct{}

func (Base) Hello() string { return "hello" }

type Wrapper struct{ Base }

// Describe calls on an interface value, and uses a method value.
func Describe(s Shape) string {
	f := s.Name
	return f() + describeArea(s)
}

func describeArea(s Shape) string {
	if s.Area() > 1 {
		return "big"
	}
	return "small"
}
