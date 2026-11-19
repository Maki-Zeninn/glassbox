# Compatibility Matrix

This document is the authoritative record of every public-facing surface in
Glassbox, its stability level, and its deprecation/removal policy. It is
updated as part of every release review and is cross-referenced by API snapshot
checks and changelog fragments.

---

## Stability levels

| Level | Meaning |
|-------|---------|
| **stable** | No breaking changes within a major version. Changes require a deprecation cycle and a migration note. |
| **beta** | Interface is functional and tested but may change in a minor release with at least one minor-version warning window. |
| **experimental** | May change or be removed in any release. No deprecation window. |
| **deprecated** | Functional but scheduled for removal. Replacement is documented. |
| **removed** | No longer present. Migration notes are in [MIGRATION_GUIDE.md](../MIGRATION_GUIDE.md). |

Stability is per-surface, not per-command. A command can have a stable CLI
surface while an adjacent JSON field remains beta.

---

## 1. CLI flags

Stability is per-flag. Adding a flag is non-breaking. Renaming or removing one
requires a deprecation cycle (see §6).

| Flag | Command(s) | Stability | Since | Notes |
|------|-----------|-----------|-------|-------|
| `--network` | `debug`, `generate-bindings`, `check-bindings` | stable | 0.1 | Values: testnet, mainnet, futurenet, standalone, public |
| `--json` | `debug`, `audit:sign`, `version`, `protocol:diagnose`, `bench` | stable | 0.1 | Enables JSON envelope output |
| `--format` | `debug`, `export`, `generate-bindings` | stable | 0.2 | Values: json, text, html, markdown |
| `--dry-run` | `debug` | stable | 0.3 | Validates inputs without executing |
| `--progress-json` | `debug` | stable | 0.4 | Emits NDJSON progress events to stderr |
| `--interactive` / `-i` | `debug` | stable | 0.2 | Launches interactive trace viewer |
| `--payload` | `audit:sign` | stable | 0.1 | Inline JSON payload |
| `--payload-file` | `audit:sign` | stable | 0.1 | Path to JSON payload file |
| `--signing-provider` | `audit:sign` | stable | 0.3 | Values: software, pkcs11 |
| `--validate-only` | `audit:sign` | stable | 0.3 | Preflight without signing |
| `--software-private-key` | `audit:sign` | stable | 0.1 | PEM or hex Ed25519 key |
| `--plan` | `audit:sign` | beta | 0.4 | Print execution plan; output format may evolve |
| `--hsm-provider` | `audit:sign` | deprecated | 0.1 | Use `--signing-provider` instead. Removed in v2.0. |
| `--bundle` | `doctor` | stable | 0.4 | Generate diagnostics bundle |
| `--bundle-output` | `doctor` | stable | 0.4 | Path for bundle output |
| `--non-interactive` | global | stable | 0.2 | Force non-interactive mode |
| `--no-color` | global | stable | 0.2 | Disable ANSI color (also: `NO_COLOR` env var) |
| `--session` | `stats` | beta | 0.3 | Load a saved session by ID |
| `--wasm` | `debug` | stable | 0.2 | Path to WASM binary for source mapping |
| `--trace-output` | `debug` | stable | 0.3 | Path for trace export file |
| `--export-format` | `debug` | stable | 0.3 | Export format for `--trace-output` |
| `--verbosity` | `debug` | stable | 0.2 | Values: quiet, normal, verbose |
| `--compare-network` | `debug` | beta | 0.4 | Secondary network for comparison |
| `--spec-file` | `generate-bindings`, `check-bindings` | beta | 0.4 | Path to contract spec |
| `--spec-format` | `generate-bindings`, `check-bindings` | beta | 0.4 | Values: json, xdr |
| `--runtime` | `generate-bindings` | beta | 0.4 | Values: node, browser, universal |

### Environment variable mirrors

Every `GLASSBOX_*` environment variable that mirrors a flag has the same
stability level as the flag it mirrors.

