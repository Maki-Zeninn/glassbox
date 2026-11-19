# Decoder Fuzz Corpus

This directory contains seed inputs for the decoder fuzz targets.

## Corpus Contents

- `valid_event.b64` - Valid diagnostic event in base64
- `empty_events.json` - Empty events array
- `invalid_base64.txt` - Invalid base64 string
- `malformed_json.json` - Malformed JSON structure
- `large_events.json` - Large events array for stress testing

## Adding New Seeds

To add new seed inputs:
1. Place the file in this directory
2. Run: `go test -fuzz=FuzzDecodeEvents -fuzztime=30s ./internal/decoder/`
3. The fuzzer will automatically incorporate new interesting inputs

## Regression Cases

Add any discovered crash inputs here to prevent regressions.
