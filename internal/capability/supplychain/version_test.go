package supplychain

import (
	"errors"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// assertAscending checks that every version in the list sorts strictly before
// every later one, and that the comparison is antisymmetric. A chain is a
// stronger test than pairs: it catches an ordering that is locally right and
// globally inconsistent, which is what a sort over OSV events would trip on.
func assertAscending(t *testing.T, eco deps.Ecosystem, chain []string) {
	t.Helper()
	for i := range chain {
		for j := range chain {
			got, err := CompareVersions(eco, chain[i], chain[j])
			if err != nil {
				t.Errorf("%s: CompareVersions(%q, %q): %v", eco, chain[i], chain[j], err)
				continue
			}
			want := sign(i - j)
			if got != want {
				t.Errorf("%s: CompareVersions(%q, %q) = %d, want %d", eco, chain[i], chain[j], got, want)
			}
		}
	}
}

func assertEqualVersions(t *testing.T, eco deps.Ecosystem, pairs [][2]string) {
	t.Helper()
	for _, p := range pairs {
		for _, order := range [][2]string{{p[0], p[1]}, {p[1], p[0]}} {
			got, err := CompareVersions(eco, order[0], order[1])
			if err != nil {
				t.Errorf("%s: CompareVersions(%q, %q): %v", eco, order[0], order[1], err)
			} else if got != 0 {
				t.Errorf("%s: CompareVersions(%q, %q) = %d, want 0", eco, order[0], order[1], got)
			}
		}
	}
}

// TestSemverPrecedence is the example chain from semver.org §11, plus the
// shapes Go and npm really produce.
func TestSemverPrecedence(t *testing.T) {
	for _, eco := range []deps.Ecosystem{deps.EcosystemNPM, deps.EcosystemGo, deps.EcosystemCargo} {
		assertAscending(t, eco, []string{
			"0.0.0-20210101000000-abcdef123456", // a Go pseudo-version: a pre-release of 0.0.0
			"0.0.1",
			"1.0.0-alpha",
			"1.0.0-alpha.1",
			"1.0.0-alpha.beta",
			"1.0.0-beta",
			"1.0.0-beta.2",
			"1.0.0-beta.11", // numeric identifiers compare as numbers: 11 > 2
			"1.0.0-rc.1",
			"1.0.0",
			"1.0.1",
			"1.2.0",
			"1.10.0", // not a string comparison: 10 > 2
			"2.0.0",
			"10.0.0",
		})
		assertEqualVersions(t, eco, [][2]string{
			{"v1.2.3", "1.2.3"},              // Go writes the v, OSV does not
			{"1.2.3+incompatible", "1.2.3"},  // build metadata has no precedence
			{"1.2.3+build.1", "1.2.3+other"}, // ... whatever it says
			{"1.2", "1.2.0"},                 // OSV events are occasionally written short
			{"0", "0.0.0"},
		})
	}
}

// TestSemverLongNumbersDoNotOverflow: a Go pseudo-version's timestamp is 14
// digits and some registries hold longer numbers. Converting to an integer
// would wrap, and a wrapped number is a wrong ordering.
func TestSemverLongNumbersDoNotOverflow(t *testing.T) {
	assertAscending(t, deps.EcosystemGo, []string{
		"1.0.0-0.20231231235959-aaaaaaaaaaaa",
		"1.0.0-0.20240101000000-aaaaaaaaaaaa",
		"99999999999999999999.0.0",
		"100000000000000000000.0.0",
	})
}

func TestSemverRefusesWhatItCannotRead(t *testing.T) {
	for _, bad := range []string{"", "latest", "^1.2.3", "1.2.3.4", "1.x", "1.2.3-", "1..2", "=1.2.3", ">=1.0"} {
		if _, err := CompareVersions(deps.EcosystemNPM, bad, "1.0.0"); err == nil {
			t.Errorf("CompareVersions(%q, 1.0.0) returned no error; an unreadable version must not be ordered", bad)
		}
		if _, err := CompareVersions(deps.EcosystemNPM, "1.0.0", bad); err == nil {
			t.Errorf("CompareVersions(1.0.0, %q) returned no error", bad)
		}
	}
}

// TestPEP440Ordering follows the ordering the PEP itself lays out: dev
// releases before pre-releases, pre-releases before the release, post-releases
// after it, and an epoch above everything.
func TestPEP440Ordering(t *testing.T) {
	assertAscending(t, deps.EcosystemPyPI, []string{
		"1.0.dev456",
		"1.0a1",
		"1.0a2.dev456",
		"1.0a12.dev456",
		"1.0a12",
		"1.0b1.dev456",
		"1.0b2",
		"1.0b2.post345.dev456",
		"1.0b2.post345",
		"1.0rc1.dev456",
		"1.0rc1",
		"1.0",
		"1.0+abc.5",
		"1.0+abc.7",
		"1.0+5",
		"1.0.post456.dev34",
		"1.0.post456",
		"1.0.15",
		"1.1.dev1",
		"1.2",
		"1.10", // 1.10 is after 1.2
		"2!0.1",
	})
}

// TestPEP440Normalisation: the spellings PEP 440 declares equivalent must
// compare equal, or an advisory written one way misses a pin written another.
func TestPEP440Normalisation(t *testing.T) {
	assertEqualVersions(t, deps.EcosystemPyPI, [][2]string{
		{"1.0", "1.0.0"},
		{"1.0", "v1.0"},
		{"1.0a1", "1.0alpha1"},
		{"1.0a1", "1.0.a.1"},
		{"1.0b2", "1.0-beta-2"},
		{"1.0rc1", "1.0c1"},
		{"1.0rc1", "1.0-pre1"},
		{"1.0.post1", "1.0-1"},
		{"1.0.post1", "1.0.rev1"},
		{"1.0.dev0", "1.0.dev"},
		{"1.0", "0!1.0"},
		{"1.0RC1", "1.0rc1"},
		{"1.0+ubuntu.1", "1.0+ubuntu-1"},
	})
}

func TestPEP440RefusesWhatItCannotRead(t *testing.T) {
	for _, bad := range []string{"", "latest", "1.0.x", ">=1.0", "1.0-SNAPSHOT", "one.two"} {
		if _, err := CompareVersions(deps.EcosystemPyPI, bad, "1.0"); err == nil {
			t.Errorf("CompareVersions(%q, 1.0) returned no error", bad)
		}
	}
}

// TestMavenOrdering uses the relations stated in the POM reference's "Version
// Order Specification".
func TestMavenOrdering(t *testing.T) {
	for _, eco := range []deps.Ecosystem{deps.EcosystemMaven, deps.EcosystemGradle} {
		assertAscending(t, eco, []string{
			"1.0-alpha-1",
			"1.0-alpha-2",
			"1.0-beta-1",
			"1.0-milestone-1",
			"1.0-rc-1",
			"1.0-SNAPSHOT",
			"1.0",
			"1.0-sp",
			"1.0-sp-1",
			"1.0-whatever", // an unknown qualifier sorts after the known ones
			"1.0-1",
			"1.0.1",
			"1.1",
			"1.2",
			"1.10",
			"2.0",
			"30.0-android",
			"30.0-jre", // guava's two flavours: unknown qualifiers, alphabetical
			"30.1-jre",
		})
		assertAscending(t, eco, []string{"1-snapshot", "1", "1-sp"})
		assertAscending(t, eco, []string{"1-foo2", "1-foo10"})
		assertAscending(t, eco, []string{"1-foo", "1-1", "1.1"})
		assertAscending(t, eco, []string{"1.foo", "1-1", "1.1"})
		assertAscending(t, eco, []string{"2.9.8", "2.9.10", "2.9.10.1", "2.9.10.8", "2.10.0"})
		assertAscending(t, eco, []string{"5.3.0.M1", "5.3.0.RC1", "5.3.0.RELEASE", "5.3.1.RELEASE"})

		assertEqualVersions(t, eco, [][2]string{
			{"1", "1.0"},
			{"1", "1.0.0"},
			{"1", "1-0"},
			{"1.ga", "1-ga"},
			{"1-ga", "1.0"},
			{"1.0.Final", "1.0"},
			{"1.0.RELEASE", "1.0"},
			{"1-a1", "1-alpha-1"},
			{"1-b2", "1-beta-2"},
			{"1-m3", "1-milestone-3"},
			{"1-cr1", "1-rc1"},
			{"1.0-RC1", "1.0-rc1"},
			{"1.0-SNAPSHOT", "1.0-snapshot"},
		})
	}
}

// TestMavenRefusesTheOneDisputedComparison: Maven 3.9 reversed how a dotted
// qualifier compares with a hyphenated one on the same release. This tool does
// not know which Maven a project builds with, so it must say it cannot compare
// — and must still compare everything around that shape.
func TestMavenRefusesTheOneDisputedComparison(t *testing.T) {
	if _, err := CompareVersions(deps.EcosystemMaven, "1.0.0.RC1", "1.0.0-RC2"); err == nil {
		t.Error("1.0.0.RC1 against 1.0.0-RC2 was ordered; Maven releases disagree on it, so it must be refused")
	}
	if _, err := CompareVersions(deps.EcosystemMaven, "1.0.0-RC2", "1.0.0.RC1"); err == nil {
		t.Error("the refusal must hold in both directions")
	}
	// The POM reference's own example of the old rule is "1.foo" < "1-foo".
	// Maven 3.9 treats the two alike.
	if _, err := CompareVersions(deps.EcosystemMaven, "1.foo", "1-foo"); err == nil {
		t.Error("1.foo against 1-foo was ordered; it is the disputed shape")
	}
	// Same separator on both sides, or different releases: no dispute.
	for _, p := range [][2]string{
		{"1.0.0.RC1", "1.0.0.RC2"}, {"1.0.0-RC1", "1.0.0-RC2"},
		{"1.0.0.RC1", "1.0.1-RC1"}, {"1.0.0.RELEASE", "1.0.0-RC1"}, {"1.0.0.RC1", "1.0.0"},
	} {
		if _, err := CompareVersions(deps.EcosystemMaven, p[0], p[1]); err != nil {
			t.Errorf("CompareVersions(%q, %q) was refused: %v", p[0], p[1], err)
		}
	}
}

func TestUnknownEcosystemHasNoOrdering(t *testing.T) {
	_, err := CompareVersions(deps.Ecosystem("nuget"), "1.0.0", "2.0.0")
	if !errors.Is(err, ErrNoScheme) {
		t.Errorf("an ecosystem with no ordering returned %v, want ErrNoScheme", err)
	}
}

// FuzzCompareVersions: any two strings either order consistently or are
// refused. A panic, or a comparison that is not antisymmetric, would make the
// event sort in inRange produce an arbitrary answer.
func FuzzCompareVersions(f *testing.F) {
	for _, s := range []string{"1.0.0", "v1.2.3-rc.1+b", "1.0a1.post2.dev3+l.1", "1.0-alpha-1", "2!1.0", "1..2", "", "-", "1.0.0.RC1"} {
		f.Add(s, "1.0.0")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		for _, eco := range []deps.Ecosystem{deps.EcosystemNPM, deps.EcosystemPyPI, deps.EcosystemMaven} {
			ab, errAB := CompareVersions(eco, a, b)
			ba, errBA := CompareVersions(eco, b, a)
			if (errAB == nil) != (errBA == nil) {
				t.Fatalf("%s: comparable one way only: (%q,%q)=%v, (%q,%q)=%v", eco, a, b, errAB, b, a, errBA)
			}
			if errAB == nil && ab != -ba {
				t.Fatalf("%s: not antisymmetric: (%q,%q)=%d, (%q,%q)=%d", eco, a, b, ab, b, a, ba)
			}
		}
	})
}
