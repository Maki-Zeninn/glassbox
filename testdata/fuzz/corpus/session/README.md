# Session Parser Fuzz Corpus

This directory contains seed inputs for the session parser fuzz targets.

## Corpus Contents

- `valid_session.json` - Valid session data structure
- `empty_session.json` - Empty session object
- `invalid_archive.zip` - Invalid ZIP archive structure
- `malformed_path.txt` - Malformed archive path
- `large_session.json` - Large session data for stress testing

## Adding New Seeds

To add new seed inputs:
1. Place the file in this directory
2. Run: `go test -fuzz=FuzzValidateIntegrity -fuzztime=30s ./internal/session/`
3. The fuzzer will automatically incorporate new interesting inputs

## Regression Cases

Add any discovered crash inputs here to prevent regressions.
