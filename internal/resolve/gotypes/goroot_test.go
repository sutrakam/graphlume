package gotypes

import (
	"go/build"
	"path/filepath"
	"testing"
)

// TestGorootFollowsTheToolchainTheRepositoryNeeds: a `go` on PATH that is
// older than the repository's go line is what the go command would replace
// with a downloaded toolchain, so the toolchain in the module cache is the
// standard library to read; one that is new enough is used as it is.
func TestGorootFollowsTheToolchainTheRepositoryNeeds(t *testing.T) {
	old := t.TempDir()
	writeTree(t, old, map[string]string{
		"VERSION":          "go1.20.1\ntime 2023-02-01T00:00:00Z\n",
		"src/fmt/print.go": "package fmt\n",
	})
	cache := t.TempDir()
	tcName := "golang.org/toolchain@v0.0.1-go1.99.0." + build.Default.GOOS + "-" + build.Default.GOARCH
	writeTree(t, cache, map[string]string{
		tcName + "/VERSION":          "go1.99.0\n",
		tcName + "/src/fmt/print.go": "package fmt\n",
	})
	env := func(k string) string {
		if k == "GOROOT" {
			return old
		}
		return ""
	}

	if got := findGoroot(env, cache, "1.10"); got != old {
		t.Errorf("an installation that is new enough must be used as is: got %q", got)
	}
	want := filepath.Join(cache, filepath.FromSlash(tcName))
	if got := findGoroot(env, cache, "1.50"); got != want {
		t.Errorf("a go line newer than the installation must use the cached toolchain: got %q want %q", got, want)
	}
	if got := findGoroot(env, "", "1.50"); got != old {
		t.Errorf("with no toolchain cached the installation is still used (what it lacks is stubbed): got %q", got)
	}
	if got := findGoroot(func(string) string { return "" }, cache, "1.50"); got != "" {
		t.Errorf("a cached toolchain alone is not a GOROOT that was found: got %q", got)
	}
}
