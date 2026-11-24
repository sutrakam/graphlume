package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain runs this package's tests with a throwaway home directory.
//
// Commands here write to the user's home: `init` registers the project in
// ~/.graphlume/registry.json, `install` and `clean` edit agent configs, and
// the user config is read from it. Run against the developer's real home,
// the suite left test temp directories in their project registry and read
// their personal configuration. Go's own caches are pinned first, because
// they default to locations under HOME and the type-checking tests need them.
func TestMain(m *testing.M) {
	if out, err := exec.Command("go", "env", "GOPATH", "GOMODCACHE", "GOCACHE").Output(); err == nil {
		vals := strings.Split(strings.TrimSpace(string(out)), "\n")
		for i, k := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
			if i < len(vals) && os.Getenv(k) == "" && vals[i] != "" {
				_ = os.Setenv(k, vals[i])
			}
		}
	}
	home, err := os.MkdirTemp("", "graphlume-cli-home-*")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
		_ = os.Unsetenv(k)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
