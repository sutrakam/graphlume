package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	afs "github.com/sutrakam/graphlume/internal/acquire/fs"
	"github.com/sutrakam/graphlume/internal/indexer"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// BridgeOptions configures RunLSPBridge.
type BridgeOptions struct {
	// Root is the repository root on disk.
	Root string
	// Language limits the run to a single language when non-empty.
	Language indexer.Language
	// Timeout bounds the LSP session per language.
	Timeout time.Duration
	// Logger receives audit and debug events.
	Logger *slog.Logger
	// MaxQueries limits definitions queried per run to prevent unbounded latency.
	MaxQueries int
	// AllowRepoBuild authorises indexers that execute repository build files.
	AllowRepoBuild bool
}

// BridgeStep records what happened for one language's LSP bridge run.
type BridgeStep struct {
	Language      string
	Server        string
	Binary        string
	Ran           bool
	SkippedReason string
	Queried       int
	Resolved      int
	EdgesUpgraded int
	EdgesInserted int
	Duration      time.Duration
	Err           error
}

// BridgeOutcome records the overall result of the LSP bridge pass.
type BridgeOutcome struct {
	Steps []BridgeStep
	Note  string
	Err   error
}

// TotalUpgraded returns the count of heuristic edges raised to exact.
func (o BridgeOutcome) TotalUpgraded() int {
	n := 0
	for _, s := range o.Steps {
		n += s.EdgesUpgraded
	}
	return n
}

// TotalInserted returns the count of new compiler edges added.
func (o BridgeOutcome) TotalInserted() int {
	n := 0
	for _, s := range o.Steps {
		n += s.EdgesInserted
	}
	return n
}

// RunLSPBridge runs language server protocol resolution for the indexed files.
// It queries the compiler daemon (e.g. clangd, rust-analyzer, gopls) directly
// and upgrades matched heuristic edges to model.ConfidenceExact.
func RunLSPBridge(ctx context.Context, reg *registry.Registry, runID int64, opts BridgeOptions) BridgeOutcome {
	var out BridgeOutcome
	if opts.Timeout <= 0 {
		opts.Timeout = 45 * time.Second
	}
	if opts.MaxQueries <= 0 {
		opts.MaxQueries = 1000
	}

	if realRoot, err := filepath.EvalSymlinks(opts.Root); err == nil {
		opts.Root = realRoot
	}

	files, err := reg.Files(ctx, runID)
	if err != nil {
		out.Err = fmt.Errorf("lsp bridge: read files: %w", err)
		return out
	}

	// Group valid files by language
	filesByLang := make(map[indexer.Language][]model.File)
	for _, f := range files {
		if f.ParseStatus == "failed" || f.ParseStatus == "skipped" {
			continue
		}
		lang := mapFileLanguage(f.Language)
		if lang != "" {
			filesByLang[lang] = append(filesByLang[lang], f)
		}
	}

	if len(filesByLang) == 0 {
		out.Note = "no supported language files indexed"
		return out
	}

	// Load symbols to map definition locations back to symbol IDs
	syms, err := reg.Symbols(ctx, runID)
	if err != nil {
		out.Err = fmt.Errorf("lsp bridge: read symbols: %w", err)
		return out
	}
	symIndex := buildSymbolIndex(syms, files)

	// Process each language present in deterministic sorted order
	var langs []indexer.Language
	for lang := range filesByLang {
		langs = append(langs, lang)
	}
	sort.Slice(langs, func(i, j int) bool { return string(langs[i]) < string(langs[j]) })

	for _, lang := range langs {
		langFiles := filesByLang[lang]
		if opts.Language != "" && lang != opts.Language {
			continue
		}

		step := BridgeStep{
			Language: string(lang),
		}

		// Security gate: compiler daemons that read/execute repository build scripts
		// require explicit authorization via --allow-repo-build.
		if !opts.AllowRepoBuild && requiresRepoBuild(lang) {
			step.SkippedReason = "requires --allow-repo-build"
			out.Steps = append(out.Steps, step)
			continue
		}

		spec, binPath, found := ProbeServer(lang)
		if !found {
			step.SkippedReason = fmt.Sprintf("no language server found for %s", lang)
			out.Steps = append(out.Steps, step)
			continue
		}

		step.Server = spec.Name
		step.Binary = binPath
		start := time.Now()

		stepErr := runLanguageSession(ctx, reg, runID, opts, spec, binPath, langFiles, symIndex, &step)
		step.Duration = time.Since(start)
		step.Err = stepErr
		out.Steps = append(out.Steps, step)
	}

	return out
}

