# audit:verify example assets

Synthetic, deterministic inputs for the walkthrough in
[`docs/audit-verify-command.md`](../../audit-verify-command.md#end-to-end-local-workflow).

- `payload.json` — unsigned payload with the fixed timestamp `2026-01-01T00:00:00.000Z`
- `payload-schema.json` — schema accepted by `audit:verify --schema`

No key material is stored here. The walkthrough generates throwaway test keys
in a temporary directory with `glassbox audit:keygen`.
