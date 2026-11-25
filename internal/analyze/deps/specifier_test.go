package deps

import (
	"strings"
	"testing"
)

// TestRequirementSpecifierIsOnlyTheSpecifier: a requirements line can carry an
// environment marker, pip options and a continuation after the version. None
// of that is the version, and a requirement with a marker and NO version is
// still a dependency.
func TestRequirementSpecifierIsOnlyTheSpecifier(t *testing.T) {
	src := strings.Join([]string{
		`requests==2.31.0`,
		`jinja2==2.11.2 ; python_version >= "3.6"`,
		`Django==3.2.0 --hash=sha256:abc \`,
		`pywin32 ; sys_platform == "win32"`,
		`uvicorn[standard]>=0.20,<1.0  # the server`,
		`flask`,
		`direct @ https://example.invalid/direct-1.0.tar.gz`,
		`-e ./local-package`,
		`--index-url https://example.invalid/simple`,
		`not a requirement line`,
	}, "\n")
	m, ok := Parse("requirements.txt", []byte(src))
	if !ok {
		t.Fatal("requirements.txt not recognised")
	}
	want := map[string]string{
		"requests": "==2.31.0",
		"jinja2":   "==2.11.2",
		"Django":   "==3.2.0",
		"pywin32":  "",
		"uvicorn":  ">=0.20,<1.0",
		"flask":    "",
		"direct":   "@ https://example.invalid/direct-1.0.tar.gz",
	}
	got := index(m.Dependencies)
	if len(got) != len(want) {
		t.Errorf("parsed %v, want exactly %d requirements", names(m.Dependencies), len(want))
	}
	for name, version := range want {
		d, found := got[name]
		if !found {
			t.Errorf("%s was not read as a requirement", name)
			continue
		}
		if d.Version != version {
			t.Errorf("%s: version = %q, want %q", name, d.Version, version)
		}
	}
}

// TestPyProjectKeepsTheOperator: ">=2.0" and "==2.0" are different statements.
// Recording both as "2.0" lets a range be read as a pin by anything that
// compares versions, which is the one mistake a vulnerability check cannot
// afford.
func TestPyProjectKeepsTheOperator(t *testing.T) {
	src := `[project]
name = "svc"
dependencies = [
    "requests>=2.0,<3",   # a range
    "idna==3.4",
    'uvicorn[standard]~=0.20',
    "jinja2==2.11.2; python_version >= '3.6'",
    "flask",
]
requires-python = ">=3.9"
`
	m, ok := Parse("pyproject.toml", []byte(src))
	if !ok {
		t.Fatal("pyproject.toml not recognised")
	}
	want := map[string]string{
		"requests": ">=2.0,<3",
		"idna":     "==3.4",
		"uvicorn":  "~=0.20",
		"jinja2":   "==2.11.2",
		"flask":    "",
	}
	got := index(m.Dependencies)
	if len(got) != len(want) {
		t.Errorf("parsed %v, want exactly %d; requires-python is not a dependency", names(m.Dependencies), len(want))
	}
	for name, version := range want {
		if d, found := got[name]; !found || d.Version != version {
			t.Errorf("%s: got %+v (found %v), want version %q", name, d, found, version)
		}
	}
}

// TestPyProjectSingleLineArray: `dependencies = ["a", "b"]` opens and closes on
// one line. Read as the start of a block it lost both requirements and then
// took every following key for a dependency.
func TestPyProjectSingleLineArray(t *testing.T) {
	src := `[project]
dependencies = ["requests>=2.0", "idna==3.4"]
requires-python = ">=3.9"
description = "not a dependency"
`
	m, ok := Parse("pyproject.toml", []byte(src))
	if !ok {
		t.Fatal("pyproject.toml not recognised")
	}
	got := index(m.Dependencies)
	if len(got) != 2 || got["requests"].Version != ">=2.0" || got["idna"].Version != "==3.4" {
		t.Errorf("parsed %+v, want requests >=2.0 and idna ==3.4 and nothing else", m.Dependencies)
	}
	for _, d := range m.Dependencies {
		if d.Line != 2 {
			t.Errorf("%s: line = %d, want 2", d.Name, d.Line)
		}
	}

	// setuptools can say where the list lives instead of giving it. The file
	// it names is not a dependency.
	dynamic := `[project]
name = "svc"

[tool.setuptools.dynamic]
dependencies = {file = ["requirements.txt"]}
version = {attr = "svc.__version__"}
`
	if m, _ := Parse("pyproject.toml", []byte(dynamic)); len(m.Dependencies) != 0 {
		t.Errorf("a dynamic dependencies table was read as dependencies: %v", names(m.Dependencies))
	}
}
