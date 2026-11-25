package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
	"github.com/sutrakam/graphlume/internal/restapi"
)

// These tests serve the estate tool over real indexes, through the wiring
// `graphlume serve` uses: two tiny Go modules indexed and resolved by the real
// commands (lib, and app which imports it), an HTTP pair seeded into two more
// indexes (front calls a route api declares), and a member whose index does
// not exist.

// estateServeFixture returns a project root to serve and an estate file
// naming lib, app, front, api and gone.
func estateServeFixture(t *testing.T) (project, estate string) {
	t.Helper()
	front, api := webIndex(t), apiIndex(t)
	ws := isolate(t)
	indexGoModule(t, filepath.Join(ws, "lib"), goLibFiles)
	indexGoModule(t, filepath.Join(ws, "app"), goAppFiles)
	writeEstate(t, ws, "  - name: lib\n    index: lib\n  - name: app\n    index: app\n"+
		"  - name: front\n    index: "+front+"\n  - name: api\n    index: "+api+"\n"+
		"  - name: gone\n    index: gone\n")
	return filepath.Join(ws, "app"), filepath.Join(ws, "estate.yml")
}

// configuredGlobal is a Global set up for project, reading config as a
// command would.
func configuredGlobal(t *testing.T, project string) *Global {
	t.Helper()
	g := &Global{Project: project, Quiet: true}
	cmd := NewRoot()
	cmd.SetArgs([]string{"--project", project, "--quiet", "version"})
	cmd.SetOut(io.Discard)
	if err := g.setup(cmd); err != nil {
		t.Fatalf("configure: %v", err)
	}
	return g
}

// estateInvoker opens the agent server as serve does and returns an in-process
// client for it.
func estateInvoker(t *testing.T, g *Global, opts agentServerOptions) (*devmcp.Server, *devmcp.Invoker) {
	t.Helper()
	ctx := context.Background()
	srv, closeServer, err := openAgentServer(ctx, g, opts)
	if err != nil {
		t.Fatalf("open agent server: %v", err)
	}
	t.Cleanup(closeServer)
	inv, err := srv.NewInvoker(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inv.Close() })
	return srv, inv
}

type estateAnswer struct {
	Summary string `json:"summary"`
	Items   []struct {
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		FQN        string `json:"fqn"`
		Path       string `json:"path"`
		Line       int    `json:"line"`
		Confidence string `json:"confidence"`
		Source     string `json:"source"`
		Why        string `json:"why"`
	} `json:"items"`
	Notes     []string `json:"notes"`
	Truncated bool     `json:"truncated"`
	Page      *struct {
		Total      int `json:"total"`
		NextOffset int `json:"next_offset"`
	} `json:"page"`
}

func callEstate(t *testing.T, inv *devmcp.Invoker, args string) (estateAnswer, string) {
	t.Helper()
	res, err := inv.Call(context.Background(), "estate", json.RawMessage(args))
	if err != nil {
		t.Fatalf("estate %s: %v", args, err)
	}
	if res.IsError {
		t.Fatalf("estate %s failed:\n%s", args, res.Text)
	}
	var a estateAnswer
	if err := json.Unmarshal(res.Structured, &a); err != nil {
		t.Fatalf("decode: %v\n%s", err, res.Structured)
	}
	return a, res.Text
}

func toolListed(srv *devmcp.Server, name string) bool {
	for _, n := range srv.Tools() {
		if n == name {
			return true
		}
	}
	return false
}

