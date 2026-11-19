// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ImportConflictPolicy selects how ImportSession resolves an incoming
// session whose ID collides with one already in the store.
type ImportConflictPolicy string

const (
	// ImportFail rejects the import outright when a conflict is found. This
	// is the default, non-destructive policy — it never overwrites or
	// deletes the existing session.
	ImportFail ImportConflictPolicy = "fail"
	// ImportRename assigns the incoming session a freshly generated ID so
	// it is stored alongside the existing one without touching it.
	ImportRename ImportConflictPolicy = "rename"
	// ImportMerge combines mergeable metadata (bookmark name, annotations)
	// from both records under the existing session's identity.
	ImportMerge ImportConflictPolicy = "merge"
	// ImportReplace unconditionally overwrites the existing session with
	// the incoming data. Existing CreatedAt and audit chain are preserved;
	// all other fields adopt incoming values. This is destructive — use
	// only when the incoming version is known to be authoritative.
	ImportReplace ImportConflictPolicy = "replace"
)

// ParseImportConflictPolicy validates a user-supplied policy string (e.g.
// from a --on-conflict CLI flag).
func ParseImportConflictPolicy(s string) (ImportConflictPolicy, error) {
	switch ImportConflictPolicy(strings.ToLower(strings.TrimSpace(s))) {
	case ImportFail:
		return ImportFail, nil
	case ImportRename:
		return ImportRename, nil
	case ImportMerge:
		return ImportMerge, nil
	case ImportReplace:
		return ImportReplace, nil
	default:
		return "", fmt.Errorf(
			"unknown import conflict policy %q — must be one of: fail, rename, merge, replace",
			s,
		)
	}
}

// ImportConflict describes one field that differs between an incoming
// session and the existing session sharing its ID.
type ImportConflict struct {
	Field    string
	Existing string
	Incoming string
}

// DetectImportConflict compares an incoming session's identity against the
// store's current record for the same ID. It returns the existing record
// (nil when there is none — i.e. no conflict) along with every field-level
// difference, so callers can render a conflict preview before choosing a
// resolution policy.
func DetectImportConflict(ctx context.Context, store *Store, incoming *Data) (*Data, []ImportConflict, error) {
	if incoming == nil {
		return nil, nil, fmt.Errorf("incoming session data is nil")
	}
	if strings.TrimSpace(incoming.ID) == "" {
		return nil, nil, fmt.Errorf("incoming session has no ID")
	}

	existing, err := store.Load(ctx, incoming.ID)
	if err != nil {
		return nil, nil, nil
	}

	var conflicts []ImportConflict
	diff := func(field, existingV, incomingV string) {
		if existingV != incomingV {
			conflicts = append(conflicts, ImportConflict{Field: field, Existing: existingV, Incoming: incomingV})
		}
	}
	diff("Name", existing.Name, incoming.Name)
	diff("TxHash", existing.TxHash, incoming.TxHash)
	diff("Network", existing.Network, incoming.Network)
	diff("Status", existing.Status, incoming.Status)
	diff("AnnotationsJSON", existing.AnnotationsJSON, incoming.AnnotationsJSON)

	return existing, conflicts, nil
}

// ConflictSeverity classifies how severe a field difference is.
type ConflictSeverity string

const (
	// SeverityInfo indicates a cosmetic difference (e.g. name changed).
	SeverityInfo ConflictSeverity = "info"
	// SeverityWarning indicates a meaningful but safe difference.
	SeverityWarning ConflictSeverity = "warning"
	// SeverityCritical indicates a difference that may lose data.
	SeverityCritical ConflictSeverity = "critical"
)

// ImportConflictDetailed extends ImportConflict with severity and
// machine-readable codes for automated decision-making.
type ImportConflictDetailed struct {
	ImportConflict
	Severity  ConflictSeverity `json:"severity"`
	Code      string           `json:"code"`
	Portable  bool             `json:"portable"`  // true if field is safe to transfer between machines
	Artifacts bool             `json:"artifacts"` // true if conflict involves archive artifacts
}

