package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/pipeline"
)

// goTypesJSON is the machine-readable form of the built-in Go type check.
func goTypesJSON(mode config.AccuracyMode, o pipeline.GoTypesOutcome) map[string]any {
	if mode == config.AccuracyFast {
		return map[string]any{"ran": false, "reason": "accuracy mode is fast"}
	}
	if !o.Ran {
		m := map[string]any{"ran": false, "reason": o.Reason}
		if o.Err != nil {
			m["error"] = o.Err.Error()
		}
		return m
	}
	c := o.Check
	m := map[string]any{
		"ran": true, "platform": c.GOOS + "/" + c.GOARCH,
		"goroot": c.Goroot, "modules": c.Modules, "packages": c.Packages,
		"packages_complete": c.Complete, "packages_with_errors": c.WithErrors,
		"packages_with_stubbed_imports": c.WithStubs, "packages_skipped_parse": c.SkippedParse,
		"files_checked": c.FilesChecked, "files_excluded_by_build_constraints": c.FilesRejected,
		"missing_imports": c.MissingCount, "first_missing_imports": firstStrings(c.MissingImports, 8),
		"calls_proven": c.Calls, "constructions_proven": c.Constructs,
		"edges_raised_to_exact": o.Import.Edges.Upgraded, "edges_new": o.Import.Edges.Inserted,
		"edges_retracted":           o.Import.Retracted.Deleted + o.Import.Retracted.Restored,
		"heuristic_edges_withdrawn": o.Import.Contradicted,
		"implementations":           c.Implementations, "implementation_methods": c.ImplementationMethods,
		"selections_withheld": c.WithheldTaint,
		"duration_ms":         c.Elapsed.Milliseconds(),
	}
	if c.Aborted != "" {
		m["aborted"] = c.Aborted
	}
	if o.Err != nil {
		m["error"] = o.Err.Error()
	}
	return m
}

func firstStrings(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// renderGoTypes prints the one summary block of the Go type check.
func renderGoTypes(out io.Writer, mode config.AccuracyMode, o pipeline.GoTypesOutcome) {
	if mode == config.AccuracyFast {
		return
	}
	if !o.Ran {
		if o.Err != nil {
			fmt.Fprintf(out, "  Go types:    not run: %v\n", o.Err)
		} else if o.Reason != "" && o.Reason != "no Go files indexed" {
			fmt.Fprintf(out, "  Go types:    not run: %s\n", o.Reason)
		}
		return
	}
	c := o.Check
	fmt.Fprintf(out, "  Go types:    %d file(s) in %d package(s) checked by go/types (built in), %s/%s\n",
		c.FilesChecked, c.Packages, c.GOOS, c.GOARCH)
	if c.Goroot == "" {
		fmt.Fprintf(out, "    no GOROOT found: standard-library imports were stubbed. Set GOROOT, or put `go` on PATH, and resolve again\n")
	}
	fmt.Fprintf(out, "    packages:  %d checked completely, %d with errors, %d with stubbed imports",
		c.Complete, c.WithErrors, c.WithStubs)
	if c.SkippedParse > 0 {
		fmt.Fprintf(out, ", %d skipped (a file did not parse)", c.SkippedParse)
	}
	fmt.Fprintln(out)
	if c.MissingCount > 0 {
		more := ""
		if c.MissingCount > 5 {
			more = fmt.Sprintf(" (+%d more)", c.MissingCount-5)
		}
		fmt.Fprintf(out, "    not found: %d import(s), e.g. %s%s\n", c.MissingCount, c.FirstMissing(5), more)
	}
	if o.Err != nil {
		fmt.Fprintf(out, "    NOT IMPORTED: %v\n", o.Err)
		return
	}
	fmt.Fprintf(out, "    proven:    %d call(s) and %d construction(s); %d edge(s) raised from heuristic to exact, %d new\n",
		c.Calls, c.Constructs, o.Import.Edges.Upgraded, o.Import.Edges.Inserted)
	if n := o.Import.Contradicted; n > 0 {
		fmt.Fprintf(out, "    withdrawn: %d heuristic call edge(s) the compiler proves false (it resolved the same call to something else)\n", n)
	}
	if r := o.Import.Retracted; r.Deleted+r.Restored > 0 {
		fmt.Fprintf(out, "    retracted: %d edge(s) an earlier pass proved and this one does not (%d deleted, %d restored to heuristic)\n",
			r.Deleted+r.Restored, r.Deleted, r.Restored)
	}
	fmt.Fprintf(out, "    implements: %d type-to-interface pair(s), %d method(s)\n",
		c.Implementations, c.ImplementationMethods)
	if c.FilesRejected > 0 {
		fmt.Fprintf(out, "    not checked: %d file(s) excluded by the build constraints of %s/%s keep their heuristic edges; nothing is claimed for other platforms\n",
			c.FilesRejected, c.GOOS, c.GOARCH)
	}
	if c.WithheldTaint > 0 {
		fmt.Fprintf(out, "    withheld:  %d selection(s) through an embedded type of an unknown package\n", c.WithheldTaint)
	}
	if o.Import.SymbolsUnmatched > 0 {
		fmt.Fprintf(out, "    unmatched: %d declaration(s) the index holds no symbol for\n", o.Import.SymbolsUnmatched)
	}
	if c.Aborted != "" {
		fmt.Fprintf(out, "    stopped:   %s\n", strings.TrimSpace(c.Aborted))
	}
	fmt.Fprintf(out, "    time:      %dms\n", c.Elapsed.Milliseconds())
}
