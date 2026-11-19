# Source Mapping Troubleshooting

Use this guide to match a source-mapping symptom to its failure class and the
next step to take. Every class below exists in `internal/sourcemap` or in the
`debug` command's flag validation. For flags and discovery rules, see
[source-mapping.md](source-mapping.md).

Run `glassbox debug --dry-run <tx-hash>` first. It checks `--contract-source`
and `--source-alias` before any simulation runs.

## Symptom table

| Symptom (message excerpt) | Failure class | Source | Next step |
|---------------------------|---------------|--------|-----------|
| `source discovery: local build directory not found` / preflight `wasm_target_dir` warning | **Missing artifacts:** there is no build output directory | `discovery.go`, `preflight.go` | Build the contract so that `target/wasm32-unknown-unknown/release/` exists, or pass `--contract-source` |
| preflight `wasm_artifacts`: `no .wasm files found in …` | **Missing artifacts:** the directory exists but is empty | `preflight.go` | Rebuild. Discovery only indexes `*.wasm` files directly in that directory |
| `does not have a valid WASM magic number (\0asm) — skipped` | **Invalid artifact:** the file is misnamed or corrupt | `discovery.go` | Remove or rebuild the file. Files that do not start with `\0asm` are never indexed |
| `could not read "<path>": … (skipped)` | **Unreadable artifact** | `discovery.go` | Fix the file permissions |
| `build mismatch: local WASM hash … does not match on-chain hash …` | **Mismatched artifact:** the local build differs from the deployed bytecode | `discovery.go` (`HashMismatchError`) | Rebuild from the deployed commit with the same flags. Matching is by SHA-256 only |
| Result quality `partial` (`.debug_info` stripped) | **Partial debug info** | `fallback.go` stage 2 | Keep DWARF in the release profile (`debug = true`) |
| Result quality `heuristic` | **No DWARF:** paths are inferred from symbols or `Cargo.toml` | `fallback.go` stages 3–4 | Keep DWARF, then treat the reported paths as approximate |
| Result quality `unknown`; WAT shown instead of source | **No mapping possible** | `fallback.go` stage 5 | Same as above |
| `WASM data is nil or too small (N bytes)` | **Truncated or empty bytecode** | `fallback.go` input guard | Make sure the full binary was uploaded or fetched |
| `Location: <path>:L:C` printed with no source excerpt | **Unresolved path:** the embedded path does not exist on this machine | `debug.go` `displaySourceLocation` | Remap the path with `--source-alias` or point `--contract-source` at the checkout. See [path remapping](source-mapping.md#path-remapping-example) |
| `--contract-source: directory not found` / `is a file, not a directory` / `value must not be empty` | **Bad override path** | `debug.go` flag validation | Pass an existing directory |
| `--source-alias: file not found` / `failed to parse … as JSON` | **Bad alias file** | `debug.go` flag validation | Fix the path, or make the file a flat JSON object |
| `alias: target for "<name>" must point to an existing directory` | **Stale alias target** | `alias.go` `LoadAliasConfig` | Update or remove that entry. A relative target is resolved against the directory that holds the alias file |
| `path contains null bytes and cannot be used` | **Unsafe path** | `debug.go`, `discovery.go`, `preflight.go` | Remove the null bytes |
| preflight `skip_source_mapping_env` | **Mapping disabled** | `preflight.go` | Unset `GLASSBOX_SKIP_SOURCE_MAPPING` |
| preflight `source_map_cache_dir` error | **Bad cache directory** | `preflight.go` | Unset `GLASSBOX_SOURCE_MAP_CACHE` or point it at a writable directory |

## Notes

- These checks do not depend on a toolchain. Discovery hashes `.wasm`
  bytes, and the fallback pipeline reads DWARF or symbol names whatever
  produced them. The `cargo build` hints in messages are only the
  default suggestion. Any build that writes a matching `.wasm` to the scanned
  directory, or any source tree passed with `--contract-source`, works.
- A hash mismatch never blocks debugging. It warns you that line numbers may
  not match the deployed contract.
# Source Mapping Troubleshooting Wizard

This wizard walks you through diagnosing source mapping failures in order of
likelihood. Work through each step; stop when the issue is resolved or when you
reach a fatal prerequisite that blocks further diagnosis.

## Quick diagnostic

Run `glassbox debug --dry-run` first. It validates source discovery
configuration without executing a simulation and reports every failure with a
`Fix:` hint:

```sh
glassbox debug --dry-run --network testnet --contract-source ./src <tx-hash>
```

If `--dry-run` passes but mapping still fails during execution, continue below.

---

## Step 1 — WASM artifact present and valid

**Symptom:** `--wasm: file not found` or `--wasm: not a valid WASM binary`

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 1a | WASM file exists at the path you passed to `--wasm` or at the default build location (`target/wasm32-unknown-unknown/release/`) | File exists and is readable |
| 1b | File starts with the WASM magic bytes (`\0asm`) | `file` command or `xxd` shows `WebAssembly` |
| 1c | File is at least 4 bytes | Non-trivial file size |

**Remediation:**

```sh
# Rebuild the contract with debug symbols
cargo build --target wasm32-unknown-unknown --release

# Verify the output
file target/wasm32-unknown-unknown/release/my_contract.wasm
# Should print: WebAssembly (wasm) binary module version 0x1 (MVP)
```

If the file exists but fails the magic-byte check, it may be an ELF binary or a
corrupt build artifact. Delete and rebuild.

**Fatal?** Yes — without a valid WASM binary, no source mapping is possible.

---

## Step 2 — On-chain hash matches local binary

**Symptom:** `build mismatch: local WASM hash "a1b2c3…" does not match on-chain hash "d4e5f6…"`

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 2a | The WASM binary was built from the same source that was deployed | Same `cargo build` invocation |
| 2b | `--opt-level` matches the on-chain deployment | `release` for production, `debug` for dev |

**Remediation:**

```sh
# Rebuild from the same source and flags used for deployment
cargo build --target wasm32-unknown-unknown --release

# If you deployed with a specific opt-level, rebuild with that level
# (check your CI logs or Cargo.toml profile)
```

A hash mismatch means DWARF symbols in the local binary correspond to
**different code** than what executed on-chain. Source lines will be wrong.

**Fatal?** No — mapping proceeds but lines may be inaccurate. The trace will
include a warning. Proceed to Step 3 to improve quality.

---

## Step 3 — DWARF debug symbols present

**Symptom:** Trace shows `mapping_quality: heuristic` or `unknown`; source
locations are missing or show WAT disassembly instead.

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 3a | `Cargo.toml` has `debug = true` under `[profile.release]` | Setting present |
| 3b | Binary was not stripped after compilation | `wasm-objdump -h` shows `.debug_*` sections |

**Remediation:**

```toml
# Cargo.toml
[profile.release]
debug = true
```

```sh
# Rebuild
cargo build --target wasm32-unknown-unknown --release

# Verify debug sections exist
wasm-objdump -h target/wasm32-unknown-unknown/release/my_contract.wasm | grep debug
# Expected: .debug_info, .debug_line, .debug_str, etc.
```

If `debug = true` is set but sections are missing, check for post-build
stripping in your build pipeline (e.g. `wasm-opt` or `wasm-strip`).

**Fatal?** No — the fallback pipeline will attempt symbol heuristics and Cargo
manifest discovery, but quality will be degraded.

---

## Step 4 — Project root and source paths

**Symptom:** `--contract-source: directory not found` or DWARF references
resolve to `unknown` paths.

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 4a | `--contract-source` points to the **source root** (where `Cargo.toml` lives or `src/` is a subdirectory) | Directory exists and is accessible |
| 4b | Paths in DWARF info are relative to the project root | File names match your directory layout |
| 4c | No null bytes in the path | Path passes validation |

**Remediation:**

```sh
# Point to the directory containing your Rust source
glassbox debug --wasm ./contract.wasm --contract-source ./src <tx-hash>

# Or to the crate root
glassbox debug --wasm ./contract.wasm --contract-source ./ <tx-hash>
```

If paths in DWARF don't match your layout (common in monorepos or when building
on CI with different absolute paths), use `--source-alias`:

