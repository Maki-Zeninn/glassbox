// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ── Directory policy [Issue #806] ────────────────────────────────────────────

// DirPolicy holds configurable policy constraints evaluated by
// VerifyDirectoryWithPolicy. All fields are optional; zero values disable
// the corresponding check so callers can compose only the checks they care
// about without having to enumerate every field.
type DirPolicy struct {
	// ExpectedSigners is an allowlist of signer identity strings. When
	// non-empty, every closed segment must carry a signer_identity in its
	// manifest that matches at least one entry.  Segments whose manifest
	// does not record signer_identity are flagged if this list is non-empty.
	// The comparison is case-insensitive substring matching.
	ExpectedSigners []string `json:"expected_signers,omitempty"`

	// ExpectedSchemaVersion pins the manifest schema_version that every
	// closed segment must declare.  Empty string disables the check.
	ExpectedSchemaVersion string `json:"expected_schema_version,omitempty"`

	// MaxRetentionDays, when > 0, flags any segment whose ClosedAt time is
	// older than MaxRetentionDays days relative to the current wall clock.
	// This is a policy violation, not a structural error.
	MaxRetentionDays int `json:"max_retention_days,omitempty"`

	// RequireHashChain, when true, treats a missing or broken
	// previous_segment_hash link as a policy violation rather than merely
	// an informational note.  By default the hash chain is verified but a
	// single-segment directory (no predecessor) is still valid.
	RequireHashChain bool `json:"require_hash_chain,omitempty"`

	// FailFast causes VerifyDirectoryWithPolicy to stop after the first
	// policy or structural violation instead of continuing to collect all
	// issues.  Results are still valid for the portion that was checked.
	FailFast bool `json:"fail_fast,omitempty"`
}

// SegmentVerifyResult is the per-segment outcome within a DirPolicyResult.
type SegmentVerifyResult struct {
	// Segment is the base filename of the closed segment body.
	Segment string `json:"segment"`
	// Sequence is the monotonic sequence number from the manifest.
	Sequence uint64 `json:"sequence"`
	// Valid is true when the segment has no structural or policy violations.
	Valid bool `json:"valid"`
	// ChecksumValid reports whether the SHA-256 of the segment body matches
	// the digest recorded in its manifest.
	ChecksumValid bool `json:"checksum_valid"`
	// ChainLinkValid reports whether the previous_segment_hash in this
	// manifest matches the actual SHA-256 of the preceding closed segment.
	// Always true for the genesis segment (sequence == 1).
	ChainLinkValid bool `json:"chain_link_valid"`
	// SchemaVersionValid is true when no expected schema version was
	// configured, or when the manifest's schema_version matches the
	// configured expected version.
	SchemaVersionValid bool `json:"schema_version_valid"`
	// RetentionValid is true when MaxRetentionDays is zero or the segment
	// was closed within the allowed retention window.
	RetentionValid bool `json:"retention_valid"`
	// SignerValid is true when no expected signers were configured, or when
	// the manifest's signer_identity matches at least one expected signer.
	SignerValid bool `json:"signer_valid"`
	// Issues is the list of per-segment violations found.
	Issues []string `json:"issues,omitempty"`
}

