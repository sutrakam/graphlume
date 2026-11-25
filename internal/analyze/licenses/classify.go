package licenses

import (
	"regexp"
	"strings"
)

// The classifier identifies licence TEXT. It is deliberately small and
// deliberately conservative: a licence it cannot place is "unknown", never the
// nearest match.
//
// Three kinds of evidence, strongest first:
//
//  1. An SPDX-License-Identifier line. The author wrote the identifier down,
//     so it is taken as written (exact) when it is a valid expression.
//  2. A canonical template. The short permissive licences (MIT, ISC, 0BSD,
//     BSD-2-Clause, BSD-3-Clause, Zlib) are bundled as their SPDX list texts,
//     normalised, with the copyright holder and similar fields as bounded
//     wildcards. The whole template matched with next to nothing around it is
//     exact; matched inside a longer file (a NOTICE appended, several licences
//     in one file) is high.
//  3. Fingerprints. The long licences (Apache-2.0, MPL-2.0, the GPL family,
//     EPL-2.0, CC0-1.0, Unlicense) are recognised by their title line, which
//     names the version, and a set of distinctive phrases from their SPDX list
//     texts. Title plus most phrases is high; fewer is medium or low. A bare
//     licence NOTICE ("Licensed under the Apache License, Version 2.0 ...") is
//     medium: it names the licence but is not the licence.
//
// Normalisation lower-cases, drops copyright lines and list markers, joins
// hyphenated words, and reduces everything else that is not a letter or digit
// to single spaces, so reflowed or re-punctuated copies still match.

