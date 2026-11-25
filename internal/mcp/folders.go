package mcp

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/knowledge"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// structure is the folder-level view of the index: one row per folder with its
// size, how much code reaches in and out of it, how much of it a test reaches,
// and who has recently changed it, plus the relationships between folders.
//
// Unlike every other tool it returns its own structured shape and not the
// Response envelope, because its reader is a screen, not a model: the rows are
// the answer. It keeps the envelope's obligations all the same. The index
// revision and provenance travel with the rows, the confidence floor is
// stated, and a cut list says so.
//
// THE COST IS BOUNDED BY FOLDERS, NOT BY EDGES. The edges are read from
// file_edges, which sums them to files at index time, and grouped again in SQL
// by file pair (registry.FileEdgeRollup) and summed again by directory. Go sees
// at most one row per pair of files that have a relationship, never one per
// edge, and one per pair of directories after that. The file list, one small
// row per file, is read whole.

const (
	// defaultStructureLimit and maxStructureEdges bound one answer. A folder
	// row is about 200 bytes and an edge about 100.
	defaultStructureLimit = 200
	maxStructureEdges     = 500
	// structureTextRows is how many rows the readable table prints.
	structureTextRows = 40
	structureTextEdge = 10
	// structureOwnerNames is how many names a folder lists.
	structureOwnerNames = 3
	// structureOwnersBudget is how long an answer waits for commit history;
	// structureWarmBudget bounds the background read that follows.
	structureOwnersBudget = 2 * time.Second
	structureWarmBudget   = 10 * time.Minute
	// maxStructureDepth keeps a deliberately deep request from producing one
	// row per directory of the repository.
	maxStructureDepth = 12
)

// structureKinds are the relationship kinds counted between folders: the same
// set the portal's map drew, without plain references.
var structureKinds = []string{"CALLS", "INSTANTIATES", "IMPLEMENTS", "EXTENDS", "READS", "WRITES", "PUBLISHES", "CONSUMES", "DEPENDS_ON", "EXPOSES", "CONFIGURED_BY"}

// manifestNames are the files that make a folder a module.
var manifestNames = map[string]bool{
	"go.mod": true, "package.json": true, "pyproject.toml": true, "setup.py": true, "setup.cfg": true,
	"cargo.toml": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"composer.json": true, "gemfile": true, "mix.exs": true, "package.swift": true, "pubspec.yaml": true,
	"deno.json": true, "requirements.txt": true,
}

type structureArgs struct {
	Path              string `json:"path,omitempty" jsonschema:"Folder to look inside, e.g. 'services/'. Default: the repository root."`
	Depth             int    `json:"depth,omitempty" jsonschema:"How many folder levels below path to group by. Default 1: the folders directly inside path."`
	IncludeSubfolders *bool  `json:"include_subfolders,omitempty" jsonschema:"Count everything below a listed folder in that folder's figures. Default true. False counts only the files directly in it."`
	Limit             int    `json:"limit,omitempty" jsonschema:"Most folders to list. Default 200."`
	Offset            int    `json:"offset,omitempty" jsonschema:"Folders to skip, for the next page. Use next_offset from the previous answer."`
	Owners            *bool  `json:"owners,omitempty" jsonschema:"Name who has recently changed each folder, from git history. Default true. False skips reading history."`
	minConfidenceArg
}

// folderOwner is one name in a folder's owners.
type folderOwner struct {
	Name  string  `json:"name"`
	Share float64 `json:"share"`
}

// folderRow is one folder of the answer.
type folderRow struct {
	Path      string        `json:"path"`
	Name      string        `json:"name"`
	Kind      string        `json:"kind"`
	Self      bool          `json:"self,omitempty"`
	Language  string        `json:"language,omitempty"`
	Files     int64         `json:"files"`
	Symbols   int64         `json:"symbols"`
	CallsOut  int64         `json:"calls_out"`
	CallsIn   int64         `json:"calls_in"`
	TestedPct *int          `json:"tested_pct,omitempty"`
	Risk      string        `json:"risk"`
	Owners    []folderOwner `json:"owners,omitempty"`
}

// folderEdge is the relationships from one folder to another.
type folderEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Calls  int64  `json:"calls"`
	Exact  int64  `json:"exact"`
	High   int64  `json:"high"`
	Medium int64  `json:"medium"`
	Low    int64  `json:"low"`
}

