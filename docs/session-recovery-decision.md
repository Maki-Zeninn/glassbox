# Session Recovery Decision Guide

Use this guide to choose the right command when a saved or interrupted
session misbehaves. All behaviour is from `internal/cmd/session.go`.

## Which command?

| Symptom | Run | Changes state? |
|---|---|---|
| "Is anything wrong with my saved sessions?" | `glassbox session doctor` | no |
| A previous Glassbox process crashed or was killed mid-session | `glassbox session recover` | yes: re-saves the row as `recovered` and clears the checkpoint |
| `session resume` fails with an integrity error | `glassbox session delete <id>`, then `glassbox debug <tx-hash> --network <net>` | yes |
| `session resume` fails with a schema version error | upgrade Glassbox, or delete and re-debug | yes |
| `session resume` says not found | `glassbox session list` | no |

## `session recover` outcomes

| Checkpoint state | Output (abridged) | Checkpoint afterwards | Exit |
|---|---|---|---|
| none | `No crash-recovery checkpoint found. Nothing to recover.` | — | `0` |
| unreadable JSON | `failed to read crash-recovery checkpoint: malformed checkpoint file: ...` | **kept**: delete `~/.Glassbox/active_session.json` by hand | `3` |
| parses but fails validation | `Checkpoint validation failed (N issue(s)):` + `Clearing corrupt checkpoint.` | cleared | `3` |
| owning PID still alive | `Checkpoint found but the owning process (PID N) is still running.` | kept | `3` |
| PID gone, session not in store | `Warning: session <id> not found in the store (...)` + re-debug hint | cleared | `0` |
| PID gone, session fails integrity | `Session integrity check FAILED for <id>:` + delete / re-debug hints | cleared | `3` |
| PID gone, session valid | `Session recovered: <id>` | cleared | `0` |

Each row has a matching test in `internal/cmd/session_recovery_test.go`
(`TestSessionRecover_*`).

> As of commit `0439a5e`, nothing in production writes the checkpoint
> (`WriteCheckpoint` is only called from tests), so in normal use
> `session recover` reports `Nothing to recover.` See
> [Session Locking](session-locking.md#the-checkpoint-and-the-liveness-probe).

## Decision flow

```text
session resume <id> fails?
├── "session schema version N was produced by a newer version"  → upgrade Glassbox  (or delete + re-debug)
├── "session schema version N is too old to load"               → delete + re-debug
├── "Session integrity check FAILED"                            → delete + re-debug
└── "not found"                                                 → session list; check the ID/name

Previous process crashed?
└── session recover
    ├── "still running"  → wait, or confirm the PID is not Glassbox and delete the checkpoint
    └── otherwise        → follow the printed hint

Unsure which sessions are affected?
└── session doctor   (read-only; see session-doctor-examples.md)
```

## Related

- [Session Doctor Examples](session-doctor-examples.md)
- [Session Locking](session-locking.md)
- [Session Schema Migration Guide](schema/session-migration-guide.md)
