package gotypes

import "testing"

func TestVersionOrder(t *testing.T) {
	for _, c := range []struct {
		lo, hi string
	}{
		{"v0.72.1", "v0.72.2-0.20261027144952-e257672f3db6"},
		{"v0.72.2-0.20261027144952-e257672f3db6", "v0.72.2"},
		{"v1.9.0", "v1.10.0"},
		{"v1.0.0-alpha", "v1.0.0-alpha.1"},
		{"v1.0.0-alpha.2", "v1.0.0-alpha.10"},
		{"v1.0.0-1", "v1.0.0-alpha"},
		{"v0.0.0-20200101000000-aaaaaaaaaaaa", "v0.0.0-20210101000000-aaaaaaaaaaaa"},
	} {
		if compareVersions(c.lo, c.hi) >= 0 || compareVersions(c.hi, c.lo) <= 0 {
			t.Errorf("%s must order below %s", c.lo, c.hi)
		}
	}
	if compareVersions("v1.2.3", "v1.2.3+incompatible") != 0 {
		t.Errorf("build metadata does not order")
	}
}

// TestAWorkspaceBuildsAtTheHighestRequiredVersion: in a go.work the program is
// built at the highest version any member requires, so a member that requires
// less still sees the newer API.
func TestAWorkspaceBuildsAtTheHighestRequiredVersion(t *testing.T) {
	cache := t.TempDir()
	writeTree(t, cache, map[string]string{
		"example.org/dep@v1.0.0/dep.go": "package dep\n\nfunc Old() {}\n",
		"example.org/dep@v1.2.0/dep.go": "package dep\n\nfunc Old() {}\n\nfunc New() {}\n",
	})
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.work":  "go 1.22\n\nuse (\n\t./a\n\t./b\n)\n",
		"a/go.mod": "module example.com/a\n\ngo 1.22\n\nrequire example.org/dep v1.0.0\n",
		"a/a.go":   "package a\n\nimport \"example.org/dep\"\n\nfunc Run() { dep.New() }\n",
		"b/go.mod": "module example.com/b\n\ngo 1.22\n\nrequire example.org/dep v1.2.0\n",
		"b/b.go":   "package b\n\nimport \"example.org/dep\"\n\nfunc Run() { dep.Old() }\n",
	})
	res := checkTree(t, root, map[string]string{"GOMODCACHE": cache})
	if res.Stats.MissingCount != 0 || res.Stats.WithErrors != 0 || res.Stats.Complete != 2 {
		t.Fatalf("the workspace was not built at v1.2.0: %+v errors %v", res.Stats, res.Stats.ErrorSamples)
	}
}
