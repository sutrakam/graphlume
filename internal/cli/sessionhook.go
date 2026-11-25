package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sessionHookCommand is what Claude Code runs when a session starts or is
// resumed after compaction. A SessionStart hook's standard output becomes
// context for the agent, so printing the guidance block puts "ask the graph
// before grepping" in front of the agent every session, including the ones
// that begin after a compaction dropped CLAUDE.md from the window.
func sessionHookCommand(root string) string {
	return fmt.Sprintf("graphlume --project %s agent --guidance", shellQuote(root))
}

// installSessionHook adds the SessionStart hook to the project's
// .claude/settings.json, merging into what is there. It never removes or
// rewrites a hook it did not write, refuses a file it cannot parse rather than
// replacing it, and reports whether anything changed.
func installSessionHook(root string, dryRun bool) (path string, changed bool, err error) {
	path = filepath.Join(root, ".claude", "settings.json")
	doc := map[string]any{}
	existing, readErr := os.ReadFile(filepath.Clean(path))
	switch {
	case readErr == nil && len(strings.TrimSpace(string(existing))) > 0:
		if err := json.Unmarshal(existing, &doc); err != nil {
			return path, false, fmt.Errorf("%s is not valid JSON; fix or remove it, then run setup --hooks again: %w", path, err)
		}
	case readErr != nil && !errors.Is(readErr, os.ErrNotExist):
		return path, false, fmt.Errorf("read %s: %w", path, readErr)
	}

	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		if _, present := doc["hooks"]; present {
			return path, false, fmt.Errorf("%s: \"hooks\" is not an object; not changing it", path)
		}
		hooks = map[string]any{}
	}
	var groups []any
	if v, present := hooks["SessionStart"]; present {
		var ok bool
		if groups, ok = v.([]any); !ok {
			return path, false, fmt.Errorf("%s: \"hooks.SessionStart\" is not a list; not changing it", path)
		}
	}

	want := sessionHookCommand(root)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		inner, _ := gm["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			cmd, _ := hm["command"].(string)
			if cmd == want {
				return path, false, nil // already installed
			}
		}
	}

	groups = append(groups, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": want, "timeout": 10}},
	})
	hooks["SessionStart"] = groups
	doc["hooks"] = hooks
	if dryRun {
		return path, true, nil
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return path, false, fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return path, false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return path, false, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return path, false, fmt.Errorf("replace %s: %w", path, err)
	}
	return path, true, nil
}