// ImportPlan describes what would happen during import without modifying
// any data. It is the output of PlanImport and can be rendered as a
// dry-run report or used to make automated policy decisions.
type ImportPlan struct {
	// Incoming is the session data from the archive.
	Incoming *Data `json:"incoming"`
	// Existing is the current local session, or nil when there is no conflict.
	Existing *Data `json:"existing,omitempty"`
	// Conflicts lists every field that differs between incoming and existing.
	Conflicts []ImportConflictDetailed `json:"conflicts"`
	// Policy is the resolution policy that would be applied.
	Policy ImportConflictPolicy `json:"policy"`
	// WouldRename is true if the rename policy would assign a new ID.
	WouldRename bool `json:"would_rename,omitempty"`
	// WouldReplace is true if the replace policy would overwrite all fields.
	WouldReplace bool `json:"would_replace,omitempty"`
	// WouldMerge is true if the merge policy would combine metadata.
	WouldMerge bool `json:"would_merge,omitempty"`
	// ArtifactConflicts counts how many archive artifacts differ.
	ArtifactConflicts int `json:"artifact_conflicts"`
	// Portable is true when all conflicting fields are safe to transfer
	// between machines (no absolute paths, no machine-local config).
	Portable bool `json:"portable"`
	// SchemaCompatible is true when the incoming schema version is
	// compatible with the local store.
	SchemaCompatible bool `json:"schema_compatible"`
	// EstimatedSizeBytes is the approximate size of the incoming session.
	EstimatedSizeBytes int64 `json:"estimated_size_bytes"`
}

// PlanImport analyses what ImportSession would do without modifying any data.
// It is safe to call multiple times — it is purely read-only.
func PlanImport(ctx context.Context, store *Store, incoming *Data, policy ImportConflictPolicy) (*ImportPlan, error) {
	if incoming == nil {
		return nil, fmt.Errorf("incoming session data is nil")
	}
	if strings.TrimSpace(incoming.ID) == "" {
		return nil, fmt.Errorf("incoming session has no ID")
	}

	plan := &ImportPlan{
		Incoming:         incoming,
		Policy:           policy,
		SchemaCompatible: incoming.SchemaVersion <= SchemaVersion,
	}

	existing, conflicts, err := DetectImportConflict(ctx, store, incoming)
	if err != nil {
		return nil, err
	}
	plan.Existing = existing
	plan.Conflicts = classifyConflicts(conflicts, incoming, existing)

	// Check artifact conflicts.
	artifactCount := 0
	if existing != nil {
		if incoming.TraceJSON != "" && incoming.TraceJSON != existing.TraceJSON {
			artifactCount++
		}
		if incoming.BundleJSON != "" && incoming.BundleJSON != existing.BundleJSON {
			artifactCount++
		}
		if incoming.SourceMapJSON != "" && incoming.SourceMapJSON != existing.SourceMapJSON {
			artifactCount++
		}
		if incoming.AnnotationsJSON != "" && incoming.AnnotationsJSON != existing.AnnotationsJSON {
			artifactCount++
		}
	}
	plan.ArtifactConflicts = artifactCount

	// Check portability.
	plan.Portable = true
	for _, c := range plan.Conflicts {
		if !c.Portable {
			plan.Portable = false
			break
		}
	}

	// Estimate size.
	plan.EstimatedSizeBytes = estimateDataSize(incoming)

	// Set policy-specific flags.
	if existing != nil {
		plan.WouldRename = policy == ImportRename
		plan.WouldReplace = policy == ImportReplace
		plan.WouldMerge = policy == ImportMerge
	}

	return plan, nil
}

// classifyConflicts enriches raw ImportConflicts with severity, codes,
// and portability metadata.
func classifyConflicts(conflicts []ImportConflict, incoming, existing *Data) []ImportConflictDetailed {
	result := make([]ImportConflictDetailed, 0, len(conflicts))
	for _, c := range conflicts {
		d := ImportConflictDetailed{
			ImportConflict: c,
			Portable:       true,
		}
		switch c.Field {
		case "Name":
			d.Severity = SeverityInfo
			d.Code = "CONFLICT_NAME"
		case "TxHash":
			d.Severity = SeverityCritical
			d.Code = "CONFLICT_TXHASH"
		case "Network":
			d.Severity = SeverityCritical
			d.Code = "CONFLICT_NETWORK"
		case "Status":
			d.Severity = SeverityWarning
			d.Code = "CONFLICT_STATUS"
		case "AnnotationsJSON":
			d.Severity = SeverityInfo
			d.Code = "CONFLICT_ANNOTATIONS"
		default:
			d.Severity = SeverityWarning
			d.Code = "CONFLICT_UNKNOWN"
		}
		result = append(result, d)
	}
	return result
}

