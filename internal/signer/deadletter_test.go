// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

// deadletter_test.go covers:
//  - DeadLetterWriter.Write produces a well-formed JSON file
//  - Write is atomic (temp file removed on success, dest complete)
//  - Concurrent writes from the same writer produce distinct files
//  - ReadDeadLetterDir returns files sorted oldest-first
//  - ReadDeadLetterDir skips files with status != "unsigned"
//  - sanitizeFilename replaces unsafe characters
//  - SigningAttempt collection: 1-retry, 2-retry, and max-retry paths
//  - errorCategoryForAttempt returns the correct category string

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ── DeadLetterWriter.Write ─────────────────────────────────────────────────

func TestDeadLetterWriter_Write_ProducesValidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newDeadLetterWriterAt(dir)

	entry := DeadLetterEntry{
		SessionID:      "corr-abc",
		IdempotencyKey: "v1:key:abc123",
		Provider:       "aws-kms",
		ErrorCode:      "ThrottlingException",
		ErrorClass:     "api",
		SigningAttempts: []SigningAttempt{
			{AttemptNumber: 1, StartedAt: time.Now(), DurationMS: 42, ErrorCategory: "throttled", ErrorMessage: "throttled", IdempotencyKey: "v1:key:abc123"},
		},
		Payload: json.RawMessage(`{"foo":"bar"}`),
	}

	if err := w.Write(entry); err != nil {
		t.Fatalf("Write: %v", err)
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	raw, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got DeadLetterEntry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Status != "unsigned" {
		t.Errorf("Status = %q, want %q", got.Status, "unsigned")
	}
	if got.SessionID != entry.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, entry.SessionID)
	}
	if got.Provider != entry.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, entry.Provider)
	}
	if got.ErrorCode != entry.ErrorCode {
		t.Errorf("ErrorCode = %q, want %q", got.ErrorCode, entry.ErrorCode)
	}
	if len(got.SigningAttempts) != 1 {
		t.Errorf("SigningAttempts len = %d, want 1", len(got.SigningAttempts))
	}
	if got.SigningAttempts[0].AttemptNumber != 1 {
		t.Errorf("attempt_number = %d, want 1", got.SigningAttempts[0].AttemptNumber)
	}
	if string(got.Payload) != `{"foo":"bar"}` {
		t.Errorf("Payload = %s, want {\"foo\":\"bar\"}", got.Payload)
	}
}

func TestDeadLetterWriter_Write_StatusAlwaysUnsigned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newDeadLetterWriterAt(dir)

	// Even if the caller accidentally sets Status to something else, Write
	// must overwrite it with "unsigned".
	entry := DeadLetterEntry{
		Status:  "signed",   // wrong — must be overwritten
		Payload: json.RawMessage(`{}`),
	}
	if err := w.Write(entry); err != nil {
		t.Fatalf("Write: %v", err)
	}

	files, _ := os.ReadDir(dir)
	raw, _ := os.ReadFile(filepath.Join(dir, files[0].Name()))
	var got DeadLetterEntry
	_ = json.Unmarshal(raw, &got)
	if got.Status != "unsigned" {
		t.Errorf("Status = %q, want %q", got.Status, "unsigned")
	}
}

func TestDeadLetterWriter_Write_FailedAtStamped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newDeadLetterWriterAt(dir)

	before := time.Now().Add(-time.Millisecond)
	entry := DeadLetterEntry{Payload: json.RawMessage(`{}`)}
	if err := w.Write(entry); err != nil {
		t.Fatalf("Write: %v", err)
	}
	after := time.Now().Add(time.Millisecond)

	files, _ := os.ReadDir(dir)
	raw, _ := os.ReadFile(filepath.Join(dir, files[0].Name()))
	var got DeadLetterEntry
	_ = json.Unmarshal(raw, &got)

	if got.FailedAt.Before(before) || got.FailedAt.After(after) {
		t.Errorf("FailedAt %v not in range [%v, %v]", got.FailedAt, before, after)
	}
}

func TestDeadLetterWriter_Write_SessionIDInFilename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newDeadLetterWriterAt(dir)

	entry := DeadLetterEntry{
		SessionID: "my-session",
		Payload:   json.RawMessage(`{}`),
	}
	if err := w.Write(entry); err != nil {
		t.Fatalf("Write: %v", err)
	}

	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	name := files[0].Name()
	if len(name) == 0 || name[:len("my-session")] != "my-session" {
		t.Errorf("filename %q does not start with session ID", name)
	}
}

