package cli

import (
	"strings"
	"testing"
)

func TestCLIPackCommand(t *testing.T) {
	root := queryFixture(t)

	// 1. Default XML pack
	outXML, err := run(t, root, "pack")
	if err != nil {
		t.Fatalf("pack XML failed: %v\nOutput: %s", err, outXML)
	}
	if !strings.Contains(outXML, "<graphlume_pack>") {
		t.Errorf("expected XML pack header, got:\n%s", outXML)
	}
	if !strings.Contains(outXML, "<file path=\"svc/payment.go\">") {
		t.Errorf("expected svc/payment.go in pack, got:\n%s", outXML)
	}

	// 2. Markdown pack
	outMD, err := run(t, root, "pack", "--format", "markdown")
	if err != nil {
		t.Fatalf("pack Markdown failed: %v\nOutput: %s", err, outMD)
	}
	if !strings.Contains(outMD, "# Codebase Context Pack") {
		t.Errorf("expected Markdown pack header, got:\n%s", outMD)
	}
	if !strings.Contains(outMD, "### svc/payment.go") {
		t.Errorf("expected svc/payment.go in markdown pack, got:\n%s", outMD)
	}
}
