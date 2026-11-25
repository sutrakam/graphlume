# HTML: forms not extracted

- **Ids are the only definition.** Tag names (including custom elements such
  as `<my-widget>`), `class`, `name`, `data-*`, `for`, `href="#target"` and
  `name="anchor"` anchors are not definitions: they repeat across elements and
  are not unique names.
- **Templated ids.** An id value containing white space or one of `{ } < > $ %
  @` (`{{ item.id }}`, `{{itemId}}`, `<%= n %>`, `${x}`, `@Model.Id`) is
  skipped, because the name is only known after rendering. A bound attribute
  such as Vue's `:id="x"` or Angular's `[id]="x"` has a different attribute
  name and is not matched.
- **Elements created by script.** `document.write('<div id="x">')` and
  `getElementById("x")` are inside a `raw_text` node of the script element and
  are not read; the JavaScript grammar sees them as strings.
- **Ids in `<script>`, `<style>` and comments** are not seen (the grammar
  gives those bodies as raw text or comment).
- **Duplicate ids** are each recorded; HTML forbids them but does not stop
  them.
- **Embedded languages.** Single-file components (`.vue`, `.svelte`) and Angular
  templates are other languages; Angular templates are ordinary `.html` files
  and are read by this grammar, so an `id` in one is extracted as above, while
  `#ref` template variables are not (see `fixtures/langs/angular`).
- **A file the grammar reads with syntax errors** (server-side template code
  inside a tag, for example) can lose ids in the unreadable region.
