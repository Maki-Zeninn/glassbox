// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// note.go — CLI commands for managing analyst notes on sessions and traces.
//
//	glassbox note add     --session <id> --body "..." [--tag perf] [--step <step-id>]
//	glassbox note list    --session <id> [--tag perf] [--json]
//	glassbox note remove  --session <id> --id <note-id>
//	glassbox note export  --session <id> --output notes.json
//	glassbox note import  --session <id> --file notes.json
//
// Notes are stored in the session's AnnotationsJSON and do NOT affect
// execution hashes. They are also exportable as a standalone NoteFile that
// can be shared or imported into another session.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/session"
	"github.com/dotandev/glassbox/internal/trace"
	"github.com/spf13/cobra"
)

var (
	noteSessionFlag string // --session  session ID (required for most subcommands)
	noteIDFlag      string // --id       note ID (remove)
	noteBodyFlag    string // --body     note text (add)
	noteTagsFlag    []string // --tag    tags (add; repeatable)
	noteStepFlag    string // --step     stable step ID anchor (add)
	noteSourceFlag  string // --source   "file.rs:42" source location anchor (add)
	noteListTagFlag string // --tag      filter by tag (list)
	noteJSONFlag    bool   // --json     emit JSON (list)
	noteOutputFlag  string // --output   destination file (export)
	noteFileFlag    string // --file     source file (import)
)

// ── parent command ────────────────────────────────────────────────────────────

var noteCmd = &cobra.Command{
	Use:     "note",
	GroupID: "management",
	Short:   "Manage analyst notes attached to sessions",
	Long: `Analyst notes are personal observations attached to specific execution steps,
source locations, or a session as a whole.

Notes persist through session save and resume, and can be exported for
sharing. They do NOT alter execution hashes — adding or removing a note
never changes the cryptographic evidence stored in a session.

Notes are keyed by a stable ID anchored to the execution step or source
location, so they survive trace re-fetching and verbosity changes.

Subcommands:
  add     Attach a new note to a session
  list    List notes stored in a session
  remove  Delete a note by ID
  export  Write session notes to a portable JSON file
  import  Merge notes from a JSON file into a session`,
}

func init() {
	noteCmd.PersistentFlags().StringVar(&noteSessionFlag, "session", "", "Session ID (use 'glassbox session list' to find IDs)")
	rootCmd.AddCommand(noteCmd)
}

// ── note add ──────────────────────────────────────────────────────────────────

var noteAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Attach a new analyst note to a session",
	Long: `Create an analyst note and attach it to the specified session.

The note is anchored to the whole session by default. Use --step to anchor
to a specific execution step (stable step ID from 'glassbox trace --export-annotations'),
or --source to anchor to a source file and line number.

Examples:
  # Add a trace-wide note
  glassbox note add --session abc123 --body "memory spike at step 42"

  # Add a note anchored to a step
  glassbox note add --session abc123 --step "step-3-a9f2b8c1" --body "unusual branch"

  # Add a note anchored to a source location with tags
  glassbox note add --session abc123 --source "token.rs:55" --body "review this" --tag perf --tag security`,
	RunE: runNoteAdd,
}

func init() {
	noteAddCmd.Flags().StringVar(&noteBodyFlag, "body", "", "Note text (required unless --tag is used alone)")
	noteAddCmd.Flags().StringArrayVar(&noteTagsFlag, "tag", nil, "Tag to attach to the note (repeatable)")
	noteAddCmd.Flags().StringVar(&noteStepFlag, "step", "", "Stable step ID to anchor the note to (from --export-annotations)")
	noteAddCmd.Flags().StringVar(&noteSourceFlag, "source", "", "Source location anchor in 'file:line' format (e.g. token.rs:55)")
	noteCmd.AddCommand(noteAddCmd)
}

func runNoteAdd(cmd *cobra.Command, _ []string) error {
	if noteSessionFlag == "" {
		return errors.WrapCliArgumentRequired("session")
	}
	if noteBodyFlag == "" && len(noteTagsFlag) == 0 {
		return errors.WrapValidationError("--body or --tag is required\n  Fix: provide note text with --body or at least one tag with --tag")
	}

	// Build target.
	target, err := buildNoteTarget(noteStepFlag, noteSourceFlag)
	if err != nil {
		return errors.WrapValidationError(err.Error())
	}

	noteID, err := trace.GenerateNoteID()
	if err != nil {
		return errors.WrapInternal("failed to generate note ID", err)
	}

	note := trace.AnalystNote{
		ID:        noteID,
		Target:    target,
		Body:      noteBodyFlag,
		Tags:      noteTagsFlag,
		CreatedAt: time.Now().UTC(),
	}
	note.Normalize()

	store, err := openSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := cmd.Context()
	data, err := store.Load(ctx, noteSessionFlag)
	if err != nil {
		return err
	}

	if err := session.AddNote(data, note); err != nil {
		return errors.WrapValidationError(err.Error())
	}

	if err := store.Save(ctx, data); err != nil {
		return err
	}

	fmt.Printf("Note added: %s\n", note.ID)
	fmt.Printf("  Session: %s\n", noteSessionFlag)
	if note.Body != "" {
		body := note.Body
		if len(body) > 60 {
			body = body[:57] + "..."
		}
		fmt.Printf("  Body:    %s\n", body)
	}
	if len(note.Tags) > 0 {
		fmt.Printf("  Tags:    %s\n", strings.Join(note.Tags, ", "))
	}
	fmt.Printf("  Target:  %s\n", note.Target.String())
	return nil
}

