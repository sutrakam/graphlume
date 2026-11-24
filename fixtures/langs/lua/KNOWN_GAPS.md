# Lua: forms that are not extracted

These are deliberate, because a pattern that reached them would also match
things that are not definitions.

- **Class objects built by a call**: `local Dog = setmetatable({}, { __index = Animal })`
  and `local Foo = class("Foo")`. The tree is a `variable_declaration` whose
  value is a `function_call`; nothing in it says the call creates a class
  rather than any other value. Only a table constructor is read as a module or
  class object.
- **Plain top-level constants**: `local MAX_DEPTH = 64`. Not a function or a
  table, and Lua has no declaration that marks a constant.
- **Functions assigned to a field inside a function body**: `self.cb = function() end`,
  `subclass.initialize = function(...) end`. The statement is not at the top
  level, so it assigns to an existing object and declares nothing; reading it
  as a definition would turn every callback registration into a symbol.
- **Function-valued fields of a table passed as an argument**:
  `setmetatable(t, { __index = function(t, k) end })`,
  `table.sort(t, function(a, b) end)`. Tags `__index` and `__call` would be
  recorded at every call site.
- **Fields deeper than three levels of table nesting.**
- **PICO-8 cartridges** (`.p8`): the file starts with a text header the Lua
  grammar cannot read, so the first definitions after it are lost
  (`tree={}` is reported inside an error region).
