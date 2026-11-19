# Viewer State Persistence

The interactive trace viewer (`glassbox debug`, trace viewing) remembers how
you left each trace: the current step, the active search query and match,
the event filter, and the stdlib-visibility toggle. Reopening the same trace
restores that state so you can pick up where you stopped.

## How it works

Viewer state is stored **outside the trace payload**, as a small versioned
JSON sidecar per trace:

```
~/.Glassbox/viewer_state/<trace-fingerprint>.json
```

```json
{
  "version": 1,
  "trace_fingerprint": "3f9a…",
  "tx_hash": "abc123…",
  "current_step": 42,
  "search_query": "require_auth",
  "current_match": 2,
  "event_filter": "contract_call",
  "hide_stdlib": true,
  "updated_at": "2026-07-24T12:00:00Z"
}
```

Trace files themselves are never modified.

### Keyed by content fingerprint, not transaction hash

The sidecar key is a SHA-256 fingerprint of the trace's semantic content
(transaction hash, step count, and each step's operation, event type,
contract, function, and error). Volatile fields such as timestamps and the
navigation cursor are excluded.

This means a **changed trace never inherits stale state**: if you re-fetch a
transaction and the resulting trace differs (for example after a contract
upgrade or an RPC change), its fingerprint differs, so the viewer starts
fresh instead of jumping to a step that no longer means the same thing.

### Robustness guarantees

- **Versioned schema** — each sidecar records its schema `version`. Files
  written by a newer Glassbox are ignored with a warning rather than
  misinterpreted.
- **Atomic writes** — state is written to a temporary file in the same
  directory and renamed into place. A crash mid-write can never leave a
  truncated sidecar.
- **Corruption tolerance** — an unreadable or corrupted sidecar is ignored
  with a warning; the viewer starts from defaults and overwrites the bad
  file on exit. Corruption is never a fatal error.
- **Read-only locations** — if the state directory is not writable, state
  writes are disabled for the session after a single warning. The viewer
  keeps working without persistence.
- **No executable content** — only plain display state is stored. On both
  save and load, strings are stripped of control characters (so a tampered
  sidecar cannot replay terminal escape sequences) and length-capped, and
  numeric fields are clamped to valid ranges. Nothing in the sidecar is ever
  interpreted as a path, command, or code.

## Resetting state

Inside the viewer:

```
reset        # clear persisted state for the current trace and restore defaults
reset all    # clear persisted state for every trace
```

You can also delete the sidecar directory manually; each file is independent.

## Relocating the state directory

Set `GLASSBOX_VIEWER_STATE_DIR` to store sidecars somewhere other than
`~/.Glassbox/viewer_state` (useful when the home directory is read-only or
shared):

```sh
GLASSBOX_VIEWER_STATE_DIR=/tmp/glassbox-state glassbox debug --tx <hash> …
```

## Compatibility

Older releases stored all viewer state in a single unversioned
`~/.Glassbox/viewer_state.json` keyed by transaction hash. That file is no
longer read (it cannot express the staleness guarantees above) and may be
deleted; state simply rebuilds as traces are viewed.
