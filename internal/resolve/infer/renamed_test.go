package infer

import (
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// renamedUse resolves a bare use of `local` in file, with the given imports,
// against idx; the file holds the definitions in idx that are written in it.
func renamedUse(t *testing.T, idx map[string][]registry.SymbolIndexEntry, file, local string, dirs []string, imports ...registry.Import) ([]model.Edge, registry.RefOutcome) {
	t.Helper()
	r := New(idx, Options{Layout: NewLayout([]string{file, "src/cal/Calendar.tsx"}, dirs)})
	es, outs, _ := resolveOne(t, r, registry.FileRefs{
		FileID: 10, Path: file, Language: "tsx", Imports: imports,
		Symbols: within(99, file, "Wrapper"),
		Refs:    []registry.Reference{callRef(1, local, model.ReceiverBare, "", 5)},
	})
	return es, outs[1]
}

// TestRenamedImportOfAPackageIsExternalWhateverTheFileDefines: tremor's
// `import { AreaChart as RechartsAreaChart } from 'recharts'` inside the
// repository's own AreaChart. The use is recharts', not this file's AreaChart.
func TestRenamedImportOfAPackageIsExternalWhateverTheFileDefines(t *testing.T) {
	own := def(1, "function", "src/AreaChart.tsx", "AreaChart", 1)
	own.Language = "tsx"
	idx := index(own)
	es, out := renamedUse(t, idx, "src/AreaChart.tsx", "RechartsAreaChart", []string{"src"},
		registry.Import{Path: "recharts", Alias: ".RechartsAreaChart=AreaChart"})
	if len(es) != 0 {
		t.Errorf("a renamed import of a package was bound inside the repository: %d edges", len(es))
	}
	if !out.External {
		t.Errorf("the use leaves the repository and is not recorded as external: %+v", out)
	}
}

// TestRenamedImportOfAModuleBindsToTheExportedName: tremor's `Calendar as
// CalendarPrimitive` from the repository's own module, beside a same-named
// definition elsewhere that the import does not name.
func TestRenamedImportOfAModuleBindsToTheExportedName(t *testing.T) {
	right := def(1, "function", "src/cal/Calendar.tsx", "Calendar", 3)
	right.Language = "tsx"
	other := def(2, "function", "src/other/Calendar.tsx", "Calendar", 3)
	other.Language = "tsx"
	idx := index(right, other)
	es, _ := renamedUse(t, idx, "src/DatePicker.tsx", "CalendarPrimitive", []string{"src"},
		registry.Import{Path: "./cal/Calendar", Alias: ".CalendarPrimitive=Calendar"})
	if len(es) != 1 || es[0].DstID != 1 || es[0].Confidence != model.ConfidenceHigh {
		t.Errorf("want one high edge to the module's Calendar (1), got %+v", es)
	}
}

// TestRenamedImportDoesNotFallBackToTheLocalName: the module does not define
// the exported name, and a definition of the local name sits in the file. The
// use is bound to neither.
func TestRenamedImportDoesNotFallBackToTheLocalName(t *testing.T) {
	local := def(1, "function", "src/DatePicker.tsx", "CalendarPrimitive", 1)
	local.Language = "tsx"
	idx := index(local)
	es, out := renamedUse(t, idx, "src/DatePicker.tsx", "CalendarPrimitive", []string{"src"},
		registry.Import{Path: "./cal/Calendar", Alias: ".CalendarPrimitive=Calendar"})
	if len(es) != 0 || out.Resolution == registry.ResolutionResolved {
		t.Errorf("a renamed import fell through to a same-file definition: %+v %+v", es, out)
	}
}
