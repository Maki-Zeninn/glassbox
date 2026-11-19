// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// analyst_notes.go — Durable analyst notes anchored to stable trace locations.
//
// An AnalystNote is a free-form observation attached to a specific point in a
// trace, a source location, or the trace as a whole. Unlike ReviewerComments
// (which model structured, author-attributed review threads with severity and
// resolution states), analyst notes are personal working memory: quick
// observations, hypotheses, and "remember to check X" entries.
//
// Design principles:
//
//   - Notes are keyed by a stable NoteID that is independent of slice position
//     and survives trace filtering, verbosity changes, and schema migration.
//   - Notes do NOT participate in execution hash computation. They are stored
//     in TraceAnnotations.Notes and exported in a separate JSON key so they
//     can be added, removed, or edited without changing the execution evidence.
//   - Invalid references (deleted steps, moved source lines) are reported as
//     "dangling" rather than silently dropped. Dangling notes still appear in
//     exports and are clearly marked.
//   - Merge semantics: on import an incoming note with an existing ID replaces
//     the stored note (idempotent, same as ReviewerComments).
//
// JSON format:
//
//	{
//	  "id":         "note-a3f2",
//	  "target":     { "kind": "step", "step_id": "step-3-a9f2b8c1" },
//	  "body":       "This host call takes ~2x expected memory on large inputs.",
//	  "tags":       ["perf", "memory"],
//	  "created_at": "2026-08-26T10:00:00Z",
//	  "updated_at": "2026-08-26T11:30:00Z"
//	}
package trace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Note size limits — kept intentionally generous since notes are personal
// working memory and not shared review artifacts.
const (
	MaxNoteBodyLength = 50000
	MaxNoteTagLength  = 64
	MaxNoteTags       = 20
	MaxNoteIDLength   = 128
	MaxNotes          = 500
)

// AnalystNote is a free-form observation attached to a trace location.
// It must not alter execution hashes — see TraceAnnotations.Notes.
type AnalystNote struct {
	// ID uniquely identifies this note. Generated automatically when empty.
	ID string `json:"id"`
	// Target identifies what the note is about, using the same AnnotationTarget
	// type as ReviewerComments so resolution logic is shared.
	Target AnnotationTarget `json:"target"`
	// Body is the note text. May be empty for tag-only notes.
	Body string `json:"body,omitempty"`
	// Tags are free-form labels for grouping and filtering.
	Tags []string `json:"tags,omitempty"`
	// CreatedAt is when the note was first written.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the body or tags were last changed.
	// Omitted from JSON when zero.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// MarshalJSON omits updated_at when the note has never been edited,
// for the same reason as ReviewerComment.MarshalJSON.
func (n AnalystNote) MarshalJSON() ([]byte, error) {
	type alias AnalystNote
	if n.UpdatedAt.IsZero() {
		return json.Marshal(struct {
			alias
			UpdatedAt *time.Time `json:"updated_at,omitempty"`
		}{alias: alias(n)})
	}
	return json.Marshal(alias(n))
}

// Normalize trims whitespace, fills a CreatedAt if absent, deduplicates
// and sorts tags, and converts timestamps to UTC.
func (n *AnalystNote) Normalize() {
	n.ID = strings.TrimSpace(n.ID)
	n.Body = strings.TrimSpace(n.Body)
	n.Target.StepID = strings.TrimSpace(n.Target.StepID)
	n.Target.SourceFile = strings.TrimSpace(n.Target.SourceFile)

	if n.Target.Kind == "" && n.Target.StepID == "" && n.Target.SourceFile == "" {
		n.Target.Kind = TargetTrace
	}

	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	} else {
		n.CreatedAt = n.CreatedAt.UTC()
	}
	if !n.UpdatedAt.IsZero() {
		n.UpdatedAt = n.UpdatedAt.UTC()
	}

	// Deduplicate and sort tags.
	seen := make(map[string]bool, len(n.Tags))
	clean := n.Tags[:0]
	for _, t := range n.Tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		clean = append(clean, t)
	}
	sort.Strings(clean)
	n.Tags = clean
}