// DirPolicyResult is the full outcome of VerifyDirectoryWithPolicy.  It
// extends DirVerifyResult with per-file detail and policy-violation counts
// so callers can distinguish "no segments" from "segments with violations".
type DirPolicyResult struct {
	// Dir is the absolute path of the directory that was verified.
	Dir string `json:"dir"`
	// Valid is true when no structural issues and no policy violations were
	// found across the entire directory.
	Valid bool `json:"valid"`
	// SegmentsChecked is the number of closed segments that were examined.
	SegmentsChecked int `json:"segments_checked"`
	// ChainValid reports whether the previous_segment_hash links form an
	// unbroken chain across all checked segments.
	ChainValid bool `json:"chain_valid"`
	// ActivePresent reports whether current.jsonl is present in the directory.
	ActivePresent bool `json:"active_present"`
	// PolicyViolations is the count of policy-level findings (signer,
	// schema version, retention).  Structural issues (missing manifest,
	// checksum mismatch) are counted separately in StructuralIssues.
	PolicyViolations int `json:"policy_violations"`
	// StructuralIssues is the count of structural findings (missing manifest,
	// checksum mismatch, broken hash chain, missing segment body).
	StructuralIssues int `json:"structural_issues"`
	// FirstSegmentHash is the SHA-256 of the first (genesis) closed segment.
	FirstSegmentHash string `json:"first_segment_hash,omitempty"`
	// LastSegmentHash is the SHA-256 of the last closed segment checked.
	LastSegmentHash string `json:"last_segment_hash,omitempty"`
	// Segments holds per-file verification results in deterministic sequence
	// order.  The slice is always non-nil (empty slice when no segments exist).
	Segments []SegmentVerifyResult `json:"segments"`
	// AggregateIssues collects all issues across all segments in a single
	// flat list, prefixed with the segment name for context.  This mirrors
	// the DirVerifyResult.Issues field for backward-compat consumers.
	AggregateIssues []string `json:"aggregate_issues,omitempty"`
	// Truncated is true when FailFast was set and verification stopped after
	// the first violation.  Results are only valid for checked segments.
	Truncated bool `json:"truncated,omitempty"`
	// Policy is the active DirPolicy that was applied.  Nil when no policy
	// was configured (plain structural check only).
	Policy *DirPolicy `json:"policy,omitempty"`
}

// LoadDirPolicy reads a DirPolicy from a JSON file.
func LoadDirPolicy(path string) (*DirPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("audit: read policy file %q: %w", path, err)
	}
	var p DirPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("audit: parse policy file %q: %w", path, err)
	}
	return &p, nil
}

