package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/config"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
	"github.com/sutrakam/graphlume/internal/restapi"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------- shared mode
//
// `graphlume serve --http ADDR --tokens-file FILE` serves one index to several
// people and bots. Each token in the file has a name and a scope; see
// teamtokens.go for the file and internal/mcp/scope.go for why a scope is a
// server of its own.
//
// What changes against the single-token server, and what does not:
//
//   - The Host allowlist, the Origin check, the loopback rule and /healthz are
//     the same code, run first, for every request.
//   - A token is accepted ONLY as `Authorization: Bearer`. The /mcp/<token>
//     path form is off. It exists for a client on your own machine that takes
//     nothing but a URL; a shared server sits behind a proxy, and a token in a
//     path is a token in that proxy's access log, readable by whoever runs it.
//   - GRAPHLUME_MCP_TOKEN is not consulted and no token is generated. One
//     unscoped token beside the scoped ones would be the way around them.
//   - Each MCP session belongs to the token that opened it. A request carrying
//     another token's session id is refused, so a narrow token cannot continue
//     a wide token's session.

// teamAccess is what a shared server needs per request: the tokens it accepts
// and, for each, the tool server and REST handler built for its scope.
type teamAccess struct {
	tokens   *tokenSet
	backends map[string]*teamBackend // by token name
}

// teamBackend is the tool surface one token reaches.
type teamBackend struct {
	mcp *sdk.Server
	api http.Handler
}

// teamPrincipal is an authenticated request's identity.
type teamPrincipal struct {
	name    string
	backend *teamBackend
}

type teamPrincipalKey struct{}

func teamPrincipalFrom(ctx context.Context) *teamPrincipal {
	p, _ := ctx.Value(teamPrincipalKey{}).(*teamPrincipal)
	return p
}

// authenticate returns the request with its identity attached, or false.
//
// Only the Authorization header is read. Not the path, not the query string,
// not a cookie, not a form field: each of those is a place a token gets logged
// or replayed from.
func (t *teamAccess) authenticate(r *http.Request) (*http.Request, *teamPrincipal) {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return r, nil
	}
	entry := t.tokens.lookup(strings.TrimSpace(bearer))
	if entry == nil {
		return r, nil
	}
	backend := t.backends[entry.Name]
	if backend == nil {
		// Unreachable when buildTeamAccess made this value; refusing is the
		// only safe answer if it ever is not.
		return r, nil
	}
	p := &teamPrincipal{name: entry.Name, backend: backend}
	return r.WithContext(context.WithValue(r.Context(), teamPrincipalKey{}, p)), p
}

// serverFor picks the tool server for a new MCP session: the one built for the
// requesting token's scope. No identity means no server.
func (t *teamAccess) serverFor(r *http.Request) *sdk.Server {
	if p := teamPrincipalFrom(r.Context()); p != nil {
		return p.backend.mcp
	}
	return nil
}

// bindSessions hands the token's NAME to the MCP SDK as the request's user.
//
// The SDK does two things with it. It records the user on the session a
// request opens and refuses any later request for that session from a
// different user, which is what stops one token continuing another's session.
// And it passes the user to the tool-call middleware, where the audit log
// reads it.
//
// The verifier does not verify: authenticate already did, with the one answer
// this server gives for every failure. It reads the identity authenticate
// attached and ignores the token string, so there is exactly one place a
// token is checked.
func (t *teamAccess) bindSessions(next http.Handler) http.Handler {
	verifier := func(ctx context.Context, _ string, _ *http.Request) (*auth.TokenInfo, error) {
		p := teamPrincipalFrom(ctx)
		if p == nil {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: p.name}, nil
	}
	// Expiry is enforced by authenticate on every request; the SDK's own
	// expiry check would answer an expired token differently from an unknown
	// one, so it is given nothing to check.
	return auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(next)
}

