package pipeline_test

// Language detection, end to end: from a file on disk, through the walker's
// content-based detection, to the grammar that parses it and the row the index
// keeps. The unit tests in internal/langdetect hold each rule to a snippet;
// these hold the wiring, which is where the original defect lived. Detection
// was correct in the library's own terms and still every Objective-C file was
// handed to the MATLAB grammar, because the pipeline asked for a parser by
// file name.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/cli"
)

const (
	detObjCImpl = `#import "SBJsonParser.h"

@implementation SBJsonParser

- (id)objectWithData:(NSData *)data {
    return nil;
}

@end
`
	detObjCImplV2 = `#import "SBJsonParser.h"

@implementation SBJsonParser

- (id)objectWithData:(NSData *)data {
    return nil;
}

- (id)objectWithString:(NSString *)repr {
    return nil;
}

@end
`
	detObjCHeader = `#import <Foundation/Foundation.h>

@interface SBJsonParser : NSObject
- (id)objectWithData:(NSData *)data;
@end
`
	detCppHeader = `#ifndef STORAGE_ENV_H_
#define STORAGE_ENV_H_

#include <string>

namespace storage {

class Env {
 public:
  virtual ~Env();
  void Schedule() {}
};

}  // namespace storage

#endif
`
	detCHeader = `#ifndef SDS_H
#define SDS_H

typedef char *sds;

static inline int sdslen(const sds s) { return 0; }

#endif
`
	detMatlab = `function y = make_filter(x)
% MAKE_FILTER builds a filter
y = x;
end
`
	detVerilog = "`timescale 1ns / 1ps\nmodule mux(input a, output b);\n  assign b = a;\nendmodule\n"
	detCoq     = "Require Import Coq.Lists.List.\n\nTheorem t : True.\nProof. trivial. Qed.\n"
	detScript  = "#!/usr/bin/env bash\n\ndeploy() {\n  echo deploying\n}\n\ndeploy\n"
	detUnknown = "nothing in here says what language this is\n"
)

func writeDetectionProject(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, dir, "ios/SBJsonParser.m", detObjCImpl)
	writeFile(t, dir, "ios/SBJsonParser.h", detObjCHeader)
	writeFile(t, dir, "storage/env.h", detCppHeader)
	writeFile(t, dir, "clib/sds.h", detCHeader)
	writeFile(t, dir, "clib/sds.c", "#include \"sds.h\"\n\nint sdsfree(sds s) { return 0; }\n")
	writeFile(t, dir, "matlab/make_filter.m", detMatlab)
	writeFile(t, dir, "hdl/mux.v", detVerilog)
	writeFile(t, dir, "proofs/Imp.v", detCoq)
	writeFile(t, dir, "bin/deploy", detScript)
	writeFile(t, dir, "notes/scratch.m", detUnknown)
}

type detectedFile struct {
	language, detectedBy, status, parseError string
	symbols                                  []string
}

