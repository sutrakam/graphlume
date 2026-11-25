# Circom: known gaps

## `bus` declarations

`bus Point() { ... }` (Circom 2.2) is a syntax error in this grammar and the
declaration is not extracted.

```
(ERROR (ERROR) (ERROR) (ERROR))
```

## Not extracted by choice

Signals (inputs, outputs and intermediates), variables and `component x = T()`
instances: they are locals of a template.
