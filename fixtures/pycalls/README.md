# pycalls (test fixture)

A small Python package whose every interesting call site states, in a comment on
its own line, which definition it reaches. It is the ground truth for Python
call resolution: `internal/cli/pycalls_test.go` indexes a copy, resolves it, and
scores every marked call against its marker.

It is not a sample application. Each file exists for one way of being wrong.

## The markers

```python
class Account(BaseModel):  # def Account
    def close(self):  # def Account.close
        self.save()  # call save -> BaseModel.save
        return target.validate()  # call validate -> ?
```

- `# def <ID>` names the definition on that line. An ID is a label, unique in
  the fixture; it is not a qualified name and nothing parses it as one.
- `# call <name> -> <ID>` says the call written as `<name>` on that line reaches
  the definition labelled `<ID>`.
- `# call <name> -> ?` says the call is dynamic: which definition runs depends
  on a value the source does not state. Nothing may claim it at `high`.

One marked call per line. The ground truth is hand-written, by reading the
program the way Python runs it.

## What each file is for

| File | Shape |
|---|---|
| `shop/models/base.py`, `account.py`, `ledger.py` | inheritance, an override, `super()`, and `Audit` — an unrelated class reusing `save`, `validate` and `close` |
| `shop/models/__init__.py`, `shop/__init__.py` | re-exports, one and two packages away from the definition |
| `shop/utils/fmt.py` | module-level functions named `save`, `close` and `render`, like the methods |
| `shop/utils/decorators.py` | decorators, and calls through a parameter |
| `shop/services/payment.py` | `self`, `cls`, a typed parameter, a constructor assignment, a module-qualified call, a decorated method, and three receivers nothing states a class for |
| `shop/services/refund.py` | inherited dispatch from a subclass in another file |
| `shop/dynamic.py` | the hard negatives: a union return, a reassigned variable, `getattr`, a dispatch table, duck typing |
| `shop/shadow.py` | an import shadowed by a local definition, by a local variable, and by a parameter |
| `shop/nested.py` | one name defined in two functions, two classes of one name, a class nested in a class |
| `tests/test_payment.py` | a test double whose `save` is the only `save` in the file that calls `account.save()` |

## Checked against a compiler, once

The markers were compared with `scip-python` 0.6.6 on this tree. It agrees on 56
of the 57 call sites that have a target. The one disagreement is
`shop/shadow.py`, where `render` is imported and then redefined at module level:
`scip-python` attaches the call in `show` to the imported `fmt.render`, and
Python binds the name to the later `def`, which is what the marker says. The
marker is right and is left as written.

The `?` rows are outside what a compiler index can check: their targets are
local values, which SCIP does not record as definitions.
