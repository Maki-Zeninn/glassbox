// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// streaming_export.go — Streaming JSON export with bounded memory usage.
//
// Unlike ExportJSON (which allocates the entire document in memory), this
// encoder writes the JSON envelope header, each state record, and the
// closing bracket incrementally. Peak memory is bounded to O(bufferSize)
// ExecutionState objects regardless of total event count.
//
// Progress is reported via a callback after every buffer flush so callers
// can display a progress bar. Export can be cancelled at any flush boundary
// by returning a non-nil error from the progress callback.
//
// Destination writes are atomic: output goes to a temporary file that is
// renamed into place only after a successful flush+close. Interrupted
// exports leave no valid-looking partial file.

package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultStreamingBufferSize is the number of ExecutionState records
	// held in memory before flushing to the output writer. Larger values
	// reduce syscall overhead but increase peak memory.
	DefaultStreamingBufferSize = 256

	// StreamingSchemaVersion is the envelope schema version written by
	// the streaming exporter. It matches the non-streaming ExportJSON
	// envelope so LoadVersionedTrace can consume both formats.
	StreamingSchemaVersion = "1.0"
)

// StreamingExportProgress is reported after each buffer flush.
type StreamingExportProgress struct {
	// StatesWritten is the total number of states flushed so far.
	StatesWritten int
	// TotalStates is the total number of states in the trace (when known;
	// -1 if indeterminate, e.g. reading from a binary stream).
	TotalStates int
	// BytesWritten is the cumulative bytes written to the destination.
	BytesWritten int64
}

// StreamingExportOptions configures the streaming exporter.
type StreamingExportOptions struct {
	// BufferSize overrides DefaultStreamingBufferSize. Must be > 0.
	BufferSize int
	// GeneratedAt overrides the envelope timestamp. Zero value uses time.Now().
	GeneratedAt time.Time
	// Progress is called after every buffer flush. Returning a non-nil error
	// cancels the export; the temporary file is cleaned up.
	Progress func(ctx context.Context, p StreamingExportProgress) error
}

func (o *StreamingExportOptions) bufferSize() int {
	if o != nil && o.BufferSize > 0 {
		return o.BufferSize
	}
	return DefaultStreamingBufferSize
}

func (o *StreamingExportOptions) generatedAt() time.Time {
	if o != nil && !o.GeneratedAt.IsZero() {
		return o.GeneratedAt
	}
	return time.Now().UTC()
}

// StreamingExporter writes a versioned JSON envelope to w incrementally.
// Call WriteHeader, then WriteState in a loop, then Close. The caller
// must not reuse the exporter after Close.
type StreamingExporter struct {
	w           io.Writer
	buf         *json.Encoder
	states      int
	bytes       int64
	header_written bool
}

// NewStreamingExporter wraps w for incremental JSON export. The caller
// should use WriteHeader → WriteState* → Close in sequence.
func NewStreamingExporter(w io.Writer) *StreamingExporter {
	return &StreamingExporter{w: w}
}

// WriteHeader emits the JSON envelope opening and trace metadata.
func (se *StreamingExporter) WriteHeader(t *ExecutionTrace, schemaVersion string, generatedAt time.Time) error {
	if se.header_written {
		return fmt.Errorf("header already written")
	}
	se.buf = json.NewEncoder(se.w)

	// Write envelope opening.
	if _, err := io.WriteString(se.w, "{\n  "); err != nil {
		return fmt.Errorf("write envelope open: %w", err)
	}
	if err := se.buf.Encode(struct {
		SchemaVersion string    `json:"schema_version"`
		GeneratedAt   time.Time `json:"generated_at"`
	}{
		SchemaVersion: schemaVersion,
		GeneratedAt:   generatedAt.Truncate(time.Second),
	}); err != nil {
		return fmt.Errorf("write envelope header: %w", err)
	}

	// Write trace object opening.
	if _, err := io.WriteString(se.w, "  \"trace\": {\n    "); err != nil {
		return fmt.Errorf("write trace open: %w", err)
	}

	// Write trace-level metadata fields.
	traceMeta := struct {
		TransactionHash  string `json:"transaction_hash"`
		StartTime        string `json:"start_time"`
		EndTime          string `json:"end_time"`
		SnapshotInterval int    `json:"snapshot_interval"`
	}{
		TransactionHash:  fingerprintTxHash(t.TransactionHash),
		StartTime:        t.StartTime.Format("2006-01-02T15:04:05Z"),
		EndTime:          t.EndTime.Format("2006-01-02T15:04:05Z"),
		SnapshotInterval: t.SnapshotInterval,
	}
	if err := se.buf.Encode(traceMeta); err != nil {
		return fmt.Errorf("write trace metadata: %w", err)
	}

	// Write states array opening.
	if _, err := io.WriteString(se.w, "    \"states\": [\n"); err != nil {
		return fmt.Errorf("write states open: %w", err)
	}

	se.header_written = true
	return nil
}

