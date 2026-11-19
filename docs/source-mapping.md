# Source Mapping

Glassbox maps WASM instruction failures back to Rust source code lines using
DWARF debug symbols embedded in the compiled WASM binary.

## Explain Mode

When a source mapping is unexpected, low-confidence, or fails entirely, the
`sourcemap explain` command reveals the full resolution decision trail:

```bash
# Explain DWARF address resolution
glassbox sourcemap explain --wasm ./contract.wasm --addr 0x1234

# Same in machine-readable form (for CI and automation)
glassbox sourcemap explain --wasm ./contract.wasm --addr 0x1234 --format json

# Explain contract source-discovery (cache / registry / GitHub / override pipeline)
glassbox sourcemap explain --contract-id C...
glassbox sourcemap explain --contract-id C... --format json
```

### What the output shows

For each pipeline stage attempted, the output lists:

| Field      | Description |
|------------|-------------|
| `stage`    | Which resolution stage was tried (e.g. `full_dwarf`, `cache`, `registry`) |
| `accepted` | Whether this stage produced the final result |
| `reason`   | Why the candidate was accepted or rejected |
| `location` | Resolved file and line number (when available) |
| `quality`  | `full` \| `partial` \| `heuristic` \| `unknown` |
| `confidence` | 0–100 score for the final mapping |

The explain output never contains raw WASM binary data or full source file contents.

### Interpreting confidence

| Score | Meaning |
|-------|---------|
| 100   | Exact DWARF line-table hit at the instruction address |
| 72    | Subprogram-level match; exact line unavailable |
| 48    | Partial DWARF — file inferred from `.debug_line` table |
| 28    | Heuristic — source path inferred from mangled symbol name |
| 22    | Heuristic — source path inferred from `Cargo.toml` discovery |
| 0     | Unresolved — no source location could be produced |

### Stages in the DWARF mapping pipeline

| Stage             | Description |
|-------------------|-------------|
| `input_guard`     | WASM data too small to contain valid content |
| `full_dwarf`      | Complete DWARF line-number tables |
| `partial_dwarf`   | File names extracted from `.debug_line` when `.debug_info` is stripped |
| `symbol_heuristic`| Source path inferred from Rust mangled symbols in WASM name section |
| `cargo_manifest`  | Source root inferred from `Cargo.toml` found in the project tree |
| `none`            | All stages exhausted without a resolution |

### Stages in the source-discovery pipeline

| Stage            | Description |
|------------------|-------------|
| `build_manifest` | `glassbox-build-manifest.json` provides the source root directly |
| `cache`          | Previously resolved source returned from local cache |
| `registry`       | Verified source fetched from stellar.expert |
| `github`         | Source retrieved from GitHub via the configured retriever |
| `local_override` | Explicit `--contract-source` path used |
| `none`           | All stages exhausted; `--skip-source-mapping` or `--contract-source` needed |

## Automatic Discovery

When a contract fails, Glassbox attempts to resolve the source location through
the following pipeline:

