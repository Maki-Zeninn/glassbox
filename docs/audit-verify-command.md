# `glassbox audit:verify` — Audit Log Verification Reference

The `audit:verify` command checks the integrity and authenticity of a signed audit log produced by [`audit:sign`](./audit-signing.md). It re-derives the payload hash, compares it to the embedded `trace_hash`, verifies the Ed25519 signature, and — when requested — validates the payload against a JSON schema and verifies the log's place in a tamper-evident **audit chain**.

Certificate and attestation verification can additionally be controlled through a configurable **trust policy** that specifies trust roots, issuer allowlists, validity windows, and revocation policy, rather than relying on ambient machine state.

---

## Synopsis

```
glassbox audit:verify --audit-log <file> [flags]
```

---

## Flags

| Flag | Description |
|---|---|
| `--audit-log` | Path to the signed audit log JSON file. **Required.** |
| `--public-key` | Hex-encoded Ed25519 public key (64 hex chars) that overrides the key embedded in the log, for independent verification. |
| `--schema` | Path to a JSON schema file to validate the payload structure against. |
| `--previous-signature-hash` | Expected hex SHA-256 of the previous log in the chain. When set, verifies that this log links to that predecessor. |
| `--json` | Emit the verification result as machine-readable JSON. |

### Trust Policy Flags

| Flag | Description |
|---|---|
| `--trust-roots <pem-file>` | Path to a PEM file of trusted root CA certificates. Only certificate chains anchored in these roots are accepted. |
| `--allowed-issuers <csv>` | Comma-separated list of allowed certificate issuer DN substrings. Untrusted issuers are explicitly identified in the output. |
| `--check-validity` | Enforce certificate `notBefore`/`notAfter` validity windows. The reference time is the log's own timestamp. |
| `--revoked-certs <csv\|file>` | Comma-separated list, or path to a text file, of revoked certificate serial numbers (hex). |
| `--revocation-mode <mode>` | `strict` (default when `--revoked-certs` is set): revoked certs fail the policy. `none`: informational only. |
| `--policy-config <json-file>` | Load a full `TrustPolicyConfig` from a JSON file. CLI flags always take precedence over file values. |

---

## Default Behaviour

When **no trust policy flags are set**, `audit:verify` runs in **cryptographic verification only** mode:

- Derives and compares the payload hash.
- Verifies the Ed25519 signature.
- Does **not** evaluate certificate issuers, validity windows, or revocation.

This is the documented safe default for offline audit review. The `policy` field is absent from JSON output in this mode.

---

## Trust Policy Evaluation

Changing any trust policy flag affects **only** the policy outcome, never the underlying cryptographic result. The two are displayed as separate sections:

```
Cryptographic Verification
  [PASS] Payload hash
  [PASS] Signature

Trust Policy
  [FAIL] Issuer
  Untrusted issuers: CN=Unknown Corp
  Issue: untrusted issuer "CN=Unknown Corp" is not in allowed issuers list

Result: INVALID — audit log failed verification.
```

Untrusted issuers are always explicitly named so they can be reviewed rather than producing an opaque failure.

---

## Validation (fail-fast)

All inputs are validated **before the audit log is read**, in `PreRunE`:

1. `--audit-log` is required and must point to a readable file.
2. `--public-key`, when provided, must be valid hex of exactly 32 bytes (64 hex characters).
3. `--schema`, when provided, must point to an existing file.
4. `--previous-signature-hash`, when provided, must be a 64-character hex string.
5. `--trust-roots`, when a file path, must point to an existing file (inline PEM strings are also accepted).
6. `--policy-config`, when provided, must point to an existing JSON file.
7. `--revocation-mode`, when provided, must be `"strict"` or `"none"`.

After the log is parsed, its **required fields** are checked before any cryptographic work. A log missing `signature`, `trace_hash`, `public_key`, or `payload` is rejected with an explicit message naming the missing field(s):

```
audit log is missing required field(s): signature
  A signed audit log must contain signature, trace_hash, public_key, and payload.
  If the public key is provided separately, pass it with --public-key.
```

