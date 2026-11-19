// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package lsp

// mock_server_test.go is a minimal JSON-RPC server that captures the
// notifications Glassbox publishes. Using a real socket rather than a mocked
// transport means the tests also cover the LSP base-protocol framing, which is
// where this kind of integration usually breaks.

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// capturedNotification is one notification the mock server received.
type capturedNotification struct {
	method string
	params json.RawMessage
}

// publishParams decodes the captured notification as a
// textDocument/publishDiagnostics payload.
func (n capturedNotification) publishParams(t *testing.T) (string, []ProtocolDiagnostic) {
	t.Helper()

	var params PublishDiagnosticsParams
	if err := json.Unmarshal(n.params, &params); err != nil {
		t.Fatalf("decode publishDiagnostics params: %v", err)
	}
	return params.URI, params.Diagnostics
}

// mockServer is a one-connection JSON-RPC listener.
type mockServer struct {
	listener net.Listener
	wg       sync.WaitGroup
}

// Addr returns the host:port the server is listening on.
func (m *mockServer) Addr() string { return m.listener.Addr().String() }

// Close stops the server and waits for its goroutine.
func (m *mockServer) Close() {
	_ = m.listener.Close()
	m.wg.Wait()
}

// startMockLSPServer listens on loopback TCP and serves framed JSON-RPC
// messages, recording every notification it receives. The returned func drains
// the recorded notifications.
func startMockLSPServer(t *testing.T) (*mockServer, string, func() []capturedNotification) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var mu sync.Mutex
	var captured []capturedNotification

	server := &mockServer{listener: listener}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		serveMockConnection(listener, func(notification capturedNotification) {
			mu.Lock()
			captured = append(captured, notification)
			mu.Unlock()
		})
	}()

	drain := func() []capturedNotification {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedNotification, len(captured))
		copy(out, captured)
		return out
	}

	t.Cleanup(server.Close)
	return server, server.Addr(), drain
}

// serveMockConnection accepts a single client and reads framed messages until
// the client disconnects.
func serveMockConnection(listener net.Listener, record func(capturedNotification)) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	for {
		payload, err := readFramedMessage(reader)
		if err != nil {
			return
		}

		var message struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		// Requests need a reply; notifications do not. Glassbox only sends
		// notifications on this path, so anything with a method is recorded.
		if message.Method != "" {
			record(capturedNotification{method: message.Method, params: message.Params})
		}
	}
}

// readFramedMessage reads one `Content-Length: <n>\r\n\r\n<body>` frame.
func readFramedMessage(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}

		name, value, found := strings.Cut(trimmed, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}

		parsed, convErr := strconv.Atoi(strings.TrimSpace(value))
		if convErr != nil {
			return nil, convErr
		}
		length = parsed
	}

	if length < 0 {
		return nil, io.ErrUnexpectedEOF
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}
