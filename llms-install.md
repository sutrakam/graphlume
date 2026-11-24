# GraphLume MCP Server Installation Guide for AI Agents

This guide is designed for AI coding assistants (Claude Code, Cursor, Codex, Windsurf, Cline, Roo Code, GitHub Copilot) to install and configure the GraphLume Model Context Protocol (MCP) server.

## Overview

GraphLume is a local-first, compiler-verified code graph engine. It provides AI agents with instant symbol lookups, call hierarchies (callers/callees), blast radius calculations, API routes, database tables, and automated secret redaction over the Model Context Protocol (MCP).

## Automated Setup (Recommended)

GraphLume can automatically detect and configure all installed AI agents with a single command:

```bash
# Preview changes without modifying files
graphlume install --dry-run

# Automatically write configs and agent guidance rules
graphlume install --rules
```

To configure a specific agent:
```bash
graphlume install --only claude-code --rules
graphlume install --only cursor --rules
```

---

## Manual Configuration

If configuring manually, add GraphLume to your agent's MCP settings configuration file.

### 1. Claude Code
**Config Location:** `~/.claude.json`

```json
{
  "mcpServers": {
    "graphlume": {
      "command": "graphlume",
      "args": ["serve", "--mcp"]
    }
  }
}
```

### 2. Cursor
**Config Location:** `~/.cursor/mcp.json` or `.cursor/mcp.json` (project-level)

```json
{
  "mcpServers": {
    "graphlume": {
      "command": "graphlume",
      "args": ["serve", "--mcp"]
    }
  }
}
```

### 3. Codex CLI
**Config Location:** `~/.codex/config.toml`

```toml
[mcp_servers.graphlume]
command = "graphlume"
args = ["serve", "--mcp"]
```

### 4. VS Code (Cline / Roo Code)
**Config Location:** 
- Cline: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json`
- Roo Code: `~/Library/Application Support/Code/User/globalStorage/rooveterinaryinc.roo-cline/settings/cline_mcp_settings.json`

```json
{
  "mcpServers": {
    "graphlume": {
      "command": "graphlume",
      "args": ["serve", "--mcp"],
      "disabled": false,
      "autoApprove": []
    }
  }
}
```

### 5. Windsurf / Devin Desktop
**Config Location:** `~/.codeium/windsurf/mcp_config.json`

```json
{
  "mcpServers": {
    "graphlume": {
      "command": "graphlume",
      "args": ["serve", "--mcp"]
    }
  }
}
```

---

## Verifying Server Status

Run the doctor command in any indexed workspace to verify the installation:

```bash
graphlume doctor
```

Check agent tool readiness:
```bash
graphlume agent check
```
