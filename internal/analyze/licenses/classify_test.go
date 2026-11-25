package licenses

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Each canonical text is a real LICENSE file as projects ship it, named by
// its SPDX identifier. The short licences match their template exactly; the
// long ones are recognised by title and fingerprint, which is high.
func TestClassifyCanonicalTexts(t *testing.T) {
	want := map[string]string{
		"MIT": Exact, "ISC": Exact, "BSD-2-Clause": Exact, "BSD-3-Clause": Exact, "Zlib": Exact,
		"Apache-2.0": High, "MPL-2.0": High, "GPL-2.0-only": High, "GPL-3.0-only": High,
		"LGPL-2.1-only": High, "CC0-1.0": High, "Unlicense": High,
	}
	ents, err := os.ReadDir(filepath.Join("testdata", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != len(want) {
		t.Errorf("%d canonical texts, %d expectations", len(ents), len(want))
	}
	for _, e := range ents {
		id := e.Name()
		m := Classify(readTestdata(t, "canonical", id))
		if m.ID != id || m.Confidence != want[id] {
			t.Errorf("%s: got %q %s (%s), want %s", id, m.ID, m.Confidence, m.How, want[id])
		}
	}
}

// The excerpts carry a licence's title and its section headings, not the
// whole text: what a truncated or abridged copy looks like.
func TestClassifyExcerpts(t *testing.T) {
	for _, id := range []string{"AGPL-3.0-only", "LGPL-3.0-only", "EPL-2.0"} {
		m := Classify(readTestdata(t, "excerpt", id))
		if m.ID != id || (m.Confidence != High && m.Confidence != Medium) {
			t.Errorf("%s: got %q %s", id, m.ID, m.Confidence)
		}
	}
}

var copyrightRE = regexp.MustCompile(`(?m)^.*Copyright.*$`)

// Lightly altered copies: another year and holder, the holder named in the
// disclaimer, and the text reflowed to another width.
func TestClassifyAlteredCopies(t *testing.T) {
	reflow := func(s string, width int) string {
		var b strings.Builder
		n := 0
		for _, w := range strings.Fields(s) {
			if n+len(w) > width {
				b.WriteByte('\n')
				n = 0
			}
			b.WriteString(w + " ")
			n += len(w) + 1
		}
		return b.String()
	}
	for _, id := range []string{"MIT", "ISC", "BSD-2-Clause", "BSD-3-Clause", "Zlib", "Apache-2.0", "GPL-3.0-only", "MPL-2.0"} {
		src := string(readTestdata(t, "canonical", id))
		src = copyrightRE.ReplaceAllString(src, "Copyright © 2031 Example Widgets GmbH & Contributors")
		src = strings.ReplaceAll(src, "THE AUTHORS OR COPYRIGHT HOLDERS", "EXAMPLE WIDGETS GMBH")
		src = strings.ReplaceAll(src, "Google LLC", "Example Widgets GmbH")
		src = strings.ReplaceAll(src, "Google Inc.", "Example Widgets GmbH")
		for _, s := range []string{src, reflow(src, 60), reflow(src, 100)} {
			m := Classify([]byte(s))
			if m.ID != id {
				t.Errorf("altered %s: got %q %s", id, m.ID, m.Confidence)
			}
		}
	}
}

func TestClassifyTemplateWithExtraTextIsHighNotExact(t *testing.T) {
	mit := string(readTestdata(t, "canonical", "MIT"))
	m := Classify([]byte(mit + "\n\nThis product bundles third-party components; see NOTICE for their terms, which are listed one by one below with the version of each and where it came from.\n"))
	if m.ID != "MIT" || m.Confidence != High {
		t.Errorf("got %q %s", m.ID, m.Confidence)
	}
}

func TestClassifySPDXIdentifier(t *testing.T) {
	for in, want := range map[string]string{
		"// SPDX-License-Identifier: Apache-2.0\npackage x\n":                    "Apache-2.0",
		"/* SPDX-License-Identifier: MIT OR Apache-2.0 */":                       "MIT OR Apache-2.0",
		"# SPDX-License-Identifier: gpl-2.0-only WITH Linux-syscall-note":        "",
		"<!-- SPDX-License-Identifier: (MIT AND BSD-3-Clause) -->":               "(MIT AND BSD-3-Clause)",
		"SPDX-License-Identifier: GPL-2.0-or-later WITH Classpath-exception-2.0": "GPL-2.0-or-later WITH Classpath-exception-2.0",
	} {
		m := Classify([]byte(in))
		if want == "" {
			// Linux-syscall-note is not an exception name this package knows.
			if m.How == "spdx-identifier" {
				t.Errorf("%q: accepted %q", in, m.ID)
			}
			continue
		}
		if m.ID != want || m.Confidence != Exact || m.How != "spdx-identifier" {
			t.Errorf("%q: got %+v, want %s", in, m, want)
		}
	}
}

func TestClassifyUnrecognised(t *testing.T) {
	for _, s := range []string{
		"",
		"This project is great. See the docs for how to install it.",
		"All rights reserved. No part of this software may be copied.",
		"Licensed under the Foo Public License, whatever that is.",
	} {
		if m := Classify([]byte(s)); m.ID != "" {
			t.Errorf("%q classified as %+v", s, m)
		}
	}
}

func TestClassifyNotice(t *testing.T) {
	m := Classify([]byte(`Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.`))
	if m.ID != "Apache-2.0" || m.Confidence != Medium || m.How != "notice" {
		t.Errorf("got %+v", m)
	}
	m = Classify([]byte(`This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.`))
	if m.ID != "GPL-3.0-or-later" {
		t.Errorf("got %+v", m)
	}
}

func TestNormalizeExpression(t *testing.T) {
	for in, want := range map[string]string{
		"MIT":                                  "MIT",
		"mit":                                  "MIT",
		"apache-2.0 or mit":                    "Apache-2.0 OR MIT",
		"MIT/Apache-2.0":                       "MIT OR Apache-2.0",
		"(MIT OR Apache-2.0)":                  "(MIT OR Apache-2.0)",
		"LicenseRef-Proprietary":               "LicenseRef-Proprietary",
		"Apache 2.0":                           "",
		"MIT OR":                               "",
		"BSD":                                  "",
		"GPL-2.0 WITH Classpath-exception-2.0": "GPL-2.0 WITH Classpath-exception-2.0",
	} {
		if got := NormalizeExpression(in); got != want {
			t.Errorf("NormalizeExpression(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCategoryOf(t *testing.T) {
	for in, want := range map[string]Category{
		"MIT":                                   Permissive,
		"MIT OR GPL-3.0-only":                   Permissive,
		"MIT AND GPL-3.0-only":                  StrongCopyleft,
		"MPL-2.0":                               WeakCopyleft,
		"(LGPL-2.1-only OR MIT) AND Apache-2.0": Permissive,
		"GPL-2.0-only WITH Classpath-exception-2.0": WeakCopyleft,
		"AGPL-3.0-only":   StrongCopyleft,
		"LicenseRef-Acme": Other,
		"unknown":         Unknown,
	} {
		if got := CategoryOf(in); got != want {
			t.Errorf("CategoryOf(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestFromNameAndURL(t *testing.T) {
	for in, want := range map[string]string{
		"The Apache Software License, Version 2.0": "Apache-2.0",
		"MIT License":                    "MIT",
		"Eclipse Public License - v 2.0": "EPL-2.0",
		"BSD License":                    "",
		"GPL":                            "",
	} {
		if got := FromName(in); got != want {
			t.Errorf("FromName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"https://www.apache.org/licenses/LICENSE-2.0.txt":    "Apache-2.0",
		"https://opensource.org/licenses/MIT":                "MIT",
		"http://www.opensource.org/licenses/bsd-license.php": "BSD-2-Clause",
		"https://example.com/license":                        "",
	} {
		if got := FromURL(in); got != want {
			t.Errorf("FromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
