package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// FuzzyHit is a definition whose name resembles the query, with how closely.
type FuzzyHit struct {
	SymbolHit
	// Score is in (0, 1]: 1 for the same name, lower for a prefix, a substring,
	// a subsequence or a name a typo or two away.
	Score float64
}

// SuggestionMinScore is the lowest score worth offering as "did you mean". An
// explicit fuzzy search keeps everything above fuzzyMinScore; a suggestion no
// one asked for must be one the reader would plausibly have meant.
const SuggestionMinScore = 0.75

// fuzzyMinScore is the lowest score a name keeps. Below it a name resembles
// the query only by chance: a handful of shared letters.
const fuzzyMinScore = 0.5

// fuzzyTrigramCap bounds the trigrams of the query used to fetch candidate
// names, and fuzzyNamesPerTrigram bounds the names each one brings. Together
// they keep the work flat however large the index is.
const (
	fuzzyTrigramCap      = 8
	fuzzyNamesPerTrigram = 3000
)

// FuzzySymbols finds definitions whose names resemble name: a prefix, a
// substring, the same letters in order, or a name a typo or two away. It is
// the "did you mean" behind an exact lookup that found nothing, and what
// `symbol --fuzzy` asks for. The exact lookup stays exact (FindSymbolsByName):
// fuzzy matching there would flood an agent's context with near-misses.
//
// Candidates are the names that share a three-letter run with the query, so a
// misspelling still finds the name it was meant for; they are ranked in
// memory and the best `limit` names' definitions are returned, best first.
func (r *Registry) FuzzySymbols(ctx context.Context, runID int64, name, kind string, limit int) ([]FuzzyHit, error) {
	if limit <= 0 {
		limit = 10
	}
	q := strings.ToLower(strings.TrimSpace(name))
	if q == "" {
		return nil, nil
	}

	names, err := r.fuzzyCandidateNames(ctx, runID, q, kind)
	if err != nil {
		return nil, err
	}
	type scored struct {
		name  string
		score float64
	}
	var ranked []scored
	for _, n := range names {
		if s := FuzzyScore(q, n); s >= fuzzyMinScore {
			ranked = append(ranked, scored{n, s})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].name < ranked[j].name
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	var out []FuzzyHit
	for _, c := range ranked {
		hits, err := r.FindSymbolsByName(ctx, runID, c.name, kind, 3)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			out = append(out, FuzzyHit{SymbolHit: h, Score: c.score})
		}
	}
	return out, nil
}

// fuzzyCandidateNames returns the distinct names that share a trigram with q.
// A query shorter than three letters has no trigram: it takes the names that
// start with it.
func (r *Registry) fuzzyCandidateNames(ctx context.Context, runID int64, q, kind string) ([]string, error) {
	seen := map[string]struct{}{}
	var names []string
	for _, p := range fuzzyPatterns(q) {
		got, err := r.namesLike(ctx, runID, p, kind)
		if err != nil {
			return nil, fmt.Errorf("registry: fuzzy candidates for %q: %w", q, err)
		}
		for _, n := range got {
			if _, dup := seen[n]; !dup {
				seen[n] = struct{}{}
				names = append(names, n)
			}
		}
	}
	return names, nil
}

// namesLike returns up to fuzzyNamesPerTrigram distinct definition names whose
// lower-cased form matches a LIKE pattern.
func (r *Registry) namesLike(ctx context.Context, runID int64, pattern, kind string) ([]string, error) {
	query := `SELECT DISTINCT s.name FROM symbols s
	           WHERE s.index_run_id = ? AND s.kind <> 'file' AND lower(s.name) LIKE ? ESCAPE '\'`
	args := []any{runID, pattern}
	if kind != "" {
		query += ` AND s.kind = ?`
		args = append(args, kind)
	}
	query += fmt.Sprintf(` LIMIT %d`, fuzzyNamesPerTrigram)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// fuzzyPatterns are the LIKE patterns that fetch candidates for q.
func fuzzyPatterns(q string) []string {
	runes := []rune(q)
	if len(runes) < 3 {
		return []string{likeEscape(q) + "%"}
	}
	seen := map[string]struct{}{}
	var out []string
	for i := 0; i+3 <= len(runes) && len(out) < fuzzyTrigramCap; i++ {
		tri := string(runes[i : i+3])
		if _, dup := seen[tri]; dup {
			continue
		}
		seen[tri] = struct{}{}
		out = append(out, "%"+likeEscape(tri)+"%")
	}
	return out
}

// likeEscape escapes the characters a LIKE pattern gives a meaning to.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// FuzzyScore says how closely name resembles query, from 0 (not at all) to 1
// (the same). Case is ignored. In falling order of score: the same name, a
// name the query starts, a name containing the query, a name holding the
// query's letters in order, and a name within a typo or two (one edit per four
// letters, at least one).
func FuzzyScore(query, name string) float64 {
	q := strings.ToLower(query)
	n := strings.ToLower(name)
	if q == "" || n == "" {
		return 0
	}
	if q == n {
		return 1
	}
	// A longer name is a weaker match for the same query: the query covers less
	// of it.
	cover := float64(len(q)) / float64(len(n))
	switch {
	case strings.HasPrefix(n, q):
		return 0.8 + 0.15*cover
	case strings.Contains(n, q):
		return 0.65 + 0.15*cover
	}
	best := 0.0
	// The same letters in order are a resemblance only where they fill most of
	// the name: in a long name almost any short query is a subsequence.
	if len(q) >= 4 && cover >= 0.5 && isSubsequence(q, n) {
		best = 0.5 + 0.15*cover
	}
	maxEdits := len([]rune(q)) / 4
	if maxEdits < 1 {
		maxEdits = 1
	}
	if d := editDistance([]rune(q), []rune(n)); d <= maxEdits {
		longer := len([]rune(n))
		if l := len([]rune(q)); l > longer {
			longer = l
		}
		if s := 0.9 * (1 - float64(d)/float64(longer)); s > best {
			best = s
		}
	}
	return best
}

// isSubsequence reports whether q's letters occur in n in order.
func isSubsequence(q, n string) bool {
	qr := []rune(q)
	i := 0
	for _, c := range n {
		if i < len(qr) && qr[i] == c {
			i++
		}
	}
	return i == len(qr)
}

// editDistance is the optimal-string-alignment distance: insertions, deletions,
// substitutions and the swap of two neighbouring letters, each one edit.
func editDistance(a, b []rune) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := prev2[j-2] + 1; t < v {
					v = t
				}
			}
			cur[j] = v
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
