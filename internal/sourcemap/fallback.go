// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package sourcemap provides source-mapping for Soroban contract WASM binaries.
//
// FallbackMapper implements a multi-stage pipeline that produces useful source
// references even when a binary is compiled without full DWARF debug symbols:
//
//  1. Full DWARF — use the existing dwarf.Parser line-number tables.
//  2. Partial DWARF — extract file names from .debug_line even when
//     .debug_info is stripped or incomplete.
//  3. Symbol-name heuristics — infer source paths from Rust mangled symbol
//     names embedded in the WASM name section or .debug_str.
//  4. Cargo manifest discovery — walk the repository tree to find Cargo.toml
//     files and resolve the package name to a likely source root.
//  5. Working-directory inference — resolve relative paths against the
//     repository root or current working directory.
package sourcemap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/dwarf"
	"github.com/dotandev/glassbox/internal/logger"
)

// MappingQuality describes how the source location was resolved.
type MappingQuality int

const (
	// MappingQualityFull — resolved from complete DWARF debug info.
	MappingQualityFull MappingQuality = iota
	// MappingQualityPartial — resolved from partial DWARF (line tables only).
	MappingQualityPartial
	// MappingQualityHeuristic — inferred from symbol names or Cargo metadata.
	MappingQualityHeuristic
	// MappingQualityUnknown — could not resolve a source location.
	MappingQualityUnknown
)

func (q MappingQuality) String() string {
	switch q {
	case MappingQualityFull:
		return "full"
	case MappingQualityPartial:
		return "partial"
	case MappingQualityHeuristic:
		return "heuristic"
	default:
		return "unknown"
	}
}

// FallbackResult is the output of the fallback mapping pipeline.
type FallbackResult struct {
	// File is the resolved source file path (may be relative).
	File string `json:"file"`
	// Line is the 1-based source line number (0 when unknown).
	Line int `json:"line"`
	// Column is the 1-based column (0 when unknown).
	Column int `json:"column"`
	// Function is the demangled function name (empty when unknown).
	Function string `json:"function,omitempty"`
	// Quality describes how the location was resolved.
	Quality MappingQuality `json:"quality"`
	// Warning is a human-readable message emitted when fallback was used.
	// Empty when Quality == MappingQualityFull.
	Warning string `json:"warning,omitempty"`
	// Confidence is a stable 0–100 score derived from MatchKind and location detail.
	Confidence int `json:"confidence"`
	// MatchKind classifies the resolution mechanism (exact_offset, function, …).
	MatchKind MatchKind `json:"match_kind"`
	// Evidence retains scoring inputs for debugging and JSON consumers.
	Evidence MappingEvidence `json:"evidence"`
	// LinkPresentation indicates whether SourceLink or CandidateLink was populated.
	LinkPresentation LinkPresentation `json:"link_presentation"`
	// SourceLink is set when LinkPresentation is auto and a URL could be resolved.
	SourceLink string `json:"source_link,omitempty"`
	// CandidateLink is set when confidence is below auto-link threshold.
	CandidateLink string `json:"candidate_link,omitempty"`
	// LinkProvenance is rendered beside source links so reports identify the replay revision.
	LinkProvenance string `json:"link_provenance,omitempty"`
	// DetailedConfidence provides structured confidence information with reason codes.
	DetailedConfidence *DetailedConfidence `json:"detailed_confidence,omitempty"`
	// Origin classifies where the resolved source file came from.
	// Use OriginPrefix() to get the terminal display prefix ([local], [crates.io], [git]).
	// Populated by MappingResolver.Resolve after source-path classification.
	Origin SourceOrigin `json:"origin,omitempty"`
	// ExternalURL is a web-accessible permalink for the source location.
	// Empty for local workspace files (OriginLocal) and unknown origins.
	// For registry crates it is a docs.rs URL; for git dependencies it
	// points to the specific commit in the remote repository.
	// When non-empty the SARIF writer uses this as the artifactLocation.uri.
	ExternalURL string `json:"external_url,omitempty"`
}

// FallbackMapper resolves source locations for WASM binaries that may lack
// full DWARF debug information.
type FallbackMapper struct {
	// ProjectRoot is the repository root used for path resolution.
	// If empty, the current working directory is used.
	ProjectRoot string
}

// NewFallbackMapper creates a FallbackMapper with the given project root.
// Pass an empty string to use the current working directory.
func NewFallbackMapper(projectRoot string) *FallbackMapper {
	if projectRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectRoot = cwd
		}
	}
	return &FallbackMapper{ProjectRoot: projectRoot}
}