// folderReport is the structured answer of structure.
type folderReport struct {
	Root          string       `json:"root"`
	Folders       []folderRow  `json:"folders"`
	Edges         []folderEdge `json:"edges"`
	Truncated     bool         `json:"truncated"`
	NextOffset    *int         `json:"next_offset,omitempty"`
	Total         int          `json:"total_folders"`
	MinConfidence string       `json:"min_confidence"`
	IndexRevision int64        `json:"index_revision"`
	IndexStatus   string       `json:"index_status,omitempty"`
	Provenance    *Provenance  `json:"provenance,omitempty"`
	Notes         []string     `json:"notes,omitempty"`
}

func (s *Server) registerFolders(allowed map[string]bool) {
	addTool(s, allowed, &sdk.Tool{
		Name:         "structure",
		Description:  toolDescriptions()["structure"],
		OutputSchema: openObjectSchema(),
	}, s.folderStructure)
}

// openObjectSchema is the output schema of the tools whose answer is not the
// Response envelope: an object, nothing more. The shape is documented in
// docs/reference/mcp-tools.md.
func openObjectSchema() map[string]any {
	return map[string]any{"type": "object"}
}

// failAs is failWith for a handler whose structured answer is not a Response.
func failAs[T any](res *sdk.CallToolResult, _ *Response, err error) (*sdk.CallToolResult, *T, error) {
	return res, nil, err
}

// folderAcc is a folder's running figures.
type folderAcc struct {
	row      folderRow
	langs    map[string]int64
	testable int64 // files that define something and are not tests
	tested   int64 // of those, files a test file reaches
}

