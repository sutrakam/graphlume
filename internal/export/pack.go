package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	regpkg "github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/security"
	"github.com/sutrakam/graphlume/pkg/model"
)

type PackFormat string

const (
	PackFormatXML      PackFormat = "xml"
	PackFormatMarkdown PackFormat = "markdown"
	PackFormatJSON     PackFormat = "json"
)

type PackOptions struct {
	Format       PackFormat
	IncludeTree  bool
	MaxFileBytes int64
	PathPrefix   string
	Redact       bool
}

type PackResult struct {
	Content         string `json:"content,omitempty"`
	FileCount       int    `json:"file_count"`
	TotalBytes      int64  `json:"total_bytes"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Tree            string `json:"tree,omitempty"`
	RedactedCount   int    `json:"redacted_count"`
}

// GeneratePack bundles codebase files into an AI-ready XML, Markdown, or JSON prompt pack.
func GeneratePack(ctx context.Context, reg *regpkg.Registry, runID int64, opts PackOptions) (*PackResult, error) {
	var sb strings.Builder
	res, err := StreamPack(ctx, reg, runID, opts, &sb)
	if err != nil {
		return nil, err
	}
	res.Content = sb.String()
	return res, nil
}

// StreamPack bundles codebase files and writes the output directly to w,
// avoiding buffering the entire codebase representation in heap memory.
func StreamPack(ctx context.Context, reg *regpkg.Registry, runID int64, opts PackOptions, w io.Writer) (*PackResult, error) {
	files, err := reg.Files(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("read files from run %d: %w", runID, err)
	}

	maxBytes := opts.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = 500 * 1024 // 500 KB
	}

	var eligible []model.File
	for _, f := range files {
		if opts.PathPrefix != "" && !strings.HasPrefix(f.Path, opts.PathPrefix) {
			continue
		}
		if f.ParseStatus == model.ParseStatusSkipped && strings.Contains(f.ParseError, "binary") {
			continue
		}
		if f.SizeBytes > maxBytes {
			continue
		}
		eligible = append(eligible, f)
	}

	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].Path < eligible[j].Path
	})

	var paths []string
	for _, f := range eligible {
		paths = append(paths, f.Path)
	}
	tree := BuildASCIITree(paths)

	var totalBytes int64
	var fileCount int
	totalRedacted := 0

	switch opts.Format {
	case PackFormatJSON:
		if _, err := io.WriteString(w, "{\n  \"tree\": "); err != nil {
			return nil, err
		}
		treeJSON, _ := json.Marshal(tree)
		if _, err := w.Write(treeJSON); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, ",\n  \"files\": [\n"); err != nil {
			return nil, err
		}

		first := true
		for _, f := range eligible {
			content, err := reg.FileContent(ctx, runID, f.Path)
			if err != nil {
				// Fallback to excerpt if FileContent fails
				content, err = reg.FileExcerpt(ctx, runID, f.Path, 1, 100000)
				if err != nil {
					continue
				}
			}
			if opts.Redact {
				clean, findings := security.Redact(content)
				content = clean
				totalRedacted += len(findings)
			}

			if !first {
				if _, err := io.WriteString(w, ",\n"); err != nil {
					return nil, err
				}
			}
			first = false

			fileItemBytes, err := json.Marshal(map[string]string{
				"path":    f.Path,
				"content": content,
			})
			if err != nil {
				return nil, err
			}
			if _, err := io.WriteString(w, "    "); err != nil {
				return nil, err
			}
			if _, err := w.Write(fileItemBytes); err != nil {
				return nil, err
			}

			fileCount++
			totalBytes += int64(len(content))
		}

		footer := fmt.Sprintf("\n  ],\n  \"file_count\": %d,\n  \"total_bytes\": %d,\n  \"estimated_tokens\": %d,\n  \"redacted_count\": %d\n}\n",
			fileCount, totalBytes, int(totalBytes/4), totalRedacted)
		if _, err := io.WriteString(w, footer); err != nil {
			return nil, err
		}

	case PackFormatMarkdown:
		if _, err := io.WriteString(w, "# Codebase Context Pack\n\n"); err != nil {
			return nil, err
		}
		if opts.IncludeTree {
			if _, err := io.WriteString(w, "## Directory Structure\n\n```text\n"); err != nil {
				return nil, err
			}
			if _, err := io.WriteString(w, tree); err != nil {
				return nil, err
			}
			if _, err := io.WriteString(w, "\n```\n\n"); err != nil {
				return nil, err
			}
		}
		if _, err := io.WriteString(w, "## Files\n\n"); err != nil {
			return nil, err
		}

		for _, f := range eligible {
			content, err := reg.FileContent(ctx, runID, f.Path)
			if err != nil {
				content, err = reg.FileExcerpt(ctx, runID, f.Path, 1, 100000)
				if err != nil {
					continue
				}
			}
			if opts.Redact {
				clean, findings := security.Redact(content)
				content = clean
				totalRedacted += len(findings)
			}

			header := fmt.Sprintf("### %s\n\n", f.Path)
			if _, err := io.WriteString(w, header); err != nil {
				return nil, err
			}
			ext := strings.TrimPrefix(filepath.Ext(f.Path), ".")
			codeOpen := fmt.Sprintf("```%s\n", ext)
			if _, err := io.WriteString(w, codeOpen); err != nil {
				return nil, err
			}
			if _, err := io.WriteString(w, content); err != nil {
				return nil, err
			}
			if !strings.HasSuffix(content, "\n") {
				if _, err := io.WriteString(w, "\n"); err != nil {
					return nil, err
				}
			}
			if _, err := io.WriteString(w, "```\n\n"); err != nil {
				return nil, err
			}

			fileCount++
			totalBytes += int64(len(content))
		}

	default: // XML
		if _, err := io.WriteString(w, "<graphlume_pack>\n"); err != nil {
			return nil, err
		}
		if opts.IncludeTree {
			if _, err := io.WriteString(w, "  <directory_structure>\n"); err != nil {
				return nil, err
			}
			for _, line := range strings.Split(tree, "\n") {
				if _, err := fmt.Fprintf(w, "    %s\n", line); err != nil {
					return nil, err
				}
			}
			if _, err := io.WriteString(w, "  </directory_structure>\n"); err != nil {
				return nil, err
			}
		}
		if _, err := io.WriteString(w, "  <files>\n"); err != nil {
			return nil, err
		}

		for _, f := range eligible {
			content, err := reg.FileContent(ctx, runID, f.Path)
			if err != nil {
				content, err = reg.FileExcerpt(ctx, runID, f.Path, 1, 100000)
				if err != nil {
					continue
				}
			}
			if opts.Redact {
				clean, findings := security.Redact(content)
				content = clean
				totalRedacted += len(findings)
			}

			if _, err := fmt.Fprintf(w, "    <file path=%q>\n", f.Path); err != nil {
				return nil, err
			}
			if _, err := io.WriteString(w, content); err != nil {
				return nil, err
			}
			if !strings.HasSuffix(content, "\n") {
				if _, err := io.WriteString(w, "\n"); err != nil {
					return nil, err
				}
			}
			if _, err := io.WriteString(w, "    </file>\n"); err != nil {
				return nil, err
			}

			fileCount++
			totalBytes += int64(len(content))
		}
		if _, err := io.WriteString(w, "  </files>\n</graphlume_pack>\n"); err != nil {
			return nil, err
		}
	}

	return &PackResult{
		FileCount:       fileCount,
		TotalBytes:      totalBytes,
		EstimatedTokens: int(totalBytes / 4),
		Tree:            tree,
		RedactedCount:   totalRedacted,
	}, nil
}

