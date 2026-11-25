package supplychain

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

func events(pairs ...string) []Event {
	var out []Event
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Event{Kind: pairs[i], Version: pairs[i+1]})
	}
	return out
}

func affected(rangeType string, ev []Event, versions ...string) AffectedPackage {
	return AffectedPackage{Ranges: []Range{{Type: rangeType, Events: ev}}, Versions: versions}
}

// TestRangeMatchingPerEcosystem is the table that has to be right. Each row is
// one version against one OSV range, under the ordering its ecosystem uses.
func TestRangeMatchingPerEcosystem(t *testing.T) {
	cases := []struct {
		name    string
		eco     deps.Ecosystem
		version string
		entry   AffectedPackage
		want    MatchStatus
	}{
		// --- semver: npm
		{"npm inside", deps.EcosystemNPM, "4.17.20", affected("SEMVER", events("introduced", "0", "fixed", "4.17.21")), MatchAffected},
		{"npm at the fix", deps.EcosystemNPM, "4.17.21", affected("SEMVER", events("introduced", "0", "fixed", "4.17.21")), MatchNotAffected},
		{"npm after the fix", deps.EcosystemNPM, "4.18.0", affected("SEMVER", events("introduced", "0", "fixed", "4.17.21")), MatchNotAffected},
		{"npm before introduced", deps.EcosystemNPM, "1.9.9", affected("SEMVER", events("introduced", "2.0.0", "fixed", "2.5.0")), MatchNotAffected},
		{"npm at introduced", deps.EcosystemNPM, "2.0.0", affected("SEMVER", events("introduced", "2.0.0", "fixed", "2.5.0")), MatchAffected},
		// 2.10.0 is AFTER 2.5.0. A string comparison says the opposite.
		{"npm numeric not lexical", deps.EcosystemNPM, "2.10.0", affected("SEMVER", events("introduced", "2.0.0", "fixed", "2.5.0")), MatchNotAffected},
		// A pre-release of the fixed version is BEFORE the fix.
		{"npm prerelease of the fix", deps.EcosystemNPM, "2.5.0-rc.1", affected("SEMVER", events("introduced", "2.0.0", "fixed", "2.5.0")), MatchAffected},
		{"npm last_affected inclusive", deps.EcosystemNPM, "1.2.5", affected("ECOSYSTEM", events("introduced", "0", "last_affected", "1.2.5")), MatchAffected},
		{"npm after last_affected", deps.EcosystemNPM, "1.2.6", affected("ECOSYSTEM", events("introduced", "0", "last_affected", "1.2.5")), MatchNotAffected},
		{"npm no fix at all", deps.EcosystemNPM, "99.0.0", affected("SEMVER", events("introduced", "0")), MatchAffected},
		// Two windows in one range: fixed, then reintroduced.
		{"npm second window", deps.EcosystemNPM, "3.1.0", affected("SEMVER",
			events("introduced", "1.0.0", "fixed", "1.5.0", "introduced", "3.0.0", "fixed", "3.2.0")), MatchAffected},
		{"npm between windows", deps.EcosystemNPM, "2.0.0", affected("SEMVER",
			events("introduced", "1.0.0", "fixed", "1.5.0", "introduced", "3.0.0", "fixed", "3.2.0")), MatchNotAffected},
		// Events out of order in the file must not change the answer.
		{"npm unsorted events", deps.EcosystemNPM, "2.0.0", affected("SEMVER",
			events("fixed", "3.2.0", "introduced", "3.0.0", "fixed", "1.5.0", "introduced", "1.0.0")), MatchNotAffected},

		// --- semver: Go (the manifest writes v, OSV does not)
		{"go inside", deps.EcosystemGo, "v0.17.0", affected("SEMVER", events("introduced", "0", "fixed", "0.23.0")), MatchAffected},
		{"go fixed", deps.EcosystemGo, "v0.23.0", affected("SEMVER", events("introduced", "0", "fixed", "0.23.0")), MatchNotAffected},
		{"go pseudo-version before a release", deps.EcosystemGo, "v0.0.0-20220101000000-abcdefabcdef",
			affected("SEMVER", events("introduced", "0", "fixed", "0.1.0")), MatchAffected},
		{"go pseudo-version against a pseudo fix", deps.EcosystemGo, "v0.0.0-20230101000000-abcdefabcdef",
			affected("SEMVER", events("introduced", "0", "fixed", "0.0.0-20220601000000-111111111111")), MatchNotAffected},
		{"go +incompatible", deps.EcosystemGo, "v24.0.7+incompatible", affected("SEMVER", events("introduced", "0", "fixed", "24.0.9")), MatchAffected},

		// --- semver: Cargo
		{"cargo inside", deps.EcosystemCargo, "1.6.0", affected("SEMVER", events("introduced", "1.0.0", "fixed", "1.6.1")), MatchAffected},
		{"cargo fixed", deps.EcosystemCargo, "1.6.1", affected("SEMVER", events("introduced", "1.0.0", "fixed", "1.6.1")), MatchNotAffected},

		// --- PEP 440
		{"pypi inside", deps.EcosystemPyPI, "2.25.0", affected("ECOSYSTEM", events("introduced", "0", "fixed", "2.31.0")), MatchAffected},
		{"pypi fixed", deps.EcosystemPyPI, "2.31.0", affected("ECOSYSTEM", events("introduced", "0", "fixed", "2.31.0")), MatchNotAffected},
		// 2.11.2 < 2.11.3, and 2.11.10 > 2.11.3.
		{"pypi numeric not lexical", deps.EcosystemPyPI, "2.11.10", affected("ECOSYSTEM", events("introduced", "0", "fixed", "2.11.3")), MatchNotAffected},
		// A release candidate of the fixed version is before the fix…
		{"pypi rc of the fix", deps.EcosystemPyPI, "3.2rc1", affected("ECOSYSTEM", events("introduced", "3.0", "fixed", "3.2")), MatchAffected},
		// …and a post-release of it is after.
		{"pypi post of the fix", deps.EcosystemPyPI, "3.2.post1", affected("ECOSYSTEM", events("introduced", "3.0", "fixed", "3.2")), MatchNotAffected},
		// 3.2 and 3.2.0 are the same release.
		{"pypi trailing zero", deps.EcosystemPyPI, "3.2.0", affected("ECOSYSTEM", events("introduced", "3.0", "fixed", "3.2")), MatchNotAffected},
		{"pypi listed version", deps.EcosystemPyPI, "1.0.0", AffectedPackage{Versions: []string{"0.9", "1.0"}}, MatchAffected},

		// --- Maven
		{"maven inside", deps.EcosystemMaven, "2.14.1", affected("ECOSYSTEM", events("introduced", "2.0-beta9", "fixed", "2.15.0")), MatchAffected},
		{"maven fixed", deps.EcosystemMaven, "2.15.0", affected("ECOSYSTEM", events("introduced", "2.0-beta9", "fixed", "2.15.0")), MatchNotAffected},
		{"maven before a beta", deps.EcosystemMaven, "2.0-alpha1", affected("ECOSYSTEM", events("introduced", "2.0-beta9", "fixed", "2.15.0")), MatchNotAffected},
		// 2.9.10.8 is a four-part version: after 2.9.10, before 2.10.0.
		{"maven four parts inside", deps.EcosystemMaven, "2.9.10.4", affected("ECOSYSTEM", events("introduced", "2.9.0", "fixed", "2.9.10.8")), MatchAffected},
		{"maven four parts fixed", deps.EcosystemMaven, "2.9.10.8", affected("ECOSYSTEM", events("introduced", "2.9.0", "fixed", "2.9.10.8")), MatchNotAffected},
		// A SNAPSHOT of the fixed version is before the fix.
		{"maven snapshot of the fix", deps.EcosystemMaven, "2.15.0-SNAPSHOT", affected("ECOSYSTEM", events("introduced", "2.0", "fixed", "2.15.0")), MatchAffected},
		// .RELEASE and .Final are the release itself.
		{"maven .RELEASE", deps.EcosystemMaven, "5.3.18.RELEASE", affected("ECOSYSTEM", events("introduced", "5.3.0", "fixed", "5.3.18")), MatchNotAffected},
		{"maven -jre flavour", deps.EcosystemMaven, "30.0-jre", affected("ECOSYSTEM", events("introduced", "0", "fixed", "32.0.0-android")), MatchAffected},
		// Gradle resolves Maven artifacts: same ordering.
		{"gradle inside", deps.EcosystemGradle, "4.9.3", affected("ECOSYSTEM", events("introduced", "0", "fixed", "4.9.4")), MatchAffected},
	}
	for _, tc := range cases {
		got := matchVersion(tc.eco, tc.version, tc.entry)
		if got.Status != tc.want {
			t.Errorf("%s: %s %s -> %q, want %q (reason: %s)", tc.name, tc.eco, tc.version, got.Status, tc.want, got.Reason)
		}
		if got.Reason == "" {
			t.Errorf("%s: no reason given for %q", tc.name, got.Status)
		}
	}
}

