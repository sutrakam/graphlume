package parse

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// defs renders a result as "line kind name" strings, sorted, so a test can
// state exactly what a component defines.
func defs(res *Result) []string {
	out := make([]string, 0, len(res.Symbols))
	for _, s := range res.Symbols {
		out = append(out, fmt.Sprintf("%d %s %s", s.Range.Start.Line, s.Kind, s.Name))
	}
	sort.Strings(out)
	return out
}

func wantDefs(t *testing.T, res *Result, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := defs(res)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("definitions differ\n got: %q\nwant: %q", got, want)
	}
}

// A top-level constant is a definition, a constant bound to a function stays a
// function (not both), and a declaration inside a function is not one.
func TestSFCTopLevelBindings(t *testing.T) {
	src := `<script setup lang="ts">
const count = ref(0)
let flag = false
const add = (a: number) => a + 1
export const sub = function (a: number) { return a - 1 }
function run() {
  const inner = 1
  let other = 2
}
</script>
`
	res := parse(t, "Calc.vue", src)
	wantDefs(t, res,
		"1 class Calc",
		"2 constant count",
		"3 variable flag",
		"4 function add",
		"5 function sub",
		"6 function run",
	)
}

// The script's lines are lines of the .vue file, in every block.
func TestSFCLineNumbersAcrossBlocks(t *testing.T) {
	src := "<template>\n  <div/>\n</template>\n\n<script>\nexport const A = 1\n</script>\n\n<script setup>\n\nconst b = 2\nfunction c() {}\n</script>\n"
	res := parse(t, "Two.vue", src)
	wantDefs(t, res,
		"1 class Two",
		"6 constant A",
		"11 constant b",
		"12 function c",
	)
}

// A `>` inside an attribute value (the generic attribute of a Vue 3.3 component)
// does not end the tag. A naive pattern would, and the script then begins in the
// middle of the attribute.
func TestSFCAttributeContainingAngleBracket(t *testing.T) {
	src := "<script setup lang=\"ts\" generic=\"T extends Array<string>\">\nconst n = 1\nfunction f(x: T) {}\n</script>\n"
	res := parse(t, "Generic.vue", src)
	wantDefs(t, res, "1 class Generic", "2 constant n", "3 function f")
	if res.Status != StatusOK {
		t.Errorf("status=%s err=%v, want ok", res.Status, res.Err)
	}
}

// A script shown inside an HTML comment is not a script.
func TestSFCScriptInCommentIsIgnored(t *testing.T) {
	src := "<!--\n<script>const hidden = 1</script>\n-->\n<script>\nconst shown = 2\n</script>\n"
	res := parse(t, "C.vue", src)
	wantDefs(t, res, "1 class C", "5 constant shown")
}

// The component is a definition, named by its file, with or without a script;
// a route or companion file name is not a component name.
func TestSFCComponentSymbol(t *testing.T) {
	for _, tc := range []struct {
		path, src string
		want      []string
	}{
		{"Button.vue", "<template><b/></template>\n", []string{"1 class Button"}},
		{"my-button.svelte", "<b>x</b>\n", []string{"1 class my-button"}},
		{"[id].vue", "<template><b/></template>\n", nil},
		{"+page.svelte", "<b>x</b>\n", nil},
		{"404.vue", "<template><b/></template>\n", nil},
		{"Foo.stories.svelte", "<b>x</b>\n", nil},
		{"Empty.vue", "  \n", nil},
	} {
		t.Run(tc.path, func(t *testing.T) {
			wantDefs(t, parse(t, tc.path, tc.src), tc.want...)
		})
	}

	res := parse(t, "Tall.vue", "<template>\n<b/>\n</template>\n")
	if len(res.Symbols) != 1 || res.Symbols[0].Range.End.Line != 3 {
		t.Errorf("the component spans the file: got %+v", res.Symbols)
	}
}

// Function-valued properties of the exported options object are methods, in
// the three ways such an object is written, and a function-valued property of
// any other object is not.
func TestSFCOptionsObjectMethods(t *testing.T) {
	for name, src := range map[string]string{
		"export default":  "<script>\nexport default {\n  a: function () {},\n  b: () => 1,\n  methods: { c: x => x },\n  other: { deeper: { d: () => 1 } },\n}\n</script>\n",
		"defineComponent": "<script>\nexport default defineComponent({\n  a: function () {},\n  b: () => 1,\n  methods: { c: x => x },\n})\n</script>\n",
		"module.exports":  "<script>\nmodule.exports = {\n  a: function () {},\n  b: () => 1,\n  methods: { c: x => x },\n}\n</script>\n",
	} {
		t.Run(name, func(t *testing.T) {
			res := parse(t, "O.vue", src)
			for _, n := range []string{"a", "b", "c"} {
				if !hasSymbol(res, n, model.KindMethod) {
					t.Errorf("%s was not extracted as a method: %v", n, defs(res))
				}
			}
			if hasSymbol(res, "d", "") {
				t.Errorf("a property three objects down is not a method of the component: %v", defs(res))
			}
		})
	}

	res := parse(t, "P.vue", "<script setup>\nuseHead({ title: () => 'x' })\nconst o = { f: () => 1 }\n</script>\n")
	if hasSymbol(res, "title", "") || hasSymbol(res, "f", model.KindMethod) {
		t.Errorf("callbacks in call arguments and in other objects are not definitions: %v", defs(res))
	}
}

