// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/version"
)

// ExportMetadata contains metadata about an exported trace for recovery and validation
type ExportMetadata struct {
	Version         string    `json:"version"`
	Format          string    `json:"format"`
	TransactionHash string    `json:"transaction_hash"`
	ExportedAt      time.Time `json:"exported_at"`
	StepCount       int       `json:"step_count"`
	Checksum        string    `json:"checksum"`
	CLIVersion      string    `json:"cli_version,omitempty"`
	Hostname        string    `json:"hostname,omitempty"`
}

// ExportRecoveryOptions configures resilient export behavior
type ExportRecoveryOptions struct {
	// EnableChecksum computes and stores a checksum for integrity verification
	EnableChecksum bool
	
	// EnableMetadata writes a companion .meta.json file with export metadata
	EnableMetadata bool
	
	// AtomicWrite uses atomic write-rename to prevent partial file writes
	AtomicWrite bool
	
	// BackupExisting creates a backup of existing files before overwrite
	BackupExisting bool
	
	// MaxRetries specifies number of retries for transient failures
	MaxRetries int
	
	// RetryDelay specifies delay between retries
	RetryDelay time.Duration
}

// DefaultRecoveryOptions returns sensible defaults for resilient export
func DefaultRecoveryOptions() ExportRecoveryOptions {
	return ExportRecoveryOptions{
		EnableChecksum: true,
		EnableMetadata: true,
		AtomicWrite:    true,
		BackupExisting: false,
		MaxRetries:     3,
		RetryDelay:     100 * time.Millisecond,
	}
}

// ExportWithResilience exports a trace with error recovery and resilience features
func ExportWithResilience(trace *ExecutionTrace, format, outputPath string, opts ExportOptions, recoveryOpts ExportRecoveryOptions) error {
	// Validate inputs first
	if err := ValidateTraceExportParams(trace, format, outputPath, opts); err != nil {
		return fmt.Errorf("trace export validation failed: %w", err)
	}
	
	if err := ValidateTraceFormatCompatibility(trace, format); err != nil {
		return fmt.Errorf("trace format compatibility check failed: %w", err)
	}
	
	// Sanitize the trace to handle potentially corrupted data
	sanitized, sanitizeErrs := SanitizeTrace(trace)
	if len(sanitizeErrs) > 0 {
		// Log warnings but continue with sanitized trace
		for _, err := range sanitizeErrs {
			fmt.Fprintf(os.Stderr, "Warning: trace sanitization: %v\n", err)
		}
	}
	
	// Generate content with retry logic
	var content string
	var err error
	
	for attempt := 0; attempt <= recoveryOpts.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(recoveryOpts.RetryDelay)
			fmt.Fprintf(os.Stderr, "Retrying trace export (attempt %d/%d)...\n", attempt+1, recoveryOpts.MaxRetries+1)
		}
		
		content, err = generateTraceContent(sanitized, format, opts)
		if err == nil {
			break
		}
		
		// Check if error is retryable
		if !isRetryableError(err) {
			return fmt.Errorf("non-retryable error during trace generation: %w", err)
		}
	}
	
	if err != nil {
		return fmt.Errorf("failed to generate trace after %d attempts: %w", recoveryOpts.MaxRetries+1, err)
	}
	
	// Compute checksum if enabled
	var checksum string
	if recoveryOpts.EnableChecksum {
		checksum = computeChecksum([]byte(content))
	}
	
	// Backup existing file if requested
	if recoveryOpts.BackupExisting {
		if err := backupFile(outputPath); err != nil {
			return fmt.Errorf("failed to backup existing file: %w\n"+
				"  Fix: ensure write permissions or disable backup with BackupExisting=false", err)
		}
	}
	
	// Write file with atomic guarantee if enabled
	if recoveryOpts.AtomicWrite {
		if err := atomicWriteFile(outputPath, []byte(content)); err != nil {
			return fmt.Errorf("failed to atomically write trace file: %w\n"+
				"  Path: %s\n"+
				"  Fix: ensure write permissions and sufficient disk space", err, outputPath)
		}
	} else {
		if err := writeFileWithRetry(outputPath, []byte(content), recoveryOpts.MaxRetries, recoveryOpts.RetryDelay); err != nil {
			return fmt.Errorf("failed to write trace file: %w\n"+
				"  Path: %s\n"+
				"  Fix: ensure write permissions and sufficient disk space", err, outputPath)
		}
	}
	
	// Write metadata file if enabled
	if recoveryOpts.EnableMetadata {
		hostname, _ := os.Hostname()
		metadata := ExportMetadata{
			// Use the canonical schema version constant so the metadata version
			// stays in sync automatically when the schema evolves.
			Version:         CurrentJSONSchemaVersion,
			Format:          format,
			TransactionHash: trace.TransactionHash,
			ExportedAt:      time.Now(),
			StepCount:       len(trace.States),
			Checksum:        checksum,
			CLIVersion:      version.Version,
			Hostname:        hostname,
		}
		
		if err := writeMetadata(outputPath, metadata); err != nil {
			// Non-fatal: log warning but don't fail the export
			fmt.Fprintf(os.Stderr, "Warning: failed to write metadata file: %v\n", err)
		}
	}
	
	return nil
}