// buildNoteTarget constructs an AnnotationTarget from the --step and --source flags.
func buildNoteTarget(stepID, source string) (trace.AnnotationTarget, error) {
	if stepID != "" && source != "" {
		return trace.AnnotationTarget{}, fmt.Errorf("--step and --source are mutually exclusive\n  Fix: use one or the other, not both")
	}
	if stepID != "" {
		return trace.StepTarget(stepID), nil
	}
	if source != "" {
		file, line, err := parseSourceLocation(source)
		if err != nil {
			return trace.AnnotationTarget{}, fmt.Errorf("invalid --source %q: %w\n  Fix: use 'file:line' format, e.g. token.rs:55", source, err)
		}
		return trace.SourceTarget(file, line), nil
	}
	return trace.TraceTarget(), nil
}

func parseSourceLocation(s string) (string, int, error) {
	idx := strings.LastIndex(s, ":")
	if idx <= 0 {
		return "", 0, fmt.Errorf("expected 'file:line' format")
	}
	file := s[:idx]
	var line int
	if _, err := fmt.Sscanf(s[idx+1:], "%d", &line); err != nil || line <= 0 {
		return "", 0, fmt.Errorf("line number must be a positive integer")
	}
	return file, line, nil
}

// ── note list ─────────────────────────────────────────────────────────────────

var noteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List analyst notes stored in a session",
	Long: `Display all analyst notes attached to the specified session.

Use --tag to filter to notes that carry a specific tag.
Use --json to emit machine-readable output.

Examples:
  # List all notes for a session
  glassbox note list --session abc123

  # List notes tagged 'perf'
  glassbox note list --session abc123 --tag perf

  # Machine-readable output
  glassbox note list --session abc123 --json`,
	RunE: runNoteList,
}

func init() {
	noteListCmd.Flags().StringVar(&noteListTagFlag, "tag", "", "Filter notes by tag")
	noteListCmd.Flags().BoolVar(&noteJSONFlag, "json", false, "Emit JSON output")
	noteCmd.AddCommand(noteListCmd)
}

func runNoteList(cmd *cobra.Command, _ []string) error {
	if noteSessionFlag == "" {
		return errors.WrapCliArgumentRequired("session")
	}

	store, err := openSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	data, err := store.Load(cmd.Context(), noteSessionFlag)
	if err != nil {
		return err
	}

	notes, err := session.GetNotes(data)
	if err != nil {
		return errors.WrapValidationError(err.Error())
	}

	// Tag filter.
	if noteListTagFlag != "" {
		filtered := notes[:0]
		for _, n := range notes {
			for _, tag := range n.Tags {
				if tag == noteListTagFlag {
					filtered = append(filtered, n)
					break
				}
			}
		}
		notes = filtered
	}

	if noteJSONFlag {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(notes)
	}

	if len(notes) == 0 {
		fmt.Printf("No notes found for session %s", noteSessionFlag)
		if noteListTagFlag != "" {
			fmt.Printf(" with tag %q", noteListTagFlag)
		}
		fmt.Println(".")
		return nil
	}

	fmt.Printf("%d note(s) for session %s:\n\n", len(notes), noteSessionFlag)
	for i, n := range notes {
		fmt.Printf("  [%d] %s\n", i+1, n.ID)
		fmt.Printf("      Target:  %s\n", n.Target.String())
		fmt.Printf("      Created: %s\n", n.CreatedAt.Format("2006-01-02 15:04 UTC"))
		if len(n.Tags) > 0 {
			fmt.Printf("      Tags:    %s\n", strings.Join(n.Tags, ", "))
		}
		if n.Body != "" {
			body := n.Body
			if len(body) > 80 {
				body = body[:77] + "..."
			}
			fmt.Printf("      Body:    %s\n", body)
		}
		fmt.Println()
	}
	return nil
}

