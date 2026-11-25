package taint

import "example.org/does/not/exist/unknown"

// Deeper has Run one level further down than the unknown embedded type.
type Deeper struct{}

func (Deeper) Run() string { return "deeper" }

type Mid struct{ Deeper }

// S embeds a type from a package that cannot be found. unknown.Base may well
// have a Run at depth one, which would shadow Mid.Deeper.Run at depth two.
type S struct {
	unknown.Base
	Mid
}

func Use(s S) string {
	return s.Run() // must not become an exact edge to Deeper.Run
}

// Plain has a direct method and an embedded unknown: a direct method always
// wins, so this one is safe to prove.
type Plain struct{ unknown.Base }

func (Plain) Go() string { return "go" }

func UsePlain(p Plain) string { return p.Go() }