// SanitizeTrace attempts to repair common trace corruption issues
func SanitizeTrace(trace *ExecutionTrace) (*ExecutionTrace, []error) {
	if trace == nil {
		return nil, []error{fmt.Errorf("cannot sanitize nil trace")}
	}
	
	var errors []error
	sanitized := &ExecutionTrace{
		TransactionHash:  trace.TransactionHash,
		StartTime:        trace.StartTime,
		EndTime:          trace.EndTime,
		States:           make([]ExecutionState, 0, len(trace.States)),
		Snapshots:        trace.Snapshots,
		DiagnosticEvents: trace.DiagnosticEvents,
		Annotations:      trace.Annotations,
		CurrentStep:      trace.CurrentStep,
		SnapshotInterval: trace.SnapshotInterval,
	}
	
	// Fix missing or zero timestamps
	if sanitized.StartTime.IsZero() {
		sanitized.StartTime = time.Now().Add(-1 * time.Hour)
		errors = append(errors, fmt.Errorf("start time was zero, set to 1 hour ago"))
	}
	if sanitized.EndTime.IsZero() || sanitized.EndTime.Before(sanitized.StartTime) {
		sanitized.EndTime = sanitized.StartTime.Add(1 * time.Minute)
		errors = append(errors, fmt.Errorf("end time invalid, set to start + 1 minute"))
	}
	
	// Fix missing transaction hash
	if sanitized.TransactionHash == "" {
		sanitized.TransactionHash = "unknown-tx-hash"
		errors = append(errors, fmt.Errorf("transaction hash was empty, set to placeholder"))
	}
	
	// Sanitize states
	for i, state := range trace.States {
		sanitizedState := state
		
		// Fix step index mismatches
		if state.Step != i {
			errors = append(errors, fmt.Errorf("step %d has incorrect index %d, corrected", i, state.Step))
			sanitizedState.Step = i
		}
		
		// Fix missing timestamps
		if sanitizedState.Timestamp.IsZero() {
			// Interpolate timestamp based on position
			duration := sanitized.EndTime.Sub(sanitized.StartTime)
			if len(trace.States) > 1 {
				offset := duration * time.Duration(i) / time.Duration(len(trace.States)-1)
				sanitizedState.Timestamp = sanitized.StartTime.Add(offset)
			} else {
				sanitizedState.Timestamp = sanitized.StartTime
			}
			errors = append(errors, fmt.Errorf("step %d missing timestamp, interpolated", i))
		}
		
		// Ensure operation or event type is set
		if sanitizedState.Operation == "" && sanitizedState.EventType == "" {
			sanitizedState.Operation = fmt.Sprintf("step_%d", i)
			errors = append(errors, fmt.Errorf("step %d missing operation/event type, set placeholder", i))
		}
		
		// Truncate overly long error messages
		if len(sanitizedState.Error) > 10000 {
			sanitizedState.Error = sanitizedState.Error[:10000] + "... (truncated)"
			errors = append(errors, fmt.Errorf("step %d error message truncated (too long)", i))
		}
		
		sanitized.States = append(sanitized.States, sanitizedState)
	}
	
	return sanitized, errors
}