// ── note remove ───────────────────────────────────────────────────────────────

var noteRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Delete an analyst note from a session by ID",
	Long: `Remove the note with the given ID from the session.

This operation is non-interactive and immediate. There is no undo; if you
need to recover a note, re-add it with 'glassbox note add'.

Example:
  glassbox note remove --session abc123 --id note-a3f2b8c1`,
	RunE: runNoteRemove,
}

func init() {
	noteRemoveCmd.Flags().StringVar(&noteIDFlag, "id", "", "Note ID to remove (from 'glassbox note list')")
	_ = noteRemoveCmd.MarkFlagRequired("id")
	noteCmd.AddCommand(noteRemoveCmd)
}

func runNoteRemove(cmd *cobra.Command, _ []string) error {
	if noteSessionFlag == "" {
		return errors.WrapCliArgumentRequired("session")
	}

	store, err := openSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := cmd.Context()
	data, err := store.Load(ctx, noteSessionFlag)
	if err != nil {
		return err
	}

	if err := session.RemoveNote(data, noteIDFlag); err != nil {
		return errors.WrapValidationError(err.Error())
	}

	if err := store.Save(ctx, data); err != nil {
		return err
	}

	fmt.Printf("Note %s removed from session %s.\n", noteIDFlag, noteSessionFlag)
	return nil
}

// ── note export ───────────────────────────────────────────────────────────────

var noteExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export analyst notes from a session to a portable JSON file",
	Long: `Write the analyst notes stored in a session to a JSON file that can be
shared with other team members or imported into another session.

The exported file uses the NoteFile envelope format. Import it back with
'glassbox note import'.

Example:
  glassbox note export --session abc123 --output my-notes.json`,
	RunE: runNoteExport,
}

func init() {
	noteExportCmd.Flags().StringVar(&noteOutputFlag, "output", "", "Output file path for the exported notes")
	_ = noteExportCmd.MarkFlagRequired("output")
	noteCmd.AddCommand(noteExportCmd)
}

func runNoteExport(cmd *cobra.Command, _ []string) error {
	if noteSessionFlag == "" {
		return errors.WrapCliArgumentRequired("session")
	}

	store, err := openSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	data, err := store.Load(cmd.Context(), noteSessionFlag)
	if err != nil {
		return err
	}

	notes, err := session.GetNotes(data)
	if err != nil {
		return errors.WrapValidationError(err.Error())
	}

	file := &trace.NoteFile{
		SchemaVersion:   trace.NoteFileSchemaVersion,
		GeneratedAt:     time.Now().UTC(),
		TransactionHash: data.TxHash,
		Notes:           notes,
	}

	if err := file.Save(noteOutputFlag); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to export notes: %v", err))
	}

	fmt.Printf("Exported %d note(s) to %s\n", len(notes), noteOutputFlag)
	return nil
}

// ── note import ───────────────────────────────────────────────────────────────

var noteImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import analyst notes from a JSON file into a session",
	Long: `Merge analyst notes from a portable JSON file into the specified session.

Incoming notes with IDs matching existing notes replace them (idempotent).
New notes are appended. Invalid notes cause the import to abort before any
changes are applied.

Example:
  glassbox note import --session abc123 --file colleague-notes.json`,
	RunE: runNoteImport,
}

func init() {
	noteImportCmd.Flags().StringVar(&noteFileFlag, "file", "", "Note file to import (JSON)")
	_ = noteImportCmd.MarkFlagRequired("file")
	noteCmd.AddCommand(noteImportCmd)
}

func runNoteImport(cmd *cobra.Command, _ []string) error {
	if noteSessionFlag == "" {
		return errors.WrapCliArgumentRequired("session")
	}

	noteFile, err := trace.LoadNoteFile(noteFileFlag)
	if err != nil {
		return errors.WrapValidationError(err.Error())
	}

	store, err := openSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := cmd.Context()
	data, err := store.Load(ctx, noteSessionFlag)
	if err != nil {
		return err
	}

	resolutions, err := session.MergeNotes(data, noteFile.Notes, nil)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("note import failed: %v", err))
	}

	if err := store.Save(ctx, data); err != nil {
		return err
	}

	dangling := 0
	for _, r := range resolutions {
		if r.Status == trace.NoteDangling {
			dangling++
			fmt.Fprintf(os.Stderr, "Warning: note %q has dangling target: %s\n", r.Note.ID, r.Reason)
		}
	}

	fmt.Printf("Imported %d note(s) into session %s", len(noteFile.Notes), noteSessionFlag)
	if dangling > 0 {
		fmt.Printf(" (%d dangling — targets not found in current trace)", dangling)
	}
	fmt.Println(".")
	return nil
}


