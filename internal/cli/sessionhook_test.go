package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("settings is not JSON: %v\n%s", err, b)
	}
	return doc
}

func sessionStartCommands(doc map[string]any) []string {
	var out []string
	hooks, _ := doc["hooks"].(map[string]any)
	groups, _ := hooks["SessionStart"].([]any)
	for _, g := range groups {
		inner, _ := g.(map[string]any)["hooks"].([]any)
		for _, h := range inner {
			if c, ok := h.(map[string]any)["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func TestSessionHookInstallsOnceAndKeepsTheUsersHooks(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o750); err != nil {
		t.Fatal(err)
	}
	user := `{"permissions":{"allow":["Bash(ls)"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo mine"}]}],"Stop":[]}}`
	if err := os.WriteFile(settings, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, changed, err := installSessionHook(root, true); err != nil || !changed {
		t.Fatalf("dry run: changed=%v err=%v", changed, err)
	}
	if b, _ := os.ReadFile(settings); string(b) != user {
		t.Fatal("a dry run modified settings.json")
	}

	if _, changed, err := installSessionHook(root, false); err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	doc := readSettings(t, settings)
	cmds := sessionStartCommands(doc)
	if len(cmds) != 2 || cmds[0] != "echo mine" || !strings.Contains(cmds[1], "agent --guidance") {
		t.Fatalf("SessionStart commands = %q; want the user's hook kept and ours added", cmds)
	}
	if _, ok := doc["permissions"]; !ok {
		t.Error("an unrelated key was dropped")
	}
	if _, ok := doc["hooks"].(map[string]any)["Stop"]; !ok {
		t.Error("another hook event was dropped")
	}

	if _, changed, err := installSessionHook(root, false); err != nil || changed {
		t.Fatalf("second install: changed=%v err=%v; want idempotent", changed, err)
	}
	if got := sessionStartCommands(readSettings(t, settings)); len(got) != 2 {
		t.Errorf("a second run added another hook: %q", got)
	}
}

func TestSessionHookRefusesSettingsItCannotParse(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o750); err != nil {
		t.Fatal(err)
	}
	broken := `{"hooks": [}`
	if err := os.WriteFile(settings, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := installSessionHook(root, false); err == nil {
		t.Fatal("malformed settings.json was accepted")
	}
	if b, _ := os.ReadFile(settings); string(b) != broken {
		t.Error("malformed settings.json was overwritten")
	}
}