// generateTraceContent generates trace content in the specified format
func generateTraceContent(trace *ExecutionTrace, format string, opts ExportOptions) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "html"
	}
	
	switch format {
	case "html":
		return GenerateTraceHTMLWithOptions(trace, opts)
	case "markdown", "md":
		return GenerateTraceMarkdownWithOptions(trace, opts)
	case "json":
		jsonBytes, err := json.MarshalIndent(trace, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal trace as JSON: %w", err)
		}
		return string(jsonBytes), nil
	case "text":
		return GenerateTracePlainText(trace)
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

// atomicWriteFile writes data to a file atomically using write-rename
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	
	// Create temp file in same directory to ensure same filesystem
	tmpFile, err := os.CreateTemp(dir, ".glassbox-trace-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	
	// Cleanup temp file on error
	defer func() {
		if err != nil {
			os.Remove(tmpPath)
		}
	}()
	
	// Write data to temp file
	if _, err = tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write to temp file: %w", err)
	}
	
	// Sync to ensure data is on disk
	if err = tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	
	if err = tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	
	// Atomic rename
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	
	return nil
}

// writeFileWithRetry writes a file with retry logic for transient failures
func writeFileWithRetry(path string, data []byte, maxRetries int, retryDelay time.Duration) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(retryDelay)
		}
		
		err := os.WriteFile(path, data, 0o644)
		if err == nil {
			return nil
		}
		
		lastErr = err
		if !isRetryableError(err) {
			break
		}
	}
	
	return lastErr
}

// backupFile creates a backup of an existing file
func backupFile(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // No file to backup
	}
	
	backupPath := path + ".bak." + time.Now().Format("20060102-150405")
	
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	
	dest, err := os.Create(backupPath)
	if err != nil {
		return err
	}
	defer dest.Close()
	
	_, err = io.Copy(dest, source)
	return err
}

// writeMetadata writes export metadata to a companion .meta.json file
func writeMetadata(tracePath string, metadata ExportMetadata) error {
	metaPath := tracePath + ".meta.json"
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0o644)
}

// computeChecksum computes SHA-256 checksum of data
func computeChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// isRetryableError determines if an error is worth retrying
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	
	errStr := strings.ToLower(err.Error())
	
	// Retryable conditions
	retryablePatterns := []string{
		"temporarily unavailable",
		"resource temporarily unavailable",
		"connection reset",
		"broken pipe",
		"interrupted system call",
		"device or resource busy",
	}
	
	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}
	
	return false
}

// VerifyExport verifies an exported trace file's integrity
func VerifyExport(tracePath string) error {
	metaPath := tracePath + ".meta.json"
	
	// Read metadata if it exists
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("metadata file not found: %s\n"+
				"  This export was not created with metadata enabled\n"+
				"  File can still be used but integrity cannot be verified", metaPath)
		}
		return fmt.Errorf("failed to read metadata file: %w", err)
	}
	
	var metadata ExportMetadata
	if err := json.Unmarshal(metaData, &metadata); err != nil {
		return fmt.Errorf("failed to parse metadata file: %w\n"+
			"  Metadata file may be corrupted", err)
	}

	// Validate the schema version recorded in the metadata file. An empty
	// Version field means the file pre-dates versioned metadata; that is
	// allowed with a warning. A non-empty but unrecognised value is rejected
	// so users know the file was produced by a newer binary.
	if metadata.Version != "" {
		if err := ValidateJSONSchemaVersion(metadata.Version); err != nil {
			return fmt.Errorf("metadata file records unsupported schema_version %q\n"+
				"  Supported versions: %s\n"+
				"  Fix: re-export the trace with the current CLI version, or upgrade Glassbox",
				metadata.Version,
				joinSupportedVersions())
		}
		if !IsJSONSchemaVersionSupported(metadata.Version) {
			return fmt.Errorf("metadata file records schema_version %q which is not supported by this binary\n"+
				"  Supported versions: %s\n"+
				"  Fix: re-export the trace with the current CLI version, or upgrade Glassbox",
				metadata.Version,
				joinSupportedVersions())
		}
	}

	// Read trace file
	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		return fmt.Errorf("failed to read trace file: %w", err)
	}
	
	// Verify checksum if present
	if metadata.Checksum != "" {
		actualChecksum := computeChecksum(traceData)
		if actualChecksum != metadata.Checksum {
			return fmt.Errorf("checksum mismatch\n"+
				"  Expected: %s\n"+
				"  Actual:   %s\n"+
				"  The trace file may have been modified or corrupted\n"+
				"  Fix: re-export the trace with glassbox debug --trace-output",
				metadata.Checksum, actualChecksum)
		}
	}
	
	// For JSON exports, verify the recorded step count matches what's in the file
	if strings.ToLower(strings.TrimSpace(metadata.Format)) == "json" && metadata.StepCount > 0 {
		var parsed struct {
			States []json.RawMessage `json:"states"`
			Trace  *struct {
				States []json.RawMessage `json:"states"`
			} `json:"trace"`
		}
		if parseErr := json.Unmarshal(traceData, &parsed); parseErr == nil {
			actualStates := len(parsed.States)
			// Handle versioned/schema envelope where trace is nested
			if actualStates == 0 && parsed.Trace != nil {
				actualStates = len(parsed.Trace.States)
			}
			if actualStates > 0 && actualStates != metadata.StepCount {
				return fmt.Errorf("step count mismatch\n"+
					"  Metadata records %d steps, trace file contains %d steps\n"+
					"  The trace file may have been truncated, appended to, or partially overwritten\n"+
					"  Fix: re-export the trace with glassbox debug --trace-output",
					metadata.StepCount, actualStates)
			}
		}
	}
	
	// Verify format matches file extension
	ext := strings.ToLower(filepath.Ext(tracePath))
	expectedExt := ""
	switch metadata.Format {
	case "html":
		expectedExt = ".html"
	case "markdown", "md":
		expectedExt = ".md"
	case "json":
		expectedExt = ".json"
	case "text":
		expectedExt = ".txt"
	}
	
	if expectedExt != "" && ext != expectedExt {
		return fmt.Errorf("format mismatch: metadata says %q but file extension is %q\n"+
			"  Fix: rename file to have correct extension or re-export",
			metadata.Format, ext)
	}
	
	return nil
}

