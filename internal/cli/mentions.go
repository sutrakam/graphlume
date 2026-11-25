package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"

	"github.com/spf13/cobra"
)

// # Mentions
//
// Which documents talk about a symbol, and which symbols a document talks about.
// A MENTION, not a proven relationship: it is read from the text of Markdown
// files when asked, and nothing about it is stored, so it cannot go stale or
// differ between a full and an incremental index.
//
// A name written in `backticks` is a deliberate reference and is reported as
// one. A bare word is reported only when it is distinctive: long enough, and
// written the way an identifier is (camelCase, PascalCase or snake_case), since
// a plain word such as "service" is as likely prose as code.

// mentionSource is what a mentions query reads.
type mentionSource interface {
	DocFiles(ctx context.Context, runID int64) ([]registry.DocFile, error)
	DocSections(ctx context.Context, runID, fileID int64) ([]registry.DocSection, error)
	FileContent(ctx context.Context, runID int64, path string) (string, error)
	FindSymbolsByName(ctx context.Context, runID int64, name, kind string, limit int) ([]registry.SymbolHit, error)
}

// mention is one place a document names a symbol.
type mention struct {
	Doc      string `json:"doc"`
	Line     int    `json:"line"`
	Section  string `json:"section,omitempty"`
	Form     string `json:"form"` // "code" for a backticked name, "prose" for a bare one
	Fragment string `json:"text"`
}

const (
	formCode  = "code"
	formProse = "prose"
	// minMentionName is the shortest name looked for: shorter ones are words.
	minMentionName = 4
)

var identRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// distinctive reports whether a bare word is written the way an identifier is.
func distinctive(name string) bool {
	if len(name) < 6 {
		return false
	}
	hasUpper, hasLower := false, false
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		}
	}
	return strings.Contains(name, "_") || (hasUpper && hasLower)
}

// findMentions returns the lines of text that name `name`. Backticks and fenced
// code blocks are tracked so a name inside code is reported as code.
func findMentions(text, name string) []mention {
	if len(name) < minMentionName {
		return nil
	}
	var out []mention
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		for _, loc := range wordLocations(line, name) {
			form := formProse
			if inFence || oddBackticksBefore(line, loc) {
				form = formCode
			}
			if form == formProse && !distinctive(name) {
				continue
			}
			out = append(out, mention{Line: i + 1, Form: form, Fragment: strings.TrimSpace(line)})
			break // one report per line
		}
	}
	return out
}

