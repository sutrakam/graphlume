package missed

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/sutrakam/graphlume/internal/analyze/api"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/tests"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// BlindspotKind classifies the category of hidden or unresolved debt.
type BlindspotKind string

const (
	BlindspotUnresolvedCall    BlindspotKind = "unresolved_call"
	BlindspotUntestedCritical  BlindspotKind = "untested_critical"
	BlindspotUnmappedEndpoint  BlindspotKind = "unmapped_endpoint"
	BlindspotOrphanTable       BlindspotKind = "orphan_table"
	BlindspotMissingConfigDecl BlindspotKind = "missing_config"
	BlindspotUnparsedFile      BlindspotKind = "unparsed_file"
	// BlindspotRuntimeRoute is a route registered at run time: from a loop
	// over a table that is not a literal, or with a path built from a value no
	// call states. Its served paths are not in the index.
	BlindspotRuntimeRoute BlindspotKind = "runtime_route"
)

// Blindspot represents a single actionable gap in code intelligence or test safety.
type Blindspot struct {
	Kind        BlindspotKind `json:"kind"`
	Severity    string        `json:"severity"` // "high", "medium", "low"
	Target      string        `json:"target"`
	Path        string        `json:"path,omitempty"`
	Line        int           `json:"line,omitempty"`
	Description string        `json:"description"`
	Remediation string        `json:"remediation"`
}

// Options bounds the blindspot detection scan.
type Options struct {
	MaxItemsPerKind int
}

// DefaultOptions returns standard blindspot detection options.
func DefaultOptions() Options {
	return Options{
		MaxItemsPerKind: 50,
	}
}

// Report aggregates all detected blindspots across the repository.
type Report struct {
	RunID           int64                 `json:"run_id"`
	TotalBlindspots int                   `json:"total_blindspots"`
	ByKind          map[BlindspotKind]int `json:"by_kind"`
	// Capped lists the kinds whose search stopped at MaxItemsPerKind. Their
	// counts, and so TotalBlindspots, are lower bounds: "50" there means
	// "at least 50", and reporting it as the number found misled a reader of
	// a real 26,000-file repository into a total of 235.
	Capped       []BlindspotKind         `json:"capped_kinds,omitempty"`
	Blindspots   []Blindspot             `json:"blindspots"`
	Completeness capability.Completeness `json:"completeness"`
}

// unparsedBlindspots turns the files a run could not fully read into findings,
// worst first, at most limit of them.
func unparsedBlindspots(files []model.File, limit int) []Blindspot {
	type ranked struct {
		rank int
		b    Blindspot
	}
	var found []ranked
	for _, f := range files {
		reason := f.ParseReason()
		b := Blindspot{Kind: BlindspotUnparsedFile, Target: f.Path, Path: f.Path}
		switch {
		case f.ParseStatus == model.ParseStatusFailed:
			b.Severity = "high"
			b.Description = "Nothing was extracted from this file: " + orUnknown(f.ParseError)
			b.Remediation = "Read the recorded error. Definitions in this file and every edge through it are missing from answers."
			found = append(found, ranked{0, b})
		case f.ParseIncomplete():
			b.Severity = "high"
			b.Description = "The parse of this file was stopped before its end: " + orUnknown(f.ParseError)
			b.Remediation = "Definitions after the stop are missing. A file stopped on a limit can be split, or excluded if it is generated."
			found = append(found, ranked{1, b})
		case reason == model.ParseReasonTooLarge:
			b.Severity = "medium"
			b.Description = "This file is over the size limit and is not in the index: " + orUnknown(f.ParseError)
			b.Remediation = "Raise index.max_file_bytes in .graphlume.yml to include it, or leave it out if it is generated."
			found = append(found, ranked{2, b})
		case reason == model.ParseReasonSyntaxErrors:
			b.Severity = "low"
			b.Description = "The parser read this file to the end and reported " + orUnknown(f.ParseError)
			b.Remediation = "Usually a construct the grammar does not model (a macro, a preprocessor branch). " +
				"Check the reported line if a definition near it cannot be found."
			found = append(found, ranked{3, b})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].rank != found[j].rank {
			return found[i].rank < found[j].rank
		}
		return found[i].b.Path < found[j].b.Path
	})
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	out := make([]Blindspot, 0, len(found))
	for _, r := range found {
		out = append(out, r.b)
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "no reason was recorded"
	}
	return s
}

// Detector discovers code intelligence gaps and untracked dependencies.
type Detector struct {
	reg  *registry.Registry
	caps capability.Deps
}

// NewDetector constructs a blindspot detector.
func NewDetector(reg *registry.Registry, caps capability.Deps) *Detector {
	return &Detector{
		reg:  reg,
		caps: caps,
	}
}

