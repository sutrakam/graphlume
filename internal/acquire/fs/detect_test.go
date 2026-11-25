package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/langdetect"
)

// The tree every test below walks: one file per defect the verification record
// lists, each with the content that makes its language unmistakable.
func ambiguousTree() map[string]string {
	return map[string]string{
		"ios/AppDelegate.m":      "#import \"AppDelegate.h\"\n\n@implementation AppDelegate\n- (void)run {}\n@end\n",
		"ios/AppDelegate.h":      "#import <UIKit/UIKit.h>\n\n@interface AppDelegate : NSObject\n@end\n",
		"matlab/make_filter.m":   "function y = make_filter(x)\n% MAKE_FILTER builds a filter\ny = x;\nend\n",
		"db/env.h":               "namespace leveldb {\nclass Env {\n public:\n  virtual ~Env();\n};\n}\n",
		"db/env.cc":              "#include \"env.h\"\nnamespace leveldb { Env::~Env() {} }\n",
		"db/port.h":              "#ifndef PORT_H\n#define PORT_H\n#define PORT_LITTLE_ENDIAN 1\n#endif\n",
		"clib/sds.h":             "#ifndef SDS_H\n#define SDS_H\ntypedef char *sds;\n#endif\n",
		"clib/sds.c":             "#include \"sds.h\"\n",
		"fsharp/Sample.fs":       "module Sample\n\nopen System\n\nlet add a b = a + b\n",
		"puppet/init.pp":         "class ntp {\n  package { 'ntp':\n    ensure => installed,\n  }\n}\n",
		"hdl/mux.v":              "`timescale 1ns / 1ps\nmodule mux(input a, output b);\n  assign b = a;\nendmodule\n",
		"proofs/Imp.v":           "Require Import Coq.Lists.List.\n\nTheorem t : True.\nProof. trivial. Qed.\n",
		"vlang/hello.v":          "module main\n\nfn main() {\n\tprintln('hi')\n}\n",
		"bin/deploy":             "#!/usr/bin/env bash\nset -e\necho deploy\n",
		"bin/report":             "#!/usr/bin/env python3\nprint('report')\n",
		"src/main.go":            "package main\n\nfunc main() {}\n",
		"notes/scratch.m":        "nothing here says what this is\n",
		"README":                 "A project.\n",
		"web/app.component.html": "<div *ngIf=\"ready\">{{ title }}</div>\n",
	}
}

func TestWalkDetectsLanguageFromContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, ambiguousTree())
	found := walkTree(t, root, minimalCfg())

	want := map[string]struct {
		language   string
		detectedBy langdetect.Method
		recognised string
	}{
		"ios/AppDelegate.m":      {"objc", langdetect.ByContent, ""},
		"ios/AppDelegate.h":      {"objc", langdetect.ByContent, ""},
		"matlab/make_filter.m":   {"matlab", langdetect.ByContent, ""},
		"db/env.h":               {"cpp", langdetect.ByContent, ""},
		"db/env.cc":              {"cpp", langdetect.ByExtension, ""},
		"db/port.h":              {"cpp", langdetect.ByNeighbours, ""}, // nothing in it; beside env.cc
		"clib/sds.h":             {"c", langdetect.ByExtension, ""},
		"clib/sds.c":             {"c", langdetect.ByExtension, ""},
		"fsharp/Sample.fs":       {"fsharp", langdetect.ByContent, ""},
		"puppet/init.pp":         {"puppet", langdetect.ByContent, ""},
		"hdl/mux.v":              {"verilog", langdetect.ByContent, ""},
		"proofs/Imp.v":           {"", langdetect.ByContent, "coq"},
		"vlang/hello.v":          {"v", langdetect.ByContent, ""},
		"bin/deploy":             {"bash", langdetect.ByShebang, ""},
		"bin/report":             {"python", langdetect.ByShebang, ""},
		"src/main.go":            {"go", langdetect.ByExtension, ""},
		"notes/scratch.m":        {"matlab", langdetect.ByAmbiguousExtension, ""},
		"README":                 {"", "", ""},
		"web/app.component.html": {"html", langdetect.ByExtension, ""},
	}
	if len(found) != len(want) {
		t.Errorf("walk found %d files, want %d", len(found), len(want))
	}
	for path, w := range want {
		d, ok := lookup(found, path)
		if !ok {
			t.Errorf("%s: not discovered", path)
			continue
		}
		if d.Language != w.language || d.DetectedBy != string(w.detectedBy) || d.Recognised != w.recognised {
			t.Errorf("%s: language %q by %q (recognised %q); want %q by %q (recognised %q)",
				path, d.Language, d.DetectedBy, d.Recognised, w.language, w.detectedBy, w.recognised)
		}
		if d.Skipped || d.ContentHash == "" {
			t.Errorf("%s: skipped=%v hash=%q; detection must not cost a file its hash", path, d.Skipped, d.ContentHash)
		}
	}
}

