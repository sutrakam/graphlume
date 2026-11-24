package infer

import "testing"

// A Rust macro_rules! is a function whose qualified name ends in `!`; a Ruby
// method may end in it too, and is not a macro.
func TestMacroDefinitionIsToldByLanguageAndMarker(t *testing.T) {
	cases := []struct {
		kind, file, qual string
		want             bool
	}{
		{"function", "src/error.rs", "print_error!", true},
		{"function", "src/error.rs", "print_error", false},
		{"function", "src/error.rs", "run.<locals>.check!", true},
		{"function", "app/user.rb", "User.save!", false},
		{"macro", "src/error.rs", "print_error", true},
		{"method", "src/error.rs", "Shape.area!", false},
	}
	for _, c := range cases {
		if got := isMacroDefinition(def(1, c.kind, c.file, c.qual, 1)); got != c.want {
			t.Errorf("%s %s:%s: isMacroDefinition = %v, want %v", c.kind, c.file, c.qual, got, c.want)
		}
	}
}
