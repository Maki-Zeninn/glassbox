# Audit Fixtures

Audit payload files, signed log stubs, and TEST-ONLY keys for audit-layer
regression tests.

## File naming

```
audit_<scenario-slug>_<issue-or-pr-slug>.payload.json    # unsigned payload
audit_<scenario-slug>_<issue-or-pr-slug>.signed.json     # signed audit log
testonly_<key-type>.key.pem                              # test-only private key
testonly_<key-type>.pub.pem                              # test-only public key
```

Examples:
- `audit_empty_payload_rejected.payload.json`
- `audit_canonical_hash_deterministic.payload.json`
- `testonly_ed25519.key.pem`

## Rules

1. **Every key file must be prefixed `testonly_`** in both the filename and a
   comment at the top of the file.  These keys must never be used to sign
   production audit logs.

2. **Empty payload test**: the canonical empty-payload fixture is
   `audit_empty_payload_rejected.payload.json`.  It contains `{}` (a valid
   but semantically empty JSON object).  A completely blank string is tested
   in-memory and does not need a file fixture.

3. **Signed log stubs** contain synthetic (all-zero) signature bytes and are
   used only to verify structural parsing, not cryptographic correctness.

4. **Regeneration**: when the canonicalization algorithm or schema version
   changes, regenerate signed fixtures with the `_regenerate` key command:
   ```json
   { "_regenerate": "glassbox audit:sign --payload-file <path> --software-private-key testonly_ed25519.key.pem" }
   ```

## Canonical test payload

```json
{
  "_comment": "Canonical audit payload stub. No real data.",
  "event": "test_event",
  "timestamp": "2026-01-01T00:00:00Z",
  "tx_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "network": "testnet"
}
```
