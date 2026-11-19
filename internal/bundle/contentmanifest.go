// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// contentmanifest.go defines the bundle content manifest: a machine-verifiable
// registry of every artifact that must be present in a complete, valid bundle.
//
// Design goals
//   - Every complete bundle has a manifest that names required artifacts,
//     their SHA-256 hash, byte-size, schema version, and redaction status.
//   - Validation runs before export completion (Save) and during import (Load).
//   - Missing or hash-modified required artifacts fail import with the precise
//     artifact path.
//   - Extra (unlisted) artifacts are either classified as extensions (allowed
//     when tagged) or rejected.
//
// Relationship to internal/manifest
//   The release-manifest package (internal/manifest) handles signed release
//   artifacts distributed to end-users.  ContentManifest is strictly a
//   bundle-internal integrity record reusing the same SHA-256 + JSON patterns
//   but with bundle-specific semantics (required vs optional, redaction flags,
//   schema versions per member).
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ContentManifestVersion is the current version of the ContentManifest schema.
const ContentManifestVersion = 1

// ArtifactRole classifies how a bundle artifact is treated during validation.
type ArtifactRole string

const (
	// RoleRequired means the artifact must be present and hash-correct;
	// absence or mutation causes import to fail.
	RoleRequired ArtifactRole = "required"
	// RoleOptional means the artifact improves fidelity (e.g. source map) but
	// its absence produces a warning rather than an error.
	RoleOptional ArtifactRole = "optional"
	// RoleExtension means the artifact is not part of the standard schema but
	// has been explicitly registered as a safe addition by the producer.
	RoleExtension ArtifactRole = "extension"
)

// ArtifactEntry records one member of a bundle in the ContentManifest.
type ArtifactEntry struct {
	// LogicalPath is the stable, platform-independent identifier for this
	// member within the bundle (e.g. "transaction.envelope_xdr").
	// It matches the JSON field path used in Manifest.
	LogicalPath string `json:"logical_path"`
	// Role controls how missing/modified entries are treated on import.
	Role ArtifactRole `json:"role"`
	// SHA256 is the lowercase hex SHA-256 digest of the artifact value.
	// For string fields this is the hash of the UTF-8 bytes.
	// For the ledger_state map it uses the same length-framed algorithm as
	// bundle.computeChecksums so results are directly comparable.
	SHA256 string `json:"sha256"`
	// SizeBytes is the byte size of the artifact value.
	SizeBytes int64 `json:"size_bytes"`
	// SchemaVersion is the version of the member's own format, when applicable.
	// Zero means "not versioned independently".
	SchemaVersion int `json:"schema_version,omitempty"`
	// Redacted is true when the artifact value has been intentionally
	// omitted or replaced with a placeholder (e.g. for privacy/security).
	// Redacted artifacts still appear in the manifest but their hash is not
	// verified.
	Redacted bool `json:"redacted,omitempty"`
	// Description is an optional human-readable note about the artifact.
	Description string `json:"description,omitempty"`
}

// ContentManifest is an ordered list of artifact entries that fully describes
// the expected contents of a bundle.  It is embedded in every Manifest under
// ContentManifest.
type ContentManifest struct {
	// Version is the ContentManifest schema version.
	Version int `json:"version"`
	// Artifacts is the ordered list of expected bundle members.
	Artifacts []ArtifactEntry `json:"artifacts"`
}

// ContentManifestValidationError is returned when one or more artifact
// entries fail validation.
type ContentManifestValidationError struct {
	// Missing lists required artifacts that are absent from the bundle.
	Missing []string
	// Modified lists artifacts whose stored hash does not match the
	// re-computed value.
	Modified []string
	// Unlisted lists artifact keys present in the bundle but absent from the
	// content manifest and not tagged as extensions.
	Unlisted []string
}

func (e *ContentManifestValidationError) Error() string {
	var parts []string
	if len(e.Missing) > 0 {
		parts = append(parts, fmt.Sprintf("missing required artifact(s): %s",
			strings.Join(e.Missing, ", ")))
	}
	if len(e.Modified) > 0 {
		parts = append(parts, fmt.Sprintf("modified artifact(s): %s",
			strings.Join(e.Modified, ", ")))
	}
	if len(e.Unlisted) > 0 {
		parts = append(parts, fmt.Sprintf("unlisted artifact(s) (not classified as extensions): %s",
			strings.Join(e.Unlisted, ", ")))
	}
	return "bundle content manifest validation failed: " + strings.Join(parts, "; ")
}

