# WASM Parser Fuzz Corpus

This directory contains seed inputs for the WASM parser fuzz targets.

## Corpus Contents

- `valid_wasm.bin` - Valid WASM binary with magic bytes
- `magic_only.bin` - WASM magic bytes only (too short)
- `wrong_magic.bin` - Invalid magic bytes
- `empty.bin` - Empty binary
- `large_wasm.bin` - Large WASM binary for stress testing

## Adding New Seeds

To add new seed inputs:
1. Place the file in this directory
2. Run: `go test -fuzz=FuzzValidateWasmMagic -fuzztime=30s ./internal/abi/`
3. The fuzzer will automatically incorporate new interesting inputs

## Regression Cases

Add any discovered crash inputs here to prevent regressions.
