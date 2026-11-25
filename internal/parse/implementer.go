package parse

import ts "github.com/odvcencio/gotreesitter"

// isImplTypeParameter reports whether name is one of the type parameters the
// impl item declares: `impl<T: Bound> Trait for T`. Such an impl is blanket and
// gives the trait to every T satisfying the bound, so it has no one type to be
// the source of an IMPLEMENTS edge and records nothing.
//
// The parameter list is `(type_parameters (type_parameter (type_identifier)
// ...))`; a lifetime or a const parameter is a different node and is not a
// type, so it cannot be the type an impl is written for.
func isImplTypeParameter(lang *ts.Language, impl *ts.Node, name string, src []byte) bool {
	if impl == nil || lang == nil {
		return false
	}
	for i := 0; i < impl.ChildCount(); i++ {
		list := impl.Child(i)
		if list == nil || list.Type(lang) != "type_parameters" {
			continue
		}
		for j := 0; j < list.ChildCount(); j++ {
			param := list.Child(j)
			if param == nil || param.Type(lang) != "type_parameter" || param.ChildCount() == 0 {
				continue
			}
			if first := param.Child(0); first != nil && first.Text(src) == name {
				return true
			}
		}
	}
	return false
}
