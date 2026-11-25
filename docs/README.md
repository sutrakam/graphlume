# GraphLume documentation

Start with the [user guide](USER_GUIDE.md): it covers installation, setting up a
project, connecting an agent and every command by task. The rest of this page
lists every document, grouped by what you are trying to do.

## Get started

| Document | What it covers |
|---|---|
| [User guide](USER_GUIDE.md) | Install, set up, connect agents, and every command by task, with real output |
| [Installation](getting-started/installation.md) | Install script, Go toolchain, release archives, container image, offline installs and Windows package managers |
| [Agent integration](agent-integration.md) | The MCP registration and the guidance block an agent needs, and how to check both |
| [Coding agents](guides/coding-agents.md) | Every supported agent: which are configured automatically, which by hand, and which over HTTP |

## Use it

| Document | What it covers |
|---|---|
| [CLI reference](CLI.md) | Every command and flag, with exit codes |
| [Reading the output](guides/cli-output-guide.md) | What each command prints and how to read it |
| [MCP tools](reference/mcp-tools.md) | The 42 agent tools: arguments, profiles and how to read an answer |
| [Protocols](guides/protocols.md) | The same tools over MCP, HTTP with OpenAPI, and the shell; team servers and tokens |
| [The web portal](guides/portal-guide.md) | Every page of `graphlume ui` and how to read it |
| [Editor extension](guides/editor-extension.md) | Callers, impact, tests and implementations in VS Code |
| [User manual](user-manual.md) | The long-form manual: every workflow in depth, and every configuration key |
| [FAQ](FAQ.md) | Common questions |
| [Troubleshooting](troubleshooting.md) | Error messages, keyed to their exact text, with the fix |

## Run it in CI and at scale

| Document | What it covers |
|---|---|
| [CI/CD change-safety gates](operations/ci-cd-gates.md) | The GitHub Action, SARIF and pull-request comments |
| [Performance](performance.md) | Indexing speed, memory, and the settings that change them |
| [Scale testing](operations/scale-testing.md) | The size suite and its measurements at 250,000 symbols |
| [Verifying a release](operations/verifying-a-release.md) | Checking a download's signature, provenance and SBOM |
| [Release checklist](operations/release-checklist.md) | Every user journey a release must pass, and the test that holds it |
| [Security](security.md) | The threat model, path containment, secret redaction and what a repository can never make GraphLume run |

## Understand it

| Document | What it covers |
|---|---|
| [Accuracy](accuracy.md) | How resolution is measured against compiler indexes, and the results |
| [Benchmarking](BENCHMARKING.md) | How to reproduce every published number |
| [Benchmarks](../BENCHMARKS.md) | The published numbers, each with its source |
| [Comparing tools](how-to/compare-tools.md) | A fair, repeatable comparison against another code-intelligence tool |
| [Design and architecture](DESIGN_AND_ARCHITECTURE.md) | Principles, package layers and design patterns |
| [Architecture internals](architecture.md) | The layers, their boundaries, the invariants and the atomic swap |
| [Why not vector RAG](WHY_NOT_VECTOR_RAG.md) | Why a graph of proven relationships, rather than similarity search |
| [Non-goals](NON-GOALS.md) | What GraphLume deliberately does not do, and why |
| [Roadmap](ROADMAP.md) | What is planned and what is known to be open |

## Contribute

| Document | What it covers |
|---|---|
| [Contributing](../.github/CONTRIBUTING.md) | How to propose a change |
| [Development](development.md) | Setting up, the test tiers, the correctness gates and the engineering rules |
| [Git workflow](git-workflow.md) | Branches, commits, releases and migrations |
| [Naming conventions](reference/naming-conventions.md) | How tools, commands, flags and routes are named |
| [Code of conduct](../.github/CODE_OF_CONDUCT.md) | Community standards |
| [Security policy](../.github/SECURITY.md) | Reporting a vulnerability |
