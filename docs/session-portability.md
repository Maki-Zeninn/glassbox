# Portable Session Bundles

A portable session bundle is a ZIP archive, conventionally with a `.gbx`
extension, that carries one debug session to another machine. It is
written by `session.ExportArchive` and read by `session.ImportArchive`
(`internal/session/archive.go`). The CLI entry points are in
`internal/cmd/share_session.go`.

## Bundle contents

| Entry | Always present | Content |
|---|---|---|
| `meta.json` | yes | Archive and compatibility metadata (below) |
| `session.json` | yes | The full `session.Data` record as indented JSON |
| `envelope.xdr` | only if `envelope_xdr` is non-empty | Raw copy of the envelope XDR string |
| `sim_response.json` | only if `sim_response_json` is non-empty | Raw copy of the simulator response JSON |

Import reads **only** `meta.json` and `session.json`. `envelope.xdr` and
`sim_response.json` duplicate fields that are already inside
`session.json`. They are there for people inspecting the archive with
`unzip`, and import ignores them. Any other entries are also ignored.
Source maps and trace JSON are **not** bundled.

### `meta.json`

```json
{
  "archive_version": 1,
  "glassbox_version": "dev",
  "created_at": "2026-11-19T10:00:00Z",
  "schema_version": 2
}
```

| Field | Meaning |
|---|---|
| `archive_version` | Layout version (`archiveVersion = 1`). Import rejects values greater than 1. |
| `glassbox_version` | `version.Version` of the exporting binary (`dev` for local builds). |
| `created_at` | Export time, UTC, RFC 3339. |
| `schema_version` | The exporting binary's `SchemaVersion` constant, **not** the row's own value. Import rejects values greater than the importer's `SchemaVersion`. |

### `session.json`

The JSON tags of `session.Data` (`internal/session/store.go`):

```json
{
  "id": "abc12345-1763500000",
  "name": "payroll-bug",
  "created_at": "2026-11-18T09:00:00Z",
  "last_access_at": "2026-11-19T10:00:00Z",
  "status": "saved",
  "network": "testnet",
  "horizon_url": "https://horizon-testnet.stellar.org",
  "tx_hash": "<64 hex chars>",
  "envelope_xdr": "<base64 XDR>",
  "result_xdr": "<base64 XDR>",
  "result_meta_xdr": "<base64 XDR>",
  "pinned_endpoint": "https://soroban-testnet.stellar.org",
  "audit_hash": "<64 hex chars, optional>",
  "audit_signature": "<128 hex chars, optional>",
  "previous_session_hash": "<64 hex chars, optional>",
  "sim_request_json": "<JSON string>",
  "sim_response_json": "<JSON string>",
  "GLASSBOX_version": "dev",
  "env_fingerprint": "<optional>",
  "schema_version": 2
}
```

Note the casing differences, which must be preserved: `meta.json` uses
`glassbox_version`, and `session.json` uses `GLASSBOX_version`. Fields with
`omitempty` (`name`, `pinned_endpoint`, `audit_*`, `previous_session_hash`,
`env_fingerprint`) are left out when empty.

### Privacy note

A bundle carries everything in the row, including the `horizon_url` and
`pinned_endpoint` URLs, the full transaction XDR, and the simulator
request and response. Review it before sharing. If a pinned endpoint URL
embeds an API key, the key is in the archive.

## Export: `glassbox session share`

```bash
glassbox session share                                   # active session (same process only)
glassbox session share <session-id-or-name>             # saved session
glassbox session share <id> --output ./debug-session.gbx
```

- `--output` / `-o` must end in `.gbx` or `.zip`, case-insensitive (see
  `SupportedArchiveExtensions`). Parent directories are created.
- The default name is `glassbox-session-<id>-<YYYYMMDD-HHMMSS>.gbx` in the
  current directory, using UTC time, with `/`, `\` and `:` in the ID
  replaced by `_`.
- The session must pass `ValidateIntegrity` before anything is written.

Successful output:

```text
Session exported: ./debug-session.gbx
  Session ID:  abc12345-1763500000
  Transaction: <tx-hash>
  Network:     testnet
  Archive:     ./debug-session.gbx (1234 bytes)