func (s *Server) folderStructure(ctx context.Context, req *sdk.CallToolRequest, args structureArgs) (*sdk.CallToolResult, *folderReport, error) {
	if args.Depth < 0 || args.Limit < 0 || args.Offset < 0 {
		return failAs[folderReport](failWith("invalid_argument",
			"Pass depth, limit and offset as positive numbers, or omit them.",
			"depth, limit and offset must not be negative"))
	}
	floor, err := parseMinConfidence(args.MinConfidence)
	if err != nil {
		return failAs[folderReport](failWith("invalid_argument",
			"Pass min_confidence as exact, high, medium or low, or omit it.", "%v", err))
	}
	depth := args.Depth
	if depth == 0 {
		depth = 1
	}
	depth = min(depth, maxStructureDepth)
	withSub := args.IncludeSubfolders == nil || *args.IncludeSubfolders
	limit := args.Limit
	if limit == 0 {
		limit = defaultStructureLimit
	}
	limit = min(limit, s.limits.MaxPageItems)

	base, ok := cleanFolder(args.Path)
	if !ok {
		return failAs[folderReport](failWith("invalid_argument",
			"Pass path as a folder inside the repository, e.g. 'services/checkout-api', or omit it for the root.",
			"path %q is outside the repository", args.Path))
	}

	runID, status, err := s.currentRun(ctx)
	if err != nil {
		res, _, rerr := s.noRun(ctx, req, err)
		return failAs[folderReport](res, nil, rerr)
	}

	// The file list, the relationships by directory and the files tests reach
	// do not depend on the folder asked about: read once per run and floor.
	sb, err := s.loadStructureBase(ctx, runID, floor)
	if err != nil {
		return failAs[folderReport](failFrom(err, "%v", err))
	}
	files := sb.files

	k := keyer{base: base, depth: depth, sub: withSub}
	accs := map[string]*folderAcc{}
	acc := func(key string) *folderAcc {
		a := accs[key]
		if a == nil {
			a = &folderAcc{langs: map[string]int64{}}
			a.row = folderRow{Path: rootOr(key), Name: nameOf(key, base), Kind: "folder", Risk: "low", Self: key == base}
			accs[key] = a
		}
		return a
	}

	isTest := make(map[int64]bool)
	hasSymbols := make(map[int64]bool)
	manifestDirs := map[string]bool{}
	fileKey := make(map[int64]string, len(files))
	for _, f := range files {
		dir := dirOf(f.Path)
		if manifestNames[strings.ToLower(path.Base(f.Path))] {
			manifestDirs[dir] = true
		}
		if capability.IsTestPath(f.Path) {
			isTest[f.ID] = true
		}
		if f.Symbols > 0 {
			hasSymbols[f.ID] = true
		}
		key, listed := k.keyOf(dir)
		if !listed {
			continue
		}
		a := acc(key)
		a.row.Files++
		a.row.Symbols += f.Symbols
		w := f.Symbols
		if w == 0 {
			w = 1
		}
		if f.Language != "" {
			a.langs[f.Language] += w
		}
		fileKey[f.ID] = key
		if f.Symbols > 0 && !isTest[f.ID] {
			a.testable++
		}
	}

	// Which non-test files a test file reaches (one grouped query, in the base).
	for _, id := range sb.reached {
		if key, ok := fileKey[id]; ok && !isTest[id] && hasSymbols[id] {
			accs[key].tested++
		}
	}

	// Edges between folders. The file pairs are summed by directory first, so
	// the loop below runs once per pair of directories.
	dirEdges := sb.dirEdges
	type pair struct{ from, to string }
	pairs := map[pair]*folderEdge{}
	for _, e := range dirEdges {
		from, fromListed := k.endKey(e.SrcDir)
		to, toListed := k.endKey(e.DstDir)
		if from == to {
			continue
		}
		n := e.Exact + e.High + e.Medium + e.Low
		if a := accs[from]; fromListed && a != nil {
			a.row.CallsOut += n
		}
		if a := accs[to]; toListed && a != nil {
			a.row.CallsIn += n
		}
		// A line is drawn only when both ends have a name worth showing: a
		// listed folder, or the top-level part of the repository outside it.
		if (!fromListed && !toListed) || (!fromListed && !k.outsideBase(e.SrcDir)) || (!toListed && !k.outsideBase(e.DstDir)) {
			continue
		}
		p := pair{rootOr(from), rootOr(to)}
		ge := pairs[p]
		if ge == nil {
			ge = &folderEdge{From: p.from, To: p.to}
			pairs[p] = ge
		}
		ge.Calls += n
		ge.Exact += e.Exact
		ge.High += e.High
		ge.Medium += e.Medium
		ge.Low += e.Low
	}

	// Order, page, then decorate the page only.
	all := make([]*folderAcc, 0, len(accs))
	maxIn := int64(0)
	for _, a := range accs {
		all = append(all, a)
		maxIn = max(maxIn, a.row.CallsIn)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i].row, all[j].row
		if a.Symbols != b.Symbols {
			return a.Symbols > b.Symbols
		}
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Path < b.Path
	})
	rep := &folderReport{
		Root:          rootOr(base),
		Folders:       []folderRow{},
		Edges:         []folderEdge{},
		Total:         len(all),
		MinConfidence: string(floor),
		IndexRevision: runID,
		IndexStatus:   status,
	}
	start := min(args.Offset, len(all))
	end := min(start+limit, len(all))
	if end < len(all) {
		rep.Truncated = true
		next := end
		rep.NextOffset = &next
	}
	page := all[start:end]
	inPage := map[string]bool{}
	for _, a := range page {
		r := a.row
		inPage[r.Path] = true
		r.Language = dominant(a.langs)
		if manifestDirs[a.dirKey(base)] {
			r.Kind = "module"
		}
		if a.testable > 0 {
			pct := int(100 * a.tested / a.testable)
			r.TestedPct = &pct
		}
		r.Risk = folderRisk(r, maxIn)
		rep.Folders = append(rep.Folders, r)
	}

	// Edges that touch the page, strongest first.
	var edges []*folderEdge
	for _, e := range pairs {
		if inPage[e.From] || inPage[e.To] {
			edges = append(edges, e)
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Calls != edges[j].Calls {
			return edges[i].Calls > edges[j].Calls
		}
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	if len(edges) > maxStructureEdges {
		edges = edges[:maxStructureEdges]
		rep.Truncated = true
		rep.Notes = append(rep.Notes, fmt.Sprintf("Only the %d strongest relationships between folders are listed.", maxStructureEdges))
	}
	var weak int64
	for _, e := range edges {
		rep.Edges = append(rep.Edges, *e)
		weak += e.Medium + e.Low
	}

	if args.Owners == nil || *args.Owners {
		s.attachFolderOwners(ctx, rep, base, depth, withSub, runID)
	}

	if len(all) == 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("Nothing is indexed under %q. Absence here is not proof the folder is empty: generated, oversized and excluded files are not indexed.", rootOr(base)))
	}
	rep.Notes = append(rep.Notes,
		"calls_out and calls_in count relationships (calls, instantiations, implements, reads, writes and the like, not plain references) between files in different folders; relationships inside one folder are not counted.",
		"tested_pct is the share of the folder's files that define something and are reached by a relationship from a test file, found by file naming. It is a floor: tests that reach code through an interface or by reflection are not seen.",
		"risk is relative to the folders listed: how much of the cross-folder traffic lands on the folder, weighed against how little of it a test reaches. It orders the list; it is not a quality measure.")
	if floor != model.ConfidenceLow {
		rep.Notes = append(rep.Notes, fmt.Sprintf("Relationships below confidence %q are not counted; pass min_confidence=\"low\" to count them.", floor))
	}
	if weak > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf(
			"%d of the relationships between the folders listed are medium or low confidence (heuristic inference with higher error rates). Pass min_confidence='high' to count only compiler-verified and high-precision ones.", weak))
	}
	rep.Provenance = s.provenanceFor(ctx, runID)
	if note := s.stagingNote(ctx, runID); note != "" {
		rep.Notes = append(rep.Notes, note)
	}

	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: renderStructure(rep, floor, status)}},
	}, rep, nil
}

