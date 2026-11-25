package gotypes

import (
	"context"
	"testing"
)

// TestLineDirectivesDoNotMoveFacts: generated code points //line at the file
// it came from. A use must be reported where it is written.
func TestLineDirectivesDoNotMoveFacts(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"l/l.go": "package l\n\nfunc G() {}\n\n//line elsewhere.y:900\nfunc F() { G() }\n",
	})
	res := checkTree(t, root, nil)
	if got := useLines(res); !has(got, "l/l.go:6 G CALLS <- F") {
		t.Errorf("a //line directive moved a fact: %v", got)
	}
}

// TestAnImportedPackageTheIndexDoesNotHoldIsStillReal: the index may exclude a
// directory; importing it must not turn it into a stub.
func TestAnImportedPackageTheIndexDoesNotHoldIsStillReal(t *testing.T) {
	needGoroot(t)
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.22\n",
		"app/app.go": "package app\n\nimport \"example.com/app/lib\"\n\nfunc Run() string { return lib.Name() }\n",
		"lib/lib.go": "package lib\n\nfunc Name() string { return \"lib\" }\n",
	})
	res, err := Check(context.Background(), Options{Root: root, Files: []string{"app/app.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.MissingCount != 0 {
		t.Fatalf("a package of the module was stubbed: %v", res.Stats.MissingImports)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "app/app.go" {
		t.Errorf("only the indexed file may carry facts: %+v", res.Files)
	}
}
