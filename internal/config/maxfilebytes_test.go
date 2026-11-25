package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoCanSetTheSizeLimitWithinBounds: index.max_file_bytes is the one
// resource setting a repository may make, and only inside a range. Parsing
// costs memory in proportion to file size, so an unbounded value would let a
// cloned repository decide how much memory `graphlume index` uses.
func TestRepoCanSetTheSizeLimitWithinBounds(t *testing.T) {
	load := func(t *testing.T, yaml string) (Config, error) {
		t.Helper()
		t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())
		repo := t.TempDir()
		if yaml != "" {
			writeFile(t, filepath.Join(repo, RepoConfigName), yaml)
		}
		return Load(repo)
	}

	t.Run("unset uses the default", func(t *testing.T) {
		cfg, err := load(t, "")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxFileBytes != DefaultMaxFileBytes {
			t.Errorf("MaxFileBytes = %d, want the %d default", cfg.MaxFileBytes, DefaultMaxFileBytes)
		}
	})

	t.Run("a value in range is taken", func(t *testing.T) {
		for _, n := range []int64{MinMaxFileBytes, 262144, 4 << 20, HardMaxFileBytes} {
			cfg, err := load(t, fmt.Sprintf("index:\n  max_file_bytes: %d\n", n))
			if err != nil {
				t.Fatalf("max_file_bytes %d: %v", n, err)
			}
			if cfg.MaxFileBytes != n {
				t.Errorf("MaxFileBytes = %d, want %d", cfg.MaxFileBytes, n)
			}
		}
	})

	t.Run("a value out of range is an error, not a silent default", func(t *testing.T) {
		for _, n := range []int64{-1, 10, MinMaxFileBytes - 1, HardMaxFileBytes + 1, 1 << 40} {
			_, err := load(t, fmt.Sprintf("index:\n  max_file_bytes: %d\n", n))
			if err == nil {
				t.Errorf("max_file_bytes %d was accepted", n)
				continue
			}
			if !strings.Contains(err.Error(), "index.max_file_bytes") {
				t.Errorf("max_file_bytes %d: error %q does not name the setting", n, err)
			}
		}
	})

	t.Run("the setting does not disturb its neighbours", func(t *testing.T) {
		cfg, err := load(t, "index:\n  max_file_bytes: 1048576\n  exclude:\n    - \"gen/**\"\n")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxFileBytes != 1<<20 {
			t.Errorf("MaxFileBytes = %d, want 1048576", cfg.MaxFileBytes)
		}
		found := false
		for _, e := range cfg.Exclude {
			if e == "gen/**" {
				found = true
			}
		}
		if !found || len(cfg.Exclude) <= 1 {
			t.Errorf("exclude = %v, want the defaults plus gen/**", cfg.Exclude)
		}
	})
}
