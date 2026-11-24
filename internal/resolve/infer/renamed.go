package infer

import (
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// # A use of a renamed import is bound through that import
//
//	import { AreaChart as RechartsAreaChart } from 'recharts'
//	import { Calendar as CalendarPrimitive } from '../Calendar'
//
// `<RechartsAreaChart />` is a use of the name RechartsAreaChart, and that
// name is what the file's import declares: an export called AreaChart of the
// module 'recharts', or an export called Calendar of '../Calendar'. It is not a
// use of whatever is called AreaChart or RechartsAreaChart in this file, in its
// directory or anywhere in the repository. An earlier fix recorded such a use
// under the exported name and let the ordinary rules bind it; those rules never
// look at where the import came from, and bound `<RechartsAreaChart>` inside
// the repository's own AreaChart to AreaChart itself (tremor: 7 wrong `high`
// edges against 2 right; hono: 13 against 3).
//
// The use keeps the name it is written with. The parser states the renaming in
// the import row (".local=exported", see internal/parse/import_bindings.go),
// and this is the whole rule:
//
//   - the specifier names a package that is not in the repository: the use
//     leaves the repository, whatever the repository also defines;
//   - the specifier names a module of the repository: the use is the definition
//     of the exported name in that module (or, for a directory's index, in the
//     one file beneath it that defines it), and nothing else;
//   - the specifier cannot be traced: the use is bound to nothing.
//
// No other rule is asked, under either name.

// ruleRenamedImport labels an edge bound through a renamed import.
const ruleRenamedImport = "imported under another name from the module that defines it"

// renamedAlias reads a member import's alias, ".local=exported".
func renamedAlias(alias string) (local, exported string, ok bool) {
	rest, isMember := strings.CutPrefix(alias, memberAliasPrefix)
	if !isMember {
		return "", "", false
	}
	local, exported, ok = strings.Cut(rest, renameSeparator)
	return local, exported, ok && local != "" && exported != "" && local != exported
}

// viaRenamedImport binds a bare use of a locally renamed import. handled is
// false when the reference is not such a use, and the ordinary rules apply.
func (r *Resolver) viaRenamedImport(spec LanguageSpec, fr registry.FileRefs, imports importSet,
	ref registry.Reference, symbols map[string][]registry.SymbolIndexEntry) (d decision, handled bool) {
	if len(imports.renamed) == 0 || spec.Calls.Imports != ImportsNameModules || ref.Receiver != "" ||
		(ref.ReceiverKind != "" && ref.ReceiverKind != model.ReceiverBare) || strings.HasSuffix(ref.Kind, indirectCallSuffix) {
		return decision{}, false
	}
	ren, ok := imports.renamed[ref.Name]
	if !ok {
		return decision{}, false
	}
	edge := edgeKindFor(ref.Kind)
	if edge != model.EdgeCalls && edge != model.EdgeExtends && edge != model.EdgeImplements {
		return decision{}, false
	}
	m := r.scriptModule(fr.Path, imports.imports[ren.entry].raw)
	if m.where == presenceExternal {
		return externalCall, true
	}
	if m.contains == nil && m.beneath == nil {
		return unresolvedCall, true
	}

	exported := ref
	exported.Name = ren.exported
	var among []registry.SymbolIndexEntry
	for _, s := range symbols[ren.exported] {
		if spec.canBind(s, edge) {
			among = append(among, s)
		}
	}
	if edge != model.EdgeCalls {
		var types []registry.SymbolIndexEntry
		for _, s := range among {
			if isTypeKind(s.Kind) {
				types = append(types, s)
			}
		}
		among = types
	}
	c := call{spec: spec, fr: fr, imports: imports, ref: exported, cands: among, symbols: symbols}
	if d, ok := bindInModule(c, m, among, ruleRenamedImport, ruleReExported); ok {
		return d, true
	}
	return unresolvedCall, true
}