// Validate checks structural validity (does not resolve the target against
// any trace — use ResolveNoteTarget for that).
func (n AnalystNote) Validate() error {
	if strings.TrimSpace(n.ID) == "" {
		return fmt.Errorf("note id is empty\n" +
			"  Fix: every note requires a unique non-empty id")
	}
	if len(n.ID) > MaxNoteIDLength {
		return fmt.Errorf("note id %q exceeds %d bytes\n"+
			"  Fix: use a shorter identifier", n.ID, MaxNoteIDLength)
	}
	if len(n.Body) > MaxNoteBodyLength {
		return fmt.Errorf("note %q body exceeds %d bytes (got %d)\n"+
			"  Fix: shorten the note or split across multiple notes",
			n.ID, MaxNoteBodyLength, len(n.Body))
	}
	if len(n.Tags) > MaxNoteTags {
		return fmt.Errorf("note %q has too many tags (%d, max %d)\n"+
			"  Fix: reduce the number of tags", n.ID, len(n.Tags), MaxNoteTags)
	}
	for _, tag := range n.Tags {
		if len(tag) > MaxNoteTagLength {
			return fmt.Errorf("note %q tag %q exceeds %d bytes\n"+
				"  Fix: shorten the tag", n.ID, tag, MaxNoteTagLength)
		}
	}
	if n.CreatedAt.IsZero() {
		return fmt.Errorf("note %q has no created_at timestamp\n"+
			"  Fix: set created_at to an RFC 3339 timestamp, e.g. 2026-08-26T10:00:00Z", n.ID)
	}
	if !n.UpdatedAt.IsZero() && n.UpdatedAt.Before(n.CreatedAt) {
		return fmt.Errorf("note %q has updated_at before created_at", n.ID)
	}
	if err := n.Target.Validate(); err != nil {
		return fmt.Errorf("note %q has invalid target: %w", n.ID, err)
	}
	return nil
}

// GenerateNoteID creates a random 4-byte hex note ID in the form "note-<hex8>".
func GenerateNoteID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate note ID: %w", err)
	}
	return "note-" + hex.EncodeToString(b), nil
}

// ── Note resolution ───────────────────────────────────────────────────────────

// NoteResolutionStatus classifies how a note's target resolved.
type NoteResolutionStatus string

const (
	NoteResolved  NoteResolutionStatus = "resolved"
	NoteDangling  NoteResolutionStatus = "dangling"
	NoteTraceWide NoteResolutionStatus = "trace_wide"
)

// ResolvedNote pairs a note with the step index it resolved to.
type ResolvedNote struct {
	Note      AnalystNote
	StepIndex int // -1 for trace-wide notes
	Status    NoteResolutionStatus
	Reason    string // set when Status == NoteDangling
}

// ResolveNotes resolves every note's target against t, sharing one index.
// Trace-wide notes always resolve. Dangling notes are returned (not dropped).
func ResolveNotes(t *ExecutionTrace, notes []AnalystNote) []ResolvedNote {
	out := make([]ResolvedNote, 0, len(notes))
	if len(notes) == 0 {
		return out
	}

	stepIndex := make(map[string]int, 0)
	sourceIndex := make(map[string]int, 0)
	if t != nil {
		for i := range t.States {
			id := StepIDOf(&t.States[i])
			if _, seen := stepIndex[id]; !seen {
				stepIndex[id] = i
			}
			if t.States[i].SourceFile != "" {
				key := sourceKey(t.States[i].SourceFile, t.States[i].SourceLine)
				if _, seen := sourceIndex[key]; !seen {
					sourceIndex[key] = i
				}
			}
		}
	}

	for _, note := range notes {
		switch note.Target.Kind {
		case TargetTrace, "":
			out = append(out, ResolvedNote{Note: note, StepIndex: -1, Status: NoteTraceWide})

		case TargetStep:
			if idx, ok := stepIndex[note.Target.StepID]; ok {
				out = append(out, ResolvedNote{Note: note, StepIndex: idx, Status: NoteResolved})
			} else {
				out = append(out, ResolvedNote{
					Note: note, StepIndex: -1, Status: NoteDangling,
					Reason: "no step in the trace has this stable ID; the step may have been removed or the note belongs to a different trace",
				})
			}

		case TargetSource:
			key := sourceKey(note.Target.SourceFile, note.Target.SourceLine)
			if idx, ok := sourceIndex[key]; ok {
				out = append(out, ResolvedNote{Note: note, StepIndex: idx, Status: NoteResolved})
			} else {
				out = append(out, ResolvedNote{
					Note: note, StepIndex: -1, Status: NoteDangling,
					Reason: "no step maps to this source location",
				})
			}

		default:
			out = append(out, ResolvedNote{
				Note: note, StepIndex: -1, Status: NoteDangling,
				Reason: fmt.Sprintf("unknown target kind %q", string(note.Target.Kind)),
			})
		}
	}
	return out
}

