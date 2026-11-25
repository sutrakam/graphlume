package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sutrakam/graphlume/internal/graph"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
	"github.com/sutrakam/graphlume/internal/registry"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Serving a project that has no index.
//
// `graphlume serve` exited with "no index at ... run 'graphlume init' and
// 'graphlume index' first" when the project had never been indexed. In a
// terminal that is the right answer: the person who typed the command reads
// it. An MCP client that LAUNCHES the server (Claude Desktop through the .mcpb
// bundle, an editor, any agent) never shows it. The client sees a process that
// died during start-up and reports "server disconnected", and the one sentence
// that says what to do is in a log file the user does not know exists.
//
// WHAT WAS CHOSEN.
//
//	Launched by a client     Start anyway. Every tool is listed, and every
//	(stdio, stdin a pipe)    call is answered with what to run and in which
//	                         directory. When the index appears the same
//	                         server starts answering from it: no restart.
//
//	Typed in a terminal      Exit non-zero with the message, as before. A
//	(stdio, stdin a tty)     server waiting on a terminal for JSON-RPC nobody
//	                         will type helps no one.
//
//	--http                   Exit non-zero with the message, as before. It is
//	                         started by a person, who sees it, and a listener
//	                         that hands out a token for an index that does not
//	                         exist is a worse thing to leave running than
//	                         nothing.
//
// Nothing is indexed implicitly and nothing is written to the project: the
// user did not ask for either. The placeholder schema lives in a temporary
// directory that is removed on exit.

// stdinIsTerminal reports whether standard input is an interactive terminal,
// which is how a person running `graphlume serve` by hand is told apart from
// a client that launched it over a pipe.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// serveWithoutIndex is the decision in the table above.
func serveWithoutIndex(http, terminal bool) bool { return !http && !terminal }

// pendingServer is a tool server for a project with no index yet.
type pendingServer struct {
	g    *Global
	opts agentServerOptions
	ctx  context.Context
	root string // absolute project root, as the user must cd to it
	db   string // where the index will be

	mu       sync.Mutex
	live     *devmcp.Invoker // the real server, once an index exists
	liveStop func()
	// liveErr is why an index file that now exists could not be opened, kept
	// so the answer can say so; retried no more often than retryAfter.
	liveErr    error
	retryAfter time.Time
}

// openPendingAgentServer builds a tool server over an empty schema, so the
// client can list the tools it will have, and makes every call answer with
// what to run. The returned function releases everything, including the real
// server if one was opened later.
func openPendingAgentServer(ctx context.Context, g *Global, opts agentServerOptions) (*devmcp.Server, func(), error) {
	cfg := g.Config()
	if opts.Profile != "" {
		if !opts.Profile.Valid() {
			return nil, nil, fmt.Errorf("--profile %q: want minimal, standard, full, compact or frontdoor", opts.Profile)
		}
		cfg.MCPProfile = opts.Profile
	}

	// A schema with no run in it. In a temporary directory, never in the
	// project: creating .graphlume/ is `graphlume init`, which nobody ran.
	tmp, err := os.MkdirTemp("", "graphlume-noindex-")
	if err != nil {
		return nil, nil, fmt.Errorf("prepare a placeholder schema: %w", err)
	}
	db, err := openPlaceholder(ctx, filepath.Join(tmp, IndexFile))
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, nil, fmt.Errorf("prepare a placeholder schema: %w", err)
	}

	srv, err := devmcp.New(devmcp.Deps{
		Registry:  registry.New(db),
		Graph:     graph.New(db, graph.WithCSR()),
		Config:    cfg,
		Judgeable: bindsReferences,
	})
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmp)
		return nil, nil, err
	}

	root := cfg.RepoRoot
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	p := &pendingServer{g: g, opts: opts, ctx: ctx, root: root, db: g.DBPath()}
	srv.SDK().AddReceivingMiddleware(p.middleware)

	cleanup := func() {
		p.mu.Lock()
		if p.liveStop != nil {
			_ = p.live.Close()
			p.liveStop()
		}
		p.mu.Unlock()
		_ = db.Close()
		_ = os.RemoveAll(tmp)
	}
	return srv, cleanup, nil
}

// steps are the commands that build an index, in the directory to run them in.
func (p *pendingServer) steps() []string {
	return []string{
		"cd " + cdTarget(p.root),
		"graphlume init",
		"graphlume index",
		"graphlume resolve",
	}
}

// cdTarget renders the project root for a `cd` a person will paste. Double
// quotes, and only when the path needs them: they mean the same thing to a
// POSIX shell, PowerShell and cmd.exe, which single quotes do not.
func cdTarget(path string) string {
	if !strings.ContainsAny(path, " \t&()[]{}^=;!'+,`~") {
		return path
	}
	return `"` + path + `"`
}

