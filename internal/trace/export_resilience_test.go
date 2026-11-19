// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeTrace(t *testing.T) {
	tests := []struct {
		name         string
		trace        *ExecutionTrace
		expectErrors int
		checkField   func(*ExecutionTrace) bool
	}{
		{
			name:         "nil trace",
			trace:        nil,
			expectErrors: 1,
		},
		{
			name: "missing start time",
			trace: &ExecutionTrace{
				TransactionHash: "test-hash",
				States:          []ExecutionState{{Operation: "test"}},
			},
			expectErrors: 1,
			checkField: func(t *ExecutionTrace) bool {
				return !t.StartTime.IsZero()
			},
		},
		{
			name: "missing transaction hash",
			trace: &ExecutionTrace{
				StartTime: time.Now(),
				EndTime:   time.Now().Add(time.Minute),
				States:    []ExecutionState{{Operation: "test"}},
			},
			expectErrors: 1,
			checkField: func(t *ExecutionTrace) bool {
				return t.TransactionHash != ""
			},
		},
		{
			name: "step index mismatch",
			trace: &ExecutionTrace{
				TransactionHash: "test-hash",
				StartTime:       time.Now(),
				EndTime:         time.Now().Add(time.Minute),
				States: []ExecutionState{
					{Step: 0, Operation: "test1"},
					{Step: 5, Operation: "test2"}, // Wrong index
				},
			},
			expectErrors: 1,
			checkField: func(t *ExecutionTrace) bool {
				return t.States[1].Step == 1
			},
		},
		{
			name: "missing timestamps in states",
			trace: &ExecutionTrace{
				TransactionHash: "test-hash",
				StartTime:       time.Now(),
				EndTime:         time.Now().Add(time.Minute),
				States: []ExecutionState{
					{Step: 0, Operation: "test1"},
					{Step: 1, Operation: "test2"},
				},
			},
			expectErrors: 2,
			checkField: func(t *ExecutionTrace) bool {
				return !t.States[0].Timestamp.IsZero() && !t.States[1].Timestamp.IsZero()
			},
		},
		{
			name: "overly long error message",
			trace: &ExecutionTrace{
				TransactionHash: "test-hash",
				StartTime:       time.Now(),
				EndTime:         time.Now().Add(time.Minute),
				States: []ExecutionState{
					{
						Step:      0,
						Operation: "test",
						Timestamp: time.Now(),
						Error:     strings.Repeat("x", 15000),
					},
				},
			},
			expectErrors: 1,
			checkField: func(t *ExecutionTrace) bool {
				return len(t.States[0].Error) <= 10000+20 // truncated + "... (truncated)"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized, errs := SanitizeTrace(tt.trace)
			
			if len(errs) != tt.expectErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.expectErrors, len(errs), errs)
			}
			
			if tt.checkField != nil && sanitized != nil {
				if !tt.checkField(sanitized) {
					t.Errorf("sanitized trace failed field check")
				}
			}
		})
	}
}

func TestExportWithResilience(t *testing.T) {
	trace := NewExecutionTrace("test-tx-hash", 10)
	trace.AddState(ExecutionState{
		Operation:  "test_op",
		ContractID: "test-contract",
		Function:   "test_func",
	})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "resilient-trace.json")

	opts := ExportOptions{}
	recoveryOpts := DefaultRecoveryOptions()
	recoveryOpts.MaxRetries = 2

	err := ExportWithResilience(trace, "json", outputPath, opts, recoveryOpts)
	if err != nil {
		t.Fatalf("ExportWithResilience failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatal("exported file does not exist")
	}

	// Verify metadata file exists
	metaPath := outputPath + ".meta.json"
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Fatal("metadata file does not exist")
	}

	// Verify content
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read exported file: %v", err)
	}

	var loaded ExecutionTrace
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to unmarshal exported trace: %v", err)
	}

	if loaded.TransactionHash != trace.TransactionHash {
		t.Errorf("transaction hash mismatch: got %s, want %s", loaded.TransactionHash, trace.TransactionHash)
	}
}

func TestAtomicWriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "atomic-test.txt")
	content := []byte("test content")

	err := atomicWriteFile(path, content)
	if err != nil {
		t.Fatalf("atomicWriteFile failed: %v", err)
	}

	// Verify file exists and has correct content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	if string(data) != string(content) {
		t.Errorf("content mismatch: got %s, want %s", data, content)
	}

	// Verify no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Errorf("temp file not cleaned up: %s", entry.Name())
		}
	}
}

func TestBackupFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := filepath.Join(tmpDir, "original.txt")
	originalContent := []byte("original content")

	// Create original file
	if err := os.WriteFile(originalPath, originalContent, 0o644); err != nil {
		t.Fatalf("failed to create original file: %v", err)
	}

	// Backup the file
	if err := backupFile(originalPath); err != nil {
		t.Fatalf("backupFile failed: %v", err)
	}

	// Find backup file
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}

	backupFound := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "original.txt.bak.") {
			backupFound = true
			
			// Verify backup content
			backupPath := filepath.Join(tmpDir, entry.Name())
			data, err := os.ReadFile(backupPath)
			if err != nil {
				t.Fatalf("failed to read backup file: %v", err)
			}
			
			if string(data) != string(originalContent) {
				t.Errorf("backup content mismatch: got %s, want %s", data, originalContent)
			}
			break
		}
	}

	if !backupFound {
		t.Error("backup file not created")
	}
}

func TestVerifyExport(t *testing.T) {
	trace := NewExecutionTrace("verify-test", 10)
	trace.AddState(ExecutionState{Operation: "test"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "verify-trace.json")

	// Export with resilience (creates metadata)
	opts := ExportOptions{}
	recoveryOpts := DefaultRecoveryOptions()
	
	if err := ExportWithResilience(trace, "json", outputPath, opts, recoveryOpts); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Verify export
	if err := VerifyExport(outputPath); err != nil {
		t.Fatalf("VerifyExport failed: %v", err)
	}

	// Corrupt the file and verify detection
	data, _ := os.ReadFile(outputPath)
	corrupted := append(data, []byte("corrupted")...)
	os.WriteFile(outputPath, corrupted, 0o644)

	err := VerifyExport(outputPath)
	if err == nil {
		t.Fatal("VerifyExport should detect corrupted file")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected checksum mismatch error, got: %v", err)
	}
}

func TestRecoverTrace(t *testing.T) {
	// Create a trace with some issues
	trace := &ExecutionTrace{
		TransactionHash: "recover-test",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Minute),
		States: []ExecutionState{
			{Step: 0, Operation: "test1", Timestamp: time.Now()},
			{Step: 5, Operation: "test2"}, // Wrong step, missing timestamp
		},
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "recover-trace.json")

	// Write trace to file
	data, _ := json.MarshalIndent(trace, "", "  ")
	os.WriteFile(path, data, 0o644)

	// Recover trace
	recovered, errs := RecoverTrace(path)
	if recovered == nil {
		t.Fatalf("RecoverTrace failed: %v", errs)
	}

	// Should have recovery errors (step mismatch, missing timestamp)
	if len(errs) == 0 {
		t.Error("expected recovery errors for problematic trace")
	}

	// Verify sanitization fixed issues
	if recovered.States[1].Step != 1 {
		t.Errorf("step index not fixed: got %d, want 1", recovered.States[1].Step)
	}

	if recovered.States[1].Timestamp.IsZero() {
		t.Error("timestamp not interpolated")
	}
}

func TestComputeChecksum(t *testing.T) {
	data := []byte("test data")
	checksum1 := computeChecksum(data)
	checksum2 := computeChecksum(data)

	if checksum1 != checksum2 {
		t.Error("checksum not deterministic")
	}

	if len(checksum1) != 64 { // SHA-256 produces 64 hex chars
		t.Errorf("unexpected checksum length: got %d, want 64", len(checksum1))
	}

	// Different data should produce different checksum
	differentData := []byte("different data")
	checksum3 := computeChecksum(differentData)
	if checksum1 == checksum3 {
		t.Error("different data produced same checksum")
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		retryable  bool
	}{
		{
			name:      "nil error",
			err:       nil,
			retryable: false,
		},
		{
			name:      "temporarily unavailable",
			err:       fmt.Errorf("resource temporarily unavailable"),
			retryable: true,
		},
		{
			name:      "connection reset",
			err:       fmt.Errorf("connection reset by peer"),
			retryable: true,
		},
		{
			name:      "broken pipe",
			err:       fmt.Errorf("broken pipe"),
			retryable: true,
		},
		{
			name:      "permission denied",
			err:       fmt.Errorf("permission denied"),
			retryable: false,
		},
		{
			name:      "file not found",
			err:       fmt.Errorf("file not found"),
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.retryable {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, result, tt.retryable)
			}
		})
	}
}

