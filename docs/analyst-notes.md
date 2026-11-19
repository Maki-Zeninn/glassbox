# Analyst Notes

Analyst notes are durable personal observations attached to specific points in
a trace or session. Unlike [reviewer comments](../internal/trace/reviewer_comments.go),
which model structured, author-attributed review threads with severity and
resolution state, analyst notes are working memory: hypotheses, "check this
later" markers, and quick observations.

## Key properties

| Property | Behaviour |
|---|---|
| **Persistence** | Stored in `session.AnnotationsJSON`; survive save, resume, and archive |
| **Execution hashes** | Notes do NOT alter execution hashes or the trace fingerprint |
| **Anchoring** | Keyed to a stable step ID, source location, or the whole trace |
| **Dangling refs** | References to deleted steps are reported, never silently dropped |
| **Export** | Travel with trace exports and can be written to a portable `NoteFile` |
| **Import** | Merge by ID: incoming note replaces stored note with same ID |

## Anchor types

A note is anchored to exactly one of:

| Kind | Flag | Example |
|---|---|---|
| `trace` | (default) | whole session or trace |
| `step` | `--step <step-id>` | `step-3-a9f2b8c1` (from `--export-annotations`) |
| `source` | `--source file:line` | `token.rs:55` |

Source-anchored notes resolve to the first step whose `SourceFile:SourceLine`
matches. Steps without DWARF source info never match a source anchor.

## CLI reference

### `glassbox note add`

```
glassbox note add --session <id> [--body "..."] [--tag <tag>] [--step <step-id>] [--source file:line]
```

At least one of `--body` or `--tag` is required. `--step` and `--source` are
mutually exclusive.

### `glassbox note list`

```
glassbox note list --session <id> [--tag <tag>] [--json]
```

`--tag` filters to notes carrying that tag. `--json` emits machine-readable output.

### `glassbox note remove`

```
glassbox note remove --session <id> --id <note-id>
```

Non-interactive and immediate. Run `glassbox note list` first to confirm the ID.

### `glassbox note export`

```
glassbox note export --session <id> --output notes.json
```

Writes a portable `NoteFile` (JSON) that can be shared or re-imported.

### `glassbox note import`

```
glassbox note import --session <id> --file notes.json
```

Merges notes from a file. Incoming notes with existing IDs replace the stored
note. Dangling targets are reported as warnings, not errors.

## JSON format

```json
{
  "schema_version": "1.0",
  "generated_at": "2026-08-26T10:00:00Z",
  "transaction_hash": "sha256:abc...",
  "notes": [
    {
      "id": "note-a3f2b8c1",
      "target": { "kind": "step", "step_id": "step-3-a9f2b8c1" },
      "body": "Memory spike here — investigate on large inputs.",
      "tags": ["memory", "perf"],
      "created_at": "2026-08-26T09:15:00Z"
    }
  ]
}
```

A bare JSON array of notes is also accepted on import (no envelope required).

## Execution hash invariant

Notes are stored in `TraceAnnotations.Notes` under a separate `notes` JSON key.
The trace `Fingerprint()` and the `ExportJSON` envelope's `transaction_hash`
are computed from execution fields only — `Notes` is excluded from both.
Adding, editing, or removing notes never changes any cryptographic evidence.

Verification: `trace.Fingerprint()` calls `sha256.New()` over
`TransactionHash`, step count, and per-step identity fields. The `Notes` slice
is not part of the input. Tests in `analyst_notes_test.go` assert this
property explicitly.

## Programmatic API

```go
// Add a note to a trace
note := trace.AnalystNote{
    ID:     id,           // use trace.GenerateNoteID()
    Target: trace.StepTarget("step-3-a9f2b8c1"),
    Body:   "worth investigating",
    Tags:   []string{"perf"},
    CreatedAt: time.Now().UTC(),
}
resolutions, err := executionTrace.AttachNotes([]trace.AnalystNote{note})

// Resolve notes against a trace (detects dangling refs)
resolutions := trace.ResolveNotes(executionTrace, notes)
for _, r := range resolutions {
    if r.Status == trace.NoteDangling {
        fmt.Printf("dangling: %s — %s\n", r.Note.ID, r.Reason)
    }
}

// Session persistence
err := session.AddNote(data, note)
notes, err := session.GetNotes(data)
err = session.RemoveNote(data, "note-a3f2b8c1")

// Export / import file
file, err := trace.ExportNoteFile(executionTrace, time.Now())
file.Save("notes.json")

loaded, err := trace.LoadNoteFile("notes.json")
```

## Implementation locations

| File | Purpose |
|---|---|
| `internal/trace/analyst_notes.go` | `AnalystNote`, `NoteFile`, `ResolveNotes`, `AttachNotes`, `RemoveNote` |
| `internal/trace/annotations.go` | `TraceAnnotations.Notes` field |
| `internal/trace/reviewer_comments.go` | `TraceAnnotations.Clone` extended for notes |
| `internal/trace/note_export.go` | `NoteExportSet`, `BuildNoteExportSet`, text/JSON rendering helpers |
| `internal/session/notes.go` | `AddNote`, `RemoveNote`, `GetNotes`, `MergeNotes`, `annotationsEnvelope` |
| `internal/cmd/note.go` | CLI commands: `glassbox note add/list/remove/export/import` |
| `internal/trace/analyst_notes_test.go` | Tests: add/list/remove, dangling refs, Unicode, JSON round trips, hash invariant |
