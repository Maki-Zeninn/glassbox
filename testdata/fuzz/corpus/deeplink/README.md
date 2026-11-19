# Deep Link URI Fuzz Corpus

Seed corpus for `FuzzParseDebugURI` (`internal/protocolreg/uri_fuzz_test.go`).

## Coverage targets

- Valid `glassbox://debug/<hash>?network=...` URIs
- Probe URI: `glassbox://doctor-probe`
- Wrong scheme, missing scheme, empty input
- Malformed transaction hashes (wrong length, non-hex chars)
- Query-string injection (SQL, path traversal, out-of-range integer fields)
- Percent-encoded path separators
- Control characters and high-codepoint Unicode
- Overlong URIs (> maxURILen = 4096 bytes)

## Adding regression inputs

When the fuzzer surfaces a new crash or hang, minimise the input with
`go test -fuzz=FuzzParseDebugURI -fuzzminimizetime=30s` and copy the
resulting file here as `regression_<issue>.txt`.
