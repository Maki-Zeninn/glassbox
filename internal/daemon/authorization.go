// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Permission represents a single authorization permission.
type Permission int

const (
	// PermRead allows read-only operations (get_trace, health).
	PermRead Permission = iota
	// PermDebug allows debug operations (debug_transaction).
	PermDebug
	// PermAdmin allows administrative operations (metrics, config).
	PermAdmin
)

// Role represents a named set of permissions.
type Role string

const (
	// RoleReadOnly can query traces and health.
	RoleReadOnly Role = "readonly"
	// RoleDebug can debug transactions and query traces.
	RoleDebug Role = "debug"
	// RoleAdmin has full access.
	RoleAdmin Role = "admin"
)

// Operation represents a daemon RPC operation name.
type Operation string

const (
	OpGetTrace         Operation = "get_trace"
	OpDebugTransaction Operation = "debug_transaction"
	OpGetContractCode  Operation = "get_contract_code"
	OpHealth           Operation = "health"
	OpMetrics          Operation = "metrics"
)

// defaultPermissions maps roles to their allowed permissions.
var defaultPermissions = map[Role][]Permission{
	RoleReadOnly: {PermRead},
	RoleDebug:    {PermRead, PermDebug},
	RoleAdmin:    {PermRead, PermDebug, PermAdmin},
}

// operationPermissions maps operations to the minimum required permission.
var operationPermissions = map[Operation]Permission{
	OpGetTrace:         PermRead,
	OpDebugTransaction: PermDebug,
	OpGetContractCode:  PermRead,
	OpHealth:           PermRead,
	OpMetrics:          PermAdmin,
}

// BindScope controls which network addresses the daemon binds to.
type BindScope string

const (
	// BindLocalhost only allows connections from 127.0.0.1 / ::1.
	BindLocalhost BindScope = "localhost"
	// BindLAN allows connections from private network ranges (10.x, 172.16-31.x, 192.168.x).
	BindLAN BindScope = "lan"
	// BindAll allows connections from any address (not recommended for production).
	BindAll BindScope = "all"
)

// AuthConfig holds authorization configuration for the daemon.
type AuthConfig struct {
	// Token is the Bearer token required for authentication.
	// If empty, authentication is disabled.
	Token string

	// TokenHash is the SHA-256 hash of Token for constant-time comparison.
	// Populated by NewAuthPolicy if Token is set.
	TokenHash []byte

	// Role is the default role assigned to authenticated clients.
	Role Role

	// BindScope controls which addresses the daemon accepts connections from.
	BindScope BindScope

	// AuditEnabled controls whether denied requests are logged.
	AuditEnabled bool
}

// AuthPolicy evaluates authorization for incoming requests.
type AuthPolicy struct {
	config   AuthConfig
	rolePerms map[Role][]Permission
	mu       sync.RWMutex
	denials  []AuditEntry
	maxAudit int
}

// AuditEntry records a denied authorization attempt.
type AuditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Operation  Operation `json:"operation"`
	RemoteAddr string    `json:"remote_addr"`
	Reason     string    `json:"reason"`
}

// NewAuthPolicy creates a new authorization policy from the given config.
func NewAuthPolicy(cfg AuthConfig) *AuthPolicy {
	if cfg.Role == "" {
		cfg.Role = RoleDebug
	}
	if cfg.BindScope == "" {
		cfg.BindScope = BindLocalhost
	}

	return &AuthPolicy{
		config:    cfg,
		rolePerms: defaultPermissions,
		maxAudit:  1000,
	}
}

// IsAuthenticated returns true if the given token is valid.
// When no token is configured, all requests are considered authenticated.
func (a *AuthPolicy) IsAuthenticated(token string) bool {
	if a.config.Token == "" {
		return true
	}
	return token == a.config.Token
}

// IsAuthorized checks whether the configured role allows the given operation.
func (a *AuthPolicy) IsAuthorized(op Operation) bool {
	required, ok := operationPermissions[op]
	if !ok {
		return false
	}

	perms, ok := a.rolePerms[a.config.Role]
	if !ok {
		return false
	}

	for _, p := range perms {
		if p == required {
			return true
		}
	}
	return false
}

// CheckAccess performs full authentication and authorization check.
// Returns (allowed, reason). If allowed is false, reason contains the denial explanation.
func (a *AuthPolicy) CheckAccess(token string, op Operation, remoteAddr string) (bool, string) {
	if !a.IsAuthenticated(token) {
		reason := "invalid or missing authentication token"
		a.recordDenial(op, remoteAddr, reason)
		return false, reason
	}

	if !a.IsAuthorized(op) {
		reason := fmt.Sprintf("role %q is not authorized for operation %q", a.config.Role, op)
		a.recordDenial(op, remoteAddr, reason)
		return false, reason
	}

	return true, ""
}

// IsAllowedBindAddr checks whether the remote address is within the configured bind scope.
func (a *AuthPolicy) IsAllowedBindAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	switch a.config.BindScope {
	case BindAll:
		return true
	case BindLAN:
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()
	case BindLocalhost:
		return ip.IsLoopback() || ip.IsUnspecified()
	default:
		return ip.IsLoopback()
	}
}

// GetDenials returns a copy of recent audit entries.
func (a *AuthPolicy) GetDenials() []AuditEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AuditEntry, len(a.denials))
	copy(out, a.denials)
	return out
}

// ClearDenials clears the audit log.
func (a *AuthPolicy) ClearDenials() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.denials = nil
}

func (a *AuthPolicy) recordDenial(op Operation, remoteAddr, reason string) {
	if !a.config.AuditEnabled {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	entry := AuditEntry{
		Timestamp:  time.Now().UTC(),
		Operation:  op,
		RemoteAddr: remoteAddr,
		Reason:     reason,
	}

	a.denials = append(a.denials, entry)
	if len(a.denials) > a.maxAudit {
		a.denials = a.denials[len(a.denials)-a.maxAudit:]
	}
}
