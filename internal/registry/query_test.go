package registry

import "testing"

// A row names its source with the owner the index holds for it. The stored
// qualified name is the path, a colon and the name as the language qualifies
// it; the path is stripped as a prefix because a C++ name has colons too.
func TestQualifiedNameComesFromTheStoredQualifiedName(t *testing.T) {
	for _, tc := range []struct{ name, fqn, path, want string }{
		{"AddEdge", "internal/registry/writer.go:Writer.AddEdge", "internal/registry/writer.go", "Writer.AddEdge"},
		{"TestEdgeValidate", "pkg/model/model_test.go:TestEdgeValidate", "pkg/model/model_test.go", "TestEdgeValidate"},
		{"draw", "src/shape.cc:geo::Shape::draw", "src/shape.cc", "geo::Shape::draw"},
		{"f", "", "a.go", "f"},
		{"f", "elsewhere.go:T.f", "a.go", "f"},
		{"f", "a.go:", "a.go", "f"},
	} {
		if got := qualifiedName(tc.name, tc.fqn, tc.path); got != tc.want {
			t.Errorf("qualifiedName(%q, %q, %q) = %q, want %q", tc.name, tc.fqn, tc.path, got, tc.want)
		}
	}
}

// Within keeps a directory's sites with a trailing slash, one file and its
// directory without one, and never a sibling that only shares the prefix:
// "src" must not match "srcgen/". LIKE wildcards in the path are literal.
func TestWithinMatchesADirectoryOrAFileAndNothingBeside(t *testing.T) {
	for _, tc := range []struct {
		within  string
		wantSQL bool
		args    []any
	}{
		{"", false, nil},
		{"  ", false, nil},
		{"src/", true, []any{"src/%"}},
		{"./src/", true, []any{"src/%"}},
		{"src", true, []any{"src", "src/%"}},
		{"src/a_b.ts", true, []any{"src/a_b.ts", `src/a\_b.ts/%`}},
		{"100%/", true, []any{`100\%/%`}},
	} {
		sql, args := withinSQL(tc.within)
		if (sql != "") != tc.wantSQL {
			t.Errorf("withinSQL(%q) sql = %q", tc.within, sql)
			continue
		}
		if len(args) != len(tc.args) {
			t.Errorf("withinSQL(%q) args = %v, want %v", tc.within, args, tc.args)
			continue
		}
		for i := range args {
			if args[i] != tc.args[i] {
				t.Errorf("withinSQL(%q) args = %v, want %v", tc.within, args, tc.args)
			}
		}
	}
}
