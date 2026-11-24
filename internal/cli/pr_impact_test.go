package cli

import (
	"strings"
	"testing"
)

func TestPRImpactCommand(t *testing.T) {
	r := newImpactRepo(t)
	r.write(t, "svc/user.go", `package svc
type User struct{}
func (u *User) GetName() string { return "alice" }
`)
	r.commit(t, "initial commit")

	r.write(t, "svc/user.go", `package svc
type User struct{}
func (u *User) GetName() string { return "bob" }
func (u *User) GetEmail() string { return "bob@example.com" }
`)
	r.commit(t, "update user")

	if _, err := run(t, r.dir, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, r.dir, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	if _, err := run(t, r.dir, "resolve"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	out, err := run(t, r.dir, "pr-impact", "--base", "HEAD~1", "--head", "HEAD")
	if err != nil {
		t.Fatalf("pr-impact failed: %v\nOutput: %s", err, out)
	}

	if !strings.Contains(out, "risk") && !strings.Contains(out, "Blast radius") {
		t.Errorf("expected markdown report to contain risk/impact summary, got:\n%s", out)
	}
}