// RecoverTrace attempts to recover a trace from a potentially corrupted export file
func RecoverTrace(tracePath string) (*ExecutionTrace, []error) {
	var recoveryErrors []error
	
	// Attempt integrity verification first — surface any checksum or step-count
	// mismatch as a recovery warning so the caller knows the file was modified.
	if verifyErr := VerifyExport(tracePath); verifyErr != nil {
		// Missing metadata is acceptable (older exports didn't have it).
		// Any other verification failure is reported as a warning, not a fatal error,
		// because we still attempt content-level recovery below.
		if !strings.Contains(verifyErr.Error(), "metadata file not found") {
			recoveryErrors = append(recoveryErrors,
				fmt.Errorf("pre-recovery integrity check failed: %w\n"+
					"  Continuing with best-effort content recovery", verifyErr))
		}
	}
	
	// Try to read the file
	data, err := os.ReadFile(tracePath)
	if err != nil {
		return nil, append(recoveryErrors, fmt.Errorf("failed to read trace file: %w", err))
	}
	
	// Determine format from extension
	ext := strings.ToLower(filepath.Ext(tracePath))
	
	var trace *ExecutionTrace
	
	switch ext {
	case ".json":
		// Try to parse as JSON
		trace, err = recoverFromJSON(data)
		if err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("JSON recovery failed: %w", err))
			return nil, recoveryErrors
		}
		
	default:
		// For HTML, Markdown, Text - these are presentation formats and can't be parsed back to trace
		recoveryErrors = append(recoveryErrors, fmt.Errorf("cannot recover trace from %s format\n"+
			"  Only JSON format exports can be recovered\n"+
			"  Recommendation: always export traces in JSON format for recovery capability", ext))
		return nil, recoveryErrors
	}
	
	// Sanitize recovered trace
	sanitized, sanitizeErrs := SanitizeTrace(trace)
	recoveryErrors = append(recoveryErrors, sanitizeErrs...)
	
	// Validate recovered trace
	validationIssues := ValidateExecutionTrace(sanitized)
	for _, issue := range validationIssues {
		recoveryErrors = append(recoveryErrors, fmt.Errorf("validation: %s", issue))
	}
	
	return sanitized, recoveryErrors
}

// recoverFromJSON attempts to recover a trace from JSON data with error tolerance
func recoverFromJSON(data []byte) (*ExecutionTrace, error) {
	var trace ExecutionTrace
	
	// Try strict unmarshaling first
	err := json.Unmarshal(data, &trace)
	if err == nil {
		return &trace, nil
	}
	
	// Strict unmarshaling failed, try lenient recovery
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields() // This will be relaxed below if needed
	
	err = decoder.Decode(&trace)
	if err != nil {
		// Try one more time with unknown fields allowed
		decoder = json.NewDecoder(bytes.NewReader(data))
		err = decoder.Decode(&trace)
		if err != nil {
			return nil, fmt.Errorf("failed to parse JSON trace: %w\n"+
				"  The JSON may be malformed or corrupted\n"+
				"  Try opening the file in a JSON validator to identify specific issues", err)
		}
	}
	
	return &trace, nil
}