// Resolve attempts to map a WASM instruction address to a source location
// using a multi-stage fallback pipeline. It always returns a non-nil result;
// Quality indicates how reliable the mapping is.
func (m *FallbackMapper) Resolve(wasmData []byte, addr uint64) *FallbackResult {
	// Guard against nil or too-small WASM data — the binary helpers would
	// silently produce no results anyway, but surfacing a clear unknown result
	// with a remediation warning is more useful than a silent no-op.
	if len(wasmData) < 8 {
		result := &FallbackResult{
			Quality: MappingQualityUnknown,
			Warning: fmt.Sprintf(
				"[sourcemap] WASM data is nil or too small (%d bytes) to contain valid content — "+
					"source location for address 0x%x cannot be resolved. "+
					"Recompile with 'cargo build --release --target wasm32-unknown-unknown' "+
					"and ensure the binary is fully uploaded.",
				len(wasmData), addr,
			),
		}
		applyConfidence(result, addr, MatchUnknown, "input_guard", "")
		return result
	}

	// ── Stage 1: full DWARF ──────────────────────────────────────────────────
	if result := m.tryFullDWARF(wasmData, addr); result != nil {
		return result
	}

	// ── Stage 2: partial DWARF (line tables only) ────────────────────────────
	if result := m.tryPartialDWARF(wasmData, addr); result != nil {
		return result
	}

	// ── Stage 3: symbol-name heuristics ─────────────────────────────────────
	if result := m.trySymbolHeuristics(wasmData, addr); result != nil {
		return result
	}

	// ── Stage 4: Cargo manifest discovery ───────────────────────────────────
	if result := m.tryCargoDiscovery(wasmData, addr); result != nil {
		return result
	}

	// ── Stage 5: nothing found ───────────────────────────────────────────────
	result := &FallbackResult{
		Quality: MappingQualityUnknown,
		Warning: capabilityWarning(wasmData, addr),
	}
	applyConfidence(result, addr, MatchUnknown, "none", "")
	return result
}

// capabilityWarning builds a precise, actionable message explaining why no
// source location could be resolved. It runs DWARF capability detection (which
// never parses the debug tree, so it cannot fail on malformed data) to
// distinguish an unsupported DWARF version, missing debug sections, and partial
// debug info — each with its own compiler guidance — instead of emitting one
// generic "no debug info" message.
func capabilityWarning(wasmData []byte, addr uint64) string {
	caps := dwarf.DetectCapabilities(wasmData)

	if len(caps.Warnings) > 0 {
		hints := make([]string, 0, len(caps.Warnings))
		for _, w := range caps.Warnings {
			hints = append(hints, w.String())
		}
		return fmt.Sprintf(
			"[sourcemap] could not resolve source location for address 0x%x: %s",
			addr, strings.Join(hints, "; "),
		)
	}

	// Capabilities look sufficient but the address still did not resolve — the
	// symbol for this specific address is absent, which is distinct from a
	// parser limitation. Say so rather than blaming missing debug info.
	return fmt.Sprintf(
		"[sourcemap] no source location for address 0x%x: the binary has usable "+
			"DWARF debug info (%s) but no symbol covers this address — it may be in "+
			"stripped standard-library or runtime code.",
		addr, caps.Summary(),
	)
}

// tryFullDWARF attempts resolution using the standard DWARF line-number tables.
func (m *FallbackMapper) tryFullDWARF(wasmData []byte, addr uint64) *FallbackResult {
	parser, err := dwarf.NewParser(wasmData)
	if err != nil || !parser.HasDebugInfo() {
		return nil
	}

	loc, err := parser.GetSourceLocation(addr)
	if err == nil && loc != nil {
		fn := ""
		if sp, err := parser.FindSubprogramAt(addr); err == nil && sp != nil {
			fn = sp.DemangledName
			if fn == "" {
				fn = sp.Name
			}
		}
		result := &FallbackResult{
			File:     m.resolveFilePath(loc.File),
			Line:     loc.Line,
			Column:   loc.Column,
			Function: fn,
			Quality:  MappingQualityFull,
		}
		applyConfidence(result, addr, MatchExactOffset, "full_dwarf", "")
		return result
	}

	if sp, err := parser.FindSubprogramAt(addr); err == nil && sp != nil {
		fn := sp.DemangledName
		if fn == "" {
			fn = sp.Name
		}
		warning := fmt.Sprintf(
			"[sourcemap] function-level DWARF match for address 0x%x in %q; "+
				"exact line mapping unavailable. Recompile with debug = true for precise lines.",
			addr, fn,
		)
		logger.Logger.Warn("Function-level DWARF fallback used",
			"function", fn, "addr", fmt.Sprintf("0x%x", addr))
		result := &FallbackResult{
			File:     m.resolveFilePath(sp.File),
			Line:     sp.Line,
			Function: fn,
			Quality:  MappingQualityPartial,
			Warning:  warning,
		}
		applyConfidence(result, addr, MatchFunction, "full_dwarf", "")
		return result
	}

	return nil
}

