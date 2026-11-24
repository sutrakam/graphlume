package langdetect

import (
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
)

func mustDetector(t *testing.T, overrides ...Override) *Detector {
	t.Helper()
	d, err := New(overrides)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// TestAmbiguousExtensionsAreSettledByContent holds every rule to a real
// snippet of each language that shares the extension, the negative cases
// included: a MATLAB file has to stay MATLAB once Objective-C can be found.
//
// Each row would fail against the extension table alone wherever want differs
// from what the library says for the name, which is the defect this package
// exists to fix; TestTheExtensionTableAloneGetsTheseWrong pins that.
func TestAmbiguousExtensionsAreSettledByContent(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		content    string
		want       string // grammar; "" when the language has none
		recognised string // set when want is "" and the content was identified
		method     Method
	}{
		// .m
		{"objc implementation", "App/Foo.m", "#import \"Foo.h\"\n\n@implementation Foo\n- (void)run {}\n@end\n", "objc", "", ByContent},
		{"objc with only C includes", "glfw/cocoa_monitor.m", "#include \"internal.h\"\n#include <stdlib.h>\n\nstatic char* getDisplayName(CGDirectDisplayID displayID)\n{\n}\n", "objc", "", ByContent},
		{"matlab function", "filters/make_filter.m", "function [filtfcn, statefcn] = makeFilter(b, a)\n% FILTFCN = MAKEFILTER(B, A) creates an IIR filtering function\n  v = zeros(size(a));\nend\n", "matlab", "", ByContent},
		{"matlab classdef", "matlab_class.m", "classdef matlab_class\n    properties\n        R;\n    end\nend\n", "matlab", "", ByContent},
		{"matlab script with comments", "example.m", "% plot a sine wave\nx = 0:0.1:10;\ny = sin(x);\nplot(x, y)\n", "matlab", "", ByContent},
		{"matlab script without comments", "run_all.m", "x = linspace(0, 1, 100);\ny = x.^2;\nfigure\nplot(x, y)\n", "matlab", "", ByContent},
		{"mumps routine", "fibonacci.m", "start ; compute the Fibonacci series\n set (a,b)=1\n for i=1:1 do  quit:term>100\n . set term=a+b\n . write !,term\n", "", "mumps", ByContent},
		{"mumps routine named with a percent", "_zewdAPI.m", "%zewdAPI\t; Enterprise Web Developer run-time functions\n ;\n ; Product: Enterprise Web Developer\n", "", "mumps", ByContent},
		{"mumps with no comment", "mileage.m", "compute(miles,gallons)\n quit miles/gallons\n\ncomputepesimist(miles,gallons)\n quit miles/(gallons+1)\n", "", "mumps", ByContent},
		{"mercury module", "hello.m", "% a comment, which MATLAB also starts with a percent\n:- module hello.\n:- interface.\n:- import_module io.\n:- pred main(io::di, io::uo) is det.\n", "", "mercury", ByContent},
		{"wolfram package", "Init.m", "(* Mathematica Init File *)\n\nGet[ \"Foobar`Foobar`\"]\n", "wolfram", "", ByContent},
		{"an .m nobody can place", "notes.m", "just some words\nwith no marker at all\n", "matlab", "", ByAmbiguousExtension},

		// .pro
		{"proguard rules", "app/proguard-rules.pro", "# Add project specific ProGuard rules here.\n-keep class com.example.** { *; }\n-dontwarn okio.**\n", "", "proguard", ByContent},
		{"qmake project", "app.pro", "TEMPLATE = app\nTARGET = demo\nSOURCES += main.cpp\n", "", "qmake", ByContent},
		{"prolog facts and rules", "family.pro", ":- module(family, []).\nparent(a, b).\nancestor(X, Y) :- parent(X, Y).\n", "prolog", "", ByContent},

		// .mm
		{"objective-c++", "EventHandlerMac.mm", "/* Copyright Apple */\n#include \"config.h\"\n#import <Cocoa/Cocoa.h>\n\nnamespace WebCore {\n}\n", "objc", "", ByContent},
		{"freemind map", "ideas.mm", "<map version=\"0.9.0\">\n<node TEXT=\"Some ideas\"/>\n</map>\n", "xml", "", ByContent},

		// .h
		{"c++ header with an export macro", "include/leveldb/env.h", "#ifndef STORAGE_LEVELDB_INCLUDE_ENV_H_\n#define STORAGE_LEVELDB_INCLUDE_ENV_H_\n\n#include <cstdarg>\n\nnamespace leveldb {\n\nclass LEVELDB_EXPORT Env {\n public:\n  Env();\n  virtual ~Env();\n};\n\n}  // namespace leveldb\n#endif\n", "cpp", "", ByContent},
		{"c++ header that is one template", "skiplist.h", "#include <atomic>\n\ntemplate <typename Key, class Comparator>\nclass SkipList {\n private:\n  struct Node;\n};\n", "cpp", "", ByContent},
		{"c++ header known only by std::", "util.h", "#pragma once\n#include \"base.h\"\n\nvoid Append(std::string* dst, int value);\n", "cpp", "", ByContent},
		{"c++ header that only tests the standard", "NoDiscard.h", "#ifndef NODISCARD_H\n#define NODISCARD_H\n#if __cplusplus > 201402l && __has_cpp_attribute(nodiscard)\n#define NODISCARD [[nodiscard]]\n#endif\n#endif\n", "cpp", "", ByContent},
		{"objc header", "SBJsonParser.h", "#import <Foundation/Foundation.h>\n\n@interface SBJsonParser : NSObject\n@property NSUInteger maxDepth;\n- (id)objectWithData:(NSData*)data;\n@end\n", "objc", "", ByContent},
		{"plain c header", "src/sds.h", "#ifndef __SDS_H\n#define __SDS_H\n\n#include <sys/types.h>\n#include <stdarg.h>\n\ntypedef char *sds;\n\nstruct sdshdr8 { uint8_t len; };\n\nsds sdsnewlen(const void *init, size_t initlen);\n#endif\n", "c", "", ByExtension},
		{"c header guarded for c++ callers", "include/leveldb/c.h", "#ifndef C_H\n#define C_H\n\n#ifdef __cplusplus\nextern \"C\" {\n#endif\n\ntypedef struct leveldb_t leveldb_t;\nvoid leveldb_close(leveldb_t* db);\n\n#ifdef __cplusplus\n} /* end extern \"C\" */\n#endif\n#endif\n", "c", "", ByExtension},
		{"c library that wraps itself in a namespace for c++ callers", "roaring/array_util.h", "#ifndef ARRAY_UTIL_H\n#define ARRAY_UTIL_H\n\n#include <stddef.h>\n\n#ifdef __cplusplus\nextern \"C\" {\nnamespace roaring {\nnamespace internal {\n#endif\n\nint32_t binarySearch(const uint16_t *array, int32_t lenarray, uint16_t ikey);\n\n#ifdef __cplusplus\n}\n}\n}\n#endif\n#endif\n", "c", "", ByExtension},
		{"c header that picks its includes by language", "lzfP.h", "#ifdef __cplusplus\n# include <cstring>\nusing namespace std;\n#else\n# include <string.h>\n#endif\n\n#if __cplusplus > 199711L\n# include <cstdint>\n#else\n# include <stdint.h>\n#endif\n\ntypedef unsigned char u8;\n", "c", "", ByExtension},
		{"c++ header with a guard and a class outside it", "api.h", "#ifdef __cplusplus\nextern \"C\" {\n#endif\nvoid api_init(void);\n#ifdef __cplusplus\n}\n#endif\n\n#if !defined(__cplusplus)\n#error C++ only below\n#endif\n\nclass Api {\n public:\n  void Init();\n};\n", "cpp", "", ByContent},
		{"c++ in the c-only branch does not count as hidden", "dual.h", "#ifndef __cplusplus\ntypedef struct dual dual;\n#else\nclass dual;\n#endif\n", "cpp", "", ByContent},
		{"c header whose comment is prose about a class", "device.h", "/*\n   class of the device\n   namespace of the bus\n*/\nint device_class(int id);\n", "c", "", ByExtension},
		{"octave script with # comments", "solve.m", "# if the matrix is singular this fails\n# define the grid first\nA = eye(3);\nx = A \\ [1; 2; 3];\n", "matlab", "", ByContent},
		{"c header whose comment cites file.c::function()", "transport.h", "struct transport_vtable {\n\t/**\n\t * Returns a list of the remote side's refs. See\n\t * transport.c::transport_get_remote_refs().\n\t */\n\tint (*get_refs_list)(void *transport);\n};\n", "c", "", ByExtension},
		{"c++ header known only by an out-of-line definition", "impl.h", "#include \"base.h\"\n\ninline int Counter::next() { return ++value_; }\n", "cpp", "", ByContent},
		{"c header whose comments say virtual and class", "vmem.h", "/* Remove pages in a specific context by physical or virtual address */\n/* the class of device is explicit in the flags */\nint vmem_remove(struct vmem *ctx, unsigned long vaddr);\n", "c", "", ByExtension},

		// .hh
		{"hack", "Assert.hh", "<?hh // strict\n\nfunction assert_int(mixed $x): int {\n  if (is_int($x)) { return $x; }\n}\n", "hack", "", ByContent},
		{"c++ .hh", "bar.hh", "class Bar\n{\n  protected:\n    char *name;\n  public:\n    void hello();\n};\n", "cpp", "", ByContent},

		// .php
		{"hack in a .php file", "funs.php", "<?hh\nfunction f(): void {}\n", "hack", "", ByContent},
		{"php", "index.php", "<?php\nnamespace App;\nfunction f() {}\n", "php", "", ByExtension},

		// .fs
		{"f# namespace", "Combinators.fs", "\xef\xbb\xbfnamespace Nessos.FsPickler.Combinators\n\n    open Nessos.FsPickler\n\n    module Json =\n        let pickle (pickler : Pickler<'T>) (value : 'T) : string = \"\"\n", "fsharp", "", ByContent},
		{"f# module", "sample.fs", "module Sample\n\nopen System\n\ntype Foo = { Bar : string }\n\nlet Sample1(xs : int list) : string = \"\"\n", "fsharp", "", ByContent},
		{"forth", "core.fs", ": immediate   lastxt @ dup c@ negate swap c! ;\n\n: \\   source nip >in ! ; immediate \\ Copyright 2004\n\n: char \\ ( \"word\" -- char )\n    bl-word here 1+ c@ ;\n", "forth", "", ByContent},
		{"glsl fragment shader", "recurse1.fs", "#version 330 core\n\n// cross-unit recursion\n\nvoid main() {}\n\nfloat cbar(int);\n", "glsl", "", ByContent},
		{"glsl fragment shader without a version line", "blur.fs", "precision mediump float;\nuniform sampler2D tex;\nvarying vec2 uv;\nvoid main() { gl_FragColor = texture2D(tex, uv); }\n", "glsl", "", ByContent},

		// .pp
		{"puppet class", "manifests/init.pp", "# Class: apache\n#\nclass apache (\n  $default_mods = true,\n) inherits ::apache::params {\n  package { 'httpd':\n    ensure => installed,\n  }\n}\n", "puppet", "", ByContent},
		{"puppet define", "expiringhost.pp", "define example::expiringhost($ip, $timestamp) {\n  $age = inline_template(\"<%= Time.now %>\")\n}\n", "puppet", "", ByContent},
		{"puppet one-liner", "hiera_include.pp", "hiera_include('classes')\n", "puppet", "", ByContent},
		{"pascal unit", "custforms.pp", "unit custforms;\n\n{$mode objfpc}{$H+}\n\ninterface\n\nuses\n  Classes, SysUtils, Forms;\n", "pascal", "", ByContent},
		{"pascal program", "large.pp", "program large;\n\n  const\n     max = 100000000;\n\n  begin\n  end.\n", "pascal", "", ByContent},
		{"nuget transform of c#", "content/Helper.cs.pp", "// Copyright\nusing System;\nnamespace $rootnamespace$ { class Helper {} }\n", "c_sharp", "", ByExtension},

		// .v
		{"verilog module", "mux.v", "`timescale 1ns / 1ps\n// a mux\nmodule mux(opA, opB, sum, dsp_sel, out);\n  input [3:0] opA, opB;\n  output [3:0] out;\nendmodule\n", "verilog", "", ByContent},
		{"verilog module after a long banner", "sign_extender.v", "////////////////////////////////////////////////////////////////////////////////\n// Original Author: Schuyler Eldridge\n////////////////////////////////////////////////////////////////////////////////\nmodule sign_extender\n  #(\n    parameter\n    INPUT_WIDTH = 8\n    )\n  (\n   input [INPUT_WIDTH-1:0] original\n   );\n  wire [7:0] extended;\nendmodule\n", "verilog", "", ByContent},
		{"v program", "hello.v", "module main\n\nimport os\n\nfn main() {\n\tmut name := os.input('name: ')\n\tprintln('hello ' + name)\n}\n", "v", "", ByContent},
		{"v struct with a field named input", "json.v", "import json\n\nstruct User {\n\tname string\n\tinput string\n}\n\nfn main() {\n\tuser := json.decode(User, '{}') or { return }\n}\n", "v", "", ByContent},
		{"coq proof", "Imp.v", "Require Export SfLib.\n\nModule AExp.\n\nInductive aexp : Type :=\n  | ANum : nat -> aexp.\n\nTheorem optimize_sound: forall a, True.\nProof. intros. trivial. Qed.\n", "", "coq", ByContent},

		// .inc
		{"php include", "file_display.inc", "<?php\n/**\n * Plugins are described by creating a $plugin array.\n */\n$plugin = array();\n", "php", "", ByContent},
		{"bitbake include", "xorg-driver-common.inc", "SUMMARY = \"X driver\"\nHOMEPAGE = \"http://www.x.org\"\nSECTION = \"x11/drivers\"\nLICENSE = \"MIT-X\"\n\ninherit autotools pkgconfig\n", "bitbake", "", ByContent},
		{"sql include", "hostcache_set_state.inc", "# Helper for hostcache_*.test\n\nflush status;\nflush hosts;\nselect @@global.debug;\n", "sql", "", ByContent},
		{"pascal include", "image_url.inc", "  function GetUnixMangaImageURL: Boolean;\n  var\n    i: Integer;\n  begin\n    Result := True;\n  end;\n", "pascal", "", ByContent},
		{"c++ include", "instances.inc", "#include \"QPBO.h\"\n\n#ifdef _MSC_VER\n#pragma warning(disable: 4661)\n#endif\n\ntemplate class QPBO<int>;\n", "cpp", "", ByContent},
		{"c x-macro include", "opcodes.inc", "#ifndef OPCODE\n#define OPCODE(name, value)\n#endif\nOPCODE(NOP, 0)\nOPCODE(HALT, 1)\n", "c", "", ByContent},
		{"an .inc nobody can place", "notes.inc", "some text\nmore text\n", "pascal", "", ByAmbiguousExtension},

		// .txt
		{"vim help file", "doc/plugin.txt", "*plugin.txt*  For Vim version 9.0.  Last change: 2024 Jan 01\n\n\t\t\tMY PLUGIN\n\n1. Intro\t\t\t\t*plugin-intro*\n", "vimdoc", "", ByContent},
		{"vim help file known by a right-aligned tag", "doc/tags.txt", "Introduction                                    *intro*\n\nThis plugin does things.\n", "vimdoc", "", ByContent},
		{"vim help file known by its modeline", "notes.txt", "Some notes\n\n vim:tw=78:ts=8:ft=help:norl:\n", "vimdoc", "", ByContent},
		{"llms.txt is plain text", "llms.txt", "# GraphLume\n\n> A code graph for agents.\n\n- [Guide](docs/guide.md): how to start\n", "", "", ""},
		{"licence text is plain text", "LICENSE.txt", "Permission is hereby granted, free of charge, to any person\n", "", "", ""},
		{"emphasis in prose is not a help tag", "NOTES.txt", "Remember to *always* run the tests.\n*bold* at the start of a line is still prose when it carries words after it\n", "", "", ""},
		{"an empty text file", "empty.txt", "\n", "", "", ""},

		// .bb
		{"babashka script", "validate.bb", "#!/usr/bin/env bb\n\n(def filter-regex \"\\\\.(clj|cljs|cljc)$\")\n\n(defn clojure-source? [path]\n  (re-find #\"x\" path))\n", "clojure", "", ByShebang},
		{"clojure without a shebang", "tasks.bb", "(ns tasks\n  (:require [babashka.fs :as fs]))\n\n(defn build [] (println \"building\"))\n", "clojure", "", ByContent},
		{"bitbake recipe", "qtbase-native.bb", "require qt5-git.inc\nrequire ${PN}.inc\n\ndo_install_append() {\n    install -d ${D}${bindir}\n}\n", "bitbake", "", ByContent},
		{"blitzbasic", "LList.bb", "; Double-linked list container class\n\nType LList\n\tField head_.ListNode\nEnd Type\n\nFunction CreateList.LList()\nEnd Function\n", "", "blitzbasic", ByContent},

		// .gs
		{"glsl geometry shader", "extrude_normals.gs", "#version 330 core\n\nlayout(triangles) in;\nlayout (line_strip, max_vertices = 6) out;\n\nvoid main() { EmitVertex(); }\n", "glsl", "", ByContent},
		{"apps script", "chart_composers.gs", "/*\nLicense\n*/\n\nfunction onOpen() {\n  var ss = SpreadsheetApp.getActiveSpreadsheet();\n}\n", "javascript", "", ByContent},
		{"gosu", "Person.gs", "package example\n\nuses java.util.*\n\nclass Person extends Contact {\n}\n", "", "gosu", ByContent},

		// .tl
		{"teal", "Emitter.tl", "-- emitter.tl\n\nlocal record Emitter\n   on: function(Emitter, string)\nend\n\nreturn Emitter\n", "teal", "", ByContent},
		{"telegram type language", "scheme.tl", "// Core types (no need to gen)\n\nboolFalse#bc799737 = Bool;\nboolTrue#997275b5 = Bool;\n\n---functions---\n\ninvokeAfterMsg#cb9f372d {X:Type} msg_id:long query:!X = X;\n", "", "type-language", ByContent},

		// .es
		{"erlang escript", "single-context.es", "#! /usr/bin/env escript\n% This file is part of Emonk released under the MIT license.\n\nmain([]) ->\n    start(64, 1000);\n", "erlang", "", ByShebang},
		{"erlang escript without a shebang", "run.es", "%% -*- erlang -*-\n-mode(compile).\n\nmain(_) ->\n    io:format(\"hi~n\").\n", "erlang", "", ByContent},
		{"ecmascript", "index.es", "import config from \"../webpack.config\";\nimport webpack from \"webpack\";\n\nexport default function run() {}\n", "javascript", "", ByContent},

		// .pl
		{"perl", "script.pl", "#!/usr/bin/perl\nuse strict;\nuse warnings;\n\nmy $x = 1;\nsub run { print \"hi\\n\"; }\n", "perl", "", ByShebang},
		{"perl without a shebang", "lib.pl", "use strict;\nuse warnings;\n\nsub run { return 1; }\n\n1;\n", "perl", "", ByContent},
		{"prolog", "queens.pl", "% the n-queens problem\n:- use_module(library(clpfd)).\n\nqueens(N, Qs) :-\n    length(Qs, N),\n    safe(Qs).\n\nsafe([]).\n", "prolog", "", ByContent},

		// .ts and .tsx
		{"typescript", "src/app.ts", "import { z } from \"zod\";\n\nexport function parse(input: string): number {\n  return Number(input);\n}\n", "typescript", "", ByExtension},
		{"qt translation file", "i18n/app_de.ts", "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<!DOCTYPE TS>\n<TS version=\"2.1\" language=\"de_DE\">\n<context><name>MainWindow</name></context>\n</TS>\n", "xml", "", ByContent},
		{"tsx", "Button.tsx", "export function Button() {\n  return <button>ok</button>;\n}\n", "tsx", "", ByExtension},
		{"tiled tileset", "tiles.tsx", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<tileset name=\"tiles\" tilewidth=\"16\"/>\n", "xml", "", ByContent},

		// .mat, .rd, .re, .lib
		{"unity material", "GapTile.mat", "%YAML 1.1\n%TAG !u! tag:unity3d.com,2011:\n--- !u!21 &2100000\nMaterial:\n", "yaml", "", ByContent},
		{"r documentation", "man/import.Rd", "% Generated by roxygen2: do not edit by hand\n\\name{import}\n\\alias{import}\n\\title{Import a module}\n", "", "rd", ByContent},
		{"reason", "Layout.re", "open Belt;\n\ntype state = { count: int };\n\nlet make = (~name) => name;\n", "", "reason", ByContent},
		{"re2c scanner", "lexer.re", "#include <stdio.h>\n\nint lex(const char *s) {\n  /*!re2c\n    * { return 0; }\n  */\n}\n", "cpp", "", ByContent},
		{"faust library", "platform.lib", "declare name \"Platform Library\";\n\nimport(\"math.lib\");\n\nSR = min(192000.0, max(1.0, fconstant(int fSamplingFreq, <math.h>)));\n", "faust", "", ByContent},
		{"a .lib that is not faust", "timing.lib", "library (demo) {\n  delay_model : table_lookup;\n}\n", "", "", ""},
	}

	det := mustDetector(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !det.NeedsContent(tc.path) && tc.method != ByExtension {
				t.Fatalf("NeedsContent(%q) = false, so the walker would never hand this file's content to detection", tc.path)
			}
			got := det.Detect(tc.path, []byte(tc.content), nil)
			if got.Language != tc.want || got.Method != tc.method || got.Recognised != tc.recognised {
				t.Errorf("Detect(%q) = language %q by %q (recognised %q); want %q by %q (recognised %q)",
					tc.path, got.Language, got.Method, got.Recognised, tc.want, tc.method, tc.recognised)
			}

			// The same file checked out with Windows line endings is the same
			// language. Every rule that anchors on the end of a line has to
			// see through the carriage return.
			crlf := strings.ReplaceAll(tc.content, "\n", "\r\n")
			if got := det.Detect(tc.path, []byte(crlf), nil); got.Language != tc.want || got.Method != tc.method {
				t.Errorf("with CRLF line endings: language %q by %q; want %q by %q",
					got.Language, got.Method, tc.want, tc.method)
			}
		})
	}
}

