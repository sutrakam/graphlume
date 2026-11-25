//go:build bench

// Command impactaccuracy measures how well impact predicts the files a
// real commit went on to change.
//
// It is behind the "bench" build tag so the default build, vet and lint never
// compile it. It uses the standard library only and drives a graphlume BINARY
// through its command line, so it can score any build, including a released
// one, without linking it.
//
//	go run -tags bench ./scripts/bench/impactaccuracy \
//	    -repo /path/to/throwaway/clone -bin /path/to/graphlume -n 100 -budget 20m
//
// # Method
//
// For each non-merge commit C, newest first, with parent P:
//
//  1. List the files C modified or deleted. A file C added does not exist at
//     P, so nothing indexed at P could name it; added files are left out of
//     both sides and counted.
//  2. Check out P, delete .graphlume, and run init, index and resolve. Every
//     commit is scored against a fresh full index of its own parent.
//  3. Keep the changed files the index holds in a language GraphLume binds
//     references for (the typed and resolved tiers of `graphlume languages`).
//     A commit with fewer than two is skipped, and so is one with more than
//     -max-files, which is a reformat or a rename rather than a change.
//  4. For each such file, the seed symbols are the innermost indexed code
//     definitions (see codeKinds) whose line range overlaps a hunk of the
//     diff, read at P.
//  5. Call impact once per seed symbol and confidence floor. The
//     predicted set is the files of the returned rows, minus the seed file.
//  6. The truth is the OTHER source files C changed. Count true positives,
//     false positives and false negatives.
//
// Three aggregations are reported, all micro-averaged: counts are summed over
// commits, then divided.
//
//   - One seed per commit, any file: the file with the most changed lines
//     among those that have a seed symbol (ties go to the lexically first
//     path).
//   - One seed per commit, not a test file: the same rule over the files that
//     are not tests. In a commit that fixes a function and extends its test,
//     the test often has more changed lines, and nothing depends on a test, so
//     the first rule asks a question whose right answer is the empty set. A
//     commit whose seed candidates are all tests is left out of this one and
//     counted.
//   - Every changed file as a seed: each file of the commit is scored in turn
//     against the rest.
//
// A test file is recognised by path alone, see isTestPath.
//
// # The baseline
//
// Every tally is printed beside a rule that needs no index at all: predict the
// other source files in the seed's own directory. It is scored on the same
// commits against the same truth, so whatever the truth's bias does to the
// figures it does to both. An impact analysis that cannot beat "the files next
// to it" on a history has told its reader nothing a directory listing would
// not.
//
// # Known bias, in both directions
//
// Files that change together are not all dependents of one another: a commit
// that edits a function and, separately, an unrelated constant counts the
// constant's file as "missed". And a commit does not touch everything that
// depends on a change: a caller that kept compiling is a correct prediction
// scored as a false positive. So recall and precision are both understated,
// against a truth that is partial on both sides. The figures compare builds of
// one tool on one history, and the tool with the baseline; they are not the
// probability that a reported file breaks.
//
// The tool answer is the agent-facing one, capped at 50 rows. Answers that hit
// the cap are counted in the report rather than hidden.
//
// # Safety
//
// The harness checks out revisions and deletes .graphlume in -repo. Point it
// at a clone made for the purpose. It refuses a working tree with uncommitted
// changes and restores the starting revision when it finishes.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The three claims an impact row can make, weakest last.
const (
	kindDirect   = "direct"
	kindIndirect = "indirect"
	kindDispatch = "dispatch"
)

// scopes are the row selections each floor is scored under.
var scopes = []string{"direct", "direct+indirect", "all"}

func inScope(scope, kind string) bool {
	switch scope {
	case "direct":
		return kind == kindDirect
	case "direct+indirect":
		return kind == kindDirect || kind == kindIndirect
	default:
		return true
	}
}

// testDirs are directory names that mark everything beneath them as test code.
var testDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true,
	"testdata": true, "e2e": true,
}

// isTestPath reports whether a path is test code by the common conventions: a
// directory named test, tests, __tests__, spec, specs, testdata or e2e, or a
// file named *_test.*, test_*.*, *.test.*, *.spec.*, conftest.py, or *Test,
// *Tests or *IT in a JVM or .NET language. It is a naming rule, not a reading
// of the file, and it is applied to seeds only: a test file still counts in
// the truth and in the predictions.
func isTestPath(p string) bool {
	parts := strings.Split(strings.ToLower(p), "/")
	for _, dir := range parts[:len(parts)-1] {
		if testDirs[dir] {
			return true
		}
	}
	base := parts[len(parts)-1]
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch {
	case strings.HasSuffix(stem, "_test"), strings.HasPrefix(stem, "test_"),
		strings.HasSuffix(stem, ".test"), strings.HasSuffix(stem, ".spec"),
		base == "conftest.py":
		return true
	}
	switch ext {
	case ".java", ".kt", ".scala", ".cs", ".groovy":
		orig := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		return strings.HasSuffix(orig, "Test") || strings.HasSuffix(orig, "Tests") || strings.HasSuffix(orig, "IT")
	}
	return false
}

