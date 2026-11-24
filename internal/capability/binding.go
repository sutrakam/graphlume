package capability

import (
	"context"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
)

// What the index knows about ONE symbol's callers, as opposed to what it knows
// about the repository.
//
// Completeness publishes the run-wide binding rate, and that was the only
// coverage figure an answer carried. It answers the wrong question. "This index bound 54% of its references" does not tell a
// reader whether the method they asked about is in the 54%; a report could
// print "0 callers, risk low" for a method with 792 references spelled like
// it, every one recorded as ambiguous, and nothing in the report said so.
//
// TargetBinding is the figure for the symbol itself: how many references are
// spelled like it, and what resolution did with each. It is what turns "no
// callers found" into either "no reference to this name exists in the index"
// or "792 exist and none could be bound", which are opposite findings.

// BindingReader is the read surface TargetBinding needs.
//
// It is NOT part of Registry, deliberately. Registry is implemented by every
// fake in the capability tests, and a method added there is a method each fake
// must grow. A registry that does not offer these reads yields a TargetBinding
// with Known false, and every consumer treats that as "not measured" rather
// than as "nothing unbound".
type BindingReader interface {
	NameBinding(ctx context.Context, runID, symbolID int64, name string) (registry.NameBinding, error)
	LanguageBindings(ctx context.Context, runID int64) (map[string]registry.LanguageBinding, error)
}

var _ BindingReader = (*registry.Registry)(nil)

// TargetBinding is the binding evidence behind an answer about one symbol.
type TargetBinding struct {
	// Known is false when the registry could not be asked. Every count below
	// is then zero and means "not measured".
	Known bool `json:"known"`

	// Name is the spelling the counts below are about: the target's name.
	Name string `json:"name,omitempty"`

	// Language is the language of the file defining the target, as the index
	// names it ("go", "kotlin", "c_sharp").
	Language string `json:"language,omitempty"`

	// LanguageExtracts is false when the index holds no reference and no
	// relationship originating in the target's language. Nothing about the
	// target's callers can then be concluded: the calls were never read.
	LanguageExtracts bool `json:"language_extracts_references"`

	// LanguageStats is reference resolution over the target's language alone,
	// so a Kotlin symbol is not described by the Java files beside it.
	LanguageStats registry.ResolutionStats `json:"language_resolution"`

	// LanguageDefinitions is how many definitions the index holds in that
	// language; it is what makes "0 references" a finding rather than an
	// empty directory.
	LanguageDefinitions int64 `json:"language_definitions"`

	// References is every reference spelled exactly like the target's name.
	References int64 `json:"references_to_name"`

	// BoundHere were bound to the target; BoundElsewhere to another
	// definition of the same name.
	BoundHere      int64 `json:"bound_to_target"`
	BoundElsewhere int64 `json:"bound_to_other_definitions"`

	// Ambiguous, Unresolved and Pending are bound to nothing. Any of them may
	// be a use of the target.
	Ambiguous  int64 `json:"ambiguous"`
	Unresolved int64 `json:"unresolved"`
	Pending    int64 `json:"pending"`

	// ProvenOther were left unbound by the resolver in files a compiler read
	// (the Go type check, or a compiler indexer), which proved them uses of
	// something else. They are not counted in Ambiguous, Unresolved or Pending.
	// The key keeps the name it had when the Go type check was the only source.
	ProvenOther int64 `json:"ruled_out_by_type_check,omitempty"`

	// Definitions is how many definitions are spelled like the target in files
	// that can call it. Zero means not counted.
	Definitions int64 `json:"definitions_of_name,omitempty"`

	// InstalledCopy is how many of the unbound references sit where the compiler
	// resolved this repository's own package to an installed copy instead of the
	// checkout. They are still counted in Ambiguous, Unresolved and Pending.
	InstalledCopy int64 `json:"seen_against_installed_copy,omitempty"`

	// WithoutEdge counts sites bound to the target that no relationship
	// records, because the reference sits outside every extracted definition
	// (module top level, an anonymous function). They are uses of the target
	// that no caller list contains.
	WithoutEdge int64 `json:"bound_without_relationship"`
}

// Unbound is the references spelled like the target that are bound to nothing.
func (b TargetBinding) Unbound() int64 { return b.Ambiguous + b.Unresolved + b.Pending }

// Unlisted is every recorded use of the target's NAME that a caller list for
// the target does not contain: the unbound ones and the ones bound outside any
// definition.
func (b TargetBinding) Unlisted() int64 { return b.Unbound() + b.WithoutEdge }

