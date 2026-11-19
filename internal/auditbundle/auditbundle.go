// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package auditbundle defines the portable audit bundle format used to verify
// signed audit logs on isolated machines with no network access.
//
// A portable audit bundle contains:
//   - One or more signed audit log files
//   - Public key(s) required for signature verification
//   - A bundle manifest covering every member (per-member SHA-256 hashes)
//   - The verifier version that produced the bundle
//   - Bundle-level integrity metadata
//
// Design goals:
//   - Complete self-contained verification: no KMS, HSM, RPC, or network calls.
//   - Path traversal safety: all member paths are validated before extraction.
//   - Tamper evidence: a missing or mismatched hash fails before any crypto work.
//   - Redaction: private key material and credentials are never included.
//   - Trust separation: the result distinguishes bundle integrity, signature
//     validity, and trust policy so each failure mode surfaces clearly.
package auditbundle

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FormatVersion is the on-disk format version.
const FormatVersion = 1

// VerifierVersion is the version of this verifier implementation.
const VerifierVersion = "1.0.0"

// BundleExtension is the canonical file extension for portable audit bundles.
const BundleExtension = ".auditbundle"

// Member name constants for well-known ZIP entries.
const (
	MemberLog        = "audit.log.json"
	MemberPublicKeys = "public_keys.json"
	MemberManifest   = "bundle_manifest.json"
	MemberMeta       = "bundle_meta.json"
)

// PortableBundle is the top-level structure serialised as bundle_meta.json.
type PortableBundle struct {
	FormatVersion   int       `json:"format_version"`
	VerifierVersion string    `json:"verifier_version"`
	CreatedAt       time.Time `json:"created_at"`
	GlassboxVersion string    `json:"glassbox_version"`
	Description     string    `json:"description,omitempty"`
	LogCount        int       `json:"log_count"`
	PublicKeyCount  int       `json:"public_key_count"`
}

// PublicKeyEntry records a single Ed25519 public key with its provenance.
// Private key material is never stored here.
type PublicKeyEntry struct {
	KeyID        string    `json:"key_id"`
	PublicKeyHex string    `json:"public_key_hex"`
	Algorithm    string    `json:"algorithm"`
	Source       string    `json:"source,omitempty"`
	AddedAt      time.Time `json:"added_at"`
}

// PublicKeyCatalog is the list of trusted public keys embedded in a bundle.
type PublicKeyCatalog struct {
	Keys []PublicKeyEntry `json:"keys"`
}

// BundleManifest records the SHA-256 hash of every bundle member.
type BundleManifest struct {
	ManifestVersion int               `json:"manifest_version"`
	CreatedAt       time.Time         `json:"created_at"`
	Members         map[string]string `json:"members"` // name → sha256 hex
}

// VerificationResult is the structured output of VerifyBundle.
// Three independent dimensions are reported:
//  1. BundleIntegrity  — did ZIP contents match the manifest?
//  2. SignatureValidity — do log signatures verify against the public keys?
//  3. TrustPolicy       — are the signing keys in the configured allowlist?
type VerificationResult struct {
	OK               bool             `json:"ok"`
	BundleIntegrity  IntegrityResult  `json:"bundle_integrity"`
	SignatureValidity SignatureResult  `json:"signature_validity"`
	TrustPolicy      TrustPolicyResult `json:"trust_policy"`
	Issues           []string         `json:"issues,omitempty"`
	LogsChecked      int              `json:"logs_checked"`
	VerifierVersion  string           `json:"verifier_version"`
	BundleCreatedAt  *time.Time       `json:"bundle_created_at,omitempty"`
}

// IntegrityResult records the outcome of member hash verification.
type IntegrityResult struct {
	OK            bool              `json:"ok"`
	MembersOK     int               `json:"members_ok"`
	MembersFailed int               `json:"members_failed"`
	Issues        []string          `json:"issues,omitempty"`
	Hashes        map[string]string `json:"hashes,omitempty"`
}

