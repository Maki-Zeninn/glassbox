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
# Session Locking and Concurrent-Writer Handling

Closes #813.

Two Glassbox processes saving the same session simultaneously can produce lost
annotations or corrupt metadata. This document describes the two-layer
concurrency policy that prevents silent overwrites.

---

## Overview

Glassbox protects session saves with:

1. **Advisory lock file** — a per-session `.lock` file in
   `~/.Glassbox/locks/<session-id>.lock` serialises concurrent writers.
2. **Optimistic revision check** — every session row carries a monotonically
   increasing `revision` counter. A writer that reads revision *N* and then
   saves is rejected if the on-disk revision is no longer *N*, which means
   another process saved in the meantime.

Read-only operations (`session list`, `session resume`) never acquire the
advisory lock and are never blocked.

---

## Advisory lock lifecycle

| Event | Action |
|-------|--------|
| Writer begins a save | `AcquireLock(sessionID)` — writes `~/.Glassbox/locks/<id>.lock` with `{pid, session_id, acquired_at}` |
| Write completes (success or error) | `LockHandle.Release()` — removes the lock file |
| Process crashes before release | Lock file remains on disk |
| Next writer finds the lock file | Checks whether the PID in the file is still alive |
| PID is alive | Returns `ErrLockHeld` — do not steal the lock |
| PID is dead **and** file is ≥ 5 minutes old | Removes the stale lock and proceeds |

Lock files are written atomically (`writeFileAtomic`) so a crash during lock
acquisition never leaves a truncated file.

### Stale-lock cleanup

`session doctor` calls `CleanStaleLocks()` which scans the locks directory and
removes any file whose owning PID is dead and whose age exceeds
`StaleLockAge` (5 minutes). The same cleanup runs automatically the next time
a writer tries to acquire the lock for a stale session.

---

## Optimistic revision check

Every `Data` record contains a `Revision int64` field (persisted in the
`sessions` table as a `NOT NULL DEFAULT 0` column for backwards compatibility).

| Scenario | Behaviour |
|----------|-----------|
| `data.Revision == 0` | No check — "new session or force" semantics |
| `data.Revision > 0` and disk revision equals `data.Revision` | Write proceeds; revision is incremented to `data.Revision + 1` |
| `data.Revision > 0` and disk revision **differs** | `Save` returns `*ConflictError` wrapping `ErrSessionConflict` |

### ConflictError

```go
type ConflictError struct {
    SessionID        string
    ExpectedRevision int64
    ActualRevision   int64
}
```

`errors.Is(err, session.ErrSessionConflict)` returns `true`.

The CLI surfaces this as:

```
Error: SESSION_WRITE_CONFLICT: session "abc123" write conflict: expected revision 2 but disk has revision 3
Hint:  Run 'glassbox session resume abc123' to reload the latest version and re-apply
       your changes, or re-run with --force to overwrite it.
```

Exit code: **1** (`ExitUserError`) — the user can recover without reinstalling.

---

## Force save

```bash
glassbox session save --force
```

`--force` calls `Store.SaveForce` which resets `data.Revision` to 0 before
calling `Save`, bypassing the optimistic check while still holding the
advisory lock.

Use `--force` only when you have inspected the conflict and consciously want
to overwrite the newer version.

---

## Stable error codes

| Code | Meaning | Exit |
|------|---------|------|
| `SESSION_WRITE_CONFLICT` | Concurrent writer saved a newer revision | 1 |
| `SESSION_LOCK_HELD` | Advisory lock held by a live process | 1 |

Both codes are registered in `internal/errors/glassbox_error_code.go` and map
to `ExitUserError` in `internal/cmd/exitcode.go`.

---

## Network filesystems (NFS, SMB, CIFS)

Advisory file locks are **not reliably enforced** by the kernel on network
filesystems. In such environments:

- The lock-file mechanism may not prevent concurrent access.
- The **optimistic revision check** at the SQLite layer is still effective,
  because SQLite WAL mode serialises concurrent writers at the database level.
- If two processes write to the same SQLite file over NFS simultaneously,
  SQLite may return a `SQLITE_BUSY` or `SQLITE_LOCKED` error; Glassbox will
  surface this as a generic "failed to save session" message rather than
  `SESSION_WRITE_CONFLICT`.

