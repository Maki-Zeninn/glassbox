# Sandboxed WASM Replay

Simulator requests can opt into sandbox mode with explicit resource and host
function exposure controls.

Sandbox mode requires:

- A non-zero `memory_limit`
- An explicit `allowed_host_functions` allowlist
- The existing simulator subprocess isolation and bounded stdout/stderr buffers

Example request fields:

```json
{
  "sandbox_mode": true,
  "memory_limit": 67108864,
  "allowed_host_functions": ["storage_get", "storage_put", "storage_del"]
}
```

Glassbox rejects sandbox requests that omit the memory limit or allowlist before
starting the simulator process. The allowlist and memory limit are also passed to
the simulator custom configuration for runtime enforcement by integrations that
support restricted host function exposure.

---

## Architecture Decision Records

The following ADRs govern the design decisions behind sandboxed replay:

- [ADR-003: Trust Boundaries and Component Trust Levels](adr/003-trust-boundaries.md) — classifies the simulator subprocess as Tier 1 (untrusted, isolated) and documents the controls applied at the CLI↔simulator boundary, including the requirement that `memory_limit` and `allowed_host_functions` are set before the subprocess starts.
- [ADR-004: Data Classification and Cross-Boundary Data Flows](adr/004-data-classification.md) — enumerates exactly what data crosses Boundary A (CLI Host → Simulator Subprocess) and confirms that no signing credentials or session secrets are propagated to the subprocess environment.
- [ADR-007: Offline Guarantees](adr/007-offline-guarantees.md) — documents that snapshot replay makes no network calls and that the replay result is deterministic for a given snapshot.