// SignatureResult records per-log signature verification outcomes.
type SignatureResult struct {
	OK           bool     `json:"ok"`
	ValidCount   int      `json:"valid_count"`
	InvalidCount int      `json:"invalid_count"`
	Issues       []string `json:"issues,omitempty"`
}

// TrustPolicyResult records whether each signer is in the trusted key catalog.
type TrustPolicyResult struct {
	OK             bool     `json:"ok"`
	TrustedSigners []string `json:"trusted_signers,omitempty"`
	UnknownSigners []string `json:"unknown_signers,omitempty"`
	Issues         []string `json:"issues,omitempty"`
}

// PackOptions controls bundle creation.
type PackOptions struct {
	GlassboxVersion string
	Description     string
	ExtraPublicKeys []PublicKeyEntry
}

// VerifyOptions controls how VerifyBundle operates.
type VerifyOptions struct {
	// AllowedKeyIDs restricts trust to a specific set of key IDs.
	// When empty, any key in the bundle's catalog is trusted.
	AllowedKeyIDs []string
	// RequireAllSigned, when true, fails if any log entry has no signature.
	RequireAllSigned bool
	// StrictIntegrity, when true, fails if any manifest member is absent from
	// the ZIP.
	StrictIntegrity bool
}

// SignedLogEntry is the shape of a single record inside audit.log.json.
type SignedLogEntry struct {
	TraceHash  string          `json:"trace_hash"`
	Signature  string          `json:"signature"`
	PublicKey  string          `json:"public_key"`
	Provider   string          `json:"provider"`
	Payload    json.RawMessage `json:"payload"`
	Provenance *LogProvenance  `json:"provenance,omitempty"`
}

// LogProvenance carries optional chain-linking metadata.
type LogProvenance struct {
	SignerIdentity        string `json:"signer_identity,omitempty"`
	KeyID                 string `json:"key_id,omitempty"`
	PreviousSignatureHash string `json:"previous_signature_hash,omitempty"`
}

