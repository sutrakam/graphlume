package gotypes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func checkTree(t *testing.T, root string, env map[string]string) *Result {
	t.Helper()
	needGoroot(t)
	get := func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
	res, err := Check(context.Background(), Options{Root: root, Env: get})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const useDep = `package app

import "example.org/Dep/sub"

type Holder struct{ sub.Base }

func Run(h Holder) string { return h.Hello() + sub.Name() }
`

// TestDependenciesResolveFromTheModuleCacheAtTheRequiredVersion: the cache's
// layout is case-escaped, versioned, and honours replace, and a dependency that
// resolves is not a stub.
func TestDependenciesResolveFromTheModuleCacheAtTheRequiredVersion(t *testing.T) {
	cache := t.TempDir()
	writeTree(t, cache, map[string]string{
		// v1.0.0 is what go.mod requires; v2.0.0 must never be read.
		"example.org/!dep@v1.0.0/sub/sub.go": "package sub\n\ntype Base struct{}\n\nfunc (Base) Hello() string { return \"\" }\n\nfunc Name() string { return \"\" }\n",
		"example.org/!dep@v2.0.0/sub/sub.go": "package sub\n\ntype Base struct{}\n\nfunc Name() string { return \"\" }\n",
	})
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.22\n\nrequire example.org/Dep v1.0.0\n",
		"app/app.go": useDep,
	})
	res := checkTree(t, root, map[string]string{"GOMODCACHE": cache})
	if res.Stats.MissingCount != 0 || res.Stats.WithStubs != 0 {
		t.Fatalf("a dependency in the cache was stubbed: %+v", res.Stats.MissingImports)
	}
	if res.Stats.Complete != 1 {
		t.Errorf("Complete = %d, want 1; errors: %v", res.Stats.Complete, res.Stats.ErrorSamples)
	}
}

// TestReplaceByLocalDirectoryAndByVersion covers both forms of replace.
func TestReplaceByLocalDirectoryAndByVersion(t *testing.T) {
	cache := t.TempDir()
	writeTree(t, cache, map[string]string{
		"example.org/other@v1.5.0/sub/sub.go": "package sub\n\ntype Base struct{}\n\nfunc (Base) Hello() string { return \"\" }\n\nfunc Name() string { return \"\" }\n",
	})
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n\nrequire example.org/Dep v1.0.0\n\nreplace example.org/Dep => ../localdep\n",
		// The replacement lives outside the repository, next to it.
		"app/app.go": useDep,
	})
	local := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-localdep")
	t.Cleanup(func() { _ = os.RemoveAll(local) })
	writeTree(t, local, map[string]string{
		"sub/sub.go": "package sub\n\ntype Base struct{}\n\nfunc (Base) Hello() string { return \"\" }\n\nfunc Name() string { return \"\" }\n",
	})
	// Point the replace at the sibling directory just created.
	writeTree(t, root, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n\nrequire example.org/Dep v1.0.0\n\nreplace example.org/Dep => ../" + filepath.Base(local) + "\n",
	})
	res := checkTree(t, root, map[string]string{"GOMODCACHE": cache})
	if res.Stats.MissingCount != 0 {
		t.Fatalf("a locally replaced dependency was not found: %v", res.Stats.MissingImports)
	}

	// By version: replaced by another module path in the cache.
	writeTree(t, root, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n\nrequire example.org/Dep v1.0.0\n\nreplace example.org/Dep => example.org/other v1.5.0\n",
	})
	res = checkTree(t, root, map[string]string{"GOMODCACHE": cache})
	if res.Stats.MissingCount != 0 {
		t.Fatalf("a dependency replaced by version was not found: %v", res.Stats.MissingImports)
	}
}

// TestVendorDirectoryIsUsedBeforeTheCache.
func TestVendorDirectoryIsUsedBeforeTheCache(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                          "module example.com/app\n\ngo 1.22\n\nrequire example.org/Dep v1.0.0\n",
		"app/app.go":                      useDep,
		"vendor/example.org/Dep/sub/s.go": "package sub\n\ntype Base struct{}\n\nfunc (Base) Hello() string { return \"\" }\n\nfunc Name() string { return \"\" }\n",
	})
	res := checkTree(t, root, map[string]string{"GOMODCACHE": t.TempDir()})
	if res.Stats.MissingCount != 0 {
		t.Fatalf("a vendored dependency was not found: %v", res.Stats.MissingImports)
	}
}

// TestAnEmptyCacheStubsAndNeverGuesses: with the dependency absent the import
// is a stub, the embedded type is unknown, and the call that could have been
// decided by it is withheld rather than proved.
func TestAnEmptyCacheStubsAndNeverGuesses(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.22\n\nrequire example.org/Dep v1.0.0\n",
		"app/app.go": useDep,
	})
	res := checkTree(t, root, map[string]string{"GOMODCACHE": t.TempDir()})
	if res.Stats.WithStubs != 1 || res.Stats.MissingCount != 1 {
		t.Fatalf("want 1 package with a stubbed import; got %+v", res.Stats)
	}
	for _, f := range res.Files {
		for _, u := range f.Uses {
			if u.Obj.Name == "Hello" {
				t.Errorf("h.Hello() was resolved through an embedded type that is a stub")
			}
		}
	}
}

// TestAPackageThatDoesNotParseIsSkipped.
func TestAPackageThatDoesNotParseIsSkipped(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":      "module example.com/app\n\ngo 1.22\n",
		"ok/ok.go":    "package ok\n\nfunc F() {}\n",
		"bad/bad.go":  "package bad\n\nfunc G( {\n",
		"use/use.go":  "package use\n\nimport \"example.com/app/ok\"\n\nfunc U() { ok.F() }\n",
		"use/use2.go": "package use\n\nfunc V() { U() }\n",
	})
	res := checkTree(t, root, nil)
	if res.Stats.SkippedParse != 1 {
		t.Errorf("SkippedParse = %d, want 1", res.Stats.SkippedParse)
	}
	for _, f := range res.Files {
		if f.Path == "bad/bad.go" {
			t.Errorf("a file that does not parse carries facts")
		}
	}
	if got := useLines(res); !has(got, "use/use.go:5 F CALLS <- U") {
		t.Errorf("the rest of the repository must still be checked: %v", got)
	}
}

// TestImportCCheckedWithFakeImportC: cgo does not fail the run.
func TestImportCCheckedWithFakeImportC(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"c/c.go": "package c\n\n// #include <stdio.h>\nimport \"C\"\n\nfunc F() { G() }\n\nfunc G() { _ = C.int(1) }\n",
	})
	res := checkTree(t, root, nil)
	if got := useLines(res); !has(got, "c/c.go:6 G CALLS <- F") {
		t.Errorf("a cgo file must still be checked: %v (errors %v)", got, res.Stats.ErrorSamples)
	}
}

// TestGenericDeclarationsAreTheirOwnOrigin: instantiating a generic type and
// calling its method maps to the declaration, not to the instance.
func TestGenericDeclarationsAreTheirOwnOrigin(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"g/g.go": "package g\n\ntype Box[T any] struct{ v T }\n\nfunc (b *Box[T]) Put(v T) { b.v = v }\n\nfunc Use() {\n\tvar b Box[string]\n\tb.Put(\"x\")\n\tc := &Box[int]{}\n\tc.Put(1)\n}\n",
	})
	res := checkTree(t, root, nil)
	got := useLines(res)
	if !has(got, "g/g.go:9 Box.Put CALLS <- Use") || !has(got, "g/g.go:11 Box.Put CALLS <- Use") {
		t.Errorf("methods of instantiated generic types must map to the declaration: %v", got)
	}
}
