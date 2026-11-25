package multirepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/registry"
)

// The estate as a long-lived service: what `graphlume system` answers once per
// invocation, answered again and again for an agent over MCP.
//
// The CLI opens each member when a command starts and closes it when the
// command ends. A server cannot do that per question without paying an open
// per member per call, and it must not hold every member open for its whole
// life either, so members are read through a [HotStorePool]: opened on first
// use, read-only, re-probed for a newer committed run on every acquire, and
// closed when idle. The questions themselves are the same Federator calls the
// CLI makes, and the symbol a question is about is resolved by [PickSymbol],
// which the CLI uses too, so the two cannot come to answer differently.

// SymbolPickErrorKind classifies why a member:symbol argument could not be
// resolved, so a caller can tell a malformed question from a missing answer.
type SymbolPickErrorKind int

const (
	// PickMalformed: the argument is not member:symbol.
	PickMalformed SymbolPickErrorKind = iota + 1
	// PickUnknownMember: no member has that name.
	PickUnknownMember
	// PickMemberUnreadable: the member exists and its index could not be read.
	PickMemberUnreadable
	// PickNotFound: the member has no such symbol, or none under the path.
	PickNotFound
	// PickLookupFailed: reading the member's index failed.
	PickLookupFailed
)

// SymbolPickError is a refusal from [PickSymbol]. Its text is what the CLI
// prints, unchanged.
type SymbolPickError struct {
	Kind SymbolPickErrorKind
	msg  string
	err  error
}

func (e *SymbolPickError) Error() string { return e.msg }

// Unwrap exposes a storage failure's cause.
func (e *SymbolPickError) Unwrap() error { return e.err }

func pickErr(kind SymbolPickErrorKind, format string, a ...any) error {
	err := fmt.Errorf(format, a...)
	return &SymbolPickError{Kind: kind, msg: err.Error(), err: errors.Unwrap(err)}
}

// SymbolPick is the symbol a system question is about, and every definition
// that shared its name in that member.
type SymbolPick struct {
	Target SymbolRef
	// All are the definitions the name matched after the path filter, the
	// target first. More than one means the answer is about the first only.
	All []registry.SymbolHit
	// Name is the symbol as asked, without the member.
	Name string
	// LookupLimit is how many definitions were read; len(All) reaching it
	// means "at least that many".
	LookupLimit int
}

// Ambiguous reports whether several definitions shared the name.
func (p SymbolPick) Ambiguous() bool { return len(p.All) > 1 }

// Locations are the definitions as file:line, in the order they were read.
func (p SymbolPick) Locations() []string {
	out := make([]string, 0, len(p.All))
	for _, s := range p.All {
		out = append(out, fmt.Sprintf("%s:%d", s.FilePath, s.StartLine))
	}
	return out
}

// PickSymbol resolves member:symbol against that member's index with the
// conventions of the single-repository callers command: exact name first,
// Owner.member as a fallback, path to choose among several, and the first
// answered when the choice is left open. lookupLimit bounds the definitions
// read for one name.
func PickSymbol(ctx context.Context, members []Member, arg, path string, lookupLimit int) (SymbolPick, error) {
	memberName, name, ok := strings.Cut(arg, ":")
	memberName, name = strings.TrimSpace(memberName), strings.TrimSpace(name)
	if !ok || memberName == "" || name == "" {
		return SymbolPick{}, pickErr(PickMalformed, "%q is not <member>:<symbol> (for example: lib:Do)", arg)
	}
	var m *Member
	var names []string
	for i := range members {
		names = append(names, members[i].Name)
		if members[i].Name == memberName {
			m = &members[i]
		}
	}
	if m == nil {
		return SymbolPick{}, pickErr(PickUnknownMember, "no member is named %q; the members are %s", memberName, strings.Join(names, ", "))
	}
	if !m.Available() {
		return SymbolPick{}, pickErr(PickMemberUnreadable, "member %q cannot be read: %s", memberName, m.Unavailable)
	}
	src, ok := m.Source.(GoModuleSource)
	if !ok {
		return SymbolPick{}, pickErr(PickMemberUnreadable, "member %q has no index this command can read", memberName)
	}

	syms, err := LookupSymbol(ctx, src, m.RunID, name, lookupLimit)
	if err != nil {
		return SymbolPick{}, &SymbolPickError{Kind: PickLookupFailed, msg: err.Error(), err: err}
	}
	if len(syms) == 0 {
		return SymbolPick{}, pickErr(PickNotFound, "no symbol named %q in %s; names must match exactly", name, memberName)
	}
	if path != "" {
		kept := capability.BestPathMatches(syms, func(h registry.SymbolHit) string { return h.FilePath }, path)
		if len(kept) == 0 {
			return SymbolPick{}, pickErr(PickNotFound, "no symbol named %q in a file of %s matching %q; %d definition(s) exist elsewhere",
				name, memberName, path, len(syms))
		}
		syms = kept
	}
	t := syms[0]
	qualified := t.Name
	if rest, ok := strings.CutPrefix(t.FQN, t.FilePath+":"); ok && rest != "" {
		qualified = rest
	}
	return SymbolPick{
		Target: SymbolRef{
			Member: memberName, ID: t.ID, Name: t.Name, Qualified: qualified,
			Kind: t.Kind, File: t.FilePath, Line: t.StartLine,
		},
		All: syms, Name: name, LookupLimit: lookupLimit,
	}, nil
}

