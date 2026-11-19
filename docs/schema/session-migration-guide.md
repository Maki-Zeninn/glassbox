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
