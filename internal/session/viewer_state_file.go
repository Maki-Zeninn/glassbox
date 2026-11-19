// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ViewerStateVersion is the current on-disk schema version for viewer state
// sidecar files. Files written with a newer version than this are ignored
// (with a warning) rather than misinterpreted.
//
// Version history:
//   - 1: initial version (current_step, search_query, event_filter, hide_stdlib)
//   - 2: adds expanded_call_frames, viewport, and annotations fields
const ViewerStateVersion = 2

// ViewerStateDirEnv, when set, overrides the directory used to store viewer
// state sidecar files. Useful when the home directory is read-only.
const ViewerStateDirEnv = "GLASSBOX_VIEWER_STATE_DIR"

const (
	maxSearchQueryLen = 512
	maxEventFilterLen = 64
	maxTxHashLen      = 128
)

var (
	stateMu sync.Mutex
	// writesDisabled is set after the first failed sidecar write so that a
	// read-only state directory produces one warning, not one per keystroke.
	writesDisabled bool
	// warnWriter receives sidecar warnings; swapped out by tests.
	warnWriter io.Writer = os.Stderr
)

// ViewerState holds the UI fields persisted per trace. It is stored as a
// standalone JSON sidecar keyed by trace fingerprint, never inside the trace
// payload itself. Only plain display state is stored — no paths, commands, or
// other executable content.
//
// The struct is version-gated: fields added in version 2+ are initialised to
// safe zero values when loaded from an older sidecar, and dropped with a
// diagnostic when the sidecar version is newer than this build supports.
type ViewerState struct {
	Version          int       `json:"version"`
	TraceFingerprint string    `json:"trace_fingerprint"`
	TxHash           string    `json:"tx_hash,omitempty"`
	CurrentStep      int       `json:"current_step"`
	SearchQuery      string    `json:"search_query,omitempty"`
	CurrentMatch     int       `json:"current_match,omitempty"` // 1-based
	EventFilter      string    `json:"event_filter,omitempty"`
	HideStdLib       bool      `json:"hide_stdlib,omitempty"`

	// ExpandedCallFrames records which call-frame step indices are expanded
	// in the tree view. Only step indices that exist in the current trace
	// are restored; dangling references are silently dropped (see
	// ValidateViewerStateReferences).
	ExpandedCallFrames []int `json:"expanded_call_frames,omitempty"`

	// Viewport captures the visible window of the step list so that a
	// restored session scrolls to the same position. The values are clamped
	// to [0, len(states)-1] during validation.
	Viewport ViewerViewport `json:"viewport,omitempty"`

	// Annotations records per-step annotation visibility (which cost
	// breakdowns, host-state panels, or memory panels are expanded).
	// Keys are step indices encoded as decimal strings; values list the
	// open panel identifiers. Unknown panel IDs are silently dropped.
	Annotations map[string][]string `json:"annotations,omitempty"`

	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// ViewerViewport captures the scroll position of the step list.
type ViewerViewport struct {
	// FirstVisible is the index of the first visible step in the list pane.
	FirstVisible int `json:"first_visible,omitempty"`
	// LastVisible is the index of the last visible step in the list pane.
	LastVisible int `json:"last_visible,omitempty"`
}

// viewerStateDir resolves the sidecar directory without creating it.
func viewerStateDir() (string, error) {
	if dir := os.Getenv(ViewerStateDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".Glassbox", "viewer_state"), nil
}

// fingerprintOK reports whether fp is a plausible hex fingerprint that is
// safe to embed in a file name (no separators, no traversal).
func fingerprintOK(fp string) bool {
	if len(fp) < 16 || len(fp) > 128 {
		return false
	}
	for _, r := range fp {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func sidecarPath(fp string) (string, error) {
	if !fingerprintOK(fp) {
		return "", fmt.Errorf("invalid trace fingerprint %q", fp)
	}
	dir, err := viewerStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, strings.ToLower(fp)+".json"), nil
}

// sanitizeText strips control characters and truncates to max runes, so that
// persisted state can never smuggle escape sequences or oversized blobs back
// into the terminal.
func sanitizeText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// sanitize normalizes a ViewerState so that loaded or saved values are always
// bounded, printable, and non-negative.
func (st *ViewerState) sanitize() {
	st.SearchQuery = sanitizeText(st.SearchQuery, maxSearchQueryLen)
	st.EventFilter = sanitizeText(st.EventFilter, maxEventFilterLen)
	st.TxHash = sanitizeText(st.TxHash, maxTxHashLen)
	if st.CurrentStep < 0 {
		st.CurrentStep = 0
	}
	if st.CurrentMatch < 0 {
		st.CurrentMatch = 0
	}
	if st.ExpandedCallFrames == nil {
		st.ExpandedCallFrames = []int{}
	}
	if st.Annotations == nil {
		st.Annotations = map[string][]string{}
	}
	// Viewport bounds are validated in ValidateViewerStateReferences.
}

// ViewerStateDiagnostic records a single issue found during state validation
// or migration. Diagnostic codes are stable strings suitable for machine
// consumption and log correlation.
type ViewerStateDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidateViewerStateReferences drops references to events, steps, or panels
// that no longer exist in the current trace. It returns diagnostics for every
// dropped reference and mutates st in place so callers never have to handle
// dangling references.
//
// totalSteps is len(trace.States) — the current trace length. It must be
// non-negative.
func (st *ViewerState) ValidateViewerStateReferences(totalSteps int) []ViewerStateDiagnostic {
	if totalSteps < 0 {
		totalSteps = 0
	}
	var diags []ViewerStateDiagnostic

	// Clamp CurrentStep.
	if st.CurrentStep >= totalSteps && totalSteps > 0 {
		diags = append(diags, ViewerStateDiagnostic{
			Code:    "step_out_of_range",
			Message: fmt.Sprintf("current_step %d exceeds trace length %d, resetting to 0", st.CurrentStep, totalSteps),
		})
		st.CurrentStep = 0
	}

	// Filter ExpandedCallFrames — drop indices outside [0, totalSteps-1].
	if len(st.ExpandedCallFrames) > 0 {
		valid := st.ExpandedCallFrames[:0]
		for _, step := range st.ExpandedCallFrames {
			if step >= 0 && step < totalSteps {
				valid = append(valid, step)
			} else {
				diags = append(diags, ViewerStateDiagnostic{
					Code:    "expanded_frame_dropped",
					Message: fmt.Sprintf("expanded call frame step %d does not exist in trace (total steps: %d)", step, totalSteps),
				})
			}
		}
		st.ExpandedCallFrames = valid
	}

	// Clamp viewport.
	if totalSteps > 0 {
		if st.Viewport.FirstVisible < 0 {
			st.Viewport.FirstVisible = 0
		}
		if st.Viewport.FirstVisible >= totalSteps {
			st.Viewport.FirstVisible = totalSteps - 1
			diags = append(diags, ViewerStateDiagnostic{
				Code:    "viewport_clamped",
				Message: fmt.Sprintf("viewport first_visible clamped to %d (trace has %d steps)", st.Viewport.FirstVisible, totalSteps),
			})
		}
		if st.Viewport.LastVisible <= 0 {
			st.Viewport.LastVisible = st.Viewport.FirstVisible
		}
		if st.Viewport.LastVisible >= totalSteps {
			st.Viewport.LastVisible = totalSteps - 1
			diags = append(diags, ViewerStateDiagnostic{
				Code:    "viewport_clamped",
				Message: fmt.Sprintf("viewport last_visible clamped to %d (trace has %d steps)", st.Viewport.LastVisible, totalSteps),
			})
		}
	}

	// Validate annotation keys — drop entries whose step key is out of range.
	if len(st.Annotations) > 0 {
		for key, panels := range st.Annotations {
			var stepIdx int
			if _, err := fmt.Sscanf(key, "%d", &stepIdx); err != nil {
				diags = append(diags, ViewerStateDiagnostic{
					Code:    "annotation_key_invalid",
					Message: fmt.Sprintf("annotation key %q is not a valid step index, dropping", key),
				})
				delete(st.Annotations, key)
				continue
			}
			if stepIdx < 0 || stepIdx >= totalSteps {
				diags = append(diags, ViewerStateDiagnostic{
					Code:    "annotation_step_dropped",
					Message: fmt.Sprintf("annotation for step %d dropped (not in trace)", stepIdx),
				})
				delete(st.Annotations, key)
				continue
			}
			// Deduplicate panels.
			seen := make(map[string]bool, len(panels))
			deduped := panels[:0]
			for _, p := range panels {
				p = sanitizeText(p, 64)
				if p != "" && !seen[p] {
					seen[p] = true
					deduped = append(deduped, p)
				}
			}
			st.Annotations[key] = deduped
		}
	}

	return diags
}

// MigrateViewerState upgrades a ViewerState loaded from an older on-disk
// version to the current ViewerStateVersion. Fields introduced in later
// versions are initialised to safe zero values so the viewer always starts
// from a consistent baseline.
//
// Migration is idempotent: calling it on an already-current version is a no-op.
func MigrateViewerState(st *ViewerState) {
	if st == nil {
		return
	}
	switch st.Version {
	case 0, 1:
		// Version 1 → 2: ExpandedCallFrames, Viewport, Annotations were added.
		// Their zero values are safe defaults (empty slice, zero viewport,
		// nil map). No data transformation needed — just bump the version.
		st.Version = ViewerStateVersion
	default:
		// Unknown or future version — leave as-is. LoadViewerState will
		// reject it with a warning.
	}
}

func warnf(format string, args ...interface{}) {
	fmt.Fprintf(warnWriter, "glassbox: warning: "+format+"\n", args...)
}

// LoadViewerState returns the persisted state for the trace identified by
// fingerprint, if present and valid. A missing, corrupted, stale, or
// incompatible sidecar is never an error: corruption and version mismatches
// are reported with a warning and the sidecar is ignored, so the viewer
// always starts from defaults rather than failing or applying bad state.
//
// When a sidecar from an older version is found, it is migrated forward
// (fields from newer versions are initialised to safe defaults) and the
// upgraded version is saved back so subsequent loads avoid re-migration.
// Dangling references to events or steps that no longer exist in the
// current trace are silently dropped with diagnostics.
func LoadViewerState(fingerprint string, totalSteps ...int) (ViewerState, bool, error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	path, err := sidecarPath(fingerprint)
	if err != nil {
		return ViewerState{}, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ViewerState{}, false, nil
		}
		warnf("cannot read viewer state sidecar %s: %v — ignoring", path, err)
		return ViewerState{}, false, nil
	}

	var st ViewerState
	if err := json.Unmarshal(b, &st); err != nil {
		warnf("viewer state sidecar %s is corrupted: %v — ignoring; it will be overwritten on exit", path, err)
		return ViewerState{}, false, nil
	}
	if st.Version > ViewerStateVersion {
		warnf("viewer state sidecar %s has version %d (this build supports %d) — ignoring", path, st.Version, ViewerStateVersion)
		return ViewerState{}, false, nil
	}
	if !strings.EqualFold(st.TraceFingerprint, fingerprint) {
		warnf("viewer state sidecar %s was saved for a different trace — ignoring stale state", path)
		return ViewerState{}, false, nil
	}

	// Migrate from older version if needed. Migrated state is written back
	// so subsequent loads avoid re-migration.
	if st.Version < ViewerStateVersion {
		MigrateViewerState(&st)
		_ = saveViewerStateRaw(fingerprint, &st)
	}

	st.sanitize()

	// Validate references against the current trace if totalSteps is provided.
	// This drops dangling expanded call frames, clamps viewport bounds, and
	// removes annotations referencing non-existent steps.
	if len(totalSteps) > 0 {
		_ = st.ValidateViewerStateReferences(totalSteps[0])
	}

	return st, true, nil
}

// SaveViewerState atomically persists st as the sidecar for fingerprint. The
// record is written to a temporary file in the same directory and renamed
// into place, so a crash mid-write can never leave a truncated sidecar.
//
// If the state directory is not writable (e.g. a read-only home), persistence
// is disabled for the remainder of the process after a single warning; the
// viewer keeps working without it.
func SaveViewerState(fingerprint string, st ViewerState) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return saveViewerStateRaw(fingerprint, &st)
}