(If `--public-key` is supplied, an absent embedded `public_key` is allowed.)

---

## Audit chain integrity

Signed logs may carry a `provenance.previous_signature_hash` that links each log to its predecessor, forming a tamper-evident chain (see [`audit:sign --previous-signature-hash`](./audit-signing.md)).

**Verifying a link:** pass the expected predecessor hash with `--previous-signature-hash`. The command compares it (case-insensitively) to the log's `previous_signature_hash` and reports a `Chain link` check:

```sh
glassbox audit:verify --audit-log log-2.json --previous-signature-hash <hash-of-log-1>
```

| Condition | Result |
|---|---|
| Hashes match | `[PASS] Chain link` |
| Hashes differ | `[FAIL] Chain link` + `chain link broken: log's previous_signature_hash … does not match the expected predecessor …` |
| Log has no `previous_signature_hash` | `[FAIL] Chain link` + a message noting it may be the genesis entry, or was signed without provenance |

**Not verifying a link:** if a log carries a `previous_signature_hash` but you do **not** pass `--previous-signature-hash`, the hash is only **format-checked**, not verified against a real predecessor. To avoid a passing report being mistaken for a verified chain, the command prints an explicit note:

```
Note: previous_signature_hash is present but chain linkage was not verified;
pass --previous-signature-hash <hex> to confirm this log links to the expected predecessor
```

---

## Output

Human-readable output lists each check as `[PASS]`/`[FAIL]` and ends with a `VALID` / `INVALID` result line. In human-readable mode a failed verification exits non-zero.
Human-readable output lists each check as `[PASS]`/`[FAIL]` under two separate sections — **Cryptographic Verification** and **Trust Policy** — and ends with a `VALID` / `INVALID` result line. A failed verification exits non-zero.

```
Audit Log Verification
──────────────────────────────────────────────────
  Version:    1.0.0
  Provider:   software
  Key ID:     1a2b3c4d…7e8f9a0b
  Trace Hash: 5c0a…90ab

Cryptographic Verification
  [PASS] Payload hash
  [PASS] Signature
  [PASS] Provenance
  [PASS] Chain link

Trust Policy
  [PASS] Trust root
  [PASS] Issuer
  [PASS] Validity
  [PASS] Revocation

Result: VALID — audit log integrity confirmed.
```

With `--json`, the same result is emitted as an object including `valid`, `hash_valid`, `signature_valid`, and (when applicable) `schema_valid`, `provenance_valid`, and `chain_link_valid`.

> **Exit status with `--json`:** once the log has been parsed, `--json` mode writes the result object and exits `0` **even when `valid` is `false`**. Scripts that use `--json` must read the `valid` field. Errors raised before a result exists (bad flags, unreadable or unparseable file, missing required fields) still exit non-zero in both modes.

---

## Integrity and acceptance

`audit:verify` answers two different questions, and a `VALID` result only answers both when you give it what it needs.

**Integrity** asks: *is this log internally consistent and unmodified since it was signed?* The command re-derives the canonical payload hash, compares it to `trace_hash` (`Payload hash` check), and verifies the Ed25519 signature over that hash (`Signature` check). If the payload was edited after signing, both checks fail.

**Acceptance** asks: *was it signed by a key I trust, and does it meet my requirements?* The command has no trust-policy file or keyring. Acceptance is expressed only through the flags you pass:

| Requirement | How it is expressed | Check reported |
|---|---|---|
| Signed by a trusted key | `--public-key <hex>` pins the verifying key | `Signature` |
| Payload has the expected shape | `--schema <file>` | `Schema` |
| Log follows a known predecessor | `--previous-signature-hash <hex>` | `Chain link` |

The `Provenance` check, shown whenever the log carries `provenance`, is structural only. It checks the certificate PEM format and the hash format. It does **not** establish trust in the signer identity.

Without `--public-key`, the signature is checked against the `public_key` embedded in the log itself. That proves integrity but not acceptance: anyone can sign an arbitrary payload with their own key and embed that key. A log signed by an untrusted key is `VALID` without a pin and `INVALID` (`[FAIL] Signature`) with your trusted key pinned. The workflow below shows both outcomes.

