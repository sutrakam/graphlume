package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

type fakeTour struct {
	endpoints []registry.Endpoint
	syms      map[int64]registry.SymbolHit
	calls     map[int64][]registry.EdgeHit
	mains     []registry.SymbolHit
}

func (f fakeTour) EndpointReport(context.Context, int64) ([]registry.Endpoint, error) {
	return f.endpoints, nil
}
func (f fakeTour) OutgoingEdges(_ context.Context, _, id int64, _ int, flt registry.EdgeFilter) ([]registry.EdgeHit, error) {
	var out []registry.EdgeHit
	for _, e := range f.calls[id] {
		if model.Confidence(e.Confidence).AtLeast(flt.MinConfidence) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f fakeTour) SymbolByID(_ context.Context, _, id int64) (registry.SymbolHit, bool, error) {
	s, ok := f.syms[id]
	return s, ok, nil
}
func (f fakeTour) FindSymbolsByName(context.Context, int64, string, string, int) ([]registry.SymbolHit, error) {
	return f.mains, nil
}

func hit(id int64, name, conf string) registry.EdgeHit {
	return registry.EdgeHit{DstID: id, DstName: name, Confidence: conf, EvidenceFile: "x.go", EvidenceLine: int(id)}
}

func TestTourOrdersEntriesByReachAndStopsAtDepth(t *testing.T) {
	f := fakeTour{
		endpoints: []registry.Endpoint{
			{Signature: "GET /small", HandlerID: 1},
			{Signature: "POST /big", HandlerID: 2},
			{Signature: "GET /big2", HandlerID: 2}, // two routes, one handler
			{Signature: "GET /nohandler"},
		},
		syms: map[int64]registry.SymbolHit{
			1: {ID: 1, Name: "small", FilePath: "a.go", StartLine: 1},
			2: {ID: 2, Name: "big", FilePath: "b.go", StartLine: 2},
		},
		calls: map[int64][]registry.EdgeHit{
			1:  {hit(10, "one", "high")},
			2:  {hit(20, "svc", "high"), hit(21, "weak", "low")},
			20: {hit(30, "repo", "high")},
			30: {hit(40, "tooDeep", "high")},
		},
	}
	stops, err := buildTour(context.Background(), f, 1, tourOptions{Stops: 5, Depth: 2, Width: 5, MinConfidence: model.ConfidenceHigh})
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) != 2 || stops[0].Entry != "big" {
		t.Fatalf("the entry that reaches most comes first: %+v", stops)
	}
	if len(stops[0].Routes) != 2 {
		t.Errorf("both routes of one handler belong to one stop: %v", stops[0].Routes)
	}
	var names []string
	for _, s := range stops[0].Steps {
		names = append(names, s.Name)
	}
	got := strings.Join(names, ",")
	if got != "svc,repo" {
		t.Errorf("steps %q: want svc,repo (low edge dropped, depth 2 stops before tooDeep)", got)
	}

	var b bytes.Buffer
	writeTour(&b, stops)
	if out := b.String(); !strings.Contains(out, "1. big") || !strings.Contains(out, "serves GET /big2") {
		t.Errorf("rendering:\n%s", out)
	}
}

func TestTourWithNoEntryPointsSaysSo(t *testing.T) {
	var b bytes.Buffer
	stops, _ := buildTour(context.Background(), fakeTour{}, 1, tourOptions{Stops: 3, Depth: 2, Width: 3, MinConfidence: model.ConfidenceHigh})
	writeTour(&b, stops)
	if !strings.Contains(b.String(), "No entry points found") {
		t.Errorf("empty tour:\n%s", b.String())
	}
}
