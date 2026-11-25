# CI/CD change-safety gates

GraphLume checks a pull request in CI: what the change reaches, whether it breaks
the architecture and policy declared in `.graphlume.yml`, and where the index is
blind. It reports the result as a pull-request comment and as SARIF for code
scanning, and can fail the check.

---

## 1. GitHub Actions Workflow (`graphlume-gate.yml`)

Add `.github/workflows/graphlume-gate.yml` to your repository (the same file is in [docs/examples/graphlume-gate.yml](../examples/graphlume-gate.yml)):

```yaml
name: GraphLume

on:
  pull_request:
    branches: [main]

permissions:
  contents: read
  pull-requests: write
  security-events: write

concurrency:
  group: graphlume-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  change-safety:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: sutrakam/graphlume@v1
        with:
          fail-on: high
      - uses: github/codeql-action/upload-sarif@v4
        if: always() && github.event.pull_request.head.repo.full_name == github.repository
        with:
          sarif_file: graphlume-out/graphlume.sarif
          category: graphlume
```

---

The action indexes the pull request, imports compiler facts where it can,
posts one sticky comment with the blast radius, and writes
`graphlume-out/graphlume.sarif`. `fetch-depth: 0` is required: the gate compares
against the base branch and reads history. Its inputs (`fail-on`, `version`,
`accuracy`, `comment-pr`, `sarif-out`, `pr-report-out`, `upload-index`) are listed
in the [user guide](../USER_GUIDE.md#13-gate-pull-requests-in-ci) and in
[`action.yml`](../../action.yml).

---

## 2. SARIF and the pull-request comment

`graphlume report` turns other commands' `--json` output into the two artefacts
CI needs:

```bash
graphlume --json blindspots > blindspots.json
graphlume --json arch > arch.json
graphlume report sarif --blindspots blindspots.json --arch arch.json --out graphlume.sarif

graphlume --json impact --base origin/main --head HEAD > impact.json
graphlume report pr --impact impact.json --blindspots blindspots.json --arch arch.json --out comment.md
```

The SARIF run holds blind spots and architecture violations, each at its file
and line, so code scanning annotates them on the pull request. The comment
carries a sticky marker, so a later run updates it rather than adding another.

---

## 3. Any other CI system

The same check is one command, with no GitHub-specific parts:

```bash
graphlume init && graphlume index && graphlume resolve
graphlume ci check --base origin/main --format sarif --out graphlume.sarif --fail-on-violations
```

`ci check` writes `markdown`, `sarif` or `json`. With `--fail-on-violations` it
exits 2 when an architecture rule or an error-severity rule of the `policy:`
block is broken (see `graphlume policy explain`). `arch --fail-on <severity>`
and `blindspots --fail-on <severity>` gate on one check each.

---

## 4. Share the index the gate builds

On `push` runs, set `upload-index: true`. The action uploads the verified index
as the artifact `graphlume-index-<commit sha>`, and developers and agents fetch
it with `graphlume index pull` instead of indexing locally.
