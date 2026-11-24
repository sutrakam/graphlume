# Dockerfile: no definition query is registered

A Dockerfile declares three things a developer might look up by name: a build
stage (`FROM image AS name`), a build argument (`ARG NAME`) and an environment
variable (`ENV NAME=value`). All three are already symbols, produced by the
deployment analyser, so a tree-sitter query would index each of them twice.
No query is registered and there is no `expected.tsv`.

## What the index already holds

`internal/analyze/infra/dockerfile.go` reads every Dockerfile (any file named
`Dockerfile`, `Containerfile`, `*.dockerfile` or `Dockerfile.*`, see
`infra.Candidate`) line by line. `internal/pipeline/infra.go` (`infraSymbols`)
turns what it finds into symbols:

- each `FROM` is a `deployment_unit` named by its `AS` alias, or `stage1`,
  `stage2`, ... when it has none;
- each `ARG` and `ENV` name is a `configuration` symbol, FQN `config:<NAME>`,
  the same node a `.env` declaration of that name produces.

On this repository's own `Dockerfile` (a self-index before any query existed):

```
configuration    GO_VERSION   25      ARG GO_VERSION=1.26.11
deployment_unit  build        27      FROM ... AS build
configuration    TARGETOS     29      ARG TARGETOS
deployment_unit  stage2       61      FROM scratch
...
```

A grammar query for `image_alias` (class) and `arg_instruction` /
`env_pair` (variable) would add a second symbol of kind `class` or `variable`
for each of those lines. The two would have different FQNs, so they would not
merge: a lookup of `build` or `VERSION` would return both, and the
symbol count would grow for nothing. The analyser's symbols also carry what
the grammar's do not: stage numbering for unnamed stages, the link from a stage
to the keys it sets, and the same `config:` node a `.env` or a Kubernetes
manifest uses.

## What the tree has

```
FROM --platform=$BUILDPLATFORM golang:${V} AS build
  (from_instruction (param) (image_spec (image_name) (image_tag ...)) (image_alias))

ENV A=1 B=2
  (env_instruction (env_pair (unquoted_string) (unquoted_string)) ...)

ARG V=1
  (arg_instruction (unquoted_string) (unquoted_string))
```

`ARG` and `ENV` names are bare `unquoted_string` leaves, not told apart from the
value without position, so a query would also need an anchor on the first child.

## Pipeline behaviour

Registering a query would not switch the analyser off (`processFile` in
`internal/pipeline/pipeline.go` runs `infra.Parse` for a Dockerfile whether or
not its grammar has definitions), so the duplication above is the only cost of
adding one. If a Dockerfile stage should one day be a code-graph `class`
instead of a `deployment_unit`, that is a decision about the analyser, not about
this grammar.
