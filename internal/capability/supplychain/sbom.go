package supplychain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// CycloneDX 1.5 (https://cyclonedx.org/docs/1.5/json/).
//
// The document is DETERMINISTIC: the same index and the same lockfiles produce
// the same bytes. No serial number (the specification asks for a random UUID,
// and it is optional), no timestamp unless the caller sets one, no tool
// version — so a committed SBOM changes only when a dependency does, and a
// diff of it is a diff of the dependencies.
//
// It lists DECLARED dependencies. The transitive closure a lockfile holds is
// not here, and the document says so in `compositions` (aggregate: incomplete)
// rather than leaving a consumer to assume it is complete.

// PropertyPrefix namespaces the properties this tool adds to a component.
const PropertyPrefix = "graphlume:"

// The property names a consumer can rely on.
const (
	PropVersionState    = PropertyPrefix + "versionState"
	PropDeclaredVersion = PropertyPrefix + "declaredVersion"
	PropVersionSource   = PropertyPrefix + "versionSource"
	PropDeclaredIn      = PropertyPrefix + "declaredIn"
	PropEcosystem       = PropertyPrefix + "ecosystem"
	PropScope           = PropertyPrefix + "scope"
	PropIndirect        = PropertyPrefix + "indirect"
	PropDeclaredName    = PropertyPrefix + "declaredName"
	PropNote            = PropertyPrefix + "note"

	// The licence found for a component: where it was read (declared-metadata,
	// license-file, first-party or unknown), the file it was read from, how
	// sure the reading is, and why none was found.
	PropLicenseSource     = PropertyPrefix + "licenseSource"
	PropLicensePath       = PropertyPrefix + "licensePath"
	PropLicenseConfidence = PropertyPrefix + "licenseConfidence"
	PropLicenseReason     = PropertyPrefix + "licenseReason"
)

// BOM is a CycloneDX 1.5 document. Field order is the output order.
type BOM struct {
	Schema       string          `json:"$schema"`
	BOMFormat    string          `json:"bomFormat"`
	SpecVersion  string          `json:"specVersion"`
	Version      int             `json:"version"`
	Metadata     BOMMetadata     `json:"metadata"`
	Components   []BOMComponent  `json:"components"`
	Dependencies []BOMDependency `json:"dependencies"`
	Compositions []BOMAggregate  `json:"compositions"`
}

// BOMMetadata describes the document and the project it is about.
type BOMMetadata struct {
	Timestamp  string        `json:"timestamp,omitempty"`
	Tools      BOMTools      `json:"tools"`
	Component  BOMComponent  `json:"component"`
	Properties []BOMProperty `json:"properties,omitempty"`
}

// BOMTools is the 1.5 form of metadata.tools.
type BOMTools struct {
	Components []BOMComponent `json:"components"`
}

// BOMComponent is one package, or the project itself.
type BOMComponent struct {
	Type       string        `json:"type"`
	BOMRef     string        `json:"bom-ref,omitempty"`
	Group      string        `json:"group,omitempty"`
	Name       string        `json:"name"`
	Version    string        `json:"version,omitempty"`
	Scope      string        `json:"scope,omitempty"`
	PURL       string        `json:"purl,omitempty"`
	Licenses   []BOMLicense  `json:"licenses,omitempty"`
	Properties []BOMProperty `json:"properties,omitempty"`
}

// BOMLicense is one entry of a component's licenses: a single SPDX licence id
// as {license: {id}}, or an SPDX expression (MIT OR Apache-2.0) as
// {expression}. CycloneDX 1.5 allows either form, not both in one list.
type BOMLicense struct {
	License    *BOMLicenseID `json:"license,omitempty"`
	Expression string        `json:"expression,omitempty"`
}

// BOMLicenseID names one SPDX licence.
type BOMLicenseID struct {
	ID string `json:"id"`
}

