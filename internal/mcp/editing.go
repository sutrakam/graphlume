package mcp

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sutrakam/graphlume/internal/edit"
	"github.com/sutrakam/graphlume/pkg/model"
)

type replaceSymbolBodyArgs struct {
	Name        string `json:"name" jsonschema:"Symbol name to replace body of (e.g. 'calculateTax'). Exact match."`
	Path        string `json:"path,omitempty" jsonschema:"File path containing the symbol, to disambiguate if multiple symbols share the name."`
	Line        int    `json:"line,omitempty" jsonschema:"1-indexed line number where definition begins, to disambiguate overloads."`
	NewBody     string `json:"new_body" jsonschema:"The new replacement body implementation code."`
	PreserveSig *bool  `json:"preserve_signature,omitempty" jsonschema:"If true (default), preserves function/method signature and docstring, replacing only the inner body. If false, replaces the entire symbol span."`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"If true, returns diff and preview without writing to disk."`
}

type insertAfterSymbolArgs struct {
	Name   string `json:"name" jsonschema:"Target symbol name to insert code after (e.g. 'calculateTax')."`
	Path   string `json:"path,omitempty" jsonschema:"File path containing the symbol."`
	Line   int    `json:"line,omitempty" jsonschema:"1-indexed line number where definition begins."`
	Code   string `json:"code" jsonschema:"The code to insert immediately after the symbol."`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"If true, returns diff and preview without writing to disk."`
}

