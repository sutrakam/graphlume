package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/multirepo"
)

func TestSystemIndexIndexesMultipleMembersFromEstate(t *testing.T) {
	tmp := t.TempDir()

	app1 := filepath.Join(tmp, "app1")
	app2 := filepath.Join(tmp, "app2")
	if err := os.MkdirAll(app1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(app2, 0o755); err != nil {
		t.Fatal(err)
	}

	f1 := "package main\n\nfunc Alpha() string { return \"hello\" }\n"
	if err := os.WriteFile(filepath.Join(app1, "main.go"), []byte(f1), 0o644); err != nil {
		t.Fatal(err)
	}
	f2 := "package main\n\nfunc Beta() int { return 42 }\n"
	if err := os.WriteFile(filepath.Join(app2, "main.go"), []byte(f2), 0o644); err != nil {
		t.Fatal(err)
	}

	estateYAML := "members:\n" +
		"  - name: app1\n    index: app1\n" +
		"  - name: app2\n    index: app2\n"
	estatePath := filepath.Join(tmp, "estate.yml")
	if err := os.WriteFile(estatePath, []byte(estateYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runSystemCLI(t, tmp, "index", "--members", estatePath)
	if err != nil {
		t.Fatalf("system index failed: %v\nOutput: %s", err, out)
	}

	if !strings.Contains(out, "All 2 estate member repositories indexed successfully") {
		t.Errorf("output missing success summary; got:\n%s", out)
	}

	db1 := filepath.Join(app1, IndexDir, IndexFile)
	if _, err := os.Stat(db1); err != nil {
		t.Errorf("app1 index.db not created: %v", err)
	}
	db2 := filepath.Join(app2, IndexDir, IndexFile)
	if _, err := os.Stat(db2); err != nil {
		t.Errorf("app2 index.db not created: %v", err)
	}

	// Verify both indexes can be opened by multirepo federator
	fed, err := multirepo.New()
	if err != nil {
		t.Fatal(err)
	}
	estateMembers, err := multirepo.LoadEstate(estatePath)
	if err != nil {
		t.Fatal(err)
	}
	cat := &memberCatalogue{byPath: map[string]*openIndex{}}
	defer cat.closeAll()

	var members []multirepo.Member
	for _, em := range estateMembers {
		_, dbPath, err := memberIndexFile(em)
		if err != nil {
			t.Fatal(err)
		}
		em.Index = dbPath
		m := cat.member(context.Background(), em)
		if !m.Available() {
			t.Fatalf("member %s unavailable: %s", em.Name, m.Unavailable)
		}
		members = append(members, m)
	}
	rep, err := fed.Build(context.Background(), members)
	if err != nil {
		t.Fatalf("federate build: %v", err)
	}
	if !rep.Complete() {
		t.Errorf("federated report incomplete: %+v", rep.Unjoined.UnavailableMembers)
	}
}

// A member named by an index file that is not <root>/.graphlume/index.db is
// refused: the indexer would write the repository's own index file and the
// system graph would keep reading the named one.
func TestMemberIndexFileRefusesForeignDatabase(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "orders")
	if err := os.MkdirAll(filepath.Join(repo, IndexDir), 0o755); err != nil {
		t.Fatal(err)
	}

	root, db, err := memberIndexFile(multirepo.EstateMember{Name: "orders", Index: repo})
	if err != nil || root != repo || db != filepath.Join(repo, IndexDir, IndexFile) {
		t.Fatalf("directory member: root=%q db=%q err=%v", root, db, err)
	}
	inside := filepath.Join(repo, IndexDir, IndexFile)
	root, db, err = memberIndexFile(multirepo.EstateMember{Name: "orders", Index: inside})
	if err != nil || root != repo || db != inside {
		t.Fatalf("index.db member: root=%q db=%q err=%v", root, db, err)
	}
	foreign := filepath.Join(tmp, "shared", "orders.db")
	if _, _, err := memberIndexFile(multirepo.EstateMember{Name: "orders", Index: foreign}); err == nil ||
		!strings.Contains(err.Error(), "not where 'graphlume index' writes") {
		t.Fatalf("foreign database accepted: %v", err)
	}
	if _, _, err := memberIndexFile(multirepo.EstateMember{Name: "x", Index: filepath.Join(tmp, "missing")}); err == nil {
		t.Fatal("missing directory accepted")
	}
}
