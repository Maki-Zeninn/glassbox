# Snapshot Integrity

Glassbox snapshots are JSON files that bundle ledger state and replay
metadata. A snapshot can be truncated or modified after capture, causing
replay results that are hard to trust. The integrity system gives you an
offline check that works without signing keys.

## Integrity vs. authenticity

| Property | What it guarantees | How to get it |
|---|---|---|
| **Integrity** | Content has not changed since it was saved | `glassbox snapshot verify` / `VerifyIntegrityFull` |
| **Authenticity** | Content was produced by a specific party | `glassbox audit sign` / `glassbox audit verify` |

Hashing detects accidents (disk corruption, partial writes, unintended
edits). It does **not** prove who created the file. For authenticity use
the audit signing commands.

## How hashes are computed

Every registry entry stores a `content_hash` field alongside the existing
`checksum`. The content hash is a SHA-256 digest of the canonical JSON
encoding of the entry's `(timestamp, snapshot)` pair.

**Canonical JSON rules** (reproducible across platforms and Go versions):

- Object keys are sorted lexicographically.
- No extra whitespace — no indentation, no trailing newlines.
- HTML-unsafe characters (`<`, `>`, `&`) are **not** escaped.
- Ledger entries are sorted by key before hashing.

This means the same snapshot state always produces the same hash regardless
of key insertion order or the Go version used to marshal it.

The algorithm name is stored as `sha256-canonical-json` and exposed via
`replay.IntegrityAlgorithm` so diagnostics and CI scripts can reference it.

## JSON diagnostics output

`glassbox snapshot verify --json` emits a machine-readable report:

```json
{
  "path": "./registry.json",
  "algorithm": "sha256-canonical-json",
  "passed": true,
  "total_entries": 3,
  "ok": 3,
  "legacy": 0,
  "tampered": 0,
  "error": 0,
  "entries": [
    {
      "index": 0,
      "timestamp": 1700000000,
      "status": "ok",
      "computed_hash": "a3f2..."
    }
  ]
}
```

## Legacy snapshots

Registries saved before this feature was introduced have `content_hash: ""`
in each entry. On load these entries are reported as **legacy** — not
failures. Their hashes are computed and back-filled in memory.

**Legacy entries are visible in JSON diagnostics** (`"status": "legacy"`)
so you know the file pre-dates the feature.

To persist the back-filled hashes, run:

```sh
glassbox snapshot repair --path ./registry.json
```

Repair is safe:

- Entries already have a valid `content_hash` → untouched.
- Entries with a **mismatch** (tampered) → repair **aborts** with a
  non-zero exit code. Investigate before overwriting.
- Entries with no hash (legacy) → hash back-filled and file rewritten
  atomically.

Use `--dry-run` to preview what would change.

## CLI reference

### `glassbox snapshot verify`

```
glassbox snapshot verify --path <registry.json> [--json]
```

| Flag | Description |
|---|---|
| `--path` | Path to the registry JSON file |
| `--json` | Emit machine-readable JSON diagnostics to stdout |

Exit codes: `0` = OK or legacy only; `1` = tampered or error entries.

### `glassbox snapshot repair`

```
glassbox snapshot repair --path <registry.json> [--dry-run]
```

| Flag | Description |
|---|---|
| `--path` | Path to the registry JSON file to repair |
| `--dry-run` | Preview back-fills without writing any files |

## Implementation locations

| File | Purpose |
|---|---|
| `internal/replay/integrity.go` | `EntryContentHash`, `VerifyIntegrityFull`, canonical JSON encoder |
| `internal/replay/registry.go` | `Entry.ContentHash` field, `Registry.Add` populates both hashes |
| `internal/cmd/snapshot_integrity.go` | `glassbox snapshot verify` and `glassbox snapshot repair` |
| `internal/replay/integrity_test.go` | Tamper fixtures, field-ordering, legacy back-fill, cross-platform tests |
