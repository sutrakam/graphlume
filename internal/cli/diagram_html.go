package cli

import (
	"fmt"
	"html"
	"io"
	"strings"
)

// # The HTML diagram
//
// A self-contained page: the layout is computed here and drawn as inline SVG, so
// the file needs no script, no network and no renderer. It opens in any browser,
// prints, and can be attached to a ticket.
//
// The layout is layered, left to right. A node's layer is its distance from the
// nearest source (a node nothing points to); nodes in a layer keep the order they
// were first met. Both are deterministic, so the same edges draw the same page.
// A cycle has no source, so its first node in order is taken as one.

const (
	htmlColGap  = 90.0 // space between layers
	htmlRowGap  = 18.0 // space between nodes in a layer
	htmlNodeH   = 34.0
	htmlPad     = 24.0
	htmlCharW   = 7.2 // a monospace-ish estimate of one character
	htmlMinW    = 80.0
	htmlMaxW    = 320.0
	htmlMaxName = 44 // longest label drawn before it is cut with an ellipsis
)

// diagramLayout is a drawn node: its box.
type diagramNode struct {
	Name         string
	Layer, Index int
	X, Y, W      float64
}

// layoutLayers assigns each node a layer and a place in it.
func layoutLayers(edges []diagramEdge) []*diagramNode {
	index := map[string]int{}
	var order []string
	add := func(n string) {
		if _, ok := index[n]; !ok {
			index[n] = len(order)
			order = append(order, n)
		}
	}
	out := map[string][]string{}
	indeg := map[string]int{}
	for _, e := range edges {
		add(e.From)
		add(e.To)
		out[e.From] = append(out[e.From], e.To)
		indeg[e.To]++
	}
	layer := map[string]int{}
	visit := func(root string) {
		if _, done := layer[root]; done {
			return
		}
		layer[root] = 0
		queue := []string{root}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range out[cur] {
				if _, done := layer[next]; !done {
					layer[next] = layer[cur] + 1
					queue = append(queue, next)
				}
			}
		}
	}
	for _, n := range order {
		if indeg[n] == 0 {
			visit(n)
		}
	}
	for _, n := range order { // what only a cycle reaches
		visit(n)
	}

	perLayer := map[int][]string{}
	maxLayer := 0
	for _, n := range order {
		l := layer[n]
		perLayer[l] = append(perLayer[l], n)
		if l > maxLayer {
			maxLayer = l
		}
	}
	width := make([]float64, maxLayer+1)
	for l := 0; l <= maxLayer; l++ {
		width[l] = htmlMinW
		for _, n := range perLayer[l] {
			if w := float64(len([]rune(shortLabel(n))))*htmlCharW + 24; w > width[l] {
				width[l] = w
			}
		}
		if width[l] > htmlMaxW {
			width[l] = htmlMaxW
		}
	}
	x := make([]float64, maxLayer+1)
	cursor := htmlPad
	for l := 0; l <= maxLayer; l++ {
		x[l] = cursor
		cursor += width[l] + htmlColGap
	}
	nodes := make([]*diagramNode, 0, len(order))
	for _, n := range order {
		l := layer[n]
		idx := 0
		for i, m := range perLayer[l] {
			if m == n {
				idx = i
			}
		}
		nodes = append(nodes, &diagramNode{
			Name: n, Layer: l, Index: idx,
			X: x[l], Y: htmlPad + float64(idx)*(htmlNodeH+htmlRowGap), W: width[l],
		})
	}
	return nodes
}

// shortLabel cuts a long name so a box stays a reasonable size; the full name is
// in the tooltip.
func shortLabel(name string) string {
	r := []rune(name)
	if len(r) <= htmlMaxName {
		return name
	}
	return string(r[:htmlMaxName-1]) + "…"
}