// Pack creates a portable audit bundle ZIP at destPath from signed audit log
// entries and public keys.  No network calls are made.  Private key material
// must not appear in entries — Pack validates this before writing.
func Pack(destPath string, entries []SignedLogEntry, keys []PublicKeyEntry, opts PackOptions) error {
	if strings.TrimSpace(destPath) == "" {
		return fmt.Errorf("destination path is required")
	}
	if strings.ContainsRune(destPath, 0) {
		return fmt.Errorf("destination path contains null bytes: %q", destPath)
	}
	if len(entries) == 0 {
		return fmt.Errorf("at least one signed audit log entry is required")
	}
	for i, e := range entries {
		if err := validateNoPrivateKey(e.Payload); err != nil {
			return fmt.Errorf("entry[%d]: %w", i, err)
		}
	}

	allKeys := collectPublicKeys(entries, keys, opts.ExtraPublicKeys)
	now := time.Now().UTC()

	meta := PortableBundle{
		FormatVersion:   FormatVersion,
		VerifierVersion: VerifierVersion,
		CreatedAt:       now,
		GlassboxVersion: opts.GlassboxVersion,
		Description:     opts.Description,
		LogCount:        len(entries),
		PublicKeyCount:  len(allKeys),
	}
	metaBytes, err := marshalJSON(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal bundle meta: %w", err)
	}

	catalog := PublicKeyCatalog{Keys: allKeys}
	catalogBytes, err := marshalJSON(catalog)
	if err != nil {
		return fmt.Errorf("failed to marshal public key catalog: %w", err)
	}

	logBytes, err := marshalJSON(entries)
	if err != nil {
		return fmt.Errorf("failed to marshal audit log entries: %w", err)
	}

	manifestMembers := map[string][]byte{
		MemberMeta:       metaBytes,
		MemberPublicKeys: catalogBytes,
		MemberLog:        logBytes,
	}
	manifest := buildBundleManifest(manifestMembers, now)
	manifestBytes, err := marshalJSON(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal bundle manifest: %w", err)
	}

	// Atomic write via temp-file rename.
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("cannot create destination directory: %w", err)
	}
	tmp, err := os.CreateTemp(destDir, filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	zw := zip.NewWriter(tmp)
	for _, m := range []struct {
		name string
		data []byte
	}{
		{MemberMeta, metaBytes},
		{MemberPublicKeys, catalogBytes},
		{MemberLog, logBytes},
		{MemberManifest, manifestBytes},
	} {
		w, err := zw.Create(m.name)
		if err != nil {
			return fmt.Errorf("zip: create %s: %w", m.name, err)
		}
		if _, err := w.Write(m.data); err != nil {
			return fmt.Errorf("zip: write %s: %w", m.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("zip: close: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	ok = true
	return nil
}

// VerifyBundle opens a bundle at srcPath and performs three-phase verification:
//  1. Bundle integrity — re-hash every member, compare to manifest.
//  2. Signature validity — verify each log entry's Ed25519 signature.
//  3. Trust policy — check signing keys against AllowedKeyIDs when provided.
//
// No network calls are made at any point.  The function never falls back to a
// network source for keys, revocation, or trust anchors.
func VerifyBundle(srcPath string, opts VerifyOptions) (*VerificationResult, error) {
	result := &VerificationResult{
		VerifierVersion:  VerifierVersion,
		BundleIntegrity:  IntegrityResult{OK: true, Hashes: make(map[string]string)},
		SignatureValidity: SignatureResult{OK: true},
		TrustPolicy:      TrustPolicyResult{OK: true},
	}

	if strings.TrimSpace(srcPath) == "" {
		return nil, fmt.Errorf("bundle path is required")
	}

	// Phase 0: open ZIP and validate member names for path traversal.
	members, err := readBundleZIP(srcPath)
	if err != nil {
		return nil, err
	}

	// Require all essential members.
	for _, required := range []string{MemberMeta, MemberPublicKeys, MemberLog, MemberManifest} {
		if _, present := members[required]; !present {
			result.BundleIntegrity.OK = false
			msg := fmt.Sprintf("bundle integrity: missing required member %q", required)
			result.BundleIntegrity.Issues = append(result.BundleIntegrity.Issues, msg)
			result.Issues = append(result.Issues, msg)
		}
	}
	if !result.BundleIntegrity.OK {
		result.OK = false
		return result, nil
	}

	// Parse meta for format-version compatibility check.
	var meta PortableBundle
	if err := json.Unmarshal(members[MemberMeta], &meta); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", MemberMeta, err)
	}
	if meta.FormatVersion != FormatVersion {
		return nil, fmt.Errorf(
			"bundle format version %d is not supported (verifier supports %d); "+
				"upgrade Glassbox to verify this bundle",
			meta.FormatVersion, FormatVersion,
		)
	}
	result.BundleCreatedAt = &meta.CreatedAt

	// Phase 1: integrity verification against embedded manifest.
	var manifest BundleManifest
	if err := json.Unmarshal(members[MemberManifest], &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", MemberManifest, err)
	}
	for memberName, content := range members {
		if memberName == MemberManifest {
			continue // manifest does not hash itself
		}
		got := sha256Hex(content)
		result.BundleIntegrity.Hashes[memberName] = got
		expected, inManifest := manifest.Members[memberName]
		if !inManifest {
			if opts.StrictIntegrity {
				msg := fmt.Sprintf("bundle integrity: member %q present in ZIP but absent from manifest", memberName)
				result.BundleIntegrity.Issues = append(result.BundleIntegrity.Issues, msg)
				result.Issues = append(result.Issues, msg)
				result.BundleIntegrity.MembersFailed++
				result.BundleIntegrity.OK = false
			}
			continue
		}
		if !strings.EqualFold(got, expected) {
			msg := fmt.Sprintf("bundle integrity: member %q hash mismatch (want %s, got %s)",
				memberName, shortHash(expected), shortHash(got))
			result.BundleIntegrity.Issues = append(result.BundleIntegrity.Issues, msg)
			result.Issues = append(result.Issues, msg)
			result.BundleIntegrity.MembersFailed++
			result.BundleIntegrity.OK = false
		} else {
			result.BundleIntegrity.MembersOK++
		}
	}
	// Manifest entries whose member is absent from the ZIP.
	for memberName := range manifest.Members {
		if _, present := members[memberName]; !present {
			msg := fmt.Sprintf("bundle integrity: manifest references missing member %q", memberName)
			result.BundleIntegrity.Issues = append(result.BundleIntegrity.Issues, msg)
			result.Issues = append(result.Issues, msg)
			result.BundleIntegrity.MembersFailed++
			result.BundleIntegrity.OK = false
		}
	}
	// Stop if integrity failed — verifying signatures on tampered data is meaningless.
	if !result.BundleIntegrity.OK {
		result.OK = false
		return result, nil
	}

	// Phase 2: parse public key catalog.
	var catalog PublicKeyCatalog
	if err := json.Unmarshal(members[MemberPublicKeys], &catalog); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", MemberPublicKeys, err)
	}
	keysByHex := make(map[string]PublicKeyEntry, len(catalog.Keys))
	for _, k := range catalog.Keys {
		keysByHex[strings.ToLower(k.PublicKeyHex)] = k
	}

	// Phase 2: signature verification.
	var entries []SignedLogEntry
	if err := json.Unmarshal(members[MemberLog], &entries); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", MemberLog, err)
	}
	result.LogsChecked = len(entries)
	seenSigners := make(map[string]bool)

	for i, entry := range entries {
		if err := verifyLogEntry(i, entry, opts); err != nil {
			msg := fmt.Sprintf("signature[%d]: %v", i, err)
			result.SignatureValidity.Issues = append(result.SignatureValidity.Issues, msg)
			result.Issues = append(result.Issues, msg)
			result.SignatureValidity.InvalidCount++
			result.SignatureValidity.OK = false
		} else {
			result.SignatureValidity.ValidCount++
			if entry.PublicKey != "" {
				seenSigners[strings.ToLower(entry.PublicKey)] = true
			}
		}
	}

	// Phase 3: trust policy.
	if len(opts.AllowedKeyIDs) > 0 {
		allowedSet := make(map[string]bool, len(opts.AllowedKeyIDs))
		for _, id := range opts.AllowedKeyIDs {
			allowedSet[strings.ToLower(id)] = true
		}
		for pubKeyHex := range seenSigners {
			k, known := keysByHex[pubKeyHex]
			if !known {
				abbrev := pubKeyHex
				if len(abbrev) > 16 {
					abbrev = abbrev[:8] + "…" + abbrev[len(abbrev)-8:]
				}
				msg := fmt.Sprintf("trust policy: signing key %s is not in the bundle's key catalog", abbrev)
				result.TrustPolicy.Issues = append(result.TrustPolicy.Issues, msg)
				result.Issues = append(result.Issues, msg)
				result.TrustPolicy.UnknownSigners = append(result.TrustPolicy.UnknownSigners, pubKeyHex)
				result.TrustPolicy.OK = false
				continue
			}
			if !allowedSet[strings.ToLower(k.KeyID)] && !allowedSet[pubKeyHex] {
				msg := fmt.Sprintf("trust policy: signer key_id %q is not in the allowed key list", k.KeyID)
				result.TrustPolicy.Issues = append(result.TrustPolicy.Issues, msg)
				result.Issues = append(result.Issues, msg)
				result.TrustPolicy.UnknownSigners = append(result.TrustPolicy.UnknownSigners, k.KeyID)
				result.TrustPolicy.OK = false
			} else {
				result.TrustPolicy.TrustedSigners = append(result.TrustPolicy.TrustedSigners, k.KeyID)
			}
		}
	} else {
		for _, k := range catalog.Keys {
			if seenSigners[strings.ToLower(k.PublicKeyHex)] {
				result.TrustPolicy.TrustedSigners = append(result.TrustPolicy.TrustedSigners, k.KeyID)
			}
		}
	}

	result.OK = result.BundleIntegrity.OK &&
		result.SignatureValidity.OK &&
		result.TrustPolicy.OK
	return result, nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

func readBundleZIP(srcPath string) (map[string][]byte, error) {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open bundle %q: %w — ensure the file is a valid audit bundle",
			srcPath, err)
	}
	defer func() { _ = zr.Close() }()

	members := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		name := f.Name
		// Path-traversal guard.
		if strings.Contains(name, "..") ||
			strings.ContainsAny(name, "/\\") ||
			strings.ContainsRune(name, 0) {
			return nil, fmt.Errorf(
				"bundle %q contains unsafe member name %q — path traversal rejected",
				srcPath, name,
			)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("bundle: open member %q: %w", name, err)
		}
		data, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("bundle: read member %q: %w", name, readErr)
		}
		members[name] = data
	}
	return members, nil
}