// tryPartialDWARF extracts file names from DWARF line-number tables even when
// .debug_info is stripped. It returns the first file referenced in the line
// table as a best-effort location.
func (m *FallbackMapper) tryPartialDWARF(wasmData []byte, addr uint64) *FallbackResult {
	files := extractDWARFLineFiles(wasmData)
	if len(files) == 0 {
		return nil
	}

	// Pick the most likely file: prefer .rs files that are not from the
	// standard library (no /rustc/ or /.cargo/registry/ in the path).
	best := ""
	for _, f := range files {
		if !strings.HasSuffix(f, ".rs") {
			continue
		}
		if strings.Contains(f, "/rustc/") || strings.Contains(f, ".cargo/registry") {
			continue
		}
		best = f
		break
	}
	if best == "" && len(files) > 0 {
		best = files[0]
	}
	if best == "" {
		return nil
	}

	warning := fmt.Sprintf(
		"[sourcemap] partial DWARF: .debug_info is stripped; "+
			"source file inferred from line-number table as %q. "+
			"Line numbers may be inaccurate. "+
			"Recompile with debug = true for full mappings.",
		best,
	)
	logger.Logger.Warn("Partial DWARF fallback used", "file", best, "addr", fmt.Sprintf("0x%x", addr))

	result := &FallbackResult{
		File:    m.resolveFilePath(best),
		Quality: MappingQualityPartial,
		Warning: warning,
	}
	applyConfidence(result, addr, MatchLineTable, "partial_dwarf", "")
	return result
}

// trySymbolHeuristics infers a source path from Rust mangled symbol names
// embedded in the WASM name section or .debug_str section.
func (m *FallbackMapper) trySymbolHeuristics(wasmData []byte, addr uint64) *FallbackResult {
	pkgName := extractPackageNameFromSymbols(wasmData)
	if pkgName == "" {
		return nil
	}

	// Rust package names use hyphens; source directories use underscores.
	dirName := strings.ReplaceAll(pkgName, "-", "_")

	// Look for a src/lib.rs or src/main.rs under a directory matching the
	// package name within the project root.
	candidates := []string{
		filepath.Join(m.ProjectRoot, dirName, "src", "lib.rs"),
		filepath.Join(m.ProjectRoot, dirName, "src", "main.rs"),
		filepath.Join(m.ProjectRoot, "src", "lib.rs"),
		filepath.Join(m.ProjectRoot, "src", "main.rs"),
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			warning := fmt.Sprintf(
				"[sourcemap] heuristic fallback: source file inferred from "+
					"symbol name %q as %q. "+
					"Exact line numbers are unavailable. "+
					"Recompile with debug = true for accurate mappings.",
				pkgName, c,
			)
			logger.Logger.Warn("Symbol heuristic fallback used",
				"package", pkgName, "file", c, "addr", fmt.Sprintf("0x%x", addr))
			result := &FallbackResult{
				File:    c,
				Quality: MappingQualityHeuristic,
				Warning: warning,
			}
			applyConfidence(result, addr, MatchHeuristic, "symbol_heuristic", pkgName)
			return result
		}
	}

	// File not found on disk — still return the inferred path so callers can
	// surface it to the user.
	inferred := filepath.Join(m.ProjectRoot, dirName, "src", "lib.rs")
	warning := fmt.Sprintf(
		"[sourcemap] heuristic fallback: source path %q inferred from "+
			"symbol name %q but file was not found on disk. "+
			"Ensure the contract source is checked out at %q.",
		inferred, pkgName, m.ProjectRoot,
	)
	logger.Logger.Warn("Symbol heuristic fallback (file not found)",
		"package", pkgName, "inferred", inferred)
	result := &FallbackResult{
		File:    inferred,
		Quality: MappingQualityHeuristic,
		Warning: warning,
	}
	applyConfidence(result, addr, MatchHeuristic, "symbol_heuristic", pkgName)
	return result
}

