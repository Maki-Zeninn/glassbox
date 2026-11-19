// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	fpA = strings.Repeat("ab", 32) // 64 hex chars
	fpB = strings.Repeat("cd", 32)
)

// setupStateDir points the sidecar store at a fresh temp directory and
// re-enables writes (a prior test may have tripped the disable latch).
func setupStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(ViewerStateDirEnv, dir)
	stateMu.Lock()
	writesDisabled = false
	stateMu.Unlock()
	return dir
}

// captureWarnings redirects sidecar warnings into a buffer for assertions.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := warnWriter
	warnWriter = buf
	t.Cleanup(func() { warnWriter = old })
	return buf
}

func TestViewerStateRoundTrip(t *testing.T) {
	setupStateDir(t)
	warns := captureWarnings(t)

	in := ViewerState{
		TxHash:       "deadbeef",
		CurrentStep:  7,
		SearchQuery:  "require_auth",
		CurrentMatch: 2,
		EventFilter:  "contract_call",
		HideStdLib:   true,
	}
	if err := SaveViewerState(fpA, in); err != nil {
		t.Fatalf("SaveViewerState failed: %v", err)
	}

	out, ok, err := LoadViewerState(fpA)
	if err != nil || !ok {
		t.Fatalf("LoadViewerState = ok=%v err=%v, want ok=true err=nil", ok, err)
	}
	if out.Version != ViewerStateVersion {
		t.Errorf("Version = %d, want %d", out.Version, ViewerStateVersion)
	}
	if out.TraceFingerprint != fpA {
		t.Errorf("TraceFingerprint = %q, want %q", out.TraceFingerprint, fpA)
	}
	if out.CurrentStep != 7 || out.SearchQuery != "require_auth" ||
		out.CurrentMatch != 2 || out.EventFilter != "contract_call" ||
		!out.HideStdLib || out.TxHash != "deadbeef" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
	if out.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be stamped on save")
	}
	if warns.Len() != 0 {
		t.Errorf("unexpected warnings: %s", warns.String())
	}
}

