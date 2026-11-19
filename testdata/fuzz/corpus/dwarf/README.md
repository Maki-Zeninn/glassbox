# DWARF Parser Fuzz Corpus

Seed corpus for the DWARF fuzz targets in `internal/dwarf/dwarf_fuzz_test.go`.

## Targets

- `FuzzNewParser` — binary format detection and DWARF extraction
- `FuzzGetSourceLocation` — PC-to-source-line lookup with arbitrary binaries

## Coverage targets

- WASM magic bytes + version (minimal valid header)
- ELF64 little-endian magic
- Mach-O 64-bit little-endian magic
- PE (MZ) header
- Empty input
- Random garbage bytes
- Truncated headers (magic only)
- WASM with overflowed ULEB128 section lengths (CVE-class: integer overflow in length field)

## Format notes

Seed files are raw binary; `.bin` extension is conventional.
The fuzzer handles format detection via the magic-byte switch in `NewParser`.

## Adding regression inputs

Copy minimised crash inputs here as `regression_<issue>.bin`. Document the
root cause in a comment at the top of the file using the `# comment` syntax
supported by the Go fuzzer corpus format.