1. **Local cache** — previously resolved source is returned immediately.
2. **Registry** — queries [stellar.expert](https://stellar.expert) for a
   verified source link.
3. **GitHub fallback** — downloads source from the linked repository when a
   `GitHubRetriever` is configured.
4. **`--contract-source` override** — uses the explicitly provided local path
   (see below).
5. **Interactive prompt** — asks the user for a WASM path when all automatic
   methods fail. In non-interactive environments (CI pipelines) this stage is
   skipped and an explicit error is returned instead of hanging on stdin.

### Non-interactive / CI mode

In CI pipelines and other non-interactive environments, the interactive prompt
is disabled automatically. When all discovery stages fail, Glassbox returns an
explicit error:

```
contract source not found: all discovery stages exhausted for contract "C..."
  Stages tried: cache, registry (stellar.expert), GitHub retriever, --contract-source override
  To resolve: provide --contract-source <path> pointing to the contract source directory,
  or verify the contract on stellar.expert to enable registry lookup.
  Use --skip-source-mapping to proceed without source mapping.
```

Set `--skip-source-mapping` to bypass source discovery entirely when you only
need raw trace output.

## `--contract-source` Override

When automatic discovery fails (e.g. the contract is not yet verified on
stellar.expert, or you are working with a private repository), you can provide
the path to the contract source directory explicitly:

```bash
glassbox debug --wasm ./target/wasm32-unknown-unknown/release/my_contract.wasm \
               --contract-source ./src \
               <transaction-hash>
```

Or for local WASM replay:

```bash
glassbox debug --wasm ./contract.wasm \
               --contract-source /path/to/contract/src
```

### Validation

The `--contract-source` path is validated before any network or simulator work begins:

| Condition | Error |
|-----------|-------|
| Path does not exist | `--contract-source: directory not found: "<path>"` |
| Path is a file, not a directory | `--contract-source: "<path>" is a file, not a directory` |
| Path is not accessible | `--contract-source: cannot access "<path>": <os error>` |
| Empty or whitespace-only value | `--contract-source: value must not be empty or whitespace` |

Each error includes a remediation hint so you know exactly what to fix.

### WASM binary validation

When checking hash mismatches, Glassbox validates that the local file is a valid WASM binary before computing its hash. Files that do not start with the WASM magic bytes (`\0asm`) produce a clear error:

```
not a valid WASM binary: "<path>" does not start with WASM magic bytes
```

This catches corrupted or non-WASM files early, before the hash comparison step.

### How it works

- When `--contract-source <path>` is set and automatic source resolution fails,
  Glassbox uses `<path>` as the root directory for resolving source file
  references from DWARF debug info.
- The path is tried directly, then as a prefix for the relative file path
  reported by the DWARF info, and finally as a prefix for just the filename.
- The path is also forwarded to the simulator via `ContractSourcePath` in the
  `SimulationRequest`, allowing the Rust simulator to resolve source lines
  during execution.

### When to use it

| Situation | Recommendation |
|-----------|---------------|
| Contract not verified on stellar.expert | `--contract-source ./src` |
| Private repository | `--contract-source /path/to/repo/src` |
| Monorepo with multiple contracts | `--contract-source ./contracts/my_contract/src` |
| CI/CD pipeline (non-interactive) | `--contract-source $CONTRACT_SRC_DIR` |

### Compiling with debug symbols

For best results, compile your contract with debug symbols:

```toml
# Cargo.toml
[profile.release]
debug = true
```

Then build:

```bash
cargo build --target wasm32-unknown-unknown --release
```

See [docs/debug-symbols-guide.md](debug-symbols-guide.md) for more details.

For Cargo manifests specifically, run `glassbox validate-cargo` to surface unsupported `lto` or `debug` values and receive actionable fixes before you build or replay a contract.

## Cross-repository source links

When contract sources live in another Git repository, map local path prefixes to
remote GitHub URLs in `.glassbox.toml`:

```toml
external_source_map = '[{"prefix":"/path/to/vendor/lib","remote_url":"https://github.com/org/lib","branch":"main"}]'
```

Glassbox uses these mappings when a source file path falls outside the workspace
repository but under the configured prefix.

## Skip source mapping

For faster raw replay when you only need WASM offsets and traces:

```bash
glassbox debug --wasm ./contract.wasm --skip-source-mapping
```

This bypasses DWARF parsing and Git link generation in the simulator.

## Trace verbosity

Control trace detail with `--trace-verbosity`:

| Level | Output |
|-------|--------|
| `summary` | Step names and status only |
| `normal` | Source locations and links (default) |
| `verbose` | Arguments, WASM instructions, and full event payloads |

```bash
glassbox debug --wasm ./contract.wasm --trace-verbosity summary
glassbox trace --print --trace-verbosity verbose execution.json
```

## Fallback pipeline

When no DWARF symbols are available, Glassbox uses a multi-stage fallback
pipeline to provide a best-effort source location:

| Stage | Mechanism | Quality |
|-------|-----------|---------|
| 1 | Full DWARF line-number tables | `full` |
| 2 | Partial DWARF — extract file names from `.debug_line` even when `.debug_info` is stripped | `partial` |
| 3 | Symbol heuristics — infer source paths from Rust mangled symbol names | `heuristic` |
| 4 | Cargo manifest discovery — walk the repo for `Cargo.toml` files | `heuristic` |
| 5 | Unknown — no mapping possible; WAT disassembly shown instead | `unknown` |

Each fallback stage emits a `Warning:` field in the result explaining what was
used and why the mapping may be inaccurate, along with a `debug = true`
remediation hint.

## Local WASM build discovery

Glassbox scans `target/wasm32-unknown-unknown/release/` for WASM files whose
SHA-256 hash matches the on-chain contract bytecode. When a match is found,
DWARF symbols are loaded automatically.

If the build directory is missing, Glassbox logs a debug-level message and
continues without local symbols. The message includes a suggestion to run
`cargo build` if local symbols are needed.

### WASM file validation during discovery

Files found in the build output directory are validated before indexing:

- Files named `.wasm` but not starting with the WASM magic bytes (`\0asm`) are
  **skipped with a warning** rather than silently hashed. This prevents corrupt
  or misnamed files (e.g. ELF binaries accidentally named `.wasm`) from
  polluting the hash table with useless entries.
- Files shorter than 4 bytes cannot contain a valid magic number and are also
  skipped with a warning.

**Example warning:**
```
"./target/wasm32-unknown-unknown/release/old_build.wasm" does not have a valid
WASM magic number (\0asm) — skipped
  Rebuild with 'cargo build --release --target wasm32-unknown-unknown'
  to ensure the file is a proper WASM binary.
```

### Path safety

Any path supplied to source-mapping functions is validated for null bytes before
filesystem access begins. Null bytes in file paths are a shell-injection risk and
produce obscure OS errors deep in the path layer:

| Input | Error |
|-------|-------|
| `--contract-source /path\x00bad` | `--contract-source: path contains null bytes and cannot be used` |
| `--source-alias /path\x00bad.json` | `--source-alias: path contains null bytes and cannot be used` |
| `GLASSBOX_SOURCE_MAP_CACHE=/path\x00bad` | `GLASSBOX_SOURCE_MAP_CACHE=... contains null bytes and cannot be used` |

Each error includes a `Fix:` hint and the offending flag name so you know
exactly what to correct.

### Fallback mapper input guard

`FallbackMapper.Resolve` now guards against nil or abnormally small WASM data
(fewer than 8 bytes). Previously, passing nil data would silently fall through
all stages and return a no-context unknown result. The new behavior returns an
explicit `MappingQualityUnknown` result with a descriptive warning:

```
[sourcemap] WASM data is nil or too small (0 bytes) to contain valid content —
source location for address 0x100 cannot be resolved.
Recompile with 'cargo build --release --target wasm32-unknown-unknown'
and ensure the binary is fully uploaded.
```
### Hash mismatch diagnostic

When the local WASM binary's SHA-256 hash differs from the on-chain contract
hash, Glassbox surfaces a `build mismatch` warning rather than the previous
misleading `opt-level mismatch` message (the hash can differ for many reasons
beyond opt-level — an outdated build, a different contract, or different
compilation flags):

```
build mismatch: local WASM hash "a1b2c3..." does not match on-chain hash "d4e5f6..." (path: ./target/.../my_contract.wasm)
  The local binary differs from the deployed contract — it may be outdated,
  built with different flags, or be a completely different contract.
  Hint: rebuild with 'cargo build --release --target wasm32-unknown-unknown'
  and verify --opt-level matches the on-chain deployment.
```

## `--source-alias` Alias Mapping

When source file paths embedded in DWARF symbols don't match your local
directory layout, remap them with an alias file:

```bash
glassbox debug --source-alias ./aliases.json <tx-hash>
```

The alias file must be a flat JSON object:

```json
{
  "my_crate": "/path/to/my_crate/src",
  "vendor_lib": "/path/to/vendor/lib/src"
}
```

**Validation:** The file must be readable and contain valid JSON. Invalid JSON
produces an explicit error, and each alias entry must have a non-empty name and
non-empty target path:

```
--source-alias: failed to parse "<path>" as JSON: <detail>
  The file must be a flat JSON object mapping alias strings to local paths.
  Example: {"my_crate": "/path/to/my_crate/src"}
```

Every alias target must be an existing directory. A missing target is
rejected with `alias: target for "<name>" must point to an existing directory`.
A relative target is resolved against the directory that holds the alias file.

### Path remapping example

Suppose a contract was built in CI, so its DWARF embeds paths like
`/ci/build/my_crate/src/lib.rs`, and your checkout is at
`/home/dev/my_crate/src`. Map the embedded prefix to the local directory:

```json
{
  "/ci/build/my_crate/src": "/home/dev/my_crate/src"
}
```

```bash
glassbox debug --dry-run --source-alias ./aliases.json <tx-hash>   # validate first
glassbox debug --source-alias ./aliases.json <tx-hash>
```

Normalization rules (`AliasResolver.Resolve` in `internal/sourcemap/alias.go`):

- Separators are converted to `/` before comparison. Matching is
  case-sensitive.
- An alias matches when it equals the whole path, or when the path starts with
  `<alias>/`. `/ci/build/my_crate/src` therefore does **not** match
  `/ci/build/my_crate/srcx/lib.rs`.
- The rest of the path is joined onto the target:
  `/ci/build/my_crate/src/lib.rs` → `/home/dev/my_crate/src/lib.rs`.
- Paths that match no alias are left unchanged. Avoid overlapping aliases,
  because the order in which they are tried is not defined.

**Confirming that it worked:** the `Location:` line always shows the
*embedded* path, not the remapped one. Remapping worked if a source excerpt,
with `> ` marking the current line, appears below that line. If only the
`Location:` line is printed, the remapped file could not be read. In that
case, check the target path, or add `--contract-source` as a fallback.

## Artifact naming and search order

Glassbox finds artifacts by **content hash, not by file name**. There are no
sidecar names to follow.

**WASM discovery** (`DiscoverLocalSymbols`, `internal/sourcemap/discovery.go`):

| Rule | Detail |
|------|--------|
| Directory scanned | `<project-root>/target/wasm32-unknown-unknown/release/` only |
| Files considered | Any name ending in `.wasm`, for example `my_contract.wasm` |
| Not considered | Subdirectories (such as `deps/`), and files with other extensions |
| Validation | The file must start with `\0asm`. Other files are skipped with a warning |
| Matching | The SHA-256 of the file is compared to the on-chain hash |

Separate debug-info files are **not** supported: `.dwp`, `.debug`, `.map`,
and split-DWARF sidecars are not searched. DWARF must be embedded in the
`.wasm` file itself.

Minimal layout:

```
my-project/
├── src/lib.rs
└── target/wasm32-unknown-unknown/release/
    └── my_contract.wasm      # discovered; any *.wasm name works
```

**Source file lookup** (`displaySourceLocation`, `internal/cmd/debug.go`).
For each embedded path, Glassbox tries these locations in order and stops at
the first readable file:

1. The path after `--source-alias` remapping (or the embedded path if no alias
   matches)
2. `<--contract-source>/<path>`
3. `<--contract-source>/<basename of path>`
4. `src/<path>` relative to the current directory

If the default search cannot find your files, use the explicit overrides
[`--contract-source`](#--contract-source-override) and
[`--source-alias`](#--source-alias-alias-mapping).

For symptom-based help, see
[source-mapping-troubleshooting.md](source-mapping-troubleshooting.md).

## Dry-run source discovery checks

`glassbox debug --dry-run` validates source discovery configuration before any
simulation runs:

```
[OK]   Source directory: ./src
[OK]   Source alias file: ./aliases.json (2 mapping(s))
```

Failures appear as numbered items in the `Dry-run FAILED` summary with a
`Fix:` hint for each.

## Reproducible Build Manifests (Issue #45)

Build paths embedded in DWARF debug information are absolute paths on the
machine that compiled the contract. On a different machine those paths don't
exist, making source mapping fragile or impossible.

A **build manifest** solves this by recording the build-time source root,
repository revision, compiler version, and artifact hash. Glassbox uses the
manifest to strip the build-machine prefix from every DWARF path and remap it
to a repo-relative path that works everywhere.

### Manifest schema

The manifest is a plain JSON file named `glassbox-build-manifest.json`:

```json
{
  "source_root":           "/home/ci/workspace/my-contract",
  "repository_revision":   "a3f8c1d2e4b7091f3d6a5c8e2b4f0d1e7c9a2b5f",
  "compiler_version":      "rustc 1.77.2 (25ef9e3d8 2024-04-09)",
  "artifact_hash":         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `source_root` | yes | Absolute path to the source tree on the build machine |
| `repository_revision` | yes | Full Git commit SHA (`git rev-parse HEAD`) |
| `compiler_version` | no | `rustc --version` output, stored for diagnostics |
| `artifact_hash` | yes | SHA-256 hex digest of the compiled `.wasm` binary |

### Generating a manifest in CI

Add a step after `cargo build` to write the manifest:

```bash
ARTIFACT=target/wasm32-unknown-unknown/release/my_contract.wasm

jq -n \
  --arg source_root   "$(pwd)" \
  --arg revision      "$(git rev-parse HEAD)" \
  --arg compiler      "$(rustc --version)" \
  --arg hash          "$(sha256sum $ARTIFACT | awk '{print $1}')" \
  '{source_root: $source_root, repository_revision: $revision,
    compiler_version: $compiler, artifact_hash: $hash}' \
  > glassbox-build-manifest.json
```

Commit or publish both `my_contract.wasm` and `glassbox-build-manifest.json`
as CI artifacts so any machine can replay the trace.

### Using the manifest

**Explicit path:**

```bash
glassbox debug \
  --wasm      ./my_contract.wasm \
  --build-manifest ./glassbox-build-manifest.json \
  <transaction-hash>
```

**Auto-discovery** — Glassbox searches these locations under the project root
automatically so you don't need the flag when the file is in the right place:

| Search path (relative to project root) | Example |
|-----------------------------------------|---------|
| `.` | `./glassbox-build-manifest.json` |
| `target/` | `./target/glassbox-build-manifest.json` |
| `target/wasm32-unknown-unknown/release/` | (CI artifact drop location) |

**Environment variable** — set a default manifest for all invocations:

```bash
export GLASSBOX_BUILD_MANIFEST=/path/to/glassbox-build-manifest.json
glassbox debug <transaction-hash>
```

Priority order (highest → lowest):

1. `--build-manifest <path>` CLI flag
2. `GLASSBOX_BUILD_MANIFEST` environment variable / `build_manifest_path` in
   `.glassbox.toml`
3. Auto-discovery in conventional locations
4. Remaining pipeline stages (cache, registry, `--contract-source`, prompt)

### Artifact hash verification

When `--wasm` is provided alongside `--build-manifest`, Glassbox computes the
SHA-256 of the local WASM file and compares it to `artifact_hash` in the
manifest. A mismatch is rejected immediately with an actionable error:

```
build manifest mismatch: artifact hash in manifest "glassbox-build-manifest.json"
  (e3b0c44298...) does not match local WASM hash (a1b2c3d4ef...) for file
  "./target/.../my_contract.wasm"
  The manifest was generated from a different build — rebuild with
  'cargo build --release --target wasm32-unknown-unknown' and regenerate
  the manifest, or omit --build-manifest to skip hash verification.
```

This guarantees that a trace captured on one machine maps correctly on
another: the manifest and the artifact must be from the same build.

### Path traversal safety

`source_root` in the manifest is validated before any filesystem access:

| Violation | Error |
|-----------|-------|
| Empty value | `build manifest: missing required field 'source_root'` |
| Contains null bytes | `build manifest: 'source_root' contains null bytes` |
| Contains `..` traversal | `build manifest: 'source_root' contains path traversal sequences (..)` |

### Repository revision and GitHub links

When `repository_revision` is present, Glassbox uses it to construct
**permalink** GitHub source links pointing at the exact commit rather than
the branch `HEAD`. This means links remain valid even after subsequent commits
are pushed.

The revision must be a full 40-character or short (≥ 7 character) hex SHA, or
a branch/tag name. An empty or whitespace-only value is rejected:

```
build manifest: missing required field 'repository_revision'
  Set it to the full Git commit SHA (e.g. output of 'git rev-parse HEAD').
```

### Cross-machine replay example

**On the build machine (CI):**

```bash
# 1. Build with debug symbols
cargo build --target wasm32-unknown-unknown --release

# 2. Generate manifest
ARTIFACT=target/wasm32-unknown-unknown/release/my_contract.wasm
jq -n \
  --arg source_root "$(pwd)" \
  --arg revision    "$(git rev-parse HEAD)" \
  --arg compiler    "$(rustc --version)" \
  --arg hash        "$(sha256sum $ARTIFACT | awk '{print $1}')" \
  '{source_root: $source_root, repository_revision: $revision,
    compiler_version: $compiler, artifact_hash: $hash}' \
  > glassbox-build-manifest.json

# 3. Upload both files as CI artifacts
```

**On the developer machine (replay):**

```bash
# Download my_contract.wasm and glassbox-build-manifest.json from CI

glassbox debug \
  --wasm           ./my_contract.wasm \
  --build-manifest ./glassbox-build-manifest.json \
  <transaction-hash>
```

Glassbox strips `/home/ci/workspace/my-contract/` from every DWARF path,
leaving a repo-relative tail (`src/lib.rs`, `src/token.rs`, …) that resolves
correctly on the developer's checkout regardless of where they cloned the
repository.