// TestWalkerDetectAgreesWithWalk is the guard for incremental indexing: a file
// re-indexed from bytes already in memory must be given the language the full
// walk gave it, or a `graphlume watch` session would relabel files one save
// at a time.
func TestWalkerDetectAgreesWithWalk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := ambiguousTree()

	// Files around and beyond the head window, with the only marker placed on
	// either side of it. The walk reads in 32 KiB chunks and hands over the
	// first langdetect.HeadBytes; Detect is given the whole file. Both must
	// look at exactly the same bytes.
	prose := "nothing a rule could match on this line\n"
	pad := func(n int) string { return strings.Repeat(prose, n/len(prose)) }
	files["edge/just_inside.m"] = pad(langdetect.HeadBytes-200) + "@implementation Inside\n@end\n"
	files["edge/just_outside.m"] = pad(langdetect.HeadBytes+len(prose)) + "@implementation Outside\n@end\n"
	files["edge/huge.h"] = pad(100<<10) + "class Late {};\n"
	files["edge/exact.m"] = strings.Repeat("x", langdetect.HeadBytes)
	writeTree(t, root, files)

	w, err := NewWalker(root, minimalCfg())
	if err != nil {
		t.Fatal(err)
	}
	found, err := w.Walk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range found {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d.Path)))
		if err != nil {
			t.Fatal(err)
		}
		got := w.Detect(d.Path, src)
		if got.Language != d.Language || string(got.Method) != d.DetectedBy || got.Recognised != d.Recognised {
			t.Errorf("%s: Walk says %q by %q (recognised %q), Detect says %q by %q (recognised %q)",
				d.Path, d.Language, d.DetectedBy, d.Recognised, got.Language, got.Method, got.Recognised)
		}
	}

	inside, _ := lookup(found, "edge/just_inside.m")
	if inside.Language != "objc" {
		t.Errorf("a marker inside the head window was missed: %q by %q", inside.Language, inside.DetectedBy)
	}
	outside, _ := lookup(found, "edge/just_outside.m")
	if outside.DetectedBy != string(langdetect.ByAmbiguousExtension) {
		t.Errorf("a marker beyond the head window decided the language (%q by %q); only the head may",
			outside.Language, outside.DetectedBy)
	}
}

func TestWalkAppliesLanguageOverrides(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, ambiguousTree())

	cfg := minimalCfg()
	cfg.LanguageOverrides = []config.LanguageOverride{
		{Pattern: "*.h", Language: "c"},                    // every header is C ...
		{Pattern: "db/**/*.h", Language: "C++"},            // ... except under db/
		{Pattern: "notes/**", Language: "none"},            // not source at all
		{Pattern: "*.component.html", Language: "angular"}, // a grammar no extension reaches
	}
	found := walkTree(t, root, cfg)

	for path, want := range map[string]string{
		"ios/AppDelegate.h":      "c", // overridden although its content is Objective-C
		"clib/sds.h":             "c",
		"db/env.h":               "cpp",
		"db/port.h":              "cpp",
		"notes/scratch.m":        "",
		"web/app.component.html": "angular",
	} {
		d, _ := lookup(found, path)
		if d.Language != want || d.DetectedBy != string(langdetect.ByOverride) {
			t.Errorf("%s: %q by %q, want %q by override", path, d.Language, d.DetectedBy, want)
		}
	}
	// Files no entry matches are untouched.
	if d, _ := lookup(found, "ios/AppDelegate.m"); d.Language != "objc" || d.DetectedBy != string(langdetect.ByContent) {
		t.Errorf("ios/AppDelegate.m: %q by %q, want objc by content", d.Language, d.DetectedBy)
	}
}

