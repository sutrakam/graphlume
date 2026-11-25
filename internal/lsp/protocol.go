package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// JSON-RPC 2.0 message framing and protocol types for LSP.

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError is a JSON-RPC 2.0 error.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("lsp: rpc error %d: %s", e.Code, e.Message)
}

// Notification is a JSON-RPC 2.0 notification (no ID).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Position is a 0-based line and character offset in an LSP document.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a start and end Position.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a physical source location (URI and Range).
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink is an LSP 3.14+ target location link.
type LocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// TextDocumentIdentifier identifies a document by URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentItem is an open document's full contents.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// DidOpenTextDocumentParams is sent on textDocument/didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// TextDocumentPositionParams locates a position inside a document.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// DefinitionParams locates a position for textDocument/definition.
type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ReferenceContext controls textDocument/references queries.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams queries textDocument/references.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// WorkspaceFolder represents a root workspace folder.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// InitializeParams is sent on the initialize request.
type InitializeParams struct {
	ProcessID        int               `json:"processId"`
	RootURI          string            `json:"rootUri,omitempty"`
	RootPath         string            `json:"rootPath,omitempty"`
	Capabilities     json.RawMessage   `json:"capabilities"`
	WorkspaceFolders []WorkspaceFolder `json:"workspaceFolders,omitempty"`
}

// InitializeResult is the server's initialize response.
type InitializeResult struct {
	Capabilities json.RawMessage `json:"capabilities"`
	ServerInfo   *ServerInfo     `json:"serverInfo,omitempty"`
}

// ServerInfo is returned by the server on initialize.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// WriteMessage encodes and writes a JSON-RPC message with Content-Length framing.
func WriteMessage(w io.Writer, payload []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(w, header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}

// ReadMessage reads a Content-Length framed JSON-RPC message.
func ReadMessage(r *bufio.Reader) ([]byte, error) {
	// Headers are a few short lines. Both their length and their number are
	// bounded, so a server writing garbage without a newline (or an endless
	// header block) is an error, not an unbounded read.
	const (
		maxHeaderLine  = 4096
		maxHeaderLines = 32
	)
	contentLength := -1
	for lines := 0; ; lines++ {
		if lines >= maxHeaderLines {
			return nil, fmt.Errorf("lsp: more than %d header lines", maxHeaderLines)
		}
		raw, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) || len(raw) > maxHeaderLine {
			return nil, fmt.Errorf("lsp: header line longer than %d bytes", maxHeaderLine)
		}
		if err != nil {
			return nil, err
		}
		line := strings.TrimRight(string(raw), "\r\n")
		if line == "" {
			// Empty line marks end of headers
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				n, parseErr := strconv.Atoi(val)
				if parseErr == nil {
					contentLength = n
				}
			}
		}
	}

	const maxContentLength = 32 * 1024 * 1024 // 32 MiB
	if contentLength < 0 || contentLength > maxContentLength {
		return nil, fmt.Errorf("lsp: invalid or excessive Content-Length: %d", contentLength)
	}

	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("lsp: read payload: %w", err)
	}
	return buf, nil
}

// ParseLocations parses either Location, []Location, or []LocationLink into []Location.
func ParseLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	// 1. Try single Location
	var single Location
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []Location{single}, nil
	}

	// 2. Try []Location
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err == nil && len(locs) > 0 && locs[0].URI != "" {
		return locs, nil
	}

	// 3. Try []LocationLink (LSP 3.14+)
	var links []LocationLink
	if err := json.Unmarshal(raw, &links); err == nil && len(links) > 0 && links[0].TargetURI != "" {
		out := make([]Location, len(links))
		for i, link := range links {
			target := link.TargetSelectionRange
			if target.Start.Line == 0 && target.End.Line == 0 &&
				target.Start.Character == 0 && target.End.Character == 0 {
				target = link.TargetRange
			}
			out[i] = Location{
				URI:   link.TargetURI,
				Range: target,
			}
		}
		return out, nil
	}

	return nil, nil
}
