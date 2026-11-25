package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClientClosed is returned when operations are attempted on a closed client.
var ErrClientClosed = errors.New("lsp: client is closed")

// Client is a JSON-RPC 2.0 client managing a subprocess language server.
type Client struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	serverName string
	logger     *slog.Logger

	mu      sync.Mutex
	pending map[int64]chan *Response
	// writeMu serialises writes to the server's stdin. It is separate from mu
	// so a write blocked on a full pipe never holds the lock readLoop needs to
	// deliver responses: holding one lock across both was a mutual stall.
	writeMu sync.Mutex
	nextID  int64
	closed  bool
	done    chan struct{}
}

// ClientOptions configures a new LSP client.
type ClientOptions struct {
	Binary     string
	Args       []string
	Dir        string
	ServerName string
	Logger     *slog.Logger
	// Env is added to the inherited environment, for settings that keep a
	// server from acting on repository content (GOTOOLCHAIN=local, for one).
	Env []string
}

// StartClient spawns the language server binary and begins message processing.
func StartClient(ctx context.Context, opts ClientOptions) (*Client, error) {
	if opts.Binary == "" {
		return nil, fmt.Errorf("lsp: empty binary")
	}

	cmd := exec.CommandContext(ctx, opts.Binary, opts.Args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	configureProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("lsp: stdout pipe: %w", err)
	}

	// Stderr is discarded or logged if logger is non-nil
	if opts.Logger != nil {
		cmd.Stderr = nil
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("lsp: start %s: %w", opts.Binary, err)
	}

	name := opts.ServerName
	if name == "" {
		name = filepath.Base(opts.Binary)
	}

	c := &Client{
		cmd:        cmd,
		stdin:      stdin,
		serverName: name,
		logger:     opts.Logger,
		pending:    make(map[int64]chan *Response),
		done:       make(chan struct{}),
	}

	go c.readLoop(bufio.NewReader(stdout))

	return c, nil
}

// ServerName returns the display name of the server.
func (c *Client) ServerName() string {
	return c.serverName
}

func (c *Client) readLoop(r *bufio.Reader) {
	defer func() {
		if r := recover(); r != nil {
			c.failAllPending(fmt.Errorf("lsp readLoop panic: %v", r))
		}
		close(c.done)
	}()
	for {
		buf, err := ReadMessage(r)
		if err != nil {
			c.failAllPending(err)
			return
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(buf, &raw); err != nil {
			continue
		}

		// Check if it's a server-to-client request (has both id AND method)
		if methodRaw, ok := raw["method"]; ok && len(methodRaw) > 0 {
			if idRaw, ok := raw["id"]; ok && len(idRaw) > 0 {
				var method string
				_ = json.Unmarshal(methodRaw, &method)
				// Automatically respond to server requests
				var result json.RawMessage = []byte("null")
				if method == "workspace/configuration" {
					result = configurationResult(raw["params"])
				}
				respPayload, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      idRaw,
					"result":  result,
				})
				// Answered off the read loop: if the server's stdin is full
				// because it is itself blocked writing to us, a write here
				// would stop the loop that unblocks it.
				go func() { _ = c.write(respPayload) }()
				continue
			}
			// Notification (method without id)
			var method string
			_ = json.Unmarshal(methodRaw, &method)
			if c.logger != nil {
				c.logger.Debug("lsp: notification received", "server", c.serverName, "method", method)
			}
			continue
		}

		// Response to a client request (has id, no method)
		if idRaw, ok := raw["id"]; ok && len(idRaw) > 0 {
			var id int64
			if err := json.Unmarshal(idRaw, &id); err == nil {
				var resp Response
				if err := json.Unmarshal(buf, &resp); err == nil {
					c.deliverResponse(id, &resp)
					continue
				}
			}
		}
	}
}

func (c *Client) deliverResponse(id int64, resp *Response) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()

	if ok && ch != nil {
		ch <- resp
	}
}

func (c *Client) failAllPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, ch := range c.pending {
		delete(c.pending, id)
		if ch != nil {
			ch <- &Response{
				Error: &ResponseError{
					Code:    -32000,
					Message: fmt.Sprintf("lsp stream terminated: %v", err),
				},
			}
		}
	}
}

// Call sends a JSON-RPC 2.0 request and waits for the response.
func (c *Client) Call(ctx context.Context, method string, params any) (*Response, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}

	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan *Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, fmt.Errorf("lsp: marshal params: %w", err)
		}
		rawParams = b
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("lsp: marshal request: %w", err)
	}

	err = c.write(payload)

	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("lsp: write request: %w", err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// Tell the server to stop working on it; best effort and off this
		// goroutine, since the caller has already given up.
		go func() {
			if b, err := json.Marshal(Notification{JSONRPC: "2.0", Method: "$/cancelRequest",
				Params: json.RawMessage(fmt.Sprintf(`{"id":%d}`, id))}); err == nil {
				_ = c.write(b)
			}
		}()
		return nil, ctx.Err()
	case <-c.done:
		return nil, ErrClientClosed
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp, nil
	}
}

