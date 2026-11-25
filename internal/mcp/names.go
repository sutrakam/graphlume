package mcp

import "strings"

// ---------------------------------------------------------------- tool names
//
// A TOOL NAME IS READ BY A MODEL ON EVERY REQUEST, BEFORE ITS DESCRIPTION.
//
// Tools follow clean, concise single-word enterprise primitives (e.g. context,
// callers, symbol, definition, explore, references, impact, tests, trace, deadcode).
//
// THERE ARE NO ALIASES. A tool has one name.

var validToolNames = map[string]bool{
	"overview":        true,
	"search":          true,
	"symbol":          true,
	"definition":      true,
	"explore":         true,
	"references":      true,
	"callers":         true,
	"rename":          true,
	"context":         true,
	"impact":          true,
	"patch":           true,
	"tests":           true,
	"endpoints":       true,
	"tables":          true,
	"coverage":        true,
	"config":          true,
	"outline":         true,
	"diff":            true,
	"plan":            true,
	"blindspots":      true,
	"changes":         true,
	"owners":          true,
	"busfactor":       true,
	"stats":           true,
	"callees":         true,
	"implementations": true,
	"trace":           true,
	"deadcode":        true,
	"hotspots":        true,
	"communities":     true,
	"graph":           true,
	"estate":          true,
	"structure":       true,
	"policy":          true,
	"arch":            true,
	"deps":            true,
	"licenses":        true,
	"vulns":           true,
	"sbom":            true,
	"tour":            true,
	"adr":             true,
	"lessons":         true,
	"events":          true,
	"graphlume":       true,
}

var validPromptNames = map[string]bool{
	"plan_refactor": true,
	"review_change": true,
	"get_oriented":  true,
}

// ValidToolName reports whether name follows the GraphLume tool naming convention.
func ValidToolName(name string) bool {
	if name == "" || name != strings.ToLower(name) {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	if strings.Contains(name, "__") || strings.HasSuffix(name, "_") || strings.HasPrefix(name, "_") {
		return false
	}
	return validToolNames[name]
}

// ValidPromptName reports whether name follows the prompt naming convention.
func ValidPromptName(name string) bool {
	return validPromptNames[name]
}

// methodCallTool is the JSON-RPC method the audit middleware acts on.
const methodCallTool = "tools/call"
