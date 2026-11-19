# Fuzz Testing Guide

This document provides an overview of the fuzz testing infrastructure for Glassbox, including setup, usage, and best practices.

## Overview

Fuzz testing is a security testing technique that automatically generates malformed inputs to find bugs, crashes, and vulnerabilities in parsers and other data processing functions. Glassbox uses Go's native fuzzing support to continuously test critical parsers.

## Fuzz Targets

The following fuzz targets are currently implemented:

### XDR Decoder (`internal/simulator/xdr_decoder_fuzz.go`)
- `FuzzDecodeEnvelope` - Tests transaction envelope decoding
- `FuzzXDRSafeUnmarshal` - Tests XDR unmarshaling
- `FuzzDecodeDiagnosticEvent` - Tests diagnostic event decoding

### Decoder (`internal/decoder/decoder_fuzz.go`)
- `FuzzDecodeEvents` - Tests event array decoding
- `FuzzDecodeDiagnosticEvents` - Tests gas-aware event decoding
- `FuzzUnmarshalJSON` - Tests JSON unmarshaling

### Trace Decoder (`internal/trace/event_decoder_fuzz.go`)
- `FuzzParseContractEvent` - Tests contract event parsing
- `FuzzParseEventEnvelope` - Tests raw event envelope parsing
- `FuzzApplyEventSchema` - Tests schema application

### Session Parser (`internal/session/archive_fuzz.go`)
- `FuzzValidateArchivePath` - Tests archive path validation
- `FuzzValidateIntegrity` - Tests session integrity validation
- `FuzzParseRedactionProfile` - Tests redaction profile parsing

### WASM Parser (`internal/abi/wasm_fuzz.go`)
- `FuzzValidateWasmMagic` - Tests WASM magic byte validation
- `FuzzExtractCustomSection` - Tests custom section extraction
- `FuzzAnalyzeWasmSize` - Tests WASM size analysis

### Certificate Parser (`internal/cmd/audit_verify_fuzz.go`)
- `FuzzParseCertificate` - Tests certificate parsing
- `FuzzParseTrustPolicyConfig` - Tests trust policy configuration
- `FuzzParsePEMCertificates` - Tests multiple PEM certificate parsing

## Running Fuzz Tests Locally

### Basic Fuzzing

Run a specific fuzz target for a short duration:

```bash
# Run XDR decoder fuzzing for 30 seconds
go test -fuzz=FuzzDecodeEnvelope -fuzztime=30s ./internal/simulator/

# Run decoder fuzzing for 1 minute
go test -fuzz=FuzzDecodeEvents -fuzztime=60s ./internal/decoder/
```

### Extended Fuzzing

For more thorough testing, run for longer durations:

```bash
# Run for 5 minutes
go test -fuzz=FuzzDecodeEnvelope -fuzztime=5m ./internal/simulator/

# Run for 1 hour
go test -fuzz=FuzzDecodeEnvelope -fuzztime=1h ./internal/simulator/
```

### With Corpus

Run with a specific corpus directory:

```bash
# The fuzzer automatically uses testdata/fuzz/FuzzTargetName/ as the corpus
go test -fuzz=FuzzDecodeEnvelope ./internal/simulator/
```

### All Fuzz Targets

Run all fuzz targets in a package:

```bash
# Run all fuzz targets in a package
go test -fuzz=. ./internal/simulator/
```

## Corpus Management

### Corpus Structure

```
testdata/fuzz/
├── corpus/
│   ├── xdr_decoder/
│   │   ├── README.md
│   │   ├── valid_envelope.b64
│   │   ├── minimal_xdr.bin
│   │   └── ...
│   ├── decoder/
│   ├── trace/
│   ├── session/
│   ├── wasm/
│   └── certificate/
└── TRIAGE.md
```

### Adding Seed Inputs

To add new seed inputs to the corpus:

1. Place the input file in the appropriate corpus directory
2. Run the fuzzer to incorporate it:
   ```bash
   go test -fuzz=FuzzTargetName -fuzztime=30s ./path/to/package
   ```
3. The fuzzer will automatically minimize and add interesting inputs to the corpus

### Regression Inputs

When a crash is found and fixed, add the crash input to the corpus to prevent regression:

```bash
# Copy the crash input to the corpus
cp <crash_input> testdata/fuzz/corpus/<target>/regression_<issue>.bin

# Document the fix in the corpus README
```

## CI Integration

### Fuzz Workflow

The fuzz testing workflow (`.github/workflows/fuzz.yml`) runs on:
- Every push to `main` and `develop` branches
- Every pull request to `main` and `develop` branches
- Weekly on Sundays at 00:00 UTC (scheduled deep run)

