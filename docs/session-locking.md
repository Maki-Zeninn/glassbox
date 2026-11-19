# Session Locking and Concurrency Failure Scenarios

This guide describes what Glassbox does, and does not do, when more than one
process or goroutine touches session state. Every statement here is traced
to code in `internal/session` and `internal/cmd/session.go`.

## Summary

Glassbox has **no cross-process session lock**. There is no lock file, no
`flock`, and no advisory lock. Coordination relies on three mechanisms:

| Mechanism | Scope | Location |
|---|---|---|
| SQLite WAL journal (`?_journal_mode=WAL`) | concurrent readers + one writer on `~/.Glassbox/sessions.db` | `NewStore` in `internal/session/store.go` |
| `sync.Mutex` / `sync.RWMutex` | goroutines **inside one process** only | `StateStore` (`state.go`), `stateMu` (`viewer_state_file.go`) |
| PID liveness probe on the checkpoint | decides whether `session recover` may take over a session | `Checkpoint.IsOrphaned` (`checkpoint.go`, `checkpoint_unix.go`, `checkpoint_windows.go`) |

The checkpoint is **not a lock**. It is a crash-recovery marker that
`session recover` reads, and nothing else consults it.

## State files under `~/.Glassbox`

| File | Written by | Protection |
|---|---|---|
| `sessions.db` (+ `-wal`, `-shm`) | `Store.Save`, `Store.Load` (re-save on upgrade, `last_access_at` update), `Store.Cleanup`, `Store.Delete` | SQLite; mode set to `0600` after open |
| `active_session.json` | `session.WriteCheckpoint` | none; plain `os.WriteFile(..., 0600)` |
| `viewer_state.json` | `session.SaveViewerState` | process-local `stateMu`; read-modify-write |

## The checkpoint and the liveness probe

`active_session.json` holds:

```json
{
  "session_id": "abc12345-1763500000",
  "tx_hash": "<64 hex chars>",
  "network": "testnet",
  "started_at": "2026-11-19T10:00:00Z",
  "pid": 41234
}
```

`IsOrphaned()` returns `true` when `pid <= 0` or the process is gone:

- **Unix:** `syscall.Kill(pid, 0)`. `EPERM` counts as **alive**, because the
  process exists but belongs to another user.
- **Windows:** `OpenProcess` + `GetExitCodeProcess`. The process is alive
  only when the exit code is `STILL_ACTIVE` (259). Any open failure,
  including access denied, counts as **not alive**.

> **Current gap:** `WriteCheckpoint` has no production caller. The only
> calls are in tests (`internal/session/checkpoint_test.go`,
> `internal/cmd/session_recovery_test.go`), and `glassbox debug` does not
> write a checkpoint. As of commit `0439a5e`, a normal run therefore never
> creates `active_session.json`. Every scenario below that needs a
> checkpoint applies to one created by tests, by hand, or by a future
> caller.

## Failure scenarios

Exit codes come from `internal/cmd/exitcode.go`. A plain error is `3`, and
errors wrapped with `errors.WrapValidationError` are `1`.

### 1. Recover while the owning process is still running

Checkpoint present, PID alive.

```text
$ glassbox session recover
Checkpoint found but the owning process (PID 41234) is still running.
Hint: use 'glassbox session list' to view active sessions,
      or wait for that process to exit before recovering.
Error: process PID 41234 is still alive — session abc12345-1763500000 cannot be recovered yet
```

Exit `3`. The checkpoint is **kept**. This is the only place Glassbox
refuses to act because another process "owns" a session. Covered by
`TestSessionRecover_LiveProcess_ReturnsError`.

### 2. Recover after the owner crashed (successful path)

Checkpoint present, PID gone, session row present and valid.

```text
$ glassbox session recover
Orphaned session checkpoint detected (PID 41234 no longer running).
  Session : abc12345-1763500000
  Tx Hash : <tx-hash>
  Network : testnet
  Started : 2026-11-19T10:00:00Z

Session recovered: abc12345-1763500000
Use 'glassbox session resume <id>' to re-enter the session,
or 'glassbox session list' to view all sessions.
```

Exit `0`. The row is re-saved with `status = recovered`, and the checkpoint
is removed.

