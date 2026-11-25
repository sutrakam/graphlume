package parse

import (
	"regexp"
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/sutrakam/graphlume/pkg/model"
)

// # A receiver whose type the source states
//
// shape.go reads the type of a local from its declaration, through the fields a
// grammar gives the declaration (`name`, `type`, `value`). Crystal, D and GDScript
// give their declarations no fields, so a call made on `x` after `x : Money`, `Cart c`
// or `var x: Money` was a call on a value of no type, and bound by the weakest rule.
//
// Where the source states the type outright, here it is read from the text of the
// function the call is in, before the call: a parameter or a variable declared
// with a type (`other : Money`, `Cart c`, `x: Money`), or with the construction of
// one (`x = Money.new`, `auto c = new Cart()`, `var x := Money.new()`). A name that
// has one such declaration is a value of that type; a name with declarations of
// different types, or none, is left as it was. A GDScript script's own typed `var`s
// at the top of the file count as well, for the call written in any method.
//
// It reads only a plain identifier as the receiver (`x.f()`, not `@x.f()` or
// `a.b.f()`), and it does not follow a reassignment: a value whose declared type
// is a base class is read as the base class, which is what the source states.
type statedTypeRule struct {
	// scopes are the node types whose text holds a local's declaration.
	scopes map[string]bool
	// patterns have one %s, the identifier, and capture the type's name.
	patterns []string
	// fields are patterns over the whole file for what a script declares at its top
	// level, matched at the start of a line: a call in any method is made on them.
	fields []string
}

var statedTypes = map[string]statedTypeRule{
	"crystal": {
		scopes: map[string]bool{"method_def": true},
		patterns: []string{
			`(?:^|[^\w@.:])%s\s+:\s*([A-Z][\w]*(?:::[A-Z]\w*)*)`,
			`(?:^|[^\w@.:])%s\s*=\s*([A-Z][\w]*(?:::[A-Z]\w*)*)\.new\b`,
		},
	},
	"gdscript": {
		scopes: map[string]bool{"function_definition": true},
		patterns: []string{
			`(?:^|[^\w.])%s\s*:\s*([A-Z]\w*)`,
			`\bvar\s+%s\s*(?::=|=)\s*([A-Z]\w*)\.new\b`,
		},
		fields: []string{
			`(?m)^(?:@\w+(?:\([^)]*\))?\s+)*var\s+%s\s*:\s*([A-Z]\w*)`,
			`(?m)^(?:@\w+(?:\([^)]*\))?\s+)*var\s+%s\s*(?::=|=)\s*([A-Z]\w*)\.new\b`,
		},
	},
	"d": {
		scopes: map[string]bool{"function_declaration": true, "constructor": true},
		patterns: []string{
			`(?:^|[^\w.!])([A-Z]\w*)(?:\[\s*\])?\s+%s\s*(?:=|;|,|\))`,
			`\bauto\s+%s\s*=\s*new\s+([A-Z]\w*)`,
		},
	},
}

var plainIdentifier = regexp.MustCompile(`^[a-z_][A-Za-z0-9_]*$`)

// applyStatedTypes rewrites the shape of each call made on a local whose type the
// source states into a call on that type. It runs for the languages above and
// changes nothing for any other.
func (p *genericParser) applyStatedTypes(tree *ts.Tree, src []byte, tags []ts.Tag, facts *shapeFacts) {
	rule, ok := statedTypes[p.entry.Name]
	if !ok || tree == nil || facts == nil {
		return
	}
	defer recoverPanic()
	root := tree.RootNode()
	cache := map[string]*regexp.Regexp{}
	compiled := func(pattern, ident string) *regexp.Regexp {
		key := pattern + "\x00" + ident
		if re, ok := cache[key]; ok {
			return re
		}
		re := regexp.MustCompile(strings.ReplaceAll(pattern, "%s", regexp.QuoteMeta(ident)))
		cache[key] = re
		return re
	}

	type scopeDecl struct {
		offset int
		typ    string
	}
	type scopeKey struct {
		start uint32
		ident string
	}
	scopeFoundCache := map[scopeKey][]scopeDecl{}
	fileFoundCache := map[string][]scopeDecl{}

	for _, tag := range tags {
		if !isCallTag(tag.Kind) {
			continue
		}
		at := tag.NameRange.StartByte
		shape, ok := facts.calls[at]
		if !ok || shape.notCall || (shape.kind != model.ReceiverValue && shape.kind != model.ReceiverName) || !plainIdentifier.MatchString(shape.receiver) {
			continue
		}
		n := root.NamedDescendantForByteRange(at, tag.NameRange.EndByte)
		var scope *ts.Node
		for a := n; a != nil; a = a.Parent() {
			if rule.scopes[a.Type(p.lang)] {
				scope = a
				break
			}
		}
		found := map[string]struct{}{}
		if scope != nil {
			sKey := scopeKey{start: scope.StartByte(), ident: shape.receiver}
			decls, ok := scopeFoundCache[sKey]
			if !ok {
				scopeStart := int(scope.StartByte())
				scopeEnd := min(int(scope.EndByte()), len(src))
				scopeText := string(src[scopeStart:scopeEnd])
				for _, pat := range rule.patterns {
					re := compiled(pat, shape.receiver)
					matches := re.FindAllStringSubmatchIndex(scopeText, -1)
					for _, m := range matches {
						if len(m) >= 4 && m[2] >= 0 && m[3] >= 0 {
							decls = append(decls, scopeDecl{
								offset: scopeStart + m[0],
								typ:    scopeText[m[2]:m[3]],
							})
						}
					}
				}
				scopeFoundCache[sKey] = decls
			}
			for _, d := range decls {
				if d.offset < int(at) {
					found[d.typ] = struct{}{}
				}
			}
		}
		if len(found) == 0 && len(rule.fields) > 0 {
			decls, ok := fileFoundCache[shape.receiver]
			if !ok {
				fileText := string(src)
				for _, pat := range rule.fields {
					re := compiled(pat, shape.receiver)
					matches := re.FindAllStringSubmatchIndex(fileText, -1)
					for _, m := range matches {
						if len(m) >= 4 && m[2] >= 0 && m[3] >= 0 {
							decls = append(decls, scopeDecl{
								offset: m[0],
								typ:    fileText[m[2]:m[3]],
							})
						}
					}
				}
				fileFoundCache[shape.receiver] = decls
			}
			for _, d := range decls {
				if d.offset < int(at) {
					found[d.typ] = struct{}{}
				}
			}
		}
		if len(found) != 1 {
			continue
		}
		for typ := range found {
			facts.calls[at] = callShape{kind: model.ReceiverType, receiver: typ}
		}
	}
}
