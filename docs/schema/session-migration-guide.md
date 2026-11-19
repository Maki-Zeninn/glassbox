# Session Schema Migration Guide

This guide explains how Glassbox versions the rows in the session store
(`~/.Glassbox/sessions.db`). It covers what happens when a binary loads a row
written by an older or newer release, and what a "schema v4 / tags" row means
for the current binary.

Sources of truth:

| Concern | Location |
|---|---|
| Current version constant | `SchemaVersion` in `internal/session/store.go` |
| Oldest loadable version | `MinSupportedSchemaVersion` in `internal/session/schema.go` |
| Classification and messages | `classifySchemaVersion` in `internal/session/schema.go` |
| In-memory upgrade | `UpgradeSessionData` in `internal/session/schema.go` |
| Column backfill on open | `initSchema` / `ensureColumn` in `internal/session/store.go` |
| Tests | `internal/session/schema_test.go` |

## Current versions

| Constant | Value |
|---|---|
| `SchemaVersion` | `2` |
| `MinSupportedSchemaVersion` | `1` |

> **There is no schema v3 or v4 in this repository, and no `tags` field.**
> The `Data` struct and the `sessions` table have no tag column. Nothing
> referenced as `changelog/fragments/1060-session-schema-v4-tags.toml` exists
> (the repository has no `changelog/` directory). Until a v4 migration
> lands, a row stamped `schema_version = 4` is a **future** row to this
> binary. It is rejected as described under
> [Rows from a newer binary](#rows-from-a-newer-binary-v3-v4-).

## How a stored version is classified

Every load path runs the row's `schema_version` through
`classifySchemaVersion`. The result decides whether the row loads:

| Stored version | Classification | Loads? | Message (verbatim from `schema.go`) |
|---|---|---|---|
| `== 2` | current | yes | `session schema version 2 is current — no upgrade needed` |
| `1` | outdated (`NeedsUpgrade`) | yes, upgraded on load | `session schema version 1 is outdated (current: 2); Glassbox will upgrade the session automatically on load` |
| `< 1` (e.g. `0`) | too old (`Unsupported`) | no | `session schema version 0 is too old to load (minimum supported: 1, current: 2); re-run 'glassbox debug <tx-hash>' to recreate the session` |
| `> 2` (e.g. `4`) | from future (`FromFuture`, `Unsupported`) | no | `session schema version 4 was produced by a newer version of Glassbox (this binary supports up to 2); upgrade Glassbox to resume this session, or re-run 'glassbox debug <tx-hash>' with the current binary` |

Rejected rows surface as a `*session.SchemaError`. Its `Error()` prefixes the
message with the session ID: `session "<id>": <message>`.

## What the upgrade actually does

Migration runs in two layers.

1. **Column backfill when the store opens** (`initSchema`). Columns added
   after the original table are created with `ALTER TABLE ... ADD COLUMN`
   when they are missing: `name`, `pinned_endpoint`, `env_fingerprint`,
   `audit_hash`, `audit_signature`, `previous_session_hash`. Existing values are
   never rewritten. The unique partial index `idx_session_name` is created at
   the same time.
2. **Row upgrade when a row loads** (`UpgradeSessionData`). For an outdated row:
   - a v0 → v1 step fills an empty `env_fingerprint` with
     `BuildEnvFingerprint()`;
   - `schema_version` is set to `SchemaVersion`;
   - `Store.Load` then re-saves the row, so the upgrade happens once.

   Rows from a newer binary are never modified.

Note that the v0 → v1 branch in `UpgradeSessionData` cannot be reached
today. Version `0` is below `MinSupportedSchemaVersion`, so
`classifySchemaVersion` rejects it before the branch runs.
`TestClassifySchemaVersion_OldUnsupportedVersion_MarksUnsupported` asserts
that rejection. A v1 row is upgraded by the version bump alone.

## Where each path checks the version

| Path | Check | Upgrade persisted? |
|---|---|---|
| `glassbox session resume <id-or-name>` (alias `load`) | `Store.Load` → `ValidateSchemaVersion` + `UpgradeSessionData` | yes (re-saved) |
| `glassbox session recover` | `Store.Load` (same as above) | yes |
| `glassbox session doctor` | `ValidateIntegrity` only; reports, never upgrades | no |
| Archive import (`session.ImportArchive`) | rejects `meta.json` `schema_version > 2` | no (in memory only) |

## Rows from a newer binary (v3, v4, …)

When an older binary meets a row written by a newer one, for example a
future v4 release that adds tags:

```text
$ glassbox session resume abc12345-1763500000
Error: session "abc12345-1763500000": session schema version 4 was produced by a newer version of Glassbox (this binary supports up to 2); upgrade Glassbox to resume this session, or re-run 'glassbox debug <tx-hash>' with the current binary
```

Exit code: `3`. The error is a plain `*SchemaError`, which
`internal/cmd/exitcode.go` maps to `ExitInternalError`.

`glassbox session doctor` reports the same row as degraded and does not change
it. See [Session Doctor Examples](../session-doctor-examples.md).

**Remediation**, in order of preference:

1. Upgrade Glassbox to the release that wrote the row.
2. Re-run `glassbox debug <tx-hash> --network <network>` with the current
   binary to create a fresh v2 session.
3. Remove the row with `glassbox session delete <session-id>`.

## Checklist for adding the next schema version

A future change (for example, adding tags) must update all of the following
together:

- [ ] Bump `SchemaVersion` in `internal/session/store.go`.
- [ ] Add the column in the `CREATE TABLE` statement **and** an
      `ensureColumn` call in `initSchema` for existing databases.
- [ ] Add the field to `Data` with a stable `json` tag (archives serialise
      `Data` directly).
- [ ] Add the column to the `INSERT`/`SELECT` lists in `Save`,
      `SavePreservingSchemaVersion`, `Load`, and `List`.
- [ ] Add a version step to `UpgradeSessionData` if old rows need a
      default value.
- [ ] Extend `internal/session/schema_test.go`. Tests derived from
      `SchemaVersion - 1` follow the bump automatically.
- [ ] Update the table in this guide.

## Verifying

```bash
go test ./internal/session/ -run 'Schema|UpgradeSessionData' -v
```

Expected on a healthy tree: all `TestClassifySchemaVersion_*`,
`TestValidateSchemaVersion_*`, `TestUpgradeSessionData_*`, and
`TestSchemaVersionSummary_*` tests pass.

**Pre-existing baseline failure (at commit `0439a5e`):** the package does
not compile, so this command fails before any test runs:

```text
internal/session/store.go:187:44: syntax error: unexpected name string in argument list; possibly missing comma or )
```

The cause is an unclosed `if` block in `initSchema` (around line 182).
Additional problems in the same file will surface once that is fixed:
`Load` passes `Data` instead of `*Data` to `UpgradeSessionData`, and the
`INSERT` in `Save` lists 20 columns but 17 placeholders. These are not
caused by, and are not fixed by, this documentation change.

## Related

- [Session Bookmarking](../session-bookmarking.md)
- [Portable Session Bundles](../session-portability.md)
- [Session Doctor Examples](../session-doctor-examples.md)
This guide covers migration between major session schema versions in Glassbox.
Session data is stored in an embedded SQLite database managed by `internal/session`.

## Version History

| Version | Introduced | Changes |
|---------|-----------|---------|
| v0 | Initial release | Base session schema (no env_fingerprint) |
| v1 | Environment binding | Added `EnvFingerprint` and `PinnedEndpoint` fields |
| v2 (current) | Current | Provenance tracking, encryption, integrity checks |

## Version Detection

Glassbox automatically detects the schema version of a stored session on load.
The detection logic lives in `internal/session/schema.go` and classifies sessions
into one of four categories:

| Category | Meaning | Action |
|----------|---------|--------|
| **Current** | `StoredVersion == SchemaVersion` | No action needed |
| **NeedsUpgrade** | `MinSupportedSchemaVersion <= StoredVersion < SchemaVersion` | Automatic upgrade on load |
| **Unsupported (too old)** | `StoredVersion < MinSupportedSchemaVersion` | Re-run `glassbox debug` to recreate |
| **Unsupported (future)** | `StoredVersion > SchemaVersion` | Upgrade Glassbox binary |

You can detect the schema version programmatically:

```go
import "github.com/dotandev/glassbox/internal/session"

result := session.SchemaVersionSummary(storedVersion)
// "session schema version 1 is outdated (current: 2); Glassbox will upgrade the session automatically on load"
```

Or via CLI:

```bash
glassbox session:info --session <session-id>
```

## v0 → v1: Environment Binding

**Breaking:** No — the upgrade is automatic and adds an `EnvFingerprint` field.

**What changed:**
- Sessions gained an `EnvFingerprint` field that captures the host environment
  at debug time (OS, Go version, Stellar SDK version, network passphrase).
- Sessions gained a `PinnedEndpoint` field to record the RPC endpoint used.

**Migration behavior:**
When Glassbox loads a v0 session, `UpgradeSessionData()` in
`internal/session/schema.go:129` automatically:
1. Generates an `EnvFingerprint` from the current environment
2. Sets `SchemaVersion` to the current value (2)
3. Records a provenance entry marking the migration

**Rollback:** None needed — v0 sessions remain readable by older Glassbox
versions until the schema version field is updated (which only happens on write).

**Compatibility timeline:**
- v0 sessions can be loaded by any Glassbox version that supports
  `MinSupportedSchemaVersion <= 0`. Currently all versions support v0.
- Future versions may raise `MinSupportedSchemaVersion` to 1, at which point
  v0 sessions must be re-created via `glassbox debug <tx-hash>`.

## v1 → v2: Provenance and Encryption

**Breaking:** No — the upgrade is automatic.

**What changed:**
- Sessions gained encryption-at-rest support (AES-256-GCM via `session_key`).
- Provenance timeline tracking was added to record every mutation.
- Integrity checks (HMAC) were added to detect tampering.

**Migration behavior:**
When Glassbox loads a v1 session, `UpgradeSessionData()`:
1. Bumps `SchemaVersion` to 2
2. Records a provenance entry
3. Does not retroactively encrypt (encryption applies to new writes)

**Rollback:** v2 sessions can be read by v1 Glassbox binaries if they were
not encrypted. Encrypted sessions require a v2+ binary.

**Compatibility timeline:**
- v1 sessions without encryption remain readable by older binaries.
- Encrypted v1-upgraded sessions require Glassbox v2+.

## Manual Rollback

If you need to revert a session to a previous schema version:

```bash
# Export session data before migration
glassbox session:export --session <id> --output backup.json

# Re-create the session from scratch (generates current schema)
glassbox debug <tx-hash> --network <network>

# Or restore from backup (if you have a v1 backup)
glassbox session:import --input backup.json
```

## Troubleshooting

### "session schema version X is too old to load"

The session predates `MinSupportedSchemaVersion`. Re-create it:

```bash
glassbox debug <tx-hash> --network <network>
```

### "session schema version X was produced by a newer version"

Upgrade Glassbox:

```bash
# Via go install
go install github.com/dotandev/glassbox/cmd/glassbox@latest

# Via release binary
# Download from https://github.com/pugsley76/glassbox/releases
```

### "Glassbox will upgrade the session automatically on load"

This is informational. The session will be upgraded transparently on first use.
No action required.