// dirPairEdges is the relationships between the files of two directories.
type dirPairEdges struct {
	SrcDir, DstDir           string
	Exact, High, Medium, Low int64
}

// foldByDirectory sums file-to-file relationships by the directory of each end,
// leaving out those inside one directory. Directories have no trailing slash
// and are "" for the repository root.
func foldByDirectory(edges []registry.FilePairEdges, files []registry.FolderFile) []dirPairEdges {
	dirOfID := make(map[int64]string, len(files))
	for _, f := range files {
		dirOfID[f.ID] = dirOf(f.Path)
	}
	at := map[[2]string]int{}
	var out []dirPairEdges
	for _, e := range edges {
		sd, ok1 := dirOfID[e.SrcID]
		td, ok2 := dirOfID[e.DstID]
		if !ok1 || !ok2 || sd == td {
			continue
		}
		k := [2]string{sd, td}
		i, ok := at[k]
		if !ok {
			i = len(out)
			at[k] = i
			out = append(out, dirPairEdges{SrcDir: sd, DstDir: td})
		}
		out[i].Exact += e.Exact
		out[i].High += e.High
		out[i].Medium += e.Medium
		out[i].Low += e.Low
	}
	return out
}

// dirKey is the folder directory the row stands for, as the index spells
// directories, for the manifest check.
func (a *folderAcc) dirKey(base string) string {
	if a.row.Self {
		return base
	}
	return a.row.Path
}

