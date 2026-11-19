# ADR-003: Trust Boundaries and Component Trust Levels

## Status

Accepted

## Context

Glassbox spans multiple execution environments and process boundaries: a Go CLI
host process, a Rust/WASM simulator subprocess, a plugin subprocess tier, a
browser-targeted TypeScript bindings layer, a signing provider tier (software,
PKCS#11 HSM, AWS KMS), an RPC tier (Soroban JSON-RPC), and local persistence
(SQLite + content-addressed snapshot store). Because these components differ in
privilege, language runtime, network access, and operator control, a shared
understanding of which components are trusted and which are not is required
before reasoning about what data may flow across each boundary and what controls
must apply.

Without an explicit trust model, security controls cannot be evaluated
consistently. A reviewer cannot determine, for example, whether input from the
simulator subprocess is validated by the host before use, or whether a plugin's
claimed identity has been verified before it is granted filesystem access.

## Decision

Glassbox components are grouped into four trust tiers. Each tier records whether
the component runs in the same process as the CLI host, how its identity is
established, and what the controls at the inbound and outbound edges of that
tier are.

### Tier 0 — CLI Host Process (fully trusted)

**Components:** `internal/cmd/*`, `internal/config`, `internal/audit`,
`internal/session`, `internal/signer` (factory + registry), `internal/snapshot`
(dedup store), crash reporter.

The Go CLI host is the root of trust for a Glassbox session. It owns the
configuration surface, the signing provider lifecycle, and the final
decision about what output is written to disk or transmitted to RPC. All other
tiers are subordinate to Tier 0.

Controls applied at this tier:
- Config loaded from operator-controlled TOML / environment; no user-supplied
  data is allowed to override signing provider selection at runtime.
- Crash reporter (Sentry) is opt-in; disabled unless `crash_reporting = true`
  and a DSN is configured. Sensitive values are not included in crash payloads.
- Build-time version and commit SHA injected via `-ldflags`; no runtime self-
  modification.

### Tier 1 — Simulator Subprocess (untrusted, isolated)

**Components:** Rust/WASM simulator binary invoked by `internal/simulator/runner.go`.

The simulator is an **untrusted subprocess**. It executes arbitrary WASM
bytecode supplied by the operator (contract under test) and is treated as
potentially hostile code that may produce malformed output, excessive resource
usage, or malicious byte sequences.

Controls applied at the boundary:
- The simulator binary is launched as a child process via `os/exec`; it does not
  share memory or file descriptors with the host.
- The subprocess environment is constructed by `simulatorEnv()`, which starts
  from `os.Environ()` (the full parent environment) and adjusts `RUST_LOG` to
  match `GLASSBOX_LOG_LEVEL`. It does **not** strip signing credentials or RPC
  tokens from the inherited environment. Operators who require credential
  isolation must not export secrets into the CLI process environment.
- All IPC uses length-bounded stdin/stdout JSON (see ADR-004). Stdout and stderr
  buffers are capped to prevent memory exhaustion.
- Sandbox mode (`sandbox_mode: true`) requires an explicit `memory_limit` and
  `allowed_host_functions` allowlist before the subprocess is started; the host
  rejects sandbox requests that omit either field.
- The host validates the structured `SimulationResponseSchema` before consuming
  any field from the simulator response.

### Tier 2 — Plugin Subprocess (conditionally trusted, isolated)

**Components:** Plugin binaries described by `internal/plugin/manifest.go`,
sandboxed by `internal/plugin/sandbox.go`, policy-controlled by
`internal/plugin/policy.go`.

Plugins are **conditionally trusted** based on their declared `TrustLevel`:

| Trust level | Meaning | Default policy |
|---|---|---|
| `verified` | Checksum-verified, maintainer-signed binary | Permitted by default |
| `community` | Known-source but not maintainer-signed | **Blocked by default** (opt-in via `--allow-untrusted-plugins` or `plugin.allow_untrusted = true`) |
| `untrusted` | Unknown or unverified provenance | **Blocked by default** (opt-in via `--allow-untrusted-plugins` or `plugin.allow_untrusted = true`) |

Controls applied at the boundary:
- Plugin binary checksum is verified against the manifest before execution.
- Each plugin runs in its own child process; IPC is via stdin/stdout JSON only.
- The subprocess environment is stripped; no signing credentials, RPC tokens, or
  session secrets are propagated.
- A `DeniedCapabilities` and `DeniedPermissions` list is enforced by the host
  before the plugin subprocess is launched; a plugin that declares a denied
  capability is refused at load time, not at runtime.
- Each plugin call is wrapped in a `context.WithTimeout` of 10 seconds; the
  child process is killed by the OS when the context is cancelled.
- `DeniedPlugins` (by plugin ID) allows operator-level blocklisting.

### Tier 3 — Signing Provider Tier (trusted-by-configuration)

**Components:** `internal/signer/inmemory.go` (software Ed25519),
`internal/signer/provider_pkcs11.go` (PKCS#11 HSM),
`internal/signer/kms.go` (AWS KMS).

Signing providers are **trusted-by-configuration**: the operator selects the
provider via `GLASSBOX_SIGNING_PROVIDER` / `GLASSBOX_SIGNER_TYPE` or config
file. The host does not independently verify the provider's correctness at
runtime beyond the preflight checks described in the signing docs.

Controls:
- Provider selection is resolved once at startup by the factory
  (`internal/signer/factory.go`) via the `GLASSBOX_SIGNER_TYPE` environment
  variable (or `audit.signing_provider` in the config TOML when the higher-level
  `cmd` layer reads it); there is no runtime provider switching.
- The software provider holds the raw Ed25519 seed in process memory; its trust
  derives entirely from OS process isolation.
- PKCS#11 interacts with a hardware token via a vendor `.so`/`.dll`; the module
  path is validated for existence and file type before the module is loaded.
- AWS KMS never exposes the private key to the host process; signing happens
  inside AWS infrastructure and the host holds only IAM credentials.

### Tier 4 — External Services (untrusted network tier)

**Components:** Soroban JSON-RPC (`soroban_rpc_urls`), AWS KMS API,
Sentry crash endpoint, OTLP telemetry collector, IPFS/Arweave
(optional publish targets in `--publish-ipfs` / `--publish-arweave`).

All external network services are **untrusted** at the application layer.
Data received from them is parsed defensively and never executed.

Controls:
- RPC responses are parsed as XDR or JSON and validated against expected schema
  before use; raw bytes are never executed or passed to the signing path.
- URL credentials (`user:password@`) are stripped before display
  (`config show`) and before telemetry emission.
- `source` and `signature` hint fields in deep links are not validated and are
  explicitly documented as untrusted free-form strings.
- Crash and telemetry payloads are sanitised (command name truncated, hash
  values fingerprinted) before transmission to external endpoints.

### Tier 5 — Browser Bindings (restricted, no subprocess access)

**Components:** TypeScript bindings generated with `--runtime browser`
(`docs/bindings-environments.md`).

Browser-targeted bindings run in a **restricted environment** with no access to
Node.js primitives.

Controls:
- The generated `package.json` `"browser"` field excludes `child_process`, `fs`,
  and `path`; bundlers will not link these modules.
- Simulator interaction uses the HTTP fetch API (pointing at an RPC endpoint)
  rather than spawning a local process.
- ABI hash metadata is embedded in generated files for staleness detection; the
  hash is SHA-256 of the canonical JSON ABI, not a security signature.

### Trust diagram

```
┌──────────────────────────────────────────────────────────────┐
│  Tier 0: CLI Host Process (root of trust)                    │
│  ┌──────────────┐  ┌────────────────┐  ┌──────────────────┐ │
│  │ cmd / config │  │  audit/signer  │  │ snapshot / session│ │
│  └──────┬───────┘  └───────┬────────┘  └──────────────────┘ │
│         │                  │ provider interface               │
│  stdin/stdout JSON         │                                  │
│  ┌──────▼───────┐  ┌───────▼────────────────────────────┐   │
│  │  Tier 1      │  │  Tier 3: Signing Providers          │   │
│  │  Simulator   │  │  software / PKCS#11 HSM / AWS KMS   │   │
│  │  subprocess  │  └────────────────────────────────────-┘   │
│  └──────────────┘                                             │
│                                                               │
│  stdin/stdout JSON (separate invocation per plugin call)     │
│  ┌──────────────┐                                             │
│  │  Tier 2      │                                             │
│  │  Plugin      │                                             │
│  │  subprocess  │                                             │
│  └──────────────┘                                             │
└──────────────────────────────────────────────────────────────┘
         ↕  HTTPS / JSON-RPC
┌──────────────────────────────────────────────────────────────┐
│  Tier 4: External Services                                   │
│  (Soroban RPC, AWS KMS API, Sentry, OTLP, IPFS/Arweave)     │
└──────────────────────────────────────────────────────────────┘

  Tier 5: Browser Bindings (separate bundle, fetch API only)
```

## Rationale

### Why not run the simulator in-process?

Running WASM in-process via a Go WASM runtime would reduce IPC overhead but
would share the host's memory space, file descriptors, and OS signal handlers
with potentially hostile WASM bytecode. The subprocess model provides OS-level
isolation at the cost of serialisation latency, which is acceptable given that
simulation is already the dominant latency.

### Why is plugin trust level enforced at load time, not runtime?

A plugin that attempts a denied operation at runtime would have already
executed partially, potentially leaving side effects. Refusing at load time
ensures no code from the plugin runs until the policy check passes.

### Why is the AWS KMS provider trusted-by-configuration rather than Tier 0?

AWS KMS performs signing in AWS infrastructure. The host cannot independently
verify that AWS signed with the correct key without also trusting the KMS API
response — a circularity. The trust therefore derives from the operator's IAM
policy and AWS account controls, not from in-process verification.

### Alternatives considered

**Single-process sandbox via seccomp/landlock:** Would provide finer-grained
syscall filtering but is Linux-only and complicates cross-platform support.
Rejected for portability reasons; subprocess isolation is cross-platform.

**Plugin signed manifests with key pinning:** Would raise plugin trust from
checksum-based to cryptographically-signed. Deferred; the current checksum
model is a necessary prerequisite and is sufficient for the current threat model.

**Browser bindings running the simulator locally via WASM-in-WASM:** Technically
possible but would expose the full simulator attack surface to browser
sandboxing, which is weaker than OS process isolation. Rejected; browser
bindings delegate simulation to an RPC endpoint.

## Implementation

| Claim | Verified in |
|---|---|
| Simulator env: inherits parent env, adjusts `RUST_LOG` only | `internal/simulator/runner.go` (`simulatorEnv`) |
| Stdout/stderr buffer caps on simulator | `internal/simulator/runner.go` |
| Plugin checksum verification | `internal/plugin/sandbox.go` (`verifyChecksum`) |
| Plugin 10s per-call timeout via `context.WithTimeout` | `internal/plugin/sandbox.go` (`sandboxTimeout`) |
| Plugin denied caps/perms enforced at load | `internal/plugin/policy.go`, `internal/plugin/manifest.go` |
| Sandbox mode requires `memory_limit` + allowlist | `internal/simulator/sandbox_cap.go`, `docs/sandboxed-replay.md` |
| Provider selected once at startup via `GLASSBOX_SIGNER_TYPE` | `internal/signer/factory.go`, `internal/signer/registry.go` |
| KMS key never leaves AWS | `docs/audit-kms-signing.md`, `internal/signer/kms.go` |
| Browser bindings exclude `child_process`/`fs`/`path` | `docs/bindings-environments.md` |
| URL credential stripping in `config show` | `docs/security-warnings.md` |
| Crash reporting opt-in only | `cmd/glassbox/main.go` |

## Consequences

**Positive:**
- A reviewer can identify the trust level of any component from this document
  without reading source code.
- The subprocess model makes lateral movement from a compromised plugin or
  simulator significantly harder: the adversary is confined to the child process
  and the bounded IPC channel.
- Trust-by-configuration for signing providers means operators can enforce
  hardware-only signing in CI by setting `GLASSBOX_SIGNING_PROVIDER=pkcs11`
  or `aws-kms` and not supplying a software key.

**Negative / trade-offs:**
- IPC serialisation overhead adds latency per simulation request.
- Plugin trust levels `community` and `untrusted` are **blocked by default** (`DefaultPolicy()` returns `AllowUntrusted = false`). Operators who require community/untrusted plugins must explicitly opt-in via `--allow-untrusted-plugins` on the CLI, `plugin.allow_untrusted = true` in the config TOML, or `GLASSBOX_PLUGIN_ALLOW_UNTRUSTED=true` environment variable.
- Browser bindings cannot run the simulator locally; they require an accessible
  RPC endpoint, which introduces a network trust dependency not present in CLI
  mode.

**Migration impact:**
- Existing deployments that run with `GLASSBOX_SIGNER_TYPE=mock` (test HSM)
  should audit whether that configuration is present in production; mock signers
  are Tier 3 trust-by-configuration but do not provide hardware isolation.
- Existing deployments with community or untrusted plugins must now explicitly
  opt-in by setting `plugin.allow_untrusted = true` in their config TOML,
  passing `--allow-untrusted-plugins` on the CLI, or setting
  `GLASSBOX_PLUGIN_ALLOW_UNTRUSTED=true` in the environment.
- Plugin deployments should review `policy.go` to ensure `DeniedCapabilities`
  aligns with their environment before enabling community plugins.

## Related

- [ADR-004: Data Classification and Cross-Boundary Data Flows](004-data-classification.md)
- [ADR-005: Canonicalization Ownership](005-canonicalization-ownership.md)
- [ADR-006: Provider Isolation](006-provider-isolation.md)
- [ADR-007: Offline Guarantees](007-offline-guarantees.md)
- [ADR-002: HSM Integration](002-hsm-integration.md)
- [Deep Link Parameter Semantics](deeplink-parameters.md)
- [Audit Signing](../audit-signing.md)
- [Sandboxed Replay](../sandboxed-replay.md)
