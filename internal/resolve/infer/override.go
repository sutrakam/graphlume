package infer

import (
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// # A member is looked up on the receiver's own class first
//
// A receiver named `$handler` is "named after the type that declares it" when
// the repository has a `Handler` with a `handleBatch`, and the call is bound
// there. That is a statement about the receiver's static type, and where the
// named type is a base class it says nothing about the value's class:
// `$handler` held a MailHandler, an ElasticaHandler and a SocketHandler in
// three of the thirty `high` edges sampled on monolog, and each of the three
// declares its own `handleBatch` or `close`. The language looks the member up
// on the value's own class and only then up its ancestry, so an override
// shadows the base method and the base method is the wrong target.
//
// Whether another declarer is a descendant of the named type is a question for
// the hierarchy, and the hierarchy exists only once every EXTENDS and
// IMPLEMENTS reference is bound (the argument at the top of inherited.go). So a
// binding made by the name hint is parked when another type declares the same
// member, carrying the hinted binding as its fallback, and settled by
// ResolveDeferred: if any other declarer is a descendant of the named type the
// call is recorded at `low` with a reason that says so, and otherwise the
// binding stands at the tier it earned. Without an Ancestry nothing is parked
// and the binding is made as before.

// deferOverride parks a name-hinted binding whose member other types also
// declare, and reports whether it did.
func (r *Resolver) deferOverride(c call, best candidate, members []registry.SymbolIndexEntry) bool {
	if r.ancestry == nil || len(r.deferred) >= MaxDeferred {
		return false
	}
	named, _ := memberOwner(best.entry)
	var others []string
	for _, s := range members {
		owner, _ := memberOwner(s)
		if owner == named || containsString(others, owner) {
			continue
		}
		others = append(others, owner)
	}
	if named == "" || len(others) == 0 {
		return false
	}
	sort.Strings(others)
	d := deferredRef{
		refID:       c.ref.ID,
		name:        c.ref.Name,
		kind:        c.ref.Kind,
		receiver:    named,
		spec:        c.spec,
		filePath:    c.fr.Path,
		line:        c.ref.Line,
		col:         c.ref.Col,
		fallback:    best,
		hasFallback: true,
		named:       named,
		overriders:  others,
	}
	d.srcID, d.hasSrc = callerOf(c.fr, c.ref.Line)
	r.deferred = append(r.deferred, d)
	return true
}

// overriddenBelow reports which of the other declarers descend from the named
// type, going only by the supertypes the drain has already fetched. A type the
// hierarchy was not walked for is not claimed to descend from anything.
func overriddenBelow(d deferredRef, known map[string][]string) []string {
	var below []string
	for _, o := range d.overriders {
		seen := map[string]struct{}{o: {}}
		frontier := []string{o}
		found := false
		for depth := 0; depth < MaxAncestryDepth && len(frontier) > 0 && !found; depth++ {
			var next []string
			for _, t := range frontier {
				for _, p := range known[t] {
					if p == d.named {
						found = true
					}
					if _, dup := seen[p]; p == "" || dup {
						continue
					}
					seen[p] = struct{}{}
					next = append(next, p)
				}
			}
			frontier = next
		}
		if found {
			below = append(below, o)
		}
	}
	return below
}

// settleOverride is the binding a name-hinted reference gets once the
// hierarchy is known.
func settleOverride(d deferredRef, known map[string][]string) candidate {
	best := d.fallback
	below := overriddenBelow(d, known)
	if len(below) == 0 {
		return best
	}
	shown := below
	if len(shown) > 3 {
		shown = shown[:3]
	}
	best.confidence = weaker(best.confidence, model.ConfidenceLow)
	best.reason = best.reason + "; " + strings.Join(shown, ", ") + " descend from " + d.named +
		" and declare " + d.name + " again, so the receiver may be an instance of one of them"
	return best
}