// buildTeamAccess builds one tool server per distinct scope and one REST
// handler per token. The returned function closes what it opened.
func buildTeamAccess(ctx context.Context, base *devmcp.Server, tokens *tokenSet, info restapi.Info) (*teamAccess, func(), error) {
	var closers []func()
	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	t := &teamAccess{tokens: tokens, backends: make(map[string]*teamBackend, len(tokens.entries))}
	scoped := map[string]*devmcp.Server{}
	for i := range tokens.entries {
		e := &tokens.entries[i]
		srv, ok := scoped[e.scopeKey()]
		if !ok {
			var err error
			if srv, err = base.ScopedAs(e.Profile, e.Tools, e.permissions()); err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("token %q: %w", e.Name, err)
			}
			scoped[e.scopeKey()] = srv
		}
		// One in-process session per token, so the audit log can name the
		// token behind a REST call without trusting anything in the request.
		inv, err := srv.NewInvokerAs(ctx, "rest", e.Name)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("token %q: %w", e.Name, err)
		}
		closers = append(closers, func() { _ = inv.Close() })

		tokenInfo := info
		tokenInfo.Profile = string(e.Profile)
		t.backends[e.Name] = &teamBackend{mcp: srv.SDK(), api: restapi.Handler(inv, tokenInfo)}
	}
	return t, cleanup, nil
}

// serveTeam is `serve --http` with a tokens file.
func serveTeam(ctx context.Context, cmd *cobra.Command, g *Global, base *devmcp.Server, opts httpServeOptions, tokensFile string) error {
	errOut := cmd.ErrOrStderr()

	// The address is checked before the file is read, so the first thing a
	// wrong command line reports is the listen address.
	if err := checkListenAddr(opts.Addr, opts.AllowRemote); err != nil {
		return fmt.Errorf("%w\nA shared server sends bearer tokens in clear text unless TLS is in front of it. "+
			"Keep it on 127.0.0.1 and put a reverse proxy that terminates TLS in front; "+
			"see 'Sharing one index with a team' in docs/guides/protocols.md", err)
	}

	tokens, warnings, err := loadTokensFile(tokensFile)
	if err != nil {
		return err
	}
	if w := teamListenWarning(opts.Addr); w != "" {
		warnings = append(warnings, w)
	}
	if strings.TrimSpace(os.Getenv(mcpTokenEnv)) != "" {
		warnings = append(warnings, mcpTokenEnv+" is set and ignored: with --tokens-file only the tokens in the file are accepted")
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintln(errOut, "warning:", w)
	}

	cfg := g.Config()
	team, closeTeam, err := buildTeamAccess(ctx, base, tokens, restapi.Info{Project: cfg.ProjectName, Version: Version})
	if err != nil {
		return err
	}
	defer closeTeam()

	g.Log().Info("serving a shared index over HTTP", "project", cfg.ProjectName, "tokens", len(tokens.entries))
	opts.Team = team
	return serveHTTP(ctx, base.SDK(), nil, opts, g.Log(), errOut, describeTokens(tokensFile, tokens))
}

// teamListenWarning is what a shared server says when it listens beyond this
// machine. --allow-remote permits that, as it does for the single-token
// server; a tokens file means the tokens belong to other people, so they are
// told what the flag accepted on their behalf.
func teamListenWarning(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || isLoopbackHost(host) {
		return ""
	}
	return "listening beyond this machine with no TLS in this process: every token crosses the network " +
		"in clear text unless a proxy that terminates TLS is the only thing that can reach " + addr
}

// describeTokens is the start-up summary: names and scopes, never a token or
// a hash.
func describeTokens(path string, tokens *tokenSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d named token(s) from %s, read once at start; restart to apply a change to the file", len(tokens.entries), path)
	for i := range tokens.entries {
		e := &tokens.entries[i]
		fmt.Fprintf(&b, "\n  %s: %s", e.Name, e.describeScope())
	}
	return b.String()
}

// ---------------------------------------------------------------- create-token