// Undetermined reports whether an EMPTY or SHORT caller list for this target
// is unexplained by the index, and says why in one sentence.
//
// It is the single test behind every "could not determine" this package
// prints. Three conditions, in the order of how little is known:
//
//  1. The target's language yields no references at all. The parser does not
//     read its calls, so absence of callers is a property of the parser.
//  2. The language's references exist but none has been resolved yet.
//  3. References spelled like the target exist and are not listed as its
//     callers, because they are ambiguous, unresolved or outside any
//     extracted definition.
//
// Low run-wide coverage is deliberately NOT a condition. It is reported
// beside every answer, but it describes the repository; a symbol whose every
// reference was bound is determined however badly its neighbours resolved.
func (b TargetBinding) Undetermined() (reason string, undetermined bool) {
	if !b.Known {
		return "", false
	}
	lang := DisplayLanguage(b.Language)
	switch {
	case !b.LanguageExtracts && b.LanguageDefinitions > 0:
		return NoReferencesClause(b.Language, b.LanguageDefinitions) +
			", so nothing about callers can be concluded", true
	case b.LanguageStats.Total > 0 && b.LanguageStats.Pending == b.LanguageStats.Total:
		return fmt.Sprintf("none of the %d %s reference(s) in this index has been resolved yet "+
			"(run 'graphlume resolve')", b.LanguageStats.Total, lang), true
	case b.Unlisted() > 0:
		return b.unlistedSentence(), true
	}
	return "", false
}

// MinDefinitionsToGeneralise is how many definitions a language must have in
// an index, with no reference from any of them, before an answer says
// GraphLume does not extract that language's calls.
//
// The claim is an inference from the index, and it needs enough of the index
// behind it. Thousands of Kotlin definitions and not one Kotlin reference is a
// parser that does not read Kotlin's calls. Six C definitions in two files and
// no reference is two small files, and saying "GraphLume does not extract
// calls from C" on that evidence would be a false statement about a language
// whose calls it does extract. Below the threshold the answer states what the
// index holds and stops there. Either way nothing about callers follows.
const MinDefinitionsToGeneralise = 20

// GeneralisesToLanguage reports whether definitions with no reference among
// them are enough to say the language's calls are not extracted at all.
func GeneralisesToLanguage(definitions int64) bool {
	return definitions >= MinDefinitionsToGeneralise
}

// NoReferencesClause states that a language yields no references in this
// index, as strongly as the evidence allows and no more.
func NoReferencesClause(language string, definitions int64) string {
	lang := DisplayLanguage(language)
	if lang == "" {
		lang = "this language"
	}
	if GeneralisesToLanguage(definitions) {
		return fmt.Sprintf("GraphLume does not extract calls from %s: this index holds %d %s "+
			"definition(s) and no reference from any of them", lang, definitions, lang)
	}
	return fmt.Sprintf("this index holds no reference from any of its %d %s definition(s)", definitions, lang)
}

// UnlistedSentence states how many references to the target's name exist and
// are not among its callers, split by why. Empty when every one is listed.
func (b TargetBinding) UnlistedSentence() string {
	if !b.Known || b.Unlisted() == 0 {
		return ""
	}
	return b.unlistedSentence()
}

func (b TargetBinding) unlistedSentence() string {
	var parts []string
	if b.Ambiguous > 0 {
		parts = append(parts, fmt.Sprintf("%d ambiguous %s", b.Ambiguous, b.ambiguousWhy()))
	}
	if b.Unresolved > 0 {
		parts = append(parts, fmt.Sprintf("%d unresolved", b.Unresolved))
	}
	if b.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d not yet resolved", b.Pending))
	}
	if b.WithoutEdge > 0 {
		parts = append(parts, fmt.Sprintf("%d bound to it from outside any indexed function "+
			"(module top level or an anonymous callback), which no caller list records", b.WithoutEdge))
	}
	s := fmt.Sprintf("%d of the %d reference(s) spelled like this name are not listed as its callers: %s",
		b.Unlisted(), b.References, strings.Join(parts, "; "))
	if b.InstalledCopy > 0 {
		s += fmt.Sprintf(". %d of the unbound sit where the compiler resolved this repository's own package to an "+
			"installed copy, not this checkout, so what it said there may describe another version "+
			"(index again with this checkout first on the import path to settle them)", b.InstalledCopy)
	}
	return s
}

// ambiguousWhy says why references are ambiguous, in terms that are true of
// this name. "Several definitions share the name" is said only when the index
// does not hold exactly one; a name with a single definition (overload stubs
// folded into it) was ambiguous because the evidence at the call did not single
// that definition out, and saying otherwise sends a reader looking for a
// namesake that does not exist.
func (b TargetBinding) ambiguousWhy() string {
	if b.Definitions == 1 {
		return "(the index holds one definition of this name; the evidence at these calls did not bind them to it)"
	}
	return "(several definitions share the name and none was chosen)"
}