// LookupSymbol finds definitions by exact name, falling back to an
// Owner.member spelling (Client.Send) read against qualified names.
func LookupSymbol(ctx context.Context, src GoModuleSource, runID int64, name string, limit int) ([]registry.SymbolHit, error) {
	syms, err := src.FindSymbolsByName(ctx, runID, name, "", limit)
	if err != nil {
		return nil, fmt.Errorf("symbol lookup: %w", err)
	}
	if len(syms) > 0 {
		return syms, nil
	}
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return nil, nil
	}
	cands, err := src.FindSymbolsByName(ctx, runID, name[i+1:], "", limit)
	if err != nil {
		return nil, fmt.Errorf("symbol lookup: %w", err)
	}
	var out []registry.SymbolHit
	for _, c := range cands {
		if strings.HasSuffix(c.FQN, ":"+name) {
			out = append(out, c)
		}
	}
	return out, nil
}

// DescribeOpenFailure words a pool open failure for the person who has to fix
// it: which index, and the command that would repair it.
func DescribeOpenFailure(dbPath string, err error) string {
	var moe *MemberOpenError
	if errors.As(err, &moe) && moe.SchemaMismatch {
		return fmt.Sprintf("index at %s was written by a different schema than this build reads; "+
			"re-index that checkout with this build of graphlume", dbPath)
	}
	switch {
	case errors.Is(err, ErrNoCommittedRun):
		return fmt.Sprintf("%s holds no committed run, so it exposes no facts; run 'graphlume index' in that checkout", dbPath)
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("no index at %s; build one with 'graphlume index --project <that checkout>'", dbPath)
	case errors.Is(err, ErrPoolExhausted):
		return "too many members are open at once: " + err.Error()
	}
	return err.Error()
}

// DefaultEstateLookupLimit is how many definitions of one name an estate
// question reads, the same bound the single-repository commands use.
const DefaultEstateLookupLimit = 50

// EstateService answers links, callers and impact questions about a fixed
// estate, reading each member read-only through a pool. Safe for concurrent
// use; Close releases every member.
type EstateService struct {
	members []EstateMember
	pool    *HotStorePool
	version string
}

// NewEstateService builds the service over members, which the caller has
// already resolved to absolute index files with distinct names. opener opens
// one member read-only; runningVersion is this build, for the version
// warnings the report carries.
//
// The pool holds at least one slot per member: a links question pins every
// member at once, and a pool smaller than the estate would refuse it.
func NewEstateService(members []EstateMember, opener StoreOpener, runningVersion string) *EstateService {
	sorted := append([]EstateMember(nil), members...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	cfg := DefaultPoolConfig()
	cfg.MaxOpenStores = max(cfg.MaxOpenStores, len(sorted))
	return &EstateService{
		members: sorted,
		pool:    NewHotStorePool(cfg, opener),
		version: runningVersion,
	}
}

// Members lists the estate, sorted by name.
func (s *EstateService) Members() []EstateMember {
	return append([]EstateMember(nil), s.members...)
}

// Close closes every member index the service opened.
func (s *EstateService) Close() error { return s.pool.Close() }

// open acquires every member. A member that cannot be opened is returned as
// unavailable with the reason, never as an error: the rest still answer.
func (s *EstateService) open(ctx context.Context) ([]Member, func()) {
	out := make([]Member, 0, len(s.members))
	var handles []*PinnedHandle
	for _, em := range s.members {
		m := Member{Name: em.Name, IndexPath: em.Index}
		h, err := s.pool.Acquire(ctx, em.Name, em.Index)
		if err != nil {
			m.Unavailable = DescribeOpenFailure(em.Index, err)
		} else {
			handles = append(handles, h)
			m.Source, m.RunID = h.Source, h.RunID
		}
		out = append(out, m)
	}
	return out, func() {
		for _, h := range handles {
			h.Release()
		}
	}
}

// Unreadable maps each member that cannot be opened now to the reason, in
// the words DescribeOpenFailure uses.
func (s *EstateService) Unreadable(ctx context.Context) map[string]string {
	members, release := s.open(ctx)
	defer release()
	out := map[string]string{}
	for _, m := range members {
		if !m.Available() {
			out[m.Name] = m.Unavailable
		}
	}
	return out
}

func (s *EstateService) federator() (*Federator, error) {
	return New(WithRunningVersion(s.version))
}

// Links is the system report: HTTP route links between members, shared
// tables, and Go function and method links with their version warnings.
func (s *EstateService) Links(ctx context.Context) (*Report, error) {
	members, release := s.open(ctx)
	defer release()
	fed, err := s.federator()
	if err != nil {
		return nil, err
	}
	return fed.Build(ctx, members)
}

// Callers lists who calls member:symbol, in its own member and through Go
// module imports in the others, up to limit rows.
func (s *EstateService) Callers(ctx context.Context, symbol, path string, limit int) (*SystemCallersReport, SymbolPick, error) {
	members, release := s.open(ctx)
	defer release()
	pick, err := PickSymbol(ctx, members, symbol, path, DefaultEstateLookupLimit)
	if err != nil {
		return nil, SymbolPick{}, err
	}
	fed, err := s.federator()
	if err != nil {
		return nil, pick, err
	}
	rep, err := fed.SystemCallers(ctx, members, pick.Target, limit)
	return rep, pick, err
}

// Impact walks member:symbol's blast radius across the estate.
func (s *EstateService) Impact(ctx context.Context, symbol, path string, opts ImpactOptions) (*SystemImpactReport, SymbolPick, error) {
	members, release := s.open(ctx)
	defer release()
	pick, err := PickSymbol(ctx, members, symbol, path, DefaultEstateLookupLimit)
	if err != nil {
		return nil, SymbolPick{}, err
	}
	fed, err := s.federator()
	if err != nil {
		return nil, pick, err
	}
	rep, err := fed.SystemImpact(ctx, members, pick.Target, opts)
	return rep, pick, err
}
