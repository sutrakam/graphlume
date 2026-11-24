package supplychain

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// MatchStatus is the answer to "does this version fall in the advisory's
// range". There are three answers, not two, and the third is the one that
// keeps the report honest.
type MatchStatus string

const (
	// MatchAffected: the version was compared and is inside an affected range,
	// or is listed by the advisory.
	MatchAffected MatchStatus = "affected"
	// MatchNotAffected: every range was compared and the version is outside
	// all of them. This is the only status that says "safe from this
	// advisory", and it is returned only when nothing was left uncompared.
	MatchNotAffected MatchStatus = "not_affected"
	// MatchUnknown: the comparison could not be made. The dependency has no
	// concrete version, the advisory's range is a Git range, or one of the
	// versions is not one this ecosystem's ordering can read.
	MatchUnknown MatchStatus = "could_not_compare"
)

// Match is the outcome of comparing one component with one affected entry.
type Match struct {
	Status MatchStatus
	// Reason is one sentence a reader can check against the advisory.
	Reason string
	// FixedIn lists the fixed versions above the component's, where the
	// advisory records any.
	FixedIn []string
}

// PackageMatches reports whether an advisory's affected entry names the
// package a component is, under the ecosystem's own naming rules.
func PackageMatches(c Component, a AffectedPackage) bool {
	if !ecosystemMatches(c.Ecosystem, a.Ecosystem) {
		return false
	}
	return sameName(c.Ecosystem, c.Name, a.Name)
}

func ecosystemMatches(component, advisory deps.Ecosystem) bool {
	if advisory == "" {
		return false
	}
	if component == advisory {
		return true
	}
	// A Gradle build resolves Maven artifacts; OSV has no Gradle ecosystem.
	return component == deps.EcosystemGradle && advisory == deps.EcosystemMaven
}

func sameName(eco deps.Ecosystem, a, b string) bool {
	switch eco {
	case deps.EcosystemPyPI:
		return pypiNormalize(a) == pypiNormalize(b)
	case deps.EcosystemNPM:
		return strings.EqualFold(a, b)
	}
	return a == b
}

// MatchComponent compares a component against one affected entry that names
// its package.
func MatchComponent(c Component, a AffectedPackage) Match {
	if !c.State.Concrete() || c.Version == "" {
		reason := fmt.Sprintf("the dependency has no single version to compare (%s", c.State)
		if c.Declared != "" {
			reason += fmt.Sprintf(", written %q", c.Declared)
		}
		reason += ")"
		if c.Note != "" {
			reason += ": " + c.Note
		}
		return Match{Status: MatchUnknown, Reason: reason}
	}
	return matchVersion(c.Ecosystem, c.Version, a)
}

func matchVersion(eco deps.Ecosystem, version string, a AffectedPackage) Match {
	for _, listed := range a.Versions {
		if sameVersion(eco, version, listed) {
			return Match{Status: MatchAffected,
				Reason: fmt.Sprintf("%s is listed in the advisory's affected versions", version)}
		}
	}

	var (
		uncompared []string
		outside    []string
	)
	for _, r := range a.Ranges {
		switch r.Type {
		case "SEMVER", "ECOSYSTEM":
			in, detail, fixed, err := inRange(eco, version, r.Events)
			if err != nil {
				uncompared = append(uncompared, err.Error())
				continue
			}
			if in {
				return Match{Status: MatchAffected, Reason: detail, FixedIn: fixed}
			}
			outside = append(outside, detail)
		case "GIT":
			uncompared = append(uncompared,
				"the advisory gives a GIT commit range, which needs the upstream repository's history to evaluate")
		default:
			uncompared = append(uncompared, fmt.Sprintf("the advisory uses an unrecognised range type %q", r.Type))
		}
	}

	// Nothing may be left uncompared before "not affected" is said. One Git
	// range beside a version range that clears the dependency is still one
	// range nobody evaluated.
	if len(uncompared) > 0 {
		return Match{Status: MatchUnknown, Reason: uncompared[0]}
	}
	if len(outside) == 0 {
		if len(a.Versions) > 0 {
			return Match{Status: MatchUnknown, Reason: fmt.Sprintf(
				"the advisory enumerates affected versions without a range; %s is not among them, "+
					"which does not establish that a release the list predates is unaffected", version)}
		}
		return Match{Status: MatchUnknown,
			Reason: "the advisory gives neither a version range nor a version list for this package"}
	}
	return Match{Status: MatchNotAffected, Reason: outside[0]}
}

