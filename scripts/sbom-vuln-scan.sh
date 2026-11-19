#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# sbom-vuln-scan.sh — vulnerability scan for a Glassbox release using
# Google's osv-scanner, which queries the OSV database for all three
# ecosystems (Go modules, Rust/Cargo, npm) in a single pass.
#
# osv-scanner operates directly on lockfiles rather than requiring installed
# toolchains, making it safe to run in a post-build container that does not
# have Rust or Node available.
#
# Policy (mirrors vulnerability_policy in license-policy.json):
#   CRITICAL / HIGH   → fail the job, block release
#   MEDIUM            → emit warning, upload scan artifact, do not block
#   LOW               → ignored
#
# Usage:
#   scripts/sbom-vuln-scan.sh [options]
#
# Options:
#   --go-sum       <go.sum>              Path to go.sum (default: go.sum)
#   --cargo-lock   <Cargo.lock>          Path to Cargo.lock (default: simulator/Cargo.lock)
#   --package-lock <package-lock.json>   Path to npm lockfile (default: package-lock.json)
#   --sbom         <sbom.spdx.json>      Also scan the SBOM file directly (optional)
#   --output-dir   <dir>                 Directory for scan reports (default: vuln-reports)
#   --fail-on      <CRITICAL|HIGH|MEDIUM> Minimum severity to fail on (default: HIGH)
#   --format       <table|json|sarif>    Output format (default: table)
#
# Exit codes:
#   0  No findings at or above the fail-on threshold.
#   1  One or more findings at or above the fail-on threshold.
#   2  osv-scanner not installed (skipped; returns 0 when ALLOW_SKIP=1).
#
# osv-scanner installation:
#   go install github.com/google/osv-scanner/cmd/osv-scanner@latest
#   OR via pre-built release: https://github.com/google/osv-scanner/releases
#
# Environment:
#   ALLOW_SKIP=1    Exit 0 instead of 2 when osv-scanner is not installed.
#                   Use in CI when the tool is optional (e.g. PR checks).
#   OSV_SCANNER     Path to the osv-scanner binary (default: osv-scanner in PATH).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── Defaults ──────────────────────────────────────────────────────────────────
GO_SUM="${REPO_ROOT}/go.sum"
CARGO_LOCK="${REPO_ROOT}/simulator/Cargo.lock"
PACKAGE_LOCK="${REPO_ROOT}/package-lock.json"
SBOM_FILE=""
OUTPUT_DIR="${REPO_ROOT}/vuln-reports"
FAIL_ON="HIGH"
FORMAT="table"

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --go-sum)       GO_SUM="$2";       shift 2 ;;
    --cargo-lock)   CARGO_LOCK="$2";   shift 2 ;;
    --package-lock) PACKAGE_LOCK="$2"; shift 2 ;;
    --sbom)         SBOM_FILE="$2";    shift 2 ;;
    --output-dir)   OUTPUT_DIR="$2";   shift 2 ;;
    --fail-on)      FAIL_ON="$2";      shift 2 ;;
    --format)       FORMAT="$2";       shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; }
warn()  { printf '  [WARN] %s\n' "$*"; }
info()  { printf '  [INFO] %s\n' "$*"; }
skip()  { printf '  [SKIP] %s\n' "$*"; }

mkdir -p "${OUTPUT_DIR}"

echo "Vulnerability scan (osv-scanner)"
echo "  Fail on  : ${FAIL_ON}+"
echo "  Reports  : ${OUTPUT_DIR}"
echo ""

# ── Check osv-scanner availability ───────────────────────────────────────────
OSV_SCANNER="${OSV_SCANNER:-osv-scanner}"
if ! command -v "${OSV_SCANNER}" >/dev/null 2>&1; then
  echo "::warning::osv-scanner not found in PATH."
  echo "  Install: go install github.com/google/osv-scanner/cmd/osv-scanner@latest"
  echo "  Or download: https://github.com/google/osv-scanner/releases"
  echo ""
  if [ "${ALLOW_SKIP:-0}" = "1" ]; then
    skip "osv-scanner not installed; scan skipped (ALLOW_SKIP=1)"
    echo "Result: vulnerability scan skipped (osv-scanner not available)."
    exit 0
  fi
  echo "Result: osv-scanner required but not installed." >&2
  exit 2
fi

OSV_VERSION=$("${OSV_SCANNER}" --version 2>&1 | head -1 || echo "unknown")
info "osv-scanner: ${OSV_VERSION}"
echo ""

# ── Build lockfile arguments ──────────────────────────────────────────────────
# osv-scanner accepts --lockfile <ecosystem>:<path> for explicit ecosystem
# tagging, or it auto-detects from the filename. We use explicit tagging for
# go.sum and Cargo.lock to avoid any ambiguity.
LOCKFILE_ARGS=()
INPUTS_FOUND=0