To load on another machine:
  Glassbox session load ./debug-session.gbx
```

Most important failure (bad extension), exit `3`:

```text
Error: failed to export session archive: unsupported archive extension ".tar" — must be one of: .gbx, .zip
  Fix: rename the output file with a supported extension
  Example: glassbox session share --output ./session.gbx
```

## Import

Validation happens in this order. The first failure stops the import:

1. Path is non-empty and ends in `.gbx` or `.zip`.
2. File opens as a ZIP.
3. `meta.json` and `session.json` both exist and decode.
4. `meta.archive_version <= 1`.
5. `meta.schema_version <= SchemaVersion` (2).
6. `ValidateIntegrity(session.json)` passes.

The CLI then re-checks `session.json`'s own `schema_version` and upgrades
an older row in memory (printing
`Session schema upgraded to version 2.`) before marking it
`status = resumed`.

Example failure, an archive from a newer release:

```text
session schema version 3 is newer than supported version 2
  Fix: upgrade Glassbox to the latest release to open this session
```

## Known contradictions (as of commit `0439a5e`)

These are documented as they are, not fixed here.

1. **`glassbox session load <archive>` does not reach the archive importer.**
   `session resume` declares the alias `load` (`internal/cmd/session.go`), and
   the archive command also has `Use: "load <archive>"`
   (`internal/cmd/share_session.go`). Cobra resolves the name to the first
   match registered, and `session.go`'s `init` runs first. Reproduced with
   cobra v1.10.2 in a minimal harness with the same registration order:
   `session load ./x.gbx` dispatched to `resume`. The argument is then
   looked up as a session ID or name and fails with `not found`. This
   matches [Session Bookmarking](session-bookmarking.md), which documents
   `session load` as an alias for `resume`.
2. **`load` followed by `save` does not persist across invocations.** The
   imported session is held in a process-global variable (`SetCurrentSession`).
   A later `glassbox session save` is a new process, so it reports
   `no active session to save`. The archive is never written to
   `sessions.db`.
3. **Help and output text uses `Glassbox session ...` (capital G)**. The
   binary is `glassbox`. Type the lowercase form.

Until (1) and (2) are fixed, a bundle can be inspected with any ZIP tool,
but it cannot be imported through the CLI.

## Inspecting a bundle without Glassbox

```bash
unzip -l ./debug-session.gbx
unzip -p ./debug-session.gbx meta.json
unzip -p ./debug-session.gbx session.json | jq '{id, tx_hash, network, schema_version}'
```

Expected `unzip -l` entries: `meta.json`, `session.json`, and, when those
fields are non-empty, `envelope.xdr` and `sim_response.json`.

## Verifying

```bash
go test ./internal/session/ -run 'Archive' -v
```

Expected on a healthy tree: `TestExportAndImportArchive` (round trip), the
`TestValidateArchivePath_*` tests, and the `TestImportArchive_*` failure
tests (missing `meta.json` or `session.json`, archive or schema version
too new, invalid data) all pass.

**Pre-existing baseline failure:** the package does not compile at commit
`0439a5e`. See the
[migration guide](schema/session-migration-guide.md#verifying).

## Related

- [Session Schema Migration Guide](schema/session-migration-guide.md)
- [Session Bookmarking](session-bookmarking.md)
# Session Export Portability

Closes #1171.

A session archive (`.gbx`) produced by `glassbox session share` is a ZIP file
containing `session.json`, `annotations.json`, and a signed `manifest.json`.
Not every field inside the archive travels between machines without
reconfiguration. This document identifies what is portable, what is
environment-specific, and how to handle each category when importing on a
different host.

---

## Portable fields

These fields are embedded in `session.json` and carry the same meaning on any
machine:

| Field | Notes |
|-------|-------|
| `id` | Stable session identifier; conflicts are resolved by `session import --on-conflict` |
| `tx_hash` | 64-char hex transaction identifier — fully portable |
| `network` | `testnet`, `mainnet`, or `futurenet` — portable |
| `status` | Lifecycle state (`saved`, `recovered`, etc.) — portable |
| `name` | Bookmark name; at most 128 characters — portable |
| `created_at` / `last_access_at` | UTC timestamps — portable |
| `envelope_xdr` / `result_xdr` / `result_meta_xdr` | Stellar XDR payloads — portable |
| `sim_request_json` / `sim_response_json` | Simulator I/O — portable |
| `audit_hash` / `audit_signature` / `previous_session_hash` | Audit chain — portable and verified on import |
| `schema_version` | Integer; see [schema compatibility](#schema-version-compatibility) |
| `provenance_json` | Append-only event log — portable; the importing host appends its own entry |
| `annotations` | Free-text notes stored in `annotations.json` — portable; `merge` policy combines them |
| `tags` | Short label array — portable |

---

## Environment-specific fields

These fields capture host-local configuration and may need reconfiguration after
a transfer:

| Field | Why it is local | What to do |
|-------|----------------|------------|
| `pinned_endpoint` | The RPC URL used on the exporting host | If the same endpoint is reachable, no action needed. Otherwise update with `glassbox session save --pin-endpoint <new-url>` after resuming |
| `env_fingerprint` | Captures OS, Go version, Stellar SDK version, and network passphrase at debug time | Informational only; not required for replay. Glassbox regenerates it on the first write after import |
| `horizon_url` | Auto-derived from `network` when not explicitly set | Automatically re-derived from `network` on import; no manual action needed unless you use a private Horizon |

---

## Transfer example

```bash
# On the exporting machine — share produces a .gbx archive
glassbox session share <session-id> --output payroll-bug.gbx