// TestTheExtensionTableAloneGetsTheseWrong is the proof that the test above
// can fail: it states what the library says for each name, so a future library
// release that changes its tables shows up here rather than as a silent change
// of which rule is doing the work.
func TestTheExtensionTableAloneGetsTheseWrong(t *testing.T) {
	for name, was := range map[string]string{
		"Foo.m":      "matlab",     // Objective-C was unreachable
		"Foo.mm":     "xml",        // so was Objective-C++
		"env.h":      "c",          // C++ classes in headers were lost
		"Assert.hh":  "cpp",        // Hack yielded a function named `if`
		"sample.fs":  "forth",      // F# was skipped: forth has no definition query
		"init.pp":    "pascal",     // Puppet was skipped the same way
		"mux.v":      "v",          // Verilog and Coq were parsed as V
		"common.inc": "pascal",     // BitBake and SQL includes were skipped
		"tasks.bb":   "bitbake",    // Babashka scripts
		"shader.gs":  "javascript", // GLSL geometry shaders
		"scheme.tl":  "teal",       // Telegram schemas, at 1.4 s each
		"run.es":     "javascript", // Erlang escripts
		"import.Rd":  "r",          // R documentation indexed as R source
		"body.pkb":   "",           // PL/SQL had no mapping at all
		"lib.lib":    "",           // nor Faust libraries
	} {
		got := ""
		if e := grammars.DetectLanguage(name); e != nil {
			got = e.Name
		}
		if got != was {
			t.Errorf("the library now says %q for %s (it said %q): re-check the rule for that extension", got, name, was)
		}
	}
}