// tryCargoDiscovery walks the project root for Cargo.toml files and returns
// a heuristic result pointing at the first package's src/lib.rs.
func (m *FallbackMapper) tryCargoDiscovery(wasmData []byte, addr uint64) *FallbackResult {
	manifests := findCargoManifests(m.ProjectRoot)
	if len(manifests) == 0 {
		return nil
	}

	for _, manifest := range manifests {
		pkg := parseCargoPackageName(manifest)
		if pkg == "" {
			continue
		}
		dir := filepath.Dir(manifest)
		srcFile := filepath.Join(dir, "src", "lib.rs")
		if _, err := os.Stat(srcFile); err != nil {
			srcFile = filepath.Join(dir, "src", "main.rs")
			if _, err := os.Stat(srcFile); err != nil {
				srcFile = filepath.Join(dir, "src", "lib.rs") // use even if absent
			}
		}

		warning := fmt.Sprintf(
			"[sourcemap] Cargo manifest fallback: source root inferred from "+
				"Cargo.toml at %q (package %q). "+
				"Line numbers are unavailable. "+
				"Recompile with debug = true for accurate mappings.",
			manifest, pkg,
		)
		logger.Logger.Warn("Cargo manifest fallback used",
			"manifest", manifest, "package", pkg, "src", srcFile)
		result := &FallbackResult{
			File:    srcFile,
			Quality: MappingQualityHeuristic,
			Warning: warning,
		}
		applyConfidence(result, addr, MatchHeuristic, "cargo_manifest", pkg)
		return result
	}
	return nil
}

// resolveFilePath resolves a potentially relative source path against the
// project root. Absolute paths are returned unchanged.
func (m *FallbackMapper) resolveFilePath(path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	resolved := filepath.Join(m.ProjectRoot, path)
	if _, err := os.Stat(resolved); err == nil {
		return resolved
	}
	// Return the original relative path if the resolved one doesn't exist.
	return path
}

// ─────────────────────────────────────────────────────────────────────────────
// WASM binary helpers
// ─────────────────────────────────────────────────────────────────────────────

// extractDWARFLineFiles parses the WASM binary's custom sections and extracts
// all file names referenced in the .debug_line section's file-name table.
// This works even when .debug_info is stripped.
func extractDWARFLineFiles(data []byte) []string {
	sections := parseWASMCustomSections(data)
	lineSec, ok := sections[".debug_line"]
	if !ok || len(lineSec) == 0 {
		return nil
	}
	return parseDWARFLineFileNames(lineSec)
}

// parseWASMCustomSections returns a map of custom-section name → content for
// a WASM binary. Mirrors the logic in dwarf.parseWASMSections but is
// package-local to avoid a circular import.
func parseWASMCustomSections(data []byte) map[string][]byte {
	sections := make(map[string][]byte)
	if len(data) < 8 {
		return sections
	}
	// Verify WASM magic + version.
	if data[0] != 0x00 || data[1] != 0x61 || data[2] != 0x73 || data[3] != 0x6d {
		return sections
	}

	pos := 8
	for pos < len(data) {
		if pos >= len(data) {
			break
		}
		sectionID := data[pos]
		pos++

		sectionSize, n := readULEB128Local(data, pos)
		if n == 0 {
			break
		}
		pos += n
		sectionEnd := pos + int(sectionSize)
		if sectionEnd > len(data) {
			break
		}

		if sectionID == 0 { // custom section
			nameLen, m := readULEB128Local(data, pos)
			if m == 0 || pos+m+int(nameLen) > sectionEnd {
				pos = sectionEnd
				continue
			}
			nameStart := pos + m
			name := string(data[nameStart : nameStart+int(nameLen)])
			content := data[nameStart+int(nameLen) : sectionEnd]
			sections[name] = content
		}
		pos = sectionEnd
	}
	return sections
}

// readULEB128Local decodes an unsigned LEB128 integer. Duplicated here to
// avoid importing the dwarf package (which would create a cycle).
func readULEB128Local(data []byte, pos int) (uint64, int) {
	var result uint64
	var shift uint
	for i := pos; i < len(data); i++ {
		b := data[i]
		result |= uint64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			return result, i - pos + 1
		}
		if shift >= 64 {
			return 0, 0
		}
	}
	return 0, 0
}

