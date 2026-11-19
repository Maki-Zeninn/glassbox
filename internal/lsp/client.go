// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package lsp

// client.go is the JSON-RPC 2.0 client Glassbox uses to push diagnostics to an
// already-running language server such as rust-analyzer. Glassbox acts as a
// client, not a server: it opens a connection, sends
// `textDocument/publishDiagnostics`, and disconnects.
//
// Framing follows the LSP base protocol: `Content-Length: <n>\r\n\r\n<body>`,
// with a JSON body. Both transports are supported because an editor may expose
// the server over a Unix socket (Linux, macOS) or a loopback TCP port
// (containers, Windows).
//
// Every operation is bounded by DefaultTimeout. Publishing diagnostics is a
// convenience for the developer, so a missing, refused or wedged editor must
// never delay or fail `glassbox debug`.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout bounds a whole connect-publish-disconnect cycle.
const DefaultTimeout = 5 * time.Second

// methodPublishDiagnostics is the LSP notification Glassbox sends.
const methodPublishDiagnostics = "textDocument/publishDiagnostics"

// jsonrpcVersion is the only JSON-RPC version LSP uses.
const jsonrpcVersion = "2.0"

// ErrNotConnected is returned when an operation is attempted without a live
// connection.
var ErrNotConnected = errors.New("lsp: not connected")

// PublishDiagnosticsParams is the payload of textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string               `json:"uri"`
	Diagnostics []ProtocolDiagnostic `json:"diagnostics"`
}

// jsonrpcRequest is an outgoing JSON-RPC request or notification. A
// notification has no ID.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      *int        `json:"id,omitempty"`
}

// jsonrpcResponse is an incoming JSON-RPC response. Glassbox only needs it to
// correlate replies with the requests it sent.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client is a JSON-RPC 2.0 connection to a language server.
type Client struct {
	address string
	timeout time.Duration

	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
	nextID int
}

// Connect opens a connection to address, which may be a Unix socket path or a
// host:port TCP address. The dial is bounded by the context and by the client's
// timeout, whichever expires first.
func Connect(ctx context.Context, address string, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("lsp: empty server address")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, networkFor(address), address)
	if err != nil {
		return nil, fmt.Errorf("lsp: connect to %s: %w", address, err)
	}

	return &Client{
		address: address,
		timeout: timeout,
		conn:    conn,
		reader:  bufio.NewReader(conn),
		nextID:  1,
	}, nil
}

// networkFor picks the transport from the address shape: a bare path is a Unix
// socket, anything containing a port is TCP.
func networkFor(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return "tcp"
	}
	return "unix"
}

// Connected reports whether the client has a live connection.
func (c *Client) Connected() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Address returns the address the client was configured with.
func (c *Client) Address() string {
	if c == nil {
		return ""
	}
	return c.address
}

// Close closes the connection. It is safe to call on a nil or already-closed
// client so callers can use `defer client.Close()` unconditionally.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.reader = nil
	return err
}

// PublishDiagnostics sends one textDocument/publishDiagnostics notification per
// document, clearing diagnostics for every URI in the map.
func (c *Client) PublishDiagnostics(ctx context.Context, byURI DiagnosticsMap) error {
	if c == nil || !c.Connected() {
		return ErrNotConnected
	}

	for uri, diags := range byURI {
		if diags == nil {
			diags = []ProtocolDiagnostic{}
		}
		params := PublishDiagnosticsParams{URI: uri, Diagnostics: diags}
		if err := c.notify(ctx, methodPublishDiagnostics, params); err != nil {
			return err
		}
	}
	return nil
}

// ClearDiagnostics sends an empty diagnostic list for each URI, which tells the
// editor to clear any annotations Glassbox published on a previous run.
func (c *Client) ClearDiagnostics(ctx context.Context, uris []string) error {
	if c == nil || !c.Connected() {
		return ErrNotConnected
	}

	byURI := make(DiagnosticsMap, len(uris))
	for _, uri := range uris {
		byURI[uri] = []ProtocolDiagnostic{}
	}
	return c.PublishDiagnostics(ctx, byURI)
}

// notify sends a JSON-RPC notification. Notifications have no ID, so no reply
// is awaited: a write error is the only failure mode.
func (c *Client) notify(ctx context.Context, method string, params interface{}) error {
	payload, err := json.Marshal(jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("lsp: encode %s: %w", method, err)
	}

	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return ErrNotConnected
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("lsp: set write deadline: %w", err)
	}

	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(c.conn, frame); err != nil {
		return fmt.Errorf("lsp: write %s header: %w", method, err)
	}
	if _, err := c.conn.Write(payload); err != nil {
		return fmt.Errorf("lsp: write %s body: %w", method, err)
	}
	return nil
}

// Call sends a JSON-RPC request and waits for the matching response. It is used
// by the lsp:serve path when Glassbox needs a reply, not by diagnostics
// publishing, which is fire-and-forget.
func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	if c == nil || !c.Connected() {
		return nil, ErrNotConnected
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, ErrNotConnected
	}

	id := c.nextID
	c.nextID++

	payload, err := json.Marshal(jsonrpcRequest{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  params,
		ID:      &id,
	})
	if err != nil {
		return nil, fmt.Errorf("lsp: encode %s: %w", method, err)
	}

	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("lsp: set deadline: %w", err)
	}

	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(c.conn, frame); err != nil {
		return nil, fmt.Errorf("lsp: write %s header: %w", method, err)
	}
	if _, err := c.conn.Write(payload); err != nil {
		return nil, fmt.Errorf("lsp: write %s body: %w", method, err)
	}

	for {
		msg, err := c.readMessage()
		if err != nil {
			return nil, err
		}

		var response jsonrpcResponse
		if err := json.Unmarshal(msg, &response); err != nil {
			// Ignore notifications and unparseable frames rather than
			// desynchronising the stream.
			continue
		}
		if response.ID == nil || *response.ID != id {
			continue
		}
		if response.Error != nil {
			return nil, fmt.Errorf("lsp: %s failed: %s", method, response.Error.Message)
		}
		return response.Result, nil
	}
}

// readMessage reads one Content-Length framed message. The caller holds c.mu.
func (c *Client) readMessage() ([]byte, error) {
	if c.reader == nil {
		return nil, ErrNotConnected
	}

	length := -1
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("lsp: read header: %w", err)
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}

		name, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, convErr := strconv.Atoi(strings.TrimSpace(value))
			if convErr != nil {
				return nil, fmt.Errorf("lsp: invalid Content-Length %q: %w", value, convErr)
			}
			length = parsed
		}
	}

	if length < 0 {
		return nil, errors.New("lsp: message missing Content-Length header")
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, fmt.Errorf("lsp: read body: %w", err)
	}
	return body, nil
}

// PublishAndClose connects to address, publishes byURI, and disconnects. It is
// the whole `glassbox debug --lsp-socket` interaction, bounded by timeout, and
// returns a descriptive error instead of panicking when the editor is not
// listening. Callers should treat the error as a warning.
func PublishAndClose(
	ctx context.Context,
	address string,
	byURI DiagnosticsMap,
	timeout time.Duration,
) error {
	client, err := Connect(ctx, address, timeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return client.PublishDiagnostics(ctx, byURI)
}