func newServeCreateTokenCmd(g *Global) *cobra.Command {
	var (
		name       string
		profile    string
		tools      []string
		expires    string
		role       string
		tokensFile string
	)
	cmd := &cobra.Command{
		Use:   "create-token",
		Short: "Create a named token for a shared server and print it once",
		Long: `Creates one token for 'graphlume serve --http ADDR --tokens-file FILE'.

The token is printed once, here, and stored nowhere: the tokens file gets its
SHA-256 hash, which lets nobody in. Give the token to the person or the bot it
is for, over a channel you trust.

    graphlume serve create-token --name ci --profile minimal --tokens-file team-tokens.jsonl
    graphlume serve create-token --name reviewer --profile standard --expires 2027-01-31 --tokens-file team-tokens.jsonl
    graphlume serve create-token --name bot --profile standard --tool callers --tool impact

With --tokens-file the entry is appended to that file (created readable only by
you). Without it the entry is printed for you to add yourself.

A scope is a profile (minimal, standard or full), optionally narrowed with
--tool to the named tools. A tool outside a token's scope does not exist for
that token: it is not listed and cannot be called.

A role says what the token may do beyond calling its tools:
  viewer  read only; editing tools preview and never write (the default)
  editor  editing tools write, when the server runs with --allow-edits
  admin   editor, and the projects action lists every project on the server's machine
A role only narrows: no token writes on a server started without --allow-edits.

    graphlume serve create-token --name dev-bot --profile full --role editor --tokens-file team-tokens.jsonl

--expires takes a date (the token works through that day, UTC) or an RFC 3339
time. A running server reads the file once, at start: restart it to load a new
token or to revoke one by deleting its line.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := newTeamToken()
			if err != nil {
				return err
			}
			raw := tokenLine{
				Name:    name,
				SHA256:  hashToken(token),
				Profile: profile,
				Tools:   tools,
				Expires: expires,
				Role:    role,
			}
			if len(raw.Tools) == 0 {
				raw.Tools = nil
			}
			entry, err := checkTokenLine(raw)
			if err != nil {
				return err
			}
			if !entry.Expires.IsZero() && !time.Now().Before(entry.Expires) {
				return errors.New("--expires is in the past; the token would never work")
			}
			// Written the way the server reads it back: tools sorted, no
			// duplicates.
			raw.Tools = entry.Tools

			if tokensFile != "" {
				if err := appendTokenLine(tokensFile, raw); err != nil {
					return err
				}
				// The same warning the server gives, at the moment the
				// operator is looking at the file.
				if fi, err := os.Stat(tokensFile); err == nil {
					if w := tokensFileModeWarning(tokensFile, fi.Mode()); w != "" {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
					}
				}
			}
			return printCreatedToken(cmd.OutOrStdout(), g.JSON, token, raw, &entry, tokensFile)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "who or what the token is for; recorded in the audit log (lower-case letters, digits, '.', '_', '-')")
	cmd.Flags().StringVar(&profile, "profile", string(config.MCPMinimal), "tool profile the token may use: minimal, standard or full")
	cmd.Flags().StringSliceVar(&tools, "tool", nil, "narrow the profile to this tool (repeatable); default is the whole profile")
	cmd.Flags().StringVar(&role, "role", roleViewer, "what the token may do: viewer (read only), editor (editing tools may write when the server allows edits) or admin (editor, plus listing every project on the server's machine)")
	cmd.Flags().StringVar(&expires, "expires", "", "last day the token works, as 2027-01-31 (UTC), or an RFC 3339 time; default is no expiry")
	cmd.Flags().StringVar(&tokensFile, "tokens-file", "", "append the hashed entry to this file instead of printing it")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// printCreatedToken is the one place a token is ever written out.
func printCreatedToken(out io.Writer, asJSON bool, token string, raw tokenLine, entry *tokenEntry, tokensFile string) error {
	if asJSON {
		return emitJSON(out, map[string]any{
			"name":        raw.Name,
			"token":       token,
			"entry":       raw,
			"tokens_file": tokensFile,
			"appended":    tokensFile != "",
		})
	}
	line, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode token entry: %w", err)
	}
	_, _ = fmt.Fprintf(out, "Token %q (%s):\n\n    %s\n\n", raw.Name, entry.describeScope(), token)
	_, _ = fmt.Fprintln(out, "This is the only time it is shown. GraphLume keeps its SHA-256 hash, not the token.")
	if tokensFile != "" {
		_, _ = fmt.Fprintf(out, "Appended the entry to %s. A running server reads the file at start: restart it to load this token.\n", tokensFile)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Add this line to the file you pass to 'graphlume serve --tokens-file':\n\n    %s\n", line)
	return nil
}
