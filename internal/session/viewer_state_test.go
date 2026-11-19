// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestViewerStateMigrateV1toV2(t *testing.T) {
	st := &ViewerState{
		Version:          1,
		TraceFingerprint: "abcdef1234567890abcdef1234567890",
		CurrentStep:      5,
		SearchQuery:      "test",
		EventFilter:      "trap",
		HideStdLib:       true,
	}

	MigrateViewerState(st)

	if st.Version != ViewerStateVersion {
		t.Errorf("expected version %d after migration, got %d", ViewerStateVersion, st.Version)
	}
	if st.ExpandedCallFrames == nil {
		t.Error("ExpandedCallFrames should be initialized after migration")
	}
	if st.Annotations == nil {
		t.Error("Annotations should be initialized after migration")
	}
}

func TestViewerStateMigrateNilSafe(t *testing.T) {
	MigrateViewerState(nil)
}

func TestViewerStateMigrateCurrentVersionNoop(t *testing.T) {
	st := &ViewerState{
		Version:          ViewerStateVersion,
		TraceFingerprint: "abcdef1234567890abcdef1234567890",
	}
	original := *st
	MigrateViewerState(st)
	if st.Version != original.Version {
		t.Errorf("migration should not change version %d", st.Version)
	}
}

func TestValidateViewerStateReferences(t *testing.T) {
	tests := []struct {
		name       string
		st         ViewerState
		totalSteps int
		wantDiags  int
		wantStep   int
	}{
		{
			name: "current step out of range",
			st: ViewerState{
				CurrentStep: 100,
			},
			totalSteps: 5,
			wantDiags:  1,
			wantStep:   0,
		},
		{
			name: "expanded frame dropped",
			st: ViewerState{
				CurrentStep:       2,
				ExpandedCallFrames: []int{0, 50, 2},
			},
			totalSteps: 3,
			wantDiags:  1,
		},
		{
			name: "viewport clamped",
			st: ViewerState{
				CurrentStep: 0,
				Viewport: ViewerViewport{
					FirstVisible: 200,
					LastVisible:  300,
				},
			},
			totalSteps: 5,
			wantDiags:  2,
		},
		{
			name: "annotation step dropped",
			st: ViewerState{
				Annotations: map[string][]string{
					"0":  {"host_state"},
					"99": {"memory"},
				},
			},
			totalSteps: 3,
			wantDiags:  1,
		},
		{
			name: "annotation key invalid",
			st: ViewerState{
				Annotations: map[string][]string{
					"abc": {"host_state"},
				},
			},
			totalSteps: 3,
			wantDiags:  1,
		},
		{
			name: "valid state no diagnostics",
			st: ViewerState{
				CurrentStep:       2,
				ExpandedCallFrames: []int{0, 1},
				Viewport:          ViewerViewport{FirstVisible: 0, LastVisible: 4},
				Annotations: map[string][]string{
					"0": {"host_state"},
					"1": {"memory", "cost"},
				},
			},
			totalSteps: 5,
			wantDiags:  0,
		},
		{
			name: "annotation panel deduplication",
			st: ViewerState{
				Annotations: map[string][]string{
					"0": {"host_state", "host_state", "memory"},
				},
			},
			totalSteps: 3,
			wantDiags:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := tt.st.ValidateViewerStateReferences(tt.totalSteps)
			if len(diags) != tt.wantDiags {
				t.Errorf("expected %d diagnostics, got %d: %v", tt.wantDiags, len(diags), diags)
			}
			if tt.wantStep > 0 && tt.st.CurrentStep != tt.wantStep {
				t.Errorf("expected CurrentStep=%d, got %d", tt.wantStep, tt.st.CurrentStep)
			}
		})
	}
}

func TestLoadViewerStateMigration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ViewerStateDirEnv, dir)

	fp := "abcdef1234567890abcdef1234567890"
	path := filepath.Join(dir, fp+".json")

	// Write a version 1 sidecar.
	v1 := ViewerState{
		Version:          1,
		TraceFingerprint: fp,
		CurrentStep:      3,
		EventFilter:      "trap",
	}
	b, err := json.MarshalIndent(v1, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	// Load — should migrate to v2.
	st, ok, err := LoadViewerState(fp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected state to be found")
	}
	if st.Version != ViewerStateVersion {
		t.Errorf("expected version %d after migration, got %d", ViewerStateVersion, st.Version)
	}
	if st.CurrentStep != 3 {
		t.Errorf("expected CurrentStep=3, got %d", st.CurrentStep)
	}
	if st.EventFilter != "trap" {
		t.Errorf("expected EventFilter=trap, got %s", st.EventFilter)
	}
}

func TestLoadViewerStateFutureVersionIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ViewerStateDirEnv, dir)

	fp := "abcdef1234567890abcdef1234567890"
	path := filepath.Join(dir, fp+".json")

	future := ViewerState{
		Version:          999,
		TraceFingerprint: fp,
		CurrentStep:      5,
	}
	b, err := json.MarshalIndent(future, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, ok, err := LoadViewerState(fp)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected future version sidecar to be ignored")
	}
}

func TestLoadViewerStateCorruptIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ViewerStateDirEnv, dir)

	fp := "abcdef1234567890abcdef1234567890"
	path := filepath.Join(dir, fp+".json")

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, ok, err := LoadViewerState(fp)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected corrupt sidecar to be ignored")
	}
}

func TestLoadViewerStateFingerprintMismatchIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ViewerStateDirEnv, dir)

	fp := "abcdef1234567890abcdef1234567890"
	path := filepath.Join(dir, fp+".json")

	st := ViewerState{
		Version:          ViewerStateVersion,
		TraceFingerprint: "00000000000000000000000000000000",
		CurrentStep:      5,
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, ok, err := LoadViewerState(fp)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected fingerprint mismatch to be ignored")
	}
}

func TestSaveAndLoadViewerStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ViewerStateDirEnv, dir)

	fp := "abcdef1234567890abcdef1234567890"

	original := ViewerState{
		TxHash:            "abc123",
		CurrentStep:       7,
		SearchQuery:       "test query",
		CurrentMatch:      3,
		EventFilter:       "contract_call",
		HideStdLib:        true,
		ExpandedCallFrames: []int{0, 2, 5},
		Viewport:          ViewerViewport{FirstVisible: 2, LastVisible: 12},
		Annotations: map[string][]string{
			"0": {"host_state", "cost"},
			"5": {"memory"},
		},
	}

	if err := SaveViewerState(fp, original); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := LoadViewerState(fp, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected state to be found")
	}
	if loaded.CurrentStep != original.CurrentStep {
		t.Errorf("CurrentStep: got %d, want %d", loaded.CurrentStep, original.CurrentStep)
	}
	if loaded.SearchQuery != original.SearchQuery {
		t.Errorf("SearchQuery: got %q, want %q", loaded.SearchQuery, original.SearchQuery)
	}
	if loaded.EventFilter != original.EventFilter {
		t.Errorf("EventFilter: got %q, want %q", loaded.EventFilter, original.EventFilter)
	}
	if len(loaded.ExpandedCallFrames) != 3 {
		t.Errorf("ExpandedCallFrames length: got %d, want 3", len(loaded.ExpandedCallFrames))
	}
	if loaded.Viewport.FirstVisible != 2 {
		t.Errorf("Viewport.FirstVisible: got %d, want 2", loaded.Viewport.FirstVisible)
	}
	if len(loaded.Annotations) != 2 {
		t.Errorf("Annotations length: got %d, want 2", len(loaded.Annotations))
	}
}

func TestViewerStateAnnotationsDroppedWhenStepInvalid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ViewerStateDirEnv, dir)

	fp := "abcdef1234567890abcdef1234567890"

	st := ViewerState{
		Annotations: map[string][]string{
			"0": {"host_state"},
			"5": {"memory"},
		},
	}
	if err := SaveViewerState(fp, st); err != nil {
		t.Fatal(err)
	}

	// Load with totalSteps=3 — step 5 should be dropped.
	loaded, ok, err := LoadViewerState(fp, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected state to be found")
	}
	if _, exists := loaded.Annotations["5"]; exists {
		t.Error("annotation for step 5 should have been dropped")
	}
	if _, exists := loaded.Annotations["0"]; !exists {
		t.Error("annotation for step 0 should have been preserved")
	}
}

func TestViewerStatePortableVsMachineLocal(t *testing.T) {
	// ViewerState should NOT contain machine-local paths, commands, or
	// executable content — only portable display preferences.
	st := ViewerState{
		TxHash:            "abc123",
		CurrentStep:       5,
		SearchQuery:       "test",
		EventFilter:       "trap",
		HideStdLib:        true,
		ExpandedCallFrames: []int{0, 1},
		Viewport:          ViewerViewport{FirstVisible: 0, LastVisible: 10},
		Annotations: map[string][]string{
			"0": {"host_state"},
		},
	}

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	// Verify no file paths or executables appear in the serialized form.
	s := string(b)
	for _, forbidden := range []string{"/bin/", "/usr/", "C:\\", ".exe", "go run"} {
		if contains(s, forbidden) {
			t.Errorf("viewer state contains machine-local content %q", forbidden)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
