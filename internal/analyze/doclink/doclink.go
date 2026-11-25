package doclink

import (
	"path/filepath"
	"regexp"
	"strings"
)

// DocMention represents a backticked symbol identifier discovered in a document.
type DocMention struct {
	SymbolName string
	FilePath   string
	Line       int
}

var backtickPattern = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_.]*)`")

// IsDocFile reports whether path is a documentation, README, or ADR file.
func IsDocFile(relPath string) bool {
	norm := filepath.ToSlash(relPath)
	lower := strings.ToLower(norm)
	if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
		return false
	}
	base := filepath.Base(lower)
	if strings.HasPrefix(base, "readme") || strings.HasPrefix(base, "architecture") {
		return true
	}
	return strings.HasPrefix(lower, "docs/") ||
		strings.HasPrefix(lower, "doc/") ||
		strings.HasPrefix(lower, "adr/") ||
		strings.Contains(lower, "/docs/") ||
		strings.Contains(lower, "/doc/") ||
		strings.Contains(lower, "/adr/")
}

// ExtractMentions scans markdown text for backticked code identifiers.
func ExtractMentions(filePath string, text []byte) []DocMention {
	var mentions []DocMention
	lines := strings.Split(string(text), "\n")
	for lineIdx, line := range lines {
		matches := backtickPattern.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) > 1 {
				ident := m[1]
				// Filter out common prose words, markdown syntax, or short keywords
				if len(ident) >= 3 && !isStopWord(ident) {
					mentions = append(mentions, DocMention{
						SymbolName: ident,
						FilePath:   filePath,
						Line:       lineIdx + 1,
					})
				}
			}
		}
	}
	return mentions
}

func isStopWord(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "null", "nil", "undefined", "string", "int", "bool", "void", "return", "const", "var", "let", "function", "class", "import", "export", "type", "interface", "struct", "error":
		return true
	default:
		return false
	}
}