// TargetBinding reads the binding evidence for tgt.
//
// A registry that cannot answer yields Known false and no error: a fake in a
// test is not a malfunction.
func (d Deps) TargetBinding(ctx context.Context, runID int64, tgt Target) (TargetBinding, error) {
	reader, ok := d.Registry.(BindingReader)
	if !ok || tgt.SymbolID == 0 {
		return TargetBinding{}, nil
	}
	nb, err := reader.NameBinding(ctx, runID, tgt.SymbolID, tgt.Name)
	if err != nil {
		return TargetBinding{}, fmt.Errorf("capability: binding of %s: %w", tgt.Name, err)
	}
	langs, err := d.LanguageBindings(ctx, runID)
	if err != nil {
		return TargetBinding{}, err
	}
	lb := LanguageBindingFor(langs, nb.Language)

	return TargetBinding{
		Known:               true,
		Name:                tgt.Name,
		Language:            nb.Language,
		LanguageExtracts:    lb.ExtractsReferences(),
		LanguageStats:       lb.ResolutionStats,
		LanguageDefinitions: lb.Definitions,
		References:          nb.References,
		BoundHere:           nb.BoundHere,
		BoundElsewhere:      nb.BoundElsewhere,
		Ambiguous:           nb.Ambiguous,
		Unresolved:          nb.Unresolved,
		Pending:             nb.Pending,
		ProvenOther:         nb.ProvenOther,
		Definitions:         nb.Definitions,
		InstalledCopy:       nb.InstalledCopy,
		WithoutEdge:         nb.WithoutEdge,
	}, nil
}

// LanguageBindings returns per-language binding outcomes for runID, loading
// them once per run. Nil, with no error, when the registry cannot answer.
func (d Deps) LanguageBindings(ctx context.Context, runID int64) (map[string]registry.LanguageBinding, error) {
	reader, ok := d.Registry.(BindingReader)
	if !ok {
		return nil, nil
	}
	load := func() (map[string]registry.LanguageBinding, error) {
		langs, err := reader.LanguageBindings(ctx, runID)
		if err != nil {
			return nil, fmt.Errorf("capability: language bindings for run %d: %w", runID, err)
		}
		return langs, nil
	}
	if d.cache == nil {
		return load()
	}
	return d.cache.getLanguages(runID, load)
}

// languageFamilies groups index language names that are one language to the
// person asking: a TypeScript function is called from .tsx files as readily as
// from .ts files, so judging its coverage by the .ts files alone would describe
// half of its callers.
//
// Deliberately short. Java, Kotlin and Scala share a runtime and are NOT one
// family: grouping them is how a Kotlin symbol came to be described by the
// Java resolution rate.
var languageFamilies = [][]string{
	{"typescript", "tsx"},
	{"javascript", "jsx"},
}

// LanguageBindingFor returns the binding outcomes for language, summed over
// its family.
func LanguageBindingFor(langs map[string]registry.LanguageBinding, language string) registry.LanguageBinding {
	language = strings.ToLower(language)
	members := []string{language}
	for _, fam := range languageFamilies {
		for _, m := range fam {
			if m == language {
				members = fam
			}
		}
	}
	out := registry.LanguageBinding{Language: language}
	for _, m := range members {
		l, ok := langs[m]
		if !ok {
			continue
		}
		out.Files += l.Files
		out.Definitions += l.Definitions
		out.Relationships += l.Relationships
		out.Hierarchy += l.Hierarchy
		out.Total += l.Total
		out.Resolved += l.Resolved
		out.Ambiguous += l.Ambiguous
		out.Unresolved += l.Unresolved
		out.Ignored += l.Ignored
		out.Pending += l.Pending
		out.ByCompiler += l.ByCompiler
	}
	return out
}

// displayLanguages spells the index's language names the way a reader does.
// A name missing here is printed as the index holds it, which is accurate.
var displayLanguages = map[string]string{
	"go": "Go", "java": "Java", "kotlin": "Kotlin", "scala": "Scala",
	"typescript": "TypeScript", "tsx": "TypeScript", "javascript": "JavaScript", "jsx": "JavaScript",
	"python": "Python", "ruby": "Ruby", "rust": "Rust", "php": "PHP", "swift": "Swift",
	"c": "C", "cpp": "C++", "c_sharp": "C#", "csharp": "C#",
}

// DisplayLanguage names an index language for prose. Empty stays empty.
func DisplayLanguage(language string) string {
	if d, ok := displayLanguages[strings.ToLower(language)]; ok {
		return d
	}
	return language
}