func TestEstateToolAnswersOverRealIndexes(t *testing.T) {
	project, estate := estateServeFixture(t)
	g := configuredGlobal(t, project)
	srv, inv := estateInvoker(t, g, agentServerOptions{Profile: config.MCPFull, Estate: estate})
	if !toolListed(srv, "estate") {
		t.Fatalf("serve --estate on the full profile does not list estate: %v", srv.Tools())
	}

	links, text := callEstate(t, inv, `{"action":"links","limit":200}`)
	seen := map[string]string{}
	for _, it := range links.Items {
		seen[it.Kind+" "+it.Name+" "+it.FQN] = it.Confidence
		if it.Confidence == "" || it.Confidence == "exact" {
			t.Errorf("%s %s crosses a repository boundary at %q", it.Kind, it.Name, it.Confidence)
		}
	}
	for _, want := range []string{
		"http_link front:useOrders front -> api GET /api/orders",
		"go_function_link app:run app -> lib:Do",
		"go_method_link app:viaAlias app -> lib:Client.Send",
	} {
		if _, ok := seen[want]; !ok {
			t.Errorf("links lack %q; got %v", want, seen)
		}
	}
	for _, want := range []string{
		"INCOMPLETE: 1 member(s) could not be read (gone)",
		"no index at",
		"LOWER BOUND",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("links answer omits %q:\n%s", want, text)
		}
	}

	callers, text := callEstate(t, inv, `{"action":"callers","symbol":"lib:Do"}`)
	var rows []string
	for _, it := range callers.Items {
		rows = append(rows, it.Name+" "+it.Path+" "+it.Confidence+" "+it.Source)
	}
	want := []string{
		"lib:Wrap wrap.go exact compiler",
		"app:run main.go high go-module",
		"app:viaAlias other.go high go-module",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("callers =\n%s\nwant\n%s\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"), text)
	}

	imp, text := callEstate(t, inv, `{"action":"impact","symbol":"lib:Do","depth":3}`)
	got := map[string]string{}
	for _, it := range imp.Items {
		got[it.Name] = it.Confidence + " " + it.Why
	}
	for name, prefix := range map[string]string{
		"app:run":     "high depth 1 via go-module",
		"app:handler": "high depth 2 via CALLS",
		"app:main":    "high depth 3 via CALLS",
	} {
		if !strings.HasPrefix(got[name], prefix) {
			t.Errorf("%s = %q, want %q...", name, got[name], prefix)
		}
	}
	if !strings.Contains(text, "members reached: lib, app") {
		t.Errorf("impact answer:\n%s", text)
	}

	// The same questions, asked again, get the same rows and notes.
	for _, args := range []string{`{"action":"links","limit":200}`, `{"action":"callers","symbol":"lib:Do"}`,
		`{"action":"impact","symbol":"lib:Do","depth":3}`} {
		a, _ := callEstate(t, inv, args)
		b, _ := callEstate(t, inv, args)
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Errorf("%s answered differently on a second call:\n%s\n%s", args, ja, jb)
		}
	}

	// An unreadable member asked about directly is an answer, not a failure.
	res, err := inv.Call(context.Background(), "estate", json.RawMessage(`{"action":"callers","symbol":"gone:Do"}`))
	if err != nil || res.IsError || !strings.Contains(res.Text, `member "gone" cannot be read`) {
		t.Errorf("gone:Do = %v %v\n%s", err, res.IsError, res.Text)
	}
}

func TestEstateToolIsAbsentWithoutAnEstateOrOffTheFullProfile(t *testing.T) {
	project, estate := estateServeFixture(t)
	g := configuredGlobal(t, project)
	if srv, _ := estateInvoker(t, g, agentServerOptions{Profile: config.MCPFull}); toolListed(srv, "estate") {
		t.Error("estate is listed with no estate configured")
	}
	for _, p := range []config.MCPProfile{config.MCPMinimal, config.MCPStandard} {
		if srv, _ := estateInvoker(t, g, agentServerOptions{Profile: p, Estate: estate}); toolListed(srv, "estate") {
			t.Errorf("estate is listed on the %s profile", p)
		}
	}
}

