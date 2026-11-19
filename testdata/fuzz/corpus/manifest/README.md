# Manifest Fuzz Corpus

Seed corpus for the manifest fuzz targets in `internal/manifest/manifest_fuzz_test.go`.

## Targets

- `FuzzVerifyManifest` — signature verification with arbitrary JSON
- `FuzzCanonicalJSON` — canonical JSON serialization
- `FuzzManifestHash` — SHA-256 hash computation

## Coverage targets

- Well-formed unsigned manifests
- Missing or empty crypto fields
- Non-object JSON (arrays, null, primitives)
- Schema version mismatches
- Hex fields of wrong length or with non-hex characters
- Artifacts with negative sizes or missing required fields

## Security note

`FuzzVerifyManifest` targets the trust boundary where an external manifest
is first parsed before its signature is checked. The fuzzer ensures that
malformed input cannot cause a panic before the signature check runs.

## Adding regression inputs

Copy minimised crash inputs here as `regression_<issue>.json`.
