// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MaxProvenanceEntries bounds the number of entries a ProvenanceTimeline
// retains. Appending beyond this limit drops the oldest entries first, so a
// session's timeline can never grow unbounded.
const MaxProvenanceEntries = 200

// ProvenanceOperation names a recorded session state transition.
type ProvenanceOperation string

const (
	ProvenanceFetched   ProvenanceOperation = "fetched"
	ProvenanceReplayed  ProvenanceOperation = "replayed"
	ProvenanceAnnotated ProvenanceOperation = "annotated"
	ProvenanceExported  ProvenanceOperation = "exported"
	ProvenanceImported  ProvenanceOperation = "imported"
	ProvenanceMigrated  ProvenanceOperation = "migrated"
	ProvenanceSaved     ProvenanceOperation = "saved"
	ProvenanceResumed   ProvenanceOperation = "resumed"
	ProvenanceRecovered ProvenanceOperation = "recovered"
)

// ProvenanceActor identifies who or what performed an operation.
type ProvenanceActor string

const (
	ActorUser   ProvenanceActor = "user"
	ActorSystem ProvenanceActor = "system"
)

// ProvenanceEntry records a single state transition in a session's history.
type ProvenanceEntry struct {
	Timestamp time.Time           `json:"timestamp"`
	Operation ProvenanceOperation `json:"operation"`
	Actor     ProvenanceActor     `json:"actor"`
	// ToolVersion is the Glassbox version that performed the operation.
	ToolVersion string `json:"tool_version"`
	// Fingerprint is a content or environment fingerprint (e.g. from
	// BuildEnvFingerprint), never a raw hostname, username, or path.
	Fingerprint string `json:"fingerprint,omitempty"`
	// Success is false when the operation failed; a failed entry must never
	// be rendered or interpreted as successful completion.
	Success bool `json:"success"`
	// Detail is a short, human-readable note, sanitized of PII before
	// being recorded.
	Detail string `json:"detail,omitempty"`
}

// ProvenanceTimeline is an append-only, bounded record of a session's
// lifecycle events (fetched, replayed, annotated, exported, imported,
// migrated, and so on).
type ProvenanceTimeline struct {
	Entries []ProvenanceEntry `json:"entries"`
}

// ParseProvenanceTimeline decodes a timeline from its persisted JSON form
// (Data.ProvenanceJSON). Empty or malformed input yields an empty, non-nil
// timeline rather than an error, so sessions created before provenance
// tracking existed — or with a corrupted timeline — still load cleanly.
func ParseProvenanceTimeline(raw string) *ProvenanceTimeline {
	if strings.TrimSpace(raw) == "" {
		return &ProvenanceTimeline{}
	}
	var t ProvenanceTimeline
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return &ProvenanceTimeline{}
	}
	return &t
}

// Append records a new entry, redacting any host-identifying details from
// Detail and Fingerprint, and trims the oldest entries once
// MaxProvenanceEntries is exceeded so the timeline stays bounded.
func (t *ProvenanceTimeline) Append(entry ProvenanceEntry) {
	entry.Detail = SanitizeErrorMessage(entry.Detail)
	entry.Fingerprint = SanitizeErrorMessage(entry.Fingerprint)
	t.Entries = append(t.Entries, entry)
	if len(t.Entries) > MaxProvenanceEntries {
		t.Entries = t.Entries[len(t.Entries)-MaxProvenanceEntries:]
	}
}

// JSON serializes the timeline for storage in Data.ProvenanceJSON.
func (t *ProvenanceTimeline) JSON() (string, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("failed to serialize provenance timeline: %w", err)
	}
	return string(b), nil
}

// RecordProvenance appends an entry to data's provenance timeline in place,
// creating the timeline if this is the first recorded event.
func RecordProvenance(data *Data, operation ProvenanceOperation, actor ProvenanceActor, toolVersion, fingerprint, detail string, success bool) error {
	if data == nil {
		return fmt.Errorf("cannot record provenance on nil session data")
	}
	timeline := ParseProvenanceTimeline(data.ProvenanceJSON)
	timeline.Append(ProvenanceEntry{
		Timestamp:   time.Now().UTC(),
		Operation:   operation,
		Actor:       actor,
		ToolVersion: toolVersion,
		Fingerprint: fingerprint,
		Success:     success,
		Detail:      detail,
	})
	serialized, err := timeline.JSON()
	if err != nil {
		return err
	}
	data.ProvenanceJSON = serialized
	return nil
}

// RenderText formats the timeline as a concise, human-readable report for
// CLI output.
func (t *ProvenanceTimeline) RenderText() string {
	if len(t.Entries) == 0 {
		return "No provenance history recorded."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Provenance timeline (%d event(s)):\n", len(t.Entries))
	for i, e := range t.Entries {
		status := "ok"
		if !e.Success {
			status = "FAILED"
		}
		fmt.Fprintf(&sb, "  %d. [%s] %-10s actor=%-6s version=%-10s status=%s",
			i+1, e.Timestamp.Format(time.RFC3339), e.Operation, e.Actor, e.ToolVersion, status)
		if e.Detail != "" {
			fmt.Fprintf(&sb, " — %s", e.Detail)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
