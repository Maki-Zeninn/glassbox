# Session Fixtures

Session record JSON files and SQLite schema dumps for session-layer regression
tests.

## File naming

```
session_<scenario-slug>_<issue-or-pr-slug>.session.json
session_<scenario-slug>_<issue-or-pr-slug>.db.sql      # schema + seed SQL
```

Examples:
- `session_missing_txhash_issue230.session.json`
- `session_legacy_schema_v1_migration.session.json`
- `session_lock_conflict_issue813.db.sql`

## Format

A `.session.json` file is a single JSON object matching `session.Data`.
Required fields:

```json
{
  "id": "sess_test_5c0a1234",
  "tx_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "network": "testnet",
  "status": "active",
  "created_at": "2026-01-01T00:00:00Z",
  "last_access_at": "2026-01-01T00:00:00Z",
  "envelope_xdr": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
  "result_meta_xdr": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
  "schema_version": 3
}
```

## Rules

- Use `sess_test_` as the session ID prefix.
- `schema_version` must match `session.SchemaVersion` unless the test
  explicitly exercises schema migration (use `session.MinSupportedSchemaVersion`
  for the oldest supported version).
- No real transaction hashes, wallet keys, or HorizonURL credentials.
- Timestamps must use `2026-01-01T00:00:00Z` unless the test exercises
  timestamp ordering (e.g. the `session_timestamps_out_of_order` class).
