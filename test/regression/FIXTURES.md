# Regression Test Fixture Guide

This document defines the naming conventions, directory layout, minimal
reproduction rules, and secret-avoidance requirements for all regression test
fixtures in `test/regression/fixtures/`.

---

## Directory layout

```
test/regression/
├── FIXTURES.md              ← you are here
├── fixture_index.json       ← auto-generated index (see scripts/index_regression_fixtures.sh)
├── fixtures/
│   ├── rpc/                 ← Stellar RPC response stubs (JSON)
│   ├── replay/              ← Snapshot registries and ledger-state files
│   ├── trace/               ← Serialised ExecutionTrace JSON files
│   ├── sourcemap/           ← Minimal WASM binaries and alias JSON files
│   ├── session/             ← Session record JSON and SQLite dumps
│   ├── audit/               ← Audit payloads, signed logs, and TEST-ONLY keys
│   └── cli/                 ← Expected CLI output fragments and env overrides
```

Each subdirectory has its own `README.md` with layer-specific rules.

### Fixture index

`fixture_index.json` is a deterministic, auto-generated catalogue of every
fixture file.  Regenerate it after adding or removing fixtures:

```bash
./scripts/index_regression_fixtures.sh
```

Run validation without regenerating (e.g. in CI):

```bash
./scripts/index_regression_fixtures.sh --check-only
```

The index records each fixture's `path`, `layer`, `failure_class`, `issue_ref`,
`schema_version`, and `test_name`.  CI fails if any fixture lacks a valid layer
or an issue/PR reference in its filename.

---

## Canonical stub values

These values are used consistently across all fixtures to prevent accidental
use of real network data.

| Field              | Stub value                                                         |
|--------------------|--------------------------------------------------------------------|
| Transaction hash   | `5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab` |
| Envelope XDR       | `AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`                     |
| Network            | `testnet`                                                          |
| Timestamp          | `2026-01-01T00:00:00Z`                                             |
| Session ID prefix  | `sess_test_`                                                       |
| Contract ID prefix | `CTEST`                                                            |

---

## Naming convention

Every fixture file follows:

```
<layer>_<scenario-slug>_<issue-or-pr-slug>.<ext>
```

Where:

- `<layer>` — one of: `rpc`, `replay`, `trace`, `sourcemap`, `session`,
  `audit`, `cli`
- `<scenario-slug>` — short snake_case description of the failure class being
  reproduced (e.g. `budget_exhaustion`, `missing_footprint`, `empty_events`)
- `<issue-or-pr-slug>` — optional GitHub issue / PR number or short slug that
  links the fixture to the original report (e.g. `issue234`, `pr_401`)
- `<ext>` — file extension appropriate for the content type

Examples:

```
rpc_gettransaction_notfound_issue150.json
trace_deprecated_host_fn_pr319.trace.json
audit_empty_payload_rejected.payload.json
cli_debug_invalid_hash_error_message.expected.txt
```

---

## Minimal reproduction rules

A fixture must demonstrate **the smallest possible input that triggers the
specific failure class** being tested.  Specifically:

1. **Include only the fields required to hit the code path under test.**
   Leave all other fields as zero-values or omit them.

2. **Use the canonical stub values above** unless the test explicitly exercises
   field validation (e.g. testing that an empty transaction hash is rejected).

3. **One fixture per failure class.**  If two bugs share an input shape but
   differ in which code path fails, create two separate fixtures with distinct
   scenario slugs.

4. **Document the original failure** in a comment at the top of the fixture
   file (JSON `_comment` key, shell `#` comment, or a companion `.md` file):

   ```json
   {
     "_comment": "Reproduces issue #234: GetTransaction returns NOT_FOUND but the CLI printed 'success'",
     "status": "NOT_FOUND",
     ...
   }
   ```

---

## Secret avoidance

Fixtures must **never** contain:

- Real private keys, PKCS#8 PEM blobs, HSM PINs, or AWS KMS key ARNs
- Real Stellar transaction hashes from mainnet or testnet
- Real contract IDs from deployed production contracts
- Bearer tokens, API keys, or RPC authentication credentials
- Personal data or internal infrastructure hostnames

Keys placed in `test/regression/fixtures/audit/` are **test-only** and must be
labeled `testonly_` in both the filename and the file content.

If a fixture accidentally contains sensitive data, rotate the affected
credential immediately and remove the fixture from git history before the
branch is merged.

---

## Linking a fixture to the issue workflow

When filing a GitHub issue that requires a regression test, include the
following section in the issue body:

```markdown
## Regression fixture

- **Fixture directory**: `test/regression/fixtures/<layer>/`
- **Suggested filename**: `<layer>_<scenario>_issue<N>.<ext>`
- **Failure class**: <one-line description of the class>
- **Canonical input**: paste a minimal JSON / shell snippet here
```

The fix PR must include both the fixture file and a test that loads it via the
helpers in `internal/testhelpers/`.  See
[docs/regression-test-guide.md](../../docs/regression-test-guide.md) for the
full contributor workflow.

---

## Regenerating fixtures

Some fixtures (particularly signed audit logs) must be regenerated when the
canonicalization algorithm or schema version changes.  A `# regenerate:`
comment in the fixture file records the exact command used:

```json
{
  "_regenerate": "glassbox audit:sign --payload-file fixtures/audit/canonical_hash_deterministic.payload.json --software-private-key fixtures/audit/testonly_ed25519.key.pem"
}
```

Run that command and replace the fixture with the new output.  Commit both
files together so the fixture and its generator stay in sync.