const noIndexSummary = "GraphLume is running, but this project has no index yet, so no tool can answer."

// absent is the answer to a tool call while there is no index: what is wrong,
// what to run, where, and what happens next.
func (p *pendingServer) absent(openErr error) *sdk.CallToolResult {
	notes := []string{
		"Project: " + p.root,
		"Build the index by running these in a terminal: " + strings.Join(p.steps(), " && "),
		"Then ask again. This server uses the index as soon as it exists and does not need restarting.",
		"GraphLume does not start indexing on its own: nothing has been read from this project yet, " +
			"and an empty answer here says nothing about the code.",
	}
	var text strings.Builder
	text.WriteString(noIndexSummary + "\n\n")
	text.WriteString("Project: " + p.root + "\n")
	text.WriteString("Run these in a terminal, then ask again:\n\n")
	for _, s := range p.steps() {
		text.WriteString("  " + s + "\n")
	}
	text.WriteString("\nThis server uses the index as soon as it exists and does not need restarting. " +
		"GraphLume does not start indexing on its own, so nothing has been read from this project yet " +
		"and this answer says nothing about the code.\n")
	if openErr != nil {
		note := fmt.Sprintf("An index file now exists at %s but could not be opened: %v", p.db, openErr)
		notes = append(notes, note)
		text.WriteString("\n" + note + "\n")
	}

	// The same envelope every tool answer uses, so a client that reads
	// index_status sees "absent" here exactly as it would see "ready".
	structured, _ := json.Marshal(devmcp.Response{
		Summary:     noIndexSummary,
		IndexStatus: "absent",
		Notes:       notes,
	})
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: text.String()}},
		StructuredContent: json.RawMessage(structured),
	}
}

// liveInvoker returns the real server once an index exists, opening it on the
// first call that finds one.
func (p *pendingServer) liveInvoker() (*devmcp.Invoker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.live != nil {
		return p.live, nil
	}
	if _, err := os.Stat(p.db); err != nil {
		return nil, nil //nolint:nilerr // no index yet is the state this type exists for, not a failure
	}
	// An index that exists and will not open (a schema this build does not
	// read, a file being written) is retried, but not on every call.
	if p.liveErr != nil && time.Now().Before(p.retryAfter) {
		return nil, p.liveErr
	}
	srv, stop, err := openAgentServer(p.ctx, p.g, p.opts)
	if err != nil {
		p.liveErr, p.retryAfter = err, time.Now().Add(2*time.Second)
		return nil, err
	}
	inv, err := srv.NewInvoker(p.ctx, "stdio")
	if err != nil {
		stop()
		p.liveErr, p.retryAfter = err, time.Now().Add(2*time.Second)
		return nil, err
	}
	p.live, p.liveStop, p.liveErr = inv, stop, nil
	p.g.Log().Info("an index now exists; answering from it", "index", p.db)
	return inv, nil
}

// middleware answers tools/call. Everything else (initialize, tools/list)
// passes through to the placeholder server, which lists the real tool set.
func (p *pendingServer) middleware(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		if method == "initialize" {
			res, err := next(ctx, method, req)
			if init, ok := res.(*sdk.InitializeResult); ok && init != nil && err == nil {
				init.Instructions = "STATE AT START-UP: this project had no index (" + p.db + " did not exist), so " +
					"tools answer with how to build one until it exists: " + strings.Join(p.steps(), " && ") +
					". Once it exists the tools answer normally without a restart.\n\n" + init.Instructions
			}
			return res, err
		}
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		call, ok := req.(*sdk.CallToolRequest)
		if !ok || call.Params == nil {
			return next(ctx, method, req)
		}

		inv, openErr := p.liveInvoker()
		if inv != nil {
			res, err := inv.Call(ctx, call.Params.Name, call.Params.Arguments)
			if err != nil {
				if errors.Is(err, devmcp.ErrUnknownTool) {
					return next(ctx, method, req) // the SDK's own unknown-tool error
				}
				return nil, err
			}
			out := &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: res.Text}},
				IsError: res.IsError,
			}
			if len(res.Structured) > 0 {
				out.StructuredContent = res.Structured
			}
			return out, nil
		}

		// Let the placeholder server handle the call first, so it is counted
		// and written to the audit log like any other, then replace its
		// answer. A name that is not a tool stays the protocol error it is.
		res, err := next(ctx, method, req)
		if err != nil {
			return res, err
		}
		return p.absent(openErr), nil
	}
}
