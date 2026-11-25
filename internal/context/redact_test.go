package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextExcerptRedactsCredentials(t *testing.T) {
	root := t.TempDir()

	code := `package config

func GetKey() string {
	const StripeSecretKey = "sk_live_0123456789abcdefEXAMPLE"
	const DBPassword = "SuperSecretPassword123!"
	return StripeSecretKey
}
`
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "api.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, g, runID := indexFixture(t, root)

	req := Request{
		Task: "GetKey config",
	}

	pack, err := NewAssembler().Assemble(context.Background(), reg, g, runID, req)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	foundExcerpt := false
	for _, it := range pack.Items {
		if it.Excerpt != "" {
			foundExcerpt = true
			if strings.Contains(it.Excerpt, "sk_live_") {
				t.Errorf("raw Stripe API key was not redacted from excerpt: %s", it.Excerpt)
			}
			if strings.Contains(it.Excerpt, "SuperSecretPassword123!") {
				t.Errorf("raw DB password was not redacted from excerpt: %s", it.Excerpt)
			}
			if !strings.Contains(it.Excerpt, "[REDACTED:") {
				t.Errorf("expected [REDACTED: placeholder in excerpt, got: %s", it.Excerpt)
			}
		}
	}

	if !foundExcerpt {
		t.Fatal("expected at least one item with an excerpt to test redaction")
	}
}
