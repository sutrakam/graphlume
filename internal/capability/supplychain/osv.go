package supplychain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// Advisories are OSV records (https://ossf.github.io/osv-schema/) read from
// files the user names. This file reads them; it never fetches one.

// Advisory is one OSV record, reduced to what matching needs.
type Advisory struct {
	ID       string
	Aliases  []string
	Summary  string
	Severity string
	Affected []AffectedPackage
	// Source is the file the record was read from.
	Source string
}

// AffectedPackage is one entry of an advisory's `affected` list.
type AffectedPackage struct {
	// Ecosystem is this tool's name for the ecosystem, or "" when the advisory
	// names one that is not matched here.
	Ecosystem    deps.Ecosystem
	RawEcosystem string
	Name         string
	Ranges       []Range
	Versions     []string
	Symbols      SymbolScope
}

// Range is one OSV range: a type and its events.
type Range struct {
	Type   string
	Events []Event
}

// Event is one point on a range's timeline.
type Event struct {
	// Kind is introduced, fixed, last_affected or limit.
	Kind    string
	Version string
}

// SymbolScope is what an advisory says about WHICH code is vulnerable, where
// it says anything. OSV has no standard field for this; three databases each
// carry their own, and all three are read:
//
//   - the Go vulnerability database: ecosystem_specific.imports[] with a
//     package path and its vulnerable symbols
//   - GitHub advisories: ecosystem_specific.affected_functions[]
//   - RustSec: ecosystem_specific.affects.functions[]
type SymbolScope struct {
	// Packages narrows a Go advisory to import paths within the module. An
	// entry with no Symbols means every symbol of that package.
	Packages []PackageSymbols
	// Functions are fully qualified names in the ecosystem's own spelling:
	// "jinja2.utils.urlize", "smallvec::SmallVec::insert_many".
	Functions []string
}

// Empty reports whether the advisory names no code at all.
func (s SymbolScope) Empty() bool { return len(s.Packages) == 0 && len(s.Functions) == 0 }

// PackageSymbols is one Go package and its vulnerable symbols.
type PackageSymbols struct {
	Path    string
	Symbols []string
}

// LoadProblem is a file that could not be used, and why.
type LoadProblem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// AdvisorySet is everything read from the paths the user supplied, including
// the account of what was NOT usable.
type AdvisorySet struct {
	Advisories []Advisory
	// Files is how many files were opened.
	Files int
	// Unreadable are files that are not valid JSON. They may be advisories
	// this report is therefore missing.
	Unreadable []LoadProblem
	// NotOSV counts JSON files that hold no OSV record.
	NotOSV int
	// Withdrawn counts records skipped because the database withdrew them.
	Withdrawn int
	// Duplicates counts records skipped because the same id was already read.
	Duplicates int
	// OtherEcosystems counts affected entries per ecosystem this tool does
	// not match dependencies for.
	OtherEcosystems map[string]int
}

// maxAdvisoryBytes bounds one advisory file. An OSV record is kilobytes; a
// scanner's whole report is megabytes. Anything beyond this is not read, and
// is reported as unreadable rather than silently skipped.
const maxAdvisoryBytes = 256 << 20

