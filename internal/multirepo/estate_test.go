package multirepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// estateService is goEstate behind a pool whose opener hands back each
// member's fake source by index path, plus one member whose index is missing.
func estateService(t *testing.T) *EstateService {
	t.Helper()
	byPath := map[string]Member{}
	var members []EstateMember
	for _, m := range goEstate() {
		byPath[m.IndexPath] = m
		members = append(members, EstateMember{Name: m.Name, Index: m.IndexPath})
	}
	members = append(members, EstateMember{Name: "gone", Index: "/estate/gone/.graphlume/index.db"})
	opener := func(_ context.Context, dbPath string) (*OpenedStore, error) {
		m, ok := byPath[dbPath]
		if !ok {
			return nil, fmt.Errorf("stat %s: %w", dbPath, fs.ErrNotExist)
		}
		return &OpenedStore{Source: m.Source, RunID: m.RunID}, nil
	}
	svc := NewEstateService(members, opener, "")
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestEstateServiceReportsAnUnopenableMemberAndAnswersForTheRest(t *testing.T) {
	svc := estateService(t)
	ctx := context.Background()

	rep, err := svc.Links(ctx)
	if err != nil {
		t.Fatalf("an unopenable member must not fail the report: %v", err)
	}
	if strings.Join(rep.Unjoined.UnavailableMembers, ",") != "gone" {
		t.Fatalf("unavailable = %v, want [gone]", rep.Unjoined.UnavailableMembers)
	}
	var reason string
	for _, m := range rep.Members {
		if m.Name == "gone" {
			reason = m.Unavailable
		}
	}
	if !strings.Contains(reason, "no index at /estate/gone/.graphlume/index.db") {
		t.Errorf("reason = %q", reason)
	}
	if rep.GoModules == nil || len(rep.GoModules.Links) == 0 {
		t.Fatalf("the readable members still link: %+v", rep.GoModules)
	}
	if got := svc.Unreadable(ctx); len(got) != 1 || got["gone"] != reason {
		t.Errorf("Unreadable = %v", got)
	}

	callers, pick, err := svc.Callers(ctx, "lib:Do", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if pick.Target.ID != 10 || pick.Target.File != "lib.go" {
		t.Errorf("picked %+v", pick.Target)
	}
	var got []string
	for _, c := range callers.Callers {
		got = append(got, c.Member+" "+c.Caller+" "+c.File+":"+strconv.Itoa(c.Line)+" "+string(c.Confidence)+" "+c.Leg)
	}
	want := []string{
		"lib Wrap caller.go:4 exact local",
		"app run main.go:10 high go-module",
		"web Fetch web.go:3 high go-module",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("callers =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Join(callers.UnavailableMembers, ",") != "gone" {
		t.Errorf("callers unavailable = %v", callers.UnavailableMembers)
	}

	imp, _, err := svc.Impact(ctx, "lib:Do", "", ImpactOptions{MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range imp.Rows {
		if r.Member != "lib" && r.Confidence == "exact" {
			t.Errorf("%s:%s is exact across a repository boundary", r.Member, r.Symbol)
		}
	}
}

func TestPickSymbolClassifiesEveryRefusal(t *testing.T) {
	svc := estateService(t)
	ctx := context.Background()
	for _, c := range []struct {
		arg, path string
		kind      SymbolPickErrorKind
		text      string
	}{
		{"Do", "", PickMalformed, `"Do" is not <member>:<symbol> (for example: lib:Do)`},
		{"nobody:Do", "", PickUnknownMember, `no member is named "nobody"; the members are app, gone, lib, library, web`},
		{"gone:Do", "", PickMemberUnreadable, `member "gone" cannot be read: no index at`},
		{"lib:Nope", "", PickNotFound, `no symbol named "Nope" in lib; names must match exactly`},
		{"lib:Platform", "zzz", PickNotFound, `no symbol named "Platform" in a file of lib matching "zzz"; 2 definition(s) exist elsewhere`},
	} {
		_, _, err := svc.Callers(ctx, c.arg, c.path, 0)
		var pe *SymbolPickError
		if !errors.As(err, &pe) {
			t.Errorf("%s: err = %v, want a SymbolPickError", c.arg, err)
			continue
		}
		if pe.Kind != c.kind || !strings.HasPrefix(pe.Error(), c.text) {
			t.Errorf("%s: kind %d %q, want kind %d %q", c.arg, pe.Kind, pe.Error(), c.kind, c.text)
		}
	}

	_, pick, err := svc.Callers(ctx, "lib:Platform", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !pick.Ambiguous() || strings.Join(pick.Locations(), ",") != "do_darwin.go:5,do_linux.go:5" {
		t.Errorf("pick = %+v", pick)
	}
	_, pick, err = svc.Callers(ctx, "lib:Platform", "do_linux", 0)
	if err != nil || pick.Ambiguous() || pick.Target.File != "do_linux.go" {
		t.Errorf("path did not choose: %+v %v", pick, err)
	}
	_, pick, err = svc.Callers(ctx, "lib:Client.Send", "", 0)
	if err != nil || pick.Target.Qualified != "Client.Send" {
		t.Errorf("Owner.member fallback: %+v %v", pick.Target, err)
	}
}

// A links question pins every member at once, so the pool must hold the whole
// estate even when it is larger than the default slot count.
func TestEstateServicePoolHoldsTheWholeEstate(t *testing.T) {
	var members []EstateMember
	for i := range 9 {
		members = append(members, EstateMember{Name: fmt.Sprintf("m%d", i), Index: fmt.Sprintf("/estate/m%d.db", i)})
	}
	opener := func(context.Context, string) (*OpenedStore, error) {
		return &OpenedStore{Source: &fakeSource{run: readyRun(1, "abc", at(0))}, RunID: 1}, nil
	}
	svc := NewEstateService(members, opener, "")
	defer func() { _ = svc.Close() }()
	rep, err := svc.Links(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unjoined.UnavailableMembers) != 0 {
		t.Fatalf("members refused by a pool smaller than the estate: %v", rep.Unjoined.UnavailableMembers)
	}
}