# Optionally preview conflicts before importing
glassbox session import payroll-bug.gbx --preview

# Import on the receiving machine (default: fail on conflict, non-destructive)
glassbox session import payroll-bug.gbx

# If the session ID already exists locally, rename it to keep both
glassbox session import payroll-bug.gbx --on-conflict rename

# Resume the imported session
glassbox session resume <session-id>
```

`--preview` is read-only and prints every conflicting field plus a portability
verdict without writing anything to disk.

---

## Redaction behavior

`glassbox session share` applies a redaction pass before packaging the archive.
By default the `full` profile is used, which removes or pseudonymizes:

- `env_metadata` (Horizon URL, endpoint) — removed
- `account_identifiers` — pseudonymized
- `contract_args` — removed if they contain secrets

Pass `--redact strict` to apply additional scrubbing, or `--redact none` to
export without redaction (not recommended for public sharing). The redaction
report is embedded in `manifest.json` so recipients can see exactly which
categories were sanitized.

See [`internal/session/redact.go`](../internal/session/redact.go) for the
complete field list.

---

## Schema version compatibility

The archive's `meta.json` records `schema_version` and `archive_version`.

| Scenario | Outcome |
|----------|---------|
| Importing host runs the same or newer Glassbox | Transparent automatic upgrade |
| Importing host runs an older Glassbox (archive schema too new) | Import rejected; upgrade Glassbox first |
| Session schema within the supported range | Auto-upgraded on first write |
| Session schema below `MinSupportedSchemaVersion` | Re-create with `glassbox debug <tx-hash>` |

Check compatibility before sharing with a team member on an older binary:

```bash
glassbox session import ./shared.gbx --preview
# Output includes: "Schema compatible: true"
```

For the full version history see
[docs/schema/session-migration-guide.md](schema/session-migration-guide.md).

---

## Related commands

| Command | Read-only? | Purpose |
|---------|-----------|---------|
| `glassbox session share` | No | Export a session as a `.gbx` archive |
| `glassbox session import --preview` | Yes | Preview conflicts without importing |
| `glassbox session import` | No | Import and persist to the local store |
| `glassbox session provenance <id>` | Yes | View the append-only provenance timeline |
| `glassbox session doctor` | Yes | Scan all local sessions for integrity issues |
