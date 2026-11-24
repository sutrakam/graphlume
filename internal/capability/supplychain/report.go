package supplychain

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// Finding is one declared dependency named by one advisory, where the version
// is affected or could not be compared.
type Finding struct {
	Advisory string   `json:"advisory"`
	Aliases  []string `json:"aliases,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Severity string   `json:"severity,omitempty"`
	Source   string   `json:"advisory_file"`

	Package Component `json:"package"`

	Status  MatchStatus `json:"status"`
	Reason  string      `json:"reason"`
	FixedIn []string    `json:"fixed_in,omitempty"`

	Reachability Reachability `json:"reachability"`
}

// Cleared is a dependency an advisory names whose version was compared and
// found outside every affected range. Listed, not just counted, so the
// comparison can be audited.
type Cleared struct {
	Advisory string `json:"advisory"`
	Package  string `json:"package"`
	Version  string `json:"version"`
	Manifest string `json:"manifest"`
	Reason   string `json:"reason"`
}

// Summary counts the report.
type Summary struct {
	Dependencies int `json:"dependencies"`
	Advisories   int `json:"advisories"`
	// AdvisoriesNamingNothing counts advisories that name no declared
	// dependency. Not declared is not the same as not present: see
	// DeclaredOnlyCaveat.
	AdvisoriesNamingNothing int `json:"advisories_naming_no_declared_dependency"`
	Affected                int `json:"affected"`
	CouldNotCompare         int `json:"could_not_compare"`
	NotAffected             int `json:"not_affected"`

	// The four reachability outcomes, over Affected + CouldNotCompare.
	Referenced       int `json:"referenced"`
	Imported         int `json:"imported_symbol_use_unknown"`
	NotImported      int `json:"not_imported"`
	ImportUseUnknown int `json:"import_use_unknown"`
}

// AdvisoryStats is the account of what was read.
type AdvisoryStats struct {
	Files      int           `json:"files_read"`
	Records    int           `json:"records"`
	Withdrawn  int           `json:"withdrawn_skipped"`
	Duplicates int           `json:"duplicates_skipped"`
	NotOSV     int           `json:"json_files_without_an_osv_record"`
	Unreadable []LoadProblem `json:"unreadable"`
	Ignored    []string      `json:"ignored"`
	// OtherEcosystems counts affected entries for ecosystems not matched here.
	OtherEcosystems map[string]int `json:"other_ecosystems,omitempty"`
	// GoStandardLibrary counts advisories about Go's own standard library or
	// toolchain, which is not a declared dependency and is not evaluated.
	GoStandardLibrary int `json:"go_standard_library_not_evaluated"`
}

// LanguageCoverage is one source language's standing in the index.
type LanguageCoverage struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
	// BindsImports is whether the index records this language's imports at
	// all. Files in a language that does not are invisible to "not imported".
	BindsImports bool `json:"binds_imports"`
	NotParsed    int  `json:"files_not_fully_parsed"`
}

// UnparsedFile is a source file the parser did not read completely.
type UnparsedFile struct {
	Path   string `json:"path"`
	Status string `json:"parse_status"`
}

// EcosystemCoverage states, for one ecosystem with declared dependencies, how
// strong each kind of answer is.
type EcosystemCoverage struct {
	Ecosystem     deps.Ecosystem `json:"ecosystem"`
	Dependencies  int            `json:"dependencies"`
	Concrete      int            `json:"with_a_concrete_version"`
	VersionScheme string         `json:"version_ordering"`
	// NotImportedIsClaimed is whether "not imported" can be said for this
	// ecosystem at all, and for which packages.
	NotImportedIsClaimed string             `json:"not_imported_is_claimed"`
	SymbolBinding        string             `json:"symbol_binding"`
	Languages            []LanguageCoverage `json:"languages"`
	// FilesNotParsed lists source files of these languages that were not
	// fully parsed; an import in one of them is not in the index.
	FilesNotParsed      []UnparsedFile `json:"files_not_fully_parsed"`
	FilesNotParsedTotal int            `json:"files_not_fully_parsed_total"`
	Notes               []string       `json:"notes,omitempty"`
}

// Report is the reachability report.
type Report struct {
	Summary     Summary             `json:"summary"`
	Findings    []Finding           `json:"findings"`
	NotAffected []Cleared           `json:"not_affected"`
	Advisories  AdvisoryStats       `json:"advisories"`
	Coverage    []EcosystemCoverage `json:"coverage"`
	// Caveats hold of every report, whatever it found.
	Caveats []string `json:"caveats"`
}

// The caveats every report carries. Constants, so they cannot be forgotten at
// a call site and so a test can require them verbatim.
const (
	// DeclaredOnlyCaveat is the largest thing this report does not cover.
	DeclaredOnlyCaveat = "Only DECLARED dependencies were checked: the ones a manifest names. " +
		"A package that appears only in a lockfile (the transitive closure) was not examined, " +
		"and most vulnerable packages are transitive. osv-scanner reads lockfiles."

	// FirstPartyCaveat bounds what an import finding means.
	FirstPartyCaveat = "Imports and references are those of first-party INDEXED code. " +
		"\"Not imported\" does not mean unused: another dependency can import the package, a build or " +
		"test tool is executed rather than imported, and reflection or dynamic loading leaves no import."

	// NoVerdictCaveat keeps the narrowest state from being read as clearance.
	NoVerdictCaveat = "\"Imported, symbol use unknown\" is not a finding that the vulnerable code is unused. " +
		"It means the index could not bind a reference to it."
)

// maxUnparsedListed bounds the unparsed files named per ecosystem.
const maxUnparsedListed = 20

// Options are the caller's inputs beyond the data.
type Options struct {
	// Ignore lists advisory ids or aliases to leave out. They are reported as
	// ignored, never dropped silently.
	Ignore []string
	// ImportLanguages are the languages whose imports the index records.
	ImportLanguages []string
}

// Analyze matches advisories against declared components and decides, for each
// match that is not cleared, what the index shows about the package's use.
func Analyze(ctx context.Context, components []Component, set AdvisorySet, idx Index, opt Options) (Report, error) {
	rep := Report{
		Findings:    []Finding{},
		NotAffected: []Cleared{},
		Caveats:     []string{DeclaredOnlyCaveat, FirstPartyCaveat, NoVerdictCaveat},
		Advisories: AdvisoryStats{
			Files: set.Files, Withdrawn: set.Withdrawn, Duplicates: set.Duplicates, NotOSV: set.NotOSV,
			Unreadable: set.Unreadable, Ignored: []string{}, OtherEcosystems: set.OtherEcosystems,
		},
	}
	if rep.Advisories.Unreadable == nil {
		rep.Advisories.Unreadable = []LoadProblem{}
	}
	rep.Summary.Dependencies = len(components)

	files, err := idx.Files(ctx)
	if err != nil {
		return rep, err
	}
	languageOf := make(map[string]string, len(files))
	for _, f := range files {
		languageOf[f.Path] = f.Language
	}

	sites, err := idx.ImportSites(ctx)
	if err != nil {
		return rep, err
	}
	required, requireUnread, err := requireSites(ctx, idx, languageOf)
	if err != nil {
		return rep, err
	}
	sites = append(sites, required...)

	r := &reacher{
		ctx: ctx, idx: idx, imports: newImportIndex(sites, components), refs: map[string][]RefSite{},
		importable: countImportable(files, opt.ImportLanguages),
	}

	ignored := map[string]bool{}
	for _, id := range opt.Ignore {
		ignored[strings.TrimSpace(id)] = true
	}
	byPackage := indexComponents(components)

	for _, adv := range set.Advisories {
		if isIgnored(adv, ignored) {
			rep.Advisories.Ignored = append(rep.Advisories.Ignored, adv.ID)
			continue
		}
		rep.Advisories.Records++
		if namesGoStandardLibrary(adv) {
			rep.Advisories.GoStandardLibrary++
		}
		namedAny := false
		for _, i := range byPackage.named(adv) {
			c := components[i]
			match, scope, named := matchAdvisory(c, adv)
			if !named {
				continue
			}
			namedAny = true
			if match.Status == MatchNotAffected {
				rep.NotAffected = append(rep.NotAffected, Cleared{
					Advisory: adv.ID, Package: c.Name, Version: c.Version,
					Manifest: fmt.Sprintf("%s:%d", c.Manifest, c.Line), Reason: match.Reason,
				})
				continue
			}
			reach, err := r.reach(c, scope)
			if err != nil {
				return rep, err
			}
			rep.Findings = append(rep.Findings, Finding{
				Advisory: adv.ID, Aliases: adv.Aliases, Summary: adv.Summary, Severity: adv.Severity,
				Source: adv.Source, Package: c, Status: match.Status, Reason: match.Reason,
				FixedIn: match.FixedIn, Reachability: reach,
			})
		}
		if namedAny {
			continue
		}
		// No manifest declares what this advisory names. That is usually the
		// end of it — but source can import a package nobody declared: a
		// transitive dependency used directly, or a manifest this tool does
		// not read. An import of a package an advisory names is not something
		// to drop on the grounds that a manifest forgot it.
		undeclared, err := r.undeclaredImports(adv)
		if err != nil {
			return rep, err
		}
		if len(undeclared) == 0 {
			rep.Summary.AdvisoriesNamingNothing++
		}
		rep.Findings = append(rep.Findings, undeclared...)
	}
	sort.Strings(rep.Advisories.Ignored)

	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if (a.Status == MatchAffected) != (b.Status == MatchAffected) {
			return a.Status == MatchAffected
		}
		if a.Reachability.State.rank() != b.Reachability.State.rank() {
			return a.Reachability.State.rank() > b.Reachability.State.rank()
		}
		if a.Package.Ecosystem != b.Package.Ecosystem {
			return a.Package.Ecosystem < b.Package.Ecosystem
		}
		if a.Package.Name != b.Package.Name {
			return a.Package.Name < b.Package.Name
		}
		if a.Advisory != b.Advisory {
			return a.Advisory < b.Advisory
		}
		return a.Package.Manifest < b.Package.Manifest
	})
	sort.SliceStable(rep.NotAffected, func(i, j int) bool {
		a, b := rep.NotAffected[i], rep.NotAffected[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Advisory < b.Advisory
	})

	rep.Summary.Advisories = rep.Advisories.Records
	rep.Summary.NotAffected = len(rep.NotAffected)
	for _, f := range rep.Findings {
		if f.Status == MatchAffected {
			rep.Summary.Affected++
		} else {
			rep.Summary.CouldNotCompare++
		}
		switch f.Reachability.State {
		case ReachReferenced:
			rep.Summary.Referenced++
		case ReachImported:
			rep.Summary.Imported++
		case ReachNotImported:
			rep.Summary.NotImported++
		case ReachUnknown:
			rep.Summary.ImportUseUnknown++
		}
	}

	// An ecosystem can appear in the findings with no declared dependency at
	// all — an undeclared package that is imported. Its answers need their
	// coverage stated like any other.
	var undeclared []deps.Ecosystem
	for _, f := range rep.Findings {
		if f.Package.Manifest == "" {
			undeclared = append(undeclared, f.Package.Ecosystem)
		}
	}
	rep.Coverage = coverage(components, undeclared, files, opt.ImportLanguages, requireUnread)
	return rep, nil
}

// undeclaredImports reports the packages an advisory names that no manifest
// declares and that indexed code imports anyway. There is no declared version,
// so the status is always "could not compare".
func (r *reacher) undeclaredImports(adv Advisory) ([]Finding, error) {
	var out []Finding
	seen := map[string]bool{}
	for _, a := range adv.Affected {
		key := string(a.Ecosystem) + "\x00" + a.Name
		if a.Name == "" || seen[key] {
			continue
		}
		seen[key] = true
		sites := r.imports.undeclaredSites(a.Ecosystem, a.Name)
		if len(sites) == 0 {
			continue
		}
		c := Component{
			Ecosystem: a.Ecosystem, Name: a.Name, State: StateUnresolved,
			Note:          "no manifest this tool reads declares it",
			VersionSource: "not declared", PURL: PURL(a.Ecosystem, a.Name, ""),
		}
		reach, err := r.reachSites(c, a.Symbols, sites)
		if err != nil {
			return nil, err
		}
		out = append(out, Finding{
			Advisory: adv.ID, Aliases: adv.Aliases, Summary: adv.Summary, Severity: adv.Severity, Source: adv.Source,
			Package: c, Status: MatchUnknown,
			Reason: "no manifest declares this package, so there is no declared version to compare; " +
				"it is imported all the same — a transitive dependency used directly, or one declared " +
				"somewhere this tool does not read",
			Reachability: reach,
		})
	}
	return out, nil
}

// componentIndex finds the components an advisory can name without comparing
// every advisory with every component. It is a pre-filter only: matchAdvisory
// still decides, with PackageMatches, whether an entry names a component.
type componentIndex map[string][]int

// packageKey is the name an advisory and a component must share, normalised
// the way sameName compares it.
func packageKey(eco deps.Ecosystem, name string) string {
	if eco == deps.EcosystemGradle {
		eco = deps.EcosystemMaven
	}
	switch eco {
	case deps.EcosystemPyPI:
		name = pypiNormalize(name)
	case deps.EcosystemNPM:
		name = strings.ToLower(name)
	}
	return string(eco) + "\x00" + name
}

func indexComponents(components []Component) componentIndex {
	ix := componentIndex{}
	for i, c := range components {
		key := packageKey(c.Ecosystem, c.Name)
		ix[key] = append(ix[key], i)
		if c.DeclaredName != "" {
			if declared := packageKey(c.Ecosystem, c.DeclaredName); declared != key {
				ix[declared] = append(ix[declared], i)
			}
		}
	}
	return ix
}

// named returns the indices of the components an advisory may name, ascending.
func (ix componentIndex) named(adv Advisory) []int {
	var out []int
	seen := map[int]bool{}
	for _, a := range adv.Affected {
		if a.Ecosystem == "" {
			continue
		}
		for _, i := range ix[packageKey(a.Ecosystem, a.Name)] {
			if !seen[i] {
				seen[i] = true
				out = append(out, i)
			}
		}
	}
	sort.Ints(out)
	return out
}

func isIgnored(adv Advisory, ignored map[string]bool) bool {
	if ignored[adv.ID] {
		return true
	}
	for _, alias := range adv.Aliases {
		if ignored[alias] {
			return true
		}
	}
	return false
}

// namesGoStandardLibrary reports an advisory about Go itself. The Go
// vulnerability database files those under the pseudo-modules "stdlib" and
// "toolchain"; the Go version is not a dependency a manifest declares, so they
// are counted and said to be unevaluated rather than silently unmatched.
func namesGoStandardLibrary(adv Advisory) bool {
	for _, a := range adv.Affected {
		if a.Ecosystem == deps.EcosystemGo && (a.Name == "stdlib" || a.Name == "toolchain") {
			return true
		}
	}
	return false
}

// matchAdvisory combines every affected entry of one advisory that names the
// component. An advisory often carries several entries for one package — one
// per release line — and the component is affected if ANY of them says so,
// uncompared if any could not be compared, and cleared only if all cleared it.
func matchAdvisory(c Component, adv Advisory) (m Match, scope SymbolScope, named bool) {
	var unknown, cleared *Match
	var unknownScope SymbolScope
	for _, a := range adv.Affected {
		if !PackageMatches(c, a) {
			// A replaced Go module still answers to the name the manifest
			// declares, but the code behind it is something else.
			if c.DeclaredName != "" && ecosystemMatches(c.Ecosystem, a.Ecosystem) &&
				sameName(c.Ecosystem, c.DeclaredName, a.Name) {
				named = true
				if unknown == nil {
					unknown = &Match{Status: MatchUnknown, Reason: fmt.Sprintf(
						"the manifest declares %s but builds %s in its place; whether the replacement "+
							"carries the code this advisory is about is not known", c.DeclaredName, c.Name)}
					unknownScope = a.Symbols
				}
			}
			continue
		}
		named = true
		got := MatchComponent(c, a)
		switch got.Status {
		case MatchAffected:
			return got, a.Symbols, true
		case MatchUnknown:
			if unknown == nil {
				unknown, unknownScope = &got, a.Symbols
			}
		case MatchNotAffected:
			if cleared == nil {
				cleared = &got
			}
		}
	}
	switch {
	case !named:
		return Match{}, SymbolScope{}, false
	case unknown != nil:
		return *unknown, unknownScope, true
	case cleared != nil:
		return *cleared, SymbolScope{}, true
	}
	return Match{Status: MatchUnknown, Reason: "the advisory names this package but gives no usable range"},
		SymbolScope{}, true
}

func coverage(components []Component, undeclared []deps.Ecosystem, files []FileInfo, importLanguages []string, requireUnread int) []EcosystemCoverage {
	binds := map[string]bool{}
	for _, l := range importLanguages {
		binds[l] = true
	}
	byEco := map[deps.Ecosystem]*EcosystemCoverage{}
	var order []deps.Ecosystem
	entry := func(eco deps.Ecosystem) *EcosystemCoverage {
		ec, ok := byEco[eco]
		if !ok {
			ec = &EcosystemCoverage{
				Ecosystem: eco, VersionScheme: SchemeName(eco),
				Languages: []LanguageCoverage{}, FilesNotParsed: []UnparsedFile{},
			}
			byEco[eco] = ec
			order = append(order, eco)
		}
		return ec
	}
	for _, c := range components {
		ec := entry(c.Ecosystem)
		ec.Dependencies++
		if c.State.Concrete() {
			ec.Concrete++
		}
	}
	for _, eco := range undeclared {
		entry(eco)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	out := make([]EcosystemCoverage, 0, len(order))
	for _, eco := range order {
		ec := byEco[eco]
		sourceFiles := 0
		for _, lang := range ecosystemLanguages[eco] {
			lc := LanguageCoverage{Language: lang, BindsImports: binds[lang]}
			for _, f := range files {
				if f.Language != lang {
					continue
				}
				lc.Files++
				if f.ParseStatus != "ok" {
					lc.NotParsed++
					ec.FilesNotParsedTotal++
					if len(ec.FilesNotParsed) < maxUnparsedListed {
						ec.FilesNotParsed = append(ec.FilesNotParsed, UnparsedFile{Path: f.Path, Status: f.ParseStatus})
					}
				}
			}
			sourceFiles += lc.Files
			if lc.Files > 0 || lc.BindsImports {
				ec.Languages = append(ec.Languages, lc)
			}
			if lc.Files > 0 && !lc.BindsImports {
				ec.Notes = append(ec.Notes, fmt.Sprintf("%d %s file(s) are indexed but this index records no imports "+
					"for %s: an import there is invisible to this report", lc.Files, lang, lang))
			}
		}
		sort.Slice(ec.FilesNotParsed, func(i, j int) bool { return ec.FilesNotParsed[i].Path < ec.FilesNotParsed[j].Path })
		if sourceFiles == 0 {
			// Dependencies are declared and there is no source that could
			// import them. Every "not imported" below is true for that reason
			// alone, and says nothing about the project.
			ec.Notes = append(ec.Notes, fmt.Sprintf("NO source file in a language that can import these packages is "+
				"indexed (%s): nothing here can be found imported, whatever the project does",
				strings.Join(ecosystemLanguages[eco], ", ")))
		}

		switch eco {
		case deps.EcosystemGo:
			ec.NotImportedIsClaimed = "yes: a Go file must import a package to use it, and the import path names the module"
			ec.SymbolBinding = "package-level functions called as pkg.Func, where the advisory lists symbols " +
				"(the Go vulnerability database does); methods are not bound"
		case deps.EcosystemNPM:
			ec.NotImportedIsClaimed = "yes: static import/export-from statements and require() calls with a literal argument"
			ec.SymbolBinding = "none: an imported npm package is always \"imported, symbol use unknown\""
			ec.Notes = append(ec.Notes, "dynamic import() is not recorded as an import")
			if requireUnread > 0 {
				ec.Notes = append(ec.Notes, fmt.Sprintf("%d require() call(s) have an argument this report could not "+
					"read (computed, split across lines, or in a file with no indexed text): those are imports it cannot see",
					requireUnread))
			}
		case deps.EcosystemPyPI:
			ec.NotImportedIsClaimed = fmt.Sprintf("only for the %d well-known distributions whose module names are "+
				"built in; for any other, no match is reported as \"import use unknown\"", len(pypiModules))
			ec.SymbolBinding = "module-level functions, where the advisory lists affected_functions; methods are not bound"
		case deps.EcosystemCargo:
			ec.NotImportedIsClaimed = "no: a crate can be used by path with no `use` declaration, so no match is " +
				"reported as \"import use unknown\""
			ec.SymbolBinding = "a `use` declaration naming the affected function itself; methods are not bound"
		case deps.EcosystemMaven, deps.EcosystemGradle:
			ec.NotImportedIsClaimed = "no: a coordinate does not name its Java packages, so no match is " +
				"reported as \"import use unknown\""
			ec.SymbolBinding = "none"
			ec.Notes = append(ec.Notes, "an import match is on the groupId prefix: it shows that SOME artifact of the "+
				"group is imported, not necessarily this one")
		}
		out = append(out, *ec)
	}
	return out
}

// ---------------------------------------------------------------- gate

// GateLevel is the threshold for a non-zero exit.
type GateLevel string

const (
	// GateReferenced fails only when a symbol an advisory names is referenced.
	// The narrowest gate: everything the index could not bind passes it.
	GateReferenced GateLevel = "referenced"
	// GateImported fails when the package is imported, or when whether it is
	// imported could not be determined.
	GateImported GateLevel = "imported"
	// GateAffected fails on any dependency an advisory matches, imported or not.
	GateAffected GateLevel = "affected"
)

// ParseGateLevel validates a --fail-on value.
func ParseGateLevel(s string) (GateLevel, bool) {
	switch GateLevel(s) {
	case GateReferenced, GateImported, GateAffected:
		return GateLevel(s), true
	}
	return "", false
}

func (g GateLevel) threshold() int {
	switch g {
	case GateReferenced:
		return 3
	case GateImported:
		return 2
	}
	return 1
}

// Gate returns the reasons the report fails at a level; none means it passes.
//
// Two things fail a gate that are not confirmed vulnerabilities, on purpose. A
// dependency whose version COULD NOT BE COMPARED counts exactly as an affected
// one does — a gate that passes on a comparison nobody made is a gate that
// reports "safe" without having looked. And an advisory file that could not be
// read fails every level, for the same reason.
func (r Report) Gate(level GateLevel) []string {
	var reasons []string
	var affected, uncompared int
	for _, f := range r.Findings {
		if f.Reachability.State.rank() < level.threshold() {
			continue
		}
		if f.Status == MatchAffected {
			affected++
		} else {
			uncompared++
		}
	}
	if affected > 0 {
		reasons = append(reasons, fmt.Sprintf("%d affected dependency finding(s) at reachability %q or stronger", affected, level))
	}
	if uncompared > 0 {
		reasons = append(reasons, fmt.Sprintf("%d finding(s) at reachability %q or stronger whose version could not be "+
			"compared, which is not the same as unaffected", uncompared, level))
	}
	if n := len(r.Advisories.Unreadable); n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d advisory file(s) could not be read, so this report may be missing advisories", n))
	}
	return reasons
}
