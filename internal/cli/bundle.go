package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/store"
)

// IssueBundle represents a sanitized diagnostic bundle ready for bug reports.
type IssueBundle struct {
	Timestamp        string         `json:"timestamp"`
	GraphLumeVersion string         `json:"graphlume_version"`
	Commit           string         `json:"commit"`
	BuildTime        string         `json:"build_time"`
	GoVersion        string         `json:"go_version"`
	OS               string         `json:"os"`
	Arch             string         `json:"arch"`
	NumCPU           int            `json:"num_cpu"`
	Project          string         `json:"project"`
	Checks           []check        `json:"doctor_checks"`
	Status           map[string]any `json:"index_status,omitempty"`
	SubmitInfo       SubmitDetails  `json:"submit_info"`
}

// SubmitDetails guides the user on where and how to submit their issue.
type SubmitDetails struct {
	GitHubIssuesURL string `json:"github_issues_url"`
	SupportEmail    string `json:"support_email"`
	Instructions    string `json:"instructions"`
}

// newBundleCmd generates a diagnostic issue bundle.
func newBundleCmd(g *Global) *cobra.Command {
	var outPath string

	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Generate a sanitized diagnostic bundle for bug reports & GitHub issues",
		Long: `Collects environment diagnostics, index health checks, and parse statistics
into a sanitized JSON file. The bundle contains NO source code, secrets, or
tokens, and is ready to attach to a GitHub issue or support email.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGenerateBundle(cmd, g, outPath)
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "file path to write the bundle to (default: graphlume-issue-bundle-<timestamp>.json)")
	return cmd
}

func runGenerateBundle(cmd *cobra.Command, g *Global, outPath string) error {
	ctx := cmd.Context()
	cfg := g.Config()
	out := cmd.OutOrStdout()
	ui := NewUI(out)

	ui.Header("Generating Issue Diagnostic Bundle")

	if outPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		outPath = fmt.Sprintf("graphlume-issue-bundle-%s.json", timestamp)
	}

	// 1. Run doctor checks (with verifyIndex = true)
	checks := collectDoctorChecks(ctx, g, cmd, true)
	ui.Success("Environment & graph integrity diagnostics collected (%d checks)", len(checks))

	// 2. Collect index status summary
	stMap := collectStatusMap(ctx, g)
	if stMap != nil {
		ui.Success("Index state & parse breakdown gathered")
	} else {
		ui.Info("No index detected (clean state)")
	}

	bundle := IssueBundle{
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		GraphLumeVersion: Version,
		Commit:           Commit,
		BuildTime:        BuildTime,
		GoVersion:        runtime.Version(),
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		NumCPU:           runtime.NumCPU(),
		Project:          cfg.ProjectName,
		Checks:           checks,
		Status:           stMap,
		SubmitInfo: SubmitDetails{
			GitHubIssuesURL: "https://github.com/sutrakam/graphlume/issues/new",
			SupportEmail:    "hi@satyendra.dev",
			Instructions:    "Attach this file to your GitHub issue or email with a brief summary of the unexpected behavior.",
		},
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal issue bundle: %w", err)
	}

	if err := os.WriteFile(outPath, data, 0o600); err != nil {
		return fmt.Errorf("write issue bundle to %s: %w", outPath, err)
	}

	absPath, _ := filepath.Abs(outPath)
	ui.Success("Diagnostic bundle saved to: %s", absPath)
	_, _ = fmt.Fprintln(out)
	ui.Header("How to Submit This Issue")
	_, _ = fmt.Fprintf(out, "  • GitHub: %s\n", bundle.SubmitInfo.GitHubIssuesURL)
	_, _ = fmt.Fprintf(out, "  • Email:  %s\n", bundle.SubmitInfo.SupportEmail)
	_, _ = fmt.Fprintln(out, "  • Attach the bundle file above.")
	_, _ = fmt.Fprintln(out, "    (Note: The bundle contains sanitized metadata only; source code and credentials are excluded.)")

	return nil
}

// collectDoctorChecks gathers all checks run by doctor.
func collectDoctorChecks(ctx context.Context, g *Global, cmd *cobra.Command, verifyIndex bool) []check {
	cfg := g.Config()
	var checks []check

	checks = append(checks, check{
		Name: "go runtime", Status: "ok",
		Detail: fmt.Sprintf("%s %s/%s (%d CPUs)", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
	})

	// SQLite + FTS5
	mem, err := openMemDB(ctx)
	if err != nil {
		checks = append(checks, check{Name: "sqlite", Status: "fail", Detail: err.Error()})
	} else {
		checks = append(checks, check{Name: "sqlite", Status: "ok", Detail: mem.ver})
		if mem.ftsErr != nil {
			checks = append(checks, check{Name: "fts5", Status: "fail", Detail: mem.ftsErr.Error()})
		} else {
			checks = append(checks, check{Name: "fts5", Status: "ok", Detail: "ENABLE_FTS5 present"})
		}
	}

	// Config files
	checks = append(checks, check{
		Name:   "repo config",
		Status: "ok",
		Detail: cfg.RepoConfigFile,
	})

	dbPath := g.DBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		checks = append(checks, check{Name: "index", Status: "warn", Detail: "absent"})
	} else {
		if bad := indexHealthCheck(cmd, dbPath); bad != nil {
			checks = append(checks, *bad)
			return checks
		}
		checks = append(checks, check{Name: "index", Status: "ok", Detail: dbPath})
		checks = append(checks, deferredSchemaChecks(cmd, dbPath)...)
		checks = append(checks, parseChecks(cmd, dbPath)...)
		if verifyIndex {
			checks = append(checks, verifyGraph(cmd, dbPath)...)
		}
	}

	return checks
}

type memDBInfo struct {
	ver    string
	ftsErr error
}

func openMemDB(ctx context.Context) (memDBInfo, error) {
	mem, err := store.Open(ctx, store.Options{})
	if err != nil {
		return memDBInfo{}, err
	}
	defer func() { _ = mem.Close() }()

	var ver string
	_ = mem.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&ver)
	ftsErr := store.VerifyFTS5(ctx, mem.DB)
	return memDBInfo{
		ver:    ver,
		ftsErr: ftsErr,
	}, nil
}

// collectStatusMap extracts a generic key-value map from status if index exists.
func collectStatusMap(ctx context.Context, g *Global) map[string]any {
	dbPath := g.DBPath()
	if fi, err := os.Stat(dbPath); err != nil || fi.Size() == 0 {
		return nil
	}
	res := map[string]any{
		"index_path":  dbPath,
		"accuracy":    string(g.Config().Accuracy),
		"mcp_profile": string(g.Config().MCPProfile),
	}
	return res
}