---

## End-to-end local workflow

This walkthrough uses only repository assets from [`docs/examples/audit-verify/`](./examples/audit-verify/) and throwaway keys generated in a temporary directory. Run it from the repository root; it needs `glassbox`, `openssl`, `sed`, and `od`.

`payload.json` is fixed and uses a synthetic timestamp, so its canonical hash (`trace_hash`) is the same on every run. The key, signature, and log `timestamp` change per run because the keys are freshly generated, but every pass/fail outcome below is the same each time. The output blocks show only the check and result lines; the header with version, key ID, and hash is omitted.

### 1. Set up throwaway keys and a signed log

```sh
WORK="$(mktemp -d)"

# "trusted" key: the one verifiers have agreed to accept
glassbox audit:keygen --key-name testonly-trusted --output-dir "$WORK"
# "other" key: stands in for an unknown signer
glassbox audit:keygen --key-name testonly-other --output-dir "$WORK"

glassbox audit:sign --payload-file docs/examples/audit-verify/payload.json \
  --software-private-key "$WORK/testonly-trusted.private.pem" > "$WORK/trusted.signed.json"

glassbox audit:sign --payload-file docs/examples/audit-verify/payload.json \
  --software-private-key "$WORK/testonly-other.private.pem" > "$WORK/other.signed.json"

# --public-key takes 64 hex chars; extract them from the trusted SPKI PEM
# (the last 32 bytes of the DER encoding are the raw Ed25519 key).
TRUSTED_HEX="$(openssl pkey -pubin -in "$WORK/testonly-trusted.public.pem" -outform DER \
  | tail -c 32 | od -An -tx1 | tr -d ' \n')"
```

### 2. Integrity: success

```sh
glassbox audit:verify --audit-log "$WORK/trusted.signed.json"
```

```
  [PASS] Payload hash
  [PASS] Signature

Result: VALID — audit log integrity confirmed.
```

### 3. Integrity: failure (tampered payload)

```sh
sed 's/"replay-ok"/"replay-failed"/' "$WORK/trusted.signed.json" > "$WORK/tampered.signed.json"
glassbox audit:verify --audit-log "$WORK/tampered.signed.json"
```

```
  [FAIL] Payload hash
  [FAIL] Signature

Result: INVALID — audit log failed verification.
```

The re-derived hash no longer matches `trace_hash`. The signature fails as well because it is checked against the re-derived hash, not the stored one. The command exits non-zero.

### 4. Acceptance: success (pinned trusted key, schema)

```sh
glassbox audit:verify --audit-log "$WORK/trusted.signed.json" \
  --public-key "$TRUSTED_HEX" \
  --schema docs/examples/audit-verify/payload-schema.json
```

```
  [PASS] Payload hash
  [PASS] Signature
  [PASS] Schema

Result: VALID — audit log integrity confirmed.
```

### 5. Acceptance: failure (intact log, untrusted signer)

```sh
# Integrity alone passes: the log is consistent with its own embedded key.
glassbox audit:verify --audit-log "$WORK/other.signed.json"          # Result: VALID

# Pinning the trusted key rejects it.
glassbox audit:verify --audit-log "$WORK/other.signed.json" --public-key "$TRUSTED_HEX"
```

```
  [PASS] Payload hash
  [FAIL] Signature

Result: INVALID — audit log failed verification.
```

`Payload hash` still passes because the log is unmodified. Only the signer is wrong. This is the result that integrity checking alone cannot give you.

### 6. Clean up

```sh
rm -rf "$WORK"
```

---

## Verifying a directory of logs

`audit:verify` has **no directory mode**. It verifies exactly one file per invocation:

- `--audit-log` takes a single path. Positional arguments are rejected.
- The command does not discover files, recurse into subdirectories, filter by extension, or apply exclusions.
- There is no per-file policy selection. Every flag (`--public-key`, `--schema`, `--previous-signature-hash`) applies to the one file being verified.
- If `--audit-log` points at a directory, it passes the pre-run existence check and then fails when the file is read (`failed to read audit log: read <path>: is a directory`), exiting non-zero.

