# Installing GraphLume: instructions for an AI coding agent

Follow these steps in order to install GraphLume, index the current repository
and register it as an MCP server for the agents on this machine. Every command
below was tested. Ask the user before step 2 if your instructions require
approval to install software.

## 1. Check whether it is already installed

```bash
graphlume version
```

If this prints a version, skip to step 3.

## 2. Install the binary

macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/sutrakam/graphlume/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/sutrakam/graphlume/main/install.ps1 | iex
```

The script verifies the download against the release checksums and installs to
`~/.local/bin` on macOS and Linux. If `graphlume version` is not found
afterwards, add that directory to `PATH`. With Go 1.26 or later you can instead
run `go install github.com/sutrakam/graphlume/cmd/graphlume@latest`.

## 3. Index the repository and register the server

From the repository root:

```bash
graphlume init --all --yes
```

This builds the index in `.graphlume/`, binds references with the compilers
available, registers the MCP server with every agent it detects and adds a
guidance block to `AGENTS.md`. Without a terminal, `--yes` accepts only the
small, pinned, checksum-verified compiler indexers; it never installs a
toolchain or runs the repository's build.

To register only this project, without touching the user's home directory, add
`--local`. To preview the agent configs first, run `graphlume install --dry-run`.

## 4. Verify

```bash
graphlume status
graphlume agent --check
```

`status` must say `Index: READY`. `agent --check` must show `ok` for the index,
the MCP registration and the guidance. Each warning names the command that fixes
it. Then tell the user to restart their agent so it loads the new server.

## Manual registration

If step 3 did not detect the agent, register the server by hand. Every client
runs the same command: `graphlume serve`, with `--project <repository path>`
before `serve` when the client does not start in the repository.

Claude Code:

```bash
claude mcp add graphlume -- graphlume serve
```

Cursor (`~/.cursor/mcp.json` or `.cursor/mcp.json`), Windsurf
(`~/.codeium/windsurf/mcp_config.json`), Claude Desktop, Cline, Roo Code and
other clients that read `mcpServers`:

```json
{
  "mcpServers": {
    "graphlume": {
      "command": "graphlume",
      "args": ["serve"]
    }
  }
}
```

Codex CLI (`~/.codex/config.toml`):

```toml
[mcp_servers.graphlume]
command = "graphlume"
args = ["serve"]
```

`graphlume install --list` prints the exact file and format for every supported
agent on this machine.

## Choosing the tool set

`graphlume serve` exposes 8 tools (`minimal`, about 2,420 tokens of schema).
Add `--profile standard` (19 tools, adds impact, tests and rename planning),
`--profile full` (42 tools) or `--profile compact` (one front-door tool, about
720 tokens) to the `args`. `graphlume init --all` registers `compact`.

## Removing it

```bash
graphlume clean --dry-run
graphlume clean --agents
```

`clean --agents` removes the registration from every agent. `graphlume uninstall
--self` removes everything, including the binary.
