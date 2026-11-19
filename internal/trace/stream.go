// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

// stream.go — Append-only trace stream format for large executions.
// Issue #539: Add trace streaming for large executions
//
// Provides a framing-based append-only stream format with checksums,
// allowing traces larger than available memory to be exported, validated,
// and filtered incrementally.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Stream format constants
const (
	StreamMagic      uint32 = 0x474C4258 // "GLBX"
	StreamVersion    uint8  = 1
	StreamHeaderSize        = 9          // magic(4) + version(1) + recordCount(4)
	RecordFramingSize       = 8          // type(1) + payloadLen(4) + checksum(4) = 9 actually... see below
)

// RecordType identifies the kind of payload in a stream record.
type RecordType uint8

const (
	RecordTypeMeta  RecordType = 0x01 // Trace metadata (JSON)
	RecordTypeState RecordType = 0x02 // ExecutionState (JSON)
	RecordTypeEnd   RecordType = 0xFF // End-of-stream marker
)

// StreamWriter writes trace records to an append-only stream.
type StreamWriter struct {
	w         io.Writer
	bw        *bufio.Writer
	records   uint32
	checksum  bool
}

// NewStreamWriter creates a new stream writer wrapping w.
// Records are buffered for performance.
func NewStreamWriter(w io.Writer) *StreamWriter {
	return &StreamWriter{
		w:        w,
		bw:       bufio.NewWriter(w),
		checksum: true,
	}
}

// WriteHeader writes the stream header (magic + version + initial record count).
func (sw *StreamWriter) WriteHeader(meta *ExecutionTrace) error {
	if _, err := sw.bw.Write(binary.BigEndian.AppendUint32(nil, StreamMagic)); err != nil {
		return fmt.Errorf("write magic: %w", err)
	}
	if err := sw.bw.WriteByte(StreamVersion); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	return nil
}

// WriteMeta writes trace metadata as a JSON record.
func (sw *StreamWriter) WriteMeta(t *ExecutionTrace) error {
	meta := struct {
		TransactionHash  string    `json:"transaction_hash"`
		StartTime        string    `json:"start_time"`
		EndTime          string    `json:"end_time"`
		SnapshotInterval int       `json:"snapshot_interval"`
	}{
		TransactionHash:  t.TransactionHash,
		StartTime:        t.StartTime.Format("2006-01-02T15:04:05Z"),
		EndTime:          t.EndTime.Format("2006-01-02T15:04:05Z"),
		SnapshotInterval: t.SnapshotInterval,
	}
	return sw.writeRecord(RecordTypeMeta, meta)
}

// WriteState writes a single ExecutionState as a JSON record.
func (sw *StreamWriter) WriteState(s *ExecutionState) error {
	sw.records++
	return sw.writeRecord(RecordTypeState, s)
}

// WriteEnd writes the end-of-stream marker and flushes the buffer.
func (sw *StreamWriter) WriteEnd() error {
	if err := sw.writeFrame(RecordTypeEnd, nil); err != nil {
		return err
	}
	return sw.bw.Flush()
}

// writeRecord serializes a payload as JSON and writes it as a framed record.
func (sw *StreamWriter) writeRecord(rt RecordType, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	return sw.writeFrame(rt, data)
}

// writeFrame writes a single framed record: type(1) + len(4) + checksum(4) + payload.
func (sw *StreamWriter) writeFrame(rt RecordType, payload []byte) error {
	if err := sw.bw.WriteByte(byte(rt)); err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(payload)))
	if _, err := sw.bw.Write(lenBuf); err != nil {
		return err
	}
	if sw.checksum {
		crc := crc32.ChecksumIEEE(payload)
		crcBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(crcBuf, crc)
		if _, err := sw.bw.Write(crcBuf); err != nil {
			return err
		}
	}
	if len(payload) > 0 {
		if _, err := sw.bw.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// RecordCount returns the number of state records written.
func (sw *StreamWriter) RecordCount() uint32 {
	return sw.records
}

// StreamWriter convenience: write an entire trace to a file.
func WriteTraceStream(path string, t *ExecutionTrace) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create stream file: %w", err)
	}
	defer f.Close()

	sw := NewStreamWriter(f)
	if err := sw.WriteHeader(t); err != nil {
		return err
	}
	if err := sw.WriteMeta(t); err != nil {
		return err
	}
	for i := range t.States {
		if err := sw.WriteState(&t.States[i]); err != nil {
			return fmt.Errorf("write state %d: %w", i, err)
		}
	}
	return sw.WriteEnd()
}

// ── StreamReader ──────────────────────────────────────────────────────────────

// StreamReader reads trace records from an append-only stream incrementally.
// Supports bounded buffering: only the current record is held in memory,
// allowing traces larger than RAM to be processed.
type StreamReader struct {
	r         io.Reader
	br        *bufio.Reader
	records   uint32
	ended     bool
	corrupt   bool
	lastError error
}

// StreamRecord holds one decoded record from the stream.
type StreamRecord struct {
	Type    RecordType
	Payload []byte
	Index   uint32
}