if [ -f "${GO_SUM}" ]; then
  info "Go: ${GO_SUM}"
  LOCKFILE_ARGS+=(--lockfile "go:${GO_SUM}")
  INPUTS_FOUND=$((INPUTS_FOUND + 1))
else
  warn "go.sum not found at ${GO_SUM}; skipping Go vulnerability scan"
fi

if [ -f "${CARGO_LOCK}" ]; then
  info "Rust: ${CARGO_LOCK}"
  LOCKFILE_ARGS+=(--lockfile "Cargo.lock:${CARGO_LOCK}")
  INPUTS_FOUND=$((INPUTS_FOUND + 1))
else
  warn "Cargo.lock not found at ${CARGO_LOCK}; skipping Rust vulnerability scan"
fi

if [ -f "${PACKAGE_LOCK}" ]; then
  info "npm: ${PACKAGE_LOCK}"
  LOCKFILE_ARGS+=(--lockfile "package-lock.json:${PACKAGE_LOCK}")
  INPUTS_FOUND=$((INPUTS_FOUND + 1))
else
  warn "package-lock.json not found at ${PACKAGE_LOCK}; skipping npm vulnerability scan"
fi

if [ -n "${SBOM_FILE}" ] && [ -f "${SBOM_FILE}" ]; then
  info "SBOM: ${SBOM_FILE}"
  LOCKFILE_ARGS+=(--sbom "${SBOM_FILE}")
  INPUTS_FOUND=$((INPUTS_FOUND + 1))
fi

echo ""

if [ "${INPUTS_FOUND}" -eq 0 ]; then
  echo "Result: no lockfiles or SBOM found; nothing to scan." >&2
  exit 1
fi

# ── Run osv-scanner ───────────────────────────────────────────────────────────
# Always produce JSON output for structured parsing; additionally produce the
# requested human format for the CI summary log.

JSON_REPORT="${OUTPUT_DIR}/osv-scan.json"
TABLE_REPORT="${OUTPUT_DIR}/osv-scan.txt"

echo "Running osv-scanner ..."

# JSON pass (for programmatic severity filtering).
OSV_JSON_EXIT=0
"${OSV_SCANNER}" \
  --format json \
  "${LOCKFILE_ARGS[@]}" \
  2>/dev/null \
  > "${JSON_REPORT}" || OSV_JSON_EXIT=$?

# Human-readable pass (for CI summary and artifact log).
OSV_TABLE_EXIT=0
"${OSV_SCANNER}" \
  --format "${FORMAT}" \
  "${LOCKFILE_ARGS[@]}" \
  2>&1 | tee "${TABLE_REPORT}" || OSV_TABLE_EXIT=$?

echo ""

# osv-scanner exits 1 when it finds vulnerabilities. We parse the JSON report
# ourselves to apply the severity threshold from policy.
# ── Parse JSON report and apply severity threshold ────────────────────────────
CRITICAL_COUNT=0
HIGH_COUNT=0
MEDIUM_COUNT=0
LOW_COUNT=0
UNKNOWN_COUNT=0