// TestDeadLetterWriter_Write_Atomic verifies that the destination file
// appears atomically (no partial .tmp file left behind after success).
func TestDeadLetterWriter_Write_Atomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newDeadLetterWriterAt(dir)

	entry := DeadLetterEntry{Payload: json.RawMessage(`{"atomic":true}`)}
	if err := w.Write(entry); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// After a successful write there must be exactly one .json file and no .tmp files.
	files, _ := os.ReadDir(dir)
	jsonCount, tmpCount := 0, 0
	for _, f := range files {
		switch {
		case filepath.Ext(f.Name()) == ".json":
			jsonCount++
		case len(f.Name()) > 4 && f.Name()[len(f.Name())-4:] == ".tmp":
			tmpCount++
		}
	}
	if jsonCount != 1 {
		t.Errorf("expected exactly 1 .json file, got %d", jsonCount)
	}
	if tmpCount != 0 {
		t.Errorf("expected 0 .tmp files, got %d", tmpCount)
	}
}

func TestDeadLetterWriter_Write_ConcurrentProducesDistinctFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := newDeadLetterWriterAt(dir)

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := DeadLetterEntry{
				SessionID: fmt.Sprintf("session-%d", i),
				Payload:   json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
			}
			errs[i] = w.Write(entry)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: Write error: %v", i, err)
		}
	}

	files, _ := os.ReadDir(dir)
	if len(files) != n {
		t.Errorf("expected %d files, got %d", n, len(files))
	}
}

// ── ReadDeadLetterDir ──────────────────────────────────────────────────────

func TestReadDeadLetterDir_SortedOldestFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write three entries with known timestamps in reverse order.
	base := time.Now()
	entries := []DeadLetterEntry{
		{FailedAt: base.Add(2 * time.Hour), Payload: json.RawMessage(`{"n":3}`)},
		{FailedAt: base.Add(1 * time.Hour), Payload: json.RawMessage(`{"n":2}`)},
		{FailedAt: base, Payload: json.RawMessage(`{"n":1}`)},
	}
	w := newDeadLetterWriterAt(dir)
	for _, e := range entries {
		if err := w.Write(e); err != nil {
			t.Fatalf("Write: %v", err)
		}
		// Small sleep to ensure distinct nanosecond filenames.
		time.Sleep(time.Millisecond)
	}

	files, err := ReadDeadLetterDir(dir)
	if err != nil {
		t.Fatalf("ReadDeadLetterDir: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	for i := 1; i < len(files); i++ {
		if files[i].Entry.FailedAt.Before(files[i-1].Entry.FailedAt) {
			t.Errorf("files not sorted oldest-first: [%d] %v before [%d] %v",
				i, files[i].Entry.FailedAt, i-1, files[i-1].Entry.FailedAt)
		}
	}
}