| Environment variable | Mirrors | Stability |
|---------------------|---------|-----------|
| `GLASSBOX_NETWORK` | `--network` | stable |
| `GLASSBOX_AUDIT_PRIVATE_KEY_PEM` | `--software-private-key` (PEM) | stable |
| `GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX` | `--software-private-key` (hex) | stable |
| `GLASSBOX_SIGNING_PROVIDER` | `--signing-provider` | stable |
| `GLASSBOX_SIGNER_TYPE` | `--signing-provider` | deprecated |
| `GLASSBOX_PKCS11_MODULE` | `--pkcs11-module` | stable |
| `GLASSBOX_PKCS11_PIN` | `--pkcs11-pin` | stable |
| `GLASSBOX_PKCS11_TOKEN_LABEL` | `--pkcs11-token-label` | stable |
| `GLASSBOX_PKCS11_KEY_LABEL` | `--pkcs11-key-label` | stable |
| `GLASSBOX_PKCS11_KEY_ID` | `--pkcs11-key-id` | stable |
| `NO_COLOR` | `--no-color` | stable |
| `GLASSBOX_NO_COLOR` | `--no-color` | stable |
| `GLASSBOX_RPC_TOKEN` | internal RPC auth | stable |

---

## 2. JSON output fields

The `data` envelope emitted by `--json` / `--format json` commands. The outer
envelope fields (`schema_version`, `glassbox_version`, `generated_at`,
`command`, `data`) are **stable** across all commands.

### `debug` data object

| Field | Type | Stability | Notes |
|-------|------|-----------|-------|
| `transaction_hash` | string | stable | |
| `network` | string | stable | |
| `simulation_status` | string | stable | Values: success, failed |
| `events` | array | stable | Array of event objects |
| `state_changes` | array | stable | Ledger entry diffs |
| `cost` | object | stable | CPU and memory budget usage |
| `cost.cpu_insns` | integer | stable | |
| `cost.mem_bytes` | integer | stable | |
| `error` | object\|null | stable | Null when simulation_status=success |
| `source_map` | object\|null | beta | Present when `--wasm` supplied; schema may gain fields |
| `comparison` | object\|null | beta | Present when `--compare-network` used |

### `audit:sign` data object

| Field | Type | Stability | Notes |
|-------|------|-----------|-------|
| `version` | string | stable | |
| `timestamp` | string (RFC 3339) | stable | |
| `trace_hash` | string | stable | SHA-256 hex |
| `signature` | string | stable | Base64 |
| `public_key` | string | stable | Base64 |
| `provider` | string | stable | |
| `provenance` | object\|null | beta | Added in 0.4; fields may expand |
| `payload` | object | stable | The signed payload verbatim |

### `version` data object

| Field | Type | Stability | Notes |
|-------|------|-----------|-------|
| `version` | string | stable | |
| `commit_sha` | string | stable | |
| `build_date` | string | stable | |
| `go_version` | string | stable | |
| `is_dev` | boolean | stable | |
| `user_agent` | string | stable | |

### Error envelope (`error` key instead of `data`)

| Field | Type | Stability | Notes |
|-------|------|-----------|-------|
| `error.code` | string | stable | Stable snake_case code; see `docs/stable-error-codes.md` |
| `error.severity` | string | stable | Values: error, warning |
| `error.message` | string | stable | Human-readable; do not parse |
| `error.remediation` | string | stable | May be empty |
| `error.context` | object | beta | Key/value pairs; keys may change |

---

## 3. Session format

Session files are written to the OS-specific user-data directory and keyed by
transaction hash. The format is versioned via a `schema_version` field.

| Surface | Stability | Notes |
|---------|-----------|-------|
| Session file location | stable | Controlled by `GLASSBOX_SESSION_DIR` |
| Session `schema_version` field | stable | Bump triggers migration on load |
| Session `tx_hash` field | stable | |
| Session `network` field | stable | |
| Session `simulation_response` object | stable | |
| Session `viewer_state` object | beta | May gain fields; old fields tolerated |
| Session `viewer_state.event_filter` | beta | |
| Session `viewer_state.hide_std_lib` | beta | |
| `.gbdiag` bundle format | beta | ZIP with fixed manifest schema; internal layout may change |

Minor version bumps to `schema_version` are backwards-compatible (new fields,
tolerant deserialization). Major bumps require a migration codepath.

---

## 4. Go public API

Monitored by `internal/apicompat` (symbol-level) and `scripts/api-snapshot.sh`
(doc-level). Removals and renames fail CI automatically.