// isShellBuiltinOrCommand names the shell builtins and system utilities a
// shell script calls that no repository defines.
var isShellBuiltinOrCommand = map[string]bool{
	"echo": true, "cd": true, "exit": true, "set": true, "cp": true, "mv": true,
	"rm": true, "mkdir": true, "git": true, "docker": true, "tar": true, "curl": true,
	"chmod": true, "chown": true, "cat": true, "grep": true, "sed": true, "awk": true,
	"which": true, "export": true, "source": true, "printf": true, "read": true,
	"shift": true, "test": true, "true": true, "false": true, "kill": true, "sleep": true,
	"wait": true, "exec": true, "pwd": true, "find": true, "xargs": true, "ssh": true,
	"ls": true, "ln": true, "head": true, "tail": true, "cut": true, "sort": true,
	"uniq": true, "wc": true, "tee": true, "env": true, "sh": true, "bash": true,
}

// warmer is the optional half of capability.Graph that loads what traversals
// read ahead of them. graph.Graph implements it.
type warmer interface {
	Warm(ctx context.Context) error
}

// Detect gathers blindspots for the specified index run.
//
// The six categories read different tables and share nothing, so they are
// gathered at once and reported in their fixed order: the audit takes as long
// as its slowest category, not as long as all of them. On a 26,000-file
// repository they were 10 s in sequence.
func (d *Detector) Detect(ctx context.Context, runID int64, opts Options) (*Report, error) {
	if opts.MaxItemsPerKind <= 0 {
		opts.MaxItemsPerKind = 50
	}

	var (
		wg       sync.WaitGroup
		stats    registry.ResolutionStats
		symbols  int
		files    []model.File
		sections [6][]Blindspot // unresolved, endpoints, tables, config, untested, runtime routes
	)
	goDo := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	goDo(func() { stats, _ = d.reg.ResolutionStats(ctx, runID) })
	goDo(func() {
		// The number of definitions, which the symbol table the probes load
		// already holds; counting them in SQL reads every row of the table.
		if d.caps.Validate() == nil {
			if idx, err := d.caps.SymbolIndex(ctx, runID); err == nil {
				symbols = idx.Len()
				return
			}
		}
		counts, _ := d.reg.Counts(ctx, runID)
		symbols = int(counts.Symbols)
	})
	goDo(func() { files, _ = d.reg.Files(ctx, runID) })
	goDo(func() { sections[0] = d.unresolvedCalls(ctx, runID, opts) })
	goDo(func() { sections[1] = d.unmappedEndpoints(ctx, runID, opts) })
	goDo(func() { sections[2] = d.orphanTables(ctx, runID, opts) })
	goDo(func() { sections[3] = d.missingConfig(ctx, runID, opts) })
	goDo(func() { sections[4] = d.untestedCritical(ctx, runID, opts) })
	goDo(func() { sections[5] = d.runtimeRoutes(ctx, runID, opts) })
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rep := &Report{
		RunID:        runID,
		ByKind:       make(map[BlindspotKind]int),
		Blindspots:   []Blindspot{},
		Completeness: capability.NewCompleteness(stats, symbols, false, ""),
	}
	add := func(bs []Blindspot) {
		for _, b := range bs {
			rep.Blindspots = append(rep.Blindspots, b)
			rep.ByKind[b.Kind]++
		}
	}
	add(sections[0])
	add(sections[1])
	add(sections[2])
	add(sections[3])
	add(sections[5])
	// 5. Files the index does not fully hold
	//
	// Worst first, so the per-kind cap drops the least serious findings and
	// never the most: a file nothing was extracted from, then a file whose
	// parse was cut short, then a file over the size limit, then a file the
	// parser read to the end with syntax errors.
	//
	// The other skips are deliberate decisions and are not blindspots: no
	// grammar claims the file (go.mod, LICENSE), discovery refused it (binary,
	// symlink), or its grammar has no definition query and it is indexed as
	// text. Reporting those made every such file a high-severity finding.
	add(unparsedBlindspots(files, opts.MaxItemsPerKind))
	add(sections[4])

	rep.TotalBlindspots = len(rep.Blindspots)
	for kind, n := range rep.ByKind {
		if n >= opts.MaxItemsPerKind {
			rep.Capped = append(rep.Capped, kind)
		}
	}
	sort.Slice(rep.Capped, func(i, j int) bool { return rep.Capped[i] < rep.Capped[j] })
	return rep, nil
}