### Execution Bounds

CI runs with the following bounds:

| Event Type | Fuzz Time | Timeout |
|------------|-----------|---------|
| Pull Request | 30s | 600s |
| Push | 60s | 600s |
| Scheduled | 300s | 600s |

### Failure Handling

When a fuzz target crashes in CI:
1. The crash input is uploaded as an artifact
2. The job fails and blocks the PR/merge
3. Developers must triage and fix the crash
4. The crash input is added to the corpus as a regression test

## Writing Fuzz Targets

### Basic Structure

```go
//go:build go1.18
// +build go1.18

package package

import "testing"

func FuzzTargetName(f *testing.F) {
    // Add seed inputs
    f.Add([]byte("valid input"))
    f.Add([]byte{})
    f.Add([]byte("malformed"))

    f.Fuzz(func(t *testing.T, data []byte) {
        // Call the function under test
        result, err := ParseFunction(data)
        
        // Expect errors for malformed input, but no panics
        _ = result
        _ = err
    })
}
```

### Best Practices

1. **Add Seed Inputs**: Provide valid and edge-case inputs to guide the fuzzer
2. **Handle Errors Gracefully**: Expect errors for malformed input, but never panic
3. **Keep Targets Focused**: Each fuzz target should test one function or small group
4. **Add Bounds Checks**: Early exit for obviously invalid or oversized inputs
5. **Avoid External Dependencies**: Fuzz targets should be self-contained

### Example: Parser Fuzz Target

```go
func FuzzParseJSON(f *testing.F) {
    // Seed with valid and edge-case JSON
    f.Add([]byte(`{"key":"value"}`))
    f.Add([]byte(`{}`))
    f.Add([]byte(`[]`))
    f.Add([]byte(`invalid`))

    f.Fuzz(func(t *testing.T, data []byte) {
        var result map[string]interface{}
        err := json.Unmarshal(data, &result)
        _ = err // Expect errors for malformed JSON, but no panics
    })
}
```

## Crash Triage

When a crash is detected, follow the triage process in [TRIAGE.md](TRIAGE.md):

1. **Reproduce the crash** with the failing input
2. **Minimize the input** using the fuzzer's minimization
3. **Analyze the root cause** (stack trace, code review)
4. **Fix the issue** (add validation, bounds checking)
5. **Add regression test** to prevent recurrence
6. **Update the corpus** with the crash input

## Timeout Triage

When a timeout occurs:

1. **Identify the slow path** with profiling
2. **Add execution bounds** to the fuzz target
3. **Optimize the target** if possible
4. **Adjust fuzz timeouts** if the slowness is expected

## Acceptance Criteria

The fuzz testing implementation meets the following acceptance criteria:

✅ **Each parser has at least one fuzz target**
- XDR decoder: 3 targets
- JSON decoder: 3 targets
- Trace decoder: 3 targets
- Session parser: 3 targets
- WASM parser: 3 targets
- Certificate parser: 3 targets

✅ **Corpus inputs are minimized and versioned**
- Corpus directories created for each target
- README files document corpus contents
- Regression inputs tracked with issue numbers

✅ **Fuzz failures produce reproducible commands**
- Crash inputs uploaded as CI artifacts
- Triage guide provides reproduction steps
- Regression tests prevent recurrence

✅ **CI enforces time and memory bounds**
- Fuzz time: 30s (PR), 60s (push), 300s (scheduled)
- Job timeout: 600s
- Memory limit: 512MB (configurable)

## Resources

- [Go Fuzzing Documentation](https://go.dev/doc/fuzz/)
- [Fuzzing Book](https://www.fuzzingbook.org/)
- [OSS-Fuzz](https://google.github.io/oss-fuzz/)
- [Triage Guide](TRIAGE.md)

## Contributing

When adding new parsers or data processing functions:

1. Create a corresponding fuzz target
2. Add seed inputs to the corpus
3. Update this documentation
4. Add the target to the CI workflow if needed
5. Run the fuzzer locally before committing

## Troubleshooting

### Fuzzer Not Finding Bugs

- Increase fuzz time
- Add more diverse seed inputs
- Review the target for proper error handling
- Check if the target is too narrow

### Fuzzer Too Slow

- Add early exit conditions for invalid inputs
- Reduce algorithmic complexity
- Limit recursion depth
- Add size bounds on parsed structures

### CI Failures

- Download crash artifacts from CI
- Reproduce locally following triage guide
- Fix the underlying issue
- Add regression test to corpus
