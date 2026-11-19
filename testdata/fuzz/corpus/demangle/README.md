# Demangle Fuzz Corpus

This directory contains seed inputs for demangling fuzz testing.

## Corpus Contents

### Valid Rust Symbols
- `legacy_two_part.bin` - Valid legacy format: `_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E`
- `legacy_three_part.bin` - Valid legacy format with three path segments
- `v0_symbol.bin` - Valid v0 format: `_RNvCs1234abcd_11my_contract6invoke`
- `already_readable.bin` - Already readable symbol: `my_contract::invoke`
- `simple.bin` - Simple readable symbol: `transfer`

### Malformed Symbols
- `missing_terminator.bin` - Legacy symbol missing 'E' terminator
- `too_short.bin` - Symbol too short for parsing
- `invalid_length.bin` - Invalid length prefix
- `oversized.bin` - Symbol exceeding MaxInputLength
- `special_chars.bin` - Symbol with special/dangerous characters
- `control_chars.bin` - Symbol with control characters

### Edge Cases
- `empty.bin` - Empty string
- `null_bytes.bin` - Symbol with null bytes
- `numeric_prefix.bin` - Symbol starting with numbers
- `hash_suffix.bin` - Symbol with various hash suffixes
- `oversized.bin` - Symbol exceeding MaxInputLength (4196+ bytes)

## Adding New Corpus Files

When adding new seed inputs:
1. Use descriptive filenames that indicate the input type
2. Keep files small (ideally < 1KB)
3. Document the purpose in this README
4. Run the fuzzer to incorporate the new inputs

## Regression Tests

Files named `regression_<issue>.bin` are regression tests for specific bugs:
- These should be added when a crash is found and fixed
- Document the issue number and brief description in this README
