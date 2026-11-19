# Dependency License and Vulnerability Policy

This document describes how Glassbox evaluates the licenses and security posture
of its Go, Rust, and Node dependencies. The machine-readable policy lives in
[`license-policy.json`](../license-policy.json) and the Rust-specific
configuration in [`.cargo/deny.toml`](../.cargo/deny.toml).

## Rationale

Glassbox is distributed under Apache-2.0. To maintain license compatibility
for users who redistribute it — as part of an enterprise toolchain, a container
image, or a managed service — every dependency must carry a license that is
compatible with permissive distribution. Copyleft licenses (GPL, AGPL, LGPL)
in transitive dependencies can create obligations that conflict with this goal.

Vulnerability scanning catches published CVEs and RustSec advisories before
they reach a published release. The policy defines clear severity thresholds so
every finding produces a named component and a remediation path rather than a
blanket scan failure with no context.

## Allowed licenses

The following SPDX identifiers are allowed without exception:

| License | Notes |
|---------|-------|
| Apache-2.0 | Project's own license |
| MIT | Most common permissive license |
| BSD-2-Clause | Two-clause BSD |
| BSD-3-Clause | Three-clause BSD |
| ISC | Functionally equivalent to MIT |
| MPL-2.0 | Mozilla Public License — file-level copyleft only; compatible |
| CC0-1.0 | Public domain dedication |
| Unlicense | Public domain dedication |
| 0BSD | Zero-clause BSD |
| BlueOak-1.0.0 | Modern permissive license |

## Disallowed licenses

These licenses are blocked at the build level:

| License | Reason |
|---------|--------|
| GPL-2.0 / GPL-3.0 (and variants) | Strong copyleft — incompatible with permissive distribution |
| AGPL-1.0 / AGPL-3.0 (and variants) | Network copyleft — triggered by SaaS deployment |
| LGPL-2.0 / LGPL-2.1 / LGPL-3.0 (and variants) | Weak copyleft — acceptable only for dynamic linking, which is not our model |
| SSPL-1.0 | Server Side Public License — designed to restrict SaaS |
| BUSL-1.1 | Business Source License — delayed open source, non-permissive during delay period |
| Commons-Clause | Restricts commercial use; not an OSI-approved open-source license |
| Proprietary | No redistribution rights |

## Ambiguous licenses

These licenses require manual review before use. A scan finding for any of
these identifiers fails the build and requires an explicit exception:

- CC-BY-4.0, CC-BY-SA-4.0 (ShareAlike may be copyleft depending on use)
- OFL-1.1 (Open Font License — allowed for fonts only; not for software)
- EPL-1.0, EPL-2.0 (Eclipse Public License — weak copyleft)
- CDDL-1.0 (Common Development and Distribution License — incompatible with GPL dependencies)
- CPOL-1.02, SISSL, OSL-3.0, CPAL-1.0

## Vulnerability severity thresholds

| Severity | Action |
|----------|--------|
| CRITICAL | Build fails; blocks release |
| HIGH | Build fails; blocks release |
| MEDIUM | Warning in CI summary; uploaded as scan artifact; does not block |
| LOW | Ignored in scan output |

A CRITICAL or HIGH finding with no upstream fix may be temporarily accepted
via a time-limited exception (maximum 90 days). The exception must document
why the attack surface is not reachable in production and who approved it.

## Scope of scanning

| Ecosystem | Tool | What is scanned |
|-----------|------|-----------------|
| Go | `go-licenses` | All direct and transitive dependencies (`./...`) |
| Rust | `cargo-deny` | All crates in `simulator/Cargo.toml` including dev-dependencies |
| Node | `license-checker` | Production dependencies only (`--production`; no `devDependencies`) |

Test-only Go dependencies are scanned but violations produce warnings, not
failures, because they are never compiled into release binaries.

## How the scan works

1. **CI triggers**: every PR touching dependency files + weekly on Sunday.
2. **Three parallel jobs** — one per ecosystem — run the appropriate scanner.
3. Each scanner's output is checked against `license-policy.json`.
4. Violations print a `[FAIL]` line identifying the package, its license, and
   the policy rule it violates.