// writeRepoConfig writes .graphlume.yml in root.
func writeRepoConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.RepoConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEstateFromRepoConfigMustResolveInsideTheRepository(t *testing.T) {
	project, estate := estateServeFixture(t)
	raw, err := os.ReadFile(estate)
	if err != nil {
		t.Fatal(err)
	}
	// Member paths in the copy are absolute, so it means the same thing from
	// inside the repository.
	ws := filepath.Dir(estate)
	inside := strings.ReplaceAll(string(raw), "index: lib\n", "index: "+filepath.Join(ws, "lib")+"\n")
	inside = strings.ReplaceAll(inside, "index: app\n", "index: "+filepath.Join(ws, "app")+"\n")
	if err := os.WriteFile(filepath.Join(project, "estate.yml"), []byte(inside), 0o644); err != nil {
		t.Fatal(err)
	}

	writeRepoConfig(t, project, "estate: estate.yml\n")
	if srv, _ := estateInvoker(t, configuredGlobal(t, project), agentServerOptions{Profile: config.MCPFull}); !toolListed(srv, "estate") {
		t.Error("an estate file inside the repository was not served")
	}

	// Lexically out of the repository: refused when the config is read.
	writeRepoConfig(t, project, "estate: ../estate.yml\n")
	g := &Global{Project: project, Quiet: true}
	cmd := NewRoot()
	cmd.SetArgs([]string{"--project", project, "--quiet", "version"})
	cmd.SetOut(io.Discard)
	if err := g.setup(cmd); err == nil || !strings.Contains(err.Error(), "escapes the repository root") {
		t.Errorf("../estate.yml: %v", err)
	}
	writeRepoConfig(t, project, "estate: "+estate+"\n")
	if err := g.setup(cmd); err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("absolute estate path: %v", err)
	}

	// Inside by name, outside through a symlink: refused when served.
	if err := os.Symlink(estate, filepath.Join(project, "link.yml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeRepoConfig(t, project, "estate: link.yml\n")
	_, _, err = openAgentServer(context.Background(), configuredGlobal(t, project), agentServerOptions{Profile: config.MCPFull})
	if err == nil || !strings.Contains(err.Error(), "resolves outside the repository") {
		t.Errorf("a symlink out of the repository was not refused: %v", err)
	}

	// A trusted source wins, and the repository's path is never followed.
	srv, _ := estateInvoker(t, configuredGlobal(t, project), agentServerOptions{Profile: config.MCPFull, Estate: estate})
	if !toolListed(srv, "estate") {
		t.Error("serve --estate did not override the repository's estate:")
	}
	userHome := t.TempDir()
	t.Setenv("GRAPHLUME_CONFIG_HOME", userHome)
	if err := os.WriteFile(filepath.Join(userHome, "config.yml"), []byte("estate: "+estate+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if srv, _ := estateInvoker(t, configuredGlobal(t, project), agentServerOptions{Profile: config.MCPFull}); !toolListed(srv, "estate") {
		t.Error("estate: in user config was not served")
	}
}

// A shared server's token may be scoped to the estate tool alone.
func TestEstateCanBeATokensOnlyTool(t *testing.T) {
	project, estate := estateServeFixture(t)
	g := configuredGlobal(t, project)

	tok := mustToken(t)
	tokens, _, err := loadTokensFile(writeTokensFile(t, 0o600, entryLine(t, "estate-only", tok, "full", "", "estate")))
	if err != nil {
		t.Fatalf("a tokens file naming estate was refused: %v", err)
	}

	ctx := context.Background()
	base, closeServer, err := openAgentServer(ctx, g, agentServerOptions{Estate: estate})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeServer)
	team, closeTeam, err := buildTeamAccess(ctx, base, tokens, restapi.Info{Project: "fixture", Version: "test"})
	if err != nil {
		t.Fatalf("build team access: %v", err)
	}
	t.Cleanup(closeTeam)
	ts := httptest.NewServer(newHTTPHandler(base.SDK(), nil, httpServeOptions{Team: team},
		slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(ts.Close)

	cs, err := connect(t, ts.URL+mcpPath, map[string]string{"Authorization": "Bearer " + tok})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	names := listedTools(t, cs)
	sort.Strings(names)
	if strings.Join(names, ",") != "estate" {
		t.Errorf("the estate-only token sees %v", names)
	}
}