// parseDWARFLineFileNames extracts file names from a raw .debug_line section.
// It implements a minimal DWARF v4/v5 line-program header parser sufficient
// to read the file-name table without executing the full line program.
func parseDWARFLineFileNames(data []byte) []string {
	var files []string
	pos := 0

	for pos < len(data) {
		if pos+4 > len(data) {
			break
		}
		// unit_length (4 bytes, 32-bit DWARF)
		unitLen := int(data[pos]) | int(data[pos+1])<<8 | int(data[pos+2])<<16 | int(data[pos+3])<<24
		pos += 4
		if unitLen == 0 || pos+unitLen > len(data) {
			break
		}
		unitEnd := pos + unitLen

		if pos+2 > unitEnd {
			pos = unitEnd
			continue
		}
		version := int(data[pos]) | int(data[pos+1])<<8
		pos += 2

		if version < 2 || version > 5 {
			pos = unitEnd
			continue
		}

		if version >= 5 {
			// DWARF v5 has address_size and segment_selector_size before header_length.
			pos += 2 // skip address_size + segment_selector_size
		}

		if pos+4 > unitEnd {
			pos = unitEnd
			continue
		}
		headerLen := int(data[pos]) | int(data[pos+1])<<8 | int(data[pos+2])<<16 | int(data[pos+3])<<24
		pos += 4
		headerEnd := pos + headerLen
		if headerEnd > unitEnd {
			pos = unitEnd
			continue
		}

		// Skip: minimum_instruction_length, maximum_ops_per_instruction (v4+),
		// default_is_stmt, line_base, line_range, opcode_base.
		skip := 4
		if version >= 4 {
			skip = 5
		}
		pos += skip
		if pos >= headerEnd {
			pos = unitEnd
			continue
		}

		// opcode_base tells us how many standard-opcode lengths to skip.
		opcodeBase := int(data[pos])
		pos++
		pos += opcodeBase - 1 // skip standard_opcode_lengths array

		if version >= 5 {
			// DWARF v5 uses directory/file entry format tables — skip them.
			pos = headerEnd
		} else {
			// DWARF v2/v3/v4: null-terminated directory table, then file table.
			// Skip include_directories.
			for pos < headerEnd {
				end := indexOfNull(data, pos, headerEnd)
				if end == pos { // empty string = end of directory table
					pos++
					break
				}
				pos = end + 1
			}
			// Read file_names table.
			for pos < headerEnd {
				end := indexOfNull(data, pos, headerEnd)
				if end == pos { // empty string = end of file table
					pos++
					break
				}
				name := string(data[pos:end])
				pos = end + 1
				// Skip dir_index, last_modified, file_size (all LEB128).
				for i := 0; i < 3; i++ {
					_, n := readULEB128Local(data, pos)
					if n == 0 {
						break
					}
					pos += n
				}
				if name != "" {
					files = append(files, name)
				}
			}
		}

		pos = unitEnd
	}
	return files
}

// indexOfNull returns the index of the first 0x00 byte in data[start:end],
// or end if none is found.
func indexOfNull(data []byte, start, end int) int {
	for i := start; i < end && i < len(data); i++ {
		if data[i] == 0 {
			return i
		}
	}
	return end
}

// extractPackageNameFromSymbols scans the WASM name section and .debug_str
// for Rust mangled symbol names and extracts the crate/package name.
func extractPackageNameFromSymbols(data []byte) string {
	sections := parseWASMCustomSections(data)

	// Try the "name" custom section first (always present in non-stripped WASM).
	if nameSec, ok := sections["name"]; ok {
		if pkg := extractCrateFromNameSection(nameSec); pkg != "" {
			return pkg
		}
	}

	// Fall back to .debug_str.
	if debugStr, ok := sections[".debug_str"]; ok {
		if pkg := extractCrateFromDebugStr(debugStr); pkg != "" {
			return pkg
		}
	}

	return ""
}

// extractCrateFromNameSection parses the WASM name section (subsection 1 =
// function names) and extracts the crate name from the first Rust mangled
// symbol it finds.
func extractCrateFromNameSection(data []byte) string {
	pos := 0
	for pos < len(data) {
		if pos >= len(data) {
			break
		}
		subsectionID := data[pos]
		pos++
		size, n := readULEB128Local(data, pos)
		if n == 0 {
			break
		}
		pos += n
		end := pos + int(size)
		if end > len(data) {
			break
		}

		if subsectionID == 1 { // function names
			count, n := readULEB128Local(data, pos)
			p := pos + n
			for i := uint64(0); i < count && p < end; i++ {
				_, n2 := readULEB128Local(data, p) // index
				p += n2
				nameLen, n3 := readULEB128Local(data, p)
				p += n3
				if p+int(nameLen) > end {
					break
				}
				name := string(data[p : p+int(nameLen)])
				p += int(nameLen)
				if pkg := crateFromMangledName(name); pkg != "" {
					return pkg
				}
			}
		}
		pos = end
	}
	return ""
}