type options struct {
	repo       string
	bin        string
	rev        string
	maxUsed    int
	maxScan    int
	budget     time.Duration
	maxFiles   int
	maxSymbols int
	tiers      []string
	out        string
	jsonOut    bool
	workers    int
}

// counts is one confusion tally over files.
type counts struct {
	Predicted int `json:"predicted"`
	TP        int `json:"tp"`
	FP        int `json:"fp"`
	FN        int `json:"fn"`
}

func (c *counts) add(o counts) {
	c.Predicted += o.Predicted
	c.TP += o.TP
	c.FP += o.FP
	c.FN += o.FN
}

func (c counts) precision() float64 { return ratio(c.TP, c.TP+c.FP) }
func (c counts) recall() float64    { return ratio(c.TP, c.TP+c.FN) }
func (c counts) f1() float64 {
	p, r := c.precision(), c.recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// score compares a predicted file set with the truth.
func score(predicted, truth map[string]bool) counts {
	c := counts{Predicted: len(predicted)}
	for p := range predicted {
		if truth[p] {
			c.TP++
		} else {
			c.FP++
		}
	}
	for t := range truth {
		if !predicted[t] {
			c.FN++
		}
	}
	return c
}

// hunk is a changed line range on the PARENT side of a diff. A pure insertion
// has Count 0 and sits after line Start.
type hunk struct {
	Start, Count int
	Added        int
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseHunks reads the hunk headers of a `git diff -U0` for one file.
func parseHunks(diff string) []hunk {
	var out []hunk
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		m := hunkHeader.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		h := hunk{Count: 1, Added: 1}
		h.Start, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			h.Count, _ = strconv.Atoi(m[2])
		}
		if m[4] != "" {
			h.Added, _ = strconv.Atoi(m[4])
		}
		out = append(out, h)
	}
	return out
}

func changedLines(hs []hunk) int {
	n := 0
	for _, h := range hs {
		n += h.Count + h.Added
	}
	return n
}

// symbol is one indexed definition at the parent revision.
type symbol struct {
	FQN       string `json:"fqn"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func (s symbol) touchedBy(h hunk) bool {
	if h.Count == 0 {
		// An insertion after line Start is inside the symbol only when the
		// symbol continues past that line.
		return s.StartLine <= h.Start && h.Start < s.EndLine
	}
	return s.StartLine <= h.Start+h.Count-1 && h.Start <= s.EndLine
}

func (s symbol) strictlyContains(o symbol) bool {
	return s.StartLine <= o.StartLine && o.EndLine <= s.EndLine &&
		(s.StartLine != o.StartLine || s.EndLine != o.EndLine)
}

// codeKinds are the symbol kinds that are definitions in source code. The index
// also records facts found inside a source file — a route, a table, a
// configuration key, an event — and those are not seeds: a route registered
// inside a function would otherwise be the "innermost symbol" of the lines
// that register it and displace the function.
var codeKinds = map[string]bool{
	"class": true, "interface": true, "function": true, "method": true,
	"field": true, "constant": true, "variable": true, "type_alias": true,
	"test": true,
}

// seedSymbols picks the innermost symbols each hunk touches. A changed method
// seeds the method, not also the class around it: the class's dependents are
// every user of the class, which is a different and far wider question.
// wholeFile seeds every symbol, for a file the commit deleted.
func seedSymbols(syms []symbol, hs []hunk, wholeFile bool) []string {
	seen := map[string]bool{}
	var out []string
	code := syms[:0:0]
	for _, s := range syms {
		if codeKinds[s.Kind] {
			code = append(code, s)
		}
	}
	syms = code
	keep := func(s symbol) {
		if s.FQN != "" && !seen[s.FQN] {
			seen[s.FQN] = true
			out = append(out, s.FQN)
		}
	}
	if wholeFile {
		for _, s := range syms {
			keep(s)
		}
		sort.Strings(out)
		return out
	}
	for _, h := range hs {
		var touched []symbol
		for _, s := range syms {
			if s.touchedBy(h) {
				touched = append(touched, s)
			}
		}
		for _, s := range touched {
			outer := false
			for _, o := range touched {
				if s.strictlyContains(o) {
					outer = true
					break
				}
			}
			if !outer {
				keep(s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// runner executes git and graphlume against one repository.
type runner struct {
	repo string
	bin  string
}

func (r runner) git(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", r.repo}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, lastLine(stderr.String()))
	}
	return stdout.String(), nil
}

func (r runner) graphlume(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.bin, append([]string{"--project", r.repo, "-q"}, args...)...)
	cmd.Dir = r.repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("graphlume %s: %w: %s", args[0], err, lastLine(stderr.String()+stdout.String()))
	}
	return stdout.Bytes(), nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// queryPage is the most rows `graphlume query` returns in one call.
const queryPage = 5000

// queryRows runs one SELECT through `graphlume query`, a page at a time, and
// decodes the rows. The statement must carry a total ORDER BY and no LIMIT.
func queryRows[T any](ctx context.Context, r runner, sql string) ([]T, error) {
	var all []T
	for offset := 0; ; offset += queryPage {
		paged := fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, queryPage, offset)
		out, err := r.graphlume(ctx, "--json", "query", paged, "--limit", strconv.Itoa(queryPage))
		if err != nil {
			return nil, err
		}
		var env struct {
			Rows []T `json:"rows"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			return nil, fmt.Errorf("decode query output: %w", err)
		}
		all = append(all, env.Rows...)
		if len(env.Rows) < queryPage {
			return all, nil
		}
	}
}