// 1. Unresolved Call Sites
func (d *Detector) unresolvedCalls(ctx context.Context, runID int64, opts Options) []Blindspot {
	unresolved, err := d.reg.UnresolvedReferences(ctx, runID, opts.MaxItemsPerKind)
	if err != nil {
		return nil
	}
	var out []Blindspot
	for _, u := range unresolved {
		isShell := strings.HasSuffix(u.Path, ".sh") || strings.HasSuffix(u.Path, ".bash") || strings.HasSuffix(u.Path, ".zsh")
		if isShell && isShellBuiltinOrCommand[u.Name] {
			continue // Skip standard shell builtins and system utilities
		}
		desc := fmt.Sprintf("Call to %q could not be bound to any definition", u.Name)
		if u.Receiver != "" {
			desc = fmt.Sprintf("Call to %s.%s could not be bound", u.Receiver, u.Name)
		}
		remediation := "Verify import paths or install SCIP indexer for full external library binding."
		if isShell {
			remediation = "External shell binary or script; verify execution path or local script definition."
		}
		out = append(out, Blindspot{
			Kind:        BlindspotUnresolvedCall,
			Severity:    "medium",
			Target:      u.Name,
			Path:        u.Path,
			Line:        u.Line,
			Description: desc,
			Remediation: remediation,
		})
	}
	return out
}

// 2. Unmapped Endpoints
func (d *Detector) unmappedEndpoints(ctx context.Context, runID int64, opts Options) []Blindspot {
	endpoints, err := d.reg.EndpointReport(ctx, runID)
	if err != nil {
		return nil
	}
	var out []Blindspot
	for _, ep := range endpoints {
		if len(out) >= opts.MaxItemsPerKind {
			break
		}
		if ep.Handler == "" {
			out = append(out, Blindspot{
				Kind:        BlindspotUnmappedEndpoint,
				Severity:    "high",
				Target:      ep.Signature,
				Path:        ep.FilePath,
				Line:        ep.Line,
				Description: fmt.Sprintf("HTTP endpoint %q has no resolved handler symbol", ep.Signature),
				Remediation: "Ensure handler function is defined and exported in the enclosing package.",
			})
		}
	}
	return out
}

// 2b. Routes registered at run time
//
// A route registered from a loop over a table built at run time, or with a
// path built from a value no call states, is indexed once with its path as
// written ("POST {base}/export"): the paths it is served at are not known.
// Each is reported with how many there are, so "no endpoint matches" a URL
// in such a service reads as a gap and not as an absence.
func (d *Detector) runtimeRoutes(ctx context.Context, runID int64, opts Options) []Blindspot {
	routes, err := d.reg.RuntimeRoutes(ctx, runID, api.RuntimeRouteDoc)
	if err != nil || len(routes) == 0 {
		return nil
	}
	var out []Blindspot
	for _, r := range routes {
		if len(out) >= opts.MaxItemsPerKind {
			break
		}
		out = append(out, Blindspot{
			Kind:     BlindspotRuntimeRoute,
			Severity: "medium",
			Target:   r.Name,
			Path:     r.Path,
			Line:     r.Line,
			Description: fmt.Sprintf("Route %q is registered at run time (one of %d routes registered at run time): "+
				"its path is built from values the source does not state, so the paths it is served at are not indexed", r.Name, len(routes)),
			Remediation: "Register the routes from a literal table (a tuple, list or dict written in the source), " +
				"or declare them with decorators, so each served path is indexed.",
		})
	}
	return out
}

// 3. Orphan Database Tables
func (d *Detector) orphanTables(ctx context.Context, runID int64, opts Options) []Blindspot {
	tables, err := d.reg.TableUsageReport(ctx, runID)
	if err != nil {
		return nil
	}
	var out []Blindspot
	for _, t := range tables {
		if len(out) >= opts.MaxItemsPerKind {
			break
		}
		if t.Readers == 0 && t.Writers == 0 {
			out = append(out, Blindspot{
				Kind:        BlindspotOrphanTable,
				Severity:    "low",
				Target:      t.Table,
				Description: fmt.Sprintf("Database table %q is declared in schema but never read or written in code", t.Table),
				Remediation: "Check if table is deprecated or accessed dynamically via unindexed queries.",
			})
		}
	}
	return out
}

// 4. Missing Config Declarations
//
// Only the undeclared keys are read: the whole configuration report, every
// declared key included, was four seconds of this audit on a repository that
// indexes its JSON files as configuration.
func (d *Detector) missingConfig(ctx context.Context, runID int64, opts Options) []Blindspot {
	configs, err := d.reg.UndeclaredConfigReads(ctx, runID, opts.MaxItemsPerKind)
	if err != nil {
		return nil
	}
	var out []Blindspot
	for _, c := range configs {
		if c.Readers > 0 && !c.Declared {
			out = append(out, Blindspot{
				Kind:        BlindspotMissingConfigDecl,
				Severity:    "high",
				Target:      c.Name,
				Path:        c.FilePath,
				Line:        c.Line,
				Description: fmt.Sprintf("Config key %q is read in code %d time(s) but not declared in any config file", c.Name, c.Readers),
				Remediation: "Add default value to .env.example or application configuration schema to prevent runtime start-up failure.",
			})
		}
	}
	return out
}

