package parse

// TypeScript. The definitions it shares with JavaScript and TSX are in
// lang_javascript.go; the grammar has no JSX, so it takes nothing more.
func init() {
	registerScriptLanguage("typescript", "")
}