// Props are the names destructured from defineProps() and $props(), whatever
// the destructuring spells, and no other destructuring declares anything.
func TestSFCDestructuredProps(t *testing.T) {
	res := parse(t, "Props.svelte", "<script>\nlet { a, b = 1, c: d, e: f = 2, ...rest } = $props()\nconst { g } = useThing()\n</script>\n")
	wantDefs(t, res,
		"1 class Props",
		"2 field a", "2 field b", "2 field d", "2 field f",
	)
	vue := parse(t, "Props.vue", "<script setup>\nconst { x, y = 1 } = defineProps(['x', 'y'])\n</script>\n")
	wantDefs(t, vue, "1 class Props", "2 field x", "2 field y")
}

// `export let` is a prop and `$:` a reactive declaration in Svelte.
func TestSvelteLegacyBindings(t *testing.T) {
	src := "<script>\n  export let name = 'x'\n  export const answer = 42\n  $: doubled = name + name\n  $: console.log(doubled)\n</script>\n"
	wantDefs(t, parse(t, "Legacy.svelte", src),
		"1 class Legacy",
		"2 field name",
		"3 constant answer",
		"4 variable doubled",
	)
}

// Snippets exist only in markup. Their end lines follow the nesting, and text
// that only looks like one (in a comment, a script or a style block) is not one.
func TestSvelteSnippets(t *testing.T) {
	src := "<script>\n  const s = '{#snippet fake(x)}'\n</script>\n\n{#snippet outer(a)}\n  {#snippet inner()}\n    <i/>\n  {/snippet}\n{/snippet}\n\n<!-- {#snippet gone(b)} -->\n{@render outer(1)}\n"
	res := parse(t, "S.svelte", src)
	wantDefs(t, res, "1 class S", "2 constant s", "5 function outer", "6 function inner")
	for _, s := range res.Symbols {
		switch s.Name {
		case "outer":
			if s.Range.End.Line != 9 {
				t.Errorf("outer ends at line %d, want 9", s.Range.End.Line)
			}
		case "inner":
			if s.Range.End.Line != 8 {
				t.Errorf("inner ends at line %d, want 8", s.Range.End.Line)
			}
		}
	}
}

// Every script language the registry maps a block to must accept the extra
// patterns. If one grammar rejected them the whole component would silently
// fall back to the plain parser and lose its state and props.
func TestSFCDefinitionsCompileForEveryScriptLanguage(t *testing.T) {
	reg, ok := NewRegistry().(*registry)
	if !ok {
		t.Fatal("not the embedded registry")
	}
	plainFor := func(ext string) *genericParser {
		p, _ := reg.ForFile("sfc." + ext).(*genericParser)
		return p
	}
	for _, ext := range []string{"ts", "tsx", "js", "jsx"} {
		plain := plainFor(ext)
		if plain == nil || plain.initErr != nil {
			t.Fatalf("no parser for %s", ext)
		}
		d := reg.sfcDelegate(ext)
		if d == plain || !strings.Contains(d.tagsQuery, "defineProps") {
			t.Errorf("%s: the component parser is the plain one, so the extra patterns were rejected by the grammar", ext)
		}
	}
}

// A script is still the language's own parse: a .ts file is unaffected.
func TestSFCDefinitionsDoNotLeakIntoScriptFiles(t *testing.T) {
	res := parse(t, "plain.ts", "const count = 1\nlet flag = false\nexport default { a: function () {} }\n")
	if len(res.Symbols) != 0 {
		t.Errorf("a .ts file gained definitions: %v", defs(res))
	}
}

// Object shorthand methods are the component's only in its own options object:
// an object that is an argument of a call, returned from a callback or bound
// to a name defines nothing, and a class is untouched.
func TestSFCMethodsOutsideOptionsObject(t *testing.T) {
	res := parse(t, "A.vue", "<script setup>\nconst rows = computed(() => ({\n  async onClick() {},\n}))\nconst table = createTable({\n  get data() { return 1 },\n  state: { get sel() { return 2 } },\n})\nclass K { m() {} }\n</script>\n<script>\nexport default { get title() { return 1 }, methods: { run() {} } }\n</script>\n")
	for _, n := range []string{"onClick", "data", "sel"} {
		if hasSymbol(res, n, "") {
			t.Errorf("%s is in an object that is not the options object: %v", n, defs(res))
		}
	}
	for _, n := range []string{"m", "title", "run"} {
		if !hasSymbol(res, n, model.KindMethod) {
			t.Errorf("%s should be a method: %v", n, defs(res))
		}
	}
}
