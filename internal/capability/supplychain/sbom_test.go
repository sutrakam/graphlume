package supplychain

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

func encodeFixtureSBOM(t *testing.T, opt SBOMOptions) ([]byte, SBOMSummary) {
	t.Helper()
	resolver := NewResolver(fixtureReader)
	components := resolver.ResolveAll(fixtureDeclared(t))
	opt.Lockfiles = resolver.LockfilesRead()
	bom, summary := BuildSBOM(components, opt)
	var buf bytes.Buffer
	if err := bom.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes(), summary
}

// TestSBOMIsCycloneDX15 decodes the document generically — as a consumer
// would — and checks the fields the 1.5 schema requires and the ones this tool
// promises.
func TestSBOMIsCycloneDX15(t *testing.T) {
	raw, summary := encodeFixtureSBOM(t, SBOMOptions{Name: "example.com/app"})

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the SBOM is not valid JSON: %v", err)
	}
	if doc["bomFormat"] != "CycloneDX" || doc["specVersion"] != "1.5" || doc["version"] != float64(1) {
		t.Errorf("header = %v / %v / %v, want CycloneDX / 1.5 / 1", doc["bomFormat"], doc["specVersion"], doc["version"])
	}
	if _, has := doc["serialNumber"]; has {
		t.Error("the document carries a serialNumber; a random UUID would make it differ on every run")
	}
	meta := doc["metadata"].(map[string]any)
	if _, has := meta["timestamp"]; has {
		t.Error("the document carries a timestamp although none was requested")
	}
	if root := meta["component"].(map[string]any); root["name"] != "example.com/app" || root["type"] != "application" {
		t.Errorf("metadata.component = %v", root)
	}

	components := doc["components"].([]any)
	if len(components) != summary.Components || len(components) == 0 {
		t.Fatalf("%d components in the document, summary says %d", len(components), summary.Components)
	}
	refs := map[string]bool{}
	validScope := map[string]bool{"required": true, "optional": true, "excluded": true}
	for _, raw := range components {
		c := raw.(map[string]any)
		name, _ := c["name"].(string)
		if c["type"] != "library" || name == "" {
			t.Errorf("component without the required type/name: %v", c)
		}
		ref, _ := c["bom-ref"].(string)
		if ref == "" || refs[ref] {
			t.Errorf("%s: bom-ref %q is empty or not unique", name, ref)
		}
		refs[ref] = true
		if scope, _ := c["scope"].(string); !validScope[scope] {
			t.Errorf("%s: scope %q is not a CycloneDX scope", name, scope)
		}
		if purl, _ := c["purl"].(string); !strings.HasPrefix(purl, "pkg:") {
			t.Errorf("%s: purl %q", name, purl)
		}
		props := properties(c)
		state := VersionState(props[PropVersionState])
		switch state {
		case StatePinned, StateLocked, StateRange, StateUnpinned, StateUnresolved:
		default:
			t.Errorf("%s: %s = %q, not a known state", name, PropVersionState, state)
		}
		// The marker: a purl has a version exactly when the state is concrete.
		purl := strings.TrimPrefix(c["purl"].(string), "pkg:npm/%40")
		if strings.Contains(purl, "@") != state.Concrete() {
			t.Errorf("%s: state %q but purl %q", name, state, c["purl"])
		}
		for _, required := range []string{PropEcosystem, PropDeclaredIn, PropVersionSource, PropScope} {
			if props[required] == "" {
				t.Errorf("%s: property %s is missing", name, required)
			}
		}
	}

	// The dependency graph names the project and only components that exist.
	graph := doc["dependencies"].([]any)
	if len(graph) != 1 {
		t.Fatalf("%d dependency nodes, want the project alone", len(graph))
	}
	node := graph[0].(map[string]any)
	if node["ref"] != meta["component"].(map[string]any)["bom-ref"] {
		t.Errorf("dependency node ref %v is not the project", node["ref"])
	}
	for _, d := range node["dependsOn"].([]any) {
		if !refs[d.(string)] {
			t.Errorf("dependsOn names %q, which is not a component", d)
		}
	}
	// golang.org/x/sys is marked indirect: listed, but not a direct dependency.
	for _, d := range node["dependsOn"].([]any) {
		if strings.Contains(d.(string), "golang.org/x/sys") {
			t.Error("an indirect dependency is listed as depended on directly by the project")
		}
	}
	if !refs["pkg:golang/golang.org/x/sys@v0.1.0"] {
		t.Error("the indirect dependency is missing from the components")
	}

	// The document says of itself that it is not the whole closure.
	comp := doc["compositions"].([]any)[0].(map[string]any)
	if comp["aggregate"] != "incomplete" {
		t.Errorf("compositions aggregate = %v, want incomplete", comp["aggregate"])
	}
}