// attachFolderOwners names who has recently changed each listed folder. It
// reads the same history owners and busfactor do, through the same port, and is
// skipped when none is available or the read fails: a folder without owners is
// an unknown, not an empty list of people.
func (s *Server) attachFolderOwners(ctx context.Context, rep *folderReport, base string, depth int, withSub bool, runID int64) {
	if s.deps.Owners == nil || len(rep.Folders) == 0 {
		return
	}
	opts := knowledge.Options{
		Limit:        1 << 20,
		MinCommits:   1,
		PathPrefix:   prefixOf(base),
		ShallowClone: s.headRepo != nil && s.headRepo.Shallow(),
	}
	if withSub {
		opts.Depth = segCount(base) + depth
	}
	// Reading history is a walk of the commits, which costs seconds on a large
	// repository the first time and is remembered after that. A screen should
	// not wait for it: past the budget the rows go out without names, and the
	// walk carries on in the background so the next call has them.
	if s.ownersWarming.Load() {
		rep.Notes = append(rep.Notes, ownersPendingNote)
		return
	}
	octx, cancel := context.WithTimeout(ctx, structureOwnersBudget)
	defer cancel()
	krep, err := knowledge.Analyze(octx, knowledgeHistory{s.deps.Owners}, s.deps.Registry, runID, opts)
	if err != nil {
		if octx.Err() != nil && ctx.Err() == nil {
			s.warmOwners()
			rep.Notes = append(rep.Notes, ownersPendingNote)
		}
		return
	}
	if krep.Partial {
		// The walk stopped at its time budget: the names below are drawn from
		// the commits read so far (the note says how many), and the rest is
		// read in the background for the next call.
		s.warmOwners()
	}
	if krep.CommitsRead == 0 {
		return
	}
	byPath := make(map[string][]knowledge.Contributor, len(krep.Areas))
	for _, a := range krep.Areas {
		p := a.Path
		if p == knowledge.Root {
			p = ""
		}
		byPath[p] = a.Top
	}
	for i := range rep.Folders {
		key := rep.Folders[i].Path
		if key == "." {
			key = ""
		}
		for j, c := range byPath[key] {
			if j == structureOwnerNames {
				break
			}
			rep.Folders[i].Owners = append(rep.Folders[i].Owners, folderOwner{
				Name:  clipRunes(c.Name, knowledgeNameRunes),
				Share: float64(int(c.Share*100+0.5)) / 100,
			})
		}
	}
	rep.Notes = append(rep.Notes, fmt.Sprintf(
		"owners are the names behind the most commits to each folder in the last %d commit(s): who has recently changed it, not who must approve. Email addresses are left out.",
		krep.CommitsRead))
}

const ownersPendingNote = "owners are not shown yet: reading the commit history for the first time takes longer than this answer may wait. It continues in the background; ask again in a moment."

// warmOwners reads the commit history in the background, once at a time, so
// the walk a first call could not wait for is in the history cache by the next.
func (s *Server) warmOwners() {
	if s.deps.Owners == nil || !s.ownersWarming.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.ownersWarming.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), structureWarmBudget)
		defer cancel()
		// A history reader may stop each walk at its own time budget and
		// continue it on the next call; keep calling until the window is whole.
		for ctx.Err() == nil {
			cs, err := s.deps.Owners.Commits(ctx, knowledge.DefaultCommitWindow)
			if err != nil || len(cs) == 0 || !cs[len(cs)-1].Truncated {
				return
			}
		}
	}()
}

// folderRisk grades one folder against the others listed. Exposure is how much
// of the busiest folder's inbound traffic this one takes; the gap is the share
// of its code no test reaches (half when that is unknown). A folder that
// defines nothing is low.
func folderRisk(r folderRow, maxIn int64) string {
	if r.Symbols == 0 {
		return "low"
	}
	exposure := 0.0
	if maxIn > 0 {
		exposure = float64(r.CallsIn) / float64(maxIn)
	}
	gap := 0.5
	if r.TestedPct != nil {
		gap = 1 - float64(*r.TestedPct)/100
	}
	score := 0.6*exposure + 0.4*gap
	switch {
	case score >= 0.65:
		return "high"
	case score >= 0.35:
		return "medium"
	}
	return "low"
}

// dominant is the language holding the most definitions in a folder, ties
// broken by name so the answer does not move between calls.
func dominant(langs map[string]int64) string {
	best, bestN := "", int64(0)
	for l, n := range langs {
		if n > bestN || (n == bestN && l < best) {
			best, bestN = l, n
		}
	}
	return best
}

// ---------------------------------------------------------------- paths

// keyer maps the directory of a file to the folder it is counted under.
type keyer struct {
	base  string // "" for the root, else "a/b" without a trailing slash
	depth int
	sub   bool
}

// keyOf maps a directory ("" for the root) to its folder. listed is false for
// a directory outside base, or too deep to be its own row when subfolders are
// not folded in.
func (k keyer) keyOf(dir string) (key string, listed bool) {
	rel, in := k.relative(dir)
	if !in {
		return "", false
	}
	if rel == "" {
		return k.base, true
	}
	segs := strings.Split(rel, "/")
	if len(segs) > k.depth {
		if !k.sub {
			return "", false
		}
		segs = segs[:k.depth]
	}
	return k.join(segs), true
}

