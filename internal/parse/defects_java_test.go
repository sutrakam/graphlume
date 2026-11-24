package parse

import "strings"
import "testing"

// this(...) and super(...) are calls of a constructor. The code is shared with C#
// (delegation.go).
func TestJavaConstructorDelegation(t *testing.T) {
	res := parse(t, "A.java", `class A extends B<String> implements C {
  A() { this(1); }
  A(int x) { super(x, "y"); }
  static class Inner extends p.Q {
    Inner() { super(); }
  }
  static class Plain {
    Plain() { super(); }
  }
}
`)
	var got []string
	for _, r := range refRows(res) {
		if strings.HasPrefix(r, "reference.call ") {
			got = append(got, r)
		}
	}
	// Plain extends nothing: its super() is Object's and names no repository
	// constructor.
	wantList(t, "delegations", got, []string{
		"reference.call A recv=this/self args=1",
		"reference.call B recv=super/super args=2",
		"reference.call Q recv=super/super args=0",
	})
}