// TestWhatCannotBeComparedIsNeverNotAffected is the safety property of the
// whole report. Each row is a comparison that cannot be made; the only
// acceptable answer is MatchUnknown.
func TestWhatCannotBeComparedIsNeverNotAffected(t *testing.T) {
	semverRange := affected("SEMVER", events("introduced", "1.0.0", "fixed", "2.0.0"))
	cases := []struct {
		name    string
		eco     deps.Ecosystem
		version string
		entry   AffectedPackage
	}{
		{"a Git commit range", deps.EcosystemGo, "v1.2.3", affected("GIT", events("introduced", "0", "fixed", "abcdef0"))},
		// One range clears the version, the other was never evaluated.
		{"a Git range beside a clearing range", deps.EcosystemGo, "v9.0.0", AffectedPackage{Ranges: []Range{
			{Type: "SEMVER", Events: events("introduced", "0", "fixed", "2.0.0")},
			{Type: "GIT", Events: events("introduced", "0", "fixed", "abcdef0")},
		}}},
		{"an unknown range type", deps.EcosystemNPM, "1.2.3", affected("FUTURE", events("introduced", "0"))},
		{"an unreadable dependency version", deps.EcosystemNPM, "not-a-version", semverRange},
		{"an unreadable fixed event", deps.EcosystemNPM, "1.5.0", affected("SEMVER", events("introduced", "1.0.0", "fixed", "soon"))},
		{"an unreadable introduced event", deps.EcosystemPyPI, "1.5", affected("ECOSYSTEM", events("introduced", "one", "fixed", "2.0"))},
		{"a range with no introduced event", deps.EcosystemNPM, "3.0.0", affected("SEMVER", events("fixed", "2.0.0"))},
		{"a range with no events", deps.EcosystemNPM, "3.0.0", affected("SEMVER", nil)},
		{"no range and no version list", deps.EcosystemNPM, "3.0.0", AffectedPackage{}},
		// A list with no range enumerates what was known when it was written;
		// a release it does not list may simply be newer than the advisory.
		{"a version list that does not list it", deps.EcosystemPyPI, "3.0", AffectedPackage{Versions: []string{"1.0", "2.0"}}},
		{"the disputed Maven separator shape", deps.EcosystemMaven, "1.0.0.RC1", affected("ECOSYSTEM", events("introduced", "0", "fixed", "1.0.0-RC2"))},
		{"an ecosystem with no ordering", deps.Ecosystem("nuget"), "1.0.0", semverRange},
	}
	for _, tc := range cases {
		got := matchVersion(tc.eco, tc.version, tc.entry)
		if got.Status != MatchUnknown {
			t.Errorf("%s: status = %q, want %q. A comparison that could not be made must never read as a verdict.",
				tc.name, got.Status, MatchUnknown)
		}
		if got.Reason == "" {
			t.Errorf("%s: could-not-compare with no reason", tc.name)
		}
	}
}