// endKey is keyOf for one end of a relationship, which always has a key: a
// directory outside the listing is named by itself (inside base, too deep to
// list) or by its top-level folder (outside base).
func (k keyer) endKey(dir string) (string, bool) {
	if key, ok := k.keyOf(dir); ok {
		return key, true
	}
	if _, in := k.relative(dir); in {
		return dir, false
	}
	return firstSegment(dir), false
}

// outsideBase reports whether dir is outside the listed folder.
func (k keyer) outsideBase(dir string) bool {
	_, in := k.relative(dir)
	return !in
}

func (k keyer) relative(dir string) (rel string, in bool) {
	if k.base == "" {
		return dir, true
	}
	if dir == k.base {
		return "", true
	}
	if strings.HasPrefix(dir, k.base+"/") {
		return dir[len(k.base)+1:], true
	}
	return "", false
}

func (k keyer) join(segs []string) string {
	if k.base == "" {
		return strings.Join(segs, "/")
	}
	return k.base + "/" + strings.Join(segs, "/")
}

// cleanFolder normalises a folder argument to "" (root) or "a/b".
func cleanFolder(p string) (string, bool) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", false
		}
	}
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" || p == "." {
		return "", true
	}
	return p, true
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

func firstSegment(dir string) string {
	if i := strings.IndexByte(dir, '/'); i >= 0 {
		return dir[:i]
	}
	return dir
}

func segCount(p string) int {
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// prefixOf is the path prefix history is filtered by: base with a trailing
// slash, so "svc" does not match "svc-old/".
func prefixOf(base string) string {
	if base == "" {
		return ""
	}
	return base + "/"
}

// rootOr names the repository root ".".
func rootOr(p string) string {
	if p == "" {
		return "."
	}
	return p
}

func nameOf(key, base string) string {
	if key == base {
		return "."
	}
	return path.Base(key)
}

// ---------------------------------------------------------------- text

// renderStructure is the readable table: the folders, then the strongest
// relationships. Both are cut to a few dozen rows; the structured answer has
// the rest.
func renderStructure(r *folderReport, floor model.Confidence, status string) string {
	var sum strings.Builder
	fmt.Fprintf(&sum, "%d folder(s) in %s", r.Total, r.Root)
	if r.Truncated && r.NextOffset != nil {
		fmt.Fprintf(&sum, ", %d listed, next page at offset %d", len(r.Folders), *r.NextOffset)
	}
	fmt.Fprintf(&sum, " (\".\" is the repository root). Relationships at confidence %s or better.", floor)

	var b strings.Builder
	b.WriteString("| folder | kind | language | files | symbols | calls out | calls in | tested | risk | owners |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---:|---:|---|---|\n")
	shown := min(len(r.Folders), structureTextRows)
	for _, f := range r.Folders[:shown] {
		tested := "-"
		if f.TestedPct != nil {
			tested = fmt.Sprintf("%d%%", *f.TestedPct)
		}
		var owners []string
		for _, o := range f.Owners {
			owners = append(owners, fmt.Sprintf("%s %.0f%%", o.Name, 100*o.Share))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d | %d | %s | %s | %s |\n",
			cell(f.Path), f.Kind, orDash(f.Language), f.Files, f.Symbols, f.CallsOut, f.CallsIn, tested, f.Risk, orDash(strings.Join(owners, ", ")))
	}
	if shown < len(r.Folders) {
		fmt.Fprintf(&b, "\n%d more folder(s) are in the structured answer.\n", len(r.Folders)-shown)
	}
	if len(r.Edges) > 0 {
		b.WriteString("\nStrongest relationships between folders (calls total; exact/high/medium/low):\n")
		for i, e := range r.Edges {
			if i == structureTextEdge {
				fmt.Fprintf(&b, "... and %d more\n", len(r.Edges)-i)
				break
			}
			fmt.Fprintf(&b, "- %s -> %s: %d (%d/%d/%d/%d)\n", cell(e.From), cell(e.To), e.Calls, e.Exact, e.High, e.Medium, e.Low)
		}
	}
	for _, n := range r.Notes {
		b.WriteString("\nNote: " + n)
	}
	b.WriteString("\n")
	return frameReport(sum.String(), r.IndexRevision, status, b.String())
}

// cell keeps a path from breaking a table row.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "/")
	if utf8.RuneCountInString(s) > 80 {
		return clipRunes(s, 80)
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