// serverEnv is the environment a language server runs with on top of the
// inherited one. gopls would otherwise download the toolchain a go.mod names
// and could update go.mod and go.sum; both are the repository deciding what
// runs or changes on this machine.
func serverEnv(lang indexer.Language) []string {
	if lang == indexer.LangGo {
		return []string{"GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOPROXY=off"}
	}
	return nil
}

func requiresRepoBuild(lang indexer.Language) bool {
	switch lang {
	case indexer.LangRust, indexer.LangJava, indexer.LangCSharp, indexer.LangCPP, indexer.LangTypeScript, indexer.LangPython:
		return true
	default:
		return false
	}
}

func mapFileLanguage(l string) indexer.Language {
	switch strings.ToLower(l) {
	case "c", "cpp", "c++", "cxx", "h", "hpp":
		return indexer.LangCPP
	case "rust", "rs":
		return indexer.LangRust
	case "go":
		return indexer.LangGo
	case "python", "py":
		return indexer.LangPython
	case "java":
		return indexer.LangJava
	case "csharp", "c#", "cs":
		return indexer.LangCSharp
	case "ruby", "rb":
		return indexer.LangRuby
	case "typescript", "ts", "javascript", "js":
		return indexer.LangTypeScript
	default:
		return ""
	}
}

type fileSymbol struct {
	id   int64
	name string
	rng  model.Range
}

type symbolIndex struct {
	byFile map[string][]fileSymbol
}

func buildSymbolIndex(syms []model.Symbol, files []model.File) *symbolIndex {
	pathOf := make(map[int64]string, len(files))
	for _, f := range files {
		pathOf[f.ID] = f.Path
	}

	idx := &symbolIndex{
		byFile: make(map[string][]fileSymbol, len(files)),
	}

	for _, s := range syms {
		p, ok := pathOf[s.FileID]
		if !ok || s.Kind == model.KindFile {
			continue
		}
		idx.byFile[p] = append(idx.byFile[p], fileSymbol{
			id:   s.ID,
			name: s.Name,
			rng:  s.Range,
		})
	}

	return idx
}

// lookupEnclosing finds the innermost symbol enclosing (line, col) in path.
func (idx *symbolIndex) lookupEnclosing(path string, line, col int) (int64, bool) {
	syms := idx.byFile[path]
	if len(syms) == 0 {
		return 0, false
	}
	var bestID int64
	bestSpan := int(^uint(0) >> 1)
	found := false

	for _, s := range syms {
		if line < s.rng.Start.Line || line > s.rng.End.Line {
			continue
		}
		if line == s.rng.Start.Line && col < s.rng.Start.Col {
			continue
		}
		if line == s.rng.End.Line && col > s.rng.End.Col {
			continue
		}
		span := (s.rng.End.Line-s.rng.Start.Line)*10000 + (s.rng.End.Col - s.rng.Start.Col)
		if span < bestSpan {
			bestSpan = span
			bestID = s.id
			found = true
		}
	}
	return bestID, found
}

// lookupTarget finds the target symbol defined at or near targetLine in path.
func (idx *symbolIndex) lookupTarget(path string, targetLine int, name string) (int64, bool) {
	syms := idx.byFile[path]
	if len(syms) == 0 {
		return 0, false
	}
	// 1. Exact start line match
	var lineMatches []fileSymbol
	for _, s := range syms {
		if s.rng.Start.Line == targetLine {
			lineMatches = append(lineMatches, s)
		}
	}
	if len(lineMatches) == 1 {
		return lineMatches[0].id, true
	}
	if len(lineMatches) > 1 && name != "" {
		for _, s := range lineMatches {
			if s.name == name {
				return s.id, true
			}
		}
	}
	// 2. Name match in file
	if name != "" {
		var nameMatches []fileSymbol
		for _, s := range syms {
			if s.name == name {
				nameMatches = append(nameMatches, s)
			}
		}
		if len(nameMatches) == 1 {
			return nameMatches[0].id, true
		}
	}
	// 3. Enclosing line match
	for _, s := range syms {
		if s.rng.Start.Line <= targetLine && targetLine <= s.rng.End.Line {
			if name == "" || s.name == name {
				return s.id, true
			}
		}
	}
	return 0, false
}

func byteColToUTF16(lineText string, byteCol int) int {
	if byteCol <= 0 || len(lineText) == 0 {
		return 0
	}
	if byteCol > len(lineText) {
		byteCol = len(lineText)
	}
	u := 0
	for _, r := range lineText[:byteCol] {
		if r <= 0xFFFF {
			u++
		} else {
			u += 2
		}
	}
	return u
}

