package indexer

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	scriptTagRegex = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script>`)
)

// SvelteShadow handles converting .svelte component files into TypeScript shadow
// files so that scip-typescript can index their definitions and calls.
type SvelteShadow struct {
	ShadowDir string
	Count     int
}

// ConvertSvelteSource converts a .svelte source string into a TypeScript source
// preserving line numbers for script contents.
func ConvertSvelteSource(src []byte) []byte {
	lines := bytes.Split(src, []byte("\n"))
	outLines := make([][]byte, len(lines))

	// Find script blocks
	matches := scriptTagRegex.FindAllSubmatchIndex(src, -1)
	if len(matches) == 0 {
		// Output empty lines matching file length
		for i := range outLines {
			outLines[i] = []byte("")
		}
		return bytes.Join(outLines, []byte("\n"))
	}

	for i := range outLines {
		outLines[i] = []byte("")
	}

	for _, m := range matches {
		contentStart := m[2]
		contentEnd := m[3]
		if contentStart < 0 || contentEnd < contentStart {
			continue
		}

		// Calculate start line of the content
		prefix := src[:contentStart]
		startLine := bytes.Count(prefix, []byte("\n"))

		scriptBytes := src[contentStart:contentEnd]
		scriptLines := bytes.Split(scriptBytes, []byte("\n"))

		for j, sl := range scriptLines {
			targetLine := startLine + j
			if targetLine < len(outLines) {
				outLines[targetLine] = sl
			}
		}
	}

	return bytes.Join(outLines, []byte("\n"))
}

// PrepareSvelteShadow scans projectDir for .svelte files, converts them to
// line-aligned .ts files in a shadow folder under outDir, and returns the
// shadow directory path and count of converted files.
func PrepareSvelteShadow(repoRoot, projectDir, outDir string) (SvelteShadow, error) {
	shadow := SvelteShadow{
		ShadowDir: filepath.Join(outDir, ".graphlume", "shadow", "svelte"),
	}

	var svelteFiles []string
	err := filepath.WalkDir(projectDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if skipDirs[name] || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".svelte") {
			rel, relErr := filepath.Rel(projectDir, p)
			if relErr == nil {
				svelteFiles = append(svelteFiles, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		return shadow, fmt.Errorf("walk svelte files: %w", err)
	}

	if len(svelteFiles) == 0 {
		return shadow, nil
	}

	for _, rel := range svelteFiles {
		srcPath := filepath.Join(projectDir, filepath.FromSlash(rel))
		srcBytes, err := os.ReadFile(filepath.Clean(srcPath)) //nolint:gosec // reading repo component files for shadow generation
		if err != nil {
			continue
		}

		converted := ConvertSvelteSource(srcBytes)
		dstPath := filepath.Join(shadow.ShadowDir, filepath.FromSlash(rel)+".ts")
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
			return shadow, fmt.Errorf("mkdir svelte shadow: %w", err)
		}
		if err := os.WriteFile(dstPath, converted, 0o600); err != nil {
			return shadow, fmt.Errorf("write svelte shadow: %w", err)
		}
		shadow.Count++
	}

	return shadow, nil
}