**Recommendation:** for shared team workflows, use a single Glassbox instance
per database file, or place the database on a local filesystem and synchronise
sessions via `glassbox session share` / `glassbox session import`.

---

## Troubleshooting lock symptoms

Closes #1172.

### Symptom: `SESSION_LOCK_HELD` error on save

```
Error: SESSION_LOCK_HELD: session "abc123" is locked by another process
Hint:  Another Glassbox instance is saving this session. Wait a moment and retry.
       If the other process has crashed, the lock clears automatically after 5 minutes.
```

**Safe first step — inspect without touching the lock:**

```bash
glassbox session lock:status <session-id>
```

Sample output when the holder is still alive:

```
Session "abc123": locked
  PID              : 18432
  Hostname         : worker-01
  Acquired at      : 2026-06-15T14:02:30Z
  Glassbox version : 1.4.0
  Status           : LIVE (process 18432 is running)
```

Sample output when the holder has crashed:

```
Session "abc123": locked
  PID              : 18432
  Hostname         : worker-01
  Acquired at      : 2026-06-15T14:02:30Z
  Glassbox version : 1.4.0
  Status           : STALE (process 18432 is no longer running)

Hint: run 'glassbox session recover' to remove the stale lock.
```

> **Warning:** do not manually delete a lock file in `~/.Glassbox/locks/` while
> the holder PID shown by `lock:status` is still alive. Doing so will break the
> advisory-lock protocol and can allow two writers to modify the session
> simultaneously, risking data loss or silent corruption. Only proceed to
> recovery steps when `lock:status` confirms `STALE`.

---

### Decision flow for a `SESSION_LOCK_HELD` error

```
SESSION_LOCK_HELD received
        │
        ▼
glassbox session lock:status <id>
        │
        ├─ LIVE  ──► Wait for the other process to finish, then retry.
        │             If it is stuck, stop that process normally first.
        │
        └─ STALE ──► glassbox session recover
                      (removes stale lock and restores the session)
```

---

### Symptom: `SESSION_WRITE_CONFLICT` error

```
Error: SESSION_WRITE_CONFLICT: session "abc123" write conflict: expected revision 2 but disk has revision 3
Hint:  Run 'glassbox session resume abc123' to reload the latest version and re-apply
       your changes, or re-run with --force to overwrite it.
```

Another process wrote a newer revision between your last load and this save.

| Option | Command | Effect |
|--------|---------|--------|
| Reload and re-apply | `glassbox session resume <id>` | Read-only load of newest version |
| Force-overwrite | `glassbox session save --force` | Discards the concurrent revision — destructive |

Prefer `session resume` unless you have inspected the conflict and are certain
the concurrent revision can be discarded.

---

### Symptom: stale lock found by `session doctor`

```
Removed 1 stale advisory lock file(s) from ~/.Glassbox/locks/
```

`session doctor` automatically removes locks whose owning PID is dead and whose
age exceeds 5 minutes (`StaleLockAge`). No manual action is required.

See [session recovery guidance](session-recovery-decision.md) for a full
symptom-to-command reference.

---

## Implementation reference

| Symbol | File | Description |
|--------|------|-------------|
| `AcquireLock` | `internal/session/lock.go` | Acquire advisory lock |
| `LockHandle.Release` | `internal/session/lock.go` | Release advisory lock |
| `CleanStaleLocks` | `internal/session/lock.go` | Remove dead-process locks |
| `ErrSessionConflict` | `internal/session/lock.go` | Conflict sentinel |
| `ConflictError` | `internal/session/lock.go` | Structured conflict error |
| `ErrLockHeld` | `internal/session/lock.go` | Lock-held sentinel |
| `LockHeldError` | `internal/session/lock.go` | Structured lock-held error |
| `Store.Save` | `internal/session/store.go` | Acquire lock + revision check + save |
| `Store.SaveForce` | `internal/session/store.go` | Force-overwrite (bypass revision check) |
| `Data.Revision` | `internal/session/store.go` | Monotonic revision counter |
| `ErstSessionConflict` | `internal/errors/glassbox_error_code.go` | Stable error code |
| `ErstSessionLockHeld` | `internal/errors/glassbox_error_code.go` | Stable error code |
