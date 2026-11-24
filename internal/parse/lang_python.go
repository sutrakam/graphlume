package parse

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Python `@overload`.
//
//	@overload
//	def convert_type(ty: None) -> str: ...
//	@overload
//	def convert_type(ty: int) -> int: ...
//	def convert_type(ty): ...
//
// is one function with three signatures declared above its implementation. Each
// stub was a definition of its own, so a function with three stubs was four
// definitions of one name and every lookup of it was ambiguous (click's
// convert_type). A stub is folded into the definition that follows it, see
// registerStubFold: the symbol starts at the first stub and ends at the
// implementation, and has no single parameter count.
//
// A stub is a function decorated `@overload`, `@typing.overload` or through any
// other module name (`@t.overload`). Any other decorator leaves the function a
// definition of its own, so a property getter and its `@x.setter` stay two.
func init() {
	registerStubFold("python", pythonOverloadStub)
}

func pythonOverloadStub(name *ts.Node, lang *ts.Language, src []byte) bool {
	fn := name.Parent()
	if fn == nil || fn.Type(lang) != "function_definition" {
		return false
	}
	deco := fn.Parent()
	if deco == nil || deco.Type(lang) != "decorated_definition" {
		return false
	}
	for i := 0; i < deco.ChildCount(); i++ {
		c := deco.Child(i)
		if c == nil || c.Type(lang) != "decorator" {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Text(src)), "@"))
		if text == "overload" || strings.HasSuffix(text, ".overload") {
			return true
		}
	}
	return false
}