// ── TraceAnnotations integration ─────────────────────────────────────────────

// AttachNotes validates and merges notes into the trace.
// An incoming note with an existing ID replaces the stored note (idempotent).
// Structural errors abort the entire merge; target resolution is non-fatal.
// Returns the resolution results for all merged notes.
func (t *ExecutionTrace) AttachNotes(notes []AnalystNote) ([]ResolvedNote, error) {
	if t == nil {
		return nil, fmt.Errorf("cannot attach notes to a nil trace")
	}

	normalized := make([]AnalystNote, len(notes))
	copy(normalized, notes)
	seen := make(map[string]int, len(normalized))
	for i := range normalized {
		normalized[i].Normalize()
		if err := normalized[i].Validate(); err != nil {
			return nil, fmt.Errorf("note %d is invalid: %w", i, err)
		}
		if first, dup := seen[normalized[i].ID]; dup {
			return nil, fmt.Errorf("duplicate note id %q at entries %d and %d\n"+
				"  Fix: give every note a unique id", normalized[i].ID, first, i)
		}
		seen[normalized[i].ID] = i
	}

	// Merge onto a copy to keep the trace untouched on limit failure.
	merged := append([]AnalystNote(nil), t.Annotations.Notes...)
	position := make(map[string]int, len(merged))
	for i, existing := range merged {
		position[existing.ID] = i
	}
	for _, n := range normalized {
		if idx, ok := position[n.ID]; ok {
			merged[idx] = n
			continue
		}
		position[n.ID] = len(merged)
		merged = append(merged, n)
	}

	if len(merged) > MaxNotes {
		return nil, fmt.Errorf("too many notes (%d) — maximum is %d per trace\n"+
			"  Fix: remove obsolete notes with 'glassbox note remove --id <id>'",
			len(merged), MaxNotes)
	}

	t.Annotations.Notes = merged
	return ResolveNotes(t, merged), nil
}

// RemoveNote removes the note with the given ID from the trace.
// Returns an error if the ID is not found.
func (t *ExecutionTrace) RemoveNote(id string) error {
	if t == nil {
		return fmt.Errorf("cannot remove note from nil trace")
	}
	before := len(t.Annotations.Notes)
	notes := t.Annotations.Notes[:0]
	for _, n := range t.Annotations.Notes {
		if n.ID != id {
			notes = append(notes, n)
		}
	}
	if len(notes) == before {
		return fmt.Errorf("note %q not found\n"+
			"  Run 'glassbox note list' to see available note IDs", id)
	}
	t.Annotations.Notes = notes
	return nil
}

// ── NoteFile — portable on-disk envelope ─────────────────────────────────────

// NoteFileSchemaVersion is written into every exported note file.
const NoteFileSchemaVersion = "1.0"

// SupportedNoteFileVersions lists every schema_version this build can import.
var SupportedNoteFileVersions = []string{"1.0"}

// NoteFile is the portable envelope exchanged between sessions, machines,
// and team members. It is analogous to AnnotationFile for analyst notes.
type NoteFile struct {
	SchemaVersion   string        `json:"schema_version"`
	GeneratedAt     time.Time     `json:"generated_at"`
	TransactionHash string        `json:"transaction_hash,omitempty"`
	Notes           []AnalystNote `json:"notes"`
}

