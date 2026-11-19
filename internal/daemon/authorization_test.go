// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"testing"
)

func TestNewAuthPolicy_Defaults(t *testing.T) {
	p := NewAuthPolicy(AuthConfig{})

	if p.config.Role != RoleDebug {
		t.Errorf("expected default role %q, got %q", RoleDebug, p.config.Role)
	}
	if p.config.BindScope != BindLocalhost {
		t.Errorf("expected default bind scope %q, got %q", BindLocalhost, p.config.BindScope)
	}
}

func TestAuthPolicy_NoTokenAllAllowed(t *testing.T) {
	p := NewAuthPolicy(AuthConfig{})

	if !p.IsAuthenticated("") {
		t.Error("expected all requests to be authenticated when no token is configured")
	}
	if !p.IsAuthenticated("anything") {
		t.Error("expected all requests to be authenticated when no token is configured")
	}
}

func TestAuthPolicy_TokenRequired(t *testing.T) {
	p := NewAuthPolicy(AuthConfig{Token: "secret123"})

	if p.IsAuthenticated("") {
		t.Error("expected empty token to be rejected")
	}
	if p.IsAuthenticated("wrong") {
		t.Error("expected wrong token to be rejected")
	}
	if !p.IsAuthenticated("secret123") {
		t.Error("expected correct token to be accepted")
	}
}

func TestAuthPolicy_RolePermissions(t *testing.T) {
	tests := []struct {
		name      string
		role      Role
		op        Operation
		wantAllow bool
	}{
		{"readonly can read trace", RoleReadOnly, OpGetTrace, true},
		{"readonly cannot debug", RoleReadOnly, OpDebugTransaction, false},
		{"readonly cannot metrics", RoleReadOnly, OpMetrics, false},
		{"debug can read trace", RoleDebug, OpGetTrace, true},
		{"debug can debug", RoleDebug, OpDebugTransaction, true},
		{"debug cannot metrics", RoleDebug, OpMetrics, false},
		{"admin can read", RoleAdmin, OpGetTrace, true},
		{"admin can debug", RoleAdmin, OpDebugTransaction, true},
		{"admin can metrics", RoleAdmin, OpMetrics, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewAuthPolicy(AuthConfig{Role: tt.role})
			got := p.IsAuthorized(tt.op)
			if got != tt.wantAllow {
				t.Errorf("IsAuthorized(%q) = %v, want %v", tt.op, got, tt.wantAllow)
			}
		})
	}
}

func TestAuthPolicy_CheckAccess_Denials(t *testing.T) {
	p := NewAuthPolicy(AuthConfig{
		Token:        "secret",
		Role:         RoleReadOnly,
		AuditEnabled: true,
	})

	// Auth failure
	allowed, reason := p.CheckAccess("wrong", OpGetTrace, "127.0.0.1:12345")
	if allowed {
		t.Error("expected denial for wrong token")
	}
	if reason == "" {
		t.Error("expected denial reason")
	}

	// Auth success but authorization failure (readonly can't debug)
	allowed, reason = p.CheckAccess("secret", OpDebugTransaction, "127.0.0.1:12345")
	if allowed {
		t.Error("expected denial for unauthorized operation")
	}

	denials := p.GetDenials()
	if len(denials) != 2 {
		t.Errorf("expected 2 audit entries, got %d", len(denials))
	}
	if denials[0].Reason == "" {
		t.Error("audit entry should have a reason")
	}
	if denials[0].Operation == "" {
		t.Error("audit entry should have an operation")
	}
}

func TestAuthPolicy_ClearDenials(t *testing.T) {
	p := NewAuthPolicy(AuthConfig{
		Token:        "secret",
		Role:         RoleReadOnly,
		AuditEnabled: true,
	})

	p.CheckAccess("wrong", OpGetTrace, "127.0.0.1:12345")
	if len(p.GetDenials()) != 1 {
		t.Fatal("expected 1 denial")
	}

	p.ClearDenials()
	if len(p.GetDenials()) != 0 {
		t.Error("expected 0 denials after clear")
	}
}

func TestAuthPolicy_BindScope(t *testing.T) {
	tests := []struct {
		name       string
		scope      BindScope
		addr       string
		wantAllow  bool
	}{
		{"localhost loopback", BindLocalhost, "127.0.0.1:1234", true},
		{"localhost ipv6 loopback", BindLocalhost, "[::1]:1234", true},
		{"localhost lan blocked", BindLocalhost, "192.168.1.1:1234", false},
		{"localhost external blocked", BindLocalhost, "8.8.8.8:1234", false},
		{"lan loopback", BindLAN, "127.0.0.1:1234", true},
		{"lan private", BindLAN, "192.168.1.1:1234", true},
		{"lan private 10.x", BindLAN, "10.0.0.1:1234", true},
		{"lan external blocked", BindLAN, "8.8.8.8:1234", false},
		{"all loopback", BindAll, "127.0.0.1:1234", true},
		{"all external", BindAll, "8.8.8.8:1234", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewAuthPolicy(AuthConfig{BindScope: tt.scope})
			got := p.IsAllowedBindAddr(tt.addr)
			if got != tt.wantAllow {
				t.Errorf("IsAllowedBindAddr(%q) = %v, want %v", tt.addr, got, tt.wantAllow)
			}
		})
	}
}

func TestAuthPolicy_AuditDisabledNoRecording(t *testing.T) {
	p := NewAuthPolicy(AuthConfig{
		Token:        "secret",
		Role:         RoleReadOnly,
		AuditEnabled: false,
	})

	p.CheckAccess("wrong", OpGetTrace, "127.0.0.1:1234")
	if len(p.GetDenials()) != 0 {
		t.Error("expected no audit entries when auditing is disabled")
	}
}

func TestAuthPolicy_UnknownOperation(t *testing.T) {
	p := NewAuthPolicy(AuthConfig{Role: RoleAdmin})
	if p.IsAuthorized("unknown_op") {
		t.Error("unknown operation should not be authorized")
	}
}
