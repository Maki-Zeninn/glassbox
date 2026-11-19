# Diagnostics Bundle (`doctor --bundle`)

## Overview

`glassbox doctor --bundle` generates a **portable, redacted diagnostics archive** that can be shared for support or issue reproduction without containing any private key, authentication token, or personally-identifying material.

The command runs the same checks as `glassbox doctor` and then writes a deterministic ZIP archive containing a `manifest.json` and a `README.txt`.

## Usage

```sh
# Generate a bundle in the OS temp directory (path is printed)
glassbox doctor --bundle

# Generate at a specific path
glassbox doctor --bundle --bundle-output ./glassbox-diag.gbdiag

# Combine with --verbose for richer path information in check results
glassbox doctor --verbose --bundle --bundle-output ./diag.gbdiag
```

## Archive Layout

```
glassbox-diag-YYYYMMDD-HHMMSS.gbdiag   (ZIP)
├── manifest.json    ← machine-readable diagnostics
└── README.txt       ← human-readable summary
```

### `manifest.json` Schema (version 1)

```jsonc
{
  "schema_version": 1,          // incremented on breaking layout changes
  "meta": {
    "glassbox_version": "1.2.3",
    "commit_sha": "abc123de",
    "build_date": "unknown",
    "go_version": "go1.26"
  },
  "platform": {
    "os": "linux",
    "arch": "amd64",
    "num_cpu": 8,
    "go_os": "linux",
    "go_arch": "amd64",
    "hostname": "myhost",       // first DNS label only (no domain)
    "protocol_registration_state": "registered"  // registered | not-registered | unknown
  },
  "config": {
    "rpc_url": "https://soroban-testnet.stellar.org",
    "network": "testnet",
    "log_level": "info",
    "request_timeout": 15,
    "max_trace_depth": 50,
    "failure_threshold": 5,
    "retry_timeout": 60,
    "failover_strategy": "",
    "telemetry": false,
    "crash_reporting": false,
    "rpc_token": "[REDACTED]",        // always redacted
    "crash_sentry_dsn": "[REDACTED]", // always redacted
    "crash_endpoint": "[REDACTED]",   // always redacted
    "cache_path": "~/.Glassbox/cache" // home dir replaced with ~
  },
  "checks": [
    {
      "id": "go",
      "name": "Go",
      "ok": true,
      "version": "go1.26.0",
      "path": "/usr/local/go/bin/go"
    },
    {
      "id": "simulator",
      "name": "Simulator Binary (glassbox-sim)",
      "ok": false,
      "fix_hint": "Build the simulator: cd simulator && cargo build --release"
    }
    // … one entry per doctor check
  ],
  "generated_at": "2026-07-24T12:00:00Z"
}
```

## Redaction Policy

The following values are **always replaced with `[REDACTED]`**:

| Category | Fields / Env vars |
|----------|------------------|
| RPC authentication | `rpc_token`, `GLASSBOX_RPC_TOKEN` |
| Crash reporting | `crash_sentry_dsn`, `crash_endpoint`, `GLASSBOX_SENTRY_DSN`, `GLASSBOX_CRASH_ENDPOINT` |
| Config passphrase | `GLASSBOX_CONFIG_PASSPHRASE` |
| Ed25519 private keys | Any 64-hex-character value |
| Stellar secret keys | Any `S`-prefixed 56-character base32 string |
| Filesystem paths | Home directory prefix replaced with `~` |

## Acceptance Criteria

- The bundle can be generated **offline** (no network requests).
- The archive **contains no private key or token material**.
- The archive has a **manifest with a schema version**.
- The archive is **readable on another machine** (standard ZIP format, `.gbdiag` or `.zip` extension).
- Tests assert redaction of representative environment values (`GLASSBOX_RPC_TOKEN`, `GLASSBOX_SENTRY_DSN`).

## File Extensions

| Extension | Notes |
|-----------|-------|
| `.gbdiag` | Canonical Glassbox diagnostics archive |
| `.zip` | Accepted for interoperability with generic ZIP tools |

Any other extension is rejected with a clear error message.
