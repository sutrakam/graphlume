// Package supplychain answers two questions about a project's declared
// dependencies: what are they, written in a form other tools consume (a
// CycloneDX SBOM), and — given advisories the user supplies — which of them
// fall in an affected range, and does the indexed code actually use them.
//
// # No network, no embedded advisories
//
// Nothing here opens a connection and nothing here ships vulnerability data.
// internal/analyze/deps states why: an advisory list embedded in a binary is
// stale the day it is built, and reporting from it is inventing vulnerability
// information. Advisories are OSV records the USER puts on disk — from
// osv-scanner, or an OSV dump — and this package only reads them. With none
// supplied it reports nothing and says how to get them.
//
// # The failure this package is built to avoid
//
// A security report has one worst outcome: "safe" when it is not. Three rules
// follow, and each is enforced by a type rather than by care:
//
//   - A version comparison that could not be made is a MatchUnknown, with the
//     reason. It is never folded into "not affected". Ranges, placeholders,
//     Git ranges and unparseable versions all land there.
//   - "Not imported" is claimed only where the mapping from a package name to
//     an import path is exact (Go, and npm's static imports and require
//     calls). Where it is a heuristic — a PyPI distribution is not named after
//     its module, a Maven coordinate does not name a Java package, a Rust path
//     needs no `use` — an unmatched package is ReachUnknown, not absent.
//   - Every report carries its own coverage: which languages record imports,
//     which files were not fully parsed, and that only DECLARED dependencies
//     were examined, never a lockfile's transitive closure.
//
// # Layout
//
// version.go orders versions per ecosystem. declared.go turns a manifest row
// into a component with a version state (pinned, locked, range, unpinned,
// unresolved), reading lockfiles for the version of a dependency declared as a
// range. sbom.go writes CycloneDX. osv.go reads advisories, match.go decides
// affected / not affected / unknown, reach.go decides reachability, and
// report.go assembles the answer.
//
// The package reads the code graph through the Index interface it declares, so
// it depends on no storage type and its tests need no database.
package supplychain
