package registry

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// TestConstructionSitesAreAConstructorsCallers: `new Keys()` is recorded as
// INSTANTIATES to the class Keys, so the callers of the constructor Keys.Keys,
// and of the class itself, are those sites. An ordinary method gets none.
func TestConstructionSitesAreAConstructorsCallers(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	keys := "core/Keys.java"
	files := []fileSpec{
		{path: keys, lang: "java", hash: "h1", content: "x", syms: []symSpec{
			{fqn: model.DefaultFQN(keys, "Keys"), name: "Keys", line: 1, kind: model.KindClass},
			{fqn: model.DefaultFQN(keys, "Keys") + ".Keys", name: "Keys", line: 3, kind: model.KindMethod},
			{fqn: model.DefaultFQN(keys, "Keys") + ".size", name: "size", line: 5, kind: model.KindMethod},
		}},
		{path: "core/Reactor.java", lang: "java", hash: "h2", content: "y", syms: []symSpec{
			{fqn: model.DefaultFQN("core/Reactor.java", "start"), name: "start", line: 10, kind: model.KindMethod},
		}},
	}
	ids := writeContent(t, r, run.ID, files, []edgeSpec{{
		srcFQN: model.DefaultFQN("core/Reactor.java", "start"), dstFQN: model.DefaultFQN(keys, "Keys"),
		kind: model.EdgeInstantiates, conf: model.ConfidenceHigh, source: model.SourceSourceCode,
		evFile: "core/Reactor.java", evLine: 12,
	}})
	if err := r.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	ctor := ids[model.DefaultFQN(keys, "Keys")+".Keys"]
	class := ids[model.DefaultFQN(keys, "Keys")]
	size := ids[model.DefaultFQN(keys, "Keys")+".size"]

	if got, ok, err := r.ConstructorClass(ctx, run.ID, ctor); err != nil || !ok || got != class {
		t.Fatalf("ConstructorClass(Keys.Keys) = %d, %v, %v; want the class %d", got, ok, err, class)
	}
	if _, ok, err := r.ConstructorClass(ctx, run.ID, size); err != nil || ok {
		t.Fatalf("an ordinary method was taken for a constructor (ok=%v, err=%v)", ok, err)
	}
	for name, id := range map[string]int64{"constructor": ctor, "class": class} {
		sites, err := r.ConstructionSites(ctx, run.ID, id, 10, EdgeFilter{})
		if err != nil || len(sites) != 1 || sites[0].SrcName != "start" || sites[0].Kind != string(model.EdgeInstantiates) {
			t.Errorf("ConstructionSites(%s) = %+v, %v; want the one site in start", name, sites, err)
		}
	}
	if sites, err := r.ConstructionSites(ctx, run.ID, size, 10, EdgeFilter{}); err != nil || len(sites) != 0 {
		t.Errorf("an ordinary method has construction sites: %+v, %v", sites, err)
	}
	if sites, err := r.ConstructionSites(ctx, run.ID, ctor, 10, EdgeFilter{MinConfidence: model.ConfidenceExact}); err != nil || len(sites) != 0 {
		t.Errorf("the confidence floor was not applied: %+v, %v", sites, err)
	}
}