func TestHeaderIsPlacedByItsNeighbours(t *testing.T) {
	// A header with nothing language-specific in it: include guards, a macro
	// and a prototype. Content cannot say; the directory can.
	const plain = "#ifndef PORT_H\n#define PORT_H\n#define PORT_VERSION 2\nint port_init(void);\n#endif\n"

	cases := []struct {
		name     string
		siblings []string
		want     string
		method   Method
	}{
		{"beside c++ sources", []string{"port.h", "port.cc", "util.cc"}, "cpp", ByNeighbours},
		{"beside .hpp headers", []string{"port.h", "types.hpp"}, "cpp", ByNeighbours},
		{"beside objective-c sources", []string{"port.h", "AppDelegate.m"}, "objc", ByNeighbours},
		{"beside c sources", []string{"port.h", "port.c"}, "c", ByExtension},
		{"beside both c and c++", []string{"port.h", "port.c", "wrap.cc"}, "c", ByExtension},
		{"beside objective-c and c++", []string{"port.h", "glue.mm", "core.cpp"}, "cpp", ByNeighbours},
		{"alone", []string{"port.h"}, "c", ByExtension},
	}
	det := mustDetector(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := det.Detect("port/port.h", []byte(plain), func() Neighbours {
				calls++
				var n Neighbours
				for _, s := range tc.siblings {
					n.Observe(s)
				}
				return n
			})
			if got.Language != tc.want || got.Method != tc.method {
				t.Errorf("got %q by %q, want %q by %q", got.Language, got.Method, tc.want, tc.method)
			}
			if calls != 1 {
				t.Errorf("neighbours consulted %d times, want exactly once", calls)
			}
		})
	}

	t.Run("content outranks the directory", func(t *testing.T) {
		got := det.Detect("port/api.h", []byte("@interface Api : NSObject\n@end\n"), func() Neighbours {
			t.Error("neighbours consulted although the content had already decided")
			return Neighbours{CPP: true}
		})
		if got.Language != "objc" || got.Method != ByContent {
			t.Errorf("got %q by %q, want objc by content", got.Language, got.Method)
		}
	})
}

