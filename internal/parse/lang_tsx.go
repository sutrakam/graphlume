package parse

// TSX: TypeScript plus JSX. The definitions are shared with JavaScript and
// TypeScript, see lang_javascript.go.
//
// # A rendered component is a call
//
// `<Button variant="x" />` records a reference to Button of the same kind as
// `Button({variant: "x"})`, shaped by the same rule: the tagged node is the
// element's name, so a bare `<Button />` is a bare call and `<Foo.Bar />` is a
// call of Bar on Foo. The edge to a function component is CALLS, because a
// function component is a function that React calls; to a class component it
// is INSTANTIATES, because React constructs one — the same edge `new Button()`
// makes. So callers on a function component lists where it is rendered,
// and on a class component references does.
//
// Only capitalised names are components (`<div>` is HTML), unless the tag is a
// member (`<ui.card>` is always a variable). `<Ctx.Provider>` and
// `<Ctx.Consumer>` are the context object's, and record a reference to Ctx.
func init() {
	registerScriptLanguage("tsx", jsxReferences)
}
