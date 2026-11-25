package cli

import (
	"strings"
	"testing"
)

func TestAgentDeeplinks(t *testing.T) {
	root := "/path/to/project"
	links := AgentDeeplinks(root)

	if len(links) < 4 {
		t.Fatalf("expected at least 4 deep links, got %d", len(links))
	}

	foundCursor := false
	foundVSCode := false
	foundWindsurf := false

	for _, dl := range links {
		if dl.Client == "Cursor" && strings.HasPrefix(dl.URI, "cursor://") {
			foundCursor = true
		}
		if dl.Client == "VS Code" && strings.HasPrefix(dl.URI, "vscode:mcp/install?") {
			foundVSCode = true
		}
		if dl.Client == "Windsurf" && strings.HasPrefix(dl.URI, "windsurf://") {
			foundWindsurf = true
		}
	}

	if !foundCursor {
		t.Errorf("missing or invalid Cursor deep link")
	}
	if !foundVSCode {
		t.Errorf("missing or invalid VS Code deep link")
	}
	if !foundWindsurf {
		t.Errorf("missing or invalid Windsurf deep link")
	}

	compactLinks := AgentDeeplinks(root, "compact")
	for _, dl := range compactLinks {
		if dl.Client == "Cursor" && !strings.Contains(dl.URI, "compact") {
			t.Errorf("expected compact profile in Cursor link: %s", dl.URI)
		}
	}
}