func TestShebangPlacesFilesTheNameCannot(t *testing.T) {
	cases := []struct {
		path, content, want string
	}{
		{"sbt", "#!/usr/bin/env bash\n\nset -e\n", "bash"},
		{"bin/deploy", "#!/bin/sh\necho deploy\n", "bash"},
		{"tool", "#!/usr/bin/env python3\nimport sys\n", "python"},
		{"tool", "#!/usr/bin/python3.11\nimport sys\n", "python"},
		{"tool", "#!/usr/bin/python2.7\nimport sys\n", "python"},
		{"julia", "#!/usr/bin/env julia\nprintln(1)\n", "julia"},
		{"runner", "#!/usr/bin/env escript\nmain(_) -> ok.\n", "erlang"},
		{"runner", "#! /usr/bin/env escript\nmain(_) -> ok.\n", "erlang"},
		{"cli", "#!/usr/bin/env node\nconsole.log(1)\n", "javascript"},
		{"cli", "#!/usr/bin/env -S deno run --allow-net\nconsole.log(1)\n", "typescript"},
		{"cli", "#!/usr/bin/env FOO=bar ruby\nputs 1\n", "ruby"},
		{"cli", "#!/usr/local/bin/ruby2.7\nputs 1\n", "ruby"},
		{"cli", "#!/usr/bin/perl -w\nprint 1;\n", "perl"},
		{"cli", "#!/usr/bin/env lua5.4\nprint(1)\n", "lua"},
		{"cli", "#!/usr/bin/env swift\nprint(1)\n", "swift"},
		// The sh-then-exec header: the script is Scala, not shell.
		{"scala", "#!/bin/sh\nexec scala \"$0\" \"$@\"\n!#\n\nobject Main extends App\n", "scala"},
		// An unknown extension is placed the same way.
		{"hooks/pre-commit.sample", "#!/bin/sh\nexit 0\n", "bash"},

		// Not a shebang, or not an interpreter there is a grammar for.
		{"notes", "# just a heading\n", ""},
		{"LICENSE", "MIT License\n", ""},
		{"tool", "#!/usr/bin/env shx\n", ""},      // must not be read as sh
		{"tool", "#!/usr/bin/env lualatex\n", ""}, // nor as lua
		{"tool", "#!/usr/bin/env perl6\n", ""},    // nor as perl
		{"tool", "#!\n", ""},
		{"tool", "", ""},
	}
	det := mustDetector(t)
	for _, tc := range cases {
		got := det.Detect(tc.path, []byte(tc.content), nil)
		if got.Language != tc.want {
			t.Errorf("%s with %q: got %q, want %q", tc.path, firstLine(tc.content), got.Language, tc.want)
		}
		if tc.want != "" && got.Method != ByShebang {
			t.Errorf("%s with %q: method %q, want %q", tc.path, firstLine(tc.content), got.Method, ByShebang)
		}
		if tc.want == "" && got.Method != "" {
			t.Errorf("%s with %q: method %q for a file nothing was detected in", tc.path, firstLine(tc.content), got.Method)
		}
	}

	t.Run("a known extension outranks the shebang", func(t *testing.T) {
		// A .py file is Python whatever its first line claims, and its content
		// is never asked for.
		if det.NeedsContent("tool.py") {
			t.Error("NeedsContent(tool.py) = true")
		}
		if got := det.Detect("tool.py", []byte("#!/bin/sh\n"), nil); got.Language != "python" || got.Method != ByExtension {
			t.Errorf("got %q by %q, want python by extension", got.Language, got.Method)
		}
	})

	t.Run("a shebang that contradicts the extension is ignored", func(t *testing.T) {
		// `.v` can be V, Verilog or Coq, never shell.
		got := det.Detect("x.v", []byte("#!/bin/sh\nmodule main\nfn main() {}\n"), nil)
		if got.Language != "v" || got.Method != ByContent {
			t.Errorf("got %q by %q, want v by content", got.Language, got.Method)
		}
	})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// TestWellKnownNamesAndUnambiguousExtensions records what the library's own
// tables already got right, so that this layer is seen to leave it alone.
func TestWellKnownNamesAndUnambiguousExtensions(t *testing.T) {
	det := mustDetector(t)
	for path, want := range map[string]struct {
		language string
		method   Method
	}{
		"Makefile":             {"make", ByFilename},
		"Dockerfile":           {"dockerfile", ByFilename},
		"Rakefile":             {"ruby", ByFilename},
		"Gemfile":              {"ruby", ByFilename},
		"BUILD":                {"starlark", ByFilename},
		"WORKSPACE":            {"starlark", ByFilename},
		"Jenkinsfile":          {"groovy", ByFilename},
		"Vagrantfile":          {"ruby", ByFilename},
		"CMakeLists.txt":       {"cmake", ByFilename},
		"sub/dir/Makefile":     {"make", ByFilename},
		"stand/Makefile.inc":   {"make", ByFilename}, // not whatever .inc is
		"main.go":              {"go", ByExtension},
		"server.cjs":           {"javascript", ByExtension},
		"server.mjs":           {"javascript", ByExtension},
		"types.mts":            {"typescript", ByExtension},
		"types.cts":            {"typescript", ByExtension},
		"pkg/body.pkb":         {"sql", ByExtension},
		"pkg/spec.pks":         {"sql", ByExtension},
		"views/a.blade.php":    {"blade", ByFilename},
		"a.component.html":     {"html", ByExtension},
		"README":               {"", ""},
		"data.unknownext":      {"", ""},
		"assets/logo.png":      {"", ""},
		"Dockerfile.dev":       {"", ""},
		"weird.name.with.dots": {"", ""},
	} {
		got := det.Detect(path, []byte("plain text\n"), nil)
		if got.Language != want.language || got.Method != want.method {
			t.Errorf("Detect(%q) = %q by %q, want %q by %q", path, got.Language, got.Method, want.language, want.method)
		}
	}
}

func TestContentThatWasNotReadIsReportedAsAGuess(t *testing.T) {
	det := mustDetector(t)

	// nil head: the walker skipped the file (too large, unreadable).
	if got := det.Detect("big.m", nil, nil); got.Language != "matlab" || !got.Ambiguous() {
		t.Errorf("unread .m: got %q by %q, want matlab by %q", got.Language, got.Method, ByAmbiguousExtension)
	}
	// A header's plain reading is safe, so it is not a guess even unread.
	if got := det.Detect("big.h", nil, nil); got.Language != "c" || got.Ambiguous() {
		t.Errorf("unread .h: got %q by %q, want c by extension", got.Language, got.Method)
	}
	// An empty file is not a guess either: there is nothing to be wrong about.
	if got := det.Detect("empty.m", []byte{}, nil); got.Language != "matlab" || got.Method != ByExtension {
		t.Errorf("empty .m: got %q by %q, want matlab by extension", got.Language, got.Method)
	}
	if got := det.Detect("blank.v", []byte("\n\n   \n"), nil); got.Language != "v" || got.Method != ByExtension {
		t.Errorf("blank .v: got %q by %q, want v by extension", got.Language, got.Method)
	}
}

func TestOnlyTheHeadIsExamined(t *testing.T) {
	// The marker sits beyond HeadBytes. Detection must not see it: the walker
	// hands over HeadBytes and no more, and a caller holding the whole file
	// has to get the same answer.
	padding := strings.Repeat("just prose, nothing a rule could match\n", HeadBytes/39+1)
	content := []byte(padding + "@implementation Late\n@end\n")
	det := mustDetector(t)

	got := det.Detect("Late.m", content[:HeadBytes], nil)
	if got.Language != "matlab" || !got.Ambiguous() {
		t.Errorf("got %q by %q, want the ambiguous default", got.Language, got.Method)
	}
}

func TestOverrides(t *testing.T) {
	suffix := func(s string) func(string) bool {
		return func(rel string) bool { return strings.HasSuffix(rel, s) }
	}
	prefix := func(s string) func(string) bool {
		return func(rel string) bool { return strings.HasPrefix(rel, s) }
	}
	det := mustDetector(t,
		Override{Pattern: "*.h", Match: suffix(".h"), Language: "C++"},
		Override{Pattern: "third_party/**", Match: prefix("third_party/"), Language: "none"},
		Override{Pattern: "legacy/*.h", Match: func(rel string) bool {
			return strings.HasPrefix(rel, "legacy/") && strings.HasSuffix(rel, ".h")
		}, Language: "objective-c"},
		Override{Pattern: "*.component.html", Match: suffix(".component.html"), Language: "angular"},
	)

	for path, want := range map[string]string{
		"src/plain.h":           "cpp",     // a plain C header, forced
		"legacy/old.h":          "objc",    // a later entry narrows an earlier one
		"third_party/zlib/z.c":  "",        // marked as not to be parsed
		"app/x.component.html":  "angular", // a grammar no extension reaches
		"third_party/include.h": "",        // the LAST matching entry wins: *.h came first
	} {
		got := det.Detect(path, []byte("int f(void);\n"), nil)
		if got.Language != want || got.Method != ByOverride {
			t.Errorf("Detect(%q) = %q by %q, want %q by override", path, got.Language, got.Method, want)
		}
		if det.NeedsContent(path) {
			t.Errorf("NeedsContent(%q) = true for an overridden file", path)
		}
	}

	// A file no entry matches is detected as usual.
	if got := det.Detect("src/main.go", nil, nil); got.Language != "go" || got.Method != ByExtension {
		t.Errorf("unmatched file: got %q by %q", got.Language, got.Method)
	}
}

func TestOverridesAreValidated(t *testing.T) {
	always := func(string) bool { return true }

	if _, err := New([]Override{{Pattern: "*.m", Match: always, Language: "objectivec++"}}); err == nil {
		t.Error("an unknown language was accepted; it must be refused, not ignored")
	} else if !strings.Contains(err.Error(), "objectivec++") || !strings.Contains(err.Error(), "*.m") {
		t.Errorf("the error does not name the language and the pattern: %v", err)
	}

	if _, err := New([]Override{{Pattern: "!*.m", Language: "objc"}}); err == nil {
		t.Error("an override with no usable matcher was accepted")
	}

	many := make([]Override, MaxOverrides+1)
	for i := range many {
		many[i] = Override{Pattern: "*.m", Match: always, Language: "objc"}
	}
	if _, err := New(many); err == nil {
		t.Errorf("%d overrides were accepted; the limit is %d", len(many), MaxOverrides)
	}

	// Every spelling the documentation offers resolves.
	for _, name := range []string{"objc", "Objective-C", "objective-c++", "C++", "cpp", "F#", "fsharp", "verilog", "SystemVerilog", "none", "NONE"} {
		if _, err := New([]Override{{Pattern: "x", Match: always, Language: name}}); err != nil {
			t.Errorf("language %q was refused: %v", name, err)
		}
	}
}

// TestEveryRuleTargetIsARealGrammar keeps the rule table honest: a rule that
// routes files to a grammar name the library does not have would send them to
// "no grammar" with nothing to say why.
func TestEveryRuleTargetIsARealGrammar(t *testing.T) {
	check := func(where, name string) {
		if name == "" {
			return
		}
		if e := grammars.DetectLanguageByName(name); e == nil || e.Name != name {
			t.Errorf("%s routes to %q, which is not a grammar name", where, name)
		}
	}
	for ext, rule := range extensionRules() {
		if ext != strings.ToLower(ext) || !strings.HasPrefix(ext, ".") {
			t.Errorf("rule key %q must be a lower-case extension with its dot", ext)
		}
		for _, c := range rule.content {
			check("the "+ext+" rule", c.language)
			if c.language == "" && c.recognised == "" {
				t.Errorf("the %s rule has a content rule that names nothing", ext)
			}
			if len(c.markers) == 0 {
				t.Errorf("the %s rule for %q%q has no marker", ext, c.language, c.recognised)
			}
		}
	}
	for ext, name := range extraExtensions {
		check("extra extension "+ext, name)
	}
	for interp, name := range extraInterpreters {
		check("interpreter "+interp, name)
	}
	for alias, name := range languageAliases {
		check("alias "+alias, name)
	}
}

// TestFoldedMarkersAreLowerCase guards the one rule a folded marker has: it is
// matched against a lower-cased copy of the text, so an upper-case letter in
// its literal can never match anything.
func TestFoldedMarkersAreLowerCase(t *testing.T) {
	for ext, rule := range extensionRules() {
		for _, c := range rule.content {
			for _, m := range append(append([]marker{}, c.markers...), c.also...) {
				if m.fold && string(m.lit) != strings.ToLower(string(m.lit)) {
					t.Errorf("the %s rule has a folded marker %q with an upper-case letter", ext, m.lit)
				}
			}
		}
	}
}

func TestDetectionIsDeterministicAndTotal(t *testing.T) {
	// Arbitrary bytes, including invalid UTF-8 and a lone CR: detection must
	// answer, and answer the same way twice.
	inputs := [][]byte{
		nil, {}, {0xff, 0xfe, 0x00}, []byte("\r"), []byte("#!"), []byte("#!\xff\xff"),
		[]byte(strings.Repeat("(", HeadBytes)), []byte(strings.Repeat(";", HeadBytes)),
		[]byte(strings.Repeat("class ", HeadBytes/6)), []byte(strings.Repeat("::", HeadBytes/2)),
	}
	det := mustDetector(t)
	for ext := range extensionRules() {
		for _, in := range inputs {
			a := det.Detect("f"+ext, in, func() Neighbours { return Neighbours{} })
			b := det.Detect("f"+ext, in, func() Neighbours { return Neighbours{} })
			if a != b {
				t.Errorf("%s: two detections of the same bytes disagree: %+v and %+v", ext, a, b)
			}
		}
	}
}

func FuzzDetect(f *testing.F) {
	f.Add("a.m", []byte("@implementation X\n@end\n"))
	f.Add("a.h", []byte("class X {};\n"))
	f.Add("a.v", []byte("module m(); endmodule\n"))
	f.Add("tool", []byte("#!/usr/bin/env python3\n"))
	f.Add("a.inc", []byte("<?php\n"))
	det, err := New(nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, name string, head []byte) {
		if len(head) > HeadBytes {
			head = head[:HeadBytes]
		}
		got := det.Detect(name, head, func() Neighbours { return Neighbours{CPP: true} })
		if got.Language != "" && got.Method == "" {
			t.Errorf("a language %q with no method", got.Language)
		}
		if got.Language != "" && grammars.DetectLanguageByName(got.Language) == nil {
			t.Errorf("detected %q, which no grammar has", got.Language)
		}
	})
}
