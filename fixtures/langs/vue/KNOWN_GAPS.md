# Vue: known gaps

A `.vue` file is read through its `<script>` blocks by `internal/parse/sfc.go`
(the Vue grammar leaves the script opaque). Everything below is a declaration
form that exists and is not extracted.

- **Destructured bindings other than props.** `const { t } = useI18n()` and
  `const [a, b] = useThing()` declare names inside a pattern. Only the names
  destructured from `defineProps()` are indexed (as fields). Tree fragment:

  ```
  (lexical_declaration (variable_declarator name: (object_pattern (shorthand_property_identifier_pattern)) value: (call_expression function: (identifier "useI18n"))))
  ```
- **Props declared by type only.** `defineProps<{ rows: T[] }>()` with no
  destructuring declares props as members of a type literal
  (`object_type > property_signature`), which are not definitions of their own;
  the interface form (`defineProps<Props>()` with `interface Props`) indexes
  `Props`.
- **Function-valued properties anywhere but the component options object.**
  `useHead({ title: () => t('x') })`, `watch(src, { onReady: () => {} })` and
  `const o = { f: () => 1 }` are callbacks and local objects, not definitions.
  In the options object (`export default {}`, `defineComponent({})`,
  `Vue.extend({})`, `module.exports = {}`) a function-valued property is a
  method, directly or one object down (`methods: { go: () => {} }`); an
  object two levels down (`computed: { x: { get: () => 1 } }`) is not matched.
  Properties with a string or computed key (`'update:x': function () {}`) are
  not matched.
- **Options-API `export default` written another way**:
  `const options = {...}; export default options`.
- **A `<script>` that has no closing tag or is self-closing**
  (`<script src="./a.js" />`), or that contains the text `</script>` in a
  string.
- **Anything in `<template>`**: component usage, `v-for` aliases, slots. A
  template declares no names a code index holds.
- **Components whose file name is not an identifier** (`[id].vue`, `404.vue`,
  `Foo.stories.vue`) get no component symbol; their script is still indexed.