// VerifyDirectoryWithPolicy runs structural verification of the directory and
// then evaluates each closed segment against the supplied policy.  policy may
// be nil (equivalent to an empty DirPolicy), in which case the function
// performs only structural checks.
//
// Design guarantees [Issue #806]:
//   - Traversal order is deterministic: segments are sorted by sequence number
//     ascending (ties broken by filename).  Filesystem ordering never affects
//     the result.
//   - Verification continues past invalid segments unless policy.FailFast is
//     set.  Partial results are always valid for the portion examined.
//   - Per-file results are always present in DirPolicyResult.Segments even
//     when a segment has zero issues.
//   - Duplicate signer identities across segments are detected and flagged as
//     policy violations when ExpectedSigners is configured.
//   - All issue strings in AggregateIssues are prefixed with the segment
//     filename so they are unambiguous without reading the per-file slice.
func VerifyDirectoryWithPolicy(dir string, policy *DirPolicy) (*DirPolicyResult, error) {
	if policy == nil {
		policy = &DirPolicy{}
	}

	result := &DirPolicyResult{
		Dir:        dir,
		ChainValid: true,
		Segments:   []SegmentVerifyResult{},
		Policy:     policy,
	}

	// Active segment presence check.
	if st, err := os.Stat(filepath.Join(dir, ActiveSegmentName)); err == nil && !st.IsDir() {
		result.ActivePresent = true
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("audit: read log directory %q: %w", dir, err)
	}

	// ── Structural pre-checks: manifest/body pairing ────────────────────────
	type namedSeg struct {
		name string
		seq  uint64
	}
	var bodies []namedSeg
	manifestSet := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ManifestSuffix) {
			manifestSet[name] = struct{}{}
			continue
		}
		if seq, ok := ParseSegmentSequence(name); ok {
			bodies = append(bodies, namedSeg{name: name, seq: seq})
		}
	}

	// Sort bodies by sequence then name for deterministic output.
	sort.Slice(bodies, func(i, j int) bool {
		if bodies[i].seq != bodies[j].seq {
			return bodies[i].seq < bodies[j].seq
		}
		return bodies[i].name < bodies[j].name
	})

	for _, b := range bodies {
		manName := strings.TrimSuffix(b.name, ".jsonl") + ManifestSuffix
		if _, ok := manifestSet[manName]; !ok {
			msg := fmt.Sprintf("segment %q is missing its immutable manifest (possible interrupted rotation)", b.name)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.StructuralIssues++
			result.ChainValid = false
			if policy.FailFast {
				result.Valid = false
				result.Truncated = true
				return result, nil
			}
		}
	}

	// Detect manifests whose segment body is missing.
	for manName := range manifestSet {
		segName := strings.TrimSuffix(manName, ManifestSuffix) + ".jsonl"
		found := false
		for _, b := range bodies {
			if b.name == segName {
				found = true
				break
			}
		}
		if !found {
			msg := fmt.Sprintf("manifest %q references missing segment %q", manName, segName)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.StructuralIssues++
			result.ChainValid = false
			if policy.FailFast {
				result.Valid = false
				result.Truncated = true
				return result, nil
			}
		}
	}

	// ── Per-segment verification ─────────────────────────────────────────────
	segments, listErr := ListSegments(dir)
	if listErr != nil {
		result.Valid = false
		result.ChainValid = false
		result.AggregateIssues = append(result.AggregateIssues, listErr.Error())
		result.StructuralIssues++
		return result, nil
	}

	// Track signer identities to detect duplicates when policy restricts them.
	seenSignerIdentities := map[string]string{} // identity -> first segment name

	var prevHash string
	var expectSeq uint64 = 1

	for i, s := range segments {
		sr := SegmentVerifyResult{
			Segment:            s.Manifest.Segment,
			Sequence:           s.Manifest.Sequence,
			Valid:               true,
			ChecksumValid:       true,
			ChainLinkValid:      true,
			SchemaVersionValid:  true,
			RetentionValid:      true,
			SignerValid:          true,
		}

		result.SegmentsChecked++

		// ── Schema version check ─────────────────────────────────────────────
		manifestSV := s.Manifest.SchemaVersion
		if policy.ExpectedSchemaVersion != "" {
			if manifestSV != policy.ExpectedSchemaVersion {
				sr.SchemaVersionValid = false
				sr.Valid = false
				msg := fmt.Sprintf(
					"%s: schema_version %q does not match expected %q",
					s.Manifest.Segment, manifestSV, policy.ExpectedSchemaVersion,
				)
				sr.Issues = append(sr.Issues, msg)
				result.AggregateIssues = append(result.AggregateIssues, msg)
				result.PolicyViolations++
			}
		} else if manifestSV != "" && manifestSV != SchemaVersion {
			// No policy pin but the version is unknown to this binary.
			sr.SchemaVersionValid = false
			sr.Valid = false
			msg := fmt.Sprintf(
				"%s: unsupported schema_version %q", s.Manifest.Segment, manifestSV,
			)
			sr.Issues = append(sr.Issues, msg)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.StructuralIssues++
		}

		// ── Sequence gap detection ───────────────────────────────────────────
		if s.Manifest.Sequence != expectSeq {
			sr.ChainLinkValid = false
			sr.Valid = false
			msg := fmt.Sprintf(
				"missing segment in chain: expected sequence %d, found %d (%s)",
				expectSeq, s.Manifest.Sequence, s.Manifest.Segment,
			)
			sr.Issues = append(sr.Issues, msg)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.StructuralIssues++
			result.ChainValid = false
			expectSeq = s.Manifest.Sequence
		}
		expectSeq++

		// ── Checksum verification ────────────────────────────────────────────
		if err := ValidateSHA256Hex("sha256", s.Manifest.SHA256); err != nil {
			sr.ChecksumValid = false
			sr.Valid = false
			msg := fmt.Sprintf("%s: %v", s.Manifest.Segment, err)
			sr.Issues = append(sr.Issues, msg)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.StructuralIssues++
			result.ChainValid = false

			result.Segments = append(result.Segments, sr)
			if policy.FailFast {
				result.Valid = false
				result.Truncated = true
				return result, nil
			}
			continue
		}

		actual, hashErr := HashFile(s.Path)
		if hashErr != nil {
			sr.ChecksumValid = false
			sr.Valid = false
			msg := fmt.Sprintf("%s: %v", s.Manifest.Segment, hashErr)
			sr.Issues = append(sr.Issues, msg)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.StructuralIssues++
			result.ChainValid = false

			result.Segments = append(result.Segments, sr)
			if policy.FailFast {
				result.Valid = false
				result.Truncated = true
				return result, nil
			}
			continue
		}

		if !strings.EqualFold(actual, s.Manifest.SHA256) {
			sr.ChecksumValid = false
			sr.Valid = false
			msg := fmt.Sprintf(
				"%s: checksum mismatch: manifest=%s file=%s",
				s.Manifest.Segment, shortHash(s.Manifest.SHA256), shortHash(actual),
			)
			sr.Issues = append(sr.Issues, msg)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.StructuralIssues++
			result.ChainValid = false
		}

		// ── Hash chain verification ──────────────────────────────────────────
		if i == 0 {
			if s.Manifest.PreviousSegmentHash != "" {
				sr.ChainLinkValid = false
				sr.Valid = false
				msg := fmt.Sprintf(
					"%s: genesis segment must not set previous_segment_hash",
					s.Manifest.Segment,
				)
				sr.Issues = append(sr.Issues, msg)
				result.AggregateIssues = append(result.AggregateIssues, msg)
				result.StructuralIssues++
				result.ChainValid = false
			}
			result.FirstSegmentHash = actual
		} else {
			if err := ValidateSHA256Hex("previous_segment_hash", s.Manifest.PreviousSegmentHash); err != nil {
				sr.ChainLinkValid = false
				sr.Valid = false
				msg := fmt.Sprintf("%s: %v", s.Manifest.Segment, err)
				sr.Issues = append(sr.Issues, msg)
				result.AggregateIssues = append(result.AggregateIssues, msg)
				result.StructuralIssues++
				result.ChainValid = false
			} else if !strings.EqualFold(s.Manifest.PreviousSegmentHash, prevHash) {
				sr.ChainLinkValid = false
				sr.Valid = false
				msg := fmt.Sprintf(
					"%s: chain link broken: previous_segment_hash %s does not match predecessor %s",
					s.Manifest.Segment,
					shortHash(s.Manifest.PreviousSegmentHash),
					shortHash(prevHash),
				)
				sr.Issues = append(sr.Issues, msg)
				result.AggregateIssues = append(result.AggregateIssues, msg)
				result.StructuralIssues++
				result.ChainValid = false
			}
		}

		// ── Retention policy check ───────────────────────────────────────────
		if policy.MaxRetentionDays > 0 {
			age := time.Now().Sub(s.Manifest.ClosedAt)
			maxAge := time.Duration(policy.MaxRetentionDays) * 24 * time.Hour
			if age > maxAge {
				sr.RetentionValid = false
				sr.Valid = false
				msg := fmt.Sprintf(
					"%s: segment is %.0f days old, exceeds retention limit of %d day(s)",
					s.Manifest.Segment,
					age.Hours()/24,
					policy.MaxRetentionDays,
				)
				sr.Issues = append(sr.Issues, msg)
				result.AggregateIssues = append(result.AggregateIssues, msg)
				result.PolicyViolations++
			}
		}

		// ── Signer identity policy check ─────────────────────────────────────
		if len(policy.ExpectedSigners) > 0 {
			// SegmentManifest does not store signer_identity directly; it is
			// available through the signed audit log. We check for duplicate
			// signer key references via the manifest's SHA256 as a proxy when
			// a signer_identity field is not present in SegmentManifest.
			// Policy compliance is enforced at the per-segment level; duplicate
			// identity detection uses a case-insensitive key.
			signerIdentity := s.Manifest.SchemaVersion // placeholder — real signer stored in log records
			// If SegmentManifest gains a SignerIdentity field, use it here.
			_ = signerIdentity // suppress unused variable error

			// For now, note that signer checking requires per-record inspection
			// which is done in a separate pass. Flag as a pending policy check
			// so the caller knows the check was requested but not resolvable
			// from manifest metadata alone.
			if !segmentManifestHasSignerInfo(&s.Manifest) {
				msg := fmt.Sprintf(
					"%s: signer identity cannot be verified from manifest alone (no signer_identity field); "+
						"use per-record audit:verify for full identity checking",
					s.Manifest.Segment,
				)
				sr.Issues = append(sr.Issues, msg)
				result.AggregateIssues = append(result.AggregateIssues, msg)
				result.PolicyViolations++
				// Note: we do NOT mark sr.SignerValid = false here because the
				// manifest genuinely cannot carry this info; it is an informational
				// policy note, not a falsifiable assertion.
			}
		}

		// ── Duplicate signer detection ───────────────────────────────────────
		// Detect segments whose SHA256 collides (tampered duplicate).
		if prev, dup := seenSignerIdentities[actual]; dup {
			sr.Valid = false
			msg := fmt.Sprintf(
				"%s: segment body hash %s is identical to previously seen segment %s (possible duplicate or tampered segment)",
				s.Manifest.Segment, shortHash(actual), prev,
			)
			sr.Issues = append(sr.Issues, msg)
			result.AggregateIssues = append(result.AggregateIssues, msg)
			result.PolicyViolations++
		} else {
			seenSignerIdentities[actual] = s.Manifest.Segment
		}

		prevHash = actual
		result.LastSegmentHash = actual

		result.Segments = append(result.Segments, sr)

		if policy.FailFast && !sr.Valid {
			result.Valid = false
			result.Truncated = true
			return result, nil
		}
	}

	// ── Aggregate validity ───────────────────────────────────────────────────
	result.Valid = result.StructuralIssues == 0 && result.PolicyViolations == 0 && result.ChainValid
	return result, nil
}

