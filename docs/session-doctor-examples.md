# Session Doctor: Before-and-After Examples

`glassbox session doctor` scans persisted sessions in
`~/.Glassbox/sessions.db` and reports schema and integrity problems. It is
**read-only**: it never upgrades, repairs, or deletes a row. Implementation:
`sessionDoctorCmd` in `internal/cmd/session.go`, `Store.RunStoreDiagnostics`
and `ValidateIntegrity` in `internal/session/store.go`.

This is different from top-level `glassbox doctor`, which checks the
development environment (Go, Rust, simulator binary, config, RPC) and
accepts `--fix`. `session doctor` takes no flags and no arguments.

## What is checked

`ValidateIntegrity` runs on each row. Issues are reported in this order:

| Field label | Problem |
|---|---|
| `ID` | empty |
| `TxHash` | empty, or not 64 characters |
| `Network` | empty, or not `testnet` / `mainnet` / `futurenet` |
| `Status` | empty, or not `active` / `saved` / `resumed` / `recovered` / `expired` |
| `AuditHash`, `PreviousSessionHash` | not 64-character hex |
| `AuditSignature` | not 128-character hex; hash/signature not paired; chain link without own hash or signature |
| `PreviousSessionHash` | equals the session's own `audit_hash` (self-reference) |
| `CreatedAt`, `LastAccessAt` | zero, or `last_access_at` before `created_at` |
| `SchemaVersion` | greater than `2` (newer binary) or less than `1` (too old) |
| `Name` | longer than 128 characters |
| `EnvelopeXdr` | empty while `sim_request_json` is set |

A row with schema version `1` is **not** an issue. It is upgraded the next
time it is loaded. See the
[migration guide](schema/session-migration-guide.md).

## Exit codes

| Outcome | Exit |
|---|---|
| No sessions, or all healthy | `0` |
| One or more degraded sessions (`N session(s) failed diagnostics`) | `3` |
| Store cannot be opened (wrapped validation error) | `1` |
| Store cannot be listed | `3` |

These come from `internal/cmd/exitcode.go`. Degraded sessions return a
plain error, which maps to `ExitInternalError`.

## Example 1: empty store

```text
$ glassbox session doctor
Session store: no sessions found.
```

Exit `0`.

## Example 2: session from a newer binary (before → after)

**Before.** The store holds two rows. One was written by a newer Glassbox
release with `schema_version = 3`.

```text
$ glassbox session doctor
Session store: 2 session(s), 1 healthy, 1 degraded — run 'glassbox session list' for details.

Degraded sessions (1):

Session abc12345-1763500000:
  1. [SchemaVersion] session schema version 3 is newer than the supported version 2
     Hint: Upgrade Glassbox to a newer release to open sessions created by a more recent version.
  Schema: session schema version 3 was produced by a newer version of Glassbox (this binary supports up to 2); upgrade Glassbox to resume this session, or re-run 'glassbox debug <tx-hash>' with the current binary

Error: 1 session(s) failed diagnostics
```

Exit `3`. The `Schema:` line appears only when the stored version is
non-zero and differs from `2`.

**Fix.** Upgrade Glassbox, or replace the row:

```bash
glassbox session delete abc12345-1763500000
glassbox debug <tx-hash> --network testnet
glassbox session save
```

**After:**

```text
$ glassbox session doctor
Session store: 2 session(s) — all healthy.
```

Exit `0`.

## Example 3: broken audit chain (before → after)

**Before.** A row has `audit_hash` set but no `audit_signature`.

```text
$ glassbox session doctor
Session store: 1 session(s), 0 healthy, 1 degraded — run 'glassbox session list' for details.

Degraded sessions (1):

Session def67890-1763600000:
  1. [AuditSignature] audit_hash is present but audit_signature is missing
     Hint: Persisted audit-chain state must sign audit_hash. Re-sign the session before saving it.

Error: 1 session(s) failed diagnostics
```

Exit `3`. No `Schema:` line is printed because the row is at version `2`.

**Fix.** `session save` rejects this state, so it cannot be corrected in
place from the CLI. Delete the row and re-create the session as in
Example 2.

**After:** `Session store: 1 session(s) — all healthy.`, exit `0`.

## Limits

- **Only the 50 most recently accessed sessions are scanned.**
  `RunStoreDiagnostics` calls `List(ctx, 0)`, and `List` treats `0` as its
  default limit of `50`. Older rows are not counted in the summary.
- `session doctor` does not look at the crash-recovery checkpoint. Use
  `glassbox session recover` for that (see the
  [recovery decision guide](session-recovery-decision.md)).
- **Known gap (commit `0439a5e`):** `Store.List` scans `created_at` and
  `last_access_at` into strings but never parses them into `Data`. When
  the package builds, every row would also report `[CreatedAt]` and
  `[LastAccessAt]` zero-timestamp issues. The examples above show the
  intended output. Treat extra timestamp issues on otherwise healthy rows
  as this defect, not as data corruption.

## Verifying

```bash
go test ./internal/session/ -run 'StoreDiagnostics' -v
go test ./internal/cmd/ -run 'TestSessionDoctorCmd' -v
```

Expected on a healthy tree: `TestRunStoreDiagnostics_*` and
`TestStoreDiagnosticsResult_Summary_*` pass. They pin the summary strings
used above.

**Pre-existing baseline failure:** neither package compiles at commit
`0439a5e`. See the
[migration guide](schema/session-migration-guide.md#verifying).

## Related

- [Session Recovery Decision Guide](session-recovery-decision.md)
- [Session Schema Migration Guide](schema/session-migration-guide.md)
- [Session Bookmarking](session-bookmarking.md)