// IsContentManifestError reports whether err is a *ContentManifestValidationError.
func IsContentManifestError(err error) bool {
	_, ok := err.(*ContentManifestValidationError)
	return ok
}

// ContentManifestWarning is a non-fatal diagnostic produced when optional
// artifacts are absent or when the content manifest itself is missing from a
// legacy bundle.
type ContentManifestWarning struct {
	// MissingOptional lists optional artifact paths that are absent.
	MissingOptional []string
	// LegacyBundle is true when the bundle predates the content manifest
	// and no manifest field is present.
	LegacyBundle bool
}

func (w *ContentManifestWarning) Error() string {
	if w.LegacyBundle {
		return "bundle predates content manifest; integrity verified via checksums only"
	}
	if len(w.MissingOptional) > 0 {
		return fmt.Sprintf("missing optional artifact(s): %s",
			strings.Join(w.MissingOptional, ", "))
	}
	return ""
}

// HasWarnings returns true when there is anything to warn about.
func (w *ContentManifestWarning) HasWarnings() bool {
	return w != nil && (w.LegacyBundle || len(w.MissingOptional) > 0)
}

// ── standard artifact paths ───────────────────────────────────────────────────

// These constants are the canonical LogicalPath values for the built-in
// bundle members.  Producers and consumers MUST use these strings.
const (
	ArtifactEnvelopeXDR   = "transaction.envelope_xdr"
	ArtifactResultMetaXDR = "transaction.result_meta_xdr"
	ArtifactLedgerState   = "ledger_state"
	ArtifactProvenance    = "provenance"
	ArtifactNetwork       = "network"
	// ArtifactSourceMap is optional; its presence improves stack-trace
	// resolution but is not required for replay.
	ArtifactSourceMap = "source_map"
	// ArtifactTrace is optional; a recorded execution trace for diagnostic replay.
	ArtifactTrace = "trace"
	// ArtifactSignature is optional; a detached Ed25519 signature over the bundle.
	ArtifactSignature = "signature"
)

// defaultContentManifestEntries returns the canonical ordered list of entries
// for a standard bundle.  Optional artifacts are included with zero hashes;
// BuildContentManifest fills them in from live data.
var defaultContentManifestEntries = []struct {
	path        string
	role        ArtifactRole
	schemaVer   int
	description string
}{
	{ArtifactEnvelopeXDR, RoleRequired, 0, "Transaction envelope (XDR)"},
	{ArtifactResultMetaXDR, RoleRequired, 0, "Transaction result metadata (XDR)"},
	{ArtifactLedgerState, RoleRequired, 0, "Ledger state snapshot (sorted key→entry map)"},
	{ArtifactProvenance, RoleRequired, 0, "Bundle provenance metadata"},
	{ArtifactNetwork, RoleRequired, 0, "Stellar network identity"},
	{ArtifactTrace, RoleOptional, 0, "Execution trace (if captured)"},
	{ArtifactSourceMap, RoleOptional, 0, "Source map manifest (if present)"},
	{ArtifactSignature, RoleOptional, 0, "Detached bundle signature (if signed)"},
}

// BuildContentManifest constructs a ContentManifest for m by computing
// the hash and size of each standard artifact.  Extra keys in extras are
// appended as RoleExtension entries so they are validated but not rejected.
func BuildContentManifest(m *Manifest, extras map[string]string) *ContentManifest {
	cm := &ContentManifest{Version: ContentManifestVersion}

	// Compute live values for each default entry.
	liveValues := liveArtifactValues(m)

	for _, def := range defaultContentManifestEntries {
		entry := ArtifactEntry{
			LogicalPath:   def.path,
			Role:          def.role,
			SchemaVersion: def.schemaVer,
			Description:   def.description,
		}

		val, present := liveValues[def.path]
		if present && val != "" {
			h, sz := hashAndSize([]byte(val))
			entry.SHA256 = h
			entry.SizeBytes = sz
		} else if def.role == RoleRequired {
			// Required artifact is missing from the manifest data; hash is
			// set to the empty-string hash as a placeholder — Validate will
			// catch this.
			h, sz := hashAndSize([]byte(""))
			entry.SHA256 = h
			entry.SizeBytes = sz
		}
		// Optional/extension entries with no data get zero SHA256.

		cm.Artifacts = append(cm.Artifacts, entry)
	}

	// Append extension entries.
	if len(extras) > 0 {
		// Sort for determinism.
		keys := make([]string, 0, len(extras))
		for k := range extras {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := extras[k]
			h, sz := hashAndSize([]byte(v))
			cm.Artifacts = append(cm.Artifacts, ArtifactEntry{
				LogicalPath: k,
				Role:        RoleExtension,
				SHA256:      h,
				SizeBytes:   sz,
			})
		}
	}

	return cm
}