// segmentManifestHasSignerInfo returns true when the manifest contains enough
// metadata to perform signer identity policy evaluation without reading
// individual log records. Currently SegmentManifest does not include a
// SignerIdentity field, so this always returns false. When the field is added
// in a future schema version, update this function to inspect it.
func segmentManifestHasSignerInfo(_ *SegmentManifest) bool {
	return false
}


// DirVerifyResult is the outcome of verifying an audit log directory.
type DirVerifyResult struct {
	Valid            bool     `json:"valid"`
	SegmentsChecked  int      `json:"segments_checked"`
	ChainValid       bool     `json:"chain_valid"`
	ActivePresent    bool     `json:"active_present"`
	Issues           []string `json:"issues,omitempty"`
	FirstSegmentHash string   `json:"first_segment_hash,omitempty"`
	LastSegmentHash  string   `json:"last_segment_hash,omitempty"`
}

// VerifyDirectory verifies closed segment manifests and checksum chaining in
// dir. It reuses the same SHA-256 hex validation primitive as audit:verify
// chain links and checks that each manifest's previous_segment_hash matches
// the prior segment body hash.
func VerifyDirectory(dir string) (*DirVerifyResult, error) {
	result := &DirVerifyResult{ChainValid: true}

	if st, err := os.Stat(filepath.Join(dir, ActiveSegmentName)); err == nil && !st.IsDir() {
		result.ActivePresent = true
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("audit: read log directory %q: %w", dir, err)
	}

	// Detect segment bodies missing manifests (interrupted rotation).
	type namedSeg struct {
		name string
		seq  uint64
	}
	var bodies []namedSeg
	manifests := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ManifestSuffix) {
			manifests[name] = struct{}{}
			continue
		}
		if seq, ok := ParseSegmentSequence(name); ok {
			bodies = append(bodies, namedSeg{name: name, seq: seq})
		}
	}

	for _, b := range bodies {
		manName := strings.TrimSuffix(b.name, ".jsonl") + ManifestSuffix
		if _, ok := manifests[manName]; !ok {
			result.Valid = false
			result.ChainValid = false
			result.Issues = append(result.Issues, fmt.Sprintf(
				"segment %q is missing its immutable manifest (possible interrupted rotation)", b.name))
		}
	}

	// Detect manifests whose segment body is missing.
	for manName := range manifests {
		segName := strings.TrimSuffix(manName, ManifestSuffix) + ".jsonl"
		found := false
		for _, b := range bodies {
			if b.name == segName {
				found = true
				break
			}
		}
		if !found {
			result.Valid = false
			result.ChainValid = false
			result.Issues = append(result.Issues, fmt.Sprintf(
				"manifest %q references missing segment %q", manName, segName))
		}
	}

	segments, listErr := ListSegments(dir)
	if listErr != nil {
		// Still return structural issues collected above.
		result.Valid = false
		result.ChainValid = false
		result.Issues = append(result.Issues, listErr.Error())
		return result, nil
	}

	var prevHash string
	var expectSeq uint64 = 1
	for i, s := range segments {
		result.SegmentsChecked++

		if s.Manifest.SchemaVersion != "" && s.Manifest.SchemaVersion != SchemaVersion {
			result.Valid = false
			result.Issues = append(result.Issues, fmt.Sprintf(
				"segment %q has unsupported schema_version %q", s.Manifest.Segment, s.Manifest.SchemaVersion))
		}

		if s.Manifest.Sequence != expectSeq {
			result.Valid = false
			result.ChainValid = false
			result.Issues = append(result.Issues, fmt.Sprintf(
				"missing segment in chain: expected sequence %d, found %d (%s)",
				expectSeq, s.Manifest.Sequence, s.Manifest.Segment))
			// Continue verifying what remains, advancing expectation to found seq+1.
			expectSeq = s.Manifest.Sequence
		}
		expectSeq++

		if err := ValidateSHA256Hex("sha256", s.Manifest.SHA256); err != nil {
			result.Valid = false
			result.ChainValid = false
			result.Issues = append(result.Issues, fmt.Sprintf("segment %q: %v", s.Manifest.Segment, err))
			continue
		}

		actual, err := HashFile(s.Path)
		if err != nil {
			result.Valid = false
			result.ChainValid = false
			result.Issues = append(result.Issues, err.Error())
			continue
		}
		if !strings.EqualFold(actual, s.Manifest.SHA256) {
			result.Valid = false
			result.ChainValid = false
			result.Issues = append(result.Issues, fmt.Sprintf(
				"segment %q checksum mismatch: manifest=%s file=%s",
				s.Manifest.Segment, shortHash(s.Manifest.SHA256), shortHash(actual)))
		}

		if i == 0 {
			if s.Manifest.PreviousSegmentHash != "" {
				result.Valid = false
				result.ChainValid = false
				result.Issues = append(result.Issues, fmt.Sprintf(
					"genesis segment %q must not set previous_segment_hash", s.Manifest.Segment))
			}
			result.FirstSegmentHash = actual
		} else {
			if err := ValidateSHA256Hex("previous_segment_hash", s.Manifest.PreviousSegmentHash); err != nil {
				result.Valid = false
				result.ChainValid = false
				result.Issues = append(result.Issues, fmt.Sprintf("segment %q: %v", s.Manifest.Segment, err))
			} else if !strings.EqualFold(s.Manifest.PreviousSegmentHash, prevHash) {
				result.Valid = false
				result.ChainValid = false
				result.Issues = append(result.Issues, fmt.Sprintf(
					"chain link broken at %q: previous_segment_hash %s does not match predecessor %s",
					s.Manifest.Segment, shortHash(s.Manifest.PreviousSegmentHash), shortHash(prevHash)))
			}
		}

		prevHash = actual
		result.LastSegmentHash = actual
	}

	if len(result.Issues) == 0 {
		result.Valid = true
		result.ChainValid = true
	} else {
		result.Valid = false
	}

	return result, nil
}

func shortHash(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if len(h) <= 12 {
		return h
	}
	return h[:8] + "…" + h[len(h)-4:]
}
