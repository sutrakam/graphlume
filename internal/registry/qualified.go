package registry

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Qualified is a name written as an owner and a member: `Guard.AgainstNull`,
// `Guard::AgainstNull`, `Mailer#deliver`, `Serilog.Guard.AgainstNull`.
//
// A caller types this form because a bare `AgainstNull` is shared by many
// definitions, and the form tells them apart: the owner is part of each
// definition's qualified name (the text after the file in its FQN).
type Qualified struct {
	// Segments are the parts of the name, owner first, member last.
	Segments []string
}

// ParseQualified reads name as an owner and a member. `.`, `::` and `#` all
// separate segments, because languages disagree on which one means "member of".
// It reports false for a name with fewer than two segments or an empty one.
func ParseQualified(name string) (Qualified, bool) {
	n := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), "::", "."), "#", ".")
	segs := strings.Split(n, ".")
	if len(segs) < 2 {
		return Qualified{}, false
	}
	for _, s := range segs {
		if strings.TrimSpace(s) == "" || strings.ContainsAny(s, " \t/\\:") {
			return Qualified{}, false
		}
	}
	return Qualified{Segments: segs}, true
}

// Member is the last segment: the name the definition itself carries.
func (q Qualified) Member() string { return q.Segments[len(q.Segments)-1] }

// Forms lists the qualified spellings to look for, longest first. A definition's
// FQN holds its owners but not the namespace around them (`Guard.AgainstNull`,
// not `Serilog.Guard.AgainstNull`), so the leading segments are dropped one at a
// time down to the owner alone.
func (q Qualified) Forms() []string {
	var out []string
	for i := 0; i < len(q.Segments)-1; i++ {
		out = append(out, strings.Join(q.Segments[i:], "."))
	}
	return out
}

// MatchesFQN reports whether an FQN of the form `path:Owner.member` ends in
// form at a segment boundary.
func MatchesFQN(fqn, form string) bool {
	rest := fqn
	if i := strings.Index(fqn, ":"); i >= 0 {
		rest = fqn[i+1:]
	}
	return rest == form || strings.HasSuffix(rest, "."+form)
}

// SymbolCandidatesQualified is SymbolCandidates for a name written as an owner
// and a member. It tries the longest spelling first and answers with the first
// that matches anything, so `Serilog.Guard.AgainstNull` finds Guard's member
// when the index does not hold the namespace.
//
// Nothing is returned when name is not in that form, or when no definition of
// the member has the owner in its qualified name.
func (r *Registry) SymbolCandidatesQualified(ctx context.Context, runID int64, name string, limit int) ([]SymbolCandidate, int, error) {
	q, ok := ParseQualified(name)
	if !ok {
		return nil, 0, nil
	}
	for _, form := range q.Forms() {
		n := utf8.RuneCountInString(form) + 1
		where := `s.index_run_id = ? AND s.name = ? AND (s.fqn = ? OR substr(s.fqn, -?) = ? OR substr(s.fqn, -?) = ?)`
		args := []any{runID, q.Member(), form, n, ":" + form, n, "." + form}
		out, total, err := r.symbolCandidatesWhere(ctx, name, where, args, limit)
		if err != nil || total > 0 {
			return out, total, err
		}
	}
	return nil, 0, nil
}