// writeHTMLDiagram draws the edges as a standalone HTML page.
func writeHTMLDiagram(w io.Writer, title string, edges []diagramEdge) error {
	edges = dedupeDiagramEdges(edges)
	nodes := layoutLayers(edges)
	byName := map[string]*diagramNode{}
	var maxX, maxY float64
	for _, n := range nodes {
		byName[n.Name] = n
		if n.X+n.W > maxX {
			maxX = n.X + n.W
		}
		if n.Y+htmlNodeH > maxY {
			maxY = n.Y + htmlNodeH
		}
	}
	hasSelfLoop := false
	for _, e := range edges {
		if e.From == e.To {
			hasSelfLoop = true
			break
		}
	}
	extraMargin := 0.0
	if hasSelfLoop {
		extraMargin = 60.0
	}
	width, height := maxX+htmlPad+extraMargin, maxY+htmlPad
	if len(nodes) == 0 {
		width, height = 240, 80
	}

	var b strings.Builder
	t := html.EscapeString(cleanDiagramString(title))
	fmt.Fprintf(&b, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root{--bg:#fff;--fg:#1b1f24;--box:#eef2f7;--line:#8a94a3;--edge:#4c5a6b;--weak:#b0651c}
@media (prefers-color-scheme:dark){:root{--bg:#14181d;--fg:#e6e9ee;--box:#232a33;--line:#5d6877;--edge:#9fb0c3;--weak:#e0a35c}}
body{margin:0;padding:16px;background:var(--bg);color:var(--fg);font:14px system-ui,sans-serif}
h1{font-size:16px;margin:0 0 4px}p{margin:0 0 12px;color:var(--line)}
svg{max-width:100%%;height:auto;display:block}
.node rect{fill:var(--box);stroke:var(--line)}.node text{fill:var(--fg);font:12px ui-monospace,monospace}
.edge{fill:none;stroke:var(--edge);stroke-width:1.4}.edge.weak{stroke:var(--weak);stroke-dasharray:6 4}
.lbl{fill:var(--line);font:10px system-ui,sans-serif}
</style></head><body>
<h1>%s</h1><p>Dashed edges are below high confidence. Hover a box or edge for detail.</p>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">
<defs><marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0L10,5L0,10z" fill="var(--edge)"/></marker></defs>
`, t, t, width, height, t)

	pairCounts := map[[2]string]int{}
	for _, e := range edges {
		from, to := byName[e.From], byName[e.To]
		if from == nil || to == nil {
			continue
		}
		cls := "edge"
		if weak(e.Confidence) {
			cls += " weak"
		}
		pairKey := [2]string{e.From, e.To}
		pairIdx := pairCounts[pairKey]
		pairCounts[pairKey]++

		if from == to {
			// Self-loop: loop outward from the right side of the node box
			x1, y1 := from.X+from.W, from.Y+htmlNodeH*0.25
			x2, y2 := from.X+from.W, from.Y+htmlNodeH*0.75
			ctrlX := from.X + from.W + 28.0 + float64(pairIdx)*12.0
			fmt.Fprintf(&b, `<path class="%s" d="M%.1f,%.1f C%.1f,%.1f %.1f,%.1f %.1f,%.1f" marker-end="url(#a)"><title>%s</title></path>
`, cls, x1, y1, ctrlX, y1-10, ctrlX, y2+10, x2, y2, html.EscapeString(cleanDiagramString(from.Name+" ↺ ("+edgeLabel(e)+")")))
			fmt.Fprintf(&b, `<text class="lbl" x="%.1f" y="%.1f" text-anchor="start">%s</text>
`, ctrlX+4, (y1+y2)/2+3, html.EscapeString(cleanDiagramString(edgeLabel(e))))
			continue
		}

		x1, y1 := from.X+from.W, from.Y+htmlNodeH/2
		x2, y2 := to.X, to.Y+htmlNodeH/2
		if to.Layer <= from.Layer { // a back or sideways edge leaves the bottom and enters the top
			x1, y1 = from.X+from.W/2, from.Y+htmlNodeH
			x2, y2 = to.X+to.W/2, to.Y
		}
		dx := (x2 - x1) / 2
		dyOffset := float64(pairIdx) * 12.0
		fmt.Fprintf(&b, `<path class="%s" d="M%.1f,%.1f C%.1f,%.1f %.1f,%.1f %.1f,%.1f" marker-end="url(#a)"><title>%s</title></path>
`, cls, x1, y1, x1+dx, y1-dyOffset, x2-dx, y2-dyOffset, x2, y2, html.EscapeString(cleanDiagramString(from.Name+" → "+to.Name+" ("+edgeLabel(e)+")")))
		fmt.Fprintf(&b, `<text class="lbl" x="%.1f" y="%.1f" text-anchor="middle">%s</text>
`, (x1+x2)/2, (y1+y2)/2-4-dyOffset, html.EscapeString(cleanDiagramString(edgeLabel(e))))
	}
	for _, n := range nodes {
		cleanName := cleanDiagramString(n.Name)
		fmt.Fprintf(&b, `<g class="node"><rect x="%.1f" y="%.1f" width="%.1f" height="%.0f" rx="5"/><text x="%.1f" y="%.1f" text-anchor="middle">%s</text><title>%s</title></g>
`, n.X, n.Y, n.W, htmlNodeH, n.X+n.W/2, n.Y+htmlNodeH/2+4, html.EscapeString(cleanDiagramString(shortLabel(cleanName))), html.EscapeString(cleanName))
	}
	b.WriteString("</svg>\n</body></html>\n")
	_, err := io.WriteString(w, b.String())
	return err
}