// 6. Untested Critical Symbols
//
// Candidates come from ONE aggregate query, widest first, and are capped.
// Loading every symbol in the run and counting each exported one's
// incoming edges with its own query would cost a round trip per exported function,
// and the whole symbol table in memory. The cap is generous relative to
// what is reported (MaxItemsPerKind), and a symbol outside the widest few
// hundred is by definition not the most critical untested one.
func (d *Detector) untestedCritical(ctx context.Context, runID int64, opts Options) []Blindspot {
	if d.caps.Graph == nil {
		return nil
	}
	// The probes read the symbol table and the graph, each loaded once per
	// index. Loading both while the candidates are chosen, rather than one
	// after the other inside the first probe, is most of a cold audit.
	var warm sync.WaitGroup
	warm.Add(1)
	go func() {
		defer warm.Done()
		_, _ = d.caps.SymbolIndex(ctx, runID)
	}()
	if w, ok := d.caps.Graph.(warmer); ok {
		warm.Add(1)
		go func() {
			defer warm.Done()
			_ = w.Warm(ctx)
		}()
	}

	type candidate struct {
		sym      registry.SymbolHit
		path     string
		inDegree int
	}
	var candidates []candidate
	fanIn, ferr := d.reg.HighFanInSymbols(ctx, runID, 2, criticalCandidateCap(opts.MaxItemsPerKind))
	if ferr != nil {
		fanIn = nil
	}
	for _, s := range fanIn {
		// A helper in a test file is not production code that needs a test.
		if capability.IsTestPath(s.FilePath) {
			continue
		}
		candidates = append(candidates, candidate{
			sym:      registry.SymbolHit{ID: s.ID, Name: s.Name, FQN: s.FQN, FilePath: s.FilePath, Kind: s.Kind},
			path:     s.FilePath,
			inDegree: s.InDegree,
		})
	}
	warm.Wait()

	// Only whether a test exists decides a finding, so the probe skips the
	// evidence a single symbol's report carries beside its tests.
	probe := tests.ProbeOptions()
	probe.CoverageOnly = true
	// A bounded probe, not a full discovery: this loop asks "is there any
	// test near it" of dozens of the widest symbols in the run. A probe that
	// stopped at its bound without finding one has not established anything,
	// so that symbol is skipped, never reported as untested.
	untested := func(c candidate) bool {
		hit, err := tests.FindTestsWith(ctx, d.caps, runID, c.sym.FQN, probe)
		return err == nil && len(hit.Tests) == 0 && !hit.Inconclusive()
	}
	// The probes are independent reads, so a batch of them runs at once; the
	// findings are still taken in candidate order and stop at the cap, so the
	// answer is the one a sequential walk gives.
	var out []Blindspot
	for lo := 0; lo < len(candidates) && len(out) < opts.MaxItemsPerKind; lo += probeBatch {
		batch := candidates[lo:min(lo+probeBatch, len(candidates))]
		found := make([]bool, len(batch))
		var wg sync.WaitGroup
		next := make(chan int)
		for w := 0; w < probeWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range next {
					found[i] = untested(batch[i])
				}
			}()
		}
		for i := range batch {
			next <- i
		}
		close(next)
		wg.Wait()
		for i, cand := range batch {
			if len(out) >= opts.MaxItemsPerKind {
				break
			}
			if !found[i] {
				continue
			}
			out = append(out, Blindspot{
				Kind:        BlindspotUntestedCritical,
				Severity:    "high",
				Target:      cand.sym.FQN,
				Path:        cand.path,
				Description: fmt.Sprintf("Critical symbol %q has %d incoming callers but 0 discovered tests", cand.sym.FQN, cand.inDegree),
				Remediation: "Add dedicated unit or integration test covering this entrypoint before refactoring.",
			})
		}
	}
	return out
}

// probeBatch and probeWorkers bound the untested-symbol probes run at once.
const (
	probeBatch   = 64
	probeWorkers = 4
)

// criticalCandidateCap is how many high fan-in symbols are examined for
// missing tests: ten for every one that may be reported, and never fewer than
// a few hundred, so a repository whose widest symbols are all tested still
// finds the untested ones below them.
func criticalCandidateCap(maxItems int) int {
	return max(maxItems*10, 300)
}