// LoadAdvisories reads OSV records from files and directories.
//
// A path the user NAMED must exist and, if it is a file, must parse: a typo in
// a CI configuration should fail the job, not produce a clean report. Inside a
// directory the rule is softer, because an OSV dump is thousands of files and
// one stray document should not discard the rest — but every file that could
// not be used is counted and returned, never dropped.
func LoadAdvisories(paths []string) (AdvisorySet, error) {
	set := AdvisorySet{OtherEcosystems: map[string]int{}}
	seen := map[string]bool{}

	add := func(records []osvRecord, source string) {
		for _, rec := range records {
			if rec.ID == "" {
				continue
			}
			if rec.Withdrawn != "" {
				set.Withdrawn++
				continue
			}
			if seen[rec.ID] {
				set.Duplicates++
				continue
			}
			seen[rec.ID] = true
			adv := rec.advisory(source)
			for _, a := range adv.Affected {
				if a.Ecosystem == "" && a.RawEcosystem != "" {
					set.OtherEcosystems[a.RawEcosystem]++
				}
			}
			set.Advisories = append(set.Advisories, adv)
		}
	}

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return set, fmt.Errorf("advisories: %w", err)
		}
		if !info.IsDir() {
			set.Files++
			records, recognised, err := readAdvisoryFile(p)
			if err != nil {
				return set, fmt.Errorf("advisories: %s: %w", p, err)
			}
			if !recognised {
				return set, fmt.Errorf("advisories: %s holds no OSV record (expected an object with "+
					"\"id\" and \"affected\", an array of them, or osv-scanner's JSON report)", p)
			}
			add(records, p)
			continue
		}
		// The walk only collects names; the files are read after it. WalkDir
		// visits in lexical order, so the result does not depend on the
		// filesystem's own ordering.
		var found []string
		walkErr := filepath.WalkDir(p, func(file string, d fs.DirEntry, err error) error {
			if err != nil {
				// Recorded, not returned: one unreadable entry must not stop
				// the walk and discard every advisory after it.
				set.Unreadable = append(set.Unreadable, LoadProblem{Path: file, Reason: err.Error()})
				return nil //nolint:nilerr // the error is reported in set.Unreadable
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(file), ".json") {
				found = append(found, file)
			}
			return nil
		})
		if walkErr != nil {
			return set, fmt.Errorf("advisories: %s: %w", p, walkErr)
		}
		// A directory with no JSON file in it is the wrong directory, or an
		// archive nobody unpacked. Reporting "0 advisories, nothing found"
		// for it would be a clean bill of health for a check that read nothing.
		if len(found) == 0 {
			return set, fmt.Errorf("advisories: %s holds no .json file (an OSV dump must be unzipped first)", p)
		}
		for _, file := range found {
			set.Files++
			records, recognised, err := readAdvisoryFile(file)
			switch {
			case err != nil:
				set.Unreadable = append(set.Unreadable, LoadProblem{Path: file, Reason: err.Error()})
			case !recognised:
				set.NotOSV++
			default:
				add(records, file)
			}
		}
	}

	sort.SliceStable(set.Advisories, func(i, j int) bool { return set.Advisories[i].ID < set.Advisories[j].ID })
	sort.Slice(set.Unreadable, func(i, j int) bool { return set.Unreadable[i].Path < set.Unreadable[j].Path })
	return set, nil
}

func readAdvisoryFile(path string) (records []osvRecord, recognised bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if info.Size() > maxAdvisoryBytes {
		return nil, false, fmt.Errorf("larger than %d MB; split it into one record per file", maxAdvisoryBytes>>20)
	}
	src, err := os.ReadFile(path) //nolint:gosec // G304: the path is the user's own --advisories argument, or a file beneath it
	if err != nil {
		return nil, false, err
	}
	return parseAdvisoryJSON(src)
}

// parseAdvisoryJSON decodes one JSON document into OSV records. It accepts:
//
//   - one OSV record
//   - a JSON array of records
//   - an OSV API response: {"vulns": [...]}
//   - osv-scanner's JSON report: results[].packages[].vulnerabilities[]
//
// recognised says whether the document is one of those shapes at all. It is
// separate from the record count because a scanner that found nothing writes
// a report with no records in it, and that file is a usable answer, not a
// mistake. A document of some other shape is recognised=false with no error;
// invalid JSON, or a recognised document with a field of the wrong type, is
// an error — it may be an advisory that could not be read.
func parseAdvisoryJSON(src []byte) (records []osvRecord, recognised bool, err error) {
	src = bytes.TrimSpace(bytes.TrimPrefix(src, []byte("\xef\xbb\xbf")))
	if len(src) == 0 {
		return nil, false, errors.New("empty file")
	}
	if !json.Valid(src) {
		return nil, false, errors.New("not valid JSON")
	}
	switch src[0] {
	case '[':
		var elements []json.RawMessage
		if err := json.Unmarshal(src, &elements); err != nil {
			return nil, false, fmt.Errorf("not valid OSV JSON: %w", err)
		}
		for _, el := range elements {
			if !looksLikeOSV(el) {
				continue
			}
			var rec osvRecord
			if err := json.Unmarshal(el, &rec); err != nil {
				return nil, true, fmt.Errorf("not valid OSV JSON: %w", err)
			}
			records = append(records, rec)
		}
		// An empty array is an empty result; an array of something else is
		// not an advisory file.
		return records, len(elements) == 0 || len(records) > 0, nil
	case '{':
		if !looksLikeOSV(src) {
			return nil, false, nil
		}
		var env struct {
			osvRecord
			Vulns   []osvRecord `json:"vulns"`
			Results []struct {
				Packages []struct {
					Vulnerabilities []osvRecord `json:"vulnerabilities"`
				} `json:"packages"`
			} `json:"results"`
		}
		if err := json.Unmarshal(src, &env); err != nil {
			return nil, true, fmt.Errorf("not valid OSV JSON: %w", err)
		}
		if env.ID != "" {
			records = append(records, env.osvRecord)
		}
		records = append(records, env.Vulns...)
		for _, r := range env.Results {
			for _, p := range r.Packages {
				records = append(records, p.Vulnerabilities...)
			}
		}
		return records, true, nil
	}
	return nil, false, nil
}

