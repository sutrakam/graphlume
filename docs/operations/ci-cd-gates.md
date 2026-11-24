# CI/CD Change-Safety Gates

GraphLume enforces architectural boundaries, blindspot detection, and refactoring safety directly in GitHub Actions and GitLab CI.

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

## 2. SARIF 2.1.0 Integration

GraphLume outputs standard SARIF 2.1.0 via:
```bash
graphlume report sarif --out graphlume.sarif
```
This renders line-level annotations in GitHub Pull Request "Files Changed" tabs for:
- Dead code / uncalled public symbols
- Architectural layering violations (e.g. `domain` importing `infrastructure`)
- Database access from non-repository packages
- Unmapped HTTP endpoints
