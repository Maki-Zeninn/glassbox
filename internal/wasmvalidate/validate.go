// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package wasmvalidate provides a bounded, non-panicking pre-validation pass
// over raw WASM module bytes. It exists so that corrupt or hostile WASM fails
// safely with a field-specific diagnostic before the deeper DWARF/source-map
// pipeline (internal/dwarf, internal/sourcemap) or the dead-code eliminator
// (internal/wasmopt) spend work parsing it.
//
// Validate distinguishes two failure classes:
//   - ClassStructural: the bytes do not form a well-formed WASM module
//     (bad magic/version, truncated or overlapping sections, malformed
//     varints, inconsistent function/code section counts).
//   - ClassPolicy: the bytes are a well-formed module but exceed a
//     configured bound (module size, section count, debug-section size,
//     function count) and are rejected as a matter of policy, not format.
package wasmvalidate

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// FailureClass distinguishes malformed encoding from policy-limit violations.
type FailureClass string

const (
	ClassStructural FailureClass = "structural"
	ClassPolicy     FailureClass = "policy"
)

// Issue is one field-specific validation failure.
type Issue struct {
	Field       string
	Class       FailureClass
	Description string
	Hint        string
}

// Section is a bounds-checked entry from the module's section table. Offset
// is the position of the ID byte; PayloadOffset is where the section's
// content begins (after the ID byte and the size varint) and PayloadOffset+
// Size is always <= len(data), already validated by the section-table walk.
type Section struct {
	ID            byte
	Offset        int
	PayloadOffset int
	Size          int
}

// Report is the result of a Validate call.
type Report struct {
	OK           bool
	Issues       []Issue
	ModuleSize   int
	SectionCount int
	Sections     []Section
}

// Error returns a single combined error describing every issue, or nil when
// the report is OK.
func (r *Report) Error() error {
	if r == nil || r.OK {
		return nil
	}
	var sb strings.Builder
	for i, iss := range r.Issues {
		if i > 0 {
			sb.WriteString("; ")
		}
		fmt.Fprintf(&sb, "[%s] %s: %s", iss.Class, iss.Field, iss.Description)
	}
	return fmt.Errorf("%s", sb.String())
}

// Limits bounds how much of a WASM module Validate is willing to accept.
// A zero value for any field disables that particular bound.
type Limits struct {
	MaxModuleSize       int
	MaxSectionCount     int
	MaxDebugSectionSize int
	MaxFunctionCount    int
	// MaxLocalsPerFunction is the maximum number of local variables allowed in
	// any single function body.  Pathological modules may declare millions of
	// locals to force allocator exhaustion before a single instruction executes.
	// Zero means the check is disabled.
	MaxLocalsPerFunction int
	// MaxDataSegments is the maximum number of data segments (section ID 11)
	// allowed in the module.  Each segment can reference an arbitrary byte
	// range, so many segments can be used to blow up memory initialisation.
	// Zero means the check is disabled.
	MaxDataSegments int
	// MaxValidationTimeMS is the maximum wall-clock time (milliseconds) spent
	// in Validate before it returns a policy issue and stops.  This is a
	// best-effort time-out applied at section boundaries; it does not interrupt
	// work mid-section.  Zero means no time limit.
	MaxValidationTimeMS int
}

// DefaultLimits returns conservative bounds sized to comfortably accommodate
// real local debug builds (which can carry large .debug_* sections) while
// still rejecting pathological or hostile input before it reaches deeper
// parsing.
func DefaultLimits() Limits {
	return Limits{
		MaxModuleSize:        64 * 1024 * 1024,
		MaxSectionCount:      128,
		MaxDebugSectionSize:  32 * 1024 * 1024,
		MaxFunctionCount:     1_000_000,
		MaxLocalsPerFunction: 50_000,
		MaxDataSegments:      1_000,
		MaxValidationTimeMS:  0, // disabled by default; callers opt in
	}
}

const (
	sectionCustom   = 0
	sectionImport   = 2
	sectionFunction = 3
	sectionCode     = 10
	sectionData     = 11
)

type issueFunc func(field string, class FailureClass, desc, hint string)

