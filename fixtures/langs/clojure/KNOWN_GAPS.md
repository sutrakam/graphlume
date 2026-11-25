# Clojure: known gaps

Definitions are the defining forms at the top level of the file (or directly
inside a top-level `#?(...)` reader conditional). These forms exist and are not
extracted, on purpose.

- A defining form wrapped in another form: `(let [x 1] (defn f [] x))`,
  `(do (defn f ...))`, `(when cond (def x ...))`, `(defn outer [] (def inner ...))`
  (`closure-defined` in `core.clj`). The tree is an ordinary `list_lit` nested in
  another `list_lit`, indistinguishable from a call to a function that happens
  to be called `defn`. Matching it at any depth is what the library's inferred
  query did, and it indexed the `def` inside every function body and
  `(comment ...)` block, so it is not done.
- `deftest` (clojure.test), `declare`, `defstruct`, `defn-memo`-style macros
  from third-party libraries, and any user macro that expands to a `def`. The
  head symbol is just a name; the grammar cannot know what a macro defines.
- Methods that `defrecord`, `deftype`, `extend-protocol`, `extend-type` and
  `reify` implement. They have the same shape as a protocol signature
  (`(perimeter [this] body)`) and are implementations of a method already
  indexed on the protocol, not new definitions.
- A `defmethod` is indexed under the multimethod's name only. The dispatch
  value (`:circle`) is the next sibling, not part of the name, and a query
  cannot join two nodes into one name.
- Fields of `defrecord`/`deftype` (`[radius]`) and `ns` `:require` aliases.