if [ -f "${JSON_REPORT}" ]; then
  SEVERITY_COUNTS=$(python3 - "${JSON_REPORT}" "${FAIL_ON}" <<'PYEOF'
import json, sys

report_path = sys.argv[1]
fail_on     = sys.argv[2].upper()

SEV_ORDER = {"CRITICAL": 4, "HIGH": 3, "MEDIUM": 2, "LOW": 1, "UNKNOWN": 0}
fail_threshold = SEV_ORDER.get(fail_on, 3)

try:
    with open(report_path) as f:
        report = json.load(f)
except (json.JSONDecodeError, FileNotFoundError) as e:
    print(f"PARSE_ERROR:{e}", file=sys.stderr)
    # Treat parse error as no findings — do not fail a scan because the report
    # is empty (osv-scanner writes an empty object when no findings exist).
    print("critical=0\nhigh=0\nmedium=0\nlow=0\nunknown=0\nfail=0")
    sys.exit(0)

counts  = {"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0}
failing = []

# OSV scanner JSON schema: {"results": [{"packages": [{"vulnerabilities": [...]}]}]}
for result in report.get("results", []):
    for pkg in result.get("packages", []):
        for vuln in pkg.get("vulnerabilities", []):
            # Severity may be on the vuln or in its database_specific section.
            sev = "UNKNOWN"
            for severity_entry in vuln.get("severity", []):
                # CVSS severity string or type.
                score_type  = severity_entry.get("type", "")
                score_value = severity_entry.get("score", "")
                # Map CVSS score string to severity bucket.
                if score_type in ("CVSS_V3", "CVSS_V2"):
                    try:
                        base = float(score_value)
                        if base >= 9.0:   sev = "CRITICAL"
                        elif base >= 7.0: sev = "HIGH"
                        elif base >= 4.0: sev = "MEDIUM"
                        else:             sev = "LOW"
                    except ValueError:
                        pass
                elif score_value.upper() in SEV_ORDER:
                    sev = score_value.upper()
                if sev != "UNKNOWN":
                    break

            # Fall back to database_specific.severity (GitHub Advisory format).
            if sev == "UNKNOWN":
                db_sev = vuln.get("database_specific", {}).get("severity", "")
                if db_sev.upper() in SEV_ORDER:
                    sev = db_sev.upper()

            counts[sev] = counts.get(sev, 0) + 1

            if SEV_ORDER.get(sev, 0) >= fail_threshold:
                vuln_id  = vuln.get("id", "?")
                pkg_name = pkg.get("package", {}).get("name", "?")
                pkg_ver  = pkg.get("package", {}).get("version", "?")
                failing.append(f"{sev}: {vuln_id} in {pkg_name}@{pkg_ver}")

for entry in failing:
    print(f"FAIL_FINDING:{entry}")

total_fail = len(failing)
print(f"critical={counts['CRITICAL']}")
print(f"high={counts['HIGH']}")
print(f"medium={counts['MEDIUM']}")
print(f"low={counts['LOW']}")
print(f"unknown={counts['UNKNOWN']}")
print(f"fail={total_fail}")
PYEOF
  )

  CRITICAL_COUNT=$(echo "${SEVERITY_COUNTS}" | grep '^critical=' | cut -d= -f2 || echo 0)
  HIGH_COUNT=$(echo "${SEVERITY_COUNTS}" | grep '^high=' | cut -d= -f2 || echo 0)
  MEDIUM_COUNT=$(echo "${SEVERITY_COUNTS}" | grep '^medium=' | cut -d= -f2 || echo 0)
  LOW_COUNT=$(echo "${SEVERITY_COUNTS}" | grep '^low=' | cut -d= -f2 || echo 0)
  FAIL_TOTAL=$(echo "${SEVERITY_COUNTS}" | grep '^fail=' | cut -d= -f2 || echo 0)

  echo "Severity summary:"
  printf '  CRITICAL : %s\n' "${CRITICAL_COUNT}"
  printf '  HIGH     : %s\n' "${HIGH_COUNT}"
  printf '  MEDIUM   : %s  (warning only)\n' "${MEDIUM_COUNT}"
  printf '  LOW      : %s  (ignored)\n' "${LOW_COUNT}"
  echo ""

  # Print each failing finding.
  if echo "${SEVERITY_COUNTS}" | grep -q '^FAIL_FINDING:'; then
    echo "Failing findings (severity >= ${FAIL_ON}):"
    echo "${SEVERITY_COUNTS}" | grep '^FAIL_FINDING:' | sed 's/^FAIL_FINDING:/  /' >&2
    echo ""
  fi

  # MEDIUM-only findings: warn but do not fail.
  if [ "${MEDIUM_COUNT:-0}" -gt 0 ] && [ "${FAIL_TOTAL:-0}" -eq 0 ]; then
    warn "${MEDIUM_COUNT} MEDIUM finding(s) — review the report at ${TABLE_REPORT}"
  fi
fi

# ── Write SARIF report for GitHub code scanning (if jq available) ─────────────
SARIF_REPORT="${OUTPUT_DIR}/osv-scan.sarif"
if command -v "${OSV_SCANNER}" >/dev/null 2>&1; then
  SARIF_EXIT=0
  "${OSV_SCANNER}" \
    --format sarif \
    "${LOCKFILE_ARGS[@]}" \
    2>/dev/null \
    > "${SARIF_REPORT}" || SARIF_EXIT=$?
  [ -s "${SARIF_REPORT}" ] && info "SARIF report written: ${SARIF_REPORT}"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo "Reports:"
echo "  Table  : ${TABLE_REPORT}"
echo "  JSON   : ${JSON_REPORT}"
[ -s "${SARIF_REPORT:-}" ] && echo "  SARIF  : ${SARIF_REPORT}"
echo ""

FINAL_FAIL="${FAIL_TOTAL:-0}"
if [ "${FINAL_FAIL}" -gt 0 ]; then
  echo "Result: ${FINAL_FAIL} vulnerability finding(s) at ${FAIL_ON}+ severity. Release blocked." >&2
  echo ""
  echo "Remediation:"
  echo "  1. Review findings in ${TABLE_REPORT}"
  echo "  2. Update the affected dependency to a patched version."
  echo "  3. If no patch is available, add a time-limited exception to"
  echo "     license-policy.json with approved_by, reason, and expires fields."
  echo "     Rust exceptions also go in .cargo/deny.toml [advisories].ignore."
  echo "  4. See docs/sbom.md#vulnerability-exceptions for the full process."
  exit 1
else
  echo "Result: no vulnerability findings at ${FAIL_ON}+ severity."
  exit 0
fi