// extractCrateFromDebugStr scans the .debug_str section for null-terminated
// strings that look like Rust crate names.
func extractCrateFromDebugStr(data []byte) string {
	pos := 0
	for pos < len(data) {
		end := indexOfNull(data, pos, len(data))
		s := string(data[pos:end])
		if pkg := crateFromMangledName(s); pkg != "" {
			return pkg
		}
		pos = end + 1
	}
	return ""
}

// crateFromMangledName extracts the crate name from a Rust v0 or legacy
// mangled symbol name.
//
// Rust v0 mangling: _RNvC<len><crate>...
// Legacy mangling:  _ZN<crate>...
func crateFromMangledName(name string) string {
	// Rust v0: _RNvC<len><crate>
	if strings.HasPrefix(name, "_RNvC") {
		rest := name[5:]
		length, n := readDecimalLength(rest)
		if n > 0 && length <= len(rest)-n {
			crate := rest[n : n+length]
			if isValidCrateName(crate) {
				return crate
			}
		}
	}

	// Rust v0 nested: _RNv... contains C<len><crate>
	if strings.HasPrefix(name, "_R") {
		idx := strings.Index(name, "C")
		if idx > 0 {
			rest := name[idx+1:]
			length, n := readDecimalLength(rest)
			if n > 0 && length <= len(rest)-n {
				crate := rest[n : n+length]
				if isValidCrateName(crate) {
					return crate
				}
			}
		}
	}

	// Legacy Itanium: _ZN<len><crate>
	if strings.HasPrefix(name, "_ZN") {
		rest := name[3:]
		length, n := readDecimalLength(rest)
		if n > 0 && length <= len(rest)-n {
			crate := rest[n : n+length]
			if isValidCrateName(crate) {
				return crate
			}
		}
	}

	return ""
}

// readDecimalLength reads a decimal integer prefix from s and returns the
// value and number of digits consumed.
func readDecimalLength(s string) (int, int) {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n == 0 {
		return 0, 0
	}
	val := 0
	for i := 0; i < n; i++ {
		val = val*10 + int(s[i]-'0')
	}
	return val, n
}

// isValidCrateName returns true if s looks like a plausible Rust crate name.
func isValidCrateName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	// Reject common stdlib crate names.
	switch s {
	case "std", "core", "alloc", "proc_macro", "test":
		return false
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// Cargo manifest helpers
// ─────────────────────────────────────────────────────────────────────────────

// findCargoManifests walks dir up to 3 levels deep and returns all Cargo.toml
// paths found, sorted so workspace roots come first.
func findCargoManifests(dir string) []string {
	if dir == "" {
		return nil
	}
	var found []string
	_ = walkDepth(dir, 3, func(path string, info os.FileInfo) {
		if !info.IsDir() && info.Name() == "Cargo.toml" {
			found = append(found, path)
		}
	})
	return found
}

// walkDepth walks a directory tree up to maxDepth levels, calling fn for each
// entry. Errors are silently ignored.
func walkDepth(root string, maxDepth int, fn func(string, os.FileInfo)) error {
	return walkDir(root, 0, maxDepth, fn)
}

func walkDir(dir string, depth, maxDepth int, fn func(string, os.FileInfo)) error {
	if depth > maxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		fn(path, info)
		if e.IsDir() && e.Name() != "target" && e.Name() != ".git" {
			_ = walkDir(path, depth+1, maxDepth, fn)
		}
	}
	return nil
}

// parseCargoPackageName extracts the [package] name field from a Cargo.toml
// file using a simple line-by-line parser (no TOML library dependency).
func parseCargoPackageName(manifestPath string) string {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ""
	}
	inPackage := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[package]" {
			inPackage = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inPackage = false
			continue
		}
		if inPackage && strings.HasPrefix(trimmed, "name") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				name := strings.TrimSpace(parts[1])
				name = strings.Trim(name, `"'`)
				if name != "" {
					return name
				}
			}
		}
	}
	return ""
}