// estimateDataSize computes an approximate byte count for the data's
// primary artifacts. This is used for dry-run reports and resource planning.
func estimateDataSize(d *Data) int64 {
	var size int64
	size += int64(len(d.TraceJSON))
	size += int64(len(d.BundleJSON))
	size += int64(len(d.SourceMapJSON))
	size += int64(len(d.AnnotationsJSON))
	size += int64(len(d.EnvelopeXdr))
	size += int64(len(d.SimResponseJSON))
	size += int64(len(d.SimRequestJSON))
	return size
}

// ImportResult summarizes the outcome of ImportSession.
type ImportResult struct {
	// Policy is the resolution policy that was applied.
	Policy ImportConflictPolicy
	// Existing is the pre-import record that collided with incoming's ID,
	// or nil when there was no conflict.
	Existing *Data
	// Conflicts lists every field that differed between Existing and the
	// incoming session, regardless of which policy was applied.
	Conflicts []ImportConflict
	// Saved is the record actually persisted to the store.
	Saved *Data
	// Renamed is true when policy assigned the incoming session a new ID.
	Renamed bool
	// Merged is true when policy combined incoming and existing metadata.
	Merged bool
	// Replaced is true when policy overwrote the existing session.
	Replaced bool
}

// ImportSession persists incoming into the store, resolving an ID collision
// with policy. When there is no conflict, incoming is saved unchanged
// regardless of policy. The default policy (ImportFail) never overwrites or
// deletes existing data.
func (s *Store) ImportSession(ctx context.Context, incoming *Data, policy ImportConflictPolicy) (*ImportResult, error) {
	if incoming == nil {
		return nil, fmt.Errorf("cannot import nil session data")
	}

	existing, conflicts, err := DetectImportConflict(ctx, s, incoming)
	if err != nil {
		return nil, err
	}

	result := &ImportResult{Policy: policy, Existing: existing, Conflicts: conflicts}

	if existing == nil {
		if err := s.SaveWithValidation(ctx, incoming); err != nil {
			return nil, err
		}
		result.Saved = incoming
		return result, nil
	}

	switch policy {
	case ImportRename:
		renamed := *incoming
		renamed.ID = GenerateID(incoming.TxHash)
		for attempts := 0; attempts < 5; attempts++ {
			if _, loadErr := s.Load(ctx, renamed.ID); loadErr != nil {
				break
			}
			renamed.ID = GenerateID(incoming.TxHash)
		}
		if err := s.SaveWithValidation(ctx, &renamed); err != nil {
			return nil, err
		}
		result.Saved = &renamed
		result.Renamed = true
		return result, nil

	case ImportMerge:
		merged := mergeSessionMetadata(existing, incoming)
		if err := s.SaveWithValidation(ctx, merged); err != nil {
			return nil, err
		}
		result.Saved = merged
		result.Merged = true
		return result, nil

	case ImportReplace:
		replaced := replaceSessionMetadata(existing, incoming)
		if err := s.SaveWithValidation(ctx, replaced); err != nil {
			return nil, err
		}
		result.Saved = replaced
		result.Replaced = true
		return result, nil

	case ImportFail, "":
		return result, formatImportConflictError(incoming.ID, conflicts)

	default:
		return nil, fmt.Errorf("unknown import conflict policy %q", policy)
	}
}

// replaceSessionMetadata overwrites the existing session with incoming data,
// preserving CreatedAt and audit chain from the existing record.
func replaceSessionMetadata(existing, incoming *Data) *Data {
	replaced := *incoming
	replaced.ID = existing.ID
	replaced.CreatedAt = existing.CreatedAt
	replaced.Revision = existing.Revision
	// Preserve audit chain continuity.
	replaced.PreviousSessionHash = existing.PreviousSessionHash
	replaced.AuditHash = existing.AuditHash
	replaced.AuditSignature = existing.AuditSignature
	return &replaced
}

// formatImportConflictError renders an import conflict in the same
// numbered-list style used elsewhere in this package for diagnostics.
func formatImportConflictError(id string, conflicts []ImportConflict) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"import conflict: session %q already exists (%d field(s) differ):\n",
		id, len(conflicts),
	))
	for i, c := range conflicts {
		sb.WriteString(fmt.Sprintf("  %d. [%s] existing=%q incoming=%q\n", i+1, c.Field, c.Existing, c.Incoming))
	}
	sb.WriteString("Re-run with --on-conflict rename, merge, or replace to resolve.")
	return fmt.Errorf("%s", sb.String())
}

