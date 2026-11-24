package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sutrakam/graphlume/internal/indexer"
)

func TestTransformVueToTS(t *testing.T) {
	sfc := `<template>
  <div class="user-card">
    <span>{{ name }}</span>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
const name = ref('GraphLume');
export function greet(s: string): string {
  return 'Hello ' + s;
}
</script>

<style scoped>
.user-card { color: red; }
</style>
`

	ts, ok := transformVueToTS([]byte(sfc))
	if !ok {
		t.Fatalf("expected transform to succeed")
	}

	// Line count and length must match exactly
	if len(ts) != len(sfc) {
		t.Fatalf("length mismatch: got %d, want %d", len(ts), len(sfc))
	}

	// Script content must be intact
	tsStr := string(ts)
	if !containsStr(tsStr, "import { ref } from 'vue';") {
		t.Fatalf("script content was corrupted: %s", tsStr)
	}
	if !containsStr(tsStr, "export function greet(s: string): string") {
		t.Fatalf("greet function missing from output: %s", tsStr)
	}

	// Template and style content must be masked with spaces
	if containsStr(tsStr, "<template>") || containsStr(tsStr, "user-card") || containsStr(tsStr, "<style") {
		t.Fatalf("template or style tags were not masked: %s", tsStr)
	}

	// Line numbers of the script block must match original
	origLines := splitLines(sfc)
	tsLines := splitLines(tsStr)
	if len(origLines) != len(tsLines) {
		t.Fatalf("line count mismatch: got %d, want %d", len(tsLines), len(origLines))
	}

	for i := range origLines {
		if containsStr(origLines[i], "const name = ref") {
			if !containsStr(tsLines[i], "const name = ref") {
				t.Errorf("line %d does not match: %q vs %q", i+1, tsLines[i], origLines[i])
			}
		}
	}
}

func TestStageVueMirrors(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "frontend")
	if err := os.MkdirAll(filepath.Join(projDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	vueFile := filepath.Join(projDir, "src", "App.vue")
	vueContent := `<template><h1>Hi</h1></template><script lang="ts">export const x = 42;</script>`
	if err := os.WriteFile(vueFile, []byte(vueContent), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := []indexer.Project{
		{
			Dir:      "frontend",
			Language: indexer.LangTypeScript,
		},
	}

	mirrors := stageVueMirrors(tmp, projects)
	if len(mirrors) != 1 {
		t.Fatalf("expected 1 mirror, got %d", len(mirrors))
	}

	expectedMirror := vueFile + ".ts"
	if mirrors[0] != expectedMirror {
		t.Errorf("unexpected mirror path: got %s, want %s", mirrors[0], expectedMirror)
	}

	if _, err := os.Stat(expectedMirror); err != nil {
		t.Fatalf("mirror file was not created on disk: %v", err)
	}

	mirrorContent, err := os.ReadFile(expectedMirror)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(string(mirrorContent), "export const x = 42;") {
		t.Errorf("mirror content incorrect: %s", string(mirrorContent))
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || searchStr(s, sub))
}

func searchStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
