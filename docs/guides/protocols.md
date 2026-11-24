# Protocols: reach the same tools from any agent

GraphLume serves one set of tools over four surfaces. Pick the one your agent
can speak; the tools, their arguments and their answers are the same on each.

| Your agent | Use | Start it with |
|---|---|---|
| Speaks MCP and runs on your machine (Claude Code, Cursor, Codex, most others) | MCP over stdio | `graphlume install --rules` |
| Accepts only a remote MCP server (ChatGPT, Android Studio) | MCP over Streamable HTTP | `graphlume serve --http 127.0.0.1:7788` |
| Calls HTTP functions, or you are writing a script or bot | REST with JSON | `graphlume serve --http 127.0.0.1:7788` |
| Has a shell and nothing else (Aider, a CI step) | The command line | `graphlume tool call <tool> key=value` |

## Contents

- [One surface, one implementation](#one-surface-one-implementation)
- [MCP](#mcp)
- [REST](#rest)
- [OpenAPI](#openapi)
- [Command line](#command-line)
- [Profiles and what a tool costs](#profiles-and-what-a-tool-costs)
- [Audit log](#audit-log)
- [Sharing one index with a team](#sharing-one-index-with-a-team)
- [Security](#security)

## One surface, one implementation

The REST endpoint and `graphlume tool` do not have their own handlers. Each opens
an in-process MCP client against the server and calls `tools/list` and
`tools/call` exactly as a remote agent would. So a tool's argument validation,
its output bounds, the framing that marks repository text as untrusted and the
audit record are the same on every surface, because there is only one of each.

Two things differ, on purpose:

- **MCP sessions remember; REST and shell calls do not.** In an MCP session the
  server elides source it already sent in that conversation. A REST call or a
  shell invocation is a single question, so it always gets the full answer.
- **`graphlume tool` defaults to the `full` profile.** A profile keeps tool schemas
  out of a model's context. A shell pays nothing for a tool it does not run.

## MCP

Over stdio, register the server with your agent:

```bash
graphlume install --rules
```

Or add it by hand:

```json
{
  "mcpServers": {
    "graphlume": { "command": "graphlume", "args": ["--project", "/absolute/path/to/repo", "serve"] }
  }
}
```

Over HTTP, for clients that take only a URL:

```bash
graphlume serve --http 127.0.0.1:7788
```

The endpoint is `/mcp`. Send the token as `Authorization: Bearer <token>`, or as
the last path segment (`/mcp/<token>`) when the client cannot set headers. The
token comes from `GRAPHLUME_MCP_TOKEN`, or is generated for the run and written to
`.graphlume/mcp-token`. Details: [coding agents guide](coding-agents.md#remote-mcp-over-http).

The server also sends:

- **Instructions**, once per session: where to start, what the confidence tiers
  license, and that an empty result is not proof. Under 900 bytes, and they name
  only tools the active profile exposes.
- **Three prompts** a user can start by name: `plan_refactor`, `changes`
  and `get_oriented`. Each is the sequence of tool calls an experienced user
  would ask for.

## REST

The same listener serves the tools as plain HTTP at `/api/v1`.

| Method and path | Returns |
|---|---|
| `GET /api/v1` | What this server is, and links to the rest |
| `GET /api/v1/tools` | Every tool the active profile exposes, with its input schema and cost |
| `GET /api/v1/tools/{name}` | One tool |
| `POST /api/v1/tools/{name}` | Calls it. The body is the JSON arguments object |
| `GET /api/v1/openapi.json` | The same surface as an OpenAPI 3.1 document |
| `GET /healthz` | `{"status":"ok"}`. No token needed; reveals nothing else |

Call a tool:

```bash
curl -s -H "Authorization: Bearer $GRAPHLUME_MCP_TOKEN" \
     -d '{"name":"ParseConfig","min_confidence":"high"}' \
     http://127.0.0.1:7788/api/v1/tools/callers
```

The response:

```json
{
  "tool": "callers",
  "text": "=== SYSTEM METADATA ===\n2 caller(s) of ParseConfig ...",
  "result": { "summary": "2 caller(s) of ParseConfig ...", "items": [ ... ] },
  "is_error": false
}
```

`text` is what an MCP client receives. `result` is the same answer as data. Send
`Accept: text/plain` to get the text alone, ready to place in a model's context.

The first line of `text` is `result.summary`, and it is written to be safe to
act on by itself. When the index cannot stand behind the answer it opens with
`UNKNOWN`, `UNRELIABLE`, `UNVERIFIED`, `INCOMPLETE` or `AMBIGUOUS` and the
reason. The same judgement is in `result.reliability.level`, and
`result.verdict.status` is computed from it, so the two cannot disagree. See
[reading an answer](../reference/mcp-tools.md#reading-an-answer).

### What the status code means

| Status | Meaning |
|---|---|
| 200 | The tool answered. This includes "nothing found" and a tool's own refusal; check `is_error` |
| 400 | The body is not a JSON object |
| 401 | The token is missing, wrong or expired. The answer does not say which |
| 403 | The `Host` or `Origin` is not allowed, or an MCP session id belongs to another token |
| 404 | No tool has that name on the active profile, or in the token's [scope](#sharing-one-index-with-a-team) |
| 405 | Wrong method for the route |
| 413 | The arguments are over 1 MB |

A tool's own failure is reported in the body, not as a 4xx or 5xx, for the same
reason MCP reports it inside a successful response: a client that retries on a
server error must not retry a question whose answer is "that limit is negative".

## OpenAPI

`GET /api/v1/openapi.json` is generated from the registered tools on every
request, so it cannot drift from them. Each tool is one `POST` operation whose
`operationId` is the tool name and whose request schema is the tool's input
schema, unchanged. A client that turns operations into functions ends up with
the names an MCP client sees.

```bash
curl -s -H "Authorization: Bearer $GRAPHLUME_MCP_TOKEN" \
     http://127.0.0.1:7788/api/v1/openapi.json > graphlume-openapi.json
```

## Command line

```bash
graphlume tool list                       # every tool, its profile and its cost
graphlume tool describe trace   # description and arguments
graphlume tool call callers name=ParseConfig min_confidence=high
```

Arguments are `key=value` pairs, one JSON object, or both (pairs win):

```bash
graphlume tool call context --args '{"task":"add retry to the webhook sender"}'
git diff | graphlume tool call diff diff=@-
graphlume tool call deadcode path=internal/billing/ limit=10
```

| Form | Sent as |
|---|---|
| `key=value` | A string, unless the value is `true`, `false`, `null` or a number |
| `key:=<json>` | The JSON value, for arrays and objects |
| `key=@path` | The file's contents as a string; `@-` reads standard input |

`--json` prints `{tool, text, result, is_error}`. The exit status is 1 when the
tool reports a failure and 0 otherwise, including when the answer is "nothing
found".

A name shared by several definitions is not a failure. The tool answers with
`outcome: ambiguous_name` and one row per definition, each ending in the
arguments that select it, and exits 0:

```bash
graphlume tool call impact symbol=fromJson
graphlume tool call impact symbol=fromJson path=gson/src/main/java/com/google/gson/Gson.java line=850
```

`path` and `line` are accepted by every tool that takes `name` or `symbol`. A
name may also be written as an owner and a member (`Guard.AgainstNull`,
`Guard::AgainstNull`, `Mailer#deliver`).

A tool about one kind of thing, such as `implementations` (types), does not
answer for a definition of another kind that shares the name. It answers
`UNKNOWN`, with `outcome: kind_not_indexed` and no rows, and exits 0.

The list tools take `limit` (default 50, at most 200) and `offset`; an answer
that does not carry every row says how many exist and which offset reads on.

For a person at a terminal, the task commands (`graphlume callers`, `graphlume
dead-code`, `graphlume hotspots`) print tables instead. `graphlume tool` is for agents
and for seeing exactly what an agent was shown.

## Profiles and what a tool costs

Every exposed tool's name, description and input schema are placed in the
model's context on every request. `graphlume tool list` prints that cost, measured
on this build:

| Profile | Tools | Schema per request |
|---|---|---|
| `compact` | 1 (`graphlume`) | under 300 tokens (saves ~25 KB of prompt context) |
| `minimal` (default for `serve`) | 7 | about 2,000 tokens |
| `standard` | 18 | about 5,000 tokens |
| `full` | 28 | about 8,400 tokens |

Tokens are estimated as bytes divided by four. Set the profile in `.graphlume.yml`:

```yaml
mcp: { profile: standard }
```

The REST endpoint follows the same profile, because an OpenAPI document handed
to a model is a list of functions with the same cost.

A client that also forwards each tool's output schema to its model pays a
little more: about 10,200 tokens on `full`. Every tool advertises one compact
output schema; a full copy per tool used to make that figure about 21,500.
`graphlume tool list --json` reports both as `schema_bytes` and `wire_bytes`.

## Audit log

Record every tool call, on any surface, as one JSON line:

```bash
graphlume serve --audit-log .graphlume/audit.jsonl
export GRAPHLUME_AUDIT_LOG=~/.graphlume-audit.jsonl    # or for every invocation
```

```json
{"time":"2026-11-12T09:15:20Z","transport":"cli","tool":"callers","arguments":{"name":"openReadOnly"},"duration_ms":182,"outcome":"ok","result_bytes":1824,"structured_bytes":2490,"cited_files":9,"cited_file_bytes":154677}
```

| Field | Meaning |
|---|---|
| `transport` | `mcp`, `rest` or `cli` |
| `token_name` | The name of the token that made the call, on a server started with `--tokens-file`. Never the token or its hash. Absent otherwise |
| `tool` | The tool that was called |
| `arguments` | As sent, cut at 2 KB and marked `arguments_truncated` |
| `outcome` | `ok`, `tool_error` or `protocol_error`. A call to a tool outside the token's scope is recorded as `protocol_error`, like a call to a tool that does not exist |
| `result_bytes` | The size of the answer's text |
| `structured_bytes` | The size of the structured copy of the same answer |
| `cited_files`, `cited_file_bytes` | How many indexed files this answer named that no earlier answer in the same session had, and their total size when indexed. Left out when zero |

The log records the call and never the answer. The answer is repository content,
and copying it into a second file would double the places private source can
leak from. The sizes are numbers only: the paths of the cited files are part of
the answer and are not written. The file is created readable only by you.

### Reading it back

`graphlume stats` totals a log per tool, per transport and per day, lists the
slowest calls and gives the failure rate. It prints numbers and tool names and
never the arguments.

```bash
graphlume stats .graphlume/audit.jsonl
graphlume stats --since 2026-11-01 --json
```

An agent can ask for the same accounting of its own session with the
`stats` tool (`full` profile), which needs no log. Both report
usage: calls, answer sizes, an estimate of those sizes in tokens (bytes / 4)
and time. Neither reports a saving, because none has been measured. See
[`stats`](../CLI.md#stats) and
[`stats`](../reference/mcp-tools.md#stats).

The log is a flag and an environment variable, not a key in `.graphlume.yml`. That
file is part of the repository, and a repository must not be able to switch off
the record of what an agent read from it.

## Sharing one index with a team

One server, one index, several people and bots. Each gets a token of their own,
with its own tools and its own expiry, and the audit log says which one asked.

```bash
# 1. One token per person or bot. Each is printed once.
graphlume serve create-token --name ci       --profile minimal  --tokens-file team-tokens.jsonl
graphlume serve create-token --name reviewer --profile standard --expires 2027-01-31 --tokens-file team-tokens.jsonl
graphlume serve create-token --name docs-bot --profile standard \
      --tool search --tool definition --tokens-file team-tokens.jsonl
graphlume serve create-token --name dev-bot  --profile full --role editor --tokens-file team-tokens.jsonl

# 2. Serve the index with that file.
graphlume serve --http 127.0.0.1:7788 --tokens-file team-tokens.jsonl \
      --allowed-host code-index.example.internal --audit-log audit.jsonl
```

```console
MCP over HTTP: http://127.0.0.1:7788/mcp
REST API:      http://127.0.0.1:7788/api/v1  (OpenAPI: http://127.0.0.1:7788/api/v1/openapi.json)
Tokens: 4 named token(s) from team-tokens.jsonl, read once at start; restart to apply a change to the file
  ci: role viewer, profile minimal, no expiry
  reviewer: role viewer, profile standard, expires 2027-02-01T00:00:00Z
  docs-bot: role viewer, profile standard, only definition, search, no expiry
  dev-bot: role editor, profile full, no expiry
Send a token as 'Authorization: Bearer <token>'. The /mcp/<token> path form is off with --tokens-file.
```

Without `--tokens-file` nothing in this section applies: the server has the one
token described under [MCP](#mcp).

### The tokens file

One JSON object per line. Blank lines and lines starting with `#` are skipped.

```json
{"name":"ci","sha256":"<64 hex characters>","profile":"minimal"}
{"name":"docs-bot","sha256":"<64 hex characters>","profile":"standard","tools":["definition","search"],"expires":"2027-01-31"}
```

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Who or what the token is for. 1 to 63 of `a-z`, `0-9`, `.`, `_`, `-`. Unique in the file. This is what the audit log records |
| `sha256` | yes | The SHA-256 of the token, in hex. Never the token |
| `profile` | yes | `minimal`, `standard` or `full`: the tools the token may use |
| `tools` | no | Narrows the profile to these tools. Each must be a tool on that profile; the list can remove tools and never add one |
| `role` | no | `viewer` (the default), `editor` or `admin`. A viewer's editing tools only preview, even on a server started with `--allow-edits`. An editor's write when the server allows edits. An admin is an editor whose `projects` action lists every project registered on the server's machine. A role never grants what the server withholds |
| `expires` | no | A date (`2027-01-31`: the token works through that day, UTC) or an RFC 3339 time. Without it the token does not expire |

- **The file holds hashes, not tokens.** A token is 256 random bits, shown once
  by `create-token` and stored nowhere. A copy of the file lets nobody in. Lose
  a token and you make another; it cannot be recovered. The hash is unsalted,
  which is safe for a random token and not for a password: do not hash a
  secret of your own into the file.
- **The file is read once, when the server starts.** Adding a token, changing a
  scope and revoking a token (delete its line) all take a restart. There is no
  reload signal and no timer.
- **A file that cannot be read exactly stops the server.** A missing file, an
  empty one, a line that is not valid, an unknown field, a misspelt tool, a
  duplicate name, an unreadable date: each is an error that names the line it
  is on, and the server does not start. It never falls back to the single
  token or to no token. The error does not quote the line, so a token pasted
  where its hash belongs is not copied into a log.
- **Keep it private.** On macOS and Linux the server warns at start if other
  accounts can read the file, and says so differently if they can write it:
  whoever can write it can add a token for themselves. `create-token` creates
  it with mode 0600. Windows has no permission bits to check, so there is no
  warning there; restrict the file with its ACL.

### What a scope does

- **A tool outside the scope does not exist for that token.** It is absent
  from MCP `tools/list`, from `GET /api/v1/tools`, from the OpenAPI document
  and from the session instructions, and calling it gets the answer a made-up
  name gets: `404 unknown_tool` over REST, `unknown tool` over MCP. The two
  answers differ only in the name you sent, so calling names does not reveal
  which tools a token is being denied. Each scope is a tool server that never
  registered the other tools, not a filter in front of one that did.
- **The token's profile decides, not `mcp.profile`.** The profile in
  `.graphlume.yml` is repository content; on a shared server the operator's
  tokens file is the only thing that grants tools.
- **A token is accepted only as `Authorization: Bearer <token>`.** Not in the
  query string, not in a cookie, and not as `/mcp/<token>`: that path form is
  for a client on your own machine, and behind a proxy a token in a path is a
  token in the proxy's access log.
- **`GRAPHLUME_MCP_TOKEN` is ignored**, with a warning, and no token is
  generated. One unscoped token beside the scoped ones would be the way round
  them.
- **Missing, unknown and expired tokens get one answer**: `401`, the same body
  and headers. Expiry is checked on every request, so an MCP session opened in
  time stops working when its token expires.
- **An MCP session belongs to the token that opened it.** A request that quotes
  another token's session id gets `403`.
- **Every tool reads.** No tool writes to the repository or the index
  (`rename` and `plan` return edits for the caller to make), and
  the index is opened read-only. A shared token cannot change anything.

With `--audit-log`, every call carries the token's name:

```json
{"time":"2026-11-12T09:41:07Z","transport":"rest","token_name":"ci","tool":"callers","arguments":{"name":"ParseConfig"},"duration_ms":4,"outcome":"ok","result_bytes":1911}
```

### Behind a reverse proxy

GraphLume does not speak TLS. A bearer token sent over plain HTTP can be read
by anyone on the path, so a shared server belongs on loopback with a proxy that
terminates TLS in front of it:

```nginx
server {
    listen 443 ssl;
    server_name code-index.example.internal;
    ssl_certificate     /etc/ssl/code-index.crt;
    ssl_certificate_key /etc/ssl/code-index.key;

    location / {
        proxy_pass http://127.0.0.1:7788;
        proxy_set_header Host $host;   # checked against --allowed-host
        proxy_http_version 1.1;
        proxy_buffering off;           # MCP streams its responses
        proxy_read_timeout 1h;
    }
}
```

- Start the server with `--allowed-host code-index.example.internal`. Requests
  whose `Host` or `Origin` is anything else get `403`.
- Clients use `https://code-index.example.internal/mcp` and
  `https://code-index.example.internal/api/v1`, with the bearer header.
- Do not log the `Authorization` header in the proxy.
- Rate limiting, IP allowlists and client certificates belong in the proxy.
  GraphLume has none of them.
- `GET /healthz` needs no token and says only `{"status":"ok"}`, for the
  proxy's health check.

Listening on another address directly (`--http 0.0.0.0:7788`) is refused unless
you pass `--allow-remote`, the same rule as the single-token server. With a
tokens file the refusal says why, and with the flag the server starts and warns
that tokens cross the network in clear text. Use it only when the proxy is the
one thing that can reach that address.

### What it does not do

- **No SSO, no OAuth, no user directory.** A token is a secret you hand to
  someone. Its name is a label you chose, not an identity anything verified.
  Two people sharing a token are one name in the audit log.
- **No per-repository and no per-path permissions.** A server serves one index.
  A scope limits which tools a token can call, not what they can ask about: any
  token can ask its tools about any file in the index. To keep two repositories
  apart, run two servers with two tokens files.
- **No live reload.** Revoking a token is deleting its line and restarting.
- **No rate limits or quotas**, and no lockout after failed attempts. Tokens are
  256 random bits, so guessing is not the risk; put limits in the proxy if you
  need them.
- **No TLS**, as above.
- **No URL-only clients.** A client that cannot send a header (the reason
  `/mcp/<token>` exists) cannot use a shared server.

## Security

Both HTTP surfaces sit behind one guard:

- **A token on every request**, compared in constant time. REST accepts it only
  as a bearer header: a token in a path ends up in access logs. With
  `--tokens-file` there are several named tokens, each with a
  [scope](#sharing-one-index-with-a-team), and both surfaces take the header
  only.
- **A `Host` allowlist**, which blocks DNS rebinding. Loopback names are always
  allowed; `--allowed-host` adds a tunnel or proxy hostname.
- **An `Origin` check**, so a web page in your browser cannot drive the server.
- **Loopback only** unless you pass `--allow-remote`.

Responses carry `Cache-Control: no-store`. An internal failure returns a fixed
message, never the underlying error text. Threat model: [security](../security.md).