// Validate checks that all required artifacts are present and unmodified,
// optional artifacts are noted as warnings, and no unlisted artifacts exist.
//
// live is a map of logical-path → current artifact value derived from a live
// bundle.  extra keys in live that are absent from cm.Artifacts and not
// classified as extensions are reported as Unlisted.
//
// Returns (nil, nil) when everything is valid.
// Returns (nil, *ContentManifestWarning) when only optional artifacts are missing.
// Returns (*ContentManifestValidationError, nil) when required/modified/unlisted
// errors are found.
// Returns both non-nil when there are both hard errors and warnings.
func (cm *ContentManifest) Validate(live map[string]string) (*ContentManifestValidationError, *ContentManifestWarning) {
	if cm == nil || len(cm.Artifacts) == 0 {
		return nil, &ContentManifestWarning{LegacyBundle: true}
	}

	var (
		missing   []string
		modified  []string
		unlisted  []string
		missingOpt []string
	)

	// Build a set of known artifact paths for unlisted-check.
	knownPaths := make(map[string]struct{}, len(cm.Artifacts))
	for _, a := range cm.Artifacts {
		knownPaths[a.LogicalPath] = struct{}{}
	}

	// Validate each registered artifact.
	for _, entry := range cm.Artifacts {
		if entry.Redacted {
			continue // explicitly redacted; skip hash check
		}

		val, present := live[entry.LogicalPath]
		if !present || val == "" {
			switch entry.Role {
			case RoleRequired:
				missing = append(missing, entry.LogicalPath)
			case RoleOptional:
				missingOpt = append(missingOpt, entry.LogicalPath)
			case RoleExtension:
				// Extensions declared in the manifest but absent from live
				// data are treated like optional artifacts.
				missingOpt = append(missingOpt, entry.LogicalPath)
			}
			continue
		}

		// Hash check — only when entry has a stored hash.
		if entry.SHA256 != "" {
			liveHash, _ := hashAndSize([]byte(val))
			if !strings.EqualFold(liveHash, entry.SHA256) {
				modified = append(modified, entry.LogicalPath)
			}
		}
	}

	// Check for unlisted keys in the live map.
	for path := range live {
		if _, known := knownPaths[path]; !known {
			unlisted = append(unlisted, path)
		}
	}
	sort.Strings(missing)
	sort.Strings(modified)
	sort.Strings(unlisted)
	sort.Strings(missingOpt)

	var hardErr *ContentManifestValidationError
	if len(missing) > 0 || len(modified) > 0 || len(unlisted) > 0 {
		hardErr = &ContentManifestValidationError{
			Missing:  missing,
			Modified: modified,
			Unlisted: unlisted,
		}
	}

	var warn *ContentManifestWarning
	if len(missingOpt) > 0 {
		warn = &ContentManifestWarning{MissingOptional: missingOpt}
	}

	return hardErr, warn
}

// ── helpers ───────────────────────────────────────────────────────────────────

// liveArtifactValues extracts the current string representation of each
// standard bundle artifact so it can be hashed.  The ledger state uses the
// same canonical JSON encoding as computeChecksums.
func liveArtifactValues(m *Manifest) map[string]string {
	vals := make(map[string]string, 8)

	vals[ArtifactEnvelopeXDR] = m.Transaction.EnvelopeXDR
	vals[ArtifactResultMetaXDR] = m.Transaction.ResultMetaXDR

	// Ledger state: use the hex of the length-framed sorted hash so the value
	// is deterministic regardless of map iteration order.
	vals[ArtifactLedgerState] = sha256HexOfLedgerState(m.LedgerState)

	if data, err := json.Marshal(m.Provenance); err == nil {
		vals[ArtifactProvenance] = string(data)
	}
	if data, err := json.Marshal(m.Network); err == nil {
		vals[ArtifactNetwork] = string(data)
	}

	// Optional fields — populated when present.
	if m.TraceData != "" {
		vals[ArtifactTrace] = m.TraceData
	}
	if m.SourceMapRef != "" {
		vals[ArtifactSourceMap] = m.SourceMapRef
	}
	if m.Signature != "" {
		vals[ArtifactSignature] = m.Signature
	}

	return vals
}

// hashAndSize returns the lowercase hex SHA-256 digest and byte-length of data.
func hashAndSize(data []byte) (string, int64) {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), int64(len(data))
}
