# Trace Decoder Fuzz Corpus

This directory contains seed inputs for the trace decoder fuzz targets.

## Corpus Contents

- `valid_event.json` - Valid contract event JSON
- `empty_object.json` - Empty JSON object
- `malformed_json.txt` - Malformed JSON structure
- `invalid_base64_event.json` - Event with invalid base64 data
- `large_event.json` - Large event structure for stress testing

## Adding New Seeds

To add new seed inputs:
1. Place the file in this directory
2. Run: `go test -fuzz=FuzzParseContractEvent -fuzztime=30s ./internal/trace/`
3. The fuzzer will automatically incorporate new interesting inputs

## Regression Cases

Add any discovered crash inputs here to prevent regressions.
