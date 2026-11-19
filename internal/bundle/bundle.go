// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package bundle implements a portable archive format for offline Soroban
// transaction replay.
//
// A bundle bundles together every artifact required for a deterministic replay:
//
//   - Transaction envelope (XDR)
//   - Transaction result metadata (XDR)
//   - Ledger state snapshot (key → entry map)
//   - Network identity (network name + passphrase)
//   - Protocol version
//   - Provenance metadata (fetched-at, glassbox version, ledger sequence, tx hash)
//   - Per-member SHA-256 checksums
//
// Bundles explicitly MUST NOT contain provider credentials (RPC tokens, API
// keys, private keys).  The export path strips any such fields.
//
// Format: a single JSON file with the .glassbox-bundle extension.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// FormatVersion is the current on-disk format version.  Increment this when
// the Manifest JSON schema changes in a breaking way.
const FormatVersion = 1

// Manifest is the complete archive written to / read from a bundle file.
type Manifest struct {
	// FormatVersion identifies the bundle schema version.
	FormatVersion int `json:"format_version"`

	// Provenance carries human-readable and machine-readable origin metadata.
	Provenance Provenance `json:"provenance"`

	// Network identifies the Stellar network.
	Network NetworkIdentity `json:"network"`

	// Transaction holds the raw XDR payloads.
	Transaction TransactionArtifacts `json:"transaction"`

	// LedgerState is the snapshot of ledger entries required for replay.
	// Keys and values are base64-encoded XDR strings.
	LedgerState map[string]string `json:"ledger_state"`

	// Checksums contains the SHA-256 hex digest of each member.
	// The key is the member name (e.g. "envelope_xdr", "result_meta_xdr",
	// "ledger_state").
	Checksums map[string]string `json:"checksums"`

	// ContentManifest is the artifact registry for this bundle.
	// It is generated automatically on Save and verified on Load.
	// Bundles created before this field was added will have a nil
	// ContentManifest; LoadFromFile treats them as legacy bundles and
	// validates via Checksums only.
	ContentManifest *ContentManifest `json:"content_manifest,omitempty"`

	// TraceData holds an optional execution trace captured during replay.
	// Empty for bundles created without --capture-trace.
	TraceData string `json:"trace_data,omitempty"`

	// SourceMapRef holds the optional source-map manifest reference
	// (e.g. a path or content hash) embedded for diagnostic replay.
	SourceMapRef string `json:"source_map_ref,omitempty"`

	// Signature holds an optional detached Ed25519 signature over the
	// bundle's canonical content hash.
	Signature string `json:"signature,omitempty"`

	// PathPolicy carries portable-path metadata used when importing the
	// bundle on a machine with a different workspace root.
	PathPolicy *BundlePathPolicy `json:"path_policy,omitempty"`
}

// Provenance carries the origin metadata of the bundle.
type Provenance struct {
	// GlassboxVersion is the CLI version that created the bundle.
	GlassboxVersion string `json:"glassbox_version"`
	// FetchedAt is the UTC wall-clock time the bundle was created.
	FetchedAt time.Time `json:"fetched_at"`
	// TxHash is the Stellar transaction hash.
	TxHash string `json:"tx_hash"`
	// LedgerSequence is the ledger the transaction was included in.
	LedgerSequence uint32 `json:"ledger_sequence,omitempty"`
	// ProtocolVersion is the Soroban protocol version at the time of fetch.
	ProtocolVersion uint32 `json:"protocol_version,omitempty"`
}

// NetworkIdentity identifies the Stellar network.
type NetworkIdentity struct {
	// Name is the canonical network name (testnet, mainnet, futurenet).
	Name string `json:"name"`
	// Passphrase is the Stellar network passphrase required for signing.
	Passphrase string `json:"passphrase"`
}

// TransactionArtifacts holds the raw XDR payloads for the transaction.
type TransactionArtifacts struct {
	// EnvelopeXDR is the base64-encoded TransactionEnvelope XDR.
	EnvelopeXDR string `json:"envelope_xdr"`
	// ResultMetaXDR is the base64-encoded TransactionResultMeta XDR.
	ResultMetaXDR string `json:"result_meta_xdr"`
}

// MemberName constants match the keys used in Checksums.
const (
	MemberEnvelopeXDR   = "envelope_xdr"
	MemberResultMetaXDR = "result_meta_xdr"
	MemberLedgerState   = "ledger_state"
	MemberProvenance    = "provenance"
	MemberNetwork       = "network"
)

