// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package testhelpers

import (
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/session"
)

// SessionFixture is a minimal builder for session.Data used in regression tests.
type SessionFixture struct {
	ID                  string
	TxHash              string
	Network             string
	Status              string
	CreatedAt           time.Time
	LastAccessAt        time.Time
	EnvelopeXdr         string
	ResultMetaXdr       string
	Name                string
	SchemaVersion       int
	AuditHash           string
	AuditSignature      string
	PreviousSessionHash string
	SimRequestJSON      string
	SimResponseJSON     string
}

// NewSessionFixture creates a new session fixture with stub defaults.
func NewSessionFixture() *SessionFixture {
	now := time.Now()
	return &SessionFixture{
		ID:            "sess_test_" + CanonicalTxHash[:8],
		TxHash:        CanonicalTxHash,
		Network:       CanonicalNetwork,
		Status:        "active",
		CreatedAt:     now,
		LastAccessAt:  now,
		EnvelopeXdr:   CanonicalEnvelopeXDR,
		ResultMetaXdr: CanonicalEnvelopeXDR,
		SchemaVersion: session.SchemaVersion,
	}
}

// WithID sets the session ID.
func (f *SessionFixture) WithID(id string) *SessionFixture {
	f.ID = id
	return f
}

// WithTxHash sets the transaction hash.
func (f *SessionFixture) WithTxHash(hash string) *SessionFixture {
	f.TxHash = hash
	return f
}

// WithNetwork sets the network.
func (f *SessionFixture) WithNetwork(network string) *SessionFixture {
	f.Network = network
	return f
}

// WithStatus sets the status.
func (f *SessionFixture) WithStatus(status string) *SessionFixture {
	f.Status = status
	return f
}

// MissingTxHash clears the TxHash field to trigger validation failures.
func (f *SessionFixture) MissingTxHash() *SessionFixture {
	f.TxHash = ""
	return f
}

// WithSchemaVersion sets an explicit schema version.
func (f *SessionFixture) WithSchemaVersion(v int) *SessionFixture {
	f.SchemaVersion = v
	return f
}

// Legacy sets the schema version to the oldest still-supported version, so the
// fixture exercises session.UpgradeSessionData's migration path.
func (f *SessionFixture) Legacy() *SessionFixture {
	f.SchemaVersion = session.MinSupportedSchemaVersion
	return f
}

// TooNew sets the schema version above the current binary's supported version,
// simulating a session produced by a newer release of Glassbox.
func (f *SessionFixture) TooNew() *SessionFixture {
	f.SchemaVersion = session.SchemaVersion + 1
	return f
}

// WithAuditHash sets a well-formed audit hash (64 hex chars).
func (f *SessionFixture) WithAuditHash(hash string) *SessionFixture {
	f.AuditHash = hash
	return f
}

// WithAuditSignature sets a well-formed audit signature (128 hex chars).
func (f *SessionFixture) WithAuditSignature(sig string) *SessionFixture {
	f.AuditSignature = sig
	return f
}

// WithPreviousSessionHash sets the predecessor session's audit hash, forming a
// chain link.
func (f *SessionFixture) WithPreviousSessionHash(hash string) *SessionFixture {
	f.PreviousSessionHash = hash
	return f
}

// ValidAuditChain populates AuditHash/AuditSignature with well-formed (but
// synthetic) values, without a predecessor link — a valid genesis chain entry.
func (f *SessionFixture) ValidAuditChain() *SessionFixture {
	f.AuditHash = canonicalSHA256Hex
	f.AuditSignature = canonicalEd25519SigHex
	return f
}

// CorruptAuditHash sets AuditHash to a malformed (wrong-length) hex string.
func (f *SessionFixture) CorruptAuditHash() *SessionFixture {
	f.AuditHash = "not-a-valid-sha256-hash"
	f.AuditSignature = canonicalEd25519SigHex
	return f
}

// CorruptAuditSignature sets AuditSignature to a malformed (wrong-length) hex
// string, alongside a well-formed AuditHash.
func (f *SessionFixture) CorruptAuditSignature() *SessionFixture {
	f.AuditHash = canonicalSHA256Hex
	f.AuditSignature = "not-a-valid-signature"
	return f
}

// SelfReferentialChain sets PreviousSessionHash equal to AuditHash, an invalid
// self-referential chain link.
func (f *SessionFixture) SelfReferentialChain() *SessionFixture {
	f.AuditHash = canonicalSHA256Hex
	f.AuditSignature = canonicalEd25519SigHex
	f.PreviousSessionHash = canonicalSHA256Hex
	return f
}

// OrphanedAuditSignature sets AuditSignature without a corresponding AuditHash.
func (f *SessionFixture) OrphanedAuditSignature() *SessionFixture {
	f.AuditHash = ""
	f.AuditSignature = canonicalEd25519SigHex
	return f
}

// EncryptedBlob sets SimRequestJSON/SimResponseJSON to a non-JSON, base64-like
// blob standing in for opaque/encrypted-at-rest content, so callers can verify
// ToSimulationRequest/ToSimulationResponse fail cleanly instead of panicking.
func (f *SessionFixture) EncryptedBlob() *SessionFixture {
	const blob = "R0JYMS1FTkNSWVBURUQtQkxPQi1OT1QtSlNPTi1QQVJTQUJMRQ=="
	f.SimRequestJSON = blob
	f.SimResponseJSON = blob
	return f
}

// PartiallyWritten zeroes CreatedAt/LastAccessAt, simulating a session record
// interrupted mid-write (e.g. a crash between INSERT and the first UPDATE).
func (f *SessionFixture) PartiallyWritten() *SessionFixture {
	f.CreatedAt = time.Time{}
	f.LastAccessAt = time.Time{}
	return f
}

// TimestampsOutOfOrder sets LastAccessAt before CreatedAt, an internally
// inconsistent temporal ordering.
func (f *SessionFixture) TimestampsOutOfOrder() *SessionFixture {
	f.CreatedAt = time.Now()
	f.LastAccessAt = f.CreatedAt.Add(-1 * time.Hour)
	return f
}

// WithName sets the session's bookmark name.
func (f *SessionFixture) WithName(name string) *SessionFixture {
	f.Name = name
	return f
}

// Build converts the fixture into a session.Data.
func (f *SessionFixture) Build() *session.Data {
	return &session.Data{
		ID:                  f.ID,
		Name:                f.Name,
		TxHash:              f.TxHash,
		Network:             f.Network,
		Status:              f.Status,
		CreatedAt:           f.CreatedAt,
		LastAccessAt:        f.LastAccessAt,
		EnvelopeXdr:         f.EnvelopeXdr,
		ResultMetaXdr:       f.ResultMetaXdr,
		HorizonURL:          "https://horizon-testnet.stellar.org",
		SchemaVersion:       f.SchemaVersion,
		AuditHash:           f.AuditHash,
		AuditSignature:      f.AuditSignature,
		PreviousSessionHash: f.PreviousSessionHash,
		SimRequestJSON:      f.SimRequestJSON,
		SimResponseJSON:     f.SimResponseJSON,
	}
}

// canonicalSHA256Hex is a synthetic (all-zero) but well-formed 64-character
// hex SHA-256 stub, used by audit-chain fixtures that need a valid hash shape
// without computing a real digest.
var canonicalSHA256Hex = strings.Repeat("0", 64)

// canonicalEd25519SigHex is a synthetic (all-zero) but well-formed
// 128-character hex Ed25519 signature stub.
var canonicalEd25519SigHex = strings.Repeat("0", 128)
