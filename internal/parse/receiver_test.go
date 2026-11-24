package parse

import (
	"context"
	"testing"
)

// TestReceiverIsCaptured pins a tier that had never once fired.
//
// Reference.Receiver was empty for EVERY reference in every language — 0 of
// 342,589 across five real repositories — so the resolver's receiver tier, the
// one its own comment calls "far stronger evidence than a bare name match",
// was unreachable code.
//
// The cost was worst in Java, where obj.method() is the dominant call form and
// method names repeat across classes: on a 2,681-file service `build` had 250
// definitions and 643 references to it were abandoned as ambiguous.
func TestReceiverIsCaptured(t *testing.T) {
	for _, tc := range []struct{ file, src, wantRecv, wantName string }{
		{"T.java", "class T {\n  void run() {\n    service.getAmount();\n  }\n}\n", "service", "getAmount"},
		{"t.go", "package p\nfunc run() {\n\tsvc.Update()\n}\n", "svc", "Update"},
		{"t.py", "def run():\n    db.query('x')\n", "db", "query"},
		{"t.ts", "function run() {\n  service.getAmount();\n}\n", "service", "getAmount"},
		{"t.rs", "fn run() {\n    service.get_amount();\n}\n", "service", "get_amount"},
		{"t.cs", "class T {\n  void Run() {\n    service.GetAmount();\n  }\n}\n", "service", "GetAmount"},
		{"t.cpp", "void run() {\n  service.getAmount();\n}\n", "service", "getAmount"},
		{"t.rb", "def run\n  service.get_amount()\nend\n", "service", "get_amount"},
	} {
		p := NewRegistry().ForFile(tc.file)
		if p == nil {
			t.Fatalf("no parser for %s", tc.file)
		}
		res, err := p.Parse(context.Background(), tc.file, []byte(tc.src))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, ref := range res.References {
			if ref.Name == tc.wantName && ref.Receiver == tc.wantRecv {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no reference %s.%s with a receiver (all refs: %+v)", tc.file, tc.wantRecv, tc.wantName, res.References)
		}
	}
}

// TestReceiverResolvesToTheDeclaredType pins the half that makes the receiver
// usable rather than merely present.
//
// receiverMatches compares a receiver against a candidate's qualified name,
// which works when a variable is named after its type and fails completely
// when it is not. `b.build()` matched every candidate containing a "b".
// Carrying the declared type forward is what turns it into Builder.build().
func TestReceiverResolvesToTheDeclaredType(t *testing.T) {
	src := []byte(`class T {
  void run() {
    Builder b = new Builder();
    b.build();
  }
}
`)
	p := NewRegistry().ForFile("T.java")
	res, err := p.Parse(context.Background(), "T.java", src)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range res.References {
		if ref.Name != "build" {
			continue
		}
		if ref.Receiver != "Builder" {
			t.Errorf("receiver for build() is %q, want the declared type Builder", ref.Receiver)
		}
		return
	}
	t.Error("no reference to build() at all")
}

// TestTwoReceiversOnOneLineAreToldApart replaces a test that pinned a refusal.
//
// Matching receivers to references by line and name fails, because the
// receiver query captures the method at its own column while the tags query
// reports the start of the expression, and the two never agree on a column.
// Two calls to the SAME method on ONE line could not then be told apart, so
// both would lose their receiver — TestAmbiguousReceiversOnOneLineAreDropped
// required exactly that.
//
// shape.go reads the receiver from the tag's own two ranges, which identify
// the call. Nothing is ambiguous any more, and each call keeps what it was
// made on.
func TestTwoReceiversOnOneLineAreToldApart(t *testing.T) {
	src := []byte("class T {\n  void run() { a.send(); b.send(); }\n}\n")
	p := NewRegistry().ForFile("T.java")
	res, err := p.Parse(context.Background(), "T.java", src)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ref := range res.References {
		if ref.Name == "send" {
			got = append(got, ref.Receiver)
		}
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("send() receivers = %v, want [a b]: each call on the line has its own", got)
	}
	for _, ref := range res.References {
		if ref.Name == "a" || ref.Name == "b" {
			t.Errorf("the receiver %q is recorded as a call; it is what the call was made on", ref.Name)
		}
	}
	if res.Qualifiers != 2 {
		t.Errorf("Qualifiers = %d, want 2: the dropped receiver rows are counted, not lost", res.Qualifiers)
	}
}