func TestRecoverFromJSON(t *testing.T) {
	// Valid JSON
	trace := &ExecutionTrace{
		TransactionHash: "json-test",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Minute),
		States: []ExecutionState{
			{Step: 0, Operation: "test", Timestamp: time.Now()},
		},
	}

	data, _ := json.Marshal(trace)
	
	recovered, err := recoverFromJSON(data)
	if err != nil {
		t.Fatalf("recoverFromJSON failed on valid JSON: %v", err)
	}

	if recovered.TransactionHash != trace.TransactionHash {
		t.Errorf("transaction hash mismatch")
	}

	// Invalid JSON
	invalidData := []byte("{invalid json")
	_, err = recoverFromJSON(invalidData)
	if err == nil {
		t.Error("recoverFromJSON should fail on invalid JSON")
	}
}

func TestRecoverFromJSON_InvalidJSON(t *testing.T) {
	invalidData := []byte("{invalid json")
	_, err := recoverFromJSON(invalidData)
	if err == nil {
		t.Error("recoverFromJSON should fail on invalid JSON")
	}
	if !strings.Contains(err.Error(), "JSON") && !strings.Contains(err.Error(), "parse") && !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should describe the JSON parsing failure, got: %v", err)
	}
}

// ── VerifyExport — step count mismatch ───────────────────────────────────────

