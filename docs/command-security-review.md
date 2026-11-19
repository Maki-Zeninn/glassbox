# Command Security Review Guide

Every new Glassbox command must pass a security review before merge. This guide
gives authors and reviewers a shared vocabulary, a concrete checklist, and two
worked examples derived from existing commands. It is intentionally concise: each
item names the helper or test that proves compliance rather than restating policy
from scratch.

---

## How to use this guide

**Authors** complete the checklist while writing the command and attach it to the
PR description. Mark each item ✅ (done), ➖ (not applicable — explain why), or
🚧 (deferred — open a tracking issue and link it).

**Reviewers** verify the marked items by looking at the referenced helpers and
test names. A checklist item marked ✅ with no corresponding test or helper call
fails review.

The two [worked examples](#worked-examples) at the bottom show how the checklist
maps to real code.

---

## Checklist

Copy this block into your PR description and fill it in.

```markdown
### Command security checklist: `glassbox <name>`

#### Secrets
- [ ] S1  No flag name contains a denylist term (`token secret password private key pin passphrase`) unless it intentionally accepts a credential — and if so, `isSensitiveKey` / `sanitizeArgs` will redact the value in the audit log.
- [ ] S2  No credential is stored in a struct field that is JSON-marshalled without redaction.
- [ ] S3  No credential is passed to a subprocess via environment variables or IPC payload fields.
- [ ] S4  `resolveAuditSignerProviderAndConfig` is the only place raw signing credentials are assembled; the command does not re-implement this.
- [ ] S5  If the command calls `signer.Sign(data)`, `data` is the 32-byte `sha256.Sum256(payload)[:]`, not the raw payload.

#### Paths
- [ ] P1  Every user-supplied input file path is validated with `ValidateInputPath(flag, path)` before any I/O.
- [ ] P2  Every user-supplied output file path is validated with `ValidateOutputPath(flag, path)` before any write.
- [ ] P3  Paths from external input (deep links, IPC, plugins) use `ValidatePathTraversal(flag, path)` or `NormalizePath(flag, path, allowedRoot)`.
- [ ] P4  The audit log output path is validated before `os.MkdirAll` / `os.WriteFile` (see `writeOperationAuditLog`).
- [ ] P5  All path validation happens in `PreRunE`, before any network call or simulation.

#### Network behaviour
- [ ] N1  Network names are validated with `validateNetwork(network)` or `validateNetworkName(name)`.
- [ ] N2  RPC URLs are validated for scheme (`https://` or `http://`) before use.
- [ ] N3  RPC responses are parsed and validated before any field is consumed; raw bytes are never executed.
- [ ] N4  The command's offline behaviour is documented: does it work without a network connection? (See [Offline Guarantees](adr/007-offline-guarantees.md).)
- [ ] N5  RPC / telemetry failures are never silent when they would leave the operator with a partial or misleading result — the CLI exits with an explicit error.
- [ ] N6  URL credentials (`user:password@`) are stripped before display or telemetry (`stripURLCredentials`).

#### Subprocesses and plugin boundaries
- [ ] SP1 The command does not launch a subprocess directly. Simulation goes through `internal/simulator/runner.go`; plugin calls go through `internal/plugin/sandbox.go`.
- [ ] SP2 If the command invokes a plugin, `Policy.CheckManifest` is called before `NewSandboxedPlugin` — the policy check must happen at load time, not at call time.
- [ ] SP3 No signing credentials, RPC tokens, or session secrets appear in the subprocess environment or IPC payload.
- [ ] SP4 The command does not read from a plugin's stderr (it is unconditionally `io.Discard`).

#### Output redaction
- [ ] R1  CLI args fed to the audit log pass through `sanitizeArgs(rawArgs)`.
- [ ] R2  Error strings fed to the audit log pass through `sanitizeError(err)`.
- [ ] R3  `--metadata` entries pass through `parseMetadataEntries(source)` (null bytes, key ≤ 128 chars, value truncated at 1024 chars).
- [ ] R4  Any config value that is JSON-marshalled into the audit log passes through `sanitizeValue(key, val)`.
- [ ] R5  RPC URLs shown to operators pass through `stripURLCredentials(urls)`.
- [ ] R6  Command names emitted to the OTLP collector are sanitised (alphanumeric/dash/colon/underscore, ≤ 64 chars).
- [ ] R7  Hash or contract-ID values emitted to telemetry are replaced with a 32-char fingerprint (`sha256:<prefix>`), not the raw value.

#### Permissions and data classification
- [ ] D1  The command's data class is identified for every flag it accepts: `SECRET`, `INTERNAL`, `PUBLIC`, or `UNTRUSTED INPUT`. (See [Data Classification](adr/004-data-classification.md).)
- [ ] D2  `SECRET` data is never written to the snapshot store, session DB, telemetry, or crash reports.
- [ ] D3  `INTERNAL` data (transaction payloads, simulation results, ledger state) is not transmitted to external services.
- [ ] D4  `UNTRUSTED INPUT` (deep-link parameters, plugin output, RPC responses) is validated before use; the `ParsedDebugURI` struct from `ParseDebugURI` is the canonical validated form for deep-link parameters.
- [ ] D5  The audit log is written with mode `0o600` (owner-read-only).

#### Tests
- [ ] T1  A test exercises the "sensitive flag is redacted" path for every flag classified `SECRET`.
- [ ] T2  A test covers each `ValidateInputPath` / `ValidateOutputPath` call with a missing-file case and a null-byte case.
- [ ] T3  A test exercises the `PreRunE` path validation before any network call — the test must not require a running RPC endpoint.
- [ ] T4  If the command calls `ParseDebugURI`, the URI parser tests in `internal/protocolreg/uri_test.go` cover the relevant parameter.
- [ ] T5  The command's offline behaviour is covered by at least one test that does not make a network call.
```

---

## Domain reference

The sections below explain each domain, name the helpers, and link to the policy
documents that govern them.

---

### Secrets

**Policy:** [ADR-004 § Data Classification](adr/004-data-classification.md) — class `SECRET`.

A flag accepts a credential when its name contains any of: `token`, `secret`,
`password`, `private`, `key`, `pin`, `passphrase`. The check is performed by
`isSensitiveKey` in `internal/cmd/operation_audit.go`.

```go
// internal/cmd/operation_audit.go
func isSensitiveKey(key string) bool {
    lower := strings.ToLower(key)
    return strings.Contains(lower, "token") ||
        strings.Contains(lower, "secret")  ||
        strings.Contains(lower, "password")||
        strings.Contains(lower, "private") ||
        strings.Contains(lower, "key")     ||
        strings.Contains(lower, "pin")     ||
        strings.Contains(lower, "passphrase")
}
```

`sanitizeArgs(rawArgs []string)` uses `isSensitiveKey` to redact both
`--flag value` and `--flag=value` forms. `sanitizeValue(key, val)` applies the
same check to config struct fields before they enter the audit log. Neither
function is infallible: a credential whose flag name is not in the denylist AND
whose value is fewer than 16 characters or contains no hex characters will not
be caught. Operators should review `--metadata` payloads manually before
distributing signed logs.

A secondary heuristic, `isLikelySecret(value string) bool`, flags values that
are ≥ 16 characters long and contain hex characters — catching unhashed Ed25519
seeds, Stellar transaction hashes, and similar long tokens passed under
innocuous flag names.

**Signing credentials** must flow only through `resolveAuditSignerProviderAndConfig`
in `internal/cmd/operation_audit.go`. Commands that need to sign must use
`signer.DefaultRegistry.CreateSigner(providerName, cfg)` — they must not
construct a `ProviderConfig` or call `signer.Sign` directly with raw key bytes.

When calling `signer.Sign(data)`, `data` must be the 32-byte SHA-256 digest of
the canonical payload — not the raw payload. The `Signer` interface accepts
`[]byte` without enforcement; the constraint is upheld in `internal/cmd/audit.go`
by always passing `hash[:]`:

```go
hash := sha256.Sum256(payloadBytes)
sig, err := signerImpl.Sign(hash[:])
```

**Tests:**
- `TestIsSensitiveKey_MatchesSensitiveTerms` — `internal/cmd/operation_audit_test.go`
- `TestSanitizeArgs_RedactsLongFlagValue` — `internal/cmd/operation_audit_test.go`
- `TestSanitizeArgs_RedactsLongFlagEqualsValue` — `internal/cmd/operation_audit_test.go`
- `TestIsLikelySecret_DetectsLongHexString` — `internal/cmd/operation_audit_test.go`

---

### Paths

**Policy:** [ADR-004 § Boundary E](adr/004-data-classification.md); path safety implemented in `internal/cmd/path_safety.go`.

Four helpers cover every path-safety requirement:

| Helper | Use case |
|---|---|
| `ValidateInputPath(flag, path)` | User-supplied file that must already exist (WASM, snapshot, XDR, JSON). Rejects null bytes, symlinks outside root, and directories. |
| `ValidateOutputPath(flag, path)` | User-supplied output file. May not exist yet; parent dir must be reachable; target must not be an existing directory. |
| `NormalizePath(flag, path, allowedRoot)` | Resolves symlinks, rejects null bytes, and optionally enforces a root boundary (use for paths from external input). |
| `ValidatePathTraversal(flag, path)` | Lightweight check for embedded resource paths from external input (plugin manifests, deep-link parameters). Rejects `..` and null bytes. |

All four include the flag name in the error message so operators can identify
the offending flag without reading source code.

Batch wrappers for the `debug` command:

```go
// Validate all input paths before any I/O:
if err := ValidateDebugInputPaths(snapshot, wasm, xdrFile, jsonFile,
    resultMetaFile, loadSnapshots, sourceAlias); err != nil {
    return err
}
// Validate all output paths before any I/O:
if err := ValidateDebugOutputPaths(saveSnapshots, exportSVG, traceOutput); err != nil {
    return err
}
```

All path validation must be called in `PreRunE` — before any network call,
simulator invocation, or file I/O.

**Tests:**
- `TestNormalizePath_NullByteRejected` — `internal/cmd/path_safety_test.go`
- `TestValidateInputPath_MissingFile_ErrorMentionsPath` — `internal/cmd/path_safety_test.go`
- `TestValidateOutputPath_ExistingDirectory_Rejected` — `internal/cmd/path_safety_test.go`
- `TestValidatePathTraversal_DoubleDotRejected` — `internal/cmd/path_safety_test.go`
- `TestDebugPreRunE_NullByteInPath_Rejected` — `internal/cmd/path_safety_test.go`

---

### Network behaviour

**Policy:** [ADR-007: Offline Guarantees](adr/007-offline-guarantees.md); [ADR-004 § Boundary D](adr/004-data-classification.md).

**Network name validation** uses `validateNetwork(network)` in
`internal/cmd/cmd_validation.go` for commands that accept a static set of
values (`testnet`, `mainnet`, `futurenet`, `standalone`, `public`). Commands
that also accept custom networks defined in config use
`validateNetworkName(name)` from `internal/cmd/network_helpers.go`, which
queries the loaded config before rejecting.

```go
// internal/cmd/cmd_validation.go
func validateNetwork(network string) error
// Accepts: testnet, mainnet, futurenet, standalone, public (case-insensitive)
// Returns errors.WrapValidationError with the invalid value and a suggestion

// internal/cmd/network_helpers.go
func validateNetworkName(name string) error
// Also accepts custom networks from [networks.<name>] in config.toml
```

**RPC responses** are untrusted (Tier 4 in [ADR-003](adr/003-trust-boundaries.md)).
Parse XDR or JSON responses into typed structs before accessing any field; never
pass raw bytes to a signing path or a subprocess.

**Offline behaviour** must be documented in the command's help text and in the
PR description. A command that requires network access must fail with an
actionable error, not a partial result, when all RPC endpoints are unreachable.

**URL credentials** are stripped before display by `stripURLCredentials(urls)` in
`internal/cmd/config_show.go`. Call this function any time `soroban_rpc_urls`
or any other URL list is surfaced to an operator or emitted to telemetry.

**Tests:**
- `TestValidateNetwork` / `TestValidateNetwork_ErrorMentionsSuggestion` — `internal/cmd/cmd_validation_test.go`

---

### Subprocesses and plugin boundaries

**Policy:** [ADR-003 § Tier 1 and Tier 2](adr/003-trust-boundaries.md); implementation in `internal/simulator/runner.go` and `internal/plugin/sandbox.go`.

Commands must not spawn subprocesses directly. Use the existing abstractions:

- **Simulator** — `runner.Run(ctx, req)` in `internal/simulator/runner.go`. The
  runner manages process lifecycle, stdout/stderr buffer caps (10 MB / 1 MB),
  and schema validation of the response.
- **Plugins** — `SandboxedPlugin.Decode(data []byte)` (and `Init`, `Cleanup`) in
  `internal/plugin/sandbox.go`. The sandbox enforces a minimal environment
  (`buildSandboxEnv`), a 10-second per-call timeout via
  `context.WithTimeout(sandboxTimeout)`, and unconditional stderr discard
  (`io.Discard`). The internal `call` method is unexported; callers use only the
  public methods above.

Plugin policy checks happen at load time, before any code runs:

```go
// internal/plugin/policy.go
func (p *Policy) CheckManifest(m *Manifest) error
// Enforces DeniedCapabilities, DeniedPermissions, DeniedPlugins, AllowUntrusted
// Must be called before NewSandboxedPlugin — not at call time
```

The default policy (`DefaultPolicy()`) sets `AllowUntrusted: true`, meaning
`community` and `untrusted` plugins are permitted. Operators who need
`verified`-only plugins must set `AllowUntrusted: false` in a policy file.

**What must not cross the subprocess boundary:**
- Signing credentials (`GLASSBOX_AUDIT_SIGNING_PROVIDER`, `GLASSBOX_SIGNING_PROVIDER`,
  `GLASSBOX_SIGNER_TYPE`, `GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX`,
  `GLASSBOX_PKCS11_PIN`, AWS IAM keys).
- RPC tokens (`GLASSBOX_RPC_TOKEN`).
- Session IDs or snapshot store paths.

Note: `simulatorEnv()` in `internal/simulator/runner.go` starts from
`os.Environ()` and only adjusts `RUST_LOG`. It does **not** strip secrets from
the inherited environment. Operators must not export signing credentials into the
shell environment in which the CLI runs.

**Tests:**
- `internal/plugin/policy_test.go` — policy manifest checks
- `internal/plugin/sandbox_test.go` — plugin sandbox execution

---

### Output redaction

**Policy:** [Security Warnings and Redaction](security-warnings.md); implementation in `internal/cmd/operation_audit.go`.

Every command that writes an audit log relies on the redaction pipeline
automatically via `writeOperationAuditLog`. For custom output or error messages,
apply the helpers directly:

```go
// Redact sensitive flag values in the stored args list:
sanitized := sanitizeArgs(os.Args[1:])

// Strip file paths and likely-secret tokens from error strings:
safe := sanitizeError(execErr)

// Validate and length-cap --metadata entries before signing:
entries := parseMetadataEntries(AuditLogMetadata)
// maxMetadataKeyLen = 128, maxMetadataValueLen = 1024

// Redact a config map value when the key matches the denylist:
display := sanitizeValue(key, rawValue)

// Strip credentials from displayed RPC URLs:
clean := stripURLCredentials(rpcURLs)
```

For **telemetry**, command names must contain only `[a-zA-Z0-9\-:_]` and be
≤ 64 characters. Hash or contract-ID values must be replaced with a 32-char
fingerprint before emission. See [security-warnings.md § Telemetry privacy](security-warnings.md#telemetry-privacy).

**Tests:**
- `TestSanitizeArgs_*` — `internal/cmd/operation_audit_test.go`
- `TestSanitizeError_*` — `internal/cmd/operation_audit_test.go`
- `TestParseMetadataEntries_*` — `internal/cmd/operation_audit_test.go`

---

### Permissions and data classification

**Policy:** [ADR-004: Data Classification and Cross-Boundary Data Flows](adr/004-data-classification.md).

Assign one of the four classes to every piece of data the command touches:

| Class | Examples | Minimum control |
|---|---|---|
| `SECRET` | Ed25519 seed, PKCS#11 PIN, AWS IAM key, RPC bearer token, URL userinfo | Never logged, never telemetered, redacted in audit log, never passed to subprocesses |
| `INTERNAL` | Envelope XDR, ledger entries, simulation results, signed audit log | Stay within CLI; not transmitted to telemetry or crash endpoints |
| `PUBLIC` | Command name, trace spans, ABI spec, hash fingerprints | Sanitised before emission |
| `UNTRUSTED INPUT` | `glassbox://` URI params, plugin stdout, RPC response bytes | Validated and typed before any use |

**Deep-link parameters** (`UNTRUSTED INPUT`) are validated by `ParseDebugURI` in
`internal/protocolreg/uri.go` before the `debug` command is invoked. The returned
`*ParsedDebugURI` struct contains validated fields; commands must consume those
fields and must not re-parse the raw URI string.

The audit log file is written with mode `0o600` (owner-read-only) by
`writeOperationAuditLog`. If a command writes any security-sensitive file, it
must use the same mode.

**Tests:**
- `internal/protocolreg/uri_test.go` — deep-link validation
- `internal/cmd/audit_test.go` — tamper detection, attestation removal

---

## Worked examples

The following two examples walk through the checklist for real commands. They are
meant to be read alongside the source files they reference.

---

### Example 1 — `glassbox audit:sign`

**Source:** `internal/cmd/audit_sign.go` (command registration),
`internal/cmd/operation_audit.go` (signing path),
`internal/cmd/cmd_validation.go` (`validateAuditSignArgs`).

**Checklist walkthrough:**

#### S1 — Sensitive flag names

`audit:sign` accepts `--software-private-key`, `--pkcs11-pin`,
`--audit-log-kms-key-id`. All three names contain denylist terms (`key`, `pin`,
`key`). `sanitizeArgs` will redact their values in the audit log.

```go
// internal/cmd/operation_audit.go — isSensitiveKey catches all three:
// "software-private-key" contains "key"   ✅
// "pkcs11-pin"           contains "pin"   ✅
// "kms-key-id"           contains "key"   ✅
```

**Test:** `TestIsSensitiveKey_MatchesSensitiveTerms`

#### S2 — No credential in marshalled struct

`SignedOperationAuditLog` stores `Signature` and `PublicKey` (hex-encoded, not
the raw seed). The raw `ProviderConfig` (containing PIN and key bytes) is
consumed inside `resolveAuditSignerProviderAndConfig` and is never stored on a
marshalled struct.

#### S4 — Signing credentials assembled in one place

`signOperationAuditRecord` calls `resolveAuditSignerProviderAndConfig()`, which
reads `AuditLogSoftwareKey`, `AuditLogPKCS11PIN`, and AWS credentials from
package-level flag variables. No other function assembles a `ProviderConfig`.

#### S5 — `Sign` receives hash, not payload

```go
// internal/cmd/operation_audit.go — signOperationAuditRecord
hash := sha256.Sum256(payloadBytes)
sig, err := signerImpl.Sign(hash[:])     // ✅ 32-byte hash slice
```

#### P1 — Input paths validated

`validateAuditSignArgs` in `internal/cmd/cmd_validation.go` validates mutual
exclusivity of `--payload` / `--payload-file` and checks that the provider name
is registered. It does **not** call `validateFilePath` for `--payload-file`;
that path check is the responsibility of the `audit:sign` command's own
`PreRunE` via `ValidateInputPath("payload-file", payloadFile)`.

```go
// internal/cmd/cmd_validation.go
func validateAuditSignArgs(payload, payloadFile, provider string) error {
    // mutual exclusivity of --payload / --payload-file …
    // provider name validated against signer.DefaultRegistry.Names() …
    // NOTE: file existence for --payload-file is checked in PreRunE, not here.
}
```

**Test:** `TestValidateAuditSignArgs` — `internal/cmd/cmd_validation_test.go`

#### P4 — Audit log output path validated

```go
// internal/cmd/operation_audit.go — writeOperationAuditLog
if _, err := ValidateOutputPath("audit-log", path); err != nil {
    return errors.WrapValidationError(...)
}
// os.MkdirAll and os.WriteFile only called after validation passes ✅
```

#### N4 — Offline behaviour documented

`audit:sign` with the software or PKCS#11 provider requires no network access.
`audit:sign` with the AWS KMS provider requires HTTPS to the KMS API for every
call — the command exits with a clear error when KMS is unreachable.
See [ADR-007 § Offline-capable operations](adr/007-offline-guarantees.md#1-offline-capable-operations).

#### SP3 — No credentials in subprocess environment

`audit:sign` does not invoke a simulator or plugin subprocess. The signing
operation is entirely in-process via the `Signer` interface.

#### R1–R4 — Redaction pipeline

All args, errors, metadata, and config values flow through the standard
`buildOperationAuditRecord` → `sanitizeArgs` + `sanitizeError` +
`parseMetadataEntries` pipeline. No custom marshalling bypasses it.

#### D1 — Data classification

| Flag | Class |
|---|---|
| `--payload` / `--payload-file` | `INTERNAL` — transaction payload |
| `--software-private-key` | `SECRET` — Ed25519 PEM seed |
| `--pkcs11-pin` | `SECRET` — PKCS#11 user PIN |
| `--audit-log-kms-key-id` | `SECRET` — KMS key identifier |
| `--signing-provider` | `PUBLIC` — provider name |
| `--audit-log` | `INTERNAL` — output path (written with `0o600`) |

#### D5 — Audit log file permissions

`os.WriteFile(path, output, 0o600)` — owner-read-only. ✅

#### T1–T5 — Tests

| Item | Test file |
|---|---|
| Sensitive flag redaction | `internal/cmd/operation_audit_test.go` |
| `validateAuditSignArgs` mutual exclusivity and provider check | `internal/cmd/cmd_validation_test.go` (`TestValidateAuditSignArgs`) |
| `ValidateOutputPath` for audit log path | `internal/cmd/path_safety_test.go` |
| Signing path determinism | `internal/cmd/canonical_test.go::TestGenerate_DeterministicHash` |

---

### Example 2 — `glassbox trace:export` (hypothetical command)

This example demonstrates the checklist for a command that does not exist yet,
to show how an author would reason through it from scratch.

**Scenario:** A new `trace:export` command that accepts a transaction hash,
fetches the execution trace from an RPC endpoint, and writes a signed JSON file
to a user-supplied output path. It also accepts an optional `--rpc-token` flag
and a `--metadata` flag.

#### S1 — Sensitive flag names

`--rpc-token` contains `token` → `isSensitiveKey` returns `true`. The value
will be redacted by `sanitizeArgs` in the audit log.

No new credential flags are needed beyond `--rpc-token`. The signing credentials
are read from the standard audit signer environment, not from command flags.

#### S3 — No credentials in subprocess IPC payload

The command calls the RPC endpoint directly over HTTPS; it does not invoke the
simulator or a plugin. `--rpc-token` must not appear in the JSON request body
sent to the RPC endpoint — it belongs in an HTTP `Authorization` header only.

#### P2 — Output path validated before write

```go
// In PreRunE:
normalized, err := ValidateOutputPath("output", outputPath)
if err != nil {
    return err
}
// Store normalized for use in RunE; do not re-derive from raw input.
```

This call rejects null bytes, directory targets, and paths with traversal
sequences before any network work begins.

#### P5 — All validation in PreRunE

```go
func (cmd *cobra.Command) preRunE(c *cobra.Command, args []string) error {
    // 1. Validate transaction hash format (64-char hex regex).
    if !txHashPattern.MatchString(args[0]) {
        return errors.WrapValidationError("invalid transaction hash ...")
    }
    // 2. Validate network name.
    if err := validateNetwork(networkFlag); err != nil {
        return err
    }
    // 3. Validate output path.
    if _, err := ValidateOutputPath("output", outputFlag); err != nil {
        return err
    }
    return nil
}
```

#### N1 — Network name validated

`validateNetwork(networkFlag)` rejects anything outside the allowed set.

#### N3 — RPC response validated before use

```go
var traceResp TraceResponse
if err := json.Unmarshal(rpcBody, &traceResp); err != nil {
    return fmt.Errorf("malformed RPC response: %w", err)
}
// Access only typed fields on traceResp — never pass rpcBody to signer.Sign.
```

#### N4 — Offline behaviour documented

`trace:export` with a live transaction requires the RPC endpoint. A `--snapshot`
mode that reads from `~/.glassbox/cache/snapshots/` would be offline-capable.
Both modes must be documented in the command's long description and in `--help`.

#### N5 — RPC failure is explicit

```go
// If all configured RPC endpoints fail, return an actionable error:
return fmt.Errorf(
    "RPC connection failed for network %q: %w\n"+
    "  Fix: check your internet connection or pass a working endpoint with --rpc-url",
    networkFlag, err,
)
// Never return a partial result that silently omits network-fetched data.
```

#### SP1 — No direct subprocess

The command uses `json.Unmarshal` on the RPC response, not `exec.Command`. If it
later needs to pass data to the simulator, it does so through `runner.Run`.

#### R1–R3 — Redaction pipeline

```go
// In writeOperationAuditLog (called automatically from Execute in root.go):
// sanitizeArgs redacts --rpc-token value ✅
// sanitizeError strips any file path that leaks into an error message ✅
// parseMetadataEntries validates --metadata entries ✅
```

No custom marshalling bypasses these helpers.

#### R5 — URL credential stripping

```go
// Before displaying or logging the RPC URL:
displayURLs := stripURLCredentials([]string{resolvedRPCURL})
fmt.Fprintf(os.Stderr, "Connecting to: %s\n", displayURLs[0])
```

#### D1 — Data classification

| Flag / data | Class |
|---|---|
| `--rpc-token` | `SECRET` |
| Transaction hash (`args[0]`) | `PUBLIC` (on-chain identifier) |
| `--network` | `PUBLIC` |
| RPC response body | `UNTRUSTED INPUT` — parse into typed struct before use |
| Trace payload written to disk | `INTERNAL` |
| `--output` path | `INTERNAL` |
| `--metadata` entries | `INTERNAL` — validated by `parseMetadataEntries` |

#### D5 — Output file permissions

```go
os.WriteFile(normalized, output, 0o600)
// Signed trace files are INTERNAL — owner-read-only. ✅
```

#### T1–T5 — Required tests

| Item | What to test |
|---|---|
| T1 | `--rpc-token` value appears as `REDACTED` in the audit log args |
| T2 | `ValidateOutputPath` called in PreRunE; missing parent dir and null byte both surface errors |
| T3 | PreRunE rejects invalid tx hash, invalid network, and directory output path without making a network call |
| T5 | A replay mode reading from a local snapshot file completes without any network call |

---

## Properties not claimed

The following properties are **not** provided by the Glassbox security model and
must not be assumed or claimed in command documentation:

- **IPC channel encryption.** CLI↔simulator communication is local stdio. No
  cryptographic confidentiality beyond OS process isolation.
- **Simulator credential isolation.** `simulatorEnv()` inherits the parent shell
  environment. Credentials exported before the CLI is invoked are visible to the
  simulator subprocess.
- **Snapshot store confidentiality.** Snapshots at `~/.glassbox/cache/snapshots/`
  are plaintext. Apply OS filesystem permissions (`chmod 700`) if ledger state is
  sensitive.
- **Audit log confidentiality.** The log is signed but not encrypted. Anyone with
  the file can read the post-redaction payload.
- **Heuristic redaction completeness.** `sanitizeArgs` will miss a credential
  whose flag name is not in the denylist AND whose value is fewer than 16
  characters or contains no hex characters.
- **PKCS#11 memory isolation.** The PKCS#11 module runs in-process; a malicious
  or vulnerable `.so` has unrestricted access to process memory.
- **Signer interface enforcement.** `Signer.Sign(data []byte)` accepts any bytes.
  The constraint that `data` is always the 32-byte SHA-256 digest is upheld only
  in `internal/cmd/audit.go` — not by the interface itself.

---

## Quick reference: helper locations

| Helper | Package | Purpose |
|---|---|---|
| `isSensitiveKey` | `internal/cmd` | Denylist check for flag names |
| `isLikelySecret` | `internal/cmd` | Heuristic check for long hex values |
| `sanitizeArgs` | `internal/cmd` | Redact sensitive flag values in stored args |
| `sanitizeError` | `internal/cmd` | Strip paths and secrets from error strings |
| `sanitizeValue` | `internal/cmd` | Redact config struct field values by key name |
| `parseMetadataEntries` | `internal/cmd` | Validate and length-cap `--metadata` entries |
| `stripURLCredentials` | `internal/cmd` | Remove `user:password@` from displayed URLs |
| `ValidateInputPath` | `internal/cmd` | Validate user-supplied input file paths |
| `ValidateOutputPath` | `internal/cmd` | Validate user-supplied output file paths |
| `NormalizePath` | `internal/cmd` | Resolve symlinks; enforce allowed root boundary |
| `ValidatePathTraversal` | `internal/cmd` | Lightweight `..` and null-byte rejection |
| `ValidateDebugInputPaths` | `internal/cmd` | Batch input path check for the `debug` command |
| `ValidateDebugOutputPaths` | `internal/cmd` | Batch output path check for the `debug` command |
| `validateNetwork` | `internal/cmd` | Validate static network name values |
| `validateNetworkName` | `internal/cmd` | Validate network name including config-defined names |
| `validateMutuallyExclusive` | `internal/cmd` | Reject conflicting flag combinations |
| `validateAuditSignArgs` | `internal/cmd` | Validate `audit:sign` flags (mutual exclusivity, provider) |
| `ParseDebugURI` | `internal/protocolreg` | Validate all deep-link URI parameters |
| `Policy.CheckManifest` | `internal/plugin` | Enforce plugin policy at load time |
| `NewSandboxedPlugin` | `internal/plugin` | Construct sandboxed plugin (runs `verifyChecksum` and `buildSandboxEnv` internally) |
| `resolveAuditSignerProviderAndConfig` | `internal/cmd` | Assemble signing credentials from flags/env |
| `writeOperationAuditLog` | `internal/cmd` | Write signed audit log with `0o600` permissions |

---

## Related documents

- [ADR-003: Trust Boundaries and Component Trust Levels](adr/003-trust-boundaries.md)
- [ADR-004: Data Classification and Cross-Boundary Data Flows](adr/004-data-classification.md)
- [ADR-005: Canonicalization Ownership](adr/005-canonicalization-ownership.md)
- [ADR-006: Provider Isolation](adr/006-provider-isolation.md)
- [ADR-007: Offline Guarantees](adr/007-offline-guarantees.md)
- [Deep Link Parameter Semantics](adr/deeplink-parameters.md)
- [Security Warnings and Redaction](security-warnings.md)
- [Audit Signing](audit-signing.md)
- [AWS KMS Signing](audit-kms-signing.md)
- [Audit Verify Command](audit-verify-command.md)
- [Sandboxed Replay](sandboxed-replay.md)
- [Bindings Environments](bindings-environments.md)
- [Debug Command Reference](debug-command.md)
