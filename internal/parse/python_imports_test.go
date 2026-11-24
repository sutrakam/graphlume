package parse

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// TestPythonRelativeImportsAreExtracted covers the gap that made Python's
// import tier unreachable in any file whose imports were relative.
//
// Python is a file-is-a-module language, so the resolver consults imports
// BEFORE proximity. A file that declares no imports has no import tier at all
// and falls through to the same-directory tier that ordering exists to demote —
// which is the exact failure docs/accuracy.md measured at 144 wrong
// high-confidence edges from one name.
//
// The assertion is on the SET of paths, not on a count, because a count passes
// just as happily when the wrong three are captured.
func TestPythonRelativeImportsAreExtracted(t *testing.T) {
	src := []byte(`
import os
import numpy as np
from scrapy.utils.test import get_crawler
from .helpers import Helper
from ..pkg.mod import Thing
from . import sibling
`)
	p := NewRegistry().ForFile("probe.py")
	if p == nil {
		t.Fatal("no parser for python")
	}
	res, err := p.Parse(context.Background(), "probe.py", src)
	if err != nil {
		t.Fatal(err)
	}

	// Member rows — `from .helpers import Helper` also records WHICH name came
	// from helpers, as a second row for the same path — are skipped here: this
	// test is about the set of modules. TestPythonImportedMembersAreRecorded
	// owns the member rows.
	got := make([]string, 0, len(res.Imports))
	aliases := map[string]string{}
	for _, imp := range res.Imports {
		if strings.HasPrefix(imp.Alias, memberAliasPrefix) {
			continue
		}
		got = append(got, imp.Path)
		if imp.Alias != "" {
			aliases[imp.Path] = imp.Alias
		}
	}
	sort.Strings(got)

	// `from . import sibling` has no MODULE row: its module_name is a bare
	// relative_import with no dotted_name, and a path of "." carries no
	// segments for the import tier to match. What it imports is recorded as a
	// member row instead, which this loop skips.
	//
	// The relative paths keep their dots; they are not stripped to the dotted name alone,
	// and the resolver trims them for the unanchored import tier;
	// the rules that know the importing file use them to anchor.
	want := []string{"..pkg.mod", ".helpers", "numpy", "os", "scrapy.utils.test"}
	if len(got) != len(want) {
		t.Fatalf("imports = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("imports = %v, want %v", got, want)
		}
	}
	if aliases["numpy"] != "np" {
		t.Errorf("alias for numpy = %q, want np", aliases["numpy"])
	}
}