// ComponentLicense is the licence found for a component on this machine (see
// internal/analyze/licenses): an SPDX expression or "unknown", and where it
// was read. It is evidence for a licence review, carried into the SBOM so a
// scanner does not have to find it again.
type ComponentLicense struct {
	Expression string `json:"expression"`
	Source     string `json:"source"`
	Path       string `json:"path,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// BOMProperty is a name/value pair.
type BOMProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// BOMDependency is one node of the dependency graph.
type BOMDependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn"`
}

// BOMAggregate states how complete a part of the document is.
type BOMAggregate struct {
	Aggregate  string   `json:"aggregate"`
	Assemblies []string `json:"assemblies"`
}

// SBOMOptions are the caller's choices.
type SBOMOptions struct {
	// Name is the project's name. Empty means "unnamed".
	Name string
	// Timestamp, when set, is written as metadata.timestamp. Left empty the
	// document carries none and is byte-for-byte reproducible.
	Timestamp string
	// Lockfiles lists the lockfiles versions were read from, for the record.
	Lockfiles []string
}

// SBOMSummary counts what the document holds, for the line printed beside it.
type SBOMSummary struct {
	Components int
	Concrete   int
	ByState    map[VersionState]int
}

const rootRef = "graphlume:project"

// BuildSBOM assembles the document from resolved components.
func BuildSBOM(components []Component, opt SBOMOptions) (BOM, SBOMSummary) {
	name := opt.Name
	if name == "" {
		name = "unnamed"
	}
	bom := BOM{
		Schema:      "http://cyclonedx.org/schema/bom-1.5.schema.json",
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Version:     1,
		Metadata: BOMMetadata{
			Timestamp: opt.Timestamp,
			Tools:     BOMTools{Components: []BOMComponent{{Type: "application", Name: "graphlume"}}},
			Component: BOMComponent{Type: "application", BOMRef: rootRef, Name: name},
			Properties: []BOMProperty{{
				Name: PropertyPrefix + "sbom:scope",
				Value: "declared dependencies only: packages that appear only in a lockfile " +
					"(the transitive closure) are not listed",
			}},
		},
		Components:   []BOMComponent{},
		Dependencies: []BOMDependency{},
	}
	for _, l := range opt.Lockfiles {
		bom.Metadata.Properties = append(bom.Metadata.Properties,
			BOMProperty{Name: PropertyPrefix + "sbom:lockfile", Value: l})
	}

	summary := SBOMSummary{ByState: map[VersionState]int{}}
	byRef := map[string]int{}
	direct := map[string]bool{}
	for _, c := range components {
		ref := bomRef(c)
		if i, seen := byRef[ref]; seen {
			mergeDeclaration(&bom.Components[i], c)
		} else {
			byRef[ref] = len(bom.Components)
			bom.Components = append(bom.Components, bomComponent(c, ref))
			summary.Components++
			summary.ByState[c.State]++
			if c.State.Concrete() {
				summary.Concrete++
			}
		}
		if !c.Indirect {
			direct[ref] = true
		}
	}
	sort.SliceStable(bom.Components, func(i, j int) bool {
		return bom.Components[i].BOMRef < bom.Components[j].BOMRef
	})

	dependsOn := make([]string, 0, len(direct))
	for ref := range direct {
		dependsOn = append(dependsOn, ref)
	}
	sort.Strings(dependsOn)
	bom.Dependencies = append(bom.Dependencies, BOMDependency{Ref: rootRef, DependsOn: dependsOn})
	bom.Compositions = []BOMAggregate{{Aggregate: "incomplete", Assemblies: []string{rootRef}}}
	return bom, summary
}

// bomRef is the component's identity within the document. A concrete package
// is identified by its purl, so the same release declared in two manifests is
// one component. A declaration without a concrete version is identified by
// where it was declared: two ranges over one package are two different
// statements, and merging them would lose one.
func bomRef(c Component) string {
	if c.State.Concrete() && c.PURL != "" {
		return c.PURL
	}
	base := c.PURL
	if base == "" {
		base = string(c.Ecosystem) + ":" + c.Name
	}
	return fmt.Sprintf("%s#%s:%d", base, c.Manifest, c.Line)
}

