// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// SchemaVersion identifies the segment manifest format.
	SchemaVersion = "1"

	// ActiveSegmentName is the filename of the currently open audit segment.
	ActiveSegmentName = "current.jsonl"

	// SegmentFilePrefix is the common prefix for closed segment files.
	SegmentFilePrefix = "segment-"

	// ManifestSuffix is appended (replacing .jsonl) for immutable manifests.
	ManifestSuffix = ".manifest.json"
)

// segmentNamePattern matches closed segment bodies:
// segment-<seq>-<UTC timestamp>.jsonl
var segmentNamePattern = regexp.MustCompile(`^segment-(\d{6})-(\d{8}T\d{6}Z)\.jsonl$`)

// SegmentManifest is the immutable metadata written when a segment is closed.
// Once written it is never modified; retention deletes the segment and its
// manifest together.
type SegmentManifest struct {
	// SchemaVersion identifies the manifest format.
	SchemaVersion string `json:"schema_version"`
	// Segment is the closed segment filename (no directory component).
	Segment string `json:"segment"`
	// Sequence is the monotonic segment index, starting at 1.
	Sequence uint64 `json:"sequence"`
	// CreatedAt is when the segment was opened (UTC).
	CreatedAt time.Time `json:"created_at"`
	// ClosedAt is when the segment was rotated closed (UTC).
	ClosedAt time.Time `json:"closed_at"`
	// RecordCount is the number of complete records in the segment.
	RecordCount int64 `json:"record_count"`
	// SizeBytes is the byte length of the segment body.
	SizeBytes int64 `json:"size_bytes"`
	// SHA256 is the lower-case hex SHA-256 of the segment body.
	SHA256 string `json:"sha256"`
	// PreviousSegmentHash is the SHA-256 of the immediately preceding closed
	// segment body. Empty for the genesis segment.
	PreviousSegmentHash string `json:"previous_segment_hash,omitempty"`
}

// SegmentInfo describes a closed segment discovered on disk.
type SegmentInfo struct {
	Path         string
	ManifestPath string
	Manifest     SegmentManifest
	ModTime      time.Time
	SizeBytes    int64
}

// FormatSegmentName builds a closed-segment filename from sequence and close time.
func FormatSegmentName(sequence uint64, closedAt time.Time) string {
	ts := closedAt.UTC().Format("20060102T150405Z")
	return fmt.Sprintf("%s%06d-%s.jsonl", SegmentFilePrefix, sequence, ts)
}

// ManifestPathFor returns the immutable manifest path for a segment body path
// or filename.
func ManifestPathFor(segmentPath string) string {
	dir := filepath.Dir(segmentPath)
	base := filepath.Base(segmentPath)
	name := strings.TrimSuffix(base, ".jsonl") + ManifestSuffix
	if dir == "." || dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

// ParseSegmentSequence extracts the sequence number from a closed segment name.
func ParseSegmentSequence(name string) (uint64, bool) {
	m := segmentNamePattern.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	seq, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

// IsClosedSegmentName reports whether name is a closed audit segment body.
func IsClosedSegmentName(name string) bool {
	return segmentNamePattern.MatchString(name)
}

// HashFile returns the lower-case hex SHA-256 of the file at path.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("audit: read %q for hash: %w", path, err)
	}
	return HashBytes(data), nil
}

// HashBytes returns the lower-case hex SHA-256 of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ValidateSHA256Hex verifies that h is a 64-character hexadecimal SHA-256 digest.
// This mirrors the audit:verify chain-hash validation primitive.
func ValidateSHA256Hex(label, h string) error {
	if len(h) != 64 {
		return fmt.Errorf("%s must be a 64-character hex string (SHA-256), got %d characters", label, len(h))
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("%s contains non-hex character %q", label, c)
		}
	}
	return nil
}

// WriteManifestAtomic writes an immutable segment manifest via temp+rename.
func WriteManifestAtomic(path string, m SegmentManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("audit: marshal segment manifest: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

// ReadManifest loads a segment manifest from path.
func ReadManifest(path string) (*SegmentManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("audit: read manifest %q: %w", path, err)
	}
	var m SegmentManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("audit: parse manifest %q: %w", path, err)
	}
	return &m, nil
}

// ListSegments returns closed segments in the directory ordered by sequence.
func ListSegments(dir string) ([]SegmentInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("audit: read log directory %q: %w", dir, err)
	}

	var out []SegmentInfo
	for _, e := range entries {
		if e.IsDir() || !IsClosedSegmentName(e.Name()) {
			continue
		}
		segPath := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("audit: stat segment %q: %w", segPath, err)
		}
		manPath := ManifestPathFor(segPath)
		man, err := ReadManifest(manPath)
		if err != nil {
			return nil, err
		}
		out = append(out, SegmentInfo{
			Path:         segPath,
			ManifestPath: manPath,
			Manifest:     *man,
			ModTime:      info.ModTime().UTC(),
			SizeBytes:    info.Size(),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Manifest.Sequence == out[j].Manifest.Sequence {
			return out[i].Path < out[j].Path
		}
		return out[i].Manifest.Sequence < out[j].Manifest.Sequence
	})
	return out, nil
}