// fileRow is one indexed file and the language it was parsed as.
type fileRow struct {
	Path     string `json:"path"`
	Language string `json:"language"`
}

func (r runner) files(ctx context.Context) ([]fileRow, error) {
	return queryRows[fileRow](ctx, r, "SELECT path, language FROM files WHERE index_run_id = :run ORDER BY path")
}

// sourceLanguages asks the binary which languages it binds references for.
func (r runner) sourceLanguages(ctx context.Context) (map[string]bool, error) {
	out, err := r.graphlume(ctx, "--json", "languages")
	if err != nil {
		return nil, err
	}
	var tiers struct {
		Typed    []string `json:"typed"`
		Resolved []string `json:"resolved"`
	}
	if err := json.Unmarshal(out, &tiers); err != nil {
		return nil, fmt.Errorf("decode languages: %w", err)
	}
	langs := map[string]bool{}
	for _, l := range append(tiers.Typed, tiers.Resolved...) {
		langs[l] = true
	}
	if len(langs) == 0 {
		return nil, errors.New("the binary reports no language with cross-file references")
	}
	return langs, nil
}

// indexStats is what one fresh index of a revision produced.
type indexStats struct {
	Files         int     `json:"files"`
	Symbols       int     `json:"symbols"`
	Relationships int     `json:"relationships"`
	Seconds       float64 `json:"seconds"`
}

// reindex builds a fresh index of whatever is checked out.
func (r runner) reindex(ctx context.Context) (indexStats, error) {
	start := time.Now()
	if err := os.RemoveAll(filepath.Join(r.repo, ".graphlume")); err != nil {
		return indexStats{}, err
	}
	for _, step := range []string{"init", "index", "resolve"} {
		if _, err := r.graphlume(ctx, step); err != nil {
			return indexStats{}, err
		}
	}
	st := indexStats{Seconds: time.Since(start).Seconds()}
	out, err := r.graphlume(ctx, "--json", "status")
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return st, fmt.Errorf("decode status: %w", err)
	}
	st.Seconds = time.Since(start).Seconds()
	return st, nil
}

// impactRow is one row of an impact answer.
type impactRow struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// impactAnswer is one impact answer, reduced to what is scored.
//
// An answer can be short for two different reasons and they are counted
// apart. RowsDropped means the walk found more than the answer carried: the
// agent-facing answer is capped by row count and by size. WalkBounded means
// the walk itself stopped early, at its depth or node limit.
type impactAnswer struct {
	Rows        []impactRow
	RowsDropped bool
	WalkBounded bool
	Failed      bool
}

