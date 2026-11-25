# receivers (test fixture)

One small hand-written tree per language. Every interesting call states, in a
comment on its own line, what it must bind to — or that it must bind to
nothing. `internal/cli/receivers_test.go` indexes a copy, resolves it, and
checks every marker.

It is the regression suite for one defect: a call that has a receiver or a
qualifier being bound as if it had none. Each file is a wrong binding the
verification of 12 November 2026 found on a public project
(the 12 November 2026 measurement run), reduced to the lines that caused it. It is not
a sample application, and nothing here is meant to compile.

## The markers

```go
func (e *Engine) addRoute(path string) { // def Engine.addRoute
	root.addRoute(path) // call addRoute -> node.addRoute
	_ = errors.New("x") // call New -> external
```

- `def <ID>` labels the definition on that line. An ID is a label, unique in
  the fixture; nothing parses it as a qualified name.
- `call <name> -> <ID>` says the call written `<name>` on that line produces a
  `CALLS` edge to the definition labelled `<ID>`, and no edge to anything else.
  A trailing `high` requires that tier.
- `call <name> -> new <ID>` says it produces an `INSTANTIATES` edge to the type.
- `call <name> -> none` says it produces no edge at all.
- `call <name> -> external` says it produces no edge and the reference is
  recorded as leaving the repository.
- `nocall <name>` says no call reference named `<name>` exists on the line:
  the receiver of a call is not itself a call.

Markers on one line are separated by `;`. C uses `/* ... */`.

## What each tree is for

| Tree | The wrong binding it pins |
|---|---|
| `go/` | `errors.New`, `log.New`, `template.New` and `http.Dir` bound to the package's own `New` and `Dir`; `require.Error` to a method named `Error`; `c.Request.Header.Set` to `(*Context).Set`; `w.Header()` to a package variable `Header`; `root.addRoute` to the enclosing `(*Engine).addRoute`; `current().LoadHTMLGlob` to the function it is written in |
| `java/` | a local variable `reader` stored as a call and bound to a method `reader(String)`; `strings.add` on a `java.util.List` bound to a test class's `add`; `asBigDecimal().intValue()` and `super.clone()` bound to the enclosing method |
| `ts/` | `util.joinValues` in the v4 tree bound to `v3/helpers/util.ts`; `Object.defineProperty` and `Reflect.defineProperty` bound to a proxy handler's method; `types.includes` and `Array...map` bound to `ZodString` |
| `js/` | CommonJS: `exports.f = function` and `module.exports = function createApp()` reached through `const x = require('./x')`, `{ a, b: c } = require(...)` and `x.f()`; `module.exports = { a() {}, b: function () {} }`; an anonymous `module.exports` named after its file; `Foo.prototype.bar = function` and `new Foo()`; `exports.f()` inside the same module; and what must stay unbound: `other.trimAll()` bound to `util`'s function of that name, `unknown.describe()` on a name two types declare, `fs.readFileSync` (a library), and `foreign.rewire = function` on an object the file does not declare |
| `ts/calls/` | calls through named and namespace imports, `new TsStore()` and a call on its stated type; `unknownThing.save()` beside two types that declare `save` and `tsRegisterAll` declared in two files; a component built by `memo()` is a function that can be called by its name |
| `tsx/` | `<Button />` bound to the imported one of two `Button` components in two directories; components built by `forwardRef`, `memo` and `styled`; a class component, bound as `INSTANTIATES`; `<Parts.Item />` through a namespace import; `<Spinner />` from a library recorded as external; `<main>` and `<div>` (HTML) recording nothing |
| `py/` | `_json.dumps` bound to the package's own `dumps`; builtin `open()` bound to `FlaskClient.open`; `client.get` producing an edge to the fixture `client` |
| `rb/` | `String.new` and `Mutex.new` bound to an in-repository `def self.new`; `obj.respond_to?` and `obj.to_s` bound to one class's methods |
| `rs/` | `self.inner.is_match()` inside `fn is_match` bound to itself; `SearcherBuilder::new().line_number(false)` bound to a test function `line_number`; `messages::errored()` producing an edge to a function named `messages`; `RegexBuilder::new` bound to another type's `new` |
| `c/`, `cpp/` | a field in a function-pointer table called as if it were a function; `current_->Next()` bound to the only `Next` in the file |
| `mixed/` | a call bound to a symbol of another language or to something that is not code: a C macro to a JSON key, libc `write` to a Python function, `req.text()` to a Prisma column, `importlib.metadata.version()` to a YAML key, a JavaScript call to a `package.json` dependency line |