func properties(component map[string]any) map[string]string {
	out := map[string]string{}
	list, _ := component["properties"].([]any)
	for _, raw := range list {
		p := raw.(map[string]any)
		out[p["name"].(string)] = p["value"].(string)
	}
	return out
}

func sbomComponent(t *testing.T, raw []byte, ref string) map[string]any {
	t.Helper()
	var doc struct {
		Components []map[string]any `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Components {
		if c["bom-ref"] == ref {
			return c
		}
	}
	t.Fatalf("no component with bom-ref %q", ref)
	return nil
}

// TestSBOMStatesVersionsAsDeclared: a pin is the version; a locked range is
// the lockfile's version with the range kept; a bare range is the range, with
// the marker and no version in the purl.
func TestSBOMStatesVersionsAsDeclared(t *testing.T) {
	raw, _ := encodeFixtureSBOM(t, SBOMOptions{})

	express := sbomComponent(t, raw, "pkg:npm/express@4.18.2")
	if express["version"] != "4.18.2" || properties(express)[PropVersionState] != "pinned" {
		t.Errorf("express = %v", express)
	}

	lodash := sbomComponent(t, raw, "pkg:npm/lodash@4.17.20")
	props := properties(lodash)
	if lodash["version"] != "4.17.20" || props[PropVersionState] != "locked" ||
		props[PropDeclaredVersion] != "^4.17.20" || props[PropVersionSource] != "web/package-lock.json" {
		t.Errorf("lodash: version %v, properties %v", lodash["version"], props)
	}

	scoped := sbomComponent(t, raw, "pkg:npm/%40scope/pkg#web/package.json:6")
	props = properties(scoped)
	if scoped["version"] != "~1.2.0" || props[PropVersionState] != "range" || scoped["purl"] != "pkg:npm/%40scope/pkg" {
		t.Errorf("@scope/pkg: version %v, purl %v, state %q", scoped["version"], scoped["purl"], props[PropVersionState])
	}
	if scoped["group"] != "@scope" || scoped["name"] != "pkg" {
		t.Errorf("@scope/pkg: group %v, name %v", scoped["group"], scoped["name"])
	}
	if props[PropNote] == "" {
		t.Error("@scope/pkg: a range carries no note saying why it has no version")
	}

	// A development dependency does not ship: CycloneDX's word is "excluded".
	if jest := sbomComponent(t, raw, "pkg:npm/jest@29.7.0"); jest["scope"] != "excluded" {
		t.Errorf("jest scope = %v, want excluded", jest["scope"])
	}

	log4j := sbomComponent(t, raw, "pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1")
	if log4j["group"] != "org.apache.logging.log4j" || log4j["name"] != "log4j-core" {
		t.Errorf("log4j-core: group %v, name %v", log4j["group"], log4j["name"])
	}

	// ">=5.3" must appear as written, not as an HTML escape.
	if !bytes.Contains(raw, []byte(`">=5.3"`)) {
		t.Error(`the declared version ">=5.3" is not in the document verbatim`)
	}
	// The escape encoding/json would write for ">" by default, assembled so
	// that this file does not itself contain it.
	if bytes.Contains(raw, []byte(`\u`+`003e`)) {
		t.Error("the document HTML-escapes its strings")
	}
}

// TestSBOMIsDeterministic: the same inputs give the same bytes, whatever order
// the declarations arrive in. This is what makes the file committable.
func TestSBOMIsDeterministic(t *testing.T) {
	first, _ := encodeFixtureSBOM(t, SBOMOptions{Name: "p"})
	for i := 0; i < 5; i++ {
		again, _ := encodeFixtureSBOM(t, SBOMOptions{Name: "p"})
		if !bytes.Equal(first, again) {
			t.Fatal("two runs over the same input produced different documents")
		}
	}

	declared := fixtureDeclared(t)
	reversed := make([]Declared, len(declared))
	for i, d := range declared {
		reversed[len(declared)-1-i] = d
	}
	resolver := NewResolver(fixtureReader)
	bom, _ := BuildSBOM(resolver.ResolveAll(reversed), SBOMOptions{Name: "p", Lockfiles: resolver.LockfilesRead()})
	var buf bytes.Buffer
	if err := bom.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, buf.Bytes()) {
		t.Error("the document depends on the order the declarations were read in")
	}

	if !bytes.HasSuffix(first, []byte("}\n")) {
		t.Error("the document does not end with a newline")
	}
}

func TestSBOMTimestampOnlyWhenAsked(t *testing.T) {
	raw, _ := encodeFixtureSBOM(t, SBOMOptions{Timestamp: "2026-01-02T03:04:05Z"})
	var doc struct {
		Metadata struct {
			Timestamp string `json:"timestamp"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Timestamp != "2026-01-02T03:04:05Z" {
		t.Errorf("timestamp = %q", doc.Metadata.Timestamp)
	}
}

// TestSBOMMergesOneReleaseDeclaredTwice: the same release in two manifests is
// one component that names both declarations. Two ranges stay two components.
func TestSBOMMergesOneReleaseDeclaredTwice(t *testing.T) {
	r := NewResolver(nil)
	components := r.ResolveAll([]Declared{
		{Name: "express", Ecosystem: deps.EcosystemNPM, Version: "4.18.2", Scope: "dev", Manifest: "a/package.json", Line: 3},
		{Name: "express", Ecosystem: deps.EcosystemNPM, Version: "4.18.2", Scope: "runtime", Manifest: "b/package.json", Line: 9},
		{Name: "lodash", Ecosystem: deps.EcosystemNPM, Version: "^4.0.0", Scope: "runtime", Manifest: "a/package.json", Line: 4},
		{Name: "lodash", Ecosystem: deps.EcosystemNPM, Version: "^3.0.0", Scope: "runtime", Manifest: "b/package.json", Line: 10},
	})
	bom, summary := BuildSBOM(components, SBOMOptions{})
	if summary.Components != 3 {
		t.Fatalf("%d components, want 3: one express, two lodash ranges", summary.Components)
	}
	var express BOMComponent
	for _, c := range bom.Components {
		if c.BOMRef == "pkg:npm/express@4.18.2" {
			express = c
		}
	}
	var declaredIn []string
	for _, p := range express.Properties {
		if p.Name == PropDeclaredIn {
			declaredIn = append(declaredIn, p.Value)
		}
	}
	if strings.Join(declaredIn, ",") != "a/package.json:3,b/package.json:9" {
		t.Errorf("express declaredIn = %v, want both manifests", declaredIn)
	}
	// Runtime in one manifest is runtime: it ships.
	if express.Scope != "required" {
		t.Errorf("express scope = %q, want required", express.Scope)
	}
}

func TestEmptySBOMIsStillADocument(t *testing.T) {
	bom, summary := BuildSBOM(nil, SBOMOptions{})
	var buf bytes.Buffer
	if err := bom.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if summary.Components != 0 || !strings.Contains(buf.String(), `"components": []`) {
		t.Errorf("an SBOM with no dependencies must carry an empty components array, not null:\n%s", buf.String())
	}
}

// TestThePackageOpensNoConnection is the no-network rule as a test: nothing in
// this package may import a networking package. An advisory database fetched
// at run time is exactly what the feature promises not to do.
func TestThePackageOpensNoConnection(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found; the test is not looking at the package")
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "net" || strings.HasPrefix(path, "net/") || strings.Contains(path, "golang.org/x/net") ||
				path == "os/exec" || path == "plugin" {
				t.Errorf("%s imports %q: this package must not reach the network or run another program", file, path)
			}
		}
	}
}
