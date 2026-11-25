package parse

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

// The conformance fixtures are the definition of "supported" for a language:
// see fixtures/langs/README.md. This test exists because a language must not be
// counted as supported the moment a query compiles for it: dozens of
// those queries match nothing in real code.

const langFixtureRoot = "../../fixtures/langs"

type expectedDef struct {
	file string
	line int
	kind string
	name string
}

func (d expectedDef) String() string {
	return fmt.Sprintf("%s:%d %s %s", d.file, d.line, d.kind, d.name)
}

func readExpected(t *testing.T, path string) []expectedDef {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("every language fixture needs an expected.tsv: %v", err)
	}
	defer func() { _ = f.Close() }()

	var out []expectedDef
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			t.Fatalf("%s:%d: want 4 tab-separated fields (file, line, kind, name), got %d", path, n, len(parts))
		}
		ln, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			t.Fatalf("%s:%d: line number %q: %v", path, n, parts[1], err)
		}
		out = append(out, expectedDef{file: filepath.ToSlash(strings.TrimSpace(parts[0])), line: ln, kind: strings.TrimSpace(parts[2]), name: parts[3]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// parserForLanguage builds the parser the pipeline would use for a language,
// chosen by name so the fixture does not depend on file-extension detection.
func parserForLanguage(language string) (Parser, error) {
	if isSFC(language) {
		reg, ok := NewRegistry().(*registry)
		if !ok {
			return nil, fmt.Errorf("the registry is not the embedded one")
		}
		return &sfcParser{language: language, registry: reg}, nil
	}
	entry := grammars.DetectLanguageByName(language)
	if entry == nil {
		return nil, fmt.Errorf("no grammar named %q", language)
	}
	p := newGenericParser(*entry, defaultParserConfig)
	if p.initErr != nil {
		return nil, p.initErr
	}
	return p, nil
}

func languageFixtures(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(langFixtureRoot)
	if err != nil {
		t.Fatalf("read %s: %v", langFixtureRoot, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// declaresNothing reports whether a fixture directory records a language that
// was examined and found to have nothing to extract: it holds a KNOWN_GAPS.md
// that says why, and no expected.tsv.
//
// Angular's template grammar is the case this exists for. A template declares
// nothing a developer searches a code index for, and the honest record of that
// is the explanation, not an empty list a later reader would take for neglect.
// A language in that state may not also register a query: that would be a
// claim of support with no fixture behind it.
func declaresNothing(t *testing.T, dir, language string) bool {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "expected.tsv")); err == nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "KNOWN_GAPS.md")); err != nil {
		t.Fatalf("fixture directory %q needs an expected.tsv, or a KNOWN_GAPS.md saying why nothing is extracted", language)
	}
	for _, registered := range HandWrittenLanguages() {
		if registered == language {
			t.Fatalf("%s registers a definition query and has no expected.tsv", language)
		}
	}
	return true
}

func TestLanguageConformance(t *testing.T) {
	for _, language := range languageFixtures(t) {
		t.Run(language, func(t *testing.T) {
			dir := filepath.Join(langFixtureRoot, language)
			if declaresNothing(t, dir, language) {
				return
			}
			p, err := parserForLanguage(language)
			if err != nil {
				t.Fatalf("fixture directory %q: %v", language, err)
			}

			want := map[string]bool{}
			files := map[string]bool{}
			for _, d := range readExpected(t, filepath.Join(dir, "expected.tsv")) {
				if want[d.String()] {
					t.Errorf("expected.tsv lists %s twice", d)
				}
				want[d.String()] = true
				files[d.file] = true
			}

			got := map[string]bool{}
			err = filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				rel, err := filepath.Rel(dir, path)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				if rel == "expected.tsv" || strings.HasSuffix(rel, ".md") {
					return nil
				}
				src, err := os.ReadFile(path) //nolint:gosec // test fixture path
				if err != nil {
					return err
				}
				res, err := p.Parse(context.Background(), path, src)
				if err != nil {
					return fmt.Errorf("%s: %w", rel, err)
				}
				if res.Status == StatusFailed {
					return fmt.Errorf("%s: parse failed: %w", rel, res.Err)
				}
				for _, s := range res.Symbols {
					got[expectedDef{file: rel, line: s.Range.Start.Line, kind: string(s.Kind), name: s.Name}.String()] = true
				}
				delete(files, rel)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for f := range files {
				t.Errorf("expected.tsv names %s, which is not in the fixture", f)
			}

			var missed, extra []string
			for d := range want {
				if !got[d] {
					missed = append(missed, d)
				}
			}
			for d := range got {
				if !want[d] {
					extra = append(extra, d)
				}
			}
			sort.Strings(missed)
			sort.Strings(extra)
			for _, d := range missed {
				t.Errorf("not extracted: %s", d)
			}
			for _, d := range extra {
				t.Errorf("extracted but not a listed definition: %s", d)
			}
			if len(want) == 0 {
				t.Errorf("expected.tsv lists no definition: a fixture that expects nothing proves nothing")
			}
		})
	}
}

// A language whose definition query was written by hand has made a claim; the
// fixture is where the claim is checked.
func TestHandWrittenLanguagesHaveAFixture(t *testing.T) {
	have := map[string]bool{}
	for _, l := range languageFixtures(t) {
		have[l] = true
	}
	for _, l := range HandWrittenLanguages() {
		if !have[l] {
			t.Errorf("%s has a hand-written definition query and no fixture in fixtures/langs/%s", l, l)
		}
	}
}

func TestNoLanguageIsRegisteredTwice(t *testing.T) {
	if len(duplicateRegistrations) > 0 {
		t.Errorf("more than one lang_*.go file registers a query for: %v", duplicateRegistrations)
	}
	for _, l := range HandWrittenLanguages() {
		if grammars.DetectLanguageByName(l) == nil {
			t.Errorf("a definition query is registered for %q, which is not a bundled grammar", l)
		}
	}
}