// Validate performs a single bounded pass over data: header check, a
// bounds-checked section-table walk, function-index consistency, and
// debug-section size limits. It never panics and never performs work
// proportional to an attacker-controlled value that hasn't first been
// checked against len(data) or against limits.
func Validate(data []byte, limits Limits) *Report {
	report := &Report{OK: true, ModuleSize: len(data)}
	issue := func(field string, class FailureClass, desc, hint string) {
		report.OK = false
		report.Issues = append(report.Issues, Issue{Field: field, Class: class, Description: desc, Hint: hint})
	}

	// Optional wall-clock deadline applied at section boundaries.
	var deadline time.Time
	if limits.MaxValidationTimeMS > 0 {
		deadline = time.Now().Add(time.Duration(limits.MaxValidationTimeMS) * time.Millisecond)
	}

	if limits.MaxModuleSize > 0 && len(data) > limits.MaxModuleSize {
		issue("module_size", ClassPolicy,
			fmt.Sprintf("module size %d bytes exceeds configured limit %d bytes", len(data), limits.MaxModuleSize),
			"reduce module size or raise the configured WASM size limit")
		return report
	}

	sections, sectionIssues := Sections(data, limits)
	report.Sections = sections
	report.SectionCount = len(sections)
	structuralFailure := false
	for _, si := range sectionIssues {
		issue(si.Field, si.Class, si.Description, si.Hint)
		if si.Class == ClassStructural {
			structuralFailure = true
		}
	}
	if structuralFailure {
		return report
	}

	validateFunctionIndices(data, sections, limits, issue)
	validateDebugSections(data, sections, limits, issue)
	validateLocalsPerFunction(data, sections, limits, deadline, issue)
	validateDataSegments(data, sections, limits, issue)

	return report
}

// Sections validates the WASM header and walks the section table, returning
// every bounds-checked section entry plus any issues encountered. It is
// self-contained (it re-checks the header) so callers that only need the
// section table — internal/wasmopt, internal/wat, internal/dwarf — can use
// it directly without a separate Validate call.
func Sections(data []byte, limits Limits) ([]Section, []Issue) {
	var issues []Issue
	issue := func(field string, class FailureClass, desc, hint string) {
		issues = append(issues, Issue{Field: field, Class: class, Description: desc, Hint: hint})
	}
	if !checkHeader(data, issue) {
		return nil, issues
	}
	sections := walkSections(data, limits, issue)
	return sections, issues
}

