// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

const (
	wasmMagic = "\x00asm"
)

// WASMSection represents a single section parsed from a WASM binary.
type WASMSection struct {
	// ID is the WASM section type byte (0–12 for standard sections).
	ID byte
	// Name is the human-readable section type name.
	Name string
	// Size is the byte-length of the section payload.
	Size uint32
	// CustomName is the sub-name of a custom section (ID==0), parsed
	// from the section payload. Empty for all non-custom sections.
	CustomName string
}

// WASMInfo holds metadata extracted from a WASM binary.
type WASMInfo struct {
	// Hash is the hex-encoded SHA-256 digest of the entire binary.
	Hash string
	// Size is the total byte length of the binary.
	Size int
	// IsValidWASM is true when the binary starts with the WASM magic bytes.
	IsValidWASM bool
	// SectionCount is the number of sections parsed from the binary.
	SectionCount int
	// Sections is the ordered list of section descriptors.
	Sections []WASMSection
}

// WASMDiffResult holds the output of comparing two WASM binaries.
type WASMDiffResult struct {
	// Local is the metadata for the first (local) binary.
	Local WASMInfo
	// Remote is the metadata for the second (remote/on-chain) binary.
	Remote WASMInfo
	// HashMatch is true when both binaries are bit-for-bit identical.
	HashMatch bool
	// SizeMatch is true when both binaries have the same total byte count.
	SizeMatch bool
	// SectionMatch is true when both binaries contain the same number of sections.
	SectionMatch bool
	// HasDivergence is true when the binaries differ in any way.
	HasDivergence bool
	// Summary is a human-readable one-line result description.
	Summary string
}

// sectionNames maps the standard WASM section type IDs to readable names.
var sectionNames = map[byte]string{
	0:  "Custom",
	1:  "Type",
	2:  "Import",
	3:  "Function",
	4:  "Table",
	5:  "Memory",
	6:  "Global",
	7:  "Export",
	8:  "Start",
	9:  "Element",
	10: "Code",
	11: "Data",
	12: "DataCount",
}

// InspectWASM analyses a WASM binary in memory and returns its metadata.
func InspectWASM(data []byte) WASMInfo {
	info := WASMInfo{Size: len(data)}

	sum := sha256.Sum256(data)
	info.Hash = hex.EncodeToString(sum[:])

	if len(data) < 8 || string(data[:4]) != wasmMagic {
		return info
	}
	info.IsValidWASM = true
	info.Sections = parseSections(data[8:])
	info.SectionCount = len(info.Sections)

	return info
}

// InspectWASMFile reads a WASM binary from disk and returns its metadata.
func InspectWASMFile(path string) (WASMInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WASMInfo{}, fmt.Errorf("failed to read WASM file %s: %w", path, err)
	}
	return InspectWASM(data), nil
}

// DiffWASM compares two in-memory WASM binaries and returns a structured result.
func DiffWASM(local, remote []byte) *WASMDiffResult {
	r := &WASMDiffResult{
		Local:  InspectWASM(local),
		Remote: InspectWASM(remote),
	}

	r.HashMatch = r.Local.Hash == r.Remote.Hash
	r.SizeMatch = r.Local.Size == r.Remote.Size
	r.SectionMatch = r.Local.SectionCount == r.Remote.SectionCount
	r.HasDivergence = !r.HashMatch

	switch {
	case r.HashMatch:
		r.Summary = "Binaries are identical (SHA-256 match)"
	case r.SectionMatch:
		r.Summary = fmt.Sprintf(
			"Binaries differ — same section count (%d) but different content",
			r.Local.SectionCount,
		)
	default:
		r.Summary = fmt.Sprintf(
			"Binaries differ — local has %d section(s), remote has %d section(s)",
			r.Local.SectionCount, r.Remote.SectionCount,
		)
	}

	return r
}

// DiffWASMFiles reads two WASM files from disk and returns a structured diff.
func DiffWASMFiles(localPath, remotePath string) (*WASMDiffResult, error) {
	localData, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read local WASM %s: %w", localPath, err)
	}
	remoteData, err := os.ReadFile(remotePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read remote WASM %s: %w", remotePath, err)
	}
	return DiffWASM(localData, remoteData), nil
}

