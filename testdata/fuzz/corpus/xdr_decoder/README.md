# XDR Decoder Fuzz Corpus

This directory contains seed inputs for the XDR decoder fuzz targets.

## Corpus Contents

- `valid_envelope.b64` - Valid transaction envelope in base64
- `minimal_xdr.bin` - Minimal valid XDR structure
- `truncated.b64` - Truncated base64 input
- `invalid_base64.txt` - Invalid base64 string
- `large_input.bin` - Large binary input for resource exhaustion testing

## Adding New Seeds

To add new seed inputs:
1. Place the file in this directory
2. Run: `go test -fuzz=FuzzDecodeEnvelope -fuzztime=30s ./internal/simulator/`
3. The fuzzer will automatically incorporate new interesting inputs

## Regression Cases

Add any discovered crash inputs here to prevent regressions.
