package parse

import (
	"strconv"
	"testing"
)

func TestKotlinBacktickedClassQualifiesItsMethods(t *testing.T) {
	res := parse(t, "a.kt", "internal class `-JsonUtf8Reader`(val s: String) : JsonReader() {\n  override fun peek(): Int = 1\n  fun other() {}\n}\n")
	got := map[string]string{}
	for _, s := range res.Symbols {
		got[s.Name] = s.FQN
	}
	for name, want := range map[string]string{
		"peek":  "a.kt:-JsonUtf8Reader.peek",
		"other": "a.kt:-JsonUtf8Reader.other",
	} {
		if got[name] != want {
			t.Errorf("%s has FQN %q, want %q (all: %v)", name, got[name], want, got)
		}
	}
}

func TestKotlinTopLevelPropertiesOwnTheirInitialiserCalls(t *testing.T) {
	src := `val NO_ANNOTATIONS = emptySet<Annotation>()
const val MAX = 10
var counter = start()
val (a, b) = Pair(1, 2)
class C { val member = make() }
`
	res := parse(t, "a.kt", src)
	var got []string
	for _, s := range res.Symbols {
		got = append(got, string(s.Kind)+" "+s.Name+" "+itoaRange(s.Range.Start.Line, s.Range.End.Line))
	}
	wantList(t, "symbols", got, []string{
		"constant NO_ANNOTATIONS 1-1", "constant MAX 2-2", "variable counter 3-3", "class C 5-5",
	})
	// Each initialiser call lies inside the range of a symbol, so it has a caller.
	for _, r := range res.References {
		if r.Name != "emptySet" && r.Name != "start" {
			continue
		}
		inside := false
		for _, s := range res.Symbols {
			if s.Range.Start.Line <= r.Range.Start.Line && r.Range.End.Line <= s.Range.End.Line {
				inside = true
			}
		}
		if !inside {
			t.Errorf("call %s at line %d has no enclosing symbol", r.Name, r.Range.Start.Line)
		}
	}
}

func itoaRange(a, b int) string { return strconv.Itoa(a) + "-" + strconv.Itoa(b) }

func TestKotlinAnonymousObjectMembersAreDropped(t *testing.T) {
	res := parse(t, "a.kt", `class Holder {
  fun make(): Runnable {
    return object : Runnable {
      override fun run() {}
      fun helper() {}
    }
  }
}
fun top() {
  val x = object : Comparable<Int> {
    override fun compareTo(other: Int): Int = 0
    inner class Named { fun kept() {} }
  }
}
`)
	var names []string
	for _, s := range res.Symbols {
		names = append(names, s.Name)
	}
	// A named class declared inside the anonymous object keeps its own members.
	wantList(t, "symbols", names, []string{"Holder", "make", "top", "Named", "kept"})
}

func TestKotlinSecondaryConstructor(t *testing.T) {
	res := parse(t, "person.kt", `class Person {
  constructor(name: String) {}
}
`)
	var got []string
	for _, s := range res.Symbols {
		got = append(got, string(s.Kind)+" "+s.Name)
	}
	wantList(t, "symbols", got, []string{
		"class Person",
		"method constructor",
	})
}