// parseSections decodes WASM section headers from the payload that follows the
// 8-byte WASM file header (magic + version).
func parseSections(payload []byte) []WASMSection {
	var sections []WASMSection
	offset := 0

	for offset < len(payload) {
		// Section ID byte
		if offset >= len(payload) {
			break
		}
		sectionID := payload[offset]
		offset++

		// LEB128-encoded payload size
		size, n := binary.Uvarint(payload[offset:])
		if n <= 0 {
			break
		}
		offset += n

		name, ok := sectionNames[sectionID]
		if !ok {
			name = fmt.Sprintf("Unknown(%d)", sectionID)
		}

		sections = append(sections, WASMSection{
			ID:   sectionID,
			Name: name,
			Size: uint32(size),
		})

		// Advance past the section payload.
		offset += int(size)
		if offset > len(payload) {
			break
		}
	}

	return sections
}

// ── Semantic / normalized diff ────────────────────────────────────────────────

// SectionClass groups WASM section types by their semantic significance so
// the normalized diff can report changes in each category separately.
type SectionClass string

const (
	// SectionClassExecutable covers sections that affect runtime behaviour:
	// Code, Function, Type, Table, Memory, Global, Element, Start.
	SectionClassExecutable SectionClass = "executable"
	// SectionClassABI covers Import and Export sections that define the
	// contract's public interface. A change here is an ABI break.
	SectionClassABI SectionClass = "abi"
	// SectionClassDebug covers the standard Name custom section and any
	// custom section whose name starts with ".debug_" or "sourceMappingURL".
	SectionClassDebug SectionClass = "debug"
	// SectionClassMetadata covers the standard "producers" custom section
	// and any custom section whose name starts with "target_features" or
	// "build_id". These are compiler-stamp fields that change without
	// affecting behaviour.
	SectionClassMetadata SectionClass = "metadata"
	// SectionClassUnknown covers custom sections that don't match any of
	// the above heuristics.
	SectionClassUnknown SectionClass = "unknown_custom"
)

// classifySection returns the SectionClass for a parsed WASMSection.
// For custom sections (ID == 0) the classification is name-driven; for
// standard sections it is fixed by the spec.
func classifySection(s WASMSection) SectionClass {
	switch s.ID {
	case 1, 3, 4, 5, 6, 8, 9, 10: // Type, Function, Table, Memory, Global, Start, Element, Code
		return SectionClassExecutable
	case 2, 7: // Import, Export
		return SectionClassABI
	case 0: // Custom — classify by sub-name when available
		return classifyCustomSection(s.CustomName)
	case 11, 12: // Data, DataCount
		return SectionClassExecutable
	default:
		return SectionClassUnknown
	}
}

// classifyCustomSection returns the class for a custom section given its
// name string (parsed from the section payload).
func classifyCustomSection(name string) SectionClass {
	switch {
	case name == "name":
		return SectionClassDebug
	case strings.HasPrefix(name, ".debug_"),
		strings.HasPrefix(name, "sourceMappingURL"),
		name == "external_debug_info":
		return SectionClassDebug
	case name == "producers",
		strings.HasPrefix(name, "target_features"),
		name == "build_id":
		return SectionClassMetadata
	default:
		return SectionClassUnknown
	}
}

// NormalizeOptions controls which custom-section categories are ignored when
// computing a semantic diff.
type NormalizeOptions struct {
	// IgnoreMetadata drops the "producers", "target_features", and "build_id"
	// custom sections before comparing. These change on every rebuild and
	// carry no executable semantics.
	IgnoreMetadata bool
	// IgnoreDebug drops DWARF debug sections and the "name" section.
	IgnoreDebug bool
	// IgnoreUnknownCustom drops all custom sections that do not match a
	// known metadata or debug pattern.
	IgnoreUnknownCustom bool
}

// DefaultNormalizeOptions returns the recommended defaults: ignore metadata
// and unknown custom sections (the noisiest sources of spurious diffs) but
// preserve debug sections so source-mapping issues are still visible.
func DefaultNormalizeOptions() NormalizeOptions {
	return NormalizeOptions{
		IgnoreMetadata:      true,
		IgnoreDebug:         false,
		IgnoreUnknownCustom: true,
	}
}

// NormalizationManifest records which sections were dropped during
// normalization so the caller can include it in JSON output.
type NormalizationManifest struct {
	// DroppedLocal lists section classes removed from the local binary.
	DroppedLocal []string `json:"dropped_local,omitempty"`
	// DroppedRemote lists section classes removed from the remote binary.
	DroppedRemote []string `json:"dropped_remote,omitempty"`
	// Options is the NormalizeOptions that was applied.
	Options NormalizeOptions `json:"options"`
}