### 3. PID reuse after a crash

If the OS reuses the crashed process's PID for an unrelated process, the
probe reports it as alive and recovery is refused as in scenario 1. The
probe compares only the PID, not the start time or executable. To clear
the condition, confirm the PID is not Glassbox and delete
`~/.Glassbox/active_session.json`.

### 4. Two processes write the checkpoint

`WriteCheckpoint` overwrites the file without checking it, and the last
writer wins. `ClearCheckpoint` from either process removes the marker for
both. A clean exit from process A can therefore delete the recovery marker
for a still-running process B.

### 5. Two processes save the same session ID

`Store.Save` is an `INSERT ... ON CONFLICT(id) DO UPDATE`, and the last
writer wins on every column except `created_at`. No version check or
compare-and-swap is done. SQLite serialises the two writes. A writer that
cannot get the database lock receives the driver's `database is locked`
(`SQLITE_BUSY`) error, wrapped as `failed to save session: ...`. No
`busy_timeout` is configured.

### 6. Two processes save different sessions with the same bookmark name

The unique partial index `idx_session_name` rejects the second write with
a `UNIQUE constraint failed` error wrapped as `failed to save session: ...`.
The first save is kept.

### 7. Resume or cleanup racing a save

`session resume` and `session save` both call
`Store.Cleanup(DefaultTTL, DefaultMaxSessions)`: a 30-day TTL and at most
1000 sessions, with the oldest removed first by `last_access_at`. Cleanup
can delete a row that another process is about to load. That process then
sees:

```text
session not found: <id>
  Fix: run 'glassbox session list' to see available sessions
```

Cleanup failures are printed as warnings and do not fail the command.

### 8. Concurrent viewer-state writes

`SaveViewerState` reads, merges, and rewrites `viewer_state.json` under
`stateMu`. The mutex is per process, so two processes can interleave and
the later write drops the other's update for a different transaction. A
corrupt file is ignored on save (best effort) but returned as an error by
`LoadViewerState`.

### 9. Malformed checkpoint

```text
$ glassbox session recover
Error: failed to read crash-recovery checkpoint: malformed checkpoint file: ...
Hint: the checkpoint file (~/.Glassbox/active_session.json) may be corrupt.
      Delete it manually to clear the error, then run 'glassbox debug' again.
```

Exit `3`. Here the file is **not** cleared automatically. In contrast, a
checkpoint that parses but fails `ValidateCheckpoint` (for example
`pid <= 0`, an empty `session_id`, or an unknown `network`) **is**
cleared, and the command exits `3`. Covered by
`TestSessionRecover_MalformedCheckpoint_ClearsAndErrors` and
`TestSessionRecover_MissingSessionID_ReportsIssueAndClears`.

## Operator guidance

- Do not run two Glassbox commands that save sessions at the same time
  against the same home directory if you depend on the result. Neither
  command detects the other.
- Before deleting `active_session.json` by hand, check its PID:
  `ps -p <pid>` (Unix) or `tasklist /FI "PID eq <pid>"` (Windows).
- For isolated runs (CI, tests), point `HOME` (`USERPROFILE` on Windows) at
  a temporary directory. Every path above is derived from
  `os.UserHomeDir()`. The tests use this approach.

## Verifying

```bash
go test ./internal/session/ -run 'Checkpoint' -v
go test ./internal/cmd/ -run 'TestSessionRecover' -v
```

Expected on a healthy tree: all listed tests pass.
`TestCheckpoint_IsOrphaned_CurrentProcess` asserts that the test's own PID
counts as alive, and `TestCheckpoint_IsOrphaned_DeadPID` asserts that an
exited PID counts as orphaned.

**Pre-existing baseline failure (at commit `0439a5e`):** both commands fail
to compile because `internal/session/store.go` has a syntax error at line
187. `internal/cmd` also depends on `internal/sourcemap` (duplicate
`DiscoverLocalSymbolsLegacy`) and `internal/signer` (syntax error in
`pkcs11_validator.go`), which fail too. See the
[migration guide](schema/session-migration-guide.md#verifying).

## Related

- [Session Recovery Decision Guide](session-recovery-decision.md)
- [Session Bookmarking](session-bookmarking.md)