// write sends one framed message, serialised with every other write.
func (c *Client) write(payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteMessage(c.stdin, payload)
}

// configurationResult answers workspace/configuration with one empty object
// per requested item: servers index the reply by item, and a single-element
// answer to a multi-item request is read as missing configuration.
func configurationResult(params json.RawMessage) json.RawMessage {
	var p struct {
		Items []json.RawMessage `json:"items"`
	}
	n := 1
	if json.Unmarshal(params, &p) == nil && len(p.Items) > 0 {
		n = min(len(p.Items), 1000)
	}
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{}
	}
	b, _ := json.Marshal(items)
	return b
}

// Notify sends a JSON-RPC 2.0 notification.
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClientClosed
	}

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("lsp: marshal notification params: %w", err)
		}
		rawParams = b
	}

	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
	}

	payload, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("lsp: marshal notification: %w", err)
	}

	return c.write(payload)
}

// Initialize performs the standard LSP initialize handshake.
func (c *Client) Initialize(ctx context.Context, rootDir string) (*InitializeResult, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		absRoot = rootDir
	}

	rootURI := FilePathToURI(absRoot)
	caps := json.RawMessage(`{
		"textDocument": {
			"definition": {"dynamicRegistration": false, "linkSupport": true},
			"references": {"dynamicRegistration": false}
		},
		"workspace": {
			"workspaceFolders": true
		}
	}`)

	params := InitializeParams{
		ProcessID:        os.Getpid(),
		RootURI:          rootURI,
		RootPath:         absRoot,
		Capabilities:     caps,
		WorkspaceFolders: []WorkspaceFolder{{URI: rootURI, Name: filepath.Base(absRoot)}},
	}

	resp, err := c.Call(ctx, "initialize", params)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	var res InitializeResult
	if len(resp.Result) > 0 {
		_ = json.Unmarshal(resp.Result, &res)
	}

	// Send initialized notification
	if err := c.Notify(ctx, "initialized", struct{}{}); err != nil {
		return nil, fmt.Errorf("notify initialized: %w", err)
	}

	return &res, nil
}

// DidOpen informs the language server of an open file and its contents.
func (c *Client) DidOpen(ctx context.Context, absPath, languageID, content string) error {
	uri := FilePathToURI(absPath)
	params := DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: languageID,
			Version:    1,
			Text:       content,
		},
	}
	return c.Notify(ctx, "textDocument/didOpen", params)
}

// Definition queries textDocument/definition at 0-based (line, char).
func (c *Client) Definition(ctx context.Context, absPath string, line0, char0 int) ([]Location, error) {
	uri := FilePathToURI(absPath)
	params := DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line0, Character: char0},
	}

	resp, err := c.Call(ctx, "textDocument/definition", params)
	if err != nil {
		return nil, err
	}

	return ParseLocations(resp.Result)
}

// References queries textDocument/references at 0-based (line, char).
func (c *Client) References(ctx context.Context, absPath string, line0, char0 int, includeDecl bool) ([]Location, error) {
	uri := FilePathToURI(absPath)
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line0, Character: char0},
		Context:      ReferenceContext{IncludeDeclaration: includeDecl},
	}

	resp, err := c.Call(ctx, "textDocument/references", params)
	if err != nil {
		return nil, err
	}

	return ParseLocations(resp.Result)
}

// Close gracefully shuts down the language server.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// Shutdown with a 2-second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = c.Call(ctx, "shutdown", nil)
	_ = c.Notify(context.Background(), "exit", nil)

	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	_ = c.stdin.Close()

	if c.cmd != nil {
		exited := make(chan error, 1)
		go func() {
			exited <- c.cmd.Wait()
		}()

		select {
		case <-time.After(1 * time.Second):
			killProcessGroup(c.cmd)
		case waitErr := <-exited:
			_ = waitErr // the server was asked to exit; its exit status says nothing more
		}
	}

	return nil
}

// FilePathToURI converts a local file path to a file:// URI.
func FilePathToURI(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.ToSlash(abs)
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	u := url.URL{
		Scheme: "file",
		Path:   abs,
	}
	return u.String()
}

// URIToFilePath converts a file:// URI to a local absolute file path.
func URIToFilePath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	u, err := url.Parse(uri)
	if err != nil {
		return strings.TrimPrefix(uri, "file://")
	}
	p := u.Path
	return filepath.FromSlash(p)
}
