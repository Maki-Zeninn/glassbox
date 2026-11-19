// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RotationConfig controls when the active audit segment is closed.
// A zero MaxSizeBytes disables size-based rotation. A zero MaxAge disables
// time-based rotation. When both are zero, segments are only rotated by an
// explicit Rotate call or Close.
type RotationConfig struct {
	MaxSizeBytes int64
	MaxAge       time.Duration
}

// Writer appends complete audit records to an active segment and rotates into
// immutable closed segments with checksum-chained manifests.
//
// Rotation always happens between records: WriteRecord never truncates or
// splits an active record across segments.
type Writer struct {
	mu sync.Mutex

	dir      string
	cfg      RotationConfig
	file     *os.File
	path     string // active segment path
	size     int64
	records  int64
	openedAt time.Time
	nextSeq  uint64
	prevHash string // SHA-256 of last closed segment; empty for genesis
	now      func() time.Time
}

// OpenWriter creates (or resumes) a rotating audit log writer in dir.
func OpenWriter(dir string, cfg RotationConfig) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("audit: create log directory: %w", err)
	}

	seq, err := highestSequence(dir)
	if err != nil {
		return nil, err
	}
	prevHash, err := latestSegmentHashByName(dir)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, ActiveSegmentName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open active segment: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("audit: stat active segment: %w", err)
	}

	w := &Writer{
		dir:      dir,
		cfg:      cfg,
		file:     f,
		path:     path,
		size:     info.Size(),
		records:  countRecordsInFile(path),
		openedAt: time.Now().UTC(),
		nextSeq:  seq + 1,
		prevHash: prevHash,
		now:      func() time.Time { return time.Now().UTC() },
	}
	if info.Size() == 0 {
		w.openedAt = w.now()
	}
	return w, nil
}

// WriteRecord appends one complete audit record. record must be a full record;
// a trailing newline is added when missing. Rotation is considered before the
// write so an in-flight record is never truncated by rotation.
func (w *Writer) WriteRecord(record []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return fmt.Errorf("audit: writer is closed")
	}
	if len(record) == 0 {
		return fmt.Errorf("audit: refusing to write empty record")
	}

	payload := record
	if payload[len(payload)-1] != '\n' {
		payload = append(append([]byte{}, record...), '\n')
	}

	if err := w.maybeRotateLocked(int64(len(payload))); err != nil {
		return err
	}

	n, err := w.file.Write(payload)
	if err != nil {
		return fmt.Errorf("audit: write record: %w", err)
	}
	w.size += int64(n)
	w.records++
	return nil
}

// Rotate closes the active segment (if non-empty), writes an immutable
// manifest with a previous-segment hash link, and opens a fresh active file.
func (w *Writer) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked()
}

// Close rotates any non-empty active segment and closes the writer.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	if w.size > 0 {
		if err := w.rotateLocked(); err != nil {
			_ = w.file.Close()
			w.file = nil
			return err
		}
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Dir returns the audit log directory.
func (w *Writer) Dir() string { return w.dir }

func (w *Writer) maybeRotateLocked(nextRecordBytes int64) error {
	if w.size == 0 {
		return nil
	}
	if w.cfg.MaxSizeBytes > 0 && w.size+nextRecordBytes > w.cfg.MaxSizeBytes {
		return w.rotateLocked()
	}
	if w.cfg.MaxAge > 0 && w.now().Sub(w.openedAt) >= w.cfg.MaxAge {
		return w.rotateLocked()
	}
	return nil
}

// rotateLocked performs atomic rotation. Must be called with w.mu held.
//
// Order:
//  1. Sync+close active
//  2. Hash active body
//  3. Write immutable manifest (atomic)
//  4. Rename active → segment-NNNNNN-timestamp.jsonl
//  5. Open fresh active
//
// A crash before step 4 leaves the active file intact and may leave a
// dangling temp/manifest that verify reports; a crash after step 4 leaves a
// complete closed segment+manifest with no active file (recovered on open).
func (w *Writer) rotateLocked() error {
	if w.file == nil {
		return fmt.Errorf("audit: writer is closed")
	}
	if w.size == 0 {
		return nil
	}

	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("audit: sync active segment before rotate: %w", err)
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("audit: close active segment before rotate: %w", err)
	}
	w.file = nil
	if err := injectFault(faultRotateAfterCloseActive); err != nil {
		return err
	}

	sum, err := HashFile(w.path)
	if err != nil {
		return err
	}
	if err := injectFault(faultRotateAfterHash); err != nil {
		return err
	}

	closedAt := w.now()
	segName := FormatSegmentName(w.nextSeq, closedAt)
	segPath := filepath.Join(w.dir, segName)
	manPath := ManifestPathFor(segPath)

	manifest := SegmentManifest{
		SchemaVersion:       SchemaVersion,
		Segment:             segName,
		Sequence:            w.nextSeq,
		CreatedAt:           w.openedAt.UTC(),
		ClosedAt:            closedAt,
		RecordCount:         w.records,
		SizeBytes:           w.size,
		SHA256:              sum,
		PreviousSegmentHash: w.prevHash,
	}

	if err := injectFault(faultRotateBeforeManifest); err != nil {
		return err
	}
	if err := WriteManifestAtomic(manPath, manifest); err != nil {
		// Re-open active so subsequent writes are not lost after a failed rotate.
		_ = w.reopenActiveLocked()
		return err
	}
	if err := injectFault(faultRotateAfterManifest); err != nil {
		return err
	}

	if err := injectFault(faultRotateBeforeSegmentRename); err != nil {
		return err
	}
	if err := os.Rename(w.path, segPath); err != nil {
		_ = os.Remove(manPath)
		_ = w.reopenActiveLocked()
		return fmt.Errorf("audit: rename active segment: %w", err)
	}
	if err := injectFault(faultRotateAfterSegmentRename); err != nil {
		return err
	}

	w.prevHash = sum
	w.nextSeq++
	w.size = 0
	w.records = 0
	w.openedAt = closedAt

	if err := injectFault(faultRotateBeforeOpenActive); err != nil {
		return err
	}
	return w.reopenActiveLocked()
}

func (w *Writer) reopenActiveLocked() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open new active segment: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: stat new active segment: %w", err)
	}
	w.file = f
	w.size = info.Size()
	if w.size == 0 {
		w.records = 0
		w.openedAt = w.now()
	} else {
		w.records = countRecordsInFile(w.path)
	}
	return nil
}

func highestSequence(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("audit: read log directory: %w", err)
	}
	var max uint64
	for _, e := range entries {
		if seq, ok := ParseSegmentSequence(e.Name()); ok && seq > max {
			max = seq
		}
	}
	return max, nil
}

func latestSegmentHashByName(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("audit: read log directory: %w", err)
	}

	var bestName string
	var bestSeq uint64
	for _, e := range entries {
		seq, ok := ParseSegmentSequence(e.Name())
		if !ok {
			continue
		}
		if bestName == "" || seq > bestSeq || (seq == bestSeq && e.Name() > bestName) {
			bestSeq = seq
			bestName = e.Name()
		}
	}
	if bestName == "" {
		return "", nil
	}
	man, err := ReadManifest(ManifestPathFor(filepath.Join(dir, bestName)))
	if err != nil {
		// Fall back to hashing the segment body when the manifest is missing
		// (e.g. interrupted rotation); verify will report the fault separately.
		return HashFile(filepath.Join(dir, bestName))
	}
	return man.SHA256, nil
}

func countRecordsInFile(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0
	}
	var n int64
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}