func TestViewerStateLoadMissing(t *testing.T) {
	setupStateDir(t)
	warns := captureWarnings(t)

	_, ok, err := LoadViewerState(fpA)
	if ok || err != nil {
		t.Fatalf("LoadViewerState on missing sidecar = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if warns.Len() != 0 {
		t.Errorf("missing sidecar should not warn, got: %s", warns.String())
	}
}

func TestViewerStateCorruptedSidecarIgnoredWithWarning(t *testing.T) {
	dir := setupStateDir(t)
	warns := captureWarnings(t)

	path := filepath.Join(dir, fpA+".json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, ok, err := LoadViewerState(fpA)
	if ok || err != nil {
		t.Fatalf("corrupted sidecar: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if !strings.Contains(warns.String(), "corrupted") {
		t.Errorf("expected corruption warning, got: %s", warns.String())
	}
}

func TestViewerStateUnsupportedVersionIgnoredWithWarning(t *testing.T) {
	dir := setupStateDir(t)
	warns := captureWarnings(t)

	body := `{"version": 99, "trace_fingerprint": "` + fpA + `", "current_step": 3}`
	if err := os.WriteFile(filepath.Join(dir, fpA+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, ok, err := LoadViewerState(fpA)
	if ok || err != nil {
		t.Fatalf("future-version sidecar: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if !strings.Contains(warns.String(), "unsupported version") {
		t.Errorf("expected version warning, got: %s", warns.String())
	}
}

func TestViewerStateFingerprintMismatchIgnored(t *testing.T) {
	dir := setupStateDir(t)
	warns := captureWarnings(t)

	if err := SaveViewerState(fpA, ViewerState{CurrentStep: 5}); err != nil {
		t.Fatal(err)
	}
	// Simulate a stale record: the sidecar file for fpB contains state that
	// was actually saved for fpA (i.e. the trace content changed).
	data, err := os.ReadFile(filepath.Join(dir, fpA+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fpB+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, ok, err := LoadViewerState(fpB)
	if ok || err != nil {
		t.Fatalf("stale sidecar: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if !strings.Contains(warns.String(), "different trace") {
		t.Errorf("expected stale-state warning, got: %s", warns.String())
	}
}

func TestViewerStateSaveIsAtomicNoTempLeftovers(t *testing.T) {
	dir := setupStateDir(t)

	if err := SaveViewerState(fpA, ViewerState{CurrentStep: 1}); err != nil {
		t.Fatal(err)
	}
	if err := SaveViewerState(fpA, ViewerState{CurrentStep: 2}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fpA+".json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected exactly one sidecar, got %v", names)
	}

	st, ok, _ := LoadViewerState(fpA)
	if !ok || st.CurrentStep != 2 {
		t.Fatalf("expected latest write to win, got ok=%v step=%d", ok, st.CurrentStep)
	}
}

func TestViewerStateReadOnlyLocationDisablesWrites(t *testing.T) {
	// Point the state dir at a regular file so MkdirAll must fail on every
	// platform, simulating an unwritable location.
	base := t.TempDir()
	blocker := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ViewerStateDirEnv, blocker)
	stateMu.Lock()
	writesDisabled = false
	stateMu.Unlock()
	warns := captureWarnings(t)

	if err := SaveViewerState(fpA, ViewerState{CurrentStep: 1}); err == nil {
		t.Fatal("expected first save into unwritable location to error")
	}
	if !strings.Contains(warns.String(), "persistence disabled") {
		t.Errorf("expected disable warning, got: %s", warns.String())
	}

	// Subsequent saves are silently skipped — no error, no extra warning.
	warnLen := warns.Len()
	if err := SaveViewerState(fpA, ViewerState{CurrentStep: 2}); err != nil {
		t.Fatalf("save after disable should be a silent no-op, got: %v", err)
	}
	if warns.Len() != warnLen {
		t.Errorf("save after disable should not warn again, got: %s", warns.String())
	}
}

func TestViewerStateSanitizesHostileContent(t *testing.T) {
	setupStateDir(t)

	in := ViewerState{
		CurrentStep:  -4,
		CurrentMatch: -1,
		SearchQuery:  "evil\x1b[2Jquery\x00" + strings.Repeat("a", maxSearchQueryLen*2),
		EventFilter:  "call\x07",
	}
	if err := SaveViewerState(fpA, in); err != nil {
		t.Fatal(err)
	}

	out, ok, _ := LoadViewerState(fpA)
	if !ok {
		t.Fatal("expected sanitized state to load")
	}
	if out.CurrentStep != 0 || out.CurrentMatch != 0 {
		t.Errorf("negative counters should clamp to 0, got step=%d match=%d", out.CurrentStep, out.CurrentMatch)
	}
	if strings.ContainsAny(out.SearchQuery, "\x00\x1b\x07") || strings.ContainsAny(out.EventFilter, "\x00\x1b\x07") {
		t.Errorf("control characters must be stripped, got query=%q filter=%q", out.SearchQuery, out.EventFilter)
	}
	if len(out.SearchQuery) > maxSearchQueryLen {
		t.Errorf("search query should be truncated to %d, got %d", maxSearchQueryLen, len(out.SearchQuery))
	}
}

func TestViewerStateRejectsUnsafeFingerprints(t *testing.T) {
	setupStateDir(t)

	for _, fp := range []string{"", "short", "../../../../etc/passwd", strings.Repeat("zz", 32), fpA + "/.."} {
		if err := SaveViewerState(fp, ViewerState{}); err == nil {
			t.Errorf("SaveViewerState(%q) should reject unsafe fingerprint", fp)
		}
		if _, ok, err := LoadViewerState(fp); ok || err != nil {
			t.Errorf("LoadViewerState(%q) = ok=%v err=%v, want ok=false err=nil", fp, ok, err)
		}
	}
}

func TestViewerStateReset(t *testing.T) {
	dir := setupStateDir(t)

	if err := SaveViewerState(fpA, ViewerState{CurrentStep: 3}); err != nil {
		t.Fatal(err)
	}

	existed, err := ResetViewerState(fpA)
	if err != nil || !existed {
		t.Fatalf("ResetViewerState = existed=%v err=%v, want existed=true err=nil", existed, err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, fpA+".json")); !os.IsNotExist(statErr) {
		t.Error("sidecar should be deleted after reset")
	}

	existed, err = ResetViewerState(fpA)
	if err != nil || existed {
		t.Fatalf("second reset = existed=%v err=%v, want existed=false err=nil", existed, err)
	}
}

func TestViewerStateResetAll(t *testing.T) {
	setupStateDir(t)

	if err := SaveViewerState(fpA, ViewerState{}); err != nil {
		t.Fatal(err)
	}
	if err := SaveViewerState(fpB, ViewerState{}); err != nil {
		t.Fatal(err)
	}

	removed, err := ResetAllViewerState()
	if err != nil || removed != 2 {
		t.Fatalf("ResetAllViewerState = %d, %v; want 2, nil", removed, err)
	}

	removed, err = ResetAllViewerState()
	if err != nil || removed != 0 {
		t.Fatalf("second ResetAllViewerState = %d, %v; want 0, nil", removed, err)
	}
}