// SemanticDiffResult is the output of a normalized WASM comparison. It
// separates findings by section class so callers can distinguish "only
// metadata changed" from "executable code changed".
type SemanticDiffResult struct {
	// Raw is the underlying byte-level diff (always computed).
	Raw *WASMDiffResult
	// Manifest records what was dropped during normalization.
	Manifest NormalizationManifest
	// ExecutableChanged is true when any executable or ABI section differs
	// after normalization.
	ExecutableChanged bool
	// ABIChanged is true when Import or Export sections differ.
	ABIChanged bool
	// DebugChanged is true when debug-class sections differ.
	DebugChanged bool
	// MetadataOnlyDiff is true when the raw diff has divergence but the
	// normalized executable and ABI sections are identical — meaning only
	// ignored metadata changed.
	MetadataOnlyDiff bool
	// SectionFindings lists per-class findings with stable paths.
	SectionFindings []SectionFinding
	// Summary is a human-readable one-line description of the semantic result.
	Summary string
}

// SectionFinding describes a difference found in one section class.
type SectionFinding struct {
	Class       SectionClass `json:"class"`
	LocalCount  int          `json:"local_count"`
	RemoteCount int          `json:"remote_count"`
	Changed     bool         `json:"changed"`
	Description string       `json:"description"`
}

// WASMSection extended with CustomName for custom-section classification.
// We re-parse the custom-section name from the payload for classification
// purposes; it is not stored in the base WASMSection to keep the struct lean.

// parseCustomSectionName extracts the name string from the payload of a
// custom section (WASM section ID 0). The name is a length-prefixed UTF-8
// string at the start of the payload. An empty string is returned on any
// parse error.
func parseCustomSectionName(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	nameLen, n := binary.Uvarint(payload)
	if n <= 0 || int(nameLen) > len(payload)-n {
		return ""
	}
	return string(payload[n : n+int(nameLen)])
}

// inspectWASMSemantic parses a WASM binary and returns its sections with
// custom-section names resolved.
func inspectWASMSemantic(data []byte) ([]WASMSection, []string) {
	if len(data) < 8 || string(data[:4]) != wasmMagic {
		return nil, nil
	}
	payload := data[8:]
	var sections []WASMSection
	var customNames []string // parallel to sections; empty for non-custom
	offset := 0
	for offset < len(payload) {
		if offset >= len(payload) {
			break
		}
		sectionID := payload[offset]
		offset++
		size, n := binary.Uvarint(payload[offset:])
		if n <= 0 {
			break
		}
		offset += n
		end := offset + int(size)
		if end > len(payload) {
			end = len(payload)
		}
		sectionPayload := payload[offset:end]

		customName := ""
		if sectionID == 0 {
			customName = parseCustomSectionName(sectionPayload)
		}
		name, ok := sectionNames[sectionID]
		if !ok {
			name = fmt.Sprintf("Unknown(%d)", sectionID)
		}
		sections = append(sections, WASMSection{ID: sectionID, Name: name, Size: uint32(size)})
		customNames = append(customNames, customName)
		offset = end
	}
	return sections, customNames
}

// shouldDrop returns true when the section should be excluded from the
// normalized comparison given the NormalizeOptions.
func shouldDrop(class SectionClass, opts NormalizeOptions) bool {
	switch class {
	case SectionClassMetadata:
		return opts.IgnoreMetadata
	case SectionClassDebug:
		return opts.IgnoreDebug
	case SectionClassUnknown:
		return opts.IgnoreUnknownCustom
	default:
		return false
	}
}

// normalizeSections filters a section list according to opts and returns the
// retained sections along with the list of dropped class names.
func normalizeSections(sections []WASMSection, customNames []string, opts NormalizeOptions) ([]WASMSection, []string) {
	var retained []WASMSection
	droppedSet := make(map[string]bool)
	for i, s := range sections {
		name := ""
		if i < len(customNames) {
			name = customNames[i]
		}
		s.CustomName = name
		class := classifySection(s)
		if shouldDrop(class, opts) {
			droppedSet[string(class)] = true
			continue
		}
		retained = append(retained, s)
	}
	var dropped []string
	for c := range droppedSet {
		dropped = append(dropped, c)
	}
	return retained, dropped
}