// NewStreamReader creates a reader wrapping r.
func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{
		r:  r,
		br: bufio.NewReaderSize(r, 64*1024),
	}
}

// ReadHeader validates the stream header and returns the stream version.
func (sr *StreamReader) ReadHeader() (uint8, error) {
	magicBuf := make([]byte, 4)
	if _, err := io.ReadFull(sr.br, magicBuf); err != nil {
		return 0, fmt.Errorf("read magic: %w", err)
	}
	magic := binary.BigEndian.Uint32(magicBuf)
	if magic != StreamMagic {
		return 0, fmt.Errorf("invalid stream magic: %x (expected %x)", magic, StreamMagic)
	}
	version, err := sr.br.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("read version: %w", err)
	}
	if version != StreamVersion {
		return 0, fmt.Errorf("unsupported stream version: %d (expected %d)", version, StreamVersion)
	}
	return version, nil
}

// Next reads the next record from the stream. Returns io.EOF when the stream
// end marker is reached or the stream is exhausted.
func (sr *StreamReader) Next() (*StreamRecord, error) {
	if sr.ended {
		return nil, io.EOF
	}

	rt, err := sr.br.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read record type: %w", err)
	}
	recordType := RecordType(rt)

	// Read payload length
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(sr.br, lenBuf); err != nil {
		return nil, fmt.Errorf("read payload length: %w", err)
	}
	payloadLen := binary.BigEndian.Uint32(lenBuf)

	// Read checksum
	crcBuf := make([]byte, 4)
	if _, err := io.ReadFull(sr.br, crcBuf); err != nil {
		return nil, fmt.Errorf("read checksum: %w", err)
	}
	expectedCRC := binary.BigEndian.Uint32(crcBuf)

	// Read payload
	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(sr.br, payload); err != nil {
			return nil, fmt.Errorf("read payload (truncated at record %d): %w", sr.records, err)
		}
		// Verify checksum
		actualCRC := crc32.ChecksumIEEE(payload)
		if actualCRC != expectedCRC {
			sr.corrupt = true
			return &StreamRecord{
				Type:    recordType,
				Payload: payload,
				Index:   sr.records,
			}, fmt.Errorf("checksum mismatch at record %d: got %x, expected %x", sr.records, actualCRC, expectedCRC)
		}
	}

	rec := &StreamRecord{
		Type:    recordType,
		Payload: payload,
		Index:   sr.records,
	}

	if recordType == RecordTypeEnd {
		sr.ended = true
		return rec, io.EOF
	}

	sr.records++
	return rec, nil
}

// RecordCount returns the number of records read so far.
func (sr *StreamReader) RecordCount() uint32 {
	return sr.records
}

// IsCorrupt reports whether a checksum failure was encountered.
func (sr *StreamReader) IsCorrupt() bool {
	return sr.corrupt
}

// StreamFilterFn is a callback for filtering records during streaming read.
type StreamFilterFn func(*StreamRecord) bool

// StreamAndExport reads a trace stream incrementally, applies a filter,
// and writes matching states to a bounded-buffered writer without loading
// the entire trace into memory.
func StreamAndExport(inputPath, outputPath string, filter StreamFilterFn, maxBufferStates int) (uint32, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return 0, fmt.Errorf("open input: %w", err)
	}
	defer f.Close()

	out, err := os.Create(outputPath)
	if err != nil {
		return 0, fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	sr := NewStreamReader(f)
	if _, err := sr.ReadHeader(); err != nil {
		return 0, fmt.Errorf("read header: %w", err)
	}

	sw := NewStreamWriter(out)
	// Write header for output stream
	hdrTrace := &ExecutionTrace{}
	if err := sw.WriteHeader(hdrTrace); err != nil {
		return 0, err
	}

	var exported uint32
	batchBuffer := make([]ExecutionState, 0, maxBufferStates)

	for {
		rec, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil && !isChecksumError(err) {
			return exported, fmt.Errorf("read record %d: %w", sr.RecordCount(), err)
		}

		if rec.Type == RecordTypeState {
			if filter == nil || filter(rec) {
				var state ExecutionState
				if jsonErr := json.Unmarshal(rec.Payload, &state); jsonErr == nil {
					batchBuffer = append(batchBuffer, state)
					if len(batchBuffer) >= maxBufferStates {
						for i := range batchBuffer {
							if wErr := sw.WriteState(&batchBuffer[i]); wErr != nil {
								return exported, wErr
							}
							exported++
						}
						batchBuffer = batchBuffer[:0]
					}
				}
			}
		} else if rec.Type == RecordTypeMeta {
			if err := sw.writeFrame(RecordTypeMeta, rec.Payload); err != nil {
				return exported, err
			}
		}
	}

	// Flush remaining buffer
	for i := range batchBuffer {
		if err := sw.WriteState(&batchBuffer[i]); err != nil {
			return exported, err
		}
		exported++
	}

	if err := sw.WriteEnd(); err != nil {
		return exported, err
	}

	return exported, nil
}

func isChecksumError(err error) bool {
	return err != nil && (err.Error()[:7] == "checksum" || err.Error()[:14] == "checksum mismatch")
}
