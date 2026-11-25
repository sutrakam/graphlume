# YAML: forms not extracted

- **Keys.** No mapping key is a definition, top level or nested. Measured on
  this repository's own YAML, the keys are not "declared once": `name`, `run`,
  `uses`, `with` and `steps` repeat in every workflow, and the configuration
  analyser already records every key of every `.yaml` / `.yml` file as a
  `configuration` symbol (`internal/analyze/config`, `yamlKeys`). A tree-sitter
  rule on top would double the count and flood name lookups.
- **Deployment units and services.** Compose services, Kubernetes resources
  and Helm values are `deployment_unit` / `configuration` symbols from
  `internal/analyze/infra`. They are not repeated here.
- **Anchors and aliases are not linked.** Only the anchor is a definition; an
  alias (`*name`) is not recorded as a reference, so "who uses this anchor" is
  answered by text search.
- **Tags** (`!Ref`, `!!str`, custom constructors) are not definitions.
- **Block scalars and strings** that contain `&word` are text, not anchors; the
  grammar does not produce an `anchor` node for them (see `anchors.yaml`).
- **A `.yaml` file the walker does not detect as YAML** (for example a
  templated Helm chart with `{{ }}` blocks that makes the file a different
  language or a parse error) is not read; anchors inside the unreadable regions
  of a file with syntax errors may be missing.
