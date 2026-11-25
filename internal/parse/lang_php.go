package parse

import (
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"

	ts "github.com/odvcencio/gotreesitter"
)

// PHP calls. The inferred tags query for PHP finds definitions and no call at
// all, so every "who calls this" answer for PHP was empty.
//
// Each pattern tags the whole call and captures the callee's name; shape.go
// reads the receiver from the text between the two, with the PHP spellings it
// already knows: `$this->m()` is `self`, `self::m()`, `static::m()` and
// `parent::m()` are `self` and `super`, `A::m()` is a call on the scope `A`,
// and `$x?->m()` is a call on `$x`.
//
//	f($x)                function_call_expression, a bare name
//	\App\f($x)           the same through a qualified name; the name is the last
//	$x->m(), $x?->m()    member_call_expression, nullsafe_member_call_expression
//	A::m(), self::m()    scoped_call_expression
//	new A(...)           a reference of kind reference.call.new whose name is the
//	                     TYPE; the resolver binds it to a class, as INSTANTIATES
//
// Not extracted: a call through a variable (`$f()`, `$obj->$name()`), `new` of
// a variable or of `static`/`self`/`parent`, a class named by a string, and
// the receiver of a name written with a namespace separator
// (`\App\A::m()` keeps the call, loses what it was made on).
func init() {
	registerDefinitions("php", `
(function_call_expression function: (name) @name) @reference.call
(function_call_expression function: (qualified_name (name) @name .)) @reference.call
(member_call_expression name: (name) @name) @reference.call
(nullsafe_member_call_expression name: (name) @name) @reference.call
(scoped_call_expression name: (name) @name) @reference.call
(object_creation_expression (name) @name @reference.call.new)
(object_creation_expression (qualified_name (name) @name .) @reference.call.new)
`, false)
}

// phpUses reads the file's `use` declarations as import rows whose path is
// written the way the other class-path languages write theirs, with dots:
// `use App\Util\Helper;` is `App.Util.Helper`, and `use App\Util\Other as Oth;`
// is the same shape with the alias `Oth`. `use function` and `use const` name a
// member of a namespace and are read the same way. The group form
// `use App\Util\{A, B as C};` is one row per member.
//
// The separator matters to the resolver: it splits an import path on `/` and
// `.`, so a path with backslashes would be one segment that names nothing.
// Whether `App\Util` is the directory `src/Util` is Composer's autoload map
// and not the file's to say, so a name is also found by being the one type of
// that name in the repository.
func (p *genericParser) phpUses(tree *ts.Tree, src []byte) (out []Import) {
	defer recoverPanic()
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	lang := p.lang
	dotted := func(s string) string {
		return strings.ReplaceAll(strings.Trim(strings.TrimSpace(s), `\`), `\`, ".")
	}
	add := func(path, alias string, at *ts.Node) {
		if path == "" || at == nil {
			return
		}
		imp := Import{Path: path, Alias: alias}
		imp.Range = model.Range{
			Start: model.Position{Line: int(at.StartPoint().Row) + 1, Col: int(at.StartPoint().Column)},
			End:   model.Position{Line: int(at.EndPoint().Row) + 1, Col: int(at.EndPoint().Column)},
		}
		out = append(out, imp)
	}
	// clause reads one `Name\Path` or `Name\Path as Alias`.
	clause := func(prefix string, c *ts.Node) {
		var name, alias string
		for i := 0; i < c.ChildCount(); i++ {
			k := c.Child(i)
			if k == nil || !k.IsNamed() {
				continue
			}
			switch k.Type(lang) {
			case "qualified_name", "namespace_name":
				name = dotted(k.Text(src))
			case "name":
				if name == "" {
					name = dotted(k.Text(src))
				} else {
					alias = k.Text(src)
				}
			}
		}
		if name == "" {
			return
		}
		if prefix != "" {
			name = prefix + "." + name
		}
		add(name, alias, c)
	}

	var walk func(n *ts.Node, depth int)
	walk = func(n *ts.Node, depth int) {
		if n == nil || depth > 6 {
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			ch := n.Child(i)
			if ch == nil || !ch.IsNamed() {
				continue
			}
			switch ch.Type(lang) {
			case "namespace_use_declaration":
				prefix := ""
				for j := 0; j < ch.ChildCount(); j++ {
					k := ch.Child(j)
					if k == nil || !k.IsNamed() {
						continue
					}
					switch k.Type(lang) {
					case "namespace_name":
						prefix = dotted(k.Text(src))
					case "namespace_use_clause":
						clause("", k)
					case "namespace_use_group":
						for m := 0; m < k.ChildCount(); m++ {
							if g := k.Child(m); g != nil && g.IsNamed() && g.Type(lang) == "namespace_use_clause" {
								clause(prefix, g)
							}
						}
					}
				}
			case "namespace_definition", "compound_statement":
				walk(ch, depth+1)
			}
		}
	}
	walk(root, 0)
	return out
}