To verify a directory, the caller chooses the files and the flags. The repository has no directory-verification tests because the command has no such behavior. The selection rules below belong to the example script, not to `audit:verify`.

Synthetic layout:

```
audit-logs/
├── 2026-01-01.signed.json
├── 2026-01-02.signed.json
├── 2026-01-03.signed.json      # payload edited after signing
├── payload.json                # unsigned input, not a signed log
├── README.txt
└── archive/
    └── 2025-12-31.signed.json
```

Example script: top-level `*.signed.json` only, non-recursive, with one pinned key for every file:

```sh
status=0
for f in audit-logs/*.signed.json; do
  [ -f "$f" ] || continue
  # Human-readable mode, so the exit status reflects VALID/INVALID (see the --json note above).
  if glassbox audit:verify --audit-log "$f" --public-key "$TRUSTED_HEX" > /dev/null; then
    echo "ok      $f"
  else
    echo "INVALID $f"
    status=1
  fi
done
exit "$status"
```

Expected result for the layout above:

| Path | Outcome |
|---|---|
| `2026-01-01.signed.json`, `2026-01-02.signed.json` | `ok` |
| `2026-01-03.signed.json` | `INVALID` (`Payload hash` and `Signature` fail); script exits `1` |
| `payload.json`, `README.txt` | not inspected (do not match `*.signed.json`) |
| `archive/2025-12-31.signed.json` | not inspected (the glob does not recurse) |

If logs in the directory were signed by different keys (for example, across a [key rotation](./audit-signing.md#key-rotation)), the script must choose the matching `--public-key` for each file. If the logs form a chain, pass each predecessor's hash with `--previous-signature-hash`. A plain loop checks each file on its own and does not verify chain order.
With `--json`, the same result is emitted as an object including `hash_valid`, `signature_valid`, and (when applicable) `schema_valid`, `provenance_valid`, `chain_link_valid`, and a `policy` object containing `trust_root_valid`, `issuer_allowed`, `validity_valid`, `revocation_valid`, `issues`, and `untrusted_issuers`.

---

## Examples

```sh
# Verify with the embedded public key
glassbox audit:verify --audit-log signed-audit.json

# Independent verification with an out-of-band public key
glassbox audit:verify --audit-log signed-audit.json --public-key <hex>

# Validate payload structure against a schema
glassbox audit:verify --audit-log signed-audit.json --schema payload-schema.json

# Verify with explicit trust roots and issuer allowlist
glassbox audit:verify --audit-log signed-audit.json \
  --trust-roots /etc/glassbox/trusted-ca.pem \
  --allowed-issuers "CN=My HSM CA,OU=Security"

# Enforce validity windows and check for revoked certificates
glassbox audit:verify --audit-log signed-audit.json \
  --check-validity --revoked-certs deadbeef01,cafebabe02

# Load trust policy from a JSON file
glassbox audit:verify --audit-log signed-audit.json --policy-config trust-policy.json

# Verify this log links to the expected previous log in the chain
glassbox audit:verify --audit-log log-2.json --previous-signature-hash <hash-of-log-1>

# Machine-readable JSON
glassbox audit:verify --audit-log signed-audit.json --json
```

---

## trust-policy.json format

```json
{
  "trust_roots": ["/etc/glassbox/ca-bundle.pem"],
  "allowed_issuers": ["CN=My HSM Attestation CA", "OU=Security Signing"],
  "check_validity": true,
  "revoked_certificates": ["deadbeef01", "cafebabe02"],
  "revocation_policy": "strict"
}
```

---

## See Also

- [`glassbox audit:sign`](./audit-signing.md) — produce a signed audit log
- [Audit canonicalization](./audit-canonicalization.md) — how the payload hash is derived
- [KMS signing](./audit-kms-signing.md)
- [Key rotation](./audit-signing.md#key-rotation) — introducing a new key and retiring the old one
- [Example assets](./examples/audit-verify/) — the payload and schema used in the walkthrough above
- [Glassbox configuration reference](../glassbox.example.toml) — `[audit.trust_policy]` section