// saveViewerStateRaw is the internal implementation shared by SaveViewerState
// and the migration path in LoadViewerState. The caller must hold stateMu.
func saveViewerStateRaw(fingerprint string, st *ViewerState) error {
	if writesDisabled {
		return nil
	}
	path, err := sidecarPath(fingerprint)
	if err != nil {
		return err
	}

	st.Version = ViewerStateVersion
	st.TraceFingerprint = strings.ToLower(fingerprint)
	st.UpdatedAt = time.Now().UTC()
	st.sanitize()

	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal viewer state: %w", err)
	}

	if err := writeSidecarAtomic(path, out); err != nil {
		writesDisabled = true
		warnf("cannot write viewer state sidecar %s: %v — viewer state persistence disabled for this session", path, err)
		return err
	}
	return nil
}

func writeSidecarAtomic(path string, data []byte) error {
	return writeFileAtomic(path, data, 0o600)
}

// ResetViewerState deletes the persisted sidecar for fingerprint. It reports
// whether a sidecar existed.
func ResetViewerState(fingerprint string) (bool, error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	path, err := sidecarPath(fingerprint)
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ResetAllViewerState deletes every viewer state sidecar and returns how many
// were removed.
func ResetAllViewerState() (int, error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	dir, err := viewerStateDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	var firstErr error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}