5. Reports are always uploaded as artifacts (retained 30 days) even on pass.
6. The `license-gate` job aggregates results; a single failing ecosystem blocks
   merge when branch protection requires this check.

## Adding an exception

Use exceptions only when:
- The dependency cannot be replaced with an allowed alternative.
- The license or vulnerability has been reviewed and the risk is accepted.
- A time limit is set for re-evaluation.

**Process:**

1. Open a PR that adds the exception to `license-policy.json` under `exceptions`.
2. Required fields:

   ```json
   {
     "id": "EX-NNN",
     "package": "module/path@version",
     "ecosystem": "go|rust|node",
     "license": "SPDX-ID",
     "reason": "One paragraph explaining why this is acceptable.",
     "scope": "direct|transitive|test-only",
     "approved_by": "github-username",
     "approved_date": "YYYY-MM-DD",
     "expires": "YYYY-MM-DD",
     "review_url": "https://github.com/dotandev/glassbox/pull/NNN"
   }
   ```

3. `expires` must be no more than **12 months** from `approved_date` for
   license exceptions, and no more than **90 days** for vulnerability exceptions.
4. At least one project owner must approve the PR.
5. An automated reminder is sent 30 days before expiry (see renewal section).

### Rust advisory exceptions

For Rust, add the exception to `.cargo/deny.toml` under `[advisories].ignore`:

```toml
ignore = [
  { id = "RUSTSEC-2024-0001",
    reason = "No upstream fix; the affected API is not reachable via our simulator surface.",
    expires = "2026-10-27" },
]
```

The same entry must also appear in `license-policy.json` for unified auditing.

## Renewal procedure

Exceptions with an upcoming `expires` date are surfaced in the weekly license
scan CI job summary. When an exception expires:

1. The weekly scan will fail with an explicit message: `EX-NNN has expired`.
2. The owner listed in the exception must either:
   - **Remove the dependency** (preferred), or
   - **Renew the exception** by opening a new PR with an updated `expires` date
     and a fresh justification, or
   - **Escalate** if the dependency cannot be replaced and the risk cannot be
     accepted — escalation requires a project-lead sign-off.
3. A lapsed exception that is not renewed within 14 days will be removed
   automatically (the weekly scan enforces this).

## Documenting false positives

A false positive is a finding where the scanner reports a wrong license (e.g.
it cannot parse a custom `LICENSE` file and falls back to `UNKNOWN`). Do not
suppress the scan or add the incorrect identifier to the allowed list.

Instead, add an entry under `false_positive_policy.entries` in
`license-policy.json`:

```json
{
  "package": "some-crate",
  "ecosystem": "rust",
  "reported_license": "UNKNOWN",
  "actual_license": "MIT",
  "evidence_url": "https://github.com/example/some-crate/blob/main/LICENSE",
  "noted_by": "dotandev",
  "noted_date": "2026-07-27"
}
```

The `check-licenses.sh` script reads this list and downgrades matching
`UNKNOWN` findings to warnings with a reference to the evidence URL.

## Running scans locally

```bash
# All ecosystems
make license-scan

# Individual ecosystems
bash scripts/check-licenses.sh --go
bash scripts/check-licenses.sh --rust
bash scripts/check-licenses.sh --node

# With automatic tool installation
AUTO_INSTALL=1 bash scripts/check-licenses.sh --all
```

Reports are written to `license-reports/` (gitignored). Open
`license-reports/summary.txt` for the human-readable summary.

## Tool versions

| Tool | Version | Install |
|------|---------|---------|
| go-licenses | latest | `go install github.com/google/go-licenses@latest` |
| cargo-deny | latest | `cargo install cargo-deny --locked` |
| license-checker | latest | `npm install -g license-checker` |

These tools are installed at their latest version in CI because they are
scanning tools rather than build inputs. Pinning them separately from the
project dependencies avoids false breakage when the scanner itself receives
updates.
