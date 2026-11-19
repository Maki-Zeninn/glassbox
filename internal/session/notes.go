// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// notes.go — Analyst note persistence within a session.
//
// Session.AnnotationsJSON already carries reviewer comments. Analyst notes
// travel inside the same JSON blob under the "notes" key so they share the
// same atomic write path, archive member, and manifest hash.
//
// Notes do NOT participate in the session's AuditHash computation — they are
// personal working memory and must not alter cryptographic evidence.
// The canonical split: AnnotationsJSON holds collaboration state (comments +
// bookmarks + notes). AuditHash covers session.json, which excludes
// AnnotationsJSON (json:"-").

package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dotandev/glassbox/internal/trace"
)

// annotationsEnvelope is the structure stored in Data.AnnotationsJSON.
// It is kept additive: older fields survive forward-compat round trips.
type annotationsEnvelope struct {
	SchemaVersion string                `json:"schema_version"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Comments      []trace.ReviewerComment `json:"comments,omitempty"`
	Bookmarks     []trace.Bookmark      `json:"bookmarks,omitempty"`
	Notes         []trace.AnalystNote   `json:"notes,omitempty"`
}

const annotationsEnvelopeVersion = "1.0"

// LoadAnnotationsEnvelope deserializes Data.AnnotationsJSON.
// Returns a zero-value envelope (no error) for empty or missing JSON.
func LoadAnnotationsEnvelope(data *Data) (*annotationsEnvelope, error) {
	if data == nil || data.AnnotationsJSON == "" {
		return &annotationsEnvelope{SchemaVersion: annotationsEnvelopeVersion}, nil
	}
	var env annotationsEnvelope
	if err := json.Unmarshal([]byte(data.AnnotationsJSON), &env); err != nil {
		return nil, fmt.Errorf("failed to parse session annotations: %w\n"+
			"  The annotations field may be corrupt. "+
			"Run 'glassbox session doctor' for diagnostics.", err)
	}
	if env.SchemaVersion == "" {
		env.SchemaVersion = annotationsEnvelopeVersion
	}
	return &env, nil
}

// SaveAnnotationsEnvelope serializes env back into Data.AnnotationsJSON.
func SaveAnnotationsEnvelope(data *Data, env *annotationsEnvelope) error {
	if data == nil {
		return fmt.Errorf("cannot save annotations to nil session data")
	}
	if env == nil {
		data.AnnotationsJSON = ""
		return nil
	}
	env.SchemaVersion = annotationsEnvelopeVersion
	env.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session annotations: %w", err)
	}
	data.AnnotationsJSON = string(b)
	return nil
}

// GetNotes returns the analyst notes stored in the session.
// Never returns nil — callers get an empty slice for sessions with no notes.
func GetNotes(data *Data) ([]trace.AnalystNote, error) {
	env, err := LoadAnnotationsEnvelope(data)
	if err != nil {
		return nil, err
	}
	if env.Notes == nil {
		return []trace.AnalystNote{}, nil
	}
	return env.Notes, nil
}

// AddNote appends or replaces a note in the session.
// An incoming note with an existing ID replaces the stored note (idempotent).
// Returns an error if the note is structurally invalid or the limit is exceeded.
func AddNote(data *Data, note trace.AnalystNote) error {
	if data == nil {
		return fmt.Errorf("cannot add note to nil session data")
	}
	note.Normalize()
	if err := note.Validate(); err != nil {
		return fmt.Errorf("invalid note: %w", err)
	}

	env, err := LoadAnnotationsEnvelope(data)
	if err != nil {
		return err
	}

	// Replace existing note with same ID, or append.
	replaced := false
	for i := range env.Notes {
		if env.Notes[i].ID == note.ID {
			now := time.Now().UTC()
			note.UpdatedAt = now
			env.Notes[i] = note
			replaced = true
			break
		}
	}
	if !replaced {
		if len(env.Notes) >= trace.MaxNotes {
			return fmt.Errorf("session has too many notes (%d, max %d)\n"+
				"  Fix: remove old notes with 'glassbox note remove --id <id>'",
				len(env.Notes), trace.MaxNotes)
		}
		env.Notes = append(env.Notes, note)
	}

	return SaveAnnotationsEnvelope(data, env)
}

// RemoveNote removes the note with id from the session.
// Returns an error if the ID is not found.
func RemoveNote(data *Data, id string) error {
	if data == nil {
		return fmt.Errorf("cannot remove note from nil session data")
	}
	env, err := LoadAnnotationsEnvelope(data)
	if err != nil {
		return err
	}

	before := len(env.Notes)
	filtered := env.Notes[:0]
	for _, n := range env.Notes {
		if n.ID != id {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == before {
		return fmt.Errorf("note %q not found in this session\n"+
			"  Run 'glassbox note list' to see available note IDs", id)
	}
	env.Notes = filtered
	return SaveAnnotationsEnvelope(data, env)
}

// MergeNotes merges incoming notes into the session using the same
// replace-by-ID semantics as AddNote. Used when importing an annotation file.
// Returns the resolution results for all notes after the merge.
func MergeNotes(data *Data, incoming []trace.AnalystNote, t *trace.ExecutionTrace) ([]trace.ResolvedNote, error) {
	if data == nil {
		return nil, fmt.Errorf("cannot merge notes into nil session data")
	}

	env, err := LoadAnnotationsEnvelope(data)
	if err != nil {
		return nil, err
	}

	// Normalize and validate all incoming notes first.
	normalized := make([]trace.AnalystNote, len(incoming))
	copy(normalized, incoming)
	seen := make(map[string]int, len(normalized))
	for i := range normalized {
		normalized[i].Normalize()
		if err := normalized[i].Validate(); err != nil {
			return nil, fmt.Errorf("incoming note %d is invalid: %w", i, err)
		}
		if first, dup := seen[normalized[i].ID]; dup {
			return nil, fmt.Errorf("duplicate note id %q at entries %d and %d",
				normalized[i].ID, first, i)
		}
		seen[normalized[i].ID] = i
	}

	// Merge.
	merged := append([]trace.AnalystNote(nil), env.Notes...)
	position := make(map[string]int, len(merged))
	for i, n := range merged {
		position[n.ID] = i
	}
	for _, n := range normalized {
		if idx, ok := position[n.ID]; ok {
			merged[idx] = n
			continue
		}
		position[n.ID] = len(merged)
		merged = append(merged, n)
	}

	if len(merged) > trace.MaxNotes {
		return nil, fmt.Errorf("merge would exceed note limit (%d, max %d)", len(merged), trace.MaxNotes)
	}

	env.Notes = merged
	if err := SaveAnnotationsEnvelope(data, env); err != nil {
		return nil, err
	}

	return trace.ResolveNotes(t, merged), nil
}