// New builds a Manifest from the supplied parameters and computes checksums for
// all members.  No provider credentials (tokens, keys) are accepted or stored.
func New(
	glassboxVersion string,
	txHash string,
	ledgerSequence uint32,
	protocolVersion uint32,
	network NetworkIdentity,
	envelopeXDR string,
	resultMetaXDR string,
	ledgerState map[string]string,
) *Manifest {
	m := &Manifest{
		FormatVersion: FormatVersion,
		Provenance: Provenance{
			GlassboxVersion: glassboxVersion,
			FetchedAt:       time.Now().UTC(),
			TxHash:          txHash,
			LedgerSequence:  ledgerSequence,
			ProtocolVersion: protocolVersion,
		},
		Network: network,
		Transaction: TransactionArtifacts{
			EnvelopeXDR:   envelopeXDR,
			ResultMetaXDR: resultMetaXDR,
		},
		LedgerState: copyLedgerState(ledgerState),
	}
	m.Checksums = computeChecksums(m)
	m.ContentManifest = BuildContentManifest(m, nil)
	return m
}

// SaveToFile serialises the manifest to a JSON file at path.
// The file is written atomically via a temp-file rename.
func (m *Manifest) SaveToFile(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal bundle: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("failed to write bundle temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename bundle file: %w", err)
	}
	return nil
}

// LoadFromFile reads and validates a bundle from path.
// It verifies the format version and all member checksums before returning.
func LoadFromFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read bundle file %q: %w", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse bundle file %q: %w", path, err)
	}

	if err := m.Validate(); err != nil {
		return nil, err
	}

	return &m, nil
}

// VerificationReport is the output of Verify.
type VerificationReport struct {
	// OK is true when all checks pass.
	OK bool
	// FieldErrors maps member name → error description for failed checks.
	FieldErrors map[string]string
	// MissingMembers lists expected members that are absent from Checksums.
	MissingMembers []string
}

// Verify re-computes checksums for all bundle members and compares them to the
// stored values.  It returns a VerificationReport so callers can surface
// per-field errors rather than aborting on the first failure.
func (m *Manifest) Verify() *VerificationReport {
	report := &VerificationReport{
		OK:          true,
		FieldErrors: make(map[string]string),
	}

	live := computeChecksums(m)

	for member, liveSum := range live {
		stored, ok := m.Checksums[member]
		if !ok {
			report.MissingMembers = append(report.MissingMembers, member)
			report.OK = false
			continue
		}
		if stored != liveSum {
			report.FieldErrors[member] = fmt.Sprintf(
				"checksum mismatch: stored=%s computed=%s",
				stored, liveSum,
			)
			report.OK = false
		}
	}

	sort.Strings(report.MissingMembers)
	return report
}

// Validate performs structural validation: format version, required fields,
// and checksum integrity.  It also runs the content manifest check when a
// ContentManifest is present.
func (m *Manifest) Validate() error {
	if m.FormatVersion != FormatVersion {
		return &ValidationError{
			Member: "format_version",
			Reason: fmt.Sprintf("unsupported bundle format version %d (expected %d); upgrade Glassbox to open this bundle", m.FormatVersion, FormatVersion),
		}
	}

	if err := m.validateRequiredFields(); err != nil {
		return err
	}

	report := m.Verify()
	if !report.OK {
		return &ChecksumMismatchError{Report: report}
	}

	// Content manifest validation (hard errors only; warnings are surfaced
	// via ValidateContent which callers can invoke separately).
	if m.ContentManifest != nil {
		live := liveArtifactValues(m)
		hardErr, _ := m.ContentManifest.Validate(live)
		if hardErr != nil {
			return hardErr
		}
	}

	return nil
}

func (m *Manifest) validateRequiredFields() error {
	var missing []string

	if m.Provenance.TxHash == "" {
		missing = append(missing, "provenance.tx_hash")
	}
	if m.Network.Name == "" {
		missing = append(missing, "network.name")
	}
	if m.Network.Passphrase == "" {
		missing = append(missing, "network.passphrase")
	}
	if m.Transaction.EnvelopeXDR == "" {
		missing = append(missing, "transaction.envelope_xdr")
	}
	if m.Transaction.ResultMetaXDR == "" {
		missing = append(missing, "transaction.result_meta_xdr")
	}
	if len(m.LedgerState) == 0 {
		missing = append(missing, "ledger_state")
	}

	if len(missing) > 0 {
		return &ValidationError{
			Member: strings.Join(missing, ", "),
			Reason: fmt.Sprintf("bundle is missing required field(s): %s", strings.Join(missing, ", ")),
		}
	}
	return nil
}

// ValidationError is returned for structural problems with a bundle.
type ValidationError struct {
	Member string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("bundle validation failed [%s]: %s", e.Member, e.Reason)
}

// IsValidationError reports whether err is a *ValidationError.
func IsValidationError(err error) bool {
	_, ok := err.(*ValidationError)
	return ok
}