```json
{
  "my_crate": "/home/user/projects/my_crate/src",
  "vendor_lib": "/home/user/projects/vendor/lib/src"
}
```

```sh
glassbox debug --wasm ./contract.wasm --source-alias ./aliases.json <tx-hash>
```

**Fatal?** Yes if `--contract-source` is invalid. No if using auto-discovery
(fallback pipeline continues).

---

## Step 5 — Compiler settings

**Symptom:** Source lines are mapped but don't match the actual code; optimiser
rearranges or inlines functions.

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 5a | `opt-level` is consistent between build and expected trace | Same profile |
| 5b | `lto` is not enabled if debugging | Disabled or `false` |
| 5c | `codegen-units = 1` is not set when single-file output is expected | Default or matching deployment |

**Remediation:**

```toml
# Cargo.toml — use these settings for debuggable builds
[profile.release]
debug = true
opt-level = "z"          # or whatever the deployment used
lto = false              # LTO merges modules and complicates source mapping
codegen-units = 16       # more units = more predictable line mapping
```

**Fatal?** No — mapping works but lines may shift under heavy optimisation.

---

## Step 6 — Registry and GitHub fallback

**Symptom:** `contract source not found: all discovery stages exhausted`

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 6a | Contract is verified on stellar.expert | Source link present on contract page |
| 6b | `GitHubRetriever` is configured in `.glassbox.toml` | Config key present and valid |
| 6c | Network access is available (not air-gapped) | `curl https://stellar.expert` succeeds |

