package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestLanguageOverridesKeepFileOrder is the property the whole key rests on:
// the last matching entry wins, so the order written in the file IS the
// meaning of two overlapping globs. Decoding into a Go map would lose it, and
// would lose it differently from run to run.
func TestLanguageOverridesKeepFileOrder(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())

	var doc strings.Builder
	doc.WriteString("index:\n  language_overrides:\n")
	var want []LanguageOverride
	for i := 0; i < 40; i++ {
		// Names chosen so that neither insertion nor sorted order is file
		// order: a map-backed decode cannot pass by accident.
		pattern := fmt.Sprintf("dir%02d/**/*.h", (i*17)%40)
		language := []string{"cpp", "objc", "c", "none"}[i%4]
		fmt.Fprintf(&doc, "    %q: %s\n", pattern, language)
		want = append(want, LanguageOverride{Pattern: pattern, Language: language})
	}
	writeFile(t, filepath.Join(repo, RepoConfigName), doc.String())

	for run := 0; run < 5; run++ {
		cfg, err := Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.LanguageOverrides) != len(want) {
			t.Fatalf("got %d overrides, want %d", len(cfg.LanguageOverrides), len(want))
		}
		for i := range want {
			if cfg.LanguageOverrides[i] != want[i] {
				t.Fatalf("run %d, entry %d = %+v, want %+v: file order was not kept",
					run, i, cfg.LanguageOverrides[i], want[i])
			}
		}
	}
}

func TestLanguageOverridesRejectMalformedInput(t *testing.T) {
	cases := map[string]string{
		"a list instead of a mapping": "index:\n  language_overrides: [\"*.h\", cpp]\n",
		"a scalar":                    "index:\n  language_overrides: cpp\n",
		"a nested value":              "index:\n  language_overrides:\n    \"*.h\": {language: cpp}\n",
		"an empty language":           "index:\n  language_overrides:\n    \"*.h\": \"\"\n",
		"an empty glob":               "index:\n  language_overrides:\n    \"\": cpp\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())
			writeFile(t, filepath.Join(repo, RepoConfigName), doc)
			_, err := Load(repo)
			if err == nil {
				t.Fatal("accepted; a malformed override list must be an error, not a silently empty one")
			}
			if !strings.Contains(err.Error(), "language_overrides") {
				t.Errorf("the error does not name the key: %v", err)
			}
		})
	}
}

// TestLanguageOverridesAreBounded: the file is untrusted and every entry is
// matched against every file in the repository.
func TestLanguageOverridesAreBounded(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())

	var doc strings.Builder
	doc.WriteString("index:\n  language_overrides:\n")
	for i := 0; i <= MaxLanguageOverrides; i++ {
		fmt.Fprintf(&doc, "    \"gen%d/*.h\": cpp\n", i)
	}
	writeFile(t, filepath.Join(repo, RepoConfigName), doc.String())

	if _, err := Load(repo); err == nil {
		t.Fatalf("%d overrides were accepted; the limit is %d", MaxLanguageOverrides+1, MaxLanguageOverrides)
	}
}

func TestNoLanguageOverridesIsTheDefault(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GRAPHLUME_CONFIG_HOME", t.TempDir())
	writeFile(t, filepath.Join(repo, RepoConfigName), "index:\n  exclude: [\"gen/**\"]\n")
	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.LanguageOverrides) != 0 {
		t.Errorf("overrides = %+v, want none", cfg.LanguageOverrides)
	}
}