// impact calls impact for one symbol at one confidence floor.
func (r runner) impact(ctx context.Context, fqn, floor string) impactAnswer {
	args, _ := json.Marshal(map[string]string{"symbol": fqn, "min_confidence": floor})
	out, _ := r.graphlume(ctx, "--json", "tool", "call", "impact", "--args", string(args))
	var env struct {
		IsError bool `json:"is_error"`
		Result  struct {
			Items     []impactRow `json:"items"`
			Truncated bool        `json:"truncated"`
			Reason    string      `json:"truncated_reason"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.IsError {
		return impactAnswer{Failed: true}
	}
	a := impactAnswer{Rows: env.Result.Items}
	if env.Result.Truncated {
		// The answer's own wording: "showing 50 of 213 results" for the row
		// cap, "response exceeded the size limit" for the byte cap. Anything
		// else is a bound on the walk.
		// Several reasons are joined with "; ".
		for _, reason := range strings.Split(env.Result.Reason, "; ") {
			if strings.HasPrefix(reason, "showing ") || strings.Contains(reason, "size limit") {
				a.RowsDropped = true
			} else {
				a.WalkBounded = true
			}
		}
	}
	return a
}

// seedFile is one changed source file of a commit, as a candidate seed.
type seedFile struct {
	Path         string   `json:"path"`
	ChangedLines int      `json:"changed_lines"`
	Symbols      []string `json:"symbols"`
	Capped       bool     `json:"symbols_capped,omitempty"`
}

// commitResult is the per-commit record written to -out.
type commitResult struct {
	Commit      string     `json:"commit"`
	Parent      string     `json:"parent"`
	Skipped     string     `json:"skipped,omitempty"`
	SourceFiles []string   `json:"source_files,omitempty"`
	AddedFiles  int        `json:"added_files,omitempty"`
	OtherFiles  int        `json:"non_source_files,omitempty"`
	Index       indexStats `json:"index"`
	PrimarySeed string     `json:"primary_seed,omitempty"`
	CodeSeed    string     `json:"non_test_seed,omitempty"`
	Seeds       []seedFile `json:"seeds,omitempty"`
	// Baseline is the same-directory rule's score, keyed by seed path.
	Baseline map[string]counts `json:"baseline,omitempty"`
	// Scores is keyed by seed path, then floor, then scope.
	Scores      map[string]map[string]map[string]counts `json:"scores,omitempty"`
	Calls       int                                     `json:"tool_calls,omitempty"`
	RowsDropped int                                     `json:"answers_rows_dropped,omitempty"`
	WalkBounded int                                     `json:"answers_walk_bounded,omitempty"`
	Failed      int                                     `json:"answers_failed,omitempty"`
}

// Skip reasons. Each is counted separately in the report.
const (
	skipNoParent   = "no parent in this clone (root or shallow boundary)"
	skipFewChanged = "fewer than two files modified or deleted"
	skipFewSource  = "fewer than two changed source files in the parent index"
	skipTooMany    = "more changed source files than -max-files"
	skipNoSeed     = "no indexed symbol overlaps any changed line"
	skipIndexError = "index of the parent failed"
)

type changedFile struct {
	Status string
	Path   string
}

func (r runner) changedFiles(parent, commit string) ([]changedFile, error) {
	out, err := r.git("diff", "--no-renames", "--name-status", "-z", parent, commit)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	var files []changedFile
	for i := 0; i+1 < len(fields); i += 2 {
		files = append(files, changedFile{Status: fields[i], Path: fields[i+1]})
	}
	return files, nil
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// evaluate scores one commit. The repository is left checked out at parent.
func evaluate(ctx context.Context, r runner, opts options, langs map[string]bool, commit, parent string, changed []changedFile) commitResult {
	res := commitResult{Commit: commit, Parent: parent}

	if _, err := r.git("checkout", "-q", "--detach", "-f", parent); err != nil {
		res.Skipped = skipIndexError + ": " + err.Error()
		return res
	}
	st, err := r.reindex(ctx)
	res.Index = st
	if err != nil {
		res.Skipped = skipIndexError + ": " + err.Error()
		return res
	}

	fileRows, err := r.files(ctx)
	if err != nil {
		res.Skipped = skipIndexError + ": " + err.Error()
		return res
	}
	language := make(map[string]string, len(fileRows))
	for _, f := range fileRows {
		language[f.Path] = f.Language
	}

	deleted := map[string]bool{}
	for _, c := range changed {
		switch {
		case c.Status == "A":
			res.AddedFiles++
		case langs[language[c.Path]]:
			res.SourceFiles = append(res.SourceFiles, c.Path)
			deleted[c.Path] = c.Status == "D"
		default:
			res.OtherFiles++
		}
	}
	sort.Strings(res.SourceFiles)
	if len(res.SourceFiles) < 2 {
		res.Skipped = skipFewSource
		return res
	}
	if opts.maxFiles > 0 && len(res.SourceFiles) > opts.maxFiles {
		res.Skipped = skipTooMany
		return res
	}

	for _, path := range res.SourceFiles {
		q := "SELECT s.fqn, s.name, s.kind, s.start_line, s.end_line FROM symbols s " +
			"JOIN files f ON f.id = s.file_id WHERE s.index_run_id = :run AND f.path = " +
			sqlQuote(path) + " ORDER BY s.start_line, s.end_line, s.fqn, s.id"
		syms, err := queryRows[symbol](ctx, r, q)
		if err != nil {
			res.Skipped = skipIndexError + ": " + err.Error()
			return res
		}
		diff, err := r.git("diff", "--no-renames", "-U0", parent, commit, "--", path)
		if err != nil {
			res.Skipped = skipIndexError + ": " + err.Error()
			return res
		}
		hs := parseHunks(diff)
		sf := seedFile{Path: path, ChangedLines: changedLines(hs), Symbols: seedSymbols(syms, hs, deleted[path])}
		if opts.maxSymbols > 0 && len(sf.Symbols) > opts.maxSymbols {
			sf.Symbols = sf.Symbols[:opts.maxSymbols]
			sf.Capped = true
		}
		res.Seeds = append(res.Seeds, sf)
	}

	// The primary seed: most changed lines among files with a seed symbol.
	// The non-test seed is the same choice over files that are not tests.
	best, bestCode := -1, -1
	for i, sf := range res.Seeds {
		if len(sf.Symbols) == 0 {
			continue
		}
		if best < 0 || sf.ChangedLines > res.Seeds[best].ChangedLines {
			best = i
		}
		if !isTestPath(sf.Path) && (bestCode < 0 || sf.ChangedLines > res.Seeds[bestCode].ChangedLines) {
			bestCode = i
		}
	}
	if best < 0 {
		res.Skipped = skipNoSeed
		return res
	}
	res.PrimarySeed = res.Seeds[best].Path
	if bestCode >= 0 {
		res.CodeSeed = res.Seeds[bestCode].Path
	}

	// Every (symbol, floor) pair is asked once, a few at a time.
	type key struct{ fqn, floor string }
	answers := map[key]impactAnswer{}
	var keys []key
	for _, sf := range res.Seeds {
		for _, fqn := range sf.Symbols {
			for _, floor := range opts.tiers {
				k := key{fqn, floor}
				if _, dup := answers[k]; !dup {
					answers[k] = impactAnswer{}
					keys = append(keys, k)
				}
			}
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan key)
	for range max(opts.workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range work {
				a := r.impact(ctx, k.fqn, k.floor)
				mu.Lock()
				answers[k] = a
				mu.Unlock()
			}
		}()
	}
	for _, k := range keys {
		work <- k
	}
	close(work)
	wg.Wait()

	res.Calls = len(keys)
	for _, a := range answers {
		if a.RowsDropped {
			res.RowsDropped++
		}
		if a.WalkBounded {
			res.WalkBounded++
		}
		if a.Failed {
			res.Failed++
		}
	}

	// The source files of each directory, for the baseline.
	inDir := map[string][]string{}
	for _, f := range fileRows {
		if langs[f.Language] {
			dir := filepath.Dir(f.Path)
			inDir[dir] = append(inDir[dir], f.Path)
		}
	}

	res.Scores = map[string]map[string]map[string]counts{}
	res.Baseline = map[string]counts{}
	for _, sf := range res.Seeds {
		if len(sf.Symbols) == 0 {
			continue
		}
		truth := map[string]bool{}
		for _, p := range res.SourceFiles {
			if p != sf.Path {
				truth[p] = true
			}
		}
		neighbours := map[string]bool{}
		for _, p := range inDir[filepath.Dir(sf.Path)] {
			if p != sf.Path {
				neighbours[p] = true
			}
		}
		res.Baseline[sf.Path] = score(neighbours, truth)
		byFloor := map[string]map[string]counts{}
		for _, floor := range opts.tiers {
			byScope := map[string]counts{}
			for _, scope := range scopes {
				predicted := map[string]bool{}
				for _, fqn := range sf.Symbols {
					for _, row := range answers[key{fqn, floor}].Rows {
						if row.Path != "" && row.Path != sf.Path && inScope(scope, row.Kind) {
							predicted[row.Path] = true
						}
					}
				}
				byScope[scope] = score(predicted, truth)
			}
			byFloor[floor] = byScope
		}
		res.Scores[sf.Path] = byFloor
	}
	return res
}

// report is the summary printed at the end and, with -json, emitted whole.
type report struct {
	Repo          string         `json:"repo"`
	StartRevision string         `json:"start_revision"`
	Binary        string         `json:"binary_version"`
	Scanned       int            `json:"commits_scanned"`
	Used          int            `json:"commits_used"`
	Skipped       map[string]int `json:"commits_skipped"`
	StoppedBy     string         `json:"stopped_by"`
	Seconds       float64        `json:"seconds"`
	IndexSeconds  float64        `json:"index_seconds"`
	Indexed       int            `json:"parents_indexed"`
	Head          indexStats     `json:"start_revision_index"`
	ToolCalls     int            `json:"tool_calls"`
	RowsDropped   int            `json:"answers_rows_dropped"`
	WalkBounded   int            `json:"answers_walk_bounded"`
	Failed        int            `json:"answers_failed"`
	SeedPairs     int            `json:"seed_files_scored"`
	SeedsCapped   int            `json:"seed_files_capped_at_max_symbols"`
	CodeSeeded    int            `json:"commits_with_a_non_test_seed"`
	TestPrimary   int            `json:"commits_whose_any_file_seed_is_a_test"`
	EmptyPrimary  map[string]int `json:"commits_with_empty_prediction_any_file_seed"`
	EmptyCode     map[string]int `json:"commits_with_empty_prediction_non_test_seed"`
	// The same-directory baseline under each of the three aggregations.
	BaselinePrimary counts `json:"baseline_one_seed_per_commit_any_file"`
	BaselineCode    counts `json:"baseline_one_seed_per_commit_non_test"`
	BaselineEvery   counts `json:"baseline_every_changed_file_as_seed"`
	// The three tallies are keyed by floor, then scope.
	Primary   map[string]map[string]counts `json:"one_seed_per_commit_any_file"`
	CodeSeed  map[string]map[string]counts `json:"one_seed_per_commit_non_test"`
	EverySeed map[string]map[string]counts `json:"every_changed_file_as_seed"`
}

func newTally(tiers []string) map[string]map[string]counts {
	t := map[string]map[string]counts{}
	for _, floor := range tiers {
		t[floor] = map[string]counts{}
		for _, scope := range scopes {
			t[floor][scope] = counts{}
		}
	}
	return t
}

func addTally(dst, src map[string]map[string]counts) {
	for floor, byScope := range src {
		for scope, c := range byScope {
			sum := dst[floor][scope]
			sum.add(c)
			dst[floor][scope] = sum
		}
	}
}

func run(opts options) (report, error) {
	ctx := context.Background()
	r := runner{repo: opts.repo, bin: opts.bin}
	rep := report{
		Repo: filepath.Base(opts.repo), Skipped: map[string]int{},
		EmptyPrimary: map[string]int{}, EmptyCode: map[string]int{},
	}
	rep.Primary, rep.CodeSeed, rep.EverySeed = newTally(opts.tiers), newTally(opts.tiers), newTally(opts.tiers)

	top, err := r.git("rev-parse", "--show-toplevel")
	if err != nil {
		return rep, err
	}
	want, _ := filepath.EvalSymlinks(opts.repo)
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(top)); got != want {
		return rep, fmt.Errorf("-repo must be the root of a git working tree, got %s inside %s", opts.repo, strings.TrimSpace(top))
	}
	dirty, err := r.git("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return rep, err
	}
	if strings.TrimSpace(dirty) != "" {
		return rep, errors.New("-repo has uncommitted changes; this harness checks out other revisions, so give it a clone made for the purpose")
	}

	// Where to return to: the branch if there is one, else the commit.
	origin, err := r.git("symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		if origin, err = r.git("rev-parse", "HEAD"); err != nil {
			return rep, err
		}
	}
	origin = strings.TrimSpace(origin)
	restore := func() {
		if _, err := r.git("checkout", "-q", "-f", origin); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not restore", origin+":", err)
		}
	}
	defer restore()
	// An interrupted run must not leave the clone on a detached parent: the
	// next run would walk back from there instead of from the branch.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupted
		restore()
		os.Exit(130)
	}()

	start, err := r.git("rev-parse", opts.rev)
	if err != nil {
		return rep, err
	}
	rep.StartRevision = strings.TrimSpace(start)
	if v, err := exec.Command(opts.bin, "version").Output(); err == nil {
		rep.Binary = strings.TrimSpace(string(v))
	}
	langs, err := r.sourceLanguages(ctx)
	if err != nil {
		return rep, err
	}

	// Index the starting revision once: it gives the repository's size for the
	// report, and the file extensions that are source, so that a commit which
	// plainly changed fewer than two source files is skipped without paying
	// for an index of its parent.
	if _, err := r.git("checkout", "-q", "--detach", "-f", rep.StartRevision); err != nil {
		return rep, err
	}
	if rep.Head, err = r.reindex(ctx); err != nil {
		return rep, fmt.Errorf("index the starting revision: %w", err)
	}
	headFiles, err := r.files(ctx)
	if err != nil {
		return rep, err
	}
	sourceExt := map[string]bool{}
	for _, f := range headFiles {
		if langs[f.Language] {
			sourceExt[filepath.Ext(f.Path)] = true
		}
	}

	revArgs := []string{"rev-list", "--no-merges"}
	if opts.maxScan > 0 {
		revArgs = append(revArgs, "--max-count", strconv.Itoa(opts.maxScan))
	}
	list, err := r.git(append(revArgs, rep.StartRevision)...)
	if err != nil {
		return rep, err
	}
	commits := strings.Fields(list)

	var sink *json.Encoder
	if opts.out != "" {
		f, err := os.Create(opts.out)
		if err != nil {
			return rep, err
		}
		// A failed close is a truncated results file, so it is said out loud.
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "warning: closing", opts.out+":", err)
			}
		}()
		sink = json.NewEncoder(f)
	}

	began := time.Now()
	rep.StoppedBy = "history exhausted"
	for _, commit := range commits {
		if rep.Used >= opts.maxUsed {
			rep.StoppedBy = fmt.Sprintf("-n %d reached", opts.maxUsed)
			break
		}
		if opts.budget > 0 && time.Since(began) > opts.budget {
			rep.StoppedBy = fmt.Sprintf("-budget %s reached", opts.budget)
			break
		}
		rep.Scanned++

		res := commitResult{Commit: commit}
		parentOut, err := r.git("rev-parse", "--verify", "-q", commit+"^")
		parent := strings.TrimSpace(parentOut)
		switch {
		case err != nil || parent == "":
			res.Skipped = skipNoParent
		default:
			res.Parent = parent
			changed, err := r.changedFiles(parent, commit)
			if err != nil {
				return rep, err
			}
			existing, likelySource := 0, 0
			for _, c := range changed {
				if c.Status != "A" {
					existing++
					if sourceExt[filepath.Ext(c.Path)] {
						likelySource++
					}
				}
			}
			switch {
			case existing < 2:
				res.Skipped = skipFewChanged
			case likelySource < 2:
				res.Skipped = skipFewSource
			default:
				res = evaluate(ctx, r, opts, langs, commit, parent, changed)
				if res.Index.Seconds > 0 {
					rep.Indexed++
					rep.IndexSeconds += res.Index.Seconds
				}
			}
		}

		if sink != nil {
			if err := sink.Encode(res); err != nil {
				return rep, err
			}
		}
		if res.Skipped != "" {
			reason, _, _ := strings.Cut(res.Skipped, ": ")
			rep.Skipped[reason]++
			continue
		}
		rep.Used++
		rep.ToolCalls += res.Calls
		rep.RowsDropped += res.RowsDropped
		rep.WalkBounded += res.WalkBounded
		rep.Failed += res.Failed
		addTally(rep.Primary, res.Scores[res.PrimarySeed])
		rep.BaselinePrimary.add(res.Baseline[res.PrimarySeed])
		if isTestPath(res.PrimarySeed) {
			rep.TestPrimary++
		}
		if res.CodeSeed != "" {
			rep.CodeSeeded++
			addTally(rep.CodeSeed, res.Scores[res.CodeSeed])
			rep.BaselineCode.add(res.Baseline[res.CodeSeed])
		}
		for _, floor := range opts.tiers {
			if res.Scores[res.PrimarySeed][floor]["all"].Predicted == 0 {
				rep.EmptyPrimary[floor]++
			}
			if res.CodeSeed != "" && res.Scores[res.CodeSeed][floor]["all"].Predicted == 0 {
				rep.EmptyCode[floor]++
			}
		}
		for seed, byFloor := range res.Scores {
			rep.SeedPairs++
			addTally(rep.EverySeed, byFloor)
			rep.BaselineEvery.add(res.Baseline[seed])
		}
		for _, sf := range res.Seeds {
			if sf.Capped {
				rep.SeedsCapped++
			}
		}
		fmt.Fprintf(os.Stderr, "%s used=%d scanned=%d files=%d index=%.1fs elapsed=%s\n",
			commit[:10], rep.Used, rep.Scanned, len(res.SourceFiles), res.Index.Seconds,
			time.Since(began).Round(time.Second))
	}
	rep.Seconds = time.Since(began).Seconds()
	return rep, nil
}

func printTally(title string, tiers []string, t map[string]map[string]counts, baseline counts) {
	row := func(floor, scope string, c counts) {
		fmt.Printf("  %-8s %-16s %9d %6d %6d %6d %9.1f%% %7.1f%% %6.3f\n",
			floor, scope, c.Predicted, c.TP, c.FP, c.FN,
			100*c.precision(), 100*c.recall(), c.f1())
	}
	fmt.Println()
	fmt.Println(title)
	fmt.Printf("  %-8s %-16s %9s %6s %6s %6s %10s %8s %6s\n",
		"FLOOR", "ROWS", "PREDICTED", "TP", "FP", "FN", "PRECISION", "RECALL", "F1")
	for _, floor := range tiers {
		for _, scope := range scopes {
			row(floor, scope, t[floor][scope])
		}
	}
	row("baseline", "same directory", baseline)
}

func printReport(rep report, opts options) {
	fmt.Printf("Impact accuracy: %s at %.12s\n", rep.Repo, rep.StartRevision)
	fmt.Printf("  Binary:    %s\n", rep.Binary)
	fmt.Printf("  Index of the starting revision: %d files, %d symbols, %d relationships, %.1fs\n",
		rep.Head.Files, rep.Head.Symbols, rep.Head.Relationships, rep.Head.Seconds)
	fmt.Printf("  Commits:   %d used of %d scanned (%s)\n", rep.Used, rep.Scanned, rep.StoppedBy)
	reasons := make([]string, 0, len(rep.Skipped))
	for reason := range rep.Skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Printf("    skipped %4d  %s\n", rep.Skipped[reason], reason)
	}
	fmt.Printf("  Time:      %.0fs in total, %.0fs of it indexing %d parent revisions\n",
		rep.Seconds, rep.IndexSeconds, rep.Indexed)
	fmt.Printf("  Tool:      %d impact calls; %d answers dropped rows at the answer cap, %d stopped at a walk limit, %d failed\n",
		rep.ToolCalls, rep.RowsDropped, rep.WalkBounded, rep.Failed)
	fmt.Printf("  Seeds:     the any-file seed is a test file in %d of %d commits; %d commits have a non-test seed\n",
		rep.TestPrimary, rep.Used, rep.CodeSeeded)
	fmt.Printf("             %d seed files scored in all, %d of them cut to -max-symbols %d\n",
		rep.SeedPairs, rep.SeedsCapped, opts.maxSymbols)
	for _, floor := range opts.tiers {
		fmt.Printf("  Empty:     at floor %s, no file was predicted for %d of %d any-file seeds and %d of %d non-test seeds\n",
			floor, rep.EmptyPrimary[floor], rep.Used, rep.EmptyCode[floor], rep.CodeSeeded)
	}
	printTally(fmt.Sprintf("ONE SEED PER COMMIT, ANY FILE (%d commits)", rep.Used), opts.tiers, rep.Primary, rep.BaselinePrimary)
	printTally(fmt.Sprintf("ONE SEED PER COMMIT, NOT A TEST FILE (%d commits)", rep.CodeSeeded), opts.tiers, rep.CodeSeed, rep.BaselineCode)
	printTally(fmt.Sprintf("EVERY CHANGED FILE AS A SEED (%d seed files)", rep.SeedPairs), opts.tiers, rep.EverySeed, rep.BaselineEvery)
}

func main() {
	var opts options
	var tiers string
	flag.StringVar(&opts.repo, "repo", "", "root of a throwaway clone; the harness checks out revisions in it (required)")
	flag.StringVar(&opts.bin, "bin", "graphlume", "the graphlume binary to measure")
	flag.StringVar(&opts.rev, "rev", "HEAD", "revision to walk back from")
	flag.IntVar(&opts.maxUsed, "n", 100, "stop after this many commits have been scored")
	flag.IntVar(&opts.maxScan, "scan", 0, "examine at most this many commits (0 = all reachable)")
	flag.DurationVar(&opts.budget, "budget", 20*time.Minute, "start no new commit after this long (0 = no limit)")
	flag.IntVar(&opts.maxFiles, "max-files", 50, "skip a commit that changed more source files than this (0 = no limit)")
	flag.IntVar(&opts.maxSymbols, "max-symbols", 100, "ask about at most this many seed symbols per file (0 = no limit)")
	flag.StringVar(&tiers, "tiers", "high,medium,low", "confidence floors to score, comma separated")
	flag.StringVar(&opts.out, "out", "", "write one JSON line per commit examined to this file")
	flag.BoolVar(&opts.jsonOut, "json", false, "print the summary as JSON")
	flag.IntVar(&opts.workers, "workers", 4, "impact calls to run at once")
	flag.Parse()

	if opts.repo == "" {
		fmt.Fprintln(os.Stderr, "impactaccuracy: -repo is required")
		flag.Usage()
		os.Exit(2)
	}
	abs, err := filepath.Abs(opts.repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "impactaccuracy:", err)
		os.Exit(1)
	}
	opts.repo = abs
	if strings.ContainsRune(opts.bin, filepath.Separator) {
		if opts.bin, err = filepath.Abs(opts.bin); err != nil {
			fmt.Fprintln(os.Stderr, "impactaccuracy:", err)
			os.Exit(1)
		}
	}
	for _, t := range strings.Split(tiers, ",") {
		if t = strings.TrimSpace(t); t != "" {
			opts.tiers = append(opts.tiers, t)
		}
	}

	rep, err := run(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "impactaccuracy:", err)
		os.Exit(1)
	}
	if opts.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}
	printReport(rep, opts)
}
