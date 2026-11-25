package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sutrakam/graphlume/internal/security"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The report tools: policy, arch, deps, licenses, vulns, sbom, tour, adr,
// lessons and events. Each answers what the command of the same name answers on the
// command line, read-only, with the CLI's own JSON as the structured result.
//
// THE COMMANDS ARE THE IMPLEMENTATION. A second copy of what `policy check` or
// `vulns` does would drift from it, and the point of these tools is that a
// screen and a terminal show the same facts. So a tool does not reimplement a
// report: it builds the command's arguments from its own typed, validated
// arguments and asks a ReportRunner to run that command in this process. The
// runner is the CLI's (internal/cli/reports.go); this package cannot import it.
//
// What a tool may ask for is therefore fixed here: the command words, and a
// short list of flags, are constants in the handler. A caller's text reaches a
// command only as the value of a flag it was written for (always as --flag=value,
// so a value that begins with a dash is still a value), or after "--" as a
// positional argument. No tool passes a flag that writes a file, sets a gate's
// exit status, or names an output path.

// ReportRunner runs one read-only command of the CLI in this process and
// returns what it wrote to standard output.
//
// args are the words after `graphlume`, without the global flags: the runner
// supplies the project root, --json is passed by the caller where the report
// has one, and logging is off. A command that fails after printing its report
// (a policy with violations exits 2, silently) returns the output with its exit
// code and no error; a command that fails without one returns an error.
type ReportRunner interface {
	Run(ctx context.Context, args ...string) (ReportOutput, error)
}

// ReportOutput is what a command printed and how it exited.
type ReportOutput struct {
	Stdout   []byte
	ExitCode int
	// Truncated says Stdout was cut at the runner's size bound.
	Truncated bool
}

// maxReportResult bounds the structured result of one report.
const maxReportResult = 8 << 20

// reportTextBytes bounds the readable text of one report.
const reportTextBytes = 20 * 1024

// reportTools are the names of the tools this file registers, in the order the
// documentation lists them.
func reportTools() []string {
	return []string{"policy", "arch", "deps", "licenses", "vulns", "sbom", "tour", "adr", "lessons", "events"}
}

// reportOut is the structured answer of a report tool: the command's own JSON,
// untouched.
type reportOut struct{ raw json.RawMessage }

func (r *reportOut) MarshalJSON() ([]byte, error) {
	if len(r.raw) == 0 {
		return []byte("{}"), nil
	}
	return r.raw, nil
}

func (s *Server) registerReports(allowed map[string]bool) {
	if s.deps.Reports == nil {
		return
	}
	descs := toolDescriptions()
	add := func(name string) *sdk.Tool {
		return &sdk.Tool{Name: name, Description: descs[name], OutputSchema: openObjectSchema()}
	}
	addTool(s, allowed, add("policy"), s.reportPolicy)
	addTool(s, allowed, add("arch"), s.reportArch)
	addTool(s, allowed, add("deps"), s.reportDeps)
	addTool(s, allowed, add("licenses"), s.reportLicenses)
	addTool(s, allowed, add("vulns"), s.reportVulns)
	addTool(s, allowed, add("sbom"), s.reportSBOM)
	addTool(s, allowed, add("tour"), s.reportTour)
	addTool(s, allowed, add("adr"), s.reportADR)
	addTool(s, allowed, add("lessons"), s.reportLessons)
	addTool(s, allowed, add("events"), s.reportEvents)
}

// ---------------------------------------------------------------- shared

// reportRun is one report to produce.
type reportRun struct {
	// json is the command with --json (or its own JSON format flag).
	json []string
	// text is the command's human form. Nil: build the text from the JSON.
	text []string
	// summary builds the one-line answer and, when text is nil, the body.
	summary func(raw json.RawMessage) (line, body string)
	// accept lists the exit codes that still carry a report.
	accept []int
}

