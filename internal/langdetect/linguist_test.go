package langdetect

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"text/tabwriter"

	"github.com/odvcencio/gotreesitter/grammars"
)

// linguistSamplesEnv names a checkout of GitHub Linguist's samples/ tree, in
// which each top-level folder is the true language of the files inside it.
//
//	git clone --depth 1 --filter=blob:none --sparse https://github.com/github-linguist/linguist.git
//	git -C linguist sparse-checkout set samples
//	GRAPHLUME_LINGUIST_SAMPLES=$PWD/linguist/samples go test -v -run TestLinguistSamples ./internal/langdetect/
//
// The samples are not vendored: they are several thousand files under many
// licences, and the table-driven tests in detect_test.go carry the snippets
// the rules are held to. This run is the accuracy check behind the table in
// docs/user-manual.md, "How a file's language is decided".
const linguistSamplesEnv = "GRAPHLUME_LINGUIST_SAMPLES"

// linguistGrammar maps a Linguist language to the grammar that should read it,
// for the languages whose name the library does not resolve by itself. A
// language absent from both has no grammar, and the right detection is none.
var linguistGrammar = map[string]string{
	"Objective-C++": "objc",
	"SystemVerilog": "verilog",
	"PLSQL":         "sql",
	"PLpgSQL":       "sql",
	"TSQL":          "sql",
	"SQLPL":         "sql",
	"Unity3D Asset": "yaml",
}

func expectedGrammar(linguistName string) string {
	if g, ok := linguistGrammar[linguistName]; ok {
		return g
	}
	if e := grammars.DetectLanguageByName(linguistName); e != nil {
		return e.Name
	}
	return ""
}

type extensionTally struct {
	files, before, after, flagged int
	wrong                         []string
}

// TestLinguistSamples reports, per ambiguous extension, how many files the
// library alone routed to the right grammar and how many this package does.
//
// "Right" is strict: the grammar for the folder's language, or no grammar at
// all when the language has none. Parsing a Coq proof as V counts as wrong
// even though neither yields a symbol, because the first costs seconds and
// labels the file with a language it is not written in.
//
// Neighbours are deliberately not supplied. In this tree a file's directory IS
// its language, so the `.h` neighbour rule would be reading the answer.
func TestLinguistSamples(t *testing.T) {
	root := os.Getenv(linguistSamplesEnv)
	if root == "" {
		t.Skipf("set %s to a checkout of github-linguist/linguist samples/ to run the accuracy check", linguistSamplesEnv)
	}
	det, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}

	tallies := map[string]*extensionTally{}
	tally := func(key string) *extensionTally {
		if tallies[key] == nil {
			tallies[key] = &extensionTally{}
		}
		return tallies[key]
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			// Directories, and the two symbolic links the tree holds: the
			// walker never follows a link, so neither does this.
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		language, _, _ := strings.Cut(rel, "/")
		base := path.Base(rel)
		ext := strings.ToLower(path.Ext(base))

		key := ext
		switch {
		case ext == "":
			key = "(no extension)"
		case extensionRules()[ext] == nil && extraExtensions[ext] == "":
			key = "(other extensions)"
		}

		head, err := readHead(p)
		if err != nil {
			return err
		}
		want := expectedGrammar(language)
		if ext == ".rd" {
			// Linguist files R documentation under R. It is markup, not R
			// source, and the right grammar for it is none.
			want = ""
		}
		before := ""
		if e := grammars.DetectLanguage(base); e != nil {
			before = e.Name
		}
		got := det.Detect(rel, head, nil)

		tl := tally(key)
		tl.files++
		if before == want {
			tl.before++
		}
		if got.Language == want {
			tl.after++
		} else {
			note := got.Language
			if note == "" {
				note = "none"
			}
			if got.Recognised != "" {
				note += " (" + got.Recognised + ")"
			}
			tl.wrong = append(tl.wrong, fmt.Sprintf("%s: got %s by %s, want %q", rel, note, got.Method, want))
		}
		if got.Ambiguous() {
			tl.flagged++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 0, len(tallies))
	for k := range tallies {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	w := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "EXTENSION\tFILES\tRIGHT BEFORE\tRIGHT AFTER\tFLAGGED AMBIGUOUS")
	var files, before, after int
	for _, k := range keys {
		tl := tallies[k]
		_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\n", k, tl.files, tl.before, tl.after, tl.flagged)
		files, before, after = files+tl.files, before+tl.before, after+tl.after
	}
	_, _ = fmt.Fprintf(w, "total\t%d\t%d\t%d\t\n", files, before, after)
	_ = w.Flush()
	t.Logf("\n%s", sb.String())

	for _, k := range keys {
		if k == "(other extensions)" {
			continue
		}
		for _, line := range tallies[k].wrong {
			t.Logf("still wrong  %s", line)
		}
	}

	// The check must be able to fail. Detection that is no better than the
	// extension table on the extensions it has rules for is a regression in
	// the one thing this package is for.
	for _, k := range keys {
		tl := tallies[k]
		if strings.HasPrefix(k, "(") {
			continue
		}
		if tl.after < tl.before {
			t.Errorf("%s: %d of %d routed correctly, fewer than the %d the extension table alone manages",
				k, tl.after, tl.files, tl.before)
		}
	}
}

// readHead returns the first HeadBytes of a file, as the walker supplies them.
func readHead(name string) ([]byte, error) {
	f, err := os.Open(name) //nolint:gosec // test input: a path under the samples directory the caller named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, HeadBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}