// Confidence grades, as the rest of GraphLume uses them.
const (
	Exact  = "exact"
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// Match is what the classifier found in one text.
type Match struct {
	// ID is an SPDX expression, or "" when nothing was recognised.
	ID         string
	Confidence string
	// How is "spdx-identifier", "template", "fingerprint" or "notice".
	How string
}

// maxClassifyBytes bounds the text classified. Every licence here is under
// 40 KB; a longer file is a bundle of many, and its head is what matters.
const maxClassifyBytes = 256 << 10

var spdxLine = regexp.MustCompile(`(?mi)SPDX-License-Identifier:\s*(.+?)\s*(?:\*/|-->|#\})?\s*$`)

// Classify identifies the licence in text.
func Classify(text []byte) Match {
	if len(text) > maxClassifyBytes {
		text = text[:maxClassifyBytes]
	}
	src := string(text)
	if ms := spdxLine.FindAllStringSubmatch(src, 8); len(ms) > 0 {
		var ids []string
		for _, m := range ms {
			if e := NormalizeExpression(strings.Trim(m[1], `"' `)); e != "" {
				ids = append(ids, e)
			}
		}
		if len(ids) > 0 {
			return Match{ID: joinIDs(ids, "AND"), Confidence: Exact, How: "spdx-identifier"}
		}
	}
	norm := normalize(src)
	if norm == "" {
		return Match{}
	}
	if m, ok := matchTemplates(norm); ok {
		return m
	}
	if m, ok := matchFingerprints(norm); ok {
		return m
	}
	for _, n := range notices {
		if strings.Contains(norm, n.phrase) {
			return Match{ID: n.id, Confidence: Medium, How: "notice"}
		}
	}
	return Match{}
}

var (
	listMarker = regexp.MustCompile(`^\s*(?:\(?[0-9]{1,2}[.)]|\(?[a-z][.)]|[*\-•])\s+`)
	// copyrightLine is a notice line ("Copyright (c) 2020 Ada", "Copyright The
	// Go Authors"), not a sentence of the licence that wraps onto a line
	// starting with "copyright notice" or "COPYRIGHT HOLDERS".
	copyrightLine = regexp.MustCompile(`^\s*(?:(?i:copyright)\s*(?:\([cC]\)|©|[0-9]|:)|[Cc]opyright\s+[A-Z][a-z]|\([cC]\)\s|©|(?i:all rights reserved))`)
	hyphenJoin    = regexp.MustCompile(`([a-z])[-‐–]\s*\n?\s*([a-z])`)
	nonAlnum      = regexp.MustCompile(`[^a-z0-9]+`)
)

// spellings folds the variants licence copies differ by.
var spellings = strings.NewReplacer(
	"licence", "license", "acknowledgement", "acknowledgment", "&", " and ",
	"'", "", "’", "", "\"", " ", "“", " ", "”", " ",
)

func normalize(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if copyrightLine.MatchString(line) {
			continue
		}
		line = listMarker.ReplaceAllString(line, "")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	t := strings.ToLower(b.String())
	t = spellings.Replace(t)
	t = hyphenJoin.ReplaceAllString(t, "$1$2")
	t = nonAlnum.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

// ---------------------------------------------------------------- templates

// wild marks a field in a template: a holder's name, "the author" or "the
// authors", and similar. It matches up to maxWildChars of normalised text.
const (
	wild         = "<<var>>"
	maxWildChars = 200
	// exactSlack is how much text may surround a template before the match
	// stops being exact: a title line ("The MIT License (MIT)") is fine, a
	// NOTICE section is not.
	exactSlack = 80
)

type template struct {
	id       string
	segments []string
}

// templateTexts are the SPDX list texts of the short licences, copyright
// lines removed. SPDX list texts are CC0-licensed.
var templateTexts = []struct{ id, text string }{
	{"MIT", `Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:
The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL <<var>> BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.`},
	{"MIT-0", `Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so.
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL <<var>> BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.`},
	{"ISC", `Permission to use, copy, modify, and<<var>>distribute this software for any purpose with or without fee is hereby granted, provided that the above copyright notice and this permission notice appear in all copies.
THE SOFTWARE IS PROVIDED "AS IS" AND <<var>> DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL <<var>> BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.`},
	{"0BSD", `Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted.
THE SOFTWARE IS PROVIDED "AS IS" AND <<var>> DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL <<var>> BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.`},
	{"BSD-3-Clause", `Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:
1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
3. Neither the name of <<var>> may be used to endorse or promote products derived from this software without specific prior written permission.
THIS SOFTWARE IS PROVIDED BY <<var>> "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL <<var>> BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.`},
	{"BSD-2-Clause", `Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:
1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
THIS SOFTWARE IS PROVIDED BY <<var>> "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL <<var>> BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.`},
	{"Zlib", `This software is provided 'as-is', without any express or implied warranty. In no event will the authors be held liable for any damages arising from the use of this software.
Permission is granted to anyone to use this software for any purpose, including commercial applications, and to alter it and redistribute it freely, subject to the following restrictions:
1. The origin of this software must not be misrepresented; you must not claim that you wrote the original software. If you use this software in a product, an acknowledgment in the product documentation would be appreciated but is not required.
2. Altered source versions must be plainly marked as such, and must not be misrepresented as being the original software.
3. This notice may not be removed or altered from any source distribution.`},
}

var templates = func() []template {
	out := make([]template, 0, len(templateTexts))
	for _, t := range templateTexts {
		parts := strings.Split(t.text, wild)
		tpl := template{id: t.id}
		for _, p := range parts {
			tpl.segments = append(tpl.segments, normalize(p))
		}
		out = append(out, tpl)
	}
	return out
}()

// matchTemplates tries each template against the normalised text and keeps
// the match with the least surrounding text.
func matchTemplates(norm string) (Match, bool) {
	best, bestExtra := "", -1
	for _, t := range templates {
		extra, ok := t.match(norm)
		if !ok {
			continue
		}
		if bestExtra < 0 || extra < bestExtra {
			best, bestExtra = t.id, extra
		}
	}
	if bestExtra < 0 {
		return Match{}, false
	}
	conf := High
	if bestExtra <= exactSlack {
		conf = Exact
	}
	return Match{ID: best, Confidence: conf, How: "template"}, true
}

// match reports whether every segment occurs in order, each wildcard gap
// within bounds, and how many characters of text lie outside the match.
func (t template) match(norm string) (int, bool) {
	start := strings.Index(norm, t.segments[0])
	if start < 0 {
		return 0, false
	}
	// The first segment may occur more than once; try each occurrence.
	for start >= 0 {
		pos, ok := start+len(t.segments[0]), true
		for _, seg := range t.segments[1:] {
			if seg == "" {
				continue
			}
			i := strings.Index(norm[pos:], seg)
			if i < 0 || i > maxWildChars {
				ok = false
				break
			}
			pos += i + len(seg)
		}
		if ok {
			return start + (len(norm) - pos), true
		}
		next := strings.Index(norm[start+1:], t.segments[0])
		if next < 0 {
			break
		}
		start += 1 + next
	}
	return 0, false
}

// ---------------------------------------------------------------- fingerprints

type fingerprint struct {
	id string
	// title names the licence and its version; without it a match is at
	// most low, because the GPL family shares most of its phrasing.
	title   string
	phrases []string
	// absent are phrases whose presence rules this licence out.
	absent []string
}

// fingerprintTexts are short phrases from the SPDX list texts, written as
// they appear there and normalised at start-up.
var fingerprintTexts = []fingerprint{
	{id: "Apache-2.0", title: "Apache License Version 2.0, January 2004", phrases: []string{
		"TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION",
		"Grant of Copyright License",
		"Grant of Patent License",
		"You may reproduce and distribute copies of the Work or Derivative Works thereof",
		"Submission of Contributions",
		"This License does not grant permission to use the trade names",
		"Disclaimer of Warranty",
		"Accepting Warranty or Additional Liability",
	}},
	{id: "MPL-2.0", title: "Mozilla Public License Version 2.0", phrases: []string{
		"means each individual or legal entity that creates, contributes to the creation of, or owns Covered Software",
		"Incompatible With Secondary Licenses",
		"Covered Software is provided under this License on an \"as is\" basis",
		"Responsibilities",
		"Litigation",
		"This Source Code Form is subject to the terms of the Mozilla Public License",
	}},
	{id: "AGPL-3.0-only", title: "GNU AFFERO GENERAL PUBLIC LICENSE Version 3, 19 November 2007", phrases: []string{
		"\"This License\" refers to version 3 of the GNU Affero General Public License",
		"Remote Network Interaction; Use with the GNU General Public License",
		"Conveying Verbatim Copies",
		"Conveying Modified Source Versions",
		"Automatic Termination",
		"Acceptance Not Required for Having Copies",
	}},
	{id: "LGPL-3.0-only", title: "GNU LESSER GENERAL PUBLIC LICENSE Version 3, 29 June 2007", phrases: []string{
		"incorporates the terms and conditions of version 3 of the GNU General Public License, supplemented by the additional permissions listed below",
		"Additional Definitions",
		"Exception to Section 3 of the GNU GPL",
		"Object Code Incorporating Material from Library Header Files",
		"Combined Works",
		"Combined Libraries",
		"Revised Versions of the GNU Lesser General Public License",
	}},
	{id: "GPL-3.0-only", title: "GNU GENERAL PUBLIC LICENSE Version 3, 29 June 2007", phrases: []string{
		"The GNU General Public License is a free, copyleft license for software and other kinds of works",
		"\"This License\" refers to version 3 of the GNU General Public License",
		"Conveying Verbatim Copies",
		"Conveying Modified Source Versions",
		"Conveying Non-Source Forms",
		"Automatic Termination",
		"Use with the GNU Affero General Public License",
	}, absent: []string{"GNU LESSER GENERAL PUBLIC LICENSE Version 3, 29 June 2007", "GNU AFFERO GENERAL PUBLIC LICENSE Version 3, 19 November 2007"}},
	{id: "LGPL-2.1-only", title: "GNU LESSER GENERAL PUBLIC LICENSE Version 2.1, February 1999", phrases: []string{
		"This is the first released version of the Lesser GPL",
		"A \"library\" means a collection of software functions and/or data prepared so as to be conveniently linked with application programs",
		"work that uses the Library",
		"You may opt to apply the terms of the ordinary GNU General Public License instead of this License to a given copy of the Library",
		"HOW TO APPLY THESE TERMS TO YOUR NEW LIBRARIES",
	}},
	{id: "GPL-2.0-only", title: "GNU GENERAL PUBLIC LICENSE Version 2, June 1991", phrases: []string{
		"The licenses for most software are designed to take away your freedom to share and change it",
		"This License applies to any program or other work which contains a notice placed by the copyright holder saying it may be distributed under the terms of this General Public License",
		"You may copy and distribute verbatim copies of the Program's source code",
		"If the distribution and/or use of the Program is restricted in certain countries",
		"BECAUSE THE PROGRAM IS LICENSED FREE OF CHARGE, THERE IS NO WARRANTY FOR THE PROGRAM",
	}, absent: []string{"GNU LESSER GENERAL PUBLIC LICENSE Version 2.1, February 1999", "GNU LIBRARY GENERAL PUBLIC LICENSE Version 2, June 1991"}},
	{id: "EPL-2.0", title: "Eclipse Public License - v 2.0", phrases: []string{
		"THE ACCOMPANYING PROGRAM IS PROVIDED UNDER THE TERMS OF THIS ECLIPSE PUBLIC LICENSE",
		"\"Secondary License\" means either the GNU General Public License, Version 2.0",
		"COMMERCIAL DISTRIBUTION",
		"Exhibit A - Form of Secondary Licenses Notice",
		"\"Contribution\" means",
	}},
	{id: "CC0-1.0", title: "CC0 1.0 Universal", phrases: []string{
		"CREATIVE COMMONS CORPORATION IS NOT A LAW FIRM",
		"Statement of Purpose",
		"Copyright and Related Rights",
		"Public License Fallback",
		"Affirmer",
	}},
	{id: "Unlicense", title: "This is free and unencumbered software released into the public domain", phrases: []string{
		"Anyone is free to copy, modify, publish, use, compile, sell, or distribute this software",
		"dedicate any and all copyright interest in the software to the public domain",
		"For more information, please refer to",
	}},
	// The short licences again, for copies altered too far for a template.
	{id: "MIT", title: "Permission is hereby granted, free of charge, to any person obtaining a copy", phrases: []string{
		"to deal in the Software without restriction",
		"The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software",
		"WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED",
	}},
	{id: "BSD-3-Clause", title: "Redistribution and use in source and binary forms, with or without modification, are permitted", phrases: []string{
		"Redistributions of source code must retain the above copyright notice",
		"Redistributions in binary form must reproduce the above copyright notice",
		"may be used to endorse or promote products derived from this software without specific prior written permission",
	}, absent: []string{"All advertising materials mentioning features or use of this software"}},
	{id: "BSD-2-Clause", title: "Redistribution and use in source and binary forms, with or without modification, are permitted", phrases: []string{
		"Redistributions of source code must retain the above copyright notice",
		"Redistributions in binary form must reproduce the above copyright notice",
		"IS PROVIDED BY",
	}, absent: []string{"endorse or promote products", "All advertising materials mentioning features"}},
	{id: "ISC", title: "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted, provided that", phrases: []string{
		"the above copyright notice and this permission notice appear in all copies",
		"DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS SOFTWARE",
	}},
	{id: "Zlib", title: "Permission is granted to anyone to use this software for any purpose, including commercial applications", phrases: []string{
		"Altered source versions must be plainly marked as such",
		"This notice may not be removed or altered from any source distribution",
	}},
}

var fingerprints = func() []fingerprint {
	out := make([]fingerprint, len(fingerprintTexts))
	for i, f := range fingerprintTexts {
		g := fingerprint{id: f.id, title: normalize(f.title)}
		for _, p := range f.phrases {
			g.phrases = append(g.phrases, normalize(p))
		}
		for _, p := range f.absent {
			g.absent = append(g.absent, normalize(p))
		}
		out[i] = g
	}
	return out
}()

// matchFingerprints scores every fingerprint and keeps the best. Ties go to
// the earlier entry, which is why the longer licences are listed first.
func matchFingerprints(norm string) (Match, bool) {
	var best Match
	bestScore := 0.0
	for _, f := range fingerprints {
		excluded := false
		for _, a := range f.absent {
			if strings.Contains(norm, a) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		titled := strings.Contains(norm, f.title)
		hit := 0
		for _, p := range f.phrases {
			if strings.Contains(norm, p) {
				hit++
			}
		}
		frac := float64(hit) / float64(len(f.phrases))
		var conf string
		switch {
		case titled && frac >= 0.8:
			conf = High
		case titled && frac >= 0.5, !titled && frac >= 0.8:
			conf = Medium
		case titled, frac >= 0.5:
			conf = Low
		default:
			continue
		}
		score := frac
		if titled {
			score++
		}
		if score > bestScore {
			best, bestScore = Match{ID: f.id, Confidence: conf, How: "fingerprint"}, score
		}
	}
	return best, bestScore > 0
}

// notices are the standard short licence notices a source header or README
// carries instead of the licence.
var notices = func() []struct{ phrase, id string } {
	raw := []struct{ phrase, id string }{
		{"Licensed under the Apache License, Version 2.0", "Apache-2.0"},
		{"GNU General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version", "GPL-3.0-or-later"},
		{"GNU General Public License as published by the Free Software Foundation; either version 2 of the License, or (at your option) any later version", "GPL-2.0-or-later"},
		{"GNU Lesser General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version", "LGPL-3.0-or-later"},
		{"GNU Lesser General Public License as published by the Free Software Foundation; either version 2.1 of the License, or (at your option) any later version", "LGPL-2.1-or-later"},
		{"GNU Affero General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version", "AGPL-3.0-or-later"},
		{"This Source Code Form is subject to the terms of the Mozilla Public License, v. 2.0", "MPL-2.0"},
	}
	for i := range raw {
		raw[i].phrase = normalize(raw[i].phrase)
	}
	return raw
}()
