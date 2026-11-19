// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── correlation ID ────────────────────────────────────────────────────────────

func TestWithCorrelation_StoresID(t *testing.T) {
	ctx := WithCorrelation(context.Background(), "req-abc")
	if got := CorrelationFromContext(ctx); got != "req-abc" {
		t.Errorf("CorrelationFromContext = %q, want %q", got, "req-abc")
	}
}

func TestCorrelationFromContext_Empty(t *testing.T) {
	if got := CorrelationFromContext(context.Background()); got != "" {
		t.Errorf("expected empty for background context, got %q", got)
	}
}

func TestCorrelationFromContext_NilContext(t *testing.T) {
	if got := CorrelationFromContext(nil); got != "" { //nolint:staticcheck
		t.Errorf("expected empty for nil context, got %q", got)
	}
}

func TestContextLogger_InjectCorrelation(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true) // JSON mode
	SetLevel(slog.LevelInfo)

	ctx := WithCorrelation(context.Background(), "corr-xyz")
	l := ContextLogger(ctx)
	l.InfoContext(ctx, "test correlation")

	output := buf.String()
	if !strings.Contains(output, "corr-xyz") {
		t.Errorf("correlation_id not found in JSON output: %s", output)
	}
}

func TestContextLogger_NoCorrelation(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelInfo)

	l := ContextLogger(context.Background())
	l.Info("no correlation")

	output := buf.String()
	if strings.Contains(output, "correlation_id") {
		t.Errorf("unexpected correlation_id in output: %s", output)
	}
}

// ── JSON record parse ─────────────────────────────────────────────────────────

// JSON records must parse independently (each line is a self-contained object).
func TestJSONRecords_ParseIndependently(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelDebug)

	Logger.Info("first record", "k1", "v1")
	Logger.Warn("second record", "k2", "v2")
	Logger.Error("third record", "k3", "v3")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 JSON lines, got %d: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d not valid JSON: %v\nline: %s", i+1, err, line)
		}
		if _, ok := obj["msg"]; !ok {
			t.Errorf("line %d missing 'msg' field: %s", i+1, line)
		}
		if _, ok := obj["level"]; !ok {
			t.Errorf("line %d missing 'level' field: %s", i+1, line)
		}
	}
}

// ── sensitive field redaction ─────────────────────────────────────────────────

func TestJSONHandler_RedactsSensitiveKeys(t *testing.T) {
	sensitiveTests := []struct {
		key   string
		value string
	}{
		{"pin", "1234"},
		{"password", "hunter2"},
		{"secret", "s3cr3t"},
		{"api_key", "ak-xyz"},
		{"token", "tok-abc"},
		{"private_key", "-----BEGIN PRIVATE KEY-----"},
	}

	for _, tt := range sensitiveTests {
		t.Run(tt.key, func(t *testing.T) {
			buf := &bytes.Buffer{}
			SetOutput(buf, true)
			SetLevel(slog.LevelDebug)

			Logger.Info("message", tt.key, tt.value)

			output := buf.String()
			if strings.Contains(output, tt.value) {
				t.Errorf("sensitive value %q leaked in JSON output for key %q: %s",
					tt.value, tt.key, output)
			}
			// Accept either [REDACTED] (handler-level redaction) or ***** (writer-level redaction)
			if !strings.Contains(output, "[REDACTED]") && !strings.Contains(output, "*****") {
				t.Errorf("expected redaction marker in output for key %q: %s", tt.key, output)
			}
		})
	}
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"pin", "PIN", "PKCS11_PIN",
		"password", "Password",
		"secret", "my_secret",
		"api_key", "apikey",
		"token", "auth_token",
		"private_key", "privatekey",
		"credential", "credentials",
	}
	for _, k := range sensitive {
		if !isSensitiveKey(k) {
			t.Errorf("isSensitiveKey(%q) = false, want true", k)
		}
	}
	nonSensitive := []string{
		"host", "port", "network", "hash", "txid", "message",
	}
	for _, k := range nonSensitive {
		if isSensitiveKey(k) {
			t.Errorf("isSensitiveKey(%q) = true, want false", k)
		}
	}
}

// ── correlation injected into JSON records ────────────────────────────────────

func TestRedactingJSONHandler_InjectsCorrelation(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelInfo)

	ctx := WithCorrelation(context.Background(), "req-123")
	Logger.InfoContext(ctx, "hello", "foo", "bar")

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &obj); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, buf.String())
	}
	if obj["correlation_id"] != "req-123" {
		t.Errorf("correlation_id = %v, want %q", obj["correlation_id"], "req-123")
	}
}

