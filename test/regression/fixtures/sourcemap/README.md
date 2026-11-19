# Source-Map Fixtures

Minimal WASM stubs, alias JSON files, and build manifests for source-mapping
regression tests.

## File naming

```
sourcemap_<scenario-slug>_<issue-or-pr-slug>.<ext>
```

Extensions:
- `.wasm`          — minimal WASM binary (may be 8-byte magic+version stub)
- `.alias.json`    — `{"prefix": "/path/to/local"}` alias map
- `.manifest.json` — `glassbox-build-manifest.json` stub

Examples:
- `sourcemap_missing_contract_source_issue117.manifest.json`
- `sourcemap_path_traversal_rejected_issue200.alias.json`
- `sourcemap_generated_path_classified_issue501.wasm`

## Minimal WASM stub

For tests that only need to verify path handling without parsing DWARF:

```
\x00asm\x01\x00\x00\x00
```

That 8-byte sequence is the minimal valid WASM magic + version.  Use it when
the test does not need real WASM content.

## Build manifest stub

```json
{
  "source_root": "/workspace/my-contract",
  "repository_revision": "aabbccddaabbccddaabbccddaabbccddaabbccdd",
  "compiler_version": "rustc 1.77.2 (25ef9e3d8 2024-04-09)",
  "artifact_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
}
```

## Rules

- WASM stubs must start with the 4-byte magic `\x00asm` or they will be
  rejected with a "not a valid WASM binary" error, which may not be the
  failure class under test.
- Alias targets that do not exist on disk produce a warning (not an error);
  include a `_comment` explaining this is intentional when it matters.
- Build manifests must not contain `..` path traversal in `source_root`.