func TestVerifyExport_StepCountMismatch_ReturnsError(t *testing.T) {
	trace := NewExecutionTrace("verify-step-count", 10)
	trace.AddState(ExecutionState{Operation: "step0"})
	trace.AddState(ExecutionState{Operation: "step1"})
	trace.AddState(ExecutionState{Operation: "step2"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	// Export normally — creates trace file + .meta.json
	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Now truncate the trace file to remove two states, simulating corruption/truncation.
	// We do this by loading the JSON, removing states, and re-writing raw bytes.
	rawData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var full ExecutionTrace
	if err := json.Unmarshal(rawData, &full); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Keep only the first state to create a mismatch against StepCount=3 in metadata
	full.States = full.States[:1]
	truncated, _ := json.MarshalIndent(full, "", "  ")
	if err := os.WriteFile(outputPath, truncated, 0o644); err != nil {
		t.Fatalf("write truncated: %v", err)
	}

	err = VerifyExport(outputPath)
	if err == nil {
		t.Fatal("VerifyExport should detect step count mismatch")
	}
	if !strings.Contains(err.Error(), "step count mismatch") {
		t.Errorf("expected 'step count mismatch' in error, got: %v", err)
	}
}

func TestVerifyExport_StepCountMatch_NoError(t *testing.T) {
	trace := NewExecutionTrace("verify-step-match", 10)
	trace.AddState(ExecutionState{Operation: "step0"})
	trace.AddState(ExecutionState{Operation: "step1"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	if err := VerifyExport(outputPath); err != nil {
		t.Errorf("VerifyExport should pass for unmodified export, got: %v", err)
	}
}

func TestVerifyExport_NoMetadata_ReturnsDescriptiveError(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "no-meta.json")
	if err := os.WriteFile(tracePath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := VerifyExport(tracePath)
	if err == nil {
		t.Fatal("expected error when metadata file is absent")
	}
	if !strings.Contains(err.Error(), "metadata file not found") {
		t.Errorf("error should say 'metadata file not found', got: %v", err)
	}
	// Must also hint that the file can still be used
	if !strings.Contains(err.Error(), "can still be used") {
		t.Errorf("error should mention file can still be used, got: %v", err)
	}
}

func TestVerifyExport_CorruptMetadata_ReturnsDescriptiveError(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "trace.json")
	metaPath := tracePath + ".meta.json"

	if err := os.WriteFile(tracePath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	if err := os.WriteFile(metaPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	err := VerifyExport(tracePath)
	if err == nil {
		t.Fatal("expected error for corrupt metadata")
	}
	if !strings.Contains(err.Error(), "parse") && !strings.Contains(err.Error(), "metadata") {
		t.Errorf("error should describe metadata parse failure, got: %v", err)
	}
}

func TestVerifyExport_FormatMismatch_ReturnsDescriptiveError(t *testing.T) {
	trace := NewExecutionTrace("format-mismatch-test", 10)
	trace.AddState(ExecutionState{Operation: "step0"})

	tmpDir := t.TempDir()
	// Export as JSON but save to a .html file name
	outputPath := filepath.Join(tmpDir, "trace.html")

	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	err := VerifyExport(outputPath)
	if err == nil {
		t.Fatal("expected error for format/extension mismatch")
	}
	if !strings.Contains(err.Error(), "format mismatch") {
		t.Errorf("expected 'format mismatch' in error, got: %v", err)
	}
}

// ── RecoverTrace — pre-recovery integrity check ───────────────────────────────

func TestRecoverTrace_WithIntegrityViolation_WarnsAndContinues(t *testing.T) {
	trace := NewExecutionTrace("recover-integrity-test", 10)
	trace.AddState(ExecutionState{Operation: "step0"})
	trace.AddState(ExecutionState{Operation: "step1"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	// Export to create both trace and metadata files
	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Tamper with the trace file to trigger checksum mismatch
	rawData, _ := os.ReadFile(outputPath)
	tampered := append(rawData, []byte("\n// tampered")...)
	if err := os.WriteFile(outputPath, tampered, 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}

	// RecoverTrace should detect the integrity violation but still return the trace
	recovered, errs := RecoverTrace(outputPath)
	if recovered == nil {
		t.Fatal("RecoverTrace should return a trace despite integrity violation")
	}
	if len(errs) == 0 {
		t.Fatal("RecoverTrace should report errors when integrity check failed")
	}
	// One of the errors should mention the integrity/checksum issue
	foundIntegrityErr := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "integrity") || strings.Contains(e.Error(), "checksum") {
			foundIntegrityErr = true
			break
		}
	}
	if !foundIntegrityErr {
		t.Errorf("expected an integrity/checksum error in recovery errors, got: %v", errs)
	}
}

func TestRecoverTrace_NoMetadata_NoIntegrityError(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "no-meta-recover",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Minute),
		States: []ExecutionState{
			{Step: 0, Operation: "op", Timestamp: time.Now()},
		},
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "trace.json")
	data, _ := json.MarshalIndent(trace, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Deliberately NO .meta.json file

	recovered, errs := RecoverTrace(path)
	if recovered == nil {
		t.Fatal("RecoverTrace should succeed without a metadata file")
	}
	// Must not report a spurious "metadata not found" error — that's an expected condition
	for _, e := range errs {
		if strings.Contains(e.Error(), "metadata file not found") {
			t.Errorf("RecoverTrace should not surface 'metadata file not found' as an error: %v", e)
		}
	}
}

func TestRecoverTrace_NonJSONFormat_ReturnsActionableError(t *testing.T) {
	tmpDir := t.TempDir()
	htmlPath := filepath.Join(tmpDir, "trace.html")
	if err := os.WriteFile(htmlPath, []byte("<html><body>trace</body></html>"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	recovered, errs := RecoverTrace(htmlPath)
	if recovered != nil {
		t.Error("RecoverTrace should not return a trace from HTML format")
	}
	if len(errs) == 0 {
		t.Fatal("RecoverTrace should return an error for non-JSON format")
	}
	msg := errs[len(errs)-1].Error()
	if !strings.Contains(msg, "JSON") {
		t.Errorf("error should mention JSON format requirement, got: %v", msg)
	}
	if !strings.Contains(msg, "Recommendation") {
		t.Errorf("error should include a Recommendation, got: %v", msg)
	}
}

// ── ExportWithResilience — CLIVersion and Hostname in metadata ────────────────

func TestExportWithResilience_MetadataIncludesCLIVersion(t *testing.T) {
	trace := NewExecutionTrace("cli-version-test", 10)
	trace.AddState(ExecutionState{Operation: "step0"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	opts := ExportOptions{}
	recoveryOpts := DefaultRecoveryOptions()
	recoveryOpts.EnableMetadata = true

	if err := ExportWithResilience(trace, "json", outputPath, opts, recoveryOpts); err != nil {
		t.Fatalf("ExportWithResilience failed: %v", err)
	}

	metaPath := outputPath + ".meta.json"
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}

	var meta ExportMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	// CLIVersion should be set (it's from version.Version which defaults to "0.0.0-dev")
	if meta.CLIVersion == "" {
		t.Error("metadata.CLIVersion should be set")
	}
	// Version should match CurrentJSONSchemaVersion
	if meta.Version != CurrentJSONSchemaVersion {
		t.Errorf("metadata.Version = %q, want %q", meta.Version, CurrentJSONSchemaVersion)
	}
}

func TestExportWithResilience_MetadataStepCountMatchesTrace(t *testing.T) {
	trace := NewExecutionTrace("step-count-meta", 10)
	trace.AddState(ExecutionState{Operation: "step0"})
	trace.AddState(ExecutionState{Operation: "step1"})
	trace.AddState(ExecutionState{Operation: "step2"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("ExportWithResilience failed: %v", err)
	}

	metaPath := outputPath + ".meta.json"
	metaData, _ := os.ReadFile(metaPath)
	var meta ExportMetadata
	json.Unmarshal(metaData, &meta)

	if meta.StepCount != 3 {
		t.Errorf("metadata.StepCount = %d, want 3", meta.StepCount)
	}
}

// ── VerifyExport — metadata Version validation ────────────────────────────────

func TestVerifyExport_MetadataVersion_CurrentVersionPasses(t *testing.T) {
	trace := NewExecutionTrace("meta-ver-ok", 10)
	trace.AddState(ExecutionState{Operation: "op"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("ExportWithResilience failed: %v", err)
	}

	// The metadata written by ExportWithResilience uses CurrentJSONSchemaVersion,
	// so VerifyExport must accept it without error.
	if err := VerifyExport(outputPath); err != nil {
		t.Errorf("VerifyExport should pass for current schema version, got: %v", err)
	}
}

func TestVerifyExport_MetadataVersion_UnsupportedVersionRejected(t *testing.T) {
	trace := NewExecutionTrace("meta-ver-bad", 10)
	trace.AddState(ExecutionState{Operation: "op"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	// Export normally to get a valid trace file.
	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("ExportWithResilience failed: %v", err)
	}

	// Overwrite the metadata file with an unsupported schema version to
	// simulate a file produced by a future binary.
	meta := ExportMetadata{
		Version:    "99.0", // deliberately unsupported
		Format:     "json",
		StepCount:  1,
		Checksum:   "", // disable checksum check so only version triggers
		ExportedAt: time.Now(),
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(outputPath+".meta.json", metaData, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	err := VerifyExport(outputPath)
	if err == nil {
		t.Fatal("VerifyExport should reject metadata with unsupported schema_version")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error should mention schema_version, got: %v", err)
	}
	if !strings.Contains(err.Error(), "99.0") {
		t.Errorf("error should echo the bad version, got: %v", err)
	}
	// Must be actionable.
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("error should include a Fix hint, got: %v", err)
	}
}

func TestVerifyExport_MetadataVersion_EmptyVersionAllowed(t *testing.T) {
	// A metadata file with an empty Version field represents a pre-versioning
	// export; VerifyExport must not reject it.
	trace := NewExecutionTrace("meta-ver-empty", 10)
	trace.AddState(ExecutionState{Operation: "op"})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "trace.json")

	if err := ExportWithResilience(trace, "json", outputPath, ExportOptions{}, DefaultRecoveryOptions()); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Read back the real checksum so we can build a valid-but-unversioned meta.
	rawTrace, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}

	meta := ExportMetadata{
		Version:   "", // pre-versioning — must be tolerated
		Format:    "json",
		StepCount: 1,
		Checksum:  computeChecksum(rawTrace),
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(outputPath+".meta.json", metaData, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	if err := VerifyExport(outputPath); err != nil {
		t.Errorf("VerifyExport should allow empty Version (pre-versioning export), got: %v", err)
	}
}