// ChecksumMismatchError is returned when one or more member checksums do not
// match the stored values.
type ChecksumMismatchError struct {
	Report *VerificationReport
}

func (e *ChecksumMismatchError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("bundle integrity check failed (%d member(s)):\n", len(e.Report.FieldErrors)+len(e.Report.MissingMembers)))

	for member, desc := range e.Report.FieldErrors {
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", member, desc))
	}
	for _, m := range e.Report.MissingMembers {
		sb.WriteString(fmt.Sprintf("  [%s] missing checksum entry\n", m))
	}
	sb.WriteString("\nThe bundle may have been tampered with or partially written.\n")
	sb.WriteString("Re-create it with 'glassbox debug bundle create' to generate a fresh, verified archive.")
	return sb.String()
}

// IsChecksumMismatch reports whether err is a *ChecksumMismatchError.
func IsChecksumMismatch(err error) bool {
	_, ok := err.(*ChecksumMismatchError)
	return ok
}

// ValidateContent runs the content manifest check and returns both hard errors
// and non-fatal warnings.  When no ContentManifest is present (legacy bundle),
// a warning is returned to indicate that content was validated via checksums only.
//
// This is separate from Validate() so callers (e.g. the CLI) can surface
// warnings without aborting the import.
func (m *Manifest) ValidateContent() (*ContentManifestValidationError, *ContentManifestWarning) {
	if m.ContentManifest == nil {
		return nil, &ContentManifestWarning{LegacyBundle: true}
	}
	live := liveArtifactValues(m)
	return m.ContentManifest.Validate(live)
}

// ContainsCredentials is a safety check that returns true if the manifest
// appears to contain provider credentials.  Bundles must not be shared if
// this returns true.
//
// This is a best-effort check: it looks for common credential field patterns
// in the JSON representation.
func (m *Manifest) ContainsCredentials() bool {
	data, err := json.Marshal(m)
	if err != nil {
		return false
	}
	s := strings.ToLower(string(data))
	credPatterns := []string{
		"api_key", "apikey", "secret", "token", "private_key", "privatekey",
		"password", "passphrase_override", "auth_token",
	}
	for _, pattern := range credPatterns {
		// Only flag if it appears as a JSON key (preceded by '"').
		if strings.Contains(s, `"`+pattern+`"`) {
			return true
		}
	}
	// The network passphrase is expected — it is not a credential.
	return false
}

// ── internal helpers ──────────────────────────────────────────────────────────

// computeChecksums calculates the SHA-256 digest for each bundle member.
func computeChecksums(m *Manifest) map[string]string {
	checksums := make(map[string]string)

	checksums[MemberEnvelopeXDR] = sha256Hex([]byte(m.Transaction.EnvelopeXDR))
	checksums[MemberResultMetaXDR] = sha256Hex([]byte(m.Transaction.ResultMetaXDR))
	checksums[MemberLedgerState] = sha256HexOfLedgerState(m.LedgerState)

	// Provenance and network identity are checksummed as stable JSON.
	if data, err := json.Marshal(m.Provenance); err == nil {
		checksums[MemberProvenance] = sha256Hex(data)
	}
	if data, err := json.Marshal(m.Network); err == nil {
		checksums[MemberNetwork] = sha256Hex(data)
	}

	return checksums
}

// sha256Hex returns the hex-encoded SHA-256 digest of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// sha256HexOfLedgerState computes a deterministic digest of the ledger state
// by sorting keys before hashing, so insertion order does not matter.
func sha256HexOfLedgerState(state map[string]string) string {
	if len(state) == 0 {
		return sha256Hex([]byte("{}"))
	}

	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	buf := make([]byte, 4)
	for _, k := range keys {
		writeFramed(h, buf, []byte(k))
		writeFramed(h, buf, []byte(state[k]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeFramed writes a length-prefixed byte slice into w.
func writeFramed(w interface{ Write([]byte) (int, error) }, buf []byte, data []byte) {
	n := uint32(len(data))
	buf[0] = byte(n >> 24)
	buf[1] = byte(n >> 16)
	buf[2] = byte(n >> 8)
	buf[3] = byte(n)
	_, _ = w.Write(buf)
	_, _ = w.Write(data)
}

// copyLedgerState returns a shallow copy of the ledger state map so the caller
// cannot mutate the bundle's internal state.
func copyLedgerState(src map[string]string) map[string]string {
	if src == nil {
		return make(map[string]string)
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// SuggestedFilename returns a filesystem-safe bundle filename based on the
// transaction hash and timestamp.
func SuggestedFilename(txHash string) string {
	short := txHash
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("glassbox-bundle-%s.json", short)
}
