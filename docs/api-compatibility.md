# API Compatibility Checks

**Issue #597 implementation.**

Glassbox protects consumers from accidental breaking changes by generating
deterministic snapshots of its public API surface and failing CI when those
snapshots diverge.

## What is covered

| Artifact | Tool | Snapshot location |
|---|---|---|
| Go exported symbols (types, funcs, consts) | `internal/apicompat` | `internal/apicompat/testdata/api-snapshots/*.txt` |
| TypeScript exports from `src/index.ts` | `scripts/api-snapshot.sh` | `.api-snapshots/ts-main.txt` |
| TypeScript exports from `src/audit/browser/index.ts` | `scripts/api-snapshot.sh` | `.api-snapshots/ts-browser.txt` |
| CLI `--help` output for each sub-command | `scripts/api-snapshot.sh` | `.api-snapshots/cli-*.txt` |
| JSON schema files (`docs/schema/`) | `scripts/api-snapshot.sh` | `.api-snapshots/schemas.txt` |

Private implementation details (unexported Go symbols, `__tests__` helpers,
internal command-building helpers) are deliberately excluded.

## Running the checks locally

### Go symbol check

```bash
# Check (exits 1 on regression):
go test ./internal/apicompat/...

# Regenerate after intentional change:
go test ./internal/apicompat/... -update
```

### TypeScript / CLI / Schema check

```bash
# Generate snapshots (first time or after intentional change):
scripts/api-snapshot.sh generate

# Check:
scripts/api-snapshot.sh check
```

## CI pipeline

`scripts/api-snapshot.sh check` is integrated into the CI workflow. It runs
after the build step so it has access to the compiled `glassbox` binary for
CLI snapshots.

## Updating snapshots after an intentional change

1. Make your change (add/remove/rename an export).
2. Run:
   ```bash
   scripts/api-snapshot.sh generate          # TypeScript, CLI, schemas
   go test ./internal/apicompat/... -update  # Go symbols
   ```
3. Review the diff:
   ```bash
   git diff .api-snapshots/ internal/apicompat/testdata/
   ```
4. Write a **migration note** in your PR description explaining what changed
   and what consumers need to do (if anything).
5. Commit the updated snapshot files alongside your code change.

## What counts as a breaking change

Removals and renames of exported symbols are always flagged as failures by
`TestAPICompatibility`. Additions are logged informally (non-breaking).

For TypeScript and CLI, any diff in the snapshot is reported; the developer
decides whether it is intentional and accepts it by regenerating.

## Determinism guarantee

Snapshots are byte-for-byte identical across runs because:
- Go symbol extraction uses `sort.Strings` on identifier names.
- TypeScript export lines are extracted with `sort` in the shell script.
- CLI help text is normalised to strip version strings.
- Schema checksums use `sha256sum` with sorted file paths.

---

## Snapshot file compatibility policy

Snapshot files (`.snap.json`) persist across releases and may be shared
between machines and team members. The following policy governs how Glassbox
handles version differences between the binary and the file on disk.

### Version table

| Schema version | What changed | Status |
|---|---|---|
| v1 | Original format: ledger entries, linear memory, fingerprint | Supported (auto-migrated to v2 on load) |
| v2 | Added `ledger_format` field and `migration_log` in metadata | Current |

### Supported-version rules

| Stored version | Binary behaviour |
|---|---|
| `< MinSupportedSchemaVersion` | Hard error before any mutation — file too old |
| `>= Min` and `< Current` | Auto-migrated in memory on `LoadPersisted`; call `SavePersisted` to persist |
| `== Current` | Loaded without change |
| `> Current` | Hard error before any mutation — produced by a newer binary; upgrade Glassbox |

`MinSupportedSchemaVersion` is defined in `internal/snapshot/schema.go`.
`PersistSchemaVersion` (the current version) is in `internal/snapshot/persist.go`.

### Migration guarantees

- **State semantics are preserved.** Every migration step is tested to ensure
  ledger entries decode identically before and after migration.
- **Mutations happen before validation.** `LoadPersisted` migrates then
  validates, so a migrated snapshot still passes fingerprint and identity checks.
- **Migration log is append-only.** Each step appends a `MigrationLogEntry`
  to `metadata.migration_log` so you can audit what was changed and when.
- **Never mutates before rejecting.** An unsupported version produces an error
  with a clear remediation hint; no fields are written.

### Snapshot-diff version output

`glassbox snapshot-diff` (and the underlying `DiffPersistedSnapshots` API)
reports the schema versions of both files and any migrations that were applied
during the diff:

```
Schema versions:    base=v1  target=v2
  (migration) base: add ledger_format field (default base64-xdr) (v1 → v2)
Snapshot diff: 1 added, 0 removed, 2 modified
...
```

### Adding a new schema version

1. Increment `PersistSchemaVersion` in `internal/snapshot/persist.go`.
2. Append a `MigrationStep` to `migrationTable` in `internal/snapshot/migration.go`.
3. Update `MinSupportedSchemaVersion` in `schema.go` if the old version is
   no longer worth migrating (rare — prefer keeping the migration).
4. Write a test in `internal/snapshot/migration_test.go` that:
   - Round-trips a v(N-1) fixture through `LoadPersisted`.
   - Asserts the new field is present after migration.
   - Asserts ledger state is byte-identical.
5. Update this table.

### Backward compatibility for existing fixtures

Fixtures in `internal/snapshot/testdata/` are version-tagged. New migrations
must not break existing fixture loads. The `TestBackwardCompat_V1Fixture_LoadsAndMigrates`
test enforces this for every supported older version.