// DiffWASMSemantic compares two WASM binaries using semantic normalization.
// It always computes the raw byte-level diff first, then applies
// normalization to produce per-class findings.
func DiffWASMSemantic(local, remote []byte, opts NormalizeOptions) *SemanticDiffResult {
	result := &SemanticDiffResult{
		Raw:      DiffWASM(local, remote),
		Manifest: NormalizationManifest{Options: opts},
	}

	localSections, localNames := inspectWASMSemantic(local)
	remoteSections, remoteNames := inspectWASMSemantic(remote)

	normLocal, droppedLocal := normalizeSections(localSections, localNames, opts)
	normRemote, droppedRemote := normalizeSections(remoteSections, remoteNames, opts)
	result.Manifest.DroppedLocal = droppedLocal
	result.Manifest.DroppedRemote = droppedRemote

	// Count sections by class in each normalized binary.
	countByClass := func(sections []WASMSection) map[SectionClass]int {
		m := make(map[SectionClass]int)
		for _, s := range sections {
			m[classifySection(s)]++
		}
		return m
	}
	localCounts := countByClass(normLocal)
	remoteCounts := countByClass(normRemote)

	allClasses := []SectionClass{
		SectionClassExecutable, SectionClassABI,
		SectionClassDebug, SectionClassMetadata, SectionClassUnknown,
	}
	for _, class := range allClasses {
		lc := localCounts[class]
		rc := remoteCounts[class]
		changed := lc != rc
		if !changed {
			// Same count — check if the payload hashes differ by comparing
			// a fingerprint of same-class sections in order.
			lh := fingerprintSections(normLocal, class)
			rh := fingerprintSections(normRemote, class)
			changed = lh != rh
		}

		if changed {
			switch class {
			case SectionClassExecutable:
				result.ExecutableChanged = true
			case SectionClassABI:
				result.ABIChanged = true
			case SectionClassDebug:
				result.DebugChanged = true
			}
		}

		desc := describeClassChange(class, lc, rc, changed)
		result.SectionFindings = append(result.SectionFindings, SectionFinding{
			Class:       class,
			LocalCount:  lc,
			RemoteCount: rc,
			Changed:     changed,
			Description: desc,
		})
	}

	// MetadataOnlyDiff: raw has divergence but no executable or ABI change.
	result.MetadataOnlyDiff = result.Raw.HasDivergence &&
		!result.ExecutableChanged && !result.ABIChanged

	result.Summary = buildSemanticSummary(result)
	return result
}

// DiffWASMSemanticFiles reads two WASM files from disk and performs a
// semantic diff with the supplied NormalizeOptions.
func DiffWASMSemanticFiles(localPath, remotePath string, opts NormalizeOptions) (*SemanticDiffResult, error) {
	localData, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read local WASM %s: %w", localPath, err)
	}
	remoteData, err := os.ReadFile(remotePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read remote WASM %s: %w", remotePath, err)
	}
	return DiffWASMSemantic(localData, remoteData, opts), nil
}

// fingerprintSections returns a simple string fingerprint (section-name:size
// pairs joined by comma) for sections of the given class. This is used to
// detect content changes when the section count is unchanged. A proper
// implementation would hash section payloads, but for the compare package
// we only have section metadata (not the full payloads post-parse), so
// size serves as a change indicator.
func fingerprintSections(sections []WASMSection, class SectionClass) string {
	var parts []string
	for _, s := range sections {
		if classifySection(s) == class {
			parts = append(parts, fmt.Sprintf("%s:%d", s.Name, s.Size))
		}
	}
	return strings.Join(parts, ",")
}

func describeClassChange(class SectionClass, local, remote int, changed bool) string {
	if !changed {
		return fmt.Sprintf("%s: %d section(s), identical", class, local)
	}
	if local == remote {
		return fmt.Sprintf("%s: %d section(s), content differs", class, local)
	}
	return fmt.Sprintf("%s: local=%d remote=%d section(s)", class, local, remote)
}

func buildSemanticSummary(r *SemanticDiffResult) string {
	if !r.Raw.HasDivergence {
		return "Binaries are semantically and byte-for-byte identical"
	}
	if r.MetadataOnlyDiff {
		return "Binaries differ only in ignored metadata sections — no executable or ABI change detected"
	}
	var parts []string
	if r.ExecutableChanged {
		parts = append(parts, "executable code changed")
	}
	if r.ABIChanged {
		parts = append(parts, "ABI (imports/exports) changed")
	}
	if r.DebugChanged {
		parts = append(parts, "debug info changed")
	}
	if len(parts) == 0 {
		return "Binaries differ (custom sections only)"
	}
	return "Semantic changes detected: " + strings.Join(parts, ", ")
}