// TestADependencyWithoutAVersionCannotBeCleared: a range, a placeholder or an
// unpinned dependency has no version to compare, whatever the advisory says.
func TestADependencyWithoutAVersionCannotBeCleared(t *testing.T) {
	// An advisory whose fix is ancient: any real release is past it.
	ancient := affected("SEMVER", events("introduced", "0", "fixed", "0.0.1"))
	for _, c := range fixtureComponents(t) {
		if c.State.Concrete() {
			continue
		}
		got := MatchComponent(c, ancient)
		if got.Status != MatchUnknown {
			t.Errorf("%s (%s, declared %q): status %q against an advisory; with no version it must be %q",
				c.Name, c.State, c.Declared, got.Status, MatchUnknown)
		}
		if !strings.Contains(got.Reason, string(c.State)) {
			t.Errorf("%s: reason does not name the version state %q: %s", c.Name, c.State, got.Reason)
		}
	}
}

func TestPackageNameMatching(t *testing.T) {
	cases := []struct {
		component Component
		entry     AffectedPackage
		want      bool
	}{
		{Component{Ecosystem: deps.EcosystemGo, Name: "golang.org/x/net"}, AffectedPackage{Ecosystem: deps.EcosystemGo, Name: "golang.org/x/net"}, true},
		// Go module paths are case-sensitive.
		{Component{Ecosystem: deps.EcosystemGo, Name: "github.com/BurntSushi/toml"}, AffectedPackage{Ecosystem: deps.EcosystemGo, Name: "github.com/burntsushi/toml"}, false},
		// PEP 503: case and separators are not significant.
		{Component{Ecosystem: deps.EcosystemPyPI, Name: "PyYAML"}, AffectedPackage{Ecosystem: deps.EcosystemPyPI, Name: "pyyaml"}, true},
		{Component{Ecosystem: deps.EcosystemPyPI, Name: "Flask_SQLAlchemy"}, AffectedPackage{Ecosystem: deps.EcosystemPyPI, Name: "flask-sqlalchemy"}, true},
		// The same name in another ecosystem is another package.
		{Component{Ecosystem: deps.EcosystemNPM, Name: "requests"}, AffectedPackage{Ecosystem: deps.EcosystemPyPI, Name: "requests"}, false},
		// A Gradle build resolves Maven artifacts.
		{Component{Ecosystem: deps.EcosystemGradle, Name: "com.squareup.okhttp3:okhttp"}, AffectedPackage{Ecosystem: deps.EcosystemMaven, Name: "com.squareup.okhttp3:okhttp"}, true},
		{Component{Ecosystem: deps.EcosystemMaven, Name: "com.squareup.okhttp3:okhttp"}, AffectedPackage{Ecosystem: deps.EcosystemMaven, Name: "com.squareup.okhttp3:okhttp-tls"}, false},
		// An advisory for an ecosystem this tool does not map names nothing.
		{Component{Ecosystem: deps.EcosystemNPM, Name: "openssl"}, AffectedPackage{Ecosystem: "", RawEcosystem: "Debian", Name: "openssl"}, false},
	}
	for _, tc := range cases {
		if got := PackageMatches(tc.component, tc.entry); got != tc.want {
			t.Errorf("PackageMatches(%s %q, %s %q) = %v, want %v", tc.component.Ecosystem, tc.component.Name,
				tc.entry.Ecosystem, tc.entry.Name, got, tc.want)
		}
	}
}

