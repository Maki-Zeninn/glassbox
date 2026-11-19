// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package lsp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/visualizer"
	"go.lsp.dev/protocol"
)

// Server provides a minimal LSP backend for Soroban hinting.
type Server struct {
	mu        sync.RWMutex
	documents map[protocol.DocumentURI]string
}

// NewServer creates a new LSP backend server.
func NewServer() *Server {
	return &Server{
		documents: make(map[protocol.DocumentURI]string),
	}
}

// Initialize validates the LSP initialization request and advertises capabilities.
func (s *Server) Initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			HoverProvider: true,
			TextDocumentSync: protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.TextDocumentSyncKindFull,
			},
		},
	}, nil
}

// Initialized is called after initialize completes.
func (s *Server) Initialized(ctx context.Context, params *protocol.InitializedParams) error {
	return nil
}

// Shutdown ends the current LSP session.
func (s *Server) Shutdown(ctx context.Context) error {
	logger.Logger.Info("LSP server shutting down")
	return nil
}

// Exit is called when the LSP client exits.
func (s *Server) Exit(ctx context.Context) error {
	return nil
}

// DidOpen handles textDocument/didOpen.
func (s *Server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	if params.TextDocument.URI == "" {
		return fmt.Errorf("document URI is empty")
	}

	s.mu.Lock()
	s.documents[params.TextDocument.URI] = params.TextDocument.Text
	s.mu.Unlock()
	return nil
}

// DidChange handles textDocument/didChange.
func (s *Server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	if params.TextDocument.URI == "" {
		return fmt.Errorf("document URI is empty")
	}

	if len(params.ContentChanges) == 0 {
		return nil
	}

	text := params.ContentChanges[0].Text
	s.mu.Lock()
	s.documents[params.TextDocument.URI] = text
	s.mu.Unlock()
	return nil
}

// DidClose handles textDocument/didClose.
func (s *Server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	if params.TextDocument.URI == "" {
		return fmt.Errorf("document URI is empty")
	}

	s.mu.Lock()
	delete(s.documents, params.TextDocument.URI)
	s.mu.Unlock()
	return nil
}

// Hover returns inline hover content for known host functions.
func (s *Server) Hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	text, found := s.getDocument(params.TextDocument.URI)
	if !found {
		return nil, fmt.Errorf("document not found: %s", params.TextDocument.URI)
	}

	lineText := lineAtPosition(text, params.Position)
	if lineText == "" {
		return nil, nil
	}

	functionName, start, end := hostFunctionAtPosition(lineText, params.Position)
	if functionName == "" {
		return nil, nil
	}

	content := visualizer.HostFunctionHoverContent(functionName)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: content,
		},
		Range: &protocol.Range{
			Start: protocol.Position{Line: params.Position.Line, Character: start},
			End:   protocol.Position{Line: params.Position.Line, Character: end},
		},
	}, nil
}

// DiagnosticsForDocument returns diagnostics for the given document URI.
func (s *Server) DiagnosticsForDocument(ctx context.Context, uri protocol.DocumentURI) ([]protocol.Diagnostic, error) {
	text, found := s.getDocument(uri)
	if !found {
		return nil, fmt.Errorf("document not found: %s", uri)
	}

	hints := visualizer.DiagnosticsForSource(text)
	diagnostics := make([]protocol.Diagnostic, 0, len(hints))
	for _, hint := range hints {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: uint32(hint.Line), Character: hint.Start},
				End:   protocol.Position{Line: uint32(hint.Line), Character: hint.End},
			},
			Severity: protocol.DiagnosticSeverityInformation,
			Source:   "Glassbox-lsp",
			Message:  hint.Message,
		})
	}

	return diagnostics, nil
}

func (s *Server) getDocument(uri protocol.DocumentURI) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	text, ok := s.documents[uri]
	return text, ok
}

func lineAtPosition(text string, position protocol.Position) string {
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	lineIndex := int(position.Line)
	if lineIndex < 0 || lineIndex >= len(lines) {
		return ""
	}
	return lines[lineIndex]
}

func hostFunctionAtPosition(line string, position protocol.Position) (string, uint32, uint32) {
	if line == "" {
		return "", 0, 0
	}

	cursor := int(position.Character)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(line) {
		cursor = len(line)
	}

	start := cursor
	for start > 0 && isWordCharacter(line[start-1]) {
		start--
	}

	end := cursor
	for end < len(line) && isWordCharacter(line[end]) {
		end++
	}

	word := line[start:end]
	if word == "" {
		return "", 0, 0
	}

	for _, candidate := range visualizer.KnownHostFunctions() {
		if word == candidate {
			return word, uint32(start), uint32(end)
		}
	}

	return "", 0, 0
}

func isWordCharacter(r byte) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}