func detectedFiles(t *testing.T, db *sql.DB) map[string]detectedFile {
	t.Helper()
	rows, err := db.Query(`
		SELECT f.path, f.language, f.detected_by, f.parse_status, COALESCE(f.parse_error,''),
		       COALESCE((SELECT group_concat(s.kind || ' ' || s.name, ',')
		                   FROM (SELECT kind, name FROM symbols WHERE file_id = f.id ORDER BY start_line, name) s), '')
		  FROM files f
		 WHERE f.index_run_id = (SELECT id FROM index_runs WHERE committed = 1)`)
	if err != nil {
		t.Fatalf("query files: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]detectedFile{}
	for rows.Next() {
		var path, syms string
		var f detectedFile
		if err := rows.Scan(&path, &f.language, &f.detectedBy, &f.status, &f.parseError, &syms); err != nil {
			t.Fatalf("scan file: %v", err)
		}
		if syms != "" {
			f.symbols = strings.Split(syms, ",")
		}
		out[path] = f
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate files: %v", err)
	}
	return out
}

// runCLI runs one graphlume command against dir and returns what it printed.
// The index must already exist: indexFixture has set GRAPHLUME_CONFIG_HOME.
func runCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	root := cli.NewRoot()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--project", dir}, args...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func hasSymbol(f detectedFile, want string) bool {
	for _, s := range f.symbols {
		if s == want {
			return true
		}
	}
	return false
}

func TestIndexRoutesFilesByContentNotByExtensionAlone(t *testing.T) {
	dir := t.TempDir()
	writeDetectionProject(t, dir)
	files := detectedFiles(t, indexFixture(t, dir))

	for path, want := range map[string]struct{ language, detectedBy string }{
		"ios/SBJsonParser.m":   {"objc", "content"},
		"ios/SBJsonParser.h":   {"objc", "content"},
		"storage/env.h":        {"cpp", "content"},
		"clib/sds.h":           {"c", "extension"},
		"clib/sds.c":           {"c", "extension"},
		"matlab/make_filter.m": {"matlab", "content"},
		"hdl/mux.v":            {"verilog", "content"},
		"proofs/Imp.v":         {"", "content"},
		"bin/deploy":           {"bash", "shebang"},
		"notes/scratch.m":      {"matlab", "extension_ambiguous"},
	} {
		got, ok := files[path]
		if !ok {
			t.Errorf("%s: not in the index", path)
			continue
		}
		if got.language != want.language || got.detectedBy != want.detectedBy {
			t.Errorf("%s: language %q detected by %q, want %q by %q",
				path, got.language, got.detectedBy, want.language, want.detectedBy)
		}
	}

	// The point of routing correctly is what the grammar then finds.
	if f := files["ios/SBJsonParser.m"]; !hasSymbol(f, "method objectWithData") {
		t.Errorf("the Objective-C grammar was reached but found no method: %v", f.symbols)
	}
	if f := files["storage/env.h"]; !hasSymbol(f, "class Env") {
		t.Errorf("a C++ class in a header is still lost: %v", f.symbols)
	}
	if f := files["matlab/make_filter.m"]; !hasSymbol(f, "function make_filter") {
		t.Errorf("a real MATLAB file lost its function: %v", f.symbols)
	}
	if f := files["bin/deploy"]; !hasSymbol(f, "function deploy") {
		t.Errorf("a script with no extension was detected but not parsed: %v", f.symbols)
	}

	// A language with no grammar is skipped under its own name, not parsed as
	// whatever else shares the extension.
	if f := files["proofs/Imp.v"]; f.status != "skipped" || f.parseError != "no grammar for coq" {
		t.Errorf("proofs/Imp.v: status %q, error %q; want skipped, \"no grammar for coq\"", f.status, f.parseError)
	}
}

// TestIncrementalReindexKeepsTheDetectedLanguage: editing an Objective-C file
// under `graphlume watch` re-indexes that one file from bytes already in
// memory. It must come back as Objective-C, with the same provenance, and the
// graph must equal a fresh index of the edited tree.
func TestIncrementalReindexKeepsTheDetectedLanguage(t *testing.T) {
	incDir, freshDir := t.TempDir(), t.TempDir()
	writeDetectionProject(t, incDir)
	writeDetectionProject(t, freshDir)

	incDB := indexFixture(t, incDir)
	writeFile(t, incDir, "ios/SBJsonParser.m", detObjCImplV2)
	writeFile(t, incDir, "storage/env.h", strings.Replace(detCppHeader, "void Schedule() {}", "void Schedule() {}\n  void Cancel() {}", 1))
	incDB = reindexFixture(t, incDir, incDB)

	writeFile(t, freshDir, "ios/SBJsonParser.m", detObjCImplV2)
	writeFile(t, freshDir, "storage/env.h", strings.Replace(detCppHeader, "void Schedule() {}", "void Schedule() {}\n  void Cancel() {}", 1))
	freshDB := indexFixture(t, freshDir)

	inc, fresh := detectedFiles(t, incDB), detectedFiles(t, freshDB)
	for _, path := range []string{"ios/SBJsonParser.m", "storage/env.h"} {
		i, f := inc[path], fresh[path]
		if i.language != f.language || i.detectedBy != f.detectedBy {
			t.Errorf("%s: incremental says %q by %q, a fresh index says %q by %q",
				path, i.language, i.detectedBy, f.language, f.detectedBy)
		}
	}
	if f := inc["ios/SBJsonParser.m"]; f.language != "objc" || !hasSymbol(f, "method objectWithString") {
		t.Errorf("after an edit the file is %q with %v; want objc with the new method", f.language, f.symbols)
	}

	got, gotLines := graphFingerprint(t, incDB)
	want, wantLines := graphFingerprint(t, freshDB)
	if len(wantLines) == 0 {
		t.Fatal("fresh index produced an empty graph: the comparison would be vacuous")
	}
	if got != want {
		t.Errorf("incremental index does not match a fresh index\n incremental:\n  %s\n fresh:\n  %s",
			strings.Join(gotLines, "\n  "), strings.Join(wantLines, "\n  "))
	}
}

func TestRepositoryCanOverrideDetection(t *testing.T) {
	dir := t.TempDir()
	writeDetectionProject(t, dir)
	writeFile(t, dir, ".graphlume.yml", `index:
  language_overrides:
    "notes/*.m": none
    "clib/*.h": cpp
`)
	files := detectedFiles(t, indexFixture(t, dir))

	if f := files["notes/scratch.m"]; f.language != "" || f.detectedBy != "override" || f.status != "skipped" {
		t.Errorf("notes/scratch.m: language %q by %q, status %q; want none by override, skipped",
			f.language, f.detectedBy, f.status)
	}
	if f := files["clib/sds.h"]; f.language != "cpp" || f.detectedBy != "override" {
		t.Errorf("clib/sds.h: language %q by %q, want cpp by override", f.language, f.detectedBy)
	}
	// Everything else is detected as before.
	if f := files["ios/SBJsonParser.m"]; f.language != "objc" || f.detectedBy != "content" {
		t.Errorf("ios/SBJsonParser.m: language %q by %q, want objc by content", f.language, f.detectedBy)
	}
}

// TestStatusCountsLanguagesThatAreGuesses: the index has to be able to say
// which files it was not sure about, or an unparsed file and a misrouted one
// look the same.
func TestStatusCountsLanguagesThatAreGuesses(t *testing.T) {
	dir := t.TempDir()
	writeDetectionProject(t, dir)
	_ = indexFixture(t, dir)

	var st struct {
		Files              int64 `json:"files"`
		AmbiguousLanguages int64 `json:"ambiguous_languages"`
	}
	out := runCLI(t, dir, "--json", "status")
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	if st.AmbiguousLanguages != 1 {
		t.Errorf("ambiguous_languages = %d, want 1 (notes/scratch.m)\n%s", st.AmbiguousLanguages, out)
	}

	text := runCLI(t, dir, "status")
	if !strings.Contains(text, "Ambiguous languages:") || !strings.Contains(text, "language_overrides") {
		t.Errorf("status does not report the ambiguous file or name the remedy:\n%s", text)
	}
}
