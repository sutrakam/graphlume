package cli

import (
	"fmt"
	"io"
	"strings"
)

// diagramEdge is one relationship drawn in a diagram.
type diagramEdge struct {
	From, To   string
	Kind       string
	Confidence string
}

// Diagram formats accepted by --diagram.
const (
	diagramMermaid = "mermaid"
	diagramDOT     = "dot"
	diagramHTML    = "html"
)

// checkDiagramFormat rejects a format this build does not draw.
func checkDiagramFormat(format string) error {
	switch format {
	case "", diagramMermaid, diagramDOT, diagramHTML:
		return nil
	}
	return fmt.Errorf("--diagram %q: want mermaid, dot or html", format)
}

// writeDiagram draws the edges as Mermaid, Graphviz DOT or a standalone HTML page.
// Mermaid and DOT are text that GitHub, most editors and `dot` draw; the HTML
// page is laid out here and drawn as inline SVG, so no renderer is needed.
// Edges are drawn in the order given, duplicates once; a weaker confidence is
// drawn dashed, so a route's weakest hop is visible at a glance.
func writeDiagram(w io.Writer, format, title string, edges []diagramEdge) error {
	if err := checkDiagramFormat(format); err != nil {
		return err
	}
	if format == diagramHTML {
		return writeHTMLDiagram(w, title, edges)
	}
	edges = dedupeDiagramEdges(edges)
	ids := map[string]string{}
	var order []string
	idOf := func(name string) string {
		if id, ok := ids[name]; ok {
			return id
		}
		id := fmt.Sprintf("n%d", len(ids)+1)
		ids[name] = id
		order = append(order, name)
		return id
	}
	for _, e := range edges {
		idOf(e.From)
		idOf(e.To)
	}
	switch format {
	case diagramDOT:
		fmt.Fprintf(w, "digraph %s {\n  rankdir=LR;\n", quoteDOT(title))
		for _, n := range order {
			fmt.Fprintf(w, "  %s [label=%s];\n", ids[n], quoteDOT(n))
		}
		for _, e := range edges {
			style := "solid"
			if weak(e.Confidence) {
				style = "dashed"
			}
			fmt.Fprintf(w, "  %s -> %s [label=%s, style=%s];\n", ids[e.From], ids[e.To],
				quoteDOT(edgeLabel(e)), style)
		}
		fmt.Fprintln(w, "}")
	default:
		fmt.Fprintf(w, "%%%% %s\nflowchart LR\n", cleanDiagramString(title))
		for _, n := range order {
			fmt.Fprintf(w, "  %s[%s]\n", ids[n], quoteMermaid(n))
		}
		for _, e := range edges {
			arrow := "-->"
			if weak(e.Confidence) {
				arrow = "-.->"
			}
			fmt.Fprintf(w, "  %s %s|%s| %s\n", ids[e.From], arrow, quoteMermaid(edgeLabel(e)), ids[e.To])
		}
	}
	return nil
}

// weak reports a confidence below high: drawn dashed.
func weak(confidence string) bool { return confidence != "exact" && confidence != "high" }

func edgeLabel(e diagramEdge) string {
	label := strings.ToLower(e.Kind)
	if e.Confidence != "" {
		label += " " + e.Confidence
	}
	return label
}

// dedupeDiagramEdges drops repeats of the same from, to and kind, keeping the
// first, and sorts nothing: the order the caller gave is the order drawn.
func dedupeDiagramEdges(edges []diagramEdge) []diagramEdge {
	seen := map[[3]string]struct{}{}
	out := make([]diagramEdge, 0, len(edges))
	for _, e := range edges {
		k := [3]string{e.From, e.To, e.Kind}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, e)
	}
	return out
}

// cleanDiagramString strips control characters and replaces CR/LF with spaces
// so text cannot break XML/SVG parsing or DOT/Mermaid grammar.
func cleanDiagramString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n':
			b.WriteByte(' ')
		case r >= 0x20 && r <= 0x7E:
			b.WriteRune(r)
		case r >= 0xA0: // non-ASCII Unicode (emojis, i18n, etc.)
			b.WriteRune(r)
		default:
			// Strip NUL (\x00), ASCII control chars (\x01-\x1F, \x7F)
		}
	}
	return b.String()
}

// quoteMermaid wraps text so punctuation in a name cannot end the label.
func quoteMermaid(s string) string {
	s = cleanDiagramString(s)
	s = strings.NewReplacer(`"`, "#quot;").Replace(s)
	return `"` + s + `"`
}

// quoteDOT quotes a DOT string.
func quoteDOT(s string) string {
	s = cleanDiagramString(s)
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + s + `"`
}