// LoadNoteFile reads a portable note file from path.
// Accepts either the full envelope or a bare []AnalystNote array.
func LoadNoteFile(path string) (*NoteFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read note file %q: %w\n"+
			"  Tip: produce one with 'glassbox note export --output notes.json'",
			path, err)
	}

	file := &NoteFile{}
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var bare []AnalystNote
		if err := json.Unmarshal(data, &bare); err != nil {
			return nil, fmt.Errorf("failed to parse note file %q as a note array: %w", path, err)
		}
		file.SchemaVersion = NoteFileSchemaVersion
		file.Notes = bare
	} else {
		if err := json.Unmarshal(data, file); err != nil {
			return nil, fmt.Errorf("failed to parse note file %q: %w", path, err)
		}
		if file.SchemaVersion == "" {
			file.SchemaVersion = NoteFileSchemaVersion
		}
		supported := false
		for _, v := range SupportedNoteFileVersions {
			if v == file.SchemaVersion {
				supported = true
				break
			}
		}
		if !supported {
			return nil, fmt.Errorf("note file %q has unsupported schema_version %q (supported: %s)\n"+
				"  Fix: upgrade Glassbox or re-export with this build",
				path, file.SchemaVersion, strings.Join(SupportedNoteFileVersions, ", "))
		}
	}

	if len(file.Notes) > MaxNotes {
		return nil, fmt.Errorf("note file %q contains %d notes — maximum is %d\n"+
			"  Fix: split across several files",
			path, len(file.Notes), MaxNotes)
	}

	for i := range file.Notes {
		file.Notes[i].Normalize()
		if err := file.Notes[i].Validate(); err != nil {
			return nil, fmt.Errorf("note file %q entry %d is invalid: %w", path, i, err)
		}
	}
	return file, nil
}

// Save writes the note file to path as indented JSON.
func (f *NoteFile) Save(path string) error {
	if f == nil {
		return fmt.Errorf("cannot save a nil note file")
	}
	if f.SchemaVersion == "" {
		f.SchemaVersion = NoteFileSchemaVersion
	}
	if f.Notes == nil {
		f.Notes = []AnalystNote{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal note file: %w", err)
	}
	data = append(data, '\n')

	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directory for note file %q: %w", path, err)
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// ExportNoteFile builds a NoteFile from a trace's analyst notes.
func ExportNoteFile(t *ExecutionTrace, generatedAt time.Time) (*NoteFile, error) {
	if t == nil {
		return nil, fmt.Errorf("cannot export notes from a nil trace")
	}
	notes := append([]AnalystNote(nil), t.Annotations.Notes...)
	// Sort deterministically: by step index where known, then creation time.
	resolutions := ResolveNotes(t, notes)
	stepOf := make(map[string]int, len(resolutions))
	for _, r := range resolutions {
		stepOf[r.Note.ID] = r.StepIndex
	}
	sort.SliceStable(notes, func(i, j int) bool {
		si := stepOf[notes[i].ID]
		sj := stepOf[notes[j].ID]
		if si != sj {
			return si < sj
		}
		if !notes[i].CreatedAt.Equal(notes[j].CreatedAt) {
			return notes[i].CreatedAt.Before(notes[j].CreatedAt)
		}
		return notes[i].ID < notes[j].ID
	})
	return &NoteFile{
		SchemaVersion:   NoteFileSchemaVersion,
		GeneratedAt:     generatedAt.UTC().Truncate(time.Second),
		TransactionHash: fingerprintTxHash(t.TransactionHash),
		Notes:           notes,
	}, nil
}

// ── Clone support ─────────────────────────────────────────────────────────────

// cloneNotes returns a deep copy of a note slice.
// Called by TraceAnnotations.Clone (in reviewer_comments.go).
func cloneNotes(notes []AnalystNote) []AnalystNote {
	if notes == nil {
		return nil
	}
	out := make([]AnalystNote, len(notes))
	copy(out, notes)
	for i := range out {
		if out[i].Tags != nil {
			out[i].Tags = append([]string(nil), out[i].Tags...)
		}
	}
	return out
}
