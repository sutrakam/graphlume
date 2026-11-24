package parse

import (
	"bytes"
	"testing"
)

// TestPHPSupertypeQualifierIsRootedByALeadingBackslash: monolog's
// `extends \DateTimeImmutable` names the built-in class, not the
// Monolog\DateTimeImmutable beside it. The marker is recorded only where the
// file declares a namespace; without one `\X` and `X` are the same name.
func TestPHPSupertypeQualifierIsRootedByALeadingBackslash(t *testing.T) {
	const withNS = "<?php\nnamespace Monolog;\n\nclass A extends "
	const noNS = "<?php\n\nclass A extends "
	for _, tc := range []struct {
		name, src, tail, want string
	}{
		{"global name", withNS, `\DateTimeImmutable {}`, `\`},
		{"rooted path", withNS, `\PHPUnit\Framework\TestCase {}`, `\PHPUnit.Framework`},
		{"relative path", withNS, `Sub\Base {}`, `Sub`},
		{"unqualified", withNS, `Base {}`, ``},
		{"no namespace, rooted", noNS, `\DateTimeImmutable {}`, ``},
		{"no namespace, rooted path", noNS, `\Lib\Base {}`, `Lib`},
		{"global block is no namespace", "<?php\nnamespace {\nclass A extends ", `\Base {}`, ``},
	} {
		src := []byte(tc.src + tc.tail)
		start := uint32(len(tc.src))
		start += uint32(bytes.LastIndexByte([]byte(tc.tail[:bytes.IndexByte([]byte(tc.tail), ' ')]), '\\') + 1)
		if got := supertypeQualifier("php", src, start); got != tc.want {
			t.Errorf("%s: qualifier = %q, want %q", tc.name, got, tc.want)
		}
	}
}
