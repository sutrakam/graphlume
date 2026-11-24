package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func scalaOsLib() map[string][]registry.SymbolIndexEntry {
	return index(
		def(1, "variable", "os/src/FileOps.scala", "exists", 10),       // object exists
		def(2, "function", "os/src/FileOps.scala", "exists.apply", 10), // its apply
		def(3, "function", "os/src/Model.scala", "StatInfo.isFile", 11),
		def(4, "variable", "os/src/StatOps.scala", "isFile", 12), // object isFile, no apply indexed
		def(5, "function", "os/src/package.scala", "root", 13),
		def(6, "function", "os/src/Path.scala", "Path.root", 14),
		def(7, "function", "os/test/src/TestUtil.scala", "TestUtil.proc", 15),
		def(8, "class", "os/src/ProcessOps.scala", "proc", 16),
		def(9, "function", "os/src/Model.scala", "StatInfo.nothing", 11),
	)
}

func osCall(t *testing.T, name string, symbols []registry.EnclosingSymbol, file string) (edge []modelEdge, kind model.EdgeKind, out registry.RefOutcome) {
	t.Helper()
	layout := NewLayout([]string{"os/src/FileOps.scala", "os/src/package.scala", "os/test/src/TestUtil.scala", file}, nil)
	r := New(scalaOsLib(), Options{Layout: layout})
	es, outs, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 99, Path: file, Language: "scala", Symbols: symbols,
		Refs: []registry.Reference{callRef(1, name, model.ReceiverName, "os", 5)},
	})
	for _, e := range es {
		edge = append(edge, modelEdge{e.DstID, e.Confidence})
		kind = e.Kind
	}
	return edge, kind, outs[1]
}

// TestAPackageQualifiedCallNamesTheObjectOrFunctionOfThePackage: os.exists(p)
// is the object's apply, os.isFile(p) the object when its apply is not among the
// candidates, os.root(...) the package's function, os.proc(...) the case class.
func TestAPackageQualifiedCallNamesTheObjectOrFunctionOfThePackage(t *testing.T) {
	file := "os/src/Use.scala"
	syms := within(90, file, "Use.run")
	for _, tc := range []struct {
		name string
		dst  int64
		kind model.EdgeKind
	}{
		{"exists", 2, model.EdgeCalls},
		{"isFile", 4, model.EdgeCalls},
		{"root", 5, model.EdgeCalls},
		{"proc", 8, model.EdgeInstantiates},
	} {
		got, kind, _ := osCall(t, tc.name, syms, file)
		if len(got) != 1 || got[0].dst != tc.dst || got[0].confidence != model.ConfidenceHigh || kind != tc.kind {
			t.Errorf("os.%s: bound as %+v (%s), want %d at high as %s", tc.name, got, kind, tc.dst, tc.kind)
		}
	}
}

// TestAPackageQualifiedCallDoesNotFallThroughToAMethod: the package defines
// nothing of the name, and a method of some type that does is not the callee.
func TestAPackageQualifiedCallDoesNotFallThroughToAMethod(t *testing.T) {
	file := "os/src/Use.scala"
	got, _, out := osCall(t, "nothing", within(90, file, "Use.run"), file)
	if len(got) != 0 || out.Resolution == registry.ResolutionResolved {
		t.Errorf("os.nothing was bound to a method: %+v %+v", got, out)
	}
}

// TestAnObjectCallInAnEnclosingObjectPrefersTheMember: inside `object write`,
// `write(...)` is the object's own method, not the object.
func TestAnObjectCallInAnEnclosingObjectPrefersTheMember(t *testing.T) {
	file := "os/src/ReadWriteOps.scala"
	idx := index(
		def(1, "variable", file, "write", 10),
		def(2, "function", file, "write.write", 10),
	)
	r := New(idx, Options{})
	edges, _, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: file, Language: "scala",
		Symbols: within(90, file, "write.run"),
		Refs:    []registry.Reference{callRef(1, "write", model.ReceiverBare, "", 5)},
	})
	if len(edges) != 1 || edges[0].DstID != 2 {
		t.Errorf("bound as %+v, want the member write.write (2)", edges)
	}
}

// TestACallThroughAPackageAndAnObjectNamesTheObjectsMember: os.read.bytes(p) is
// the member of object read, not a method `bytes` of an unrelated type.
func TestACallThroughAPackageAndAnObjectNamesTheObjectsMember(t *testing.T) {
	file := "os/src/Use.scala"
	idx := index(
		def(1, "variable", "os/src/ReadWriteOps.scala", "read", 10),
		def(2, "variable", "os/src/ReadWriteOps.scala", "read.bytes", 10),
		def(3, "function", "os/src/SubProcess.scala", "SubProcess.OutputStream.bytes", 11),
	)
	layout := NewLayout([]string{"os/src/ReadWriteOps.scala", "os/src/SubProcess.scala", file}, nil)
	r := New(idx, Options{Layout: layout})
	edges, _, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 99, Path: file, Language: "scala", Symbols: within(90, file, "Use.run"),
		Refs: []registry.Reference{
			callRef(1, "bytes", model.ReceiverPath, "os.read", 5),
			callRef(2, "missing", model.ReceiverPath, "os.read", 6),
		},
	})
	got := edgesByLine(edges)
	if got[5].dst != 2 || got[5].confidence != model.ConfidenceHigh {
		t.Errorf("os.read.bytes bound as %+v, want the nested object (2) at high", got[5])
	}
	if _, bound := got[6]; bound {
		t.Errorf("os.read.missing was bound: %+v", got[6])
	}
}
