package infer

import "testing"

// The core-library names of PHP and Kotlin: a library call on a receiver of
// unknown type must not be bound to the one repository method of the name.
func TestCoreMembersOfPHPAndKotlin(t *testing.T) {
	for _, tc := range []struct {
		lang, name string
		want       bool
	}{
		{"php", "getBody", true},       // PSR-7
		{"php", "getHeaderLine", true}, // PSR-7
		{"php", "fail", true},          // PHPUnit
		{"php", "with", true},          // PHPUnit mock builder: $mock->expects(...)->method(...)->with(...)
		{"php", "expects", true},
		{"php", "assertSame", true}, // PHPUnit, by prefix
		{"php", "assertStringContainsString", true},
		{"php", "expectException", true}, // PHPUnit, by prefix
		{"php", "markTestSkipped", true},
		{"php", "assertion", false}, // a prefix needs a capital after it
		{"php", "expected", false},
		{"php", "getLastRequest", false}, // an application's own name
		{"php", "save", false},
		{"kotlin", "toLong", true},
		{"kotlin", "lowercase", true},
		{"kotlin", "let", true},
		{"kotlin", "toString", true}, // the JVM's, kept
		{"kotlin", "use", false},     // okio declares its own, and its calls to it are right
		{"kotlin", "reposition", false},
		{"kotlin", "utf8", false}, // okio's own
		{"java", "toLong", false}, // Java has longValue; the Kotlin list is Kotlin's
		{"scala", "assertSame", false},
		{"python", "execute", true},
		{"python", "json", true},
		{"python", "commit", true},
		{"python", "scalars", true},
		{"python", "custom_business_logic", false},
	} {
		spec := LanguageSpec{Name: tc.lang}
		if got := spec.isCoreMember(tc.name); got != tc.want {
			t.Errorf("%s: isCoreMember(%q) = %v, want %v", tc.lang, tc.name, got, tc.want)
		}
	}
}
