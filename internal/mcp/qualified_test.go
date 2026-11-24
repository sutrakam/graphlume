package mcp

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// twoGuards is serilog's shape: `AgainstNull` is a member of Guard and of
// another class, and a caller who writes Guard.AgainstNull means the first.
func twoGuards(b *indexBuilder) {
	b.file("src/Guard.cs", "c_sharp", 40,
		typ("Guard", model.KindClass, 1, 30),
		method("AgainstNull", 5, 9, 1).ownedBy("Guard"))
	b.file("src/Check.cs", "c_sharp", 40,
		typ("Check", model.KindClass, 1, 30),
		method("AgainstNull", 5, 9, 1).ownedBy("Check"))
	b.file("src/Use.cs", "c_sharp", 40, typ("Use", model.KindClass, 1, 30), method("Run", 5, 9, 0).ownedBy("Use"))
	b.call(symKey("src/Use.cs", "Run", 5), symKey("src/Guard.cs", "AgainstNull", 5),
		"AgainstNull", "Guard", "src/Use.cs", 7, model.ConfidenceHigh)
	b.file("lib/mailer.rb", "ruby", 40,
		typ("Mailer", model.KindClass, 1, 20), method("deliver", 5, 9, 0).ownedBy("Mailer"))
	b.file("lib/outbox.rb", "ruby", 40,
		typ("Outbox", model.KindClass, 1, 20), method("deliver", 5, 9, 0).ownedBy("Outbox"))
	b.wellBound("c_sharp", ".cs")
	b.wellBound("ruby", ".rb")
}

// TestAQualifiedNameSelectsTheMemberOfThatOwner: `Guard.AgainstNull` was
// answered "No symbol named", while the bare AgainstNull worked.
func TestAQualifiedNameSelectsTheMemberOfThatOwner(t *testing.T) {
	fs := serverFor(t, twoGuards)

	bare, _ := answer(t, fs.sess, "impact", map[string]any{"symbol": "AgainstNull"})
	if !strings.HasPrefix(firstLine(bare), leadAmbiguous) {
		t.Fatalf("the fixture's bare name should be ambiguous: %q", firstLine(bare))
	}

	for _, name := range []string{"Guard.AgainstNull", "Guard::AgainstNull", "Serilog.Guard.AgainstNull"} {
		text, res := answer(t, fs.sess, "impact", map[string]any{"symbol": name})
		if res.IsError || strings.Contains(text, "No symbol named") {
			t.Errorf("%s was not found:\n%s", name, text)
			continue
		}
		if first := firstLine(text); !strings.Contains(first, "AgainstNull (src/Guard.cs:5)") || !strings.Contains(first, "1 direct") {
			t.Errorf("%s did not select Guard's member: %q", name, first)
		}
	}
	if text, _ := answer(t, fs.sess, "callers", map[string]any{"name": "Check.AgainstNull"}); !strings.Contains(firstLine(text), "src/Check.cs:5") {
		t.Errorf("callers did not select Check's member: %q", firstLine(text))
	}
	if text, _ := answer(t, fs.sess, "callers", map[string]any{"name": "Mailer#deliver"}); !strings.Contains(firstLine(text), "lib/mailer.rb:5") {
		t.Errorf("Owner#member was not selected: %q", firstLine(text))
	}
	if text, _ := answer(t, fs.sess, "definition", map[string]any{"name": "Guard.AgainstNull"}); !strings.Contains(text, "src/Guard.cs") ||
		strings.Contains(text, "No symbol named") {
		t.Errorf("definition did not find the member by its owner:\n%s", text)
	}
	if text, _ := answer(t, fs.sess, "symbol", map[string]any{"name": "Guard::AgainstNull"}); !strings.Contains(firstLine(text), "1 definition(s)") {
		t.Errorf("symbol did not find the member by its owner: %q", firstLine(text))
	}
}

// TestAQualifiedNameWithTheWrongOwnerIsNotFound: an owner that does not hold
// the member must not be answered for another owner's.
func TestAQualifiedNameWithTheWrongOwnerIsNotFound(t *testing.T) {
	fs := serverFor(t, twoGuards)
	text, _ := answer(t, fs.sess, "impact", map[string]any{"symbol": "Mailer.AgainstNull"})
	if !strings.Contains(firstLine(text), "No symbol named") {
		t.Errorf("a member was found under an owner that does not declare it: %q", firstLine(text))
	}
}