// TestSeveralEntriesForOnePackage: an advisory often carries one affected
// entry per release line. The dependency is affected if ANY entry says so, and
// cleared only if every entry cleared it.
func TestSeveralEntriesForOnePackage(t *testing.T) {
	c := Component{Ecosystem: deps.EcosystemNPM, Name: "lib", Version: "2.1.0", State: StatePinned}
	entry := func(ev []Event, rangeType string) AffectedPackage {
		a := affected(rangeType, ev)
		a.Ecosystem, a.Name = deps.EcosystemNPM, "lib"
		return a
	}
	line1 := entry(events("introduced", "1.0.0", "fixed", "1.9.0"), "SEMVER")
	line2 := entry(events("introduced", "2.0.0", "fixed", "2.4.0"), "SEMVER")
	gitOnly := entry(events("introduced", "0"), "GIT")
	other := AffectedPackage{Ecosystem: deps.EcosystemNPM, Name: "something-else"}

	if m, _, named := matchAdvisory(c, Advisory{Affected: []AffectedPackage{line1, line2}}); !named || m.Status != MatchAffected {
		t.Errorf("affected by the second release line: got %q (named %v)", m.Status, named)
	}
	if m, _, _ := matchAdvisory(c, Advisory{Affected: []AffectedPackage{line1}}); m.Status != MatchNotAffected {
		t.Errorf("only the 1.x line: got %q, want not_affected", m.Status)
	}
	// Cleared by one entry, uncomparable under another: not cleared.
	if m, _, _ := matchAdvisory(c, Advisory{Affected: []AffectedPackage{line1, gitOnly}}); m.Status != MatchUnknown {
		t.Errorf("one clearing entry and one Git-only entry: got %q, want %q", m.Status, MatchUnknown)
	}
	if _, _, named := matchAdvisory(c, Advisory{Affected: []AffectedPackage{other}}); named {
		t.Error("an advisory about another package was reported as naming this one")
	}
}