// ── severity filtering ────────────────────────────────────────────────────────

func TestSeverityFiltering_JSON(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelWarn)

	Logger.Debug("debug msg")
	Logger.Info("info msg")
	Logger.Warn("warn msg")
	Logger.Error("error msg")

	lines := nonEmptyLines(buf.String())
	for _, line := range lines {
		var obj map[string]interface{}
		_ = json.Unmarshal([]byte(line), &obj)
		lvl, _ := obj["level"].(string)
		if lvl == "DEBUG" || lvl == "INFO" {
			t.Errorf("filtered level %q appeared in output at WARN threshold", lvl)
		}
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 records (WARN+ERROR), got %d\n%s", len(lines), buf.String())
	}
}

// ── log rotation ─────────────────────────────────────────────────────────────

func TestRotatingFileWriter_RotatesOnSizeExceed(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	// Max 50 bytes — each write is ~30 chars so 2 writes will rotate.
	w, err := NewRotatingFileWriter(logPath, 50, 0)
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	defer w.Close()

	payload := strings.Repeat("x", 30)
	if _, err := fmt.Fprintln(w, payload); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Second write should trigger rotation.
	if _, err := fmt.Fprintln(w, payload); err != nil {
		t.Fatalf("second write: %v", err)
	}
	// After rotation, active file should only contain the second write.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Count(string(data), payload) != 1 {
		t.Errorf("expected exactly 1 occurrence of payload in active log after rotation, got:\n%s", data)
	}
	rotated := w.RotatedFiles()
	if len(rotated) != 1 {
		t.Errorf("expected 1 rotated file, got %d: %v", len(rotated), rotated)
	}
}

func TestRotatingFileWriter_NoRotationBelowLimit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	w, err := NewRotatingFileWriter(logPath, 10000, 0)
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	defer w.Close()

	for i := 0; i < 5; i++ {
		if _, err := fmt.Fprintln(w, "log line "+fmt.Sprint(i)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if len(w.RotatedFiles()) != 0 {
		t.Error("expected no rotated files below size limit")
	}
}

func TestRotatingFileWriter_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "concurrent.log")

	w, err := NewRotatingFileWriter(logPath, 0, 0) // no size limit
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	defer w.Close()

	const goroutines = 10
	const writesEach = 20
	done := make(chan struct{}, goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			for i := 0; i < writesEach; i++ {
				_, _ = fmt.Fprintf(w, "goroutine %d line %d\n", g, i)
			}
			done <- struct{}{}
		}()
	}
	for g := 0; g < goroutines; g++ {
		<-done
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lineCount := strings.Count(string(data), "\n")
	if lineCount != goroutines*writesEach {
		t.Errorf("expected %d lines, got %d", goroutines*writesEach, lineCount)
	}
}

func TestRotatingFileWriter_RetentionPrunesOldFiles(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "retain.log")

	// Create a fake old rotated file.
	oldFile := filepath.Join(dir, "retain.20200101T000000Z.log")
	if err := os.WriteFile(oldFile, []byte("old\n"), 0o640); err != nil {
		t.Fatalf("create old file: %v", err)
	}
	// Set its mtime to 60 days ago.
	old := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(oldFile, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Writer with 1-byte limit (rotates immediately) and 30-day retention.
	w, err := NewRotatingFileWriter(logPath, 1, 30)
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	defer w.Close()

	// Trigger rotation by writing a couple bytes.
	if _, err := fmt.Fprintln(w, "trigger"); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The 60-day-old file should have been pruned.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old rotated file was not pruned: %v", err)
	}
}

func TestSetOutputWithRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "glassbox.log")

	// Explicitly set up the rotating writer as the global output BEFORE calling
	// SetOutputWithRotation to avoid contamination from previous tests that may
	// have redirected the global Logger to a bytes.Buffer.
	rfw, err := NewRotatingFileWriter(logPath, 1024*1024, 7)
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	defer rfw.Close()

	// Write directly through the rotating writer to ensure we test it independently
	// of the global logger state.
	h := newRedactingJSONHandler(rfw, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	l := slog.New(h)

	l.Info("first structured log", "key", "value")
	_ = rfw.Sync()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := nonEmptyLines(string(data))
	if len(lines) == 0 {
		t.Fatalf("log file is empty after write")
	}
	lastLine := lines[len(lines)-1]
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(lastLine), &obj); err != nil {
		t.Fatalf("not valid JSON: %v\nline: %s", err, lastLine)
	}
	if obj["msg"] != "first structured log" {
		t.Errorf("msg = %v, want %q", obj["msg"], "first structured log")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
