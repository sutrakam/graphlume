# Teal: forms that are not extracted

- **Record fields and enum values** are not definitions (`x: number`, `"red"`).
  Functions held in a record field (`new: function(): Point`) are the type's
  declaration of the field; the implementation is `function Point.new()`, which
  is extracted.
- **Module-level constants that are aliases or imports**: `local x <const> = require("m")`,
  `local ivalues <const> = util.tab.ivalues`, `local insert <const> = table.insert`.
  These rebind something defined elsewhere, so they are left out. Only a
  `<const>` (or `<total>`) local whose value is a literal, an operator
  expression, a table constructor or a call other than `require` is read as a
  constant.
- **Plain top-level locals and forward declarations**: `local x: integer = 1`,
  `global counter: integer`. Only a table-valued top-level local or global is
  recorded (as a variable).
- **Functions stored in a table constructor** (`local t = { on_start = function() end }`).
  Teal code declares such things as records with `function R.f()`; the
  constructor form is not read.
- **Parse failures in the grammar.** `local type _ = require("busted")` (the
  first line of busted spec files) is an ERROR node, and so are some record
  field types with constrained generics and optional parameters
  (`config?: ListenerConfig` after `function<E is Event>(...)`); such files are
  reported as `partial` and the declarations the parser did read are still
  extracted. Spec files made only of `describe` blocks declare nothing.
- **`.tl` files that are Telegram Type Language schemas** (Linguist's "Type
  Language", for example `scheme.tl`) are detected as Teal and yield no
  definitions, since they are not Teal source. This is language detection,
  outside this fixture.
