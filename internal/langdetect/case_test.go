package langdetect

import "testing"

// TestUpperCaseExtensionsAreRecognised: the grammar library's extension table
// is in lower case, and a file named in upper case was skipped as a file of no
// known type. Fortran sources are conventionally `.F90` when they are to be
// preprocessed, so that was most preprocessed Fortran.
func TestUpperCaseExtensionsAreRecognised(t *testing.T) {
	for name, want := range map[string]string{
		"solver.F90":   "fortran",
		"solver.f90":   "fortran",
		"MAIN.CPP":     "cpp",
		"Widget.JAVA":  "java",
		"legacy.C":     "cpp", // upper-case .C is C++ by convention
		"legacy.c":     "c",
		"legacy.H":     "cpp",
		"SCRIPT.PY":    "python",
		"README.NOPE":  "",
		"Makefile":     "make",
		"noextension":  "",
		"query.PGSQL":  "sql",
		"archive.TAR":  "",
		"Program.Cs":   "c_sharp",
		"module.GLEAM": "gleam",
	} {
		if got := FromName(name); got != want {
			t.Errorf("FromName(%q) = %q, want %q", name, got, want)
		}
	}
}
