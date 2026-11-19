// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ManifestVersion tracks the manifest format. Bump it whenever the hash
// algorithm or the meaning of an existing member name changes.
const ManifestVersion = 1

// RedactionManifest is embedded in the archive manifest to describe which
// field categories were processed during export. It records counts and
// category names — never the sensitive values themselves — so the recipient
// can see what was removed or pseudonymized without re-exposing the data.
type RedactionManifest struct {
	// Profile is the redaction profile name applied (e.g. "strict").
	Profile string `json:"profile"`
	// Categories lists each field class that had the policy applied.
	Categories []RedactionCategoryEntry `json:"categories"`
	// IdentifiersPseudonymized is the total count of unique identifiers
	// that were pseudonymized across all JSON fields.
	IdentifiersPseudonymized int `json:"identifiers_pseudonymized,omitempty"`
}

// RedactionCategoryEntry records one field class in the redaction manifest.
type RedactionCategoryEntry struct {
	// Name is the logical field class (e.g. "env_metadata").
	Name string `json:"name"`
	// Policy is the treatment that was applied ("redact", "pseudonymize", "keep").
	Policy string `json:"policy"`
	// Applied is true when the policy actually changed something in this export.
	Applied bool `json:"applied"`
	// Count is the number of distinct values affected (where applicable).
	Count int `json:"count,omitempty"`
}

// CanonicalManifestMembers lists every artifact a session archive manifest
// can cover. A member absent from a given session is simply omitted from
// Manifest.Members rather than recorded with an empty hash.
var CanonicalManifestMembers = []string{"trace", "bundle", "source_map", "annotations", "metadata"}

// Manifest records a SHA-256 hash for every embedded artifact in a session
// archive so the archive's contents — and the relationships between its
// members — can be verified byte-for-byte on import.
type Manifest struct {
	ManifestVersion int               `json:"manifest_version"`
	SchemaVersion   int               `json:"schema_version"`
	CreatedAt       string            `json:"created_at"`
	Members         map[string]string `json:"members"` // canonical member name -> sha256 hex digest
	// Redaction is set when the archive was produced with a redaction profile
	// other than "full". It records the categories that were removed or
	// pseudonymized without retaining any sensitive values. This field is
	// nil for archives exported with the default "full" (unredacted) profile.
	Redaction *RedactionManifest `json:"redaction,omitempty"`
}

// hashBytes returns the lowercase hex-encoded SHA-256 digest of b.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// BuildManifest computes a canonical manifest over the supplied members,
// keyed by canonical member name (see CanonicalManifestMembers). Only
// non-nil entries are recorded so that sessions without optional artifacts
// (trace, bundle, source_map, annotations) don't carry misleading zero
// hashes.
func BuildManifest(members map[string][]byte, schemaVersion int, createdAt time.Time) *Manifest {
	return BuildManifestWithRedaction(members, schemaVersion, createdAt, nil)
}

// BuildManifestWithRedaction is like BuildManifest but also embeds a
// RedactionManifest when report is non-nil and its profile is not "full".
// This allows archive recipients to inspect what was sanitised before sharing
// without the manifest retaining any of the removed values.
func BuildManifestWithRedaction(members map[string][]byte, schemaVersion int, createdAt time.Time, report *RedactionReport) *Manifest {
	m := &Manifest{
		ManifestVersion: ManifestVersion,
		SchemaVersion:   schemaVersion,
		CreatedAt:       createdAt.UTC().Format(time.RFC3339),
		Members:         make(map[string]string, len(members)),
	}
	for name, content := range members {
		if content == nil {
			continue
		}
		m.Members[name] = hashBytes(content)
	}
	if report != nil && report.Profile != RedactionFull {
		m.Redaction = buildRedactionManifest(report)
	}
	return m
}

