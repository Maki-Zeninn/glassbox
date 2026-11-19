// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RotatingFileWriter is a concurrent-safe io.WriteCloser that rotates the
// active log file when it exceeds maxSizeBytes and optionally deletes old
// rotated files once they are older than maxAgeDays.
//
// Rotation is atomic at the write boundary: once the size threshold is crossed
// the current file is renamed with a timestamp suffix, a new file is opened,
// and the caller's write proceeds without data loss.
//
// Writes are serialised with a mutex so the writer is safe for concurrent
// daemon workers.
type RotatingFileWriter struct {
	mu           sync.Mutex
	path         string
	maxSizeBytes int64
	maxAgeDays   int
	file         *os.File
	currentSize  int64
}

// NewRotatingFileWriter opens (or creates) the log file at path and returns a
// RotatingFileWriter ready for use.
//
// maxSizeBytes: rotate when the file exceeds this size in bytes.
//              Set to 0 to disable size-based rotation.
// maxAgeDays:  delete rotated files older than this many days after each rotation.
//              Set to 0 to keep all rotated files indefinitely.
func NewRotatingFileWriter(path string, maxSizeBytes int64, maxAgeDays int) (*RotatingFileWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("logger: create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("logger: open log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("logger: stat log file: %w", err)
	}
	return &RotatingFileWriter{
		path:         path,
		maxSizeBytes: maxSizeBytes,
		maxAgeDays:   maxAgeDays,
		file:         f,
		currentSize:  info.Size(),
	}, nil
}

// Write writes p to the underlying file, rotating first if the size threshold
// would be exceeded. The operation is atomic from the caller's perspective.
func (w *RotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rotate if the write would push us over the limit.
	if w.maxSizeBytes > 0 && w.currentSize+int64(len(p)) > w.maxSizeBytes {
		if err := w.rotate(); err != nil {
			// Fall through and write to the existing file rather than losing data.
			// A warning is appended to the same file so it appears in-stream.
			_, _ = fmt.Fprintf(w.file, `{"level":"WARN","msg":"log rotation failed","error":%q}`+"\n", err.Error())
		}
	}

	n, err := w.file.Write(p)
	w.currentSize += int64(n)
	return n, err
}

// Close flushes and closes the underlying file.
func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// Sync flushes the underlying file to disk (useful before a process exit).
func (w *RotatingFileWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

// rotate renames the current file and opens a fresh one at the original path.
// Must be called with w.mu held.
func (w *RotatingFileWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("logger: close before rotate: %w", err)
		}
		w.file = nil
	}

	// Build a timestamp-based archive name next to the original file.
	dir := filepath.Dir(w.path)
	base := filepath.Base(w.path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	ts := time.Now().UTC().Format("20060102T150405Z")
	archiveName := filepath.Join(dir, fmt.Sprintf("%s.%s%s", stem, ts, ext))

	if err := os.Rename(w.path, archiveName); err != nil {
		// Rename failed (e.g. cross-device); try opening a fresh file anyway.
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("logger: open new log file after rotate: %w", err)
	}
	w.file = f
	w.currentSize = 0

	// Enforce retention policy after rotation.
	if w.maxAgeDays > 0 {
		w.pruneOldLogs(dir, stem, ext)
	}
	return nil
}

// pruneOldLogs removes rotated log files that are older than maxAgeDays.
// Must be called with w.mu held.
func (w *RotatingFileWriter) pruneOldLogs(dir, stem, ext string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -w.maxAgeDays)
	// Prefix for rotated files: "stem." followed by a timestamp and ext.
	prefix := stem + "."

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ext) {
			continue
		}
		// The suffix between prefix and ext is the rotation timestamp.
		mid := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ext)
		if len(mid) == 0 {
			continue // original file, skip
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UTC().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// RotatedFiles returns a sorted list of rotated log file paths found next to
// the primary log file. Useful for tests and diagnostics.
func (w *RotatingFileWriter) RotatedFiles() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	dir := filepath.Dir(w.path)
	base := filepath.Base(w.path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	prefix := stem + "."

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ext) && name != base {
			out = append(out, filepath.Join(dir, name))
		}
	}
	sort.Strings(out)
	return out
}

// Compile-time check that RotatingFileWriter satisfies io.WriteCloser.
var _ io.WriteCloser = (*RotatingFileWriter)(nil)