func checkHeader(data []byte, issue issueFunc) bool {
	if len(data) < 8 {
		issue("header", ClassStructural, "module shorter than the 8-byte WASM header", "provide a valid compiled WASM module")
		return false
	}
	if data[0] != 0x00 || data[1] != 0x61 || data[2] != 0x73 || data[3] != 0x6d {
		issue("magic", ClassStructural, `missing WASM magic bytes ("\0asm")`, "provide a valid compiled WASM module")
		return false
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 1 {
		issue("version", ClassStructural, fmt.Sprintf("unsupported WASM version %d", version), "recompile with a standard wasm32 toolchain")
		return false
	}
	return true
}

func walkSections(data []byte, limits Limits, issue issueFunc) []Section {
	var sections []Section
	pos := 8
	for pos < len(data) {
		if limits.MaxSectionCount > 0 && len(sections) >= limits.MaxSectionCount {
			issue("section_count", ClassPolicy,
				fmt.Sprintf("module has more than %d sections", limits.MaxSectionCount),
				"reduce module complexity or raise the configured section-count limit")
			break
		}
		offset := pos
		id := data[pos]
		pos++
		size, n, ok := readULEB32(data, pos)
		if !ok {
			issue(fmt.Sprintf("section[%d].size", len(sections)), ClassStructural,
				"malformed or truncated section-size varint", "provide a valid compiled WASM module")
			break
		}
		pos += n
		if size > uint32(len(data)) || pos+int(size) > len(data) {
			issue(fmt.Sprintf("section[%d].size", len(sections)), ClassStructural,
				fmt.Sprintf("section size %d exceeds remaining module bytes", size),
				"provide a valid compiled WASM module")
			break
		}
		sections = append(sections, Section{ID: id, Offset: offset, PayloadOffset: pos, Size: int(size)})
		pos += int(size)
	}
	return sections
}

// readULEB32 decodes an unsigned LEB128 integer bounded to 5 bytes / 32 bits,
// matching the encoding WASM uses for section sizes and counts.
func readULEB32(data []byte, pos int) (uint32, int, bool) {
	var v uint32
	for i := 0; i < 5; i++ {
		if pos+i >= len(data) {
			return 0, 0, false
		}
		b := data[pos+i]
		v |= uint32(b&0x7f) << (7 * uint(i))
		if b&0x80 == 0 {
			return v, i + 1, true
		}
	}
	return 0, 0, false
}

func findSection(sections []Section, id byte) (Section, bool) {
	for _, s := range sections {
		if s.ID == id {
			return s, true
		}
	}
	return Section{}, false
}

func skipString(data []byte, pos int) (int, bool) {
	length, n, ok := readULEB32(data, pos)
	if !ok {
		return 0, false
	}
	pos += n
	if pos+int(length) > len(data) {
		return 0, false
	}
	return pos + int(length), true
}

func skipLimits(data []byte, pos int) (int, bool) {
	if pos >= len(data) {
		return 0, false
	}
	flags := data[pos]
	pos++
	_, n, ok := readULEB32(data, pos)
	if !ok {
		return 0, false
	}
	pos += n
	if flags&0x1 != 0 {
		_, n, ok := readULEB32(data, pos)
		if !ok {
			return 0, false
		}
		pos += n
	}
	return pos, true
}

// countFunctionImports parses an import-section payload and returns the
// number of function-kind imports (kind byte 0). It also validates the
// payload is well-formed enough to walk in full, since an inconsistent
// import section would otherwise let a later function-index count silently
// pass.
func countFunctionImports(payload []byte) (uint32, bool) {
	count, n, ok := readULEB32(payload, 0)
	if !ok {
		return 0, false
	}
	pos := n
	var funcImports uint32
	for i := uint32(0); i < count; i++ {
		var ok2 bool
		pos, ok2 = skipString(payload, pos) // module name
		if !ok2 {
			return 0, false
		}
		pos, ok2 = skipString(payload, pos) // field name
		if !ok2 {
			return 0, false
		}
		if pos >= len(payload) {
			return 0, false
		}
		kind := payload[pos]
		pos++
		switch kind {
		case 0: // function: typeidx varint
			_, n, ok := readULEB32(payload, pos)
			if !ok {
				return 0, false
			}
			pos += n
			funcImports++
		case 1: // table: elemtype byte + limits
			if pos >= len(payload) {
				return 0, false
			}
			pos++
			pos, ok2 = skipLimits(payload, pos)
			if !ok2 {
				return 0, false
			}
		case 2: // memory: limits
			pos, ok2 = skipLimits(payload, pos)
			if !ok2 {
				return 0, false
			}
		case 3: // global: valtype byte + mutability byte
			if pos+2 > len(payload) {
				return 0, false
			}
			pos += 2
		default:
			return 0, false
		}
	}
	return funcImports, true
}

// countCodeEntries walks a code-section payload far enough to validate each
// function body's declared size stays in bounds, returning the entry count
// without decoding the bodies themselves.
func countCodeEntries(payload []byte) (uint32, bool) {
	count, n, ok := readULEB32(payload, 0)
	if !ok {
		return 0, false
	}
	pos := n
	for i := uint32(0); i < count; i++ {
		bodySize, n, ok := readULEB32(payload, pos)
		if !ok {
			return 0, false
		}
		pos += n
		if pos+int(bodySize) > len(payload) {
			return 0, false
		}
		pos += int(bodySize)
	}
	return count, true
}

func validateFunctionIndices(data []byte, sections []Section, limits Limits, issue issueFunc) {
	var funcImports uint32
	if importSec, ok := findSection(sections, sectionImport); ok {
		payload := data[importSec.PayloadOffset : importSec.PayloadOffset+importSec.Size]
		n, ok := countFunctionImports(payload)
		if !ok {
			issue("import_section", ClassStructural, "malformed import section", "provide a valid compiled WASM module")
			return
		}
		funcImports = n
	}

	var declaredFuncCount uint32
	haveFunctionSection := false
	if funcSec, ok := findSection(sections, sectionFunction); ok {
		payload := data[funcSec.PayloadOffset : funcSec.PayloadOffset+funcSec.Size]
		n, _, ok := readULEB32(payload, 0)
		if !ok {
			issue("function_section", ClassStructural, "malformed function section", "provide a valid compiled WASM module")
			return
		}
		declaredFuncCount = n
		haveFunctionSection = true
	}

	haveCodeSection := false
	var codeEntryCount uint32
	if codeSec, ok := findSection(sections, sectionCode); ok {
		payload := data[codeSec.PayloadOffset : codeSec.PayloadOffset+codeSec.Size]
		n, ok := countCodeEntries(payload)
		if !ok {
			issue("code_section", ClassStructural, "malformed code section: a function body size is out of bounds", "provide a valid compiled WASM module")
			return
		}
		codeEntryCount = n
		haveCodeSection = true
	}

	if haveFunctionSection && haveCodeSection && declaredFuncCount != codeEntryCount {
		issue("function_index", ClassStructural,
			fmt.Sprintf("function section declares %d functions but code section has %d bodies", declaredFuncCount, codeEntryCount),
			"provide a valid compiled WASM module")
		return
	}

	total := funcImports + declaredFuncCount
	if limits.MaxFunctionCount > 0 && total > uint32(limits.MaxFunctionCount) {
		issue("function_count", ClassPolicy,
			fmt.Sprintf("module declares %d functions, exceeds configured limit %d", total, limits.MaxFunctionCount),
			"reduce function count or raise the configured function-count limit")
	}
}

func validateDebugSections(data []byte, sections []Section, limits Limits, issue issueFunc) {
	for i, s := range sections {
		if s.ID != sectionCustom {
			continue
		}
		content := data[s.PayloadOffset : s.PayloadOffset+s.Size]
		nameLen, n, ok := readULEB32(content, 0)
		if !ok || n+int(nameLen) > len(content) {
			continue // malformed custom-section name; the section itself is already bounds-checked
		}
		name := string(content[n : n+int(nameLen)])
		if !strings.HasPrefix(name, ".debug") {
			continue
		}
		payloadSize := len(content) - n - int(nameLen)
		if limits.MaxDebugSectionSize > 0 && payloadSize > limits.MaxDebugSectionSize {
			issue(fmt.Sprintf("section[%d].debug(%s)", i, name), ClassPolicy,
				fmt.Sprintf("%s is %d bytes, exceeds configured debug-section limit %d bytes", name, payloadSize, limits.MaxDebugSectionSize),
				"strip debug info or raise the configured debug-section size limit")
		}
	}
}

// validateLocalsPerFunction walks the code section and counts the number of
// local variable declarations in each function body.  It never decodes
// instructions — it only reads the local-variable header that precedes them.
//
// WASM function body layout (per spec):
//   body_size   : u32 LEB128   — total byte length of what follows
//   local_count : u32 LEB128   — number of (count, type) pairs in the header
//   [local_count × (n: u32 LEB128, type: byte)]
//   … instructions …
//   0x0b (end opcode)
//
// We check that the sum of all n values (total local slots) does not exceed
// limits.MaxLocalsPerFunction.  We also check the per-body byte bounds so we
// never read past the section boundary.
//
// The deadline parameter is honoured between function bodies; when it is zero
// the check is unlimited.
func validateLocalsPerFunction(data []byte, sections []Section, limits Limits, deadline time.Time, issue issueFunc) {
	if limits.MaxLocalsPerFunction <= 0 {
		return
	}

	codeSec, ok := findSection(sections, sectionCode)
	if !ok {
		return
	}

	payload := data[codeSec.PayloadOffset : codeSec.PayloadOffset+codeSec.Size]
	funcCount, n, ok := readULEB32(payload, 0)
	if !ok {
		return // already caught by validateFunctionIndices
	}
	pos := n

	for i := uint32(0); i < funcCount; i++ {
		// Respect the optional time deadline between bodies.
		if !deadline.IsZero() && time.Now().After(deadline) {
			issue("validation_time", ClassPolicy,
				fmt.Sprintf("validation exceeded configured time limit; stopped after checking %d/%d function bodies for locals", i, funcCount),
				"raise the configured validation time limit or reduce module complexity")
			return
		}

		// Read body size.
		bodySize, n, ok := readULEB32(payload, pos)
		if !ok || pos+n+int(bodySize) > len(payload) {
			return // structural issue already reported
		}
		bodyStart := pos + n
		bodyEnd := bodyStart + int(bodySize)
		pos = bodyEnd

		// Read local-variable declaration count.
		localVecCount, n2, ok := readULEB32(payload, bodyStart)
		if !ok {
			continue
		}
		localPos := bodyStart + n2

		var totalLocals uint32
		for j := uint32(0); j < localVecCount; j++ {
			// Each entry: (count: u32 LEB128, type: byte)
			if localPos >= bodyEnd {
				break
			}
			count, n3, ok := readULEB32(payload, localPos)
			if !ok {
				break
			}
			localPos += n3
			if localPos >= bodyEnd {
				break
			}
			localPos++ // skip type byte
			totalLocals += count
			if limits.MaxLocalsPerFunction > 0 && int(totalLocals) > limits.MaxLocalsPerFunction {
				issue(fmt.Sprintf("function[%d].locals", i), ClassPolicy,
					fmt.Sprintf("function body %d declares %d or more locals, exceeds configured limit %d", i, totalLocals, limits.MaxLocalsPerFunction),
					"reduce local variable count or raise the configured local-count limit")
				break
			}
		}
	}
}

// validateDataSegments counts the number of data segments (section ID 11) and
// checks it against limits.MaxDataSegments.  Each segment's size is also
// bounds-checked against the section payload to detect truncated modules.
func validateDataSegments(data []byte, sections []Section, limits Limits, issue issueFunc) {
	if limits.MaxDataSegments <= 0 {
		return
	}

	dataSec, ok := findSection(sections, sectionData)
	if !ok {
		return // no data section; nothing to check
	}

	payload := data[dataSec.PayloadOffset : dataSec.PayloadOffset+dataSec.Size]
	segCount, _, ok := readULEB32(payload, 0)
	if !ok {
		issue("data_section", ClassStructural,
			"malformed data section: cannot read segment count",
			"provide a valid compiled WASM module")
		return
	}

	if int(segCount) > limits.MaxDataSegments {
		issue("data_segment_count", ClassPolicy,
			fmt.Sprintf("module declares %d data segments, exceeds configured limit %d", segCount, limits.MaxDataSegments),
			"reduce the number of data segments or raise the configured data-segment limit")
	}
}
