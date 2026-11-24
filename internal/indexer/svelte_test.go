package indexer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestConvertSvelteSource(t *testing.T) {
	svelteSrc := `<script lang="ts">
  import { onMount } from 'svelte'
  import { formatMoney } from './money'

  function checkout() {
    formatMoney(10)
  }
</script>

<div>
  <p>Hello world</p>
</div>
`
	converted := ConvertSvelteSource([]byte(svelteSrc))
	lines := bytes.Split(converted, []byte("\n"))

	// Verify line count matches
	origLines := bytes.Split([]byte(svelteSrc), []byte("\n"))
	if len(lines) != len(origLines) {
		t.Fatalf("line count mismatch: got %d, want %d", len(lines), len(origLines))
	}

	// Line 1 should have import onMount (0-indexed: line 1)
	if !bytes.Contains(lines[1], []byte("import { onMount }")) {
		t.Errorf("line 1: %q, want import { onMount }", string(lines[1]))
	}

	// Line 4 should have function checkout
	if !bytes.Contains(lines[4], []byte("function checkout()")) {
		t.Errorf("line 4: %q, want function checkout()", string(lines[4]))
	}

	// Line 5 should have formatMoney(10)
	if !bytes.Contains(lines[5], []byte("formatMoney(10)")) {
		t.Errorf("line 5: %q, want formatMoney(10)", string(lines[5]))
	}

	// Template lines (lines 9-11) should be empty/stripped
	if len(bytes.TrimSpace(lines[9])) != 0 || len(bytes.TrimSpace(lines[10])) != 0 {
		t.Errorf("template lines should be empty: %q, %q", string(lines[9]), string(lines[10]))
	}
}

func TestPrepareSvelteShadow(t *testing.T) {
	tmpDir := t.TempDir()
	outDir := t.TempDir()

	sveltePath := filepath.Join(tmpDir, "src", "Cart.svelte")
	if err := os.MkdirAll(filepath.Dir(sveltePath), 0o750); err != nil {
		t.Fatal(err)
	}
	content := `<script>
const x = 42
</script>
<p>text</p>`
	if err := os.WriteFile(sveltePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	shadow, err := PrepareSvelteShadow(tmpDir, tmpDir, outDir)
	if err != nil {
		t.Fatalf("PrepareSvelteShadow: %v", err)
	}
	if shadow.Count != 1 {
		t.Fatalf("count = %d, want 1", shadow.Count)
	}

	shadowFile := filepath.Join(shadow.ShadowDir, "src", "Cart.svelte.ts")
	b, err := os.ReadFile(shadowFile)
	if err != nil {
		t.Fatalf("read shadow file: %v", err)
	}
	if !bytes.Contains(b, []byte("const x = 42")) {
		t.Errorf("shadow file missing script content: %s", string(b))
	}
}