func bomComponent(c Component, ref string) BOMComponent {
	out := BOMComponent{Type: "library", BOMRef: ref, Name: c.Name, PURL: c.PURL}

	switch c.Ecosystem {
	case deps.EcosystemMaven, deps.EcosystemGradle:
		if group, artifact, ok := strings.Cut(c.Name, ":"); ok {
			out.Group, out.Name = group, artifact
		}
	case deps.EcosystemNPM:
		if strings.HasPrefix(c.Name, "@") {
			if scope, pkg, ok := strings.Cut(c.Name, "/"); ok {
				out.Group, out.Name = scope, pkg
			}
		}
	}

	// The version field holds what was stated: the exact version when there
	// is one, otherwise the manifest's own text. versionState says which.
	out.Version = c.Version
	if !c.State.Concrete() {
		out.Version = c.Declared
	}

	// CycloneDX has no "development" scope; "excluded" is its word for a
	// component that does not ship with the product.
	out.Scope = "required"
	if c.Scope == string(deps.ScopeDev) {
		out.Scope = "excluded"
	}

	out.Properties = []BOMProperty{
		{Name: PropEcosystem, Value: string(c.Ecosystem)},
		{Name: PropVersionState, Value: string(c.State)},
		{Name: PropDeclaredVersion, Value: c.Declared},
		{Name: PropVersionSource, Value: c.VersionSource},
		{Name: PropScope, Value: c.Scope},
	}
	if c.Indirect {
		out.Properties = append(out.Properties, BOMProperty{Name: PropIndirect, Value: "true"})
	}
	if c.DeclaredName != "" {
		out.Properties = append(out.Properties, BOMProperty{Name: PropDeclaredName, Value: c.DeclaredName})
	}
	if c.Note != "" {
		out.Properties = append(out.Properties, BOMProperty{Name: PropNote, Value: c.Note})
	}
	if l := c.License; l != nil {
		out.Licenses = bomLicenses(l.Expression)
		for _, p := range []BOMProperty{
			{Name: PropLicenseSource, Value: l.Source}, {Name: PropLicensePath, Value: l.Path},
			{Name: PropLicenseConfidence, Value: l.Confidence}, {Name: PropLicenseReason, Value: l.Reason},
		} {
			if p.Value != "" {
				out.Properties = append(out.Properties, p)
			}
		}
	}
	out.Properties = append(out.Properties,
		BOMProperty{Name: PropDeclaredIn, Value: fmt.Sprintf("%s:%d", c.Manifest, c.Line)})
	return out
}

// bomLicenses is the CycloneDX licenses list for an SPDX expression: one
// licence id when it is a single id, the expression otherwise, and nothing
// for an unknown licence (whose reason is a property instead).
func bomLicenses(expr string) []BOMLicense {
	expr = strings.TrimSpace(expr)
	if expr == "" || strings.EqualFold(expr, "unknown") {
		return nil
	}
	if !strings.ContainsAny(expr, " ()") {
		return []BOMLicense{{License: &BOMLicenseID{ID: expr}}}
	}
	return []BOMLicense{{Expression: expr}}
}

// mergeDeclaration folds a second declaration of the same release into the
// component already written for it.
func mergeDeclaration(into *BOMComponent, c Component) {
	into.Properties = append(into.Properties,
		BOMProperty{Name: PropDeclaredIn, Value: fmt.Sprintf("%s:%d", c.Manifest, c.Line)})
	if c.Scope != string(deps.ScopeDev) {
		// Shipping anywhere is shipping.
		into.Scope = "required"
		for i := range into.Properties {
			if into.Properties[i].Name == PropScope {
				into.Properties[i].Value = c.Scope
			}
		}
	}
	if !c.Indirect {
		kept := into.Properties[:0]
		for _, p := range into.Properties {
			if p.Name != PropIndirect {
				kept = append(kept, p)
			}
		}
		into.Properties = kept
	}
}

// Encode writes the document as indented JSON with a trailing newline.
//
// HTML escaping is off. encoding/json writes ">" as a six-character Unicode
// escape by default, and a declared version of ">=5.3" must read ">=5.3" in a
// file people diff.
func (b BOM) Encode(w io.Writer) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(b); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
