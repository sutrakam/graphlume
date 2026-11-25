package cli

import (
	"sort"

	"github.com/sutrakam/graphlume/internal/analyze/licenses"

	"github.com/spf13/cobra"
)

// licenseRow is one dependency of `graphlume licenses --json`: the declared
// dependency as `deps` reports it, and the licence found for it.
type licenseRow struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Ecosystem string `json:"ecosystem"`
	Scope     string `json:"scope,omitempty"`
	Direct    bool   `json:"direct"`
	Manifest  string `json:"manifest,omitempty"`
	Line      int    `json:"line,omitempty"`
	licenses.Result
}

type licenseReportOut struct {
	IndexRevision int64            `json:"index_revision"`
	Total         int              `json:"total"`
	ByEcosystem   map[string]int   `json:"by_ecosystem"`
	LicensesRead  bool             `json:"licenses_read"`
	Summary       licenses.Summary `json:"summary"`
	Dependencies  []licenseRow     `json:"dependencies"`
	ElapsedMS     int64            `json:"elapsed_ms"`
	BudgetSpent   bool             `json:"budget_spent"`
	Note          string           `json:"note"`
}

const licenseReportNote = "Licences are read only from files already on this machine: each dependency's own metadata " +
	"(an installed package.json, a package-lock.json licence field, a project virtualenv's METADATA, the Cargo registry " +
	"cache, the Maven or Gradle cache), then licence files (Go module cache, vendor/, node_modules) identified by a " +
	"built-in classifier. Nothing is downloaded and no package manager is run. An \"unknown\" row says why. " +
	"The repository policy has no licence rules, so no row is reported as denied. This is evidence for a licence review, not legal advice."

// licenseReport prints the licence of every declared dependency. It writes
// no file and runs no toolchain, which is what lets the licenses tool use it.
func licenseReport(cmd *cobra.Command, g *Global) error {
	ctx := cmd.Context()
	s, err := openQuery(ctx, g)
	if err != nil {
		return err
	}
	defer s.close()

	list, err := s.reg.DependencyReport(ctx, s.runID, nil)
	if err != nil {
		return err
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Ecosystem != b.Ecosystem {
			return a.Ecosystem < b.Ecosystem
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Manifest < b.Manifest
	})

	in := make([]licenses.Dependency, len(list))
	for i, d := range list {
		in[i] = licenses.Dependency{Name: d.Name, Version: d.Version, Ecosystem: d.Ecosystem, Manifest: d.Manifest}
	}
	found, st := licenses.Resolve(ctx, licenses.EnvFromOS(g.Config().RepoRoot), in)

	out := licenseReportOut{
		IndexRevision: s.runID, Total: len(list), ByEcosystem: map[string]int{}, LicensesRead: true,
		Summary: licenses.Summarize(found), Dependencies: make([]licenseRow, 0, len(list)),
		ElapsedMS: st.Elapsed.Milliseconds(), BudgetSpent: st.BudgetSpent, Note: licenseReportNote,
	}
	for i, d := range list {
		out.ByEcosystem[d.Ecosystem]++
		out.Dependencies = append(out.Dependencies, licenseRow{
			Name: d.Name, Version: d.Version, Ecosystem: d.Ecosystem, Scope: d.Scope,
			Direct: !d.Indirect, Manifest: d.Manifest, Line: d.Line, Result: found[i],
		})
	}
	return emitJSON(cmd.OutOrStdout(), out)
}