// BuildASCIITree constructs an ASCII file hierarchy from a sorted list of relative paths.
func BuildASCIITree(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	type node struct {
		name     string
		children map[string]*node
		isDir    bool
	}

	newNode := func(name string, isDir bool) *node {
		return &node{
			name:     name,
			children: make(map[string]*node),
			isDir:    isDir,
		}
	}

	root := newNode("", true)

	for _, p := range paths {
		parts := strings.Split(filepath.ToSlash(p), "/")
		curr := root
		for i, part := range parts {
			isLast := i == len(parts)-1
			if _, exists := curr.children[part]; !exists {
				curr.children[part] = newNode(part, !isLast)
			}
			curr = curr.children[part]
		}
	}

	var lines []string
	var walk func(n *node, prefix string, isTail bool, isRoot bool)
	walk = func(n *node, prefix string, isTail bool, isRoot bool) {
		if !isRoot {
			connector := "├── "
			if isTail {
				connector = "└── "
			}
			name := n.name
			if n.isDir {
				name += "/"
			}
			lines = append(lines, prefix+connector+name)
		}

		childPrefix := prefix
		if !isRoot {
			if isTail {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}

		// Sort child names
		var names []string
		for k := range n.children {
			names = append(names, k)
		}
		sort.Strings(names)

		for i, k := range names {
			walk(n.children[k], childPrefix, i == len(names)-1, false)
		}
	}

	walk(root, "", true, true)
	return strings.Join(lines, "\n")
}
