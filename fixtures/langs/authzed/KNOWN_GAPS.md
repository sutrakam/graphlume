# Authzed: known gaps

## Caveats with an `ipaddress` parameter

`ipaddress` is a SpiceDB caveat type, but this grammar does not know it. A
caveat that declares one is a syntax error, and the whole file is lost with it:
the caveat and every definition in the same file, before and after it, are not
extracted (see `network.zed`). Every other caveat type (`any`, `bool`, `string`,
`int`, `uint`, `double`, `bytes`, `duration`, `timestamp`, `list<T>`, `map<T>`)
parses.

```
caveat in_network(ip ipaddress) { ... }   ->   (ERROR (caveat_literal) (identifier) ...)
```

## Any name with a number in it

An identifier that contains a numeral (`server1`, `hcs_de_p43_case`, `r1`) is
a syntax error, whether it is declared or only used. Error recovery then drops
the declaration around it and often the declarations that follow (see
`numerals.zed`). In the real schema `oviva.zed` this loses seven definitions and
27 permissions after the first such name.

```
definition server1 { ... }   ->   (ERROR ...)
```

## Not extracted by choice

Subject types, `with` caveat references, and names used inside permission
expressions. Prefixed definition names such as `project/folder` are extracted
whole.
