package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func superRef(id int64, name, qualifier string, line int) registry.Reference {
	ref := registry.Reference{ID: id, Name: name, Kind: "reference.extends", Receiver: qualifier, Line: line}
	if qualifier != "" {
		ref.ReceiverKind = model.ReceiverPath
	}
	return ref
}

// TestAQualifiedSupertypeIsNotItsUnqualifiedNamesake is os-lib's
// `class InputStream extends java.io.OutputStream`, with an OutputStream of its
// own declared nested in the same file.
func TestAQualifiedSupertypeIsNotItsUnqualifiedNamesake(t *testing.T) {
	file := "os/src/SubProcess.scala"
	base := def(3, "class", "os/src/pkg/Base.scala", "Base", 12)
	base.Exported = true
	idx := index(
		def(1, "class", file, "SubProcess.OutputStream", 10),
		def(2, "class", "os/src/Other.scala", "Other.Stream", 11),
		base,
	)
	layout := NewLayout([]string{file, "os/src/pkg/Base.scala"}, nil)
	r := New(idx, Options{Layout: layout})
	fr := registry.FileRefs{
		FileID: 10, Path: file, Language: "scala",
		Symbols: within(90, file, "SubProcess.InputStream"),
		Refs: []registry.Reference{
			superRef(1, "OutputStream", "java.io", 5),         // the JDK's
			superRef(2, "OutputStream", "", 6),                // the file's own, unqualified
			superRef(3, "OutputStream", "SubProcess", 7),      // the nested one, by its owner
			superRef(4, "ByteData", "geny", 8),                // a library that is not a package here
			superRef(5, "Base", "pkg", 9),                     // a package of this repository
			superRef(6, "OutputStream", "somewhere.else", 10), // qualified by a package that is not here
		},
	}
	edges, outs, _ := resolveOne(t, r, fr)
	got := edgesByLine(edges)

	if _, bound := got[5]; bound || !outs[1].External {
		t.Errorf("extends java.io.OutputStream: bound=%v external=%v, want external and unbound", got[5], outs[1].External)
	}
	if got[6].dst != 1 {
		t.Errorf("unqualified OutputStream bound as %+v, want the nested class (1)", got[6])
	}
	if got[7].dst != 1 {
		t.Errorf("SubProcess.OutputStream bound as %+v, want the nested class (1)", got[7])
	}
	if _, bound := got[8]; bound || !outs[4].External {
		t.Errorf("geny.ByteData: bound=%v external=%v, want external and unbound", got[8], outs[4].External)
	}
	if got[9].dst != 3 {
		t.Errorf("pkg.Base bound as %+v, want Base (3)", got[9])
	}
	if _, bound := got[10]; bound {
		t.Errorf("somewhere.else.OutputStream was bound: %+v", got[10])
	}
}

// TestARootedPHPSupertypeIsNotAClassOfTheFilesNamespace is monolog's
// `extends \DateTimeImmutable` beside Monolog\DateTimeImmutable in the same
// directory, which the "same directory" rule bound at `medium`.
func TestARootedPHPSupertypeIsNotAClassOfTheFilesNamespace(t *testing.T) {
	own := def(1, "class", "src/Monolog/DateTimeImmutable.php", "DateTimeImmutable", 11)
	idx := index(own)
	r := New(idx, Options{Layout: NewLayout([]string{"src/Monolog/Json.php", own.FilePath}, nil)})
	fr := registry.FileRefs{
		FileID: 10, Path: "src/Monolog/Json.php", Language: "php",
		Symbols: within(90, "src/Monolog/Json.php", "Json"),
		Refs: []registry.Reference{
			superRef(1, "DateTimeImmutable", `\`, 5), // \DateTimeImmutable: the global class
			superRef(2, "DateTimeImmutable", "", 6),  // DateTimeImmutable: this namespace's
		},
	}
	edges, outs, _ := resolveOne(t, r, fr)
	got := edgesByLine(edges)
	if _, bound := got[5]; bound || !outs[1].External {
		t.Errorf(`extends \DateTimeImmutable: bound=%v external=%v, want external and unbound`, got[5], outs[1].External)
	}
	if got[6].dst != 1 {
		t.Errorf("extends DateTimeImmutable bound as %+v, want the namespace's own class (1)", got[6])
	}
}
