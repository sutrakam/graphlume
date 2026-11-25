# Svelte: known gaps

A `.svelte` file is read through its `<script>` blocks by
`internal/parse/sfc.go` (the Svelte grammar leaves the script opaque), plus the
`{#snippet}` blocks found in the markup. Everything below exists and is not
extracted.

- **`$:` statements that assign nothing.** `$: console.log(x)`,
  `$: if (x) {}` and `$: { ... }` are reactive statements; only
  `$: name = expr` declares a name. Tree fragment of the form that is indexed:

  ```
  (labeled_statement label: (statement_identifier "$") body: (expression_statement (assignment_expression left: (identifier))))
  ```
  A destructuring reactive assignment (`$: ({ a, b } = obj)`) is not matched.
- **Destructured bindings other than props.** Only the names destructured from
  `$props()` are indexed (as fields). `let { a } = $state(obj)` is not.
- **Svelte 5 rune state inside `.svelte.js` / `.svelte.ts` modules** is plain
  JavaScript or TypeScript to the parser for those files and is not changed.
- **Snippets are found by pattern, not by tree.** The Svelte grammar leaves the
  markup opaque, so `{#snippet name(...)}` is matched in the markup text after
  `<script>`, `<style>` and comments are blanked. A snippet inside a `{#if}`
  or `{#each}` block is found like any other. `{@const}` and
  `{#each ... as item}` aliases are not definitions.
- **Store auto-subscriptions** (`$store`) and **`<svelte:options>`** declare
  nothing indexable.
- **Components whose file name is not an identifier** (`+page.svelte`,
  `+layout.svelte`, `[slug]/+page.svelte`) get no component symbol; their
  script is still indexed.
- **A `<script>` that has no closing tag, is self-closing, or contains the text
  `</script>` in a string.**