func buildBundleManifest(members map[string][]byte, createdAt time.Time) *BundleManifest {
	m := &BundleManifest{
		ManifestVersion: 1,
		CreatedAt:       createdAt,
		Members:         make(map[string]string, len(members)),
	}
	for name, data := range members {
		m.Members[name] = sha256Hex(data)
	}
	return m
}

func verifyLogEntry(idx int, entry SignedLogEntry, opts VerifyOptions) error {
	if entry.Signature == "" {
		if opts.RequireAllSigned {
			return fmt.Errorf("entry has no signature and RequireAllSigned is set")
		}
		return nil
	}
	if entry.TraceHash == "" {
		return fmt.Errorf("trace_hash is missing")
	}
	if entry.PublicKey == "" {
		return fmt.Errorf("public_key is missing")
	}
	pubKeyBytes, err := hex.DecodeString(entry.PublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public_key (must be %d-byte Ed25519 hex): %v",
			ed25519.PublicKeySize, err)
	}
	sigBytes, err := hex.DecodeString(entry.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature (must be %d-byte Ed25519 hex): %v",
			ed25519.SignatureSize, err)
	}
	traceHashBytes, err := hex.DecodeString(entry.TraceHash)
	if err != nil || len(traceHashBytes) != 32 {
		return fmt.Errorf("invalid trace_hash (must be 32-byte SHA-256 hex): %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), traceHashBytes, sigBytes) {
		abbrev := entry.PublicKey
		if len(abbrev) > 16 {
			abbrev = abbrev[:8] + "…" + abbrev[len(abbrev)-8:]
		}
		return fmt.Errorf("signature verification FAILED for key %s", abbrev)
	}
	return nil
}