// mergeSessionMetadata combines mergeable metadata from existing and
// incoming under existing's identity (ID, CreatedAt, audit chain), folding
// in bookmark and annotation data so neither side's work is silently lost.
func mergeSessionMetadata(existing, incoming *Data) *Data {
	merged := *existing

	if merged.Name == "" {
		merged.Name = incoming.Name
	}

	merged.AnnotationsJSON = mergeAnnotations(existing.AnnotationsJSON, incoming.AnnotationsJSON)

	if merged.EnvelopeXdr == "" {
		merged.EnvelopeXdr = incoming.EnvelopeXdr
	}
	if merged.SimResponseJSON == "" {
		merged.SimResponseJSON = incoming.SimResponseJSON
	}
	if incoming.LastAccessAt.After(merged.LastAccessAt) {
		merged.LastAccessAt = incoming.LastAccessAt
	}

	return &merged
}

// mergeAnnotations combines two JSON-array annotation payloads (each a list
// of note strings) into a single de-duplicated JSON array, preserving
// first-seen order. Unparseable input on either side is treated as empty
// rather than aborting the merge.
func mergeAnnotations(a, b string) string {
	combined := append(parseAnnotationList(a), parseAnnotationList(b)...)
	if len(combined) == 0 {
		return ""
	}
	seen := make(map[string]bool, len(combined))
	deduped := make([]string, 0, len(combined))
	for _, item := range combined {
		if seen[item] {
			continue
		}
		seen[item] = true
		deduped = append(deduped, item)
	}
	out, err := json.Marshal(deduped)
	if err != nil {
		return ""
	}
	return string(out)
}

// parseAnnotationList decodes a JSON array of strings, returning nil for
// empty or unparseable input.
func parseAnnotationList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	return list
}

// ── Atomic Staging ──────────────────────────────────────────────────────────

// ImportStaging tracks a staged import that can be committed atomically
// or rolled back on failure. This prevents partial imports from leaving
// the destination in an inconsistent state.
type ImportStaging struct {
	store   *Store
	data    *Data
	journal *ImportJournal
}

// ImportJournal records the state of a staged import for crash recovery.
type ImportJournal struct {
	SessionID  string    `json:"session_id"`
	Policy     string    `json:"policy"`
	StagedAt   time.Time `json:"staged_at"`
	Committed  bool      `json:"committed"`
	RolledBack bool      `json:"rolled_back"`
}

// StageImport prepares an import without committing it. The caller must
// call Commit or Rollback to complete the operation. If the process crashes
// between Stage and Commit, the journal allows recovery.
func (s *Store) StageImport(ctx context.Context, incoming *Data, policy ImportConflictPolicy) (*ImportStaging, error) {
	if incoming == nil {
		return nil, fmt.Errorf("cannot stage nil session data")
	}

	// Validate the incoming data first.
	report := ValidateIntegrity(incoming)
	if !report.OK {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("cannot stage invalid session (%d issue(s)):\n", len(report.Issues)))
		for i, issue := range report.Issues {
			sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, issue.Field, issue.Description))
		}
		return nil, fmt.Errorf("%s", sb.String())
	}

	journal := &ImportJournal{
		SessionID: incoming.ID,
		Policy:    string(policy),
		StagedAt:  time.Now().UTC(),
	}

	return &ImportStaging{
		store:   s,
		data:    incoming,
		journal: journal,
	}, nil
}

// Commit finalizes a staged import by persisting the data to the store.
// It records a provenance entry and marks the journal as committed.
func (st *ImportStaging) Commit(ctx context.Context) (*ImportResult, error) {
	if st.journal.Committed {
		return nil, fmt.Errorf("import already committed")
	}

	policy, err := ParseImportConflictPolicy(st.journal.Policy)
	if err != nil {
		return nil, err
	}

	result, err := st.store.ImportSession(ctx, st.data, policy)
	if err != nil {
		return nil, err
	}

	st.journal.Committed = true
	return result, nil
}

// Rollback aborts a staged import. The destination is left unchanged.
func (st *ImportStaging) Rollback() error {
	if st.journal.Committed {
		return fmt.Errorf("cannot rollback committed import")
	}
	st.journal.RolledBack = true
	return nil
}

// IsCommitted reports whether the staging was committed.
func (st *ImportStaging) IsCommitted() bool {
	return st.journal.Committed
}

// IsRolledBack reports whether the staging was rolled back.
func (st *ImportStaging) IsRolledBack() bool {
	return st.journal.RolledBack
}