// sameVersion reports whether two version strings name the same release.
// Unparseable input falls back to exact string equality, which can only ever
// fail to match — it cannot produce a false "listed".
func sameVersion(eco deps.Ecosystem, a, b string) bool {
	if c, err := CompareVersions(eco, a, b); err == nil {
		return c == 0
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// inRange replays one OSV range's events against a version.
//
// This is the evaluation algorithm from the OSV schema: sort the events, then
// walk them — an `introduced` at or below the version turns "affected" on, a
// `fixed` at or below it turns it off, a `last_affected` strictly below it
// turns it off. `introduced: "0"` means "from the first release".
//
// The error return is the important part. If ANY event's version cannot be
// ordered against the dependency's, the range was not evaluated and the caller
// must not conclude anything from it.
func inRange(eco deps.Ecosystem, version string, events []Event) (affected bool, detail string, fixedIn []string, err error) {
	if len(events) == 0 {
		return false, "", nil, errors.New("the advisory's range has no events")
	}
	type point struct {
		Event
		// cmp is the dependency's version compared with this event's: -1 when
		// the dependency is older.
		cmp int
	}
	points := make([]point, 0, len(events))
	hasIntroduced := false
	for _, ev := range events {
		p := point{Event: ev}
		if ev.Kind == "introduced" {
			hasIntroduced = true
		}
		if ev.Kind == "introduced" && ev.Version == "0" {
			p.cmp = 1
		} else {
			c, cmpErr := CompareVersions(eco, version, ev.Version)
			if cmpErr != nil {
				return false, "", nil, fmt.Errorf("%s %q could not be ordered against %q under %s: %w",
					ev.Kind, ev.Version, version, SchemeName(eco), cmpErr)
			}
			p.cmp = c
		}
		points = append(points, p)
	}
	if !hasIntroduced {
		return false, "", nil, errors.New("the advisory's range has no introduced event")
	}

	// Order the events among themselves. "0" sorts first.
	var sortErr error
	sort.SliceStable(points, func(i, j int) bool {
		a, b := points[i], points[j]
		aZero := a.Kind == "introduced" && a.Version == "0"
		bZero := b.Kind == "introduced" && b.Version == "0"
		if aZero || bZero {
			return aZero && !bZero
		}
		c, cmpErr := CompareVersions(eco, a.Version, b.Version)
		if cmpErr != nil && sortErr == nil {
			sortErr = cmpErr
		}
		return c < 0
	})
	if sortErr != nil {
		return false, "", nil, fmt.Errorf("the advisory's events could not be ordered under %s: %w",
			SchemeName(eco), sortErr)
	}

	var since, cleared string
	for _, p := range points {
		switch p.Kind {
		case "introduced":
			if p.cmp >= 0 {
				affected, since = true, p.Version
			}
		case "fixed", "limit":
			if p.cmp >= 0 {
				affected = false
				cleared = fmt.Sprintf("%s is at or after the fix %s", version, p.Version)
			} else if p.Kind == "fixed" {
				fixedIn = append(fixedIn, p.Version)
			}
		case "last_affected":
			if p.cmp > 0 {
				affected = false
				cleared = fmt.Sprintf("%s is after the last affected version %s", version, p.Version)
			}
		}
	}

	if affected {
		detail = fmt.Sprintf("%s is at or after the introduced version %s", version, since)
		if since == "0" {
			detail = fmt.Sprintf("%s is in a range affected from the first release", version)
		}
		if len(fixedIn) > 0 {
			detail += fmt.Sprintf(" and before the fix %s", fixedIn[0])
		} else {
			detail += "; the advisory records no fixed version above it"
		}
		return true, detail, fixedIn, nil
	}
	if cleared == "" {
		cleared = fmt.Sprintf("%s is before the first affected version", version)
	}
	return false, cleared, nil, nil
}