// wordLocations returns where name occurs in line as a whole identifier.
func wordLocations(line, name string) []int {
	var out []int
	for from := 0; ; {
		i := strings.Index(line[from:], name)
		if i < 0 {
			return out
		}
		at := from + i
		end := at + len(name)
		before := at == 0 || !isIdentByte(line[at-1])
		after := end == len(line) || !isIdentByte(line[end])
		if before && after {
			out = append(out, at)
		}
		from = end
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// oddBackticksBefore reports whether position at is inside an inline code span.
func oddBackticksBefore(line string, at int) bool {
	return strings.Count(line[:at], "`")%2 == 1
}

// sectionAt names the heading a line falls under: the last one that starts at or
// before it.
func sectionAt(sections []registry.DocSection, line int) string {
	name := ""
	for _, s := range sections {
		if s.StartLine <= line {
			name = s.Name
		} else {
			break
		}
	}
	return name
}

// docsMentioning finds every document line that names a symbol.
func docsMentioning(ctx context.Context, src mentionSource, runID int64, name string, limit int) ([]mention, error) {
	docs, err := src.DocFiles(ctx, runID)
	if err != nil {
		return nil, err
	}
	var out []mention
	for _, d := range docs {
		text, err := src.FileContent(ctx, runID, d.Path)
		if err != nil {
			continue // a document that cannot be read says nothing
		}
		found := findMentions(text, name)
		if len(found) == 0 {
			continue
		}
		sections, err := src.DocSections(ctx, runID, d.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range found {
			m.Doc = d.Path
			m.Section = sectionAt(sections, m.Line)
			out = append(out, m)
		}
	}
	// Names in code first, then by path and line, so the order is the same every run.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Form != out[j].Form {
			return out[i].Form == formCode
		}
		if out[i].Doc != out[j].Doc {
			return out[i].Doc < out[j].Doc
		}
		return out[i].Line < out[j].Line
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// docSymbol is a symbol a document names.
type docSymbol struct {
	Name        string `json:"name"`
	Definitions int    `json:"definitions"`
	Where       string `json:"where,omitempty"`
	Lines       []int  `json:"lines"`
}

// symbolsInDoc lists the symbols a document names in inline code, with how many
// definitions each name has (one is a clean link; more are ambiguous). A name is
// taken only if it is written like an identifier or used as a call.
func symbolsInDoc(ctx context.Context, src mentionSource, runID int64, path string) ([]docSymbol, error) {
	text, err := src.FileContent(ctx, runID, path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%q is not a file in the index: give its path from the project root, such as docs/adr/0007.md", path)
	}
	if err != nil {
		return nil, err
	}
	lines := map[string][]int{}
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		parts := strings.Split(line, "`")
		for p := 1; p < len(parts); p += 2 { // the odd parts are between backticks
			span := parts[p]
			for _, loc := range identRE.FindAllStringIndex(span, -1) {
				tok := span[loc[0]:loc[1]]
				// A plain word in backticks (`data`, `code`) names no symbol in
				// particular; an identifier, or a name used as a call, does.
				called := loc[1] < len(span) && span[loc[1]] == '('
				if len(tok) >= minMentionName && (distinctive(tok) || called) {
					lines[tok] = append(lines[tok], i+1)
				}
			}
		}
	}
	names := make([]string, 0, len(lines))
	for n := range lines {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []docSymbol
	for _, n := range names {
		defs, err := src.FindSymbolsByName(ctx, runID, n, "", 5)
		if err != nil {
			return nil, err
		}
		if len(defs) == 0 {
			continue
		}
		ds := docSymbol{Name: n, Definitions: len(defs), Lines: dedupeInts(lines[n])}
		if len(defs) == 1 {
			ds.Where = fmt.Sprintf("%s:%d", defs[0].FilePath, defs[0].StartLine)
		}
		out = append(out, ds)
	}
	return out, nil
}

func dedupeInts(in []int) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, v := range in {
		if _, dup := seen[v]; !dup {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func writeMentions(w io.Writer, name string, ms []mention) {
	if len(ms) == 0 {
		fmt.Fprintf(w, "No document names %q.\n", name)
		return
	}
	for _, m := range ms {
		sec := ""
		if m.Section != "" {
			sec = "  [" + m.Section + "]"
		}
		fmt.Fprintf(w, "%s:%d%s  (%s)\n", m.Doc, m.Line, sec, m.Form)
	}
	fmt.Fprintf(w, "\n%d mention(s) of %q. These are mentions found in the text, not proven relationships.\n", len(ms), name)
}

func writeDocSymbols(w io.Writer, doc string, ss []docSymbol) {
	if len(ss) == 0 {
		fmt.Fprintf(w, "%s names no symbol the index defines in inline code.\n", doc)
		return
	}
	for _, s := range ss {
		switch s.Definitions {
		case 1:
			fmt.Fprintf(w, "%-30s %s  (lines %v)\n", s.Name, s.Where, s.Lines)
		default:
			fmt.Fprintf(w, "%-30s %d+ definitions: ambiguous  (lines %v)\n", s.Name, s.Definitions, s.Lines)
		}
	}
}

func newMentionsCmd(g *Global) *cobra.Command {
	var doc string
	var limit int
	cmd := &cobra.Command{
		Use:   "mentions [name]",
		Short: "Which documents name a symbol, or which symbols a document names",
		Long: `Reads the Markdown files the index holds and reports where prose and code meet.

  graphlume mentions TransactionService      documents that name the symbol
  graphlume mentions --doc docs/adr/0007.md  symbols that document names in backticks

A name in backticks or a code block is a deliberate reference. A bare word counts
only if it is written like an identifier (camelCase, PascalCase or snake_case).
These are mentions found in the text when you ask, not proven relationships, and
nothing is stored.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			switch {
			case doc == "" && len(args) == 0:
				return errors.New("give a symbol name, or --doc <file>")
			case doc != "" && len(args) > 0:
				return errors.New("give a symbol name or --doc, not both")
			}
			s, err := openQuery(ctx, g)
			if err != nil {
				return err
			}
			defer s.close()
			if doc != "" {
				ss, err := symbolsInDoc(ctx, s.reg, s.runID, doc)
				if err != nil {
					return err
				}
				if g.JSON {
					if ss == nil {
						ss = []docSymbol{}
					}
					return emitJSON(cmd.OutOrStdout(), map[string]any{"index_revision": s.runID, "doc": doc, "symbols": ss})
				}
				writeDocSymbols(cmd.OutOrStdout(), doc, ss)
				return nil
			}
			name := strings.TrimSpace(args[0])
			ms, err := docsMentioning(ctx, s.reg, s.runID, name, limit)
			if err != nil {
				return err
			}
			if g.JSON {
				if ms == nil {
					ms = []mention{}
				}
				return emitJSON(cmd.OutOrStdout(), map[string]any{"index_revision": s.runID, "name": name, "mentions": ms})
			}
			writeMentions(cmd.OutOrStdout(), name, ms)
			return nil
		},
	}
	cmd.Flags().StringVar(&doc, "doc", "", "list the symbols this document names, instead of the documents naming a symbol")
	cmd.Flags().IntVarP(&limit, "limit", "n", 30, "maximum mentions to report")
	return cmd
}