// runReport runs a report and shapes it as a tool answer.
func (s *Server) runReport(ctx context.Context, req *sdk.CallToolRequest, rr reportRun) (*sdk.CallToolResult, *reportOut, error) {
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		res, _, rerr := s.noRun(ctx, req, err)
		return res, nil, rerr
	}

	// The readable form is asked for at the same time as the JSON: both read
	// the same index, and the two runs together cost one.
	type textRun struct {
		out ReportOutput
		err error
	}
	var textc chan textRun
	if rr.text != nil {
		textc = make(chan textRun, 1)
		go func() {
			o, e := s.deps.Reports.Run(ctx, rr.text...)
			textc <- textRun{o, e}
		}()
	}
	out, err := s.deps.Reports.Run(ctx, rr.json...)
	if err != nil {
		return failAs[reportOut](failWith("report_failed",
			"The command could not produce this report. The message says why; fix the arguments or the repository's configuration and ask again.",
			"%s", err.Error()))
	}
	if out.ExitCode != 0 && !containsInt(rr.accept, out.ExitCode) {
		return failAs[reportOut](failWith("report_failed",
			"The command exited without a report.", "exit status %d: %s", out.ExitCode, clipRunes(strings.TrimSpace(string(out.Stdout)), 300)))
	}
	raw := bytes.TrimSpace(out.Stdout)
	if out.Truncated || len(raw) > maxReportResult {
		return failAs[reportOut](failWith("too_large",
			"The report is larger than a tool answer may be. Run the same command on the command line, or narrow it with its arguments.",
			"the report is over %d MB", maxReportResult>>20))
	}
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return failAs[reportOut](failWith("report_failed",
			"The command did not print a JSON object.", "unexpected output: %s", clipRunes(string(raw), 200)))
	}

	line, body := rr.summary(raw)
	if textc != nil {
		if t := <-textc; t.err == nil && (t.out.ExitCode == 0 || containsInt(rr.accept, t.out.ExitCode)) && len(bytes.TrimSpace(t.out.Stdout)) > 0 {
			body = strings.TrimRight(string(t.out.Stdout), "\n")
		}
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: frameReport(line, runID, status, body)}},
	}, &reportOut{raw: append(json.RawMessage(nil), raw...)}, nil
}