func runLanguageSession(
	ctx context.Context,
	reg *registry.Registry,
	runID int64,
	opts BridgeOptions,
	spec ServerSpec,
	binPath string,
	files []model.File,
	symIdx *symbolIndex,
	step *BridgeStep,
) error {
	sessCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	client, err := StartClient(sessCtx, ClientOptions{
		Binary:     binPath,
		Args:       spec.Args,
		Dir:        opts.Root,
		ServerName: spec.Name,
		Logger:     opts.Logger,
		Env:        serverEnv(spec.Language),
	})
	if err != nil {
		step.SkippedReason = fmt.Sprintf("failed to spawn %s: %v", spec.Name, err)
		return err
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(sessCtx, opts.Root); err != nil {
		step.SkippedReason = fmt.Sprintf("handshake failed: %v", err)
		return err
	}

	step.Ran = true

	// Open files in language server
	filePaths := make(map[string]bool, len(files))
	fileLines := make(map[string][]string, len(files))
	// A file is opened only when it is the file the index describes: positions
	// come from the index, so a file edited since would be queried at the
	// wrong lines. Oversized files are skipped whole rather than truncated
	// (half a file is a different program), and the total is bounded.
	const (
		maxOpenFileBytes  = 2 << 20
		maxOpenTotalBytes = 256 << 20
	)
	var openedBytes int64
	for _, f := range files {
		absPath := filepath.Join(opts.Root, filepath.FromSlash(f.Path))
		info, statErr := os.Stat(absPath)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxOpenFileBytes ||
			openedBytes+info.Size() > maxOpenTotalBytes {
			continue
		}
		content, readErr := os.ReadFile(absPath) //nolint:gosec // G304: an indexed file under the repository root
		if readErr != nil || (f.ContentHash != "" && afs.HashBytes(content) != f.ContentHash) {
			continue
		}
		openedBytes += int64(len(content))
		filePaths[f.Path] = true
		text := string(content)
		fileLines[f.Path] = strings.Split(text, "\n")
		_ = client.DidOpen(sessCtx, absPath, spec.LanguageID, text)
	}

	getLineText := func(filePath string, line1 int) string {
		lines := fileLines[filePath]
		if line1 > 0 && line1 <= len(lines) {
			return lines[line1-1]
		}
		return ""
	}

	// Read existing heuristic edges for this language
	allEdges, err := reg.Edges(sessCtx, runID)
	if err != nil {
		return fmt.Errorf("read edges: %w", err)
	}

	edgeConfidence := model.ConfidenceExact
	edgeSource := model.SourceCompiler
	if spec.Language == indexer.LangPython {
		if spec.Name == "pyright" {
			edgeConfidence = model.ConfidenceHigh
			edgeSource = model.SourceSourceCode
		} else {
			edgeConfidence = model.ConfidenceMedium
			edgeSource = model.SourceInferred
		}
	}

	var candidateEdges []model.Edge
	for _, e := range allEdges {
		if filePaths[e.Evidence.FilePath] && e.Confidence != model.ConfidenceExact {
			if e.Kind == model.EdgeCalls || e.Kind == model.EdgeReferences {
				candidateEdges = append(candidateEdges, e)
			}
		}
	}

	// Also check unresolved references
	unresolved, _ := reg.UnresolvedReferences(sessCtx, runID, opts.MaxQueries)

	var upgrades []model.Edge
	queryCount := 0

	// 1. Query candidate heuristic edges
	for _, e := range candidateEdges {
		if queryCount >= opts.MaxQueries {
			break
		}
		queryCount++
		step.Queried++

		absPath := filepath.Join(opts.Root, filepath.FromSlash(e.Evidence.FilePath))
		lineText := getLineText(e.Evidence.FilePath, e.Evidence.Range.Start.Line)
		char0 := byteColToUTF16(lineText, e.Evidence.Range.Start.Col)

		queryCtx, queryCancel := context.WithTimeout(sessCtx, 3*time.Second)
		locs, defErr := client.Definition(queryCtx, absPath, e.Evidence.Range.Start.Line-1, char0)
		if (defErr == nil && len(locs) == 0) && queryCount == 1 {
			// First query on a freshly spawned server may race initial background index/workspace load; retry once
			time.Sleep(600 * time.Millisecond)
			locs, defErr = client.Definition(queryCtx, absPath, e.Evidence.Range.Start.Line-1, char0)
		}
		queryCancel()

		if opts.Logger != nil {
			opts.Logger.Debug("lsp query definition", "file", absPath, "line", e.Evidence.Range.Start.Line-1, "col", char0, "locs", locs, "err", defErr)
		}
		if defErr != nil || len(locs) == 0 {
			continue
		}

		targetLoc := locs[0]
		targetPath := URIToFilePath(targetLoc.URI)
		if realTarget, err := filepath.EvalSymlinks(targetPath); err == nil {
			targetPath = realTarget
		}
		relTarget, relErr := filepath.Rel(opts.Root, targetPath)
		if relErr != nil || strings.HasPrefix(relTarget, "..") {
			// Outside repository
			continue
		}
		relTarget = filepath.ToSlash(relTarget)
		targetLine := targetLoc.Range.Start.Line + 1

		targetSymID, found := symIdx.lookupTarget(relTarget, targetLine, "")
		if opts.Logger != nil {
			opts.Logger.Debug("lsp lookup symbol", "relTarget", relTarget, "targetLine", targetLine, "found", found, "targetSymID", targetSymID)
		}
		if !found {
			continue
		}

		conf := edgeConfidence
		if len(locs) > 1 && conf == model.ConfidenceExact {
			conf = model.ConfidenceMedium
		}

		step.Resolved++
		ev := e.Evidence
		ev.Source = edgeSource
		ev.Detail = "lsp: " + spec.Name

		upgraded := model.Edge{
			SrcID:      e.SrcID,
			DstID:      targetSymID,
			Kind:       e.Kind,
			Confidence: conf,
			Evidence:   ev,
		}
		upgrades = append(upgrades, upgraded)
	}

	// 2. Query unresolved references where parser found a call but resolver didn't bind
	for _, u := range unresolved {
		if queryCount >= opts.MaxQueries {
			break
		}
		if !filePaths[u.Path] {
			continue
		}
		queryCount++
		step.Queried++

		absPath := filepath.Join(opts.Root, filepath.FromSlash(u.Path))
		lineText := getLineText(u.Path, u.Line)
		char0 := byteColToUTF16(lineText, u.Col)

		queryCtx, queryCancel := context.WithTimeout(sessCtx, 3*time.Second)
		locs, defErr := client.Definition(queryCtx, absPath, u.Line-1, char0)
		queryCancel()

		if defErr != nil || len(locs) == 0 {
			continue
		}

		targetLoc := locs[0]
		targetPath := URIToFilePath(targetLoc.URI)
		if realTarget, err := filepath.EvalSymlinks(targetPath); err == nil {
			targetPath = realTarget
		}
		relTarget, relErr := filepath.Rel(opts.Root, targetPath)
		if relErr != nil || strings.HasPrefix(relTarget, "..") {
			continue
		}
		relTarget = filepath.ToSlash(relTarget)
		targetLine := targetLoc.Range.Start.Line + 1

		targetSymID, found := symIdx.lookupTarget(relTarget, targetLine, u.Name)
		if !found {
			continue
		}

		// Look up caller symbol enclosing the call in source file
		srcSymID, hasSrc := symIdx.lookupEnclosing(u.Path, u.Line, u.Col)
		if !hasSrc {
			continue
		}

		conf := edgeConfidence
		if len(locs) > 1 && conf == model.ConfidenceExact {
			conf = model.ConfidenceMedium
		}

		step.Resolved++
		edgeKind := model.EdgeCalls
		if u.Kind != "" && u.Kind != "call" {
			edgeKind = model.EdgeReferences
		}

		upgrades = append(upgrades, model.Edge{
			SrcID:      srcSymID,
			DstID:      targetSymID,
			Kind:       edgeKind,
			Confidence: conf,
			Evidence: model.Evidence{
				FilePath: u.Path,
				Range: model.Range{
					Start: model.Position{Line: u.Line, Col: u.Col},
					End:   model.Position{Line: u.Line, Col: u.Col + len(u.Name)},
				},
				Source: edgeSource,
				Detail: "lsp: " + spec.Name,
			},
		})
	}

	if len(upgrades) > 0 {
		st, err := reg.UpgradeEdges(ctx, runID, upgrades)
		if err != nil {
			return fmt.Errorf("upgrade edges: %w", err)
		}
		step.EdgesUpgraded = st.Upgraded
		step.EdgesInserted = st.Inserted

		// Mark settled resolutions
		_, _ = reg.MarkCompilerResolutions(ctx, runID)
	}

	return nil
}
