package infer

import "github.com/sutrakam/graphlume/internal/registry"

// # A production file does not call a test helper by name
//
// Test trees are full of stubs, doubles and helpers that reuse production
// names: `TestUtil.proc`, a Rack middleware double with an `env`, a Java
// `android.util.Pair`. A rule that picks its target by NAME ALONE (the only
// member of that name, the named type, the neighbour in the directory, the one
// free function of the name) has no evidence about which of them a call reaches,
// and the one thing known is that production code is not compiled with the
// test tree. Measured: os-lib's library code bound `os.proc(...)` to
// `TestUtil.proc`, rack's `request.env` in lib/ to a test double's `env`,
// moshi's `Pair(...)` to a Java class under src/test.
//
// The convention for what is a test file is LanguageSpec.IsTestFile, which this
// package already had. language.go explains why it was DESCRIPTIVE and not
// consulted: A19 used it to drop test candidates up front, which shrank the
// candidate sets until honestly ambiguous references resolved, 362 wrongly. This
// does not repeat that. It is applied to the binding a name-only rule has
// already decided on, so it can only turn a binding into no binding; it never
// removes a candidate before a rule weighs the others, and so cannot make an
// ambiguous reference resolve.

// callsTestHelper reports whether binding the call to target would have a file
// that is not test code reach into one that is. Where the caller's language has
// no test-file convention, nothing is known and nothing is refused.
func (r *Resolver) callsTestHelper(c call, target registry.SymbolIndexEntry) bool {
	tf := c.spec.TestFiles
	if len(tf.Suffixes)+len(tf.Prefixes)+len(tf.BaseNames)+len(tf.Dirs) == 0 {
		return false
	}
	if c.spec.IsTestFile(c.fr.Path) {
		return false
	}
	return r.specs.For(languageOfCandidate(target)).IsTestFile(target.FilePath)
}