// looksLikeOSV reports whether a JSON object has one of the keys that mark a
// record, an API response or a scanner report.
func looksLikeOSV(src []byte) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(src, &keys) != nil {
		return false
	}
	for _, k := range []string{"id", "affected", "vulns", "results"} {
		if _, ok := keys[k]; ok {
			return true
		}
	}
	return false
}

// osvRecord is the wire shape. Fields this tool does not use are not declared,
// and the free-form ones are kept raw so an unexpected type in a field nobody
// reads cannot make a whole record unreadable.
type osvRecord struct {
	ID        string   `json:"id"`
	Aliases   []string `json:"aliases"`
	Summary   string   `json:"summary"`
	Withdrawn string   `json:"withdrawn"`
	Severity  []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		Ranges []struct {
			Type   string           `json:"type"`
			Events []map[string]any `json:"events"`
		} `json:"ranges"`
		Versions          []string        `json:"versions"`
		EcosystemSpecific json.RawMessage `json:"ecosystem_specific"`
	} `json:"affected"`
	DatabaseSpecific json.RawMessage `json:"database_specific"`
}

// eventKinds are read in this order when one event object carries several
// keys, which the schema forbids and real data occasionally does anyway.
var eventKinds = []string{"introduced", "fixed", "last_affected", "limit"}

func (rec osvRecord) advisory(source string) Advisory {
	adv := Advisory{ID: rec.ID, Aliases: rec.Aliases, Summary: strings.TrimSpace(rec.Summary), Source: source}

	var db struct {
		Severity any `json:"severity"`
	}
	if len(rec.DatabaseSpecific) > 0 && json.Unmarshal(rec.DatabaseSpecific, &db) == nil {
		if s, ok := db.Severity.(string); ok {
			adv.Severity = strings.ToLower(s)
		}
	}
	if adv.Severity == "" && len(rec.Severity) > 0 {
		adv.Severity = rec.Severity[0].Score
	}

	for _, a := range rec.Affected {
		pkg := AffectedPackage{
			RawEcosystem: baseEcosystem(a.Package.Ecosystem),
			Name:         a.Package.Name,
			Versions:     a.Versions,
		}
		pkg.Ecosystem = mapEcosystem(pkg.RawEcosystem)
		for _, r := range a.Ranges {
			rng := Range{Type: strings.ToUpper(strings.TrimSpace(r.Type))}
			for _, ev := range r.Events {
				for _, kind := range eventKinds {
					if v, ok := ev[kind].(string); ok {
						rng.Events = append(rng.Events, Event{Kind: kind, Version: strings.TrimSpace(v)})
					}
				}
			}
			pkg.Ranges = append(pkg.Ranges, rng)
		}
		pkg.Symbols = parseSymbolScope(a.EcosystemSpecific)
		adv.Affected = append(adv.Affected, pkg)
	}
	return adv
}

func parseSymbolScope(raw json.RawMessage) SymbolScope {
	var scope SymbolScope
	if len(raw) == 0 {
		return scope
	}
	var spec struct {
		Imports []struct {
			Path    string   `json:"path"`
			Symbols []string `json:"symbols"`
		} `json:"imports"`
		AffectedFunctions []string `json:"affected_functions"`
		Affects           struct {
			Functions []string `json:"functions"`
		} `json:"affects"`
	}
	if json.Unmarshal(raw, &spec) != nil {
		return scope
	}
	for _, imp := range spec.Imports {
		if imp.Path != "" {
			scope.Packages = append(scope.Packages, PackageSymbols{Path: imp.Path, Symbols: imp.Symbols})
		}
	}
	scope.Functions = append(scope.Functions, spec.AffectedFunctions...)
	scope.Functions = append(scope.Functions, spec.Affects.Functions...)
	return scope
}

// baseEcosystem drops an OSV ecosystem's suffix: "Debian:11" is Debian.
func baseEcosystem(e string) string {
	base, _, _ := strings.Cut(strings.TrimSpace(e), ":")
	return base
}

// mapEcosystem translates an OSV ecosystem name to this tool's, or "".
//
// Gradle has no OSV ecosystem of its own: a Gradle build resolves Maven
// artifacts, so a Maven advisory applies to both. MatchEcosystem handles that.
func mapEcosystem(osv string) deps.Ecosystem {
	switch strings.ToLower(osv) {
	case "go":
		return deps.EcosystemGo
	case "npm":
		return deps.EcosystemNPM
	case "pypi":
		return deps.EcosystemPyPI
	case "crates.io":
		return deps.EcosystemCargo
	case "maven":
		return deps.EcosystemMaven
	}
	return ""
}
