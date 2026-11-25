package infer

import (
	"path"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
)

// # A qualified supertype is not its unqualified namesake
//
// `class InputStream extends java.io.OutputStream` names the JDK's
// OutputStream. os-lib also declares a nested `SubProcess.OutputStream`, and the
// supertype was bound to it at `high` because the name matches and it is in the
// same file. Both of the file's stream classes extended the wrong one.
//
// What the parser must emit for this rule to act (it does not yet): the
// qualifier a supertype is written with, as the reference's receiver. Today
// `extends java.io.OutputStream` is recorded as a reference named OutputStream
// with an empty receiver, which cannot be told from `extends OutputStream`, and
// nothing in the resolver can recover it. The qualifier is recorded the way a
// call's is: the path as written, `java.io` (`::` is read the same as `.`), with
// the reference's kind unchanged (reference.extends, reference.implements).
//
// With it, a supertype written with a qualifier is bound only to a type the
// qualifier fits, and the qualifier is read as:
//
//   - a root the language owns (java, javax, scala, kotlin): the standard
//     library, external;
//   - a package, shown by a directory of that name in the repository, which
//     the type's file lies under, or the type an inner type is nested in
//     (`Outer.Inner`);
//   - anything else: not a package of this repository, so external. A name
//     the repository declares unqualified, in the same file or anywhere, is
//     not what a qualified clause reaches.

// rootedQualifier begins the qualifier of a PHP supertype written with a
// leading backslash, in a file that declares a namespace. It is
// parse.RootedQualifier; the two packages do not import each other.
const rootedQualifier = `\`

// qualifiedSupertype narrows the candidates of a supertype reference by the
// qualifier it was written with. kept is what the qualifier fits; when it is
// empty the verdict says whether the supertype leaves the repository.
func (r *Resolver) qualifiedSupertype(spec LanguageSpec, ref registry.Reference, cands []registry.SymbolIndexEntry) (kept []registry.SymbolIndexEntry, v verdict) {
	rooted := strings.HasPrefix(ref.Receiver, rootedQualifier)
	segs := receiverSegments(strings.TrimPrefix(ref.Receiver, rootedQualifier))
	if rooted && len(segs) == 0 {
		// `extends \DateTimeImmutable` in a file that declares a namespace: a
		// class declared in NO namespace. The index does not say which
		// namespace a definition is in, and a class of the name that lies in a
		// namespace (monolog's own Monolog\DateTimeImmutable, bound at
		// `medium` as "same directory") is not it. Nothing the index holds is
		// known to be in the global namespace, so the supertype is the
		// language's or a library's.
		return nil, verdictExternal
	}
	if len(segs) == 0 {
		return cands, verdictBound
	}
	if spec.hasExternalRoot(segs) {
		return nil, verdictExternal
	}
	for _, c := range cands {
		if supertypeQualifierFits(segs, c) {
			kept = append(kept, c)
		}
	}
	if len(kept) > 0 {
		return kept, verdictBound
	}
	if r.layout.Known() && !r.layout.HasDirNamed(segs[0]) && !r.layout.HasStem(segs[0]) && !hasNestingOwner(segs, cands) {
		return nil, verdictExternal
	}
	return nil, verdictUnresolved
}

// supertypeQualifierFits reports whether a type is where a qualifier says: the
// qualifier is the tail of the type's own qualified name (nesting), or a run of
// the directories above the type's file, with its name, spells it (a package
// or a module).
func supertypeQualifierFits(segs []string, c registry.SymbolIndexEntry) bool {
	qual, _ := strings.CutPrefix(c.FQN, c.FilePath+":")
	typeSegs := strings.Split(qual, ".")
	if len(typeSegs) > 1 && len(segs) <= len(typeSegs)-1 {
		nested := true
		for i := 1; i <= len(segs); i++ {
			if segs[len(segs)-i] != typeSegs[len(typeSegs)-1-i] {
				nested = false
				break
			}
		}
		if nested {
			return true
		}
	}
	var dir []string
	for _, p := range strings.Split(path.Dir(c.FilePath), "/") {
		if p != "" && p != "." {
			dir = append(dir, p)
		}
	}
	// The qualifier names a package or a module: some run of the directories
	// above the file, or of those and the file's own name (a Python or Rust
	// module is a file). Not only the innermost directories: a source root
	// (`os/src/`, `src/main/java/org/x/`) sits between the package and the file.
	stem := path.Base(c.FilePath)
	if i := strings.IndexByte(stem, '.'); i > 0 {
		stem = stem[:i]
	}
	dir = append(dir, stem)
	for start := 0; start+len(segs) <= len(dir); start++ {
		fits := true
		for i, seg := range segs {
			if !strings.EqualFold(dir[start+i], seg) {
				fits = false
				break
			}
		}
		if fits {
			return true
		}
	}
	return false
}

// hasNestingOwner reports whether the qualifier's last segment names a type
// that some candidate is nested in: the qualifier is then a type of this
// repository even if no candidate fits it.
func hasNestingOwner(segs []string, cands []registry.SymbolIndexEntry) bool {
	last := segs[len(segs)-1]
	for _, c := range cands {
		if strings.Contains(strings.TrimPrefix(c.FQN, c.FilePath+":"), last+".") {
			return true
		}
	}
	return false
}
