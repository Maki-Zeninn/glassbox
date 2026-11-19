# Certificate Parser Fuzz Corpus

This directory contains seed inputs for the certificate parser fuzz targets.

## Corpus Contents

- `valid_cert.pem` - Valid PEM-encoded certificate
- `invalid_pem.txt` - Invalid PEM structure
- `incomplete_pem.pem` - Incomplete PEM block
- `invalid_base64.pem` - PEM with invalid base64
- `malformed_cert.pem` - Malformed certificate data
- `multiple_certs.pem` - Multiple PEM-encoded certificates

## Adding New Seeds

To add new seed inputs:
1. Place the file in this directory
2. Run: `go test -fuzz=FuzzParseCertificate -fuzztime=30s ./internal/cmd/`
3. The fuzzer will automatically incorporate new interesting inputs

## Regression Cases

Add any discovered crash inputs here to prevent regressions.