// frameReport lays an answer out the way every tool does: GraphLume's own
// one-line statement first, then what came from the repository, labelled as
// such and cut to a size a model can take.
func frameReport(summary string, runID int64, status, body string) string {
	var b strings.Builder
	b.WriteString("=== " + sectionMetadata + " ===\n")
	b.WriteString(summary + "\n")
	fmt.Fprintf(&b, "index_revision: %d  status: %s\n", runID, status)
	if strings.TrimSpace(body) != "" {
		b.WriteString("\n=== " + sectionEvidence + " ===\n")
		b.WriteString(untrustedPreamble + "\n\n")
		if len(body) > reportTextBytes {
			cut := reportTextBytes
			for cut > 0 && !utf8Start(body[cut]) {
				cut--
			}
			b.WriteString(body[:cut])
			fmt.Fprintf(&b, "\n... %d more byte(s); the structured result has the whole report.\n", len(body)-cut)
		} else {
			b.WriteString(body)
			if !strings.HasSuffix(body, "\n") {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// jsonFields decodes the fields of a report named in want; the rest are left.
func jsonFields(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	return m
}

// jsonLen is the length of the array at key, or -1 when it is not one.
func jsonLen(m map[string]json.RawMessage, key string) int {
	var a []json.RawMessage
	if json.Unmarshal(m[key], &a) != nil {
		return -1
	}
	return len(a)
}

func jsonInt(m map[string]json.RawMessage, key string) int {
	var n int
	_ = json.Unmarshal(m[key], &n)
	return n
}

func jsonBool(m map[string]json.RawMessage, key string) bool {
	var v bool
	_ = json.Unmarshal(m[key], &v)
	return v
}

func jsonString(m map[string]json.RawMessage, key string) string {
	var v string
	_ = json.Unmarshal(m[key], &v)
	return v
}

// flag is "--name=value". The value stays one word to the command's parser
// whatever it holds, so a value that starts with a dash is not read as a flag.
func flag(name, value string) string { return "--" + name + "=" + value }

// requireNotNegative rejects a negative number the caller passed.
func requireNotNegative(name string, n int) error {
	if n < 0 {
		return fmt.Errorf("%s must not be negative", name)
	}
	return nil
}

func badArgs(err error) (*sdk.CallToolResult, *reportOut, error) {
	return failAs[reportOut](failWith("invalid_argument",
		"Correct the arguments and call again; retrying unchanged will fail identically.", "%v", err))
}

// ---------------------------------------------------------------- policy

type policyArgs struct {
	Base string `json:"base,omitempty" jsonschema:"Git revision to compare from. Default: origin/main, main or master."`
	Head string `json:"head,omitempty" jsonschema:"Git revision to compare to. Default HEAD."`
}

func (s *Server) reportPolicy(ctx context.Context, req *sdk.CallToolRequest, a policyArgs) (*sdk.CallToolResult, *reportOut, error) {
	cmd := []string{"policy", "check"}
	if a.Base != "" {
		cmd = append(cmd, flag("base", a.Base))
	}
	if a.Head != "" {
		cmd = append(cmd, flag("head", a.Head))
	}
	return s.runReport(ctx, req, reportRun{
		json:   append(append([]string{}, cmd...), "--format=json"),
		text:   append(append([]string{}, cmd...), "--format=text"),
		accept: []int{2},
		summary: func(raw json.RawMessage) (string, string) {
			m := jsonFields(raw)
			ev := jsonLen(m, "evaluated")
			if ev <= 0 {
				return "No policy rule was evaluated: the repository has no policy: block in .graphlume.yml, or the check evaluated none.", ""
			}
			verdict := "passed"
			if !jsonBool(m, "passed") {
				verdict = "FAILED"
			}
			return fmt.Sprintf("Policy %s: %d error(s), %d warning(s) from %d rule type(s) checked. Read-only: nothing was changed.",
				verdict, jsonInt(m, "errors"), jsonInt(m, "warnings"), ev), ""
		},
	})
}

// ---------------------------------------------------------------- arch

type archArgs struct {
	Crossings bool `json:"crossings,omitempty" jsonschema:"Also list every component-to-component dependency in the text, not only the violations. The structured result has them either way."`
	minConfidenceArg
}

func (s *Server) reportArch(ctx context.Context, req *sdk.CallToolRequest, a archArgs) (*sdk.CallToolResult, *reportOut, error) {
	cmd := []string{"arch"}
	if f := strings.TrimSpace(a.MinConfidence); f != "" {
		if _, err := parseMinConfidence(f); err != nil {
			return badArgs(err)
		}
		cmd = append(cmd, flag("min-confidence", strings.ToLower(f)))
	}
	text := append([]string{}, cmd...)
	if a.Crossings {
		text = append(text, "--crossings")
	}
	return s.runReport(ctx, req, reportRun{
		json: append(append([]string{}, cmd...), "--json", "--crossings"),
		text: text,
		summary: func(raw json.RawMessage) (string, string) {
			m := jsonFields(raw)
			if !jsonBool(m, "architecture_declared") {
				return "No architecture is declared in .graphlume.yml, so nothing was checked against one.", jsonString(m, "note")
			}
			return fmt.Sprintf("%d violation(s) of the declared architecture across %d component(s).",
				jsonLen(m, "violations"), jsonLen(m, "components")), ""
		},
	})
}

// ---------------------------------------------------------------- deps

type depsArgs struct {
	All bool `json:"all,omitempty" jsonschema:"List every declared dependency in the text, not only the unused ones. The structured result lists every one either way."`
}

func (s *Server) reportDeps(ctx context.Context, req *sdk.CallToolRequest, a depsArgs) (*sdk.CallToolResult, *reportOut, error) {
	text := []string{"deps"}
	if a.All {
		text = append(text, "--all")
	}
	return s.runReport(ctx, req, reportRun{
		json: []string{"deps", "--json"},
		text: text,
		summary: func(raw json.RawMessage) (string, string) {
			n := jsonLen(jsonFields(raw), "dependencies")
			if n <= 0 {
				return "No dependency manifest is indexed. Read: go.mod, package.json, pom.xml, build.gradle, requirements.txt, pyproject.toml, Cargo.toml.", ""
			}
			return fmt.Sprintf("%d declared dependency(ies), each with how many indexed files import it. No vulnerability data is part of this report; vulns checks advisories you supply.", n), ""
		},
	})
}

// ---------------------------------------------------------------- licenses

// licenses answers the third-party licence question by running
// `licenses --json`: the declared dependencies the index holds, each with the
// licence found for it on this machine and where. That form of the command
// writes no file and runs no toolchain (without --json it regenerates
// THIRD_PARTY.md, which a tool must not do); the runner admits it alone.
type licenseRow struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Ecosystem  string `json:"ecosystem"`
	Direct     bool   `json:"direct"`
	Manifest   string `json:"manifest,omitempty"`
	License    string `json:"license"`
	Source     string `json:"source"`
	Path       string `json:"path,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Category   string `json:"category"`
	Reason     string `json:"reason,omitempty"`
}

type licensesIn struct {
	Total   int `json:"total"`
	Summary struct {
		ByCategory map[string]int `json:"by_category"`
		BySource   map[string]int `json:"by_source"`
	} `json:"summary"`
	Dependencies []licenseRow `json:"dependencies"`
	BudgetSpent  bool         `json:"budget_spent"`
	Note         string       `json:"note"`
}

// licenseCategories and licenseSources are the summary keys in report order.
var (
	licenseCategories = []string{"permissive", "weak_copyleft", "strong_copyleft", "other", "unknown"}
	licenseSources    = []string{"declared-metadata", "license-file", "first-party", "unknown"}
)

// maxLicenseRowsText bounds the rows listed in the text; the structured
// result has every one.
const maxLicenseRowsText = 200

func (s *Server) reportLicenses(ctx context.Context, req *sdk.CallToolRequest, _ emptyArgs) (*sdk.CallToolResult, *reportOut, error) {
	return s.runReport(ctx, req, reportRun{
		json: []string{"licenses", "--json"},
		summary: func(raw json.RawMessage) (string, string) {
			var in licensesIn
			if err := json.Unmarshal(raw, &in); err != nil {
				return "The licence report could not be read.", ""
			}
			if in.Total == 0 {
				return "No dependency manifest is indexed, so there are no licences to report.", ""
			}
			var b strings.Builder
			b.WriteString("By category:")
			for _, c := range licenseCategories {
				fmt.Fprintf(&b, " %s %d", c, in.Summary.ByCategory[c])
			}
			b.WriteString("\nBy source:")
			for _, src := range licenseSources {
				fmt.Fprintf(&b, " %s %d", src, in.Summary.BySource[src])
			}
			b.WriteString("\n\n")
			for i, d := range in.Dependencies {
				if i == maxLicenseRowsText {
					fmt.Fprintf(&b, "... %d more in the structured result\n", len(in.Dependencies)-i)
					break
				}
				direct := "direct"
				if !d.Direct {
					direct = "indirect"
				}
				evidence := d.Source
				if d.Confidence != "" {
					evidence += ", " + d.Confidence
				}
				if d.Path != "" {
					evidence += ", " + d.Path
				}
				if d.Reason != "" && d.Source == "unknown" {
					evidence += ": " + d.Reason
				}
				fmt.Fprintf(&b, "- %s %s  [%s, %s]  %s (%s)\n", d.Name, d.Version, d.Ecosystem, direct, d.License, evidence)
			}
			line := fmt.Sprintf("%d declared third-party dependency(ies): %d licence(s) found on this machine, %d unknown.",
				in.Total, in.Total-in.Summary.BySource["unknown"], in.Summary.BySource["unknown"])
			if in.BudgetSpent {
				line += " The time bound ended the search early; the rest are unknown."
			}
			if in.Note != "" {
				line += " " + in.Note
			}
			return line, b.String()
		},
	})
}

// ---------------------------------------------------------------- vulns

type vulnsArgs struct {
	Advisories []string `json:"advisories,omitempty" jsonschema:"OSV-format advisory files, or directories of them, inside the repository, e.g. ['security/osv.json']. Nothing is downloaded and no advisories are built in: without any, the answer says nothing was checked and where to get them."`
	Ignore     []string `json:"ignore,omitempty" jsonschema:"Advisory ids or aliases to leave out. The report lists what was ignored."`
	All        bool     `json:"all,omitempty" jsonschema:"List the not-affected comparisons and every importing file in the text."`
	Limit      int      `json:"limit,omitempty" jsonschema:"Importing files and symbol references shown per finding in the text. Default 5."`
}

// maxAdvisoryArgs bounds the files one call may name.
const maxAdvisoryArgs = 32

func (s *Server) reportVulns(ctx context.Context, req *sdk.CallToolRequest, a vulnsArgs) (*sdk.CallToolResult, *reportOut, error) {
	if err := requireNotNegative("limit", a.Limit); err != nil {
		return badArgs(err)
	}
	if len(a.Advisories) > maxAdvisoryArgs || len(a.Ignore) > 256 {
		return badArgs(fmt.Errorf("at most %d advisory paths and 256 ignored ids may be given", maxAdvisoryArgs))
	}
	root := s.deps.Config.RepoRoot
	cmd := []string{"vulns"}
	for _, p := range a.Advisories {
		abs, err := advisoryPath(root, p)
		if err != nil {
			return badArgs(err)
		}
		cmd = append(cmd, flag("advisories", abs))
	}
	for _, id := range a.Ignore {
		if id = strings.TrimSpace(id); id != "" {
			cmd = append(cmd, flag("ignore", id))
		}
	}
	text := append([]string{}, cmd...)
	if a.All {
		text = append(text, "--all")
	}
	if a.Limit > 0 {
		text = append(text, fmt.Sprintf("--limit=%d", a.Limit))
	}
	return s.runReport(ctx, req, reportRun{
		json: append(append([]string{}, cmd...), "--json"),
		text: text,
		summary: func(raw json.RawMessage) (string, string) {
			m := jsonFields(raw)
			if _, has := m["checked"]; has && !jsonBool(m, "checked") {
				return fmt.Sprintf("No advisories were given, so nothing was checked. %d declared dependency(ies) would be.", jsonInt(m, "declared_dependencies")), ""
			}
			return "Declared dependencies checked against the advisories you supplied, with what the index shows about their use. Declared dependencies only; lockfile-only packages are not examined.", ""
		},
	})
}

// advisoryPath resolves an advisory path the caller named to a file or
// directory inside the repository. An absolute path, or a path that leaves the
// repository by a dot-dot or a symbolic link, is refused: the tool reads what
// the repository holds, not what the machine does.
func advisoryPath(root, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("an advisory path is empty")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("advisory path %q must be relative to the repository root", p)
	}
	abs, err := security.ValidatePath(root, filepath.ToSlash(p))
	if err != nil {
		return "", fmt.Errorf("advisory path %q is not inside the repository: %w", p, err)
	}
	if _, err := os.Stat(abs); err != nil { //nolint:gosec // G703: contained under the repository root by ValidatePath
		return "", fmt.Errorf("advisory path %q cannot be read: %w", p, err)
	}
	return abs, nil
}

// ---------------------------------------------------------------- sbom

type sbomArgs struct {
	Name string `json:"name,omitempty" jsonschema:"Name of the project the SBOM describes. Default: the root manifest's own module or package name."`
}

func (s *Server) reportSBOM(ctx context.Context, req *sdk.CallToolRequest, a sbomArgs) (*sdk.CallToolResult, *reportOut, error) {
	cmd := []string{"sbom"}
	if n := strings.TrimSpace(a.Name); n != "" {
		cmd = append(cmd, flag("name", n))
	}
	return s.runReport(ctx, req, reportRun{
		json: cmd,
		summary: func(raw json.RawMessage) (string, string) {
			m := jsonFields(raw)
			n := jsonLen(m, "components")
			if n <= 0 {
				return "The index holds no declared dependency, so the SBOM lists no components.", ""
			}
			var b strings.Builder
			var comps []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				PURL    string `json:"purl"`
			}
			_ = json.Unmarshal(m["components"], &comps)
			for i, c := range comps {
				if i == 60 {
					fmt.Fprintf(&b, "... and %d more\n", len(comps)-i)
					break
				}
				fmt.Fprintf(&b, "- %s %s  %s\n", c.Name, c.Version, c.PURL)
			}
			return fmt.Sprintf("CycloneDX %s SBOM of the %d declared dependency(ies); transitive packages that only a lockfile names are not listed. The structured result is the whole document.",
				jsonString(m, "specVersion"), n), b.String()
		},
	})
}

// ---------------------------------------------------------------- tour

type tourArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"Most entry points to show. Default 8."`
	Depth int `json:"depth,omitempty" jsonschema:"How many calls deep to follow from each entry point. Default 2."`
	Width int `json:"width,omitempty" jsonschema:"Most callees to follow from each symbol. Default 5."`
	minConfidenceArg
}

func (s *Server) reportTour(ctx context.Context, req *sdk.CallToolRequest, a tourArgs) (*sdk.CallToolResult, *reportOut, error) {
	for name, n := range map[string]int{"limit": a.Limit, "depth": a.Depth, "width": a.Width} {
		if err := requireNotNegative(name, n); err != nil {
			return badArgs(err)
		}
	}
	cmd := []string{"tour"}
	if a.Limit > 0 {
		cmd = append(cmd, fmt.Sprintf("--limit=%d", min(a.Limit, 50)))
	}
	if a.Depth > 0 {
		cmd = append(cmd, fmt.Sprintf("--depth=%d", min(a.Depth, 6)))
	}
	if a.Width > 0 {
		cmd = append(cmd, fmt.Sprintf("--width=%d", min(a.Width, 20)))
	}
	if f := strings.TrimSpace(a.MinConfidence); f != "" {
		if _, err := parseMinConfidence(f); err != nil {
			return badArgs(err)
		}
		cmd = append(cmd, flag("min-confidence", strings.ToLower(f)))
	}
	// The readable text is drawn from the JSON rather than by running the
	// command a second time: a tour walks every entry point of the service,
	// and two walks of the same index for one answer doubled what it cost.
	return s.runReport(ctx, req, reportRun{
		json: append(append([]string{}, cmd...), "--json"),
		summary: func(raw json.RawMessage) (string, string) {
			var t struct {
				Stops []tourStopJSON `json:"stops"`
			}
			_ = json.Unmarshal(raw, &t)
			body := strings.TrimRight(tourText(t.Stops), "\n")
			if len(t.Stops) == 0 {
				return "No entry points found: the index holds no route with a bound handler and no main function that calls anything it bound.", body
			}
			return fmt.Sprintf("A reading order from %d entry point(s): each one, then what it calls, breadth first, from relationships the index holds at the confidence asked for. Nothing is guessed.", len(t.Stops)), body
		},
	})
}

// tourStopJSON is one stop of `graphlume tour --json`.
type tourStopJSON struct {
	Entry  string   `json:"entry"`
	Where  string   `json:"where"`
	Routes []string `json:"routes"`
	Steps  []struct {
		Name       string `json:"name"`
		Depth      int    `json:"depth"`
		Confidence string `json:"confidence"`
		CalledAt   string `json:"called_at"`
	} `json:"steps"`
}

// tourText is what `graphlume tour` prints for these stops, line for line
// (internal/cli/tour.go writeTour; a test there holds the two together).
func tourText(stops []tourStopJSON) string {
	var b strings.Builder
	if len(stops) == 0 {
		b.WriteString("No entry points found: the index holds no route with a bound handler and no main function that calls anything it bound.\n")
		return b.String()
	}
	for i, s := range stops {
		fmt.Fprintf(&b, "%d. %s  (%s)\n", i+1, s.Entry, s.Where)
		for _, r := range s.Routes {
			fmt.Fprintf(&b, "   serves %s\n", r)
		}
		for _, st := range s.Steps {
			fmt.Fprintf(&b, "   %s-> %s  [%s, called at %s]\n", strings.Repeat("  ", max(st.Depth-1, 0)), st.Name, st.Confidence, st.CalledAt)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------- adr

type adrArgs struct {
	Query string `json:"query,omitempty" jsonschema:"An ADR number or part of its title or path. Default: list every record."`
}

func (s *Server) reportADR(ctx context.Context, req *sdk.CallToolRequest, a adrArgs) (*sdk.CallToolResult, *reportOut, error) {
	cmd := []string{"arch", "adr"}
	if q := strings.TrimSpace(a.Query); q != "" {
		// After "--", so a query that starts with a dash is still a query.
		cmd = append(cmd, "--", q)
	}
	run := reportRun{
		json: append([]string{"arch", "adr", "--json"}, cmd[2:]...),
		text: cmd,
		summary: func(raw json.RawMessage) (string, string) {
			n := jsonLen(jsonFields(raw), "adrs")
			if n <= 0 {
				return "No architecture decision records found in docs/adr, docs/decisions or the other conventional places.", ""
			}
			return fmt.Sprintf("%d architecture decision record(s). They are evidence of a decision, not of the current state of the code.", n), ""
		},
	}
	res, out, err := s.runReport(ctx, req, run)
	if res != nil && res.IsError && strings.TrimSpace(a.Query) != "" && strings.Contains(firstText(res), "no ADR matches") {
		// A query that matches nothing is an answer.
		runID, status, _ := s.currentRun(ctx)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: frameReport(
			fmt.Sprintf("No architecture decision record matches %q.", clipRunes(a.Query, 80)), runID, status, "")}},
		}, &reportOut{raw: json.RawMessage(`{"adrs":[]}`)}, nil
	}
	return res, out, err
}

// ---------------------------------------------------------------- lessons

type lessonsArgs struct {
	Top int `json:"top,omitempty" jsonschema:"How many names to list in each ranking. Default 10."`
}

func (s *Server) reportLessons(ctx context.Context, req *sdk.CallToolRequest, a lessonsArgs) (*sdk.CallToolResult, *reportOut, error) {
	if err := requireNotNegative("top", a.Top); err != nil {
		return badArgs(err)
	}
	cmd := []string{"lesson", "report"}
	if a.Top > 0 {
		cmd = append(cmd, fmt.Sprintf("--top=%d", min(a.Top, 100)))
	}
	return s.runReport(ctx, req, reportRun{
		json: append(append([]string{}, cmd...), "--json"),
		text: cmd,
		summary: func(raw json.RawMessage) (string, string) {
			return "What the recorded lessons say the context keeps missing, and what keeps helping. Lessons are a local memory of outcomes; this report only reads them.", ""
		},
	})
}

// ---------------------------------------------------------------- events

type eventsArgs struct {
	Topic  string `json:"topic,omitempty" jsonschema:"Keep only topics whose name or qualified name contains this text. Case-insensitive."`
	Broker string `json:"broker,omitempty" jsonschema:"Keep only one broker, e.g. kafka, rabbitmq, sqs, sns, pubsub. Case-insensitive."`
}

// eventsOut is the answer of the events tool. Topics is what `events --json`
// prints, an array, kept as the command printed it apart from the filter; a
// tool's structured result must be an object, so it is held under a key.
type eventsOut struct {
	IndexRevision int64             `json:"index_revision"`
	Total         int               `json:"total"`
	Matched       int               `json:"matched"`
	Topics        []json.RawMessage `json:"topics"`
	Note          string            `json:"note"`
}

const eventsNote = "Topics are recognised from broker client calls and annotations in the code (Kafka, RabbitMQ, SQS, SNS, Pub/Sub, JMS). " +
	"A topic named by a variable the scan cannot follow, or configured outside the code, is not listed; a topic with no producer here " +
	"is published by another service or by nothing, and the index cannot tell which."

// eventsTextRows bounds the topics the readable text lists.
const eventsTextRows = 200

func (s *Server) reportEvents(ctx context.Context, req *sdk.CallToolRequest, a eventsArgs) (*sdk.CallToolResult, *reportOut, error) {
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		res, _, rerr := s.noRun(ctx, req, err)
		return res, nil, rerr
	}
	out, err := s.deps.Reports.Run(ctx, "events", "--json")
	if err != nil {
		return failAs[reportOut](failWith("report_failed",
			"The command could not produce this report. The message says why.", "%s", err.Error()))
	}
	raw := bytes.TrimSpace(out.Stdout)
	if out.Truncated || len(raw) > maxReportResult {
		return failAs[reportOut](failWith("too_large",
			"The report is larger than a tool answer may be. Narrow it with topic or broker, or run `graphlume events --json`.",
			"the report is over %d MB", maxReportResult>>20))
	}
	var all []json.RawMessage
	if out.ExitCode != 0 || json.Unmarshal(raw, &all) != nil {
		return failAs[reportOut](failWith("report_failed",
			"The command did not print a JSON array.", "unexpected output: %s", clipRunes(string(raw), 200)))
	}

	topic := strings.ToLower(strings.TrimSpace(a.Topic))
	broker := strings.ToLower(strings.TrimSpace(a.Broker))
	eo := eventsOut{IndexRevision: runID, Total: len(all), Topics: []json.RawMessage{}, Note: eventsNote}
	var b strings.Builder
	var producers, consumers int
	for _, t := range all {
		var row struct {
			Broker    string            `json:"broker"`
			Topic     string            `json:"topic"`
			FQN       string            `json:"fqn"`
			Producers []json.RawMessage `json:"producers"`
			Consumers []json.RawMessage `json:"consumers"`
		}
		if json.Unmarshal(t, &row) != nil {
			continue
		}
		if broker != "" && strings.ToLower(row.Broker) != broker {
			continue
		}
		if topic != "" && !strings.Contains(strings.ToLower(row.Topic), topic) && !strings.Contains(strings.ToLower(row.FQN), topic) {
			continue
		}
		eo.Topics = append(eo.Topics, t)
		producers += len(row.Producers)
		consumers += len(row.Consumers)
		if len(eo.Topics) <= eventsTextRows {
			fmt.Fprintf(&b, "- [%s] %s  producers: %d  consumers: %d\n", row.Broker, row.Topic, len(row.Producers), len(row.Consumers))
		}
	}
	eo.Matched = len(eo.Topics)
	if eo.Matched > eventsTextRows {
		fmt.Fprintf(&b, "... and %d more topic(s); the structured result lists every one with its producers and consumers.\n", eo.Matched-eventsTextRows)
	}
	res, err := json.Marshal(eo)
	if err != nil {
		return failAs[reportOut](failWith("internal", "", "encode events: %v", err))
	}

	var summary string
	switch {
	case eo.Total == 0:
		summary = "No event bus producer or consumer was found in the index. " + eventsNote
	case eo.Matched == 0:
		summary = fmt.Sprintf("None of the %d event topic(s) matches the topic and broker given.", eo.Total)
	default:
		summary = fmt.Sprintf("%d event topic(s)", eo.Matched)
		if eo.Matched != eo.Total {
			summary += fmt.Sprintf(" of %d", eo.Total)
		}
		summary += fmt.Sprintf(", with %d producing and %d consuming site(s), each with its file, line and confidence in the structured result.", producers, consumers)
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: frameReport(summary, runID, status, b.String())}},
	}, &reportOut{raw: res}, nil
}

// firstText is the text of a result's first text block.
func firstText(res *sdk.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*sdk.TextContent); ok {
			return t.Text
		}
	}
	return ""
}