// buildRedactionManifest converts a RedactionReport into the compact
// RedactionManifest that is stored in the archive. Sensitive values are
// never copied — only counts and category names are recorded.
func buildRedactionManifest(report *RedactionReport) *RedactionManifest {
	if report == nil {
		return nil
	}
	rm := &RedactionManifest{
		Profile:                  string(report.Profile),
		IdentifiersPseudonymized: report.IdentifiersPseudonymized,
	}
	for _, f := range report.Fields {
		entry := RedactionCategoryEntry{
			Name:    f.Field,
			Policy:  string(f.Policy),
			Applied: f.Applied,
		}
		if f.Field == FieldAccountIdentifiers && f.Applied {
			entry.Count = report.IdentifiersPseudonymized
		}
		rm.Categories = append(rm.Categories, entry)
	}
	return rm
}

// ManifestIssue describes a single mismatch found while verifying a
// manifest against the artifacts actually present in an archive.
type ManifestIssue struct {
	// Member is the canonical member name the issue applies to.
	Member string
	// Description explains what is wrong.
	Description string
	// Hint is an actionable remediation suggestion.
	Hint string
}

// ManifestReport is the result of verifying an archive's members against
// its manifest.
type ManifestReport struct {
	// OK is true when no issues were found.
	OK bool
	// Issues lists every mismatch, missing member, or unexpected member.
	Issues []ManifestIssue
	// Compatible is false when the archive predates manifests entirely (no
	// manifest.json present). Old archives are accepted without hash
	// verification for backward compatibility — see VerifyManifest.
	Compatible bool
}

// VerifyManifest checks that every member present in the archive matches
// the hash recorded in the manifest, and flags any manifest entry whose
// member is missing from the archive (or vice versa).
//
// manifest may be nil, indicating an archive produced before manifests
// existed. This is the documented compatibility path for old sessions:
// VerifyManifest reports Compatible=false but OK=true, so loading such an
// archive is never blocked by the absence of a manifest.
func VerifyManifest(manifest *Manifest, members map[string][]byte) *ManifestReport {
	report := &ManifestReport{Compatible: true}

	if manifest == nil {
		report.OK = true
		report.Compatible = false
		return report
	}

	seen := make(map[string]bool, len(members))
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		seen[name] = true
		want, ok := manifest.Members[name]
		if !ok {
			report.Issues = append(report.Issues, ManifestIssue{
				Member:      name,
				Description: fmt.Sprintf("member %q is present in the archive but missing from the manifest", name),
				Hint:        "Re-export the archive with 'glassbox session share' to regenerate a matching manifest.",
			})
			continue
		}
		got := hashBytes(members[name])
		if !strings.EqualFold(got, want) {
			report.Issues = append(report.Issues, ManifestIssue{
				Member:      name,
				Description: fmt.Sprintf("member %q hash mismatch: expected %s, got %s", name, want, got),
				Hint:        "This artifact was altered after the archive was created. Re-export a fresh archive from the original session.",
			})
		}
	}

	manifestNames := make([]string, 0, len(manifest.Members))
	for name := range manifest.Members {
		manifestNames = append(manifestNames, name)
	}
	sort.Strings(manifestNames)
	for _, name := range manifestNames {
		if !seen[name] {
			report.Issues = append(report.Issues, ManifestIssue{
				Member:      name,
				Description: fmt.Sprintf("member %q is recorded in the manifest but missing from the archive", name),
				Hint:        "The archive is incomplete or was tampered with. Re-export it with 'glassbox session share'.",
			})
		}
	}

	report.OK = len(report.Issues) == 0
	return report
}

// FormatManifestIssues renders a ManifestReport's issues in the same
// numbered "[Field] Description / Hint" format used elsewhere in this
// package (see ValidateIntegrity), so manifest failures read consistently
// with other session diagnostics.
func FormatManifestIssues(report *ManifestReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("session archive failed manifest verification (%d issue(s)):\n", len(report.Issues)))
	for i, issue := range report.Issues {
		sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, issue.Member, issue.Description))
		if issue.Hint != "" {
			sb.WriteString(fmt.Sprintf("     Hint: %s\n", issue.Hint))
		}
	}
	return sb.String()
}
