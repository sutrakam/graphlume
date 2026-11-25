package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestWriteDiagramMermaid(t *testing.T) {
	var b bytes.Buffer
	edges := []diagramEdge{
		{From: "handle", To: "save", Kind: "CALLS", Confidence: "exact"},
		{From: "save", To: "write \"x\"", Kind: "CALLS", Confidence: "low"},
		{From: "handle", To: "save", Kind: "CALLS", Confidence: "exact"}, // repeat
	}
	if err := writeDiagram(&b, diagramMermaid, "handle to write", edges); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"flowchart LR", `n1["handle"]`, "n1 -->|\"calls exact\"| n2", "-.->", "#quot;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "n1 -->") != 1 {
		t.Errorf("a repeated edge was drawn twice:\n%s", out)
	}
}

func TestWriteDiagramDOTAndErrors(t *testing.T) {
	var b bytes.Buffer
	if err := writeDiagram(&b, diagramDOT, "g", []diagramEdge{{From: "a", To: "b", Kind: "CALLS", Confidence: "medium"}}); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "digraph") || !strings.Contains(out, "style=dashed") {
		t.Errorf("dot output:\n%s", out)
	}
	if err := writeDiagram(&b, "svg", "g", nil); err == nil {
		t.Error("an unknown format was accepted")
	}
}

func TestHTMLDiagramLayersAndIsSelfContained(t *testing.T) {
	edges := []diagramEdge{
		{From: "handle", To: "svc", Kind: "CALLS", Confidence: "exact"},
		{From: "svc", To: "repo", Kind: "CALLS", Confidence: "low"},
		{From: "handle", To: "log", Kind: "CALLS", Confidence: "high"},
	}
	nodes := layoutLayers(edges)
	layers := map[string]int{}
	for _, n := range nodes {
		layers[n.Name] = n.Layer
	}
	if layers["handle"] != 0 || layers["svc"] != 1 || layers["repo"] != 2 || layers["log"] != 1 {
		t.Errorf("layers: %v", layers)
	}

	var b bytes.Buffer
	if err := writeDiagram(&b, diagramHTML, "handle", edges); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"<!doctype html>", "<svg", "stroke-dasharray", "<title>handle → svc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Nothing to fetch and nothing to run.
	for _, bad := range []string{"<script", "http://", "https://", "<link", "@import"} {
		if strings.Contains(strings.ReplaceAll(out, "http://www.w3.org/2000/svg", ""), bad) {
			t.Errorf("page is not self-contained: found %q", bad)
		}
	}
}

func TestHTMLDiagramEscapesNamesAndSurvivesCycles(t *testing.T) {
	var b bytes.Buffer
	edges := []diagramEdge{
		{From: "<img src=x onerror=alert(1)>", To: "b", Kind: "CALLS", Confidence: "high"},
		{From: "b", To: "<img src=x onerror=alert(1)>", Kind: "CALLS", Confidence: "high"}, // a cycle
	}
	if err := writeDiagram(&b, diagramHTML, `t"</title><script>`, edges); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "<img") || strings.Contains(out, "<script>") {
		t.Errorf("a name or title reached the page unescaped:\n%s", out)
	}
	var empty bytes.Buffer
	if err := writeDiagram(&empty, diagramHTML, "none", nil); err != nil || !strings.Contains(empty.String(), "<svg") {
		t.Errorf("an empty diagram: %v", err)
	}
}

func TestHTMLDiagramBoxesNeverOverlap(t *testing.T) {
	var edges []diagramEdge
	for i := 0; i < 12; i++ {
		edges = append(edges, diagramEdge{From: "root", To: fmt.Sprintf("child-with-a-long-name-%d", i), Kind: "CALLS", Confidence: "high"})
		edges = append(edges, diagramEdge{From: fmt.Sprintf("child-with-a-long-name-%d", i), To: "leaf", Kind: "CALLS", Confidence: "high"})
	}
	nodes := layoutLayers(edges)
	for i, a := range nodes {
		for _, b := range nodes[i+1:] {
			sameColumn := a.X < b.X+b.W && b.X < a.X+a.W
			sameRow := a.Y < b.Y+htmlNodeH && b.Y < a.Y+htmlNodeH
			if sameColumn && sameRow {
				t.Fatalf("%q and %q overlap", a.Name, b.Name)
			}
		}
	}
}