func TestNewWalkerRefusesABadLanguageOverride(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	for name, o := range map[string]config.LanguageOverride{
		"a language no grammar has": {Pattern: "*.m", Language: "objective-sea"},
		"a negated glob":            {Pattern: "!*.m", Language: "objc"},
	} {
		cfg := minimalCfg()
		cfg.LanguageOverrides = []config.LanguageOverride{o}
		_, err := NewWalker(root, cfg)
		if err == nil {
			t.Errorf("%s: accepted; an override that cannot take effect must be refused, not ignored", name)
			continue
		}
		if !strings.Contains(err.Error(), "language_overrides") || !strings.Contains(err.Error(), o.Pattern) {
			t.Errorf("%s: the error does not name the key and the pattern: %v", name, err)
		}
	}
}

// TestSkippedFilesAreIdentifiedByNameOnly: a file the walk does not read is
// not opened for detection either.
func TestSkippedFilesAreIdentifiedByNameOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"big/Huge.m":    "@implementation Huge\n@end\n" + strings.Repeat("// padding\n", 400),
		"big/Huge.h":    "class Huge {};\n" + strings.Repeat("// padding\n", 400),
		"bin/blob.m":    "@implementation Blob\x00\x00\x00",
		"small/Small.m": "@implementation Small\n@end\n",
	})
	found := walkTree(t, root, minimalCfg(), WithMaxFileSize(1024))

	huge, _ := lookup(found, "big/Huge.m")
	if !huge.Skipped || huge.SkipReason != SkipTooLarge {
		t.Fatalf("big/Huge.m: skipped=%v reason=%q, want too large", huge.Skipped, huge.SkipReason)
	}
	if huge.Language != "matlab" || huge.DetectedBy != string(langdetect.ByAmbiguousExtension) {
		t.Errorf("big/Huge.m: %q by %q; an unread .m is the extension's default and is marked as a guess",
			huge.Language, huge.DetectedBy)
	}
	if h, _ := lookup(found, "big/Huge.h"); h.Language != "c" || h.DetectedBy != string(langdetect.ByExtension) {
		t.Errorf("big/Huge.h: %q by %q, want c by extension", h.Language, h.DetectedBy)
	}
	if b, _ := lookup(found, "bin/blob.m"); !b.Skipped || b.SkipReason != SkipBinary ||
		b.DetectedBy != string(langdetect.ByAmbiguousExtension) {
		t.Errorf("bin/blob.m: skipped=%v reason=%q by %q, want a binary skip marked as a guess",
			b.Skipped, b.SkipReason, b.DetectedBy)
	}
	if s, _ := lookup(found, "small/Small.m"); s.Language != "objc" {
		t.Errorf("small/Small.m: %q, want objc", s.Language)
	}
}

// TestHashIsUnchangedByHeadCapture: copying the head out of the hashing loop
// must not disturb the hash, at any size around the chunk and window edges.
func TestHashIsUnchangedByHeadCapture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, size := range []int{0, 1, langdetect.HeadBytes - 1, langdetect.HeadBytes, langdetect.HeadBytes + 1,
		32*1024 - 1, 32 * 1024, 32*1024 + 1, 100_000} {
		content := []byte(strings.Repeat("abcdefghij\n", size/11+1)[:size])
		name := filepath.Join(dir, "f")
		if err := os.WriteFile(name, content, 0o644); err != nil {
			t.Fatal(err)
		}
		plain, _, err := hashFile(name, DefaultMaxFileSize)
		if err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 0, langdetect.HeadBytes)
		withHead, _, err := hashFileHead(name, DefaultMaxFileSize, &head)
		if err != nil {
			t.Fatal(err)
		}
		if plain != withHead || plain != HashBytes(content) {
			t.Errorf("size %d: hash changed when the head was captured", size)
		}
		wantHead := content
		if len(wantHead) > langdetect.HeadBytes {
			wantHead = wantHead[:langdetect.HeadBytes]
		}
		if string(head) != string(wantHead) {
			t.Errorf("size %d: captured %d head bytes, want the first %d of the file", size, len(head), len(wantHead))
		}
	}
}