func TestReadDeadLetterDir_SkipsNonUnsigned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a file manually with status "signed" — should be skipped.
	signed := DeadLetterEntry{Status: "signed", Payload: json.RawMessage(`{}`)}
	data, _ := json.Marshal(signed)
	_ = os.WriteFile(filepath.Join(dir, "already-signed.json"), data, 0o600)

	// Write one "unsigned" entry via the writer.
	w := newDeadLetterWriterAt(dir)
	if err := w.Write(DeadLetterEntry{Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	files, err := ReadDeadLetterDir(dir)
	if err != nil {
		t.Fatalf("ReadDeadLetterDir: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file (skipping signed), got %d", len(files))
	}
}

func TestReadDeadLetterDir_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files, err := ReadDeadLetterDir(dir)
	if err != nil {
		t.Fatalf("ReadDeadLetterDir on empty dir: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestReadDeadLetterDir_NonExistentDir(t *testing.T) {
	t.Parallel()
	files, err := ReadDeadLetterDir(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("expected nil error for non-existent dir, got: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for non-existent dir, got %d", len(files))
	}
}

// ── sanitizeFilename ───────────────────────────────────────────────────────

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"with/slash", "with_slash"},
		{"with\\back", "with_back"},
		{"with:colon", "with_colon"},
		{"with*star", "with_star"},
		{"with?question", "with_question"},
		{`with"quote`, "with_quote"},
		{"with<lt", "with_lt"},
		{"with>gt", "with_gt"},
		{"with|pipe", "with_pipe"},
		{"with\x00null", "with_null"},
		{"normal-session-id", "normal-session-id"},
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ── errorCategoryForAttempt ────────────────────────────────────────────────

func TestErrorCategoryForAttempt(t *testing.T) {
	t.Parallel()
	someErr := errors.New("some error")
	cases := []struct {
		err       error
		code      string
		class     string
		want      string
	}{
		{nil, "", "", "success"},
		{someErr, "", "context", "context"},
		{someErr, "", "network", "network"},
		{someErr, "AccessDeniedException", "api", "auth_failure"},
		{someErr, "ThrottlingException", "api", "throttled"},
		{someErr, "InternalError", "api", "transient"},
		{someErr, "Unknown", "unknown", "transient"},
	}
	for _, tc := range cases {
		got := errorCategoryForAttempt(tc.err, tc.code, tc.class)
		if got != tc.want {
			t.Errorf("errorCategoryForAttempt(%v, %q, %q) = %q, want %q",
				tc.err, tc.code, tc.class, got, tc.want)
		}
	}
}

// ── SigningAttempt recording in KMSSigner ──────────────────────────────────
//
// These tests use fakeKMSClient from kms_retry_test.go (same package).

// TestSignWithMetadata_AttemptsRecorded_OneRetry verifies that after one
// transient failure and one success, SigningAttempts has exactly two entries:
// attempt 1 with category "transient" and attempt 2 with category "success".
func TestSignWithMetadata_AttemptsRecorded_OneRetry(t *testing.T) {
	t.Parallel()

	client := &fakeKMSClient{
		failFirst:    1,
		failWithCode: "InternalError",
		signature:    []byte("fake-sig"),
		getPublicKey: []byte("fake-pub"),
	}

	s := newTestSigner(client, KMSRetryConfig{MaxRetries: 3, InitialBackoff: 0, MaxBackoff: 0, IdempotencyMaxEntries: 16, IdempotencyTTL: time.Minute}, func(_ logLevel, _ string, _ ...any) {})

	meta, err := s.SignWithMetadata(context.Background(), []byte("hello"), "corr-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta.SigningAttempts) != 2 {
		t.Fatalf("expected 2 SigningAttempts, got %d", len(meta.SigningAttempts))
	}

	a1 := meta.SigningAttempts[0]
	if a1.AttemptNumber != 1 {
		t.Errorf("attempt 1 number = %d, want 1", a1.AttemptNumber)
	}
	if a1.ErrorCategory != "transient" {
		t.Errorf("attempt 1 category = %q, want %q", a1.ErrorCategory, "transient")
	}
	if a1.ErrorMessage == "" {
		t.Error("attempt 1 ErrorMessage must be non-empty on failure")
	}
	if a1.IdempotencyKey == "" {
		t.Error("attempt 1 IdempotencyKey must be non-empty")
	}

	a2 := meta.SigningAttempts[1]
	if a2.AttemptNumber != 2 {
		t.Errorf("attempt 2 number = %d, want 2", a2.AttemptNumber)
	}
	if a2.ErrorCategory != "success" {
		t.Errorf("attempt 2 category = %q, want %q", a2.ErrorCategory, "success")
	}
	if a2.ErrorMessage != "" {
		t.Errorf("attempt 2 ErrorMessage must be empty on success, got %q", a2.ErrorMessage)
	}
}

// TestSignWithMetadata_AttemptsRecorded_TwoRetries verifies that after two
// transient failures and one success, there are exactly three attempt entries.
func TestSignWithMetadata_AttemptsRecorded_TwoRetries(t *testing.T) {
	t.Parallel()

	client := &fakeKMSClient{
		failFirst:    2,
		failWithCode: "ServiceUnavailable",
		signature:    []byte("ok-sig"),
		getPublicKey: []byte("fake-pub"),
	}

	s := newTestSigner(client, KMSRetryConfig{MaxRetries: 3, InitialBackoff: 0, MaxBackoff: 0, IdempotencyMaxEntries: 16, IdempotencyTTL: time.Minute}, func(_ logLevel, _ string, _ ...any) {})

	meta, err := s.SignWithMetadata(context.Background(), []byte("world"), "corr-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta.SigningAttempts) != 3 {
		t.Fatalf("expected 3 SigningAttempts, got %d", len(meta.SigningAttempts))
	}
	for i := 0; i < 2; i++ {
		if meta.SigningAttempts[i].ErrorCategory != "transient" {
			t.Errorf("attempt %d category = %q, want transient", i+1, meta.SigningAttempts[i].ErrorCategory)
		}
	}
	last := meta.SigningAttempts[2]
	if last.ErrorCategory != "success" {
		t.Errorf("last attempt category = %q, want success", last.ErrorCategory)
	}
}

// TestSignWithMetadata_AttemptsRecorded_MaxRetries verifies that when all
// MaxRetries+1 attempts fail, all attempt entries are recorded and there is
// no "success" entry.
func TestSignWithMetadata_AttemptsRecorded_MaxRetries(t *testing.T) {
	t.Parallel()

	maxRetries := 3
	client := &fakeKMSClient{
		failFirst:    maxRetries + 100, // always fail
		failWithCode: "ThrottlingException",
		signature:    []byte("never-reached"),
		getPublicKey: []byte("fake-pub"),
	}

	s := newTestSigner(client, KMSRetryConfig{MaxRetries: maxRetries, InitialBackoff: 0, MaxBackoff: 0, IdempotencyMaxEntries: 16, IdempotencyTTL: time.Minute}, func(_ logLevel, _ string, _ ...any) {})

	meta, err := s.SignWithMetadata(context.Background(), []byte("throttled-msg"), "corr-3")
	if err == nil {
		t.Fatal("expected error when all retries exhausted, got nil")
	}

	expectedAttempts := maxRetries + 1 // loop runs attempt 0..MaxRetries inclusive
	if len(meta.SigningAttempts) != expectedAttempts {
		t.Errorf("expected %d SigningAttempts, got %d", expectedAttempts, len(meta.SigningAttempts))
	}
	for i, a := range meta.SigningAttempts {
		if a.AttemptNumber != i+1 {
			t.Errorf("attempt[%d].AttemptNumber = %d, want %d", i, a.AttemptNumber, i+1)
		}
		if a.ErrorCategory != "throttled" {
			t.Errorf("attempt[%d].ErrorCategory = %q, want throttled", i, a.ErrorCategory)
		}
		if a.IdempotencyKey == "" {
			t.Errorf("attempt[%d].IdempotencyKey must not be empty", i)
		}
	}
}

// TestSignWithMetadata_AttemptsRecorded_ImmediateSuccess verifies that a
// first-try success produces exactly one entry with category "success".
func TestSignWithMetadata_AttemptsRecorded_ImmediateSuccess(t *testing.T) {
	t.Parallel()

	client := &fakeKMSClient{
		failFirst:    0,
		signature:    []byte("instant-sig"),
		getPublicKey: []byte("fake-pub"),
	}
	s := newTestSigner(client, DefaultKMSRetryConfig(), func(_ logLevel, _ string, _ ...any) {})

	meta, err := s.SignWithMetadata(context.Background(), []byte("quick"), "corr-ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta.SigningAttempts) != 1 {
		t.Fatalf("expected 1 SigningAttempt for immediate success, got %d", len(meta.SigningAttempts))
	}
	a := meta.SigningAttempts[0]
	if a.AttemptNumber != 1 {
		t.Errorf("AttemptNumber = %d, want 1", a.AttemptNumber)
	}
	if a.ErrorCategory != "success" {
		t.Errorf("ErrorCategory = %q, want success", a.ErrorCategory)
	}
	if a.ErrorMessage != "" {
		t.Errorf("ErrorMessage must be empty on success, got %q", a.ErrorMessage)
	}
}

// TestSignWithMetadata_IdempotencyHit_NoAttempts verifies that a cache hit
// produces zero SigningAttempts (no KMS API calls were made).
func TestSignWithMetadata_IdempotencyHit_NoAttempts(t *testing.T) {
	t.Parallel()

	client := &fakeKMSClient{
		failFirst:    0,
		signature:    []byte("cached-sig"),
		getPublicKey: []byte("fake-pub"),
	}
	s := newTestSigner(client, DefaultKMSRetryConfig(), func(_ logLevel, _ string, _ ...any) {})

	msg := []byte("cached-message")

	// First call — should call KMS and cache result.
	meta1, err := s.SignWithMetadata(context.Background(), msg, "corr-cache")
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if len(meta1.SigningAttempts) != 1 {
		t.Errorf("first call: expected 1 attempt, got %d", len(meta1.SigningAttempts))
	}

	// Second call — idempotency hit, no KMS calls.
	meta2, err := s.SignWithMetadata(context.Background(), msg, "corr-cache")
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if !meta2.IdempotencyHit {
		t.Error("expected IdempotencyHit=true on second call")
	}
	if len(meta2.SigningAttempts) != 0 {
		t.Errorf("idempotency hit: expected 0 SigningAttempts, got %d", len(meta2.SigningAttempts))
	}
	if client.signatureCalls != 1 {
		t.Errorf("KMS called %d times, want exactly 1", client.signatureCalls)
	}
}