type safeDeleteSymbolArgs struct {
	Name   string `json:"name" jsonschema:"Symbol name to delete (e.g. 'unusedHelper')."`
	Path   string `json:"path,omitempty" jsonschema:"File path containing the symbol."`
	Line   int    `json:"line,omitempty" jsonschema:"1-indexed line number where definition begins."`
	Force  bool   `json:"force,omitempty" jsonschema:"If true, deletes even if callers or dependent relationships exist. If false (default), refuses deletion and reports dependencies."`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"If true, returns diff and preview without writing to disk."`
}

func (s *Server) replaceSymbolBody(ctx context.Context, req *sdk.CallToolRequest, args replaceSymbolBodyArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Name) == "" {
		return failWith("invalid_argument", "Pass a non-empty name, then call replace_symbol_body again.", "name must not be empty")
	}
	if strings.TrimSpace(args.NewBody) == "" {
		return failWith("invalid_argument", "Pass non-empty new_body code, then call replace_symbol_body again.", "new_body must not be empty")
	}

	if !args.DryRun && !s.deps.Config.AllowEdits {
		return failWith("permission_denied", s.editDeniedHint(), "editing is disabled")
	}

	preserve := true
	if args.PreserveSig != nil {
		preserve = *args.PreserveSig
	}

	q := symbolQuery{
		Tool: "replace_symbol_body", Param: "name", PathParam: "path", LineParam: "line",
		Name: strings.TrimSpace(args.Name), Path: args.Path, Line: args.Line,
	}
	sel, sc, scResp := s.selectSymbol(ctx, req, q)
	if sc != nil {
		return sc, scResp, nil
	}

	target := sel.Target
	repoRoot := s.deps.Config.RepoRoot
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	absPath := filepath.Join(repoRoot, filepath.FromSlash(target.FilePath))
	contentBytes, err := os.ReadFile(absPath) //nolint:gosec // G304: an indexed file of this repository
	if err != nil {
		return failFrom(err, "read %s: %v", target.FilePath, err)
	}

	originalText := string(contentBytes)
	lines := strings.Split(originalText, "\n")
	if target.StartLine < 1 || target.EndLine > len(lines) || target.StartLine > target.EndLine {
		return failWith("range_error", "Symbol line boundaries are out of range for current file contents. Re-index and retry.", "line out of range")
	}

	startIdx := target.StartLine - 1
	endIdx := target.EndLine - 1

	var newText string
	if preserve {
		// 1. Go AST accurate parsing if Go file
		if strings.HasSuffix(target.FilePath, ".go") {
			openOff, closeOff, ok := findGoBodyOffsets(originalText, target.StartLine)
			if ok {
				pre := originalText[:openOff+1]
				post := originalText[closeOff:]
				newText = pre + "\n" + args.NewBody + "\n" + post
			}
		}
		// 2. Python body preservation
		if newText == "" && strings.HasSuffix(target.FilePath, ".py") {
			newText = replacePythonBody(lines, startIdx, endIdx, args.NewBody)
		}
		// 3. General brace matching with balanced parens/brackets
		if newText == "" {
			spanText := strings.Join(lines[startIdx:endIdx+1], "\n")
			openBrace, closeBrace, ok := findBodyBraces(spanText)
			if ok {
				pre := spanText[:openBrace+1]
				post := spanText[closeBrace:]
				newSpan := pre + "\n" + args.NewBody + "\n" + post

				var b strings.Builder
				for i := 0; i < startIdx; i++ {
					b.WriteString(lines[i])
					b.WriteString("\n")
				}
				b.WriteString(newSpan)
				if endIdx+1 < len(lines) {
					b.WriteString("\n")
					b.WriteString(strings.Join(lines[endIdx+1:], "\n"))
				}
				newText = b.String()
			}
		}
	}

	if newText == "" {
		// Whole span replacement fallback
		var b strings.Builder
		for i := 0; i < startIdx; i++ {
			b.WriteString(lines[i])
			b.WriteString("\n")
		}
		b.WriteString(args.NewBody)
		if endIdx+1 < len(lines) {
			b.WriteString("\n")
			b.WriteString(strings.Join(lines[endIdx+1:], "\n"))
		}
		newText = b.String()
	}

	diffPreview := generateUnifiedDiff(target.FilePath, originalText, newText)

	resp := &Response{
		IndexRevision: sel.RunID,
		IndexStatus:   sel.Status,
		Summary: fmt.Sprintf("Replaced implementation body of %s (%s) at %s:%d-%d.",
			target.Name, target.Kind, target.FilePath, target.StartLine, target.EndLine),
	}
	if args.DryRun {
		resp.Notes = append(resp.Notes, "[dry-run] No changes written to disk.")
		resp.Items = append(resp.Items, Item{
			Kind: target.Kind, Name: target.Name, Path: target.FilePath, Line: target.StartLine,
			Why: "diff_preview", Excerpt: diffPreview,
		})
		return s.reply(ctx, req, resp)
	}

	// Commit safely through internal/edit with lock, hash check, and rollback
	file, _ := s.deps.Registry.FileByPath(ctx, sel.RunID, target.FilePath)
	wantHash := ""
	if file != nil {
		wantHash = file.ContentHash
	}
	ed := edit.New(repoRoot)
	prep, probs, err := ed.PrepareDirect(ctx, target.FilePath, wantHash, []byte(newText))
	if err != nil {
		return failFrom(err, "prepare edit %s: %v", target.FilePath, err)
	}
	if len(probs) > 0 {
		if errors.Is(probs[0].Err, edit.ErrStale) {
			return editCommitFailure(probs[0].Err, target.FilePath)
		}
		return failWith("edit_error", probs[0].Reason, "%s", probs[0].Reason)
	}
	if err := ed.Commit(ctx, []edit.Prepared{prep}); err != nil {
		return editCommitFailure(err, target.FilePath)
	}
	s.invalidateIndex()

	resp.Items = append(resp.Items, Item{
		Kind: target.Kind, Name: target.Name, Path: target.FilePath, Line: target.StartLine,
		Why: "replaced_body", Excerpt: diffPreview,
	})
	return s.reply(ctx, req, resp)
}

func (s *Server) insertAfterSymbol(ctx context.Context, req *sdk.CallToolRequest, args insertAfterSymbolArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Name) == "" {
		return failWith("invalid_argument", "Pass a non-empty name, then call insert_after_symbol again.", "name must not be empty")
	}
	if strings.TrimSpace(args.Code) == "" {
		return failWith("invalid_argument", "Pass non-empty code to insert, then call insert_after_symbol again.", "code must not be empty")
	}

	if !args.DryRun && !s.deps.Config.AllowEdits {
		return failWith("permission_denied", s.editDeniedHint(), "editing is disabled")
	}

	q := symbolQuery{
		Tool: "insert_after_symbol", Param: "name", PathParam: "path", LineParam: "line",
		Name: strings.TrimSpace(args.Name), Path: args.Path, Line: args.Line,
	}
	sel, sc, scResp := s.selectSymbol(ctx, req, q)
	if sc != nil {
		return sc, scResp, nil
	}

	target := sel.Target
	repoRoot := s.deps.Config.RepoRoot
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	absPath := filepath.Join(repoRoot, filepath.FromSlash(target.FilePath))
	contentBytes, err := os.ReadFile(absPath) //nolint:gosec // G304: an indexed file of this repository
	if err != nil {
		return failFrom(err, "read %s: %v", target.FilePath, err)
	}

	originalText := string(contentBytes)
	lines := strings.Split(originalText, "\n")
	insertLine := target.EndLine
	if insertLine > len(lines) {
		insertLine = len(lines)
	}

	var newLines []string
	newLines = append(newLines, lines[:insertLine]...)
	newLines = append(newLines, args.Code)
	newLines = append(newLines, lines[insertLine:]...)
	newText := strings.Join(newLines, "\n")

	diffPreview := generateUnifiedDiff(target.FilePath, originalText, newText)

	resp := &Response{
		IndexRevision: sel.RunID,
		IndexStatus:   sel.Status,
		Summary: fmt.Sprintf("Inserted code after %s (%s) at %s:%d.",
			target.Name, target.Kind, target.FilePath, target.EndLine),
	}
	if args.DryRun {
		resp.Notes = append(resp.Notes, "[dry-run] No changes written to disk.")
		resp.Items = append(resp.Items, Item{
			Kind: target.Kind, Name: target.Name, Path: target.FilePath, Line: target.EndLine,
			Why: "diff_preview", Excerpt: diffPreview,
		})
		return s.reply(ctx, req, resp)
	}

	file, _ := s.deps.Registry.FileByPath(ctx, sel.RunID, target.FilePath)
	wantHash := ""
	if file != nil {
		wantHash = file.ContentHash
	}
	ed := edit.New(repoRoot)
	prep, probs, err := ed.PrepareDirect(ctx, target.FilePath, wantHash, []byte(newText))
	if err != nil {
		return failFrom(err, "prepare edit %s: %v", target.FilePath, err)
	}
	if len(probs) > 0 {
		if errors.Is(probs[0].Err, edit.ErrStale) {
			return editCommitFailure(probs[0].Err, target.FilePath)
		}
		return failWith("edit_error", probs[0].Reason, "%s", probs[0].Reason)
	}
	if err := ed.Commit(ctx, []edit.Prepared{prep}); err != nil {
		return editCommitFailure(err, target.FilePath)
	}
	s.invalidateIndex()

	resp.Items = append(resp.Items, Item{
		Kind: target.Kind, Name: target.Name, Path: target.FilePath, Line: target.EndLine,
		Why: "inserted_code", Excerpt: diffPreview,
	})
	return s.reply(ctx, req, resp)
}

func (s *Server) safeDeleteSymbol(ctx context.Context, req *sdk.CallToolRequest, args safeDeleteSymbolArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Name) == "" {
		return failWith("invalid_argument", "Pass a non-empty name, then call safe_delete_symbol again.", "name must not be empty")
	}

	if !args.DryRun && !s.deps.Config.AllowEdits {
		return failWith("permission_denied", s.editDeniedHint(), "editing is disabled")
	}

	q := symbolQuery{
		Tool: "safe_delete_symbol", Param: "name", PathParam: "path", LineParam: "line",
		Name: strings.TrimSpace(args.Name), Path: args.Path, Line: args.Line,
	}
	sel, sc, scResp := s.selectSymbol(ctx, req, q)
	if sc != nil {
		return sc, scResp, nil
	}

	target := sel.Target

	// Dependency check, failing closed: an error reading the graph refuses the
	// deletion rather than treating "could not look" as "nothing depends on it".
	if !args.Force {
		edges, err := s.deps.Registry.EdgesTo(ctx, sel.RunID, target.ID)
		if err != nil {
			return failFrom(err, "check dependents of %s: %v", target.Name, err)
		}
		var deps []model.Edge
		for _, e := range edges {
			if e.SrcID != target.ID { // recursion is not a dependent
				deps = append(deps, e)
			}
		}
		if len(deps) > 0 {
			sample := deps[0]
			return failWith("safety_violation",
				fmt.Sprintf("Refusing to delete %s (%s): %d active relationship(s) depend on it (e.g. %s at %s:%d). Pass force=true to override.",
					target.Name, target.Kind, len(deps), sample.Kind, sample.Evidence.FilePath, sample.Evidence.Range.Start.Line),
				"symbol has active dependencies")
		}
		// No edge is not proof of no use: a reference the resolver could not
		// bind may be a call to this very definition.
		unbound, err := s.deps.Registry.UnboundReferencesNamed(ctx, sel.RunID, target.Name)
		if err != nil {
			return failFrom(err, "check unbound references to %s: %v", target.Name, err)
		}
		if unbound > 0 {
			return failWith("safety_violation",
				fmt.Sprintf("Refusing to delete %s (%s): %d reference(s) spelled %q were not bound by the index and may use it. "+
					"Check them (search), then pass force=true to override.", target.Name, target.Kind, unbound, target.Name),
				"symbol has unbound references")
		}
	}

	repoRoot := s.deps.Config.RepoRoot
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	absPath := filepath.Join(repoRoot, filepath.FromSlash(target.FilePath))
	contentBytes, err := os.ReadFile(absPath) //nolint:gosec // G304: an indexed file of this repository
	if err != nil {
		return failFrom(err, "read %s: %v", target.FilePath, err)
	}

	originalText := string(contentBytes)
	lines := strings.Split(originalText, "\n")
	startIdx := target.StartLine - 1
	endIdx := target.EndLine - 1

	if startIdx < 0 || endIdx >= len(lines) || startIdx > endIdx {
		return failWith("range_error", "Symbol line boundaries are out of range. Re-index and retry.", "line out of range")
	}

	var remainingLines []string
	remainingLines = append(remainingLines, lines[:startIdx]...)
	if endIdx+1 < len(lines) {
		remainingLines = append(remainingLines, lines[endIdx+1:]...)
	}
	newText := strings.Join(remainingLines, "\n")

	diffPreview := generateUnifiedDiff(target.FilePath, originalText, newText)

	resp := &Response{
		IndexRevision: sel.RunID,
		IndexStatus:   sel.Status,
		Summary: fmt.Sprintf("Deleted symbol %s (%s) from %s (lines %d-%d).",
			target.Name, target.Kind, target.FilePath, target.StartLine, target.EndLine),
	}
	if args.DryRun {
		resp.Notes = append(resp.Notes, "[dry-run] No changes written to disk.")
		resp.Items = append(resp.Items, Item{
			Kind: target.Kind, Name: target.Name, Path: target.FilePath, Line: target.StartLine,
			Why: "diff_preview", Excerpt: diffPreview,
		})
		return s.reply(ctx, req, resp)
	}

	file, _ := s.deps.Registry.FileByPath(ctx, sel.RunID, target.FilePath)
	wantHash := ""
	if file != nil {
		wantHash = file.ContentHash
	}
	ed := edit.New(repoRoot)
	prep, probs, err := ed.PrepareDirect(ctx, target.FilePath, wantHash, []byte(newText))
	if err != nil {
		return failFrom(err, "prepare edit %s: %v", target.FilePath, err)
	}
	if len(probs) > 0 {
		if errors.Is(probs[0].Err, edit.ErrStale) {
			return editCommitFailure(probs[0].Err, target.FilePath)
		}
		return failWith("edit_error", probs[0].Reason, "%s", probs[0].Reason)
	}
	if err := ed.Commit(ctx, []edit.Prepared{prep}); err != nil {
		return editCommitFailure(err, target.FilePath)
	}
	s.invalidateIndex()

	resp.Items = append(resp.Items, Item{
		Kind: target.Kind, Name: target.Name, Path: target.FilePath, Line: target.StartLine,
		Why: "deleted_symbol", Excerpt: diffPreview,
	})
	return s.reply(ctx, req, resp)
}

func findGoBodyOffsets(src string, startLine int) (openBrace, closeBrace int, ok bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return 0, 0, false
	}
	for _, decl := range file.Decls {
		fn, isFn := decl.(*ast.FuncDecl)
		if !isFn || fn.Body == nil {
			continue
		}
		pos := fset.Position(fn.Pos())
		if pos.Line == startLine {
			lpos := fset.Position(fn.Body.Lbrace)
			rpos := fset.Position(fn.Body.Rbrace)
			return lpos.Offset, rpos.Offset, true
		}
	}
	return 0, 0, false
}

func findBodyBraces(spanText string) (openBrace, closeBrace int, ok bool) {
	parenDepth := 0
	bracketDepth := 0
	openIdx := -1
	for i, r := range spanText {
		switch r {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			if parenDepth == 0 && bracketDepth == 0 {
				openIdx = i
				goto foundOpen
			}
		}
	}
foundOpen:
	if openIdx < 0 {
		return 0, 0, false
	}
	closeIdx := strings.LastIndex(spanText, "}")
	if closeIdx <= openIdx {
		return 0, 0, false
	}
	return openIdx, closeIdx, true
}

func replacePythonBody(lines []string, startIdx, endIdx int, newBody string) string {
	colonLine := startIdx
	for i := startIdx; i <= endIdx && i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasSuffix(trimmed, ":") {
			colonLine = i
			break
		}
	}
	docstringEnd := colonLine
	bodyStart := colonLine + 1
	if bodyStart <= endIdx && bodyStart < len(lines) {
		trimmed := strings.TrimSpace(lines[bodyStart])
		if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
			quote := trimmed[:3]
			if strings.HasSuffix(trimmed[3:], quote) && len(trimmed) > 5 {
				docstringEnd = bodyStart
			} else {
				for j := bodyStart + 1; j <= endIdx && j < len(lines); j++ {
					if strings.Contains(lines[j], quote) {
						docstringEnd = j
						break
					}
				}
			}
		}
	}
	indent := "    "
	if docstringEnd+1 <= endIdx && docstringEnd+1 < len(lines) {
		line := lines[docstringEnd+1]
		trimmed := strings.TrimLeft(line, " \t")
		if len(line) > len(trimmed) {
			indent = line[:len(line)-len(trimmed)]
		}
	}
	var indentedBody strings.Builder
	for _, bl := range strings.Split(newBody, "\n") {
		if strings.TrimSpace(bl) == "" {
			indentedBody.WriteString("\n")
		} else {
			indentedBody.WriteString(indent)
			indentedBody.WriteString(strings.TrimLeft(bl, " \t"))
			indentedBody.WriteString("\n")
		}
	}

	var b strings.Builder
	for i := 0; i <= docstringEnd; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}
	b.WriteString(strings.TrimRight(indentedBody.String(), "\n"))
	if endIdx+1 < len(lines) {
		b.WriteString("\n")
		b.WriteString(strings.Join(lines[endIdx+1:], "\n"))
	}
	return b.String()
}

func generateUnifiedDiff(filePath string, before, after string) string {
	bLines := strings.Split(before, "\n")
	aLines := strings.Split(after, "\n")

	firstDiff := 0
	for firstDiff < len(bLines) && firstDiff < len(aLines) && bLines[firstDiff] == aLines[firstDiff] {
		firstDiff++
	}
	if firstDiff == len(bLines) && len(bLines) == len(aLines) {
		return ""
	}

	bEnd := len(bLines) - 1
	aEnd := len(aLines) - 1
	for bEnd > firstDiff && aEnd > firstDiff && bLines[bEnd] == aLines[aEnd] {
		bEnd--
		aEnd--
	}

	ctxStart := max(0, firstDiff-3)
	bCtxEnd := min(len(bLines), bEnd+4)
	aCtxEnd := min(len(aLines), aEnd+4)

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n",
		filePath, filePath, ctxStart+1, bCtxEnd-ctxStart, ctxStart+1, aCtxEnd-ctxStart)

	for i := ctxStart; i < firstDiff; i++ {
		sb.WriteString(" " + bLines[i] + "\n")
	}
	for i := firstDiff; i <= bEnd && i < len(bLines); i++ {
		sb.WriteString("-" + bLines[i] + "\n")
	}
	for i := firstDiff; i <= aEnd && i < len(aLines); i++ {
		sb.WriteString("+" + aLines[i] + "\n")
	}
	for i := bEnd + 1; i < bCtxEnd && i < len(bLines); i++ {
		sb.WriteString(" " + bLines[i] + "\n")
	}
	return sb.String()
}

// editCommitFailure explains a refused or failed write. A stale file is the
// common case after an earlier edit to the same file: the index still holds
// the old line ranges, and writing by them would corrupt the file.
func editCommitFailure(err error, path string) (*sdk.CallToolResult, *Response, error) {
	if errors.Is(err, edit.ErrStale) {
		return failWith("stale_index",
			"The file changed since it was indexed (an earlier edit, or another writer), so its line ranges "+
				"cannot be trusted. Changed files are re-indexed before the next call: retry this edit.",
			"%s changed since it was indexed: %v", path, err)
	}
	return failFrom(err, "commit %s: %v", path, err)
}

// editDeniedHint says why a write was refused and what would allow it: the
// caller's role on a shared server, or the server's own configuration.
func (s *Server) editDeniedHint() string {
	if s.deps.EditRole != "" {
		return "editing disabled for this token: its role is " + s.deps.EditRole +
			", and only editor or admin tokens may write. Pass dry_run=true to preview without writing, or ask the server's operator for an editor token."
	}
	return "editing disabled: write actions require --allow-edits on 'graphlume serve' or 'allow_edits: true' in user config. Pass dry_run=true to preview without writing."
}