func collectPublicKeys(entries []SignedLogEntry, provided, extras []PublicKeyEntry) []PublicKeyEntry {
	seen := make(map[string]bool)
	var result []PublicKeyEntry
	add := func(keys []PublicKeyEntry) {
		for _, k := range keys {
			lo := strings.ToLower(k.PublicKeyHex)
			if seen[lo] {
				continue
			}
			seen[lo] = true
			result = append(result, k)
		}
	}
	add(provided)
	add(extras)
	for _, e := range entries {
		lo := strings.ToLower(e.PublicKey)
		if seen[lo] || e.PublicKey == "" {
			continue
		}
		seen[lo] = true
		abbrev := e.PublicKey
		if len(abbrev) > 8 {
			abbrev = abbrev[:8] + "…"
		}
		result = append(result, PublicKeyEntry{
			KeyID:        "auto:" + abbrev,
			PublicKeyHex: lo,
			Algorithm:    "ed25519",
			AddedAt:      time.Now().UTC(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].KeyID < result[j].KeyID
	})
	return result
}

func validateNoPrivateKey(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil // non-object payloads pass
	}
	danger := []string{
		"private_key", "privatekey", "private_key_hex",
		"secret_key", "seed_hex", "signing_key",
	}
	for _, d := range danger {
		if _, found := m[d]; found {
			return fmt.Errorf("payload contains field %q which may contain private key material; "+
				"remove it before packing an audit bundle", d)
		}
	}
	return nil
}

func marshalJSON(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func shortHash(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:8] + "…" + h[len(h)-8:]
}