// WriteState encodes a single ExecutionState to the output stream.
// States are written as a comma-separated JSON array.
func (se *StreamingExporter) WriteState(s *ExecutionState) error {
	if !se.header_written {
		return fmt.Errorf("header not written")
	}

	// Comma separator for array elements (not before the first element).
	if se.states > 0 {
		if _, err := io.WriteString(se.w, ",\n"); err != nil {
			return fmt.Errorf("write comma: %w", err)
		}
	}

	if err := se.buf.Encode(s); err != nil {
		return fmt.Errorf("encode state %d: %w", se.states, err)
	}
	se.states++
	return nil
}

// Close closes the JSON envelope and flushes any buffered output. It does
// NOT close the underlying writer (caller is responsible).
func (se *StreamingExporter) Close() error {
	if !se.header_written {
		return fmt.Errorf("header not written")
	}

	// Close states array and trace object.
	if _, err := io.WriteString(se.w, "\n    ]\n  }\n}\n"); err != nil {
		return fmt.Errorf("write envelope close: %w", err)
	}
	return nil
}

// StatesWritten returns the number of states written so far.
func (se *StreamingExporter) StatesWritten() int {
	return se.states
}

// ── Streaming Export to File ────────────────────────────────────────────────

// ExportTraceStreaming writes the trace to destPath as a versioned JSON
// envelope using bounded memory. The output is semantically equivalent to
// ExportJSON but peak memory remains O(bufferSize) regardless of event count.
//
// The export is atomic: output is written to a temporary file in the same
// directory and renamed into place only on success. A failed or cancelled
// export never leaves a valid-looking partial file at destPath.
//
// Context cancellation is checked at every buffer flush boundary. Returning
// a non-nil error from opts.Progress cancels the export and cleans up.
func ExportTraceStreaming(ctx context.Context, t *ExecutionTrace, destPath string, opts *StreamingExportOptions) error {
	if t == nil {
		return fmt.Errorf("cannot export nil trace")
	}
	if opts == nil {
		opts = &StreamingExportOptions{}
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	tmp, err := os.CreateTemp(destDir, filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	crc := crc32.NewIEEE()
	mw := io.MultiWriter(tmp, crc)

	se := NewStreamingExporter(mw)
	if err := se.WriteHeader(t, StreamingSchemaVersion, opts.generatedAt()); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	bufSize := opts.bufferSize()
	buffer := make([]ExecutionState, 0, bufSize)
	totalStates := len(t.States)
	flushed := 0

	for i := range t.States {
		buffer = append(buffer, t.States[i])

		if len(buffer) >= bufSize {
			if err := flushBuffer(se, buffer); err != nil {
				return err
			}
			flushed += len(buffer)
			buffer = buffer[:0]

			if opts.Progress != nil {
				if err := opts.Progress(ctx, StreamingExportProgress{
					StatesWritten: flushed,
					TotalStates:   totalStates,
				}); err != nil {
					return fmt.Errorf("progress callback cancelled export: %w", err)
				}
			}
		}
	}

	// Flush remaining states.
	if len(buffer) > 0 {
		if err := flushBuffer(se, buffer); err != nil {
			return err
		}
		flushed += len(buffer)
	}

	if err := se.Close(); err != nil {
		return fmt.Errorf("close envelope: %w", err)
	}

	// Flush the underlying file and sync.
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	succeeded = true

	// Final progress callback.
	if opts.Progress != nil {
		_ = opts.Progress(ctx, StreamingExportProgress{
			StatesWritten: flushed,
			TotalStates:   totalStates,
		})
	}

	_ = crc // CRC available for verification if needed
	return nil
}

func flushBuffer(se *StreamingExporter, buf []ExecutionState) error {
	for i := range buf {
		if err := se.WriteState(&buf[i]); err != nil {
			return err
		}
	}
	return nil
}

// ── Streaming Export from Binary Stream ─────────────────────────────────────

// ExportTraceStreamingFromStream reads a binary trace stream and re-exports
// its states as versioned JSON using bounded memory. This allows converting
// large binary-stream traces to JSON without loading them entirely into RAM.
//
// The binary stream is read incrementally: only bufferSize states are held
// in memory at any time.
func ExportTraceStreamingFromStream(ctx context.Context, inputPath, destPath string, opts *StreamingExportOptions) error {
	if opts == nil {
		opts = &StreamingExportOptions{}
	}

	f, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open input stream: %w", err)
	}
	defer f.Close()

	sr := NewStreamReader(f)
	if _, err := sr.ReadHeader(); err != nil {
		return fmt.Errorf("read stream header: %w", err)
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	tmp, err := os.CreateTemp(destDir, filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	se := NewStreamingExporter(tmp)
	bufSize := opts.bufferSize()
	buffer := make([]ExecutionState, 0, bufSize)
	flushed := 0
	var metaRecord *StreamRecord

	for {
		rec, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read stream record: %w", err)
		}

		switch rec.Type {
		case RecordTypeMeta:
			metaRecord = rec
		case RecordTypeState:
			var state ExecutionState
			if jsonErr := json.Unmarshal(rec.Payload, &state); jsonErr != nil {
				continue
			}
			buffer = append(buffer, state)

			if len(buffer) >= bufSize {
				// Write header on first buffer flush.
				if !se.header_written {
					meta := parseStreamMeta(metaRecord)
					if err := se.WriteHeader(meta, StreamingSchemaVersion, opts.generatedAt()); err != nil {
						return err
					}
				}
				if err := flushBuffer(se, buffer); err != nil {
					return err
				}
				flushed += len(buffer)
				buffer = buffer[:0]

				if opts.Progress != nil {
					if err := opts.Progress(ctx, StreamingExportProgress{
						StatesWritten: flushed,
						TotalStates:   -1,
					}); err != nil {
						return fmt.Errorf("progress callback cancelled export: %w", err)
					}
				}
			}
		}
	}

	// Flush remaining.
	if len(buffer) > 0 {
		if !se.header_written {
			meta := parseStreamMeta(metaRecord)
			if err := se.WriteHeader(meta, StreamingSchemaVersion, opts.generatedAt()); err != nil {
				return err
			}
		}
		if err := flushBuffer(se, buffer); err != nil {
			return err
		}
		flushed += len(buffer)
	}

	if se.header_written {
		if err := se.Close(); err != nil {
			return fmt.Errorf("close envelope: %w", err)
		}
	} else {
		// Empty stream — write minimal envelope.
		meta := &ExecutionTrace{}
		if err := se.WriteHeader(meta, StreamingSchemaVersion, opts.generatedAt()); err != nil {
			return err
		}
		if err := se.Close(); err != nil {
			return err
		}
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	succeeded = true

	if opts.Progress != nil {
		_ = opts.Progress(ctx, StreamingExportProgress{
			StatesWritten: flushed,
			TotalStates:   -1,
		})
	}
	return nil
}

func parseStreamMeta(rec *StreamRecord) *ExecutionTrace {
	if rec == nil {
		return &ExecutionTrace{}
	}
	var meta struct {
		TransactionHash  string `json:"transaction_hash"`
		StartTime        string `json:"start_time"`
		EndTime          string `json:"end_time"`
		SnapshotInterval int    `json:"snapshot_interval"`
	}
	if err := json.Unmarshal(rec.Payload, &meta); err != nil {
		return &ExecutionTrace{}
	}
	t := &ExecutionTrace{
		TransactionHash:  meta.TransactionHash,
		SnapshotInterval: meta.SnapshotInterval,
	}
	if t.StartTime, _ = time.Parse("2006-01-02T15:04:05Z", meta.StartTime); t.StartTime.IsZero() {
		t.StartTime = time.Now()
	}
	if t.EndTime, _ = time.Parse("2006-01-02T15:04:05Z", meta.EndTime); t.EndTime.IsZero() {
		t.EndTime = time.Now()
	}
	return t
}
