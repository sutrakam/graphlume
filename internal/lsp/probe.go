package lsp

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sutrakam/graphlume/internal/indexer"
)

// ServerSpec describes a candidate language server for a language.
type ServerSpec struct {
	Language   indexer.Language
	Name       string
	Binary     string
	Args       []string
	LanguageID string
}

// candidateServers is the priority-ordered list of candidate LSP binaries per language.
var candidateServers = map[indexer.Language][]ServerSpec{
	indexer.LangCPP: {
		{
			Language:   indexer.LangCPP,
			Name:       "clangd",
			Binary:     "clangd",
			Args:       []string{"--background-index=false", "-j=2"},
			LanguageID: "cpp",
		},
		{
			Language:   indexer.LangCPP,
			Name:       "ccls",
			Binary:     "ccls",
			Args:       nil,
			LanguageID: "cpp",
		},
	},
	indexer.LangRust: {
		{
			Language:   indexer.LangRust,
			Name:       "rust-analyzer",
			Binary:     "rust-analyzer",
			Args:       nil,
			LanguageID: "rust",
		},
	},
	indexer.LangGo: {
		{
			Language:   indexer.LangGo,
			Name:       "gopls",
			Binary:     "gopls",
			Args:       nil,
			LanguageID: "go",
		},
	},
	indexer.LangPython: {
		{
			Language:   indexer.LangPython,
			Name:       "pyright",
			Binary:     "pyright-langserver",
			Args:       []string{"--stdio"},
			LanguageID: "python",
		},
		{
			Language:   indexer.LangPython,
			Name:       "pylsp",
			Binary:     "pylsp",
			Args:       nil,
			LanguageID: "python",
		},
		{
			Language:   indexer.LangPython,
			Name:       "jedi-language-server",
			Binary:     "jedi-language-server",
			Args:       nil,
			LanguageID: "python",
		},
	},
	indexer.LangJava: {
		{
			Language:   indexer.LangJava,
			Name:       "jdtls",
			Binary:     "jdtls",
			Args:       nil,
			LanguageID: "java",
		},
	},
	indexer.LangCSharp: {
		{
			Language:   indexer.LangCSharp,
			Name:       "csharp-ls",
			Binary:     "csharp-ls",
			Args:       nil,
			LanguageID: "csharp",
		},
		{
			Language:   indexer.LangCSharp,
			Name:       "omnisharp",
			Binary:     "OmniSharp",
			Args:       []string{"-lsp"},
			LanguageID: "csharp",
		},
	},
	indexer.LangTypeScript: {
		{
			Language:   indexer.LangTypeScript,
			Name:       "typescript-language-server",
			Binary:     "typescript-language-server",
			Args:       []string{"--stdio"},
			LanguageID: "typescript",
		},
	},
}

// GraphlumeBinDir returns the ~/.graphlume/bin directory.
func GraphlumeBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".graphlume", "bin")
}

// FindBinary searches for binary name on PATH and in ~/.graphlume/bin.
func FindBinary(binary string) (string, bool) {
	if binary == "" {
		return "", false
	}
	// 1. Check standard PATH
	if p, err := exec.LookPath(binary); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, true
		}
		return p, true
	}

	// 2. Check ~/.graphlume/bin
	if binDir := GraphlumeBinDir(); binDir != "" {
		target := filepath.Join(binDir, binary)
		if fi, err := os.Stat(target); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return target, true
		}
	}

	return "", false
}

// ProbeServer checks if an LSP server is available for the given language.
func ProbeServer(lang indexer.Language) (ServerSpec, string, bool) {
	candidates, ok := candidateServers[lang]
	if !ok {
		return ServerSpec{}, "", false
	}

	for _, spec := range candidates {
		if path, found := FindBinary(spec.Binary); found {
			return spec, path, true
		}
	}

	return ServerSpec{}, "", false
}

// AvailableServers returns all detected language servers on this machine.
func AvailableServers() map[indexer.Language]ServerSpec {
	out := make(map[indexer.Language]ServerSpec)
	for lang := range candidateServers {
		if spec, absPath, found := ProbeServer(lang); found {
			spec.Binary = absPath
			out[lang] = spec
		}
	}
	return out
}
