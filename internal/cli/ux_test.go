package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIFormatting(t *testing.T) {
	var buf bytes.Buffer
	ui := &UI{w: &buf, color: false}

	ui.Header("Testing Header")
	ui.Success("Item succeeded: %s", "file.go")
	ui.Info("Notice: %s", "check something")
	ui.Warn("Warning: %s", "look out")

	out := buf.String()
	if !strings.Contains(out, "==> Testing Header") {
		t.Errorf("expected header, got:\n%s", out)
	}
	if !strings.Contains(out, "✔ Item succeeded: file.go") {
		t.Errorf("expected success mark, got:\n%s", out)
	}
	if !strings.Contains(out, "ℹ Notice: check something") {
		t.Errorf("expected info mark, got:\n%s", out)
	}
	if !strings.Contains(out, "▲ Warning: look out") {
		t.Errorf("expected warn mark, got:\n%s", out)
	}
}

func TestProgressBarNonTerminal(t *testing.T) {
	var buf bytes.Buffer
	pb := newProgressBar(&buf, "Indexing", false)
	pb.isTerminal = false

	pb.Update(25, 100)
	pb.Update(50, 100)
	pb.Update(75, 100)
	pb.Update(100, 100)
	pb.Finish("Done indexing 100 files")

	out := buf.String()
	if !strings.Contains(out, "Indexing: 25% (25/100 files)") {
		t.Errorf("expected 25%% milestone, got:\n%s", out)
	}
	if !strings.Contains(out, "Done indexing 100 files") {
		t.Errorf("expected finish message, got:\n%s", out)
	}
}

func TestPrintInstallSummary(t *testing.T) {
	var buf bytes.Buffer
	results := []installResult{
		{Client: "Claude Code", Action: "updated"},
		{Client: "Cursor", Action: "updated"},
		{Client: "VS Code", Action: "unchanged"},
		{Client: "Windsurf", Action: "created"},
	}

	printInstallSummary(&buf, results)
	out := buf.String()
	if !strings.Contains(out, "Configured 4 coding agent integrations") {
		t.Errorf("expected count 4, got:\n%s", out)
	}
	if !strings.Contains(out, "Claude Code") {
		t.Errorf("expected sample client names, got:\n%s", out)
	}
}

func TestDoctorBundleGeneration(t *testing.T) {
	dir := t.TempDir()
	bundleFile := filepath.Join(dir, "bundle.json")

	g := &Global{Project: dir}
	cmd := newBundleCmd(g)
	cmd.SetArgs([]string{"--out", bundleFile})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("bundle command failed: %v\noutput:\n%s", err, out.String())
	}

	if _, err := os.Stat(bundleFile); err != nil {
		t.Fatalf("expected bundle file to exist at %s: %v", bundleFile, err)
	}

	data, err := os.ReadFile(bundleFile)
	if err != nil {
		t.Fatalf("read bundle file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "graphlume_version") || !strings.Contains(content, "doctor_checks") {
		t.Errorf("bundle content missing expected keys:\n%s", content)
	}
	if !strings.Contains(content, "github_issues_url") {
		t.Errorf("bundle content missing github URL:\n%s", content)
	}
}
