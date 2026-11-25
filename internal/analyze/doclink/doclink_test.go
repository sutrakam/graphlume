package doclink

import (
	"testing"
)

func TestIsDocFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"docs/adr/0001-record-architecture.md", true},
		{"doc/architecture.markdown", true},
		{"src/main.go", false},
		{"internal/service.rs", false},
		{"README.txt", false},
	}
	for _, tc := range cases {
		got := IsDocFile(tc.path)
		if got != tc.want {
			t.Errorf("IsDocFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestExtractMentions(t *testing.T) {
	content := []byte(`# Architecture Decision Record
We use ` + "`PaymentService`" + ` to handle checkout operations.
Do not invoke ` + "`executeRawQuery`" + ` directly.
Ignore keyword ` + "`return`" + ` or ` + "`nil`" + `.
`)
	mentions := ExtractMentions("docs/adr/0002-payments.md", content)
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d: %+v", len(mentions), mentions)
	}
	if mentions[0].SymbolName != "PaymentService" || mentions[0].Line != 2 {
		t.Errorf("unexpected first mention: %+v", mentions[0])
	}
	if mentions[1].SymbolName != "executeRawQuery" || mentions[1].Line != 3 {
		t.Errorf("unexpected second mention: %+v", mentions[1])
	}
}