**Remediation:**

```sh
# Verify contract on stellar.expert to enable registry lookup
# Or configure GitHub retrieval in .glassbox.toml
```

```toml
# .glassbox.toml
external_source_map = '[{"prefix":"/path/to/vendor/lib","remote_url":"https://github.com/org/lib","branch":"main"}]'
```

In air-gapped or CI environments, always provide `--contract-source` explicitly
and disable the interactive prompt.

**Fatal?** No if `--contract-source` is provided. Yes in CI without it.

---

## Step 7 — Alias and path remapping

**Symptom:** Source files found but paths resolve to wrong locations.

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 7a | `--source-alias` file is valid JSON | Parseable as flat object |
| 7b | Alias target directories exist on disk | All targets accessible |
| 7c | Prefix mappings in `external_source_map` cover the paths | Prefix matches DWARF paths |

**Remediation:**

```sh
# Validate alias file
glassbox debug --dry-run --source-alias ./aliases.json

# Check which aliases have stale targets (warnings, not errors)
# Then update the alias file to match current paths
```

**Fatal?** No — warnings are issued but debugging continues.

---

## Step 8 — Fallback mapper quality

**Symptom:** `mapping_quality: unknown` or empty source location in trace output.

**Checks:**

| # | Check | Pass criteria |
|---|-------|---------------|
| 8a | WASM binary is not nil or truncated (< 8 bytes) | Full binary available |
| 8b | At least one DWARF section is present | `.debug_line` or `.debug_info` |
| 8c | Cargo manifest (`Cargo.toml`) exists in the workspace | Found by directory walk |

**Remediation:**

```sh
# Rebuild with full debug info
cargo build --target wasm32-unknown-unknown --release

# Ensure the binary is fully uploaded to the build output directory
ls -la target/wasm32-unknown-unknown/release/my_contract.wasm
```

If the binary is nil or too small, the upload may have been interrupted.
Rebuild and re-upload.

**Fatal?** No — WAT disassembly is shown as a last resort.

---

## Copyable fixes summary

| Problem | Fix |
|---------|-----|
| Missing WASM file | `cargo build --target wasm32-unknown-unknown --release` |
| Bad WASM magic bytes | Delete corrupt file and rebuild |
| Hash mismatch | Rebuild from same source; verify `--opt-level` matches deployment |
| No debug symbols | Add `debug = true` to `[profile.release]` in `Cargo.toml` |
| Stripped debug sections | Remove `wasm-strip` / `wasm-opt` from build pipeline |
| Wrong `--contract-source` path | Point to source root; use `--source-alias` for remapping |
| Monorepo path mismatch | Create alias JSON mapping embedded paths to local paths |
| Contract not verified | Verify on stellar.expert or provide `--contract-source` |
| CI environment hang | Always pass `--contract-source` and `--skip-source-mapping` in CI |

---

## Machine-readable report

When `--json` is used, the source mapping result includes a structured
`source_mapping` object:

```json
{
  "source_mapping": {
    "quality": "full",
    "method": "dwarf",
    "wasm_path": "./target/wasm32-unknown-unknown/release/my_contract.wasm",
    "hash_match": true,
    "source_root": "./src",
    "warnings": [],
    "fallback_stages_tried": [],
    "resolutions": [
      {
        "address": "0x1a2b",
        "file": "src/lib.rs",
        "line": 42,
        "function": "process_transfer"
      }
    ]
  }
}
```

Use this output in CI pipelines to assert source mapping quality:

```sh
glassbox debug --json --wasm ./contract.wasm <tx-hash> | \
  jq '.source_mapping.quality' | grep -q '"full"'
```

---

## See also

- [Source mapping](./source-mapping.md) — full reference for flags and discovery pipeline
- [Debug command](./debug-command.md) — complete debug command documentation
- [CI artifacts](./ci-artifacts.md) — reproducing CI failures locally
- [Diagnostics bundle](./diagnostics-bundle.md) — generating portable diagnostics
