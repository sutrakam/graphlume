# Puppet known gaps

## `plan`

The grammar has no plan declaration. `plan webserver::rollout(TargetSpec $targets) { ... }`
parses as loose statements and an error, and nothing in the tree names the plan:

```
(identifier) (function_call (class_identifier (identifier) (identifier)) (identifier)) (variable (identifier)) (ERROR) (hash (ERROR ...))
```

Not extracted. `plan` is in `init.pp` as the last declaration so the failure
cannot hide a later one.

## `node /regex/`

A regular-expression node name is not accepted:

```
node /^db\d+\.example\.com$/ {   ->   (ERROR (regex)) (identifier) (class_identifier ...) (ERROR)
```

The block that follows is read as loose statements. Quoted names and
`default` are extracted.

## `class { 'name': }` is parsed as a class definition

The grammar reads a class resource declaration as `class_definition` with a
block, and its name node spans the whole text. The query keeps a class name
only if it is made of identifier characters and `::`, which drops this form.
A real class whose name the grammar cannot read cleanly would be dropped the
same way.

## Parse status

Resource-heavy manifests often come back as partial (the grammar rejects
some attribute forms). A definition inside a region the parser could not read
can be missing.