| Package | Stability | Snapshot file |
|---------|-----------|---------------|
| `internal/errors` | stable | `internal-errors.txt` |
| `internal/audit` | stable | `internal-audit.txt` |
| `internal/rpc` | stable | `internal-rpc.txt` |
| `internal/simulator` | stable | `internal-simulator.txt` |
| `internal/signer` | stable | `internal-signer.txt` |
| `internal/snapshot` | stable | `internal-snapshot.txt` |
| `internal/session` | stable | `internal-session.txt` |
| `internal/trace` | stable | `internal-trace.txt` |
| `internal/testhelpers` | beta | `internal-testhelpers.txt` |
| `internal/progress` | beta | not yet snapshotted |
| `internal/telemetry` | experimental | not snapshotted |
| `internal/termctx` | experimental | not snapshotted |

---

## 5. Schemas and exit codes

| Surface | Stability | Reference |
|---------|-----------|-----------|
| Exit code values (0, 2–6, 130, 143) | stable | `docs/exit-codes.md` |
| `ErstError` code strings | stable | `docs/stable-error-codes.md` |
| Progress event schema (`phase`, `status`, `error_code`) | stable | `docs/progress-events.md` |
| Progress event `meta` keys | beta | May gain keys; existing keys stable |
| JSON schema files in `docs/schema/` | stable | Snapshotted by `scripts/api-snapshot.sh` |
| `diagnostics.Manifest` schema | beta | `schema_version` field provides migration hook |

---

## 6. Deprecation and removal policy

### Deprecation window

| Stability level of surface | Minimum deprecation window before removal |
|---------------------------|------------------------------------------|
| stable | 2 minor releases (e.g. deprecated in 1.3 → earliest removal in 1.5) |
| beta | 1 minor release |
| experimental | No window required |

### Process for deprecating a surface

1. Add a `deprecated` entry to this matrix with the replacement and target
   removal version.
2. Add a runtime warning: CLI flags print to stderr on use; Go functions add a
   `// Deprecated:` godoc comment. JSON fields emit a `"_deprecated": true`
   sibling key in beta outputs.
3. Add a changelog fragment with `category = "cli"` (or relevant category) and
   note the deprecation in `summary`.
4. Add an entry to `MIGRATION_GUIDE.md` with before/after examples.

### Process for removing a deprecated surface

1. Verify the deprecation window has passed (check the `Since` and this matrix).
2. Update the stability level from `deprecated` to `removed` in this matrix.
3. Add a changelog fragment with `category = "breaking"`, `breaking = true`, and
   a `migration_note` pointing to `MIGRATION_GUIDE.md`.
4. The PR must include the updated API snapshot files (the CI snapshot check
   will fail until they are regenerated with `scripts/api-snapshot.sh generate`
   and `go test ./internal/apicompat/... -update`).

### Currently deprecated surfaces

| Surface | Type | Deprecated since | Removal target | Replacement |
|---------|------|-----------------|----------------|-------------|
| `--hsm-provider` | CLI flag | 0.3 | 2.0 | `--signing-provider pkcs11` |
| `GLASSBOX_SIGNER_TYPE` | Env var | 0.3 | 2.0 | `GLASSBOX_SIGNING_PROVIDER` |

---

## 7. Updating this matrix

This matrix must be updated in the same PR as any change to a listed surface.
The release checklist in `docs/command-development-guide.md` requires it.

Steps:

1. Add or update the row for the changed surface.
2. If adding a new stable surface, add it to the appropriate section.
3. If deprecating, follow the process in §6.
4. If the change affects the `affects` field of a changelog fragment, ensure
   the fragment lists this surface (`cli-flags`, `json-output`, `session-format`,
   `go-api`, `schema`, or `exit-codes`).
5. Run `make changelog-check` to confirm all fragments referencing this surface
   are valid.

---

## 8. Connection to API snapshot checks

The `TestAPICompatibility` test in `internal/apicompat/` and
`scripts/api-snapshot.sh check` enforce that snapshot files match the current
source. When this matrix shows a surface as **stable**, any divergence between
the snapshot and the current code is a CI failure that cannot be merged without
either reverting the change or regenerating the snapshot with an accompanying
migration note.

The relationship is:

```
PR changes a stable surface
  → snapshot diverges
  → CI fails (api_compat_test or api-snapshot.sh check)
  → Developer must either:
      (a) Revert the change, or
      (b) Regenerate snapshot + add deprecation entry here + add migration note
          + add changelog fragment with breaking=true
```
