# CUE known gaps

## Top-level field with a quoted label

```
"quoted-field": true     ->  (field (label (string)) (value (boolean)))
```

The label is a `string` node, so the name would carry its quotes and its
escapes. Not extracted. A label that is an identifier is.

## Nested regular fields

Only definitions (`#Name`) are indexed at depth. A regular field inside a
struct (`spec: { replicas: 3 }`) is not, because every struct in a large
configuration repeats the same field names and the index would hold hundreds
of `name` entries that mean nothing alone.

## Several labels on one field

`cluster: region: "eu-west-1"` is one `field` with two `label` children. Only
the first (`cluster`) is a definition.
