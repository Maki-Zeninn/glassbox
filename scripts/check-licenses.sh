#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# check-licenses.sh — scan Go, Rust, and Node dependencies against
# license-policy.json and report violations.
#
# Usage:
#   scripts/check-licenses.sh [--go] [--rust] [--node] [--all]
#
#   With no flags, --all is assumed.
#   Individual ecosystem flags let you run a subset in CI jobs.
#
# Exit codes:
#   0  Policy satisfied across all scanned ecosystems.
#   1  One or more violations found.
#
# Required tools (installed automatically if missing when AUTO_INSTALL=1):
#   go-licenses   — github.com/google/go-licenses
#   cargo-deny    — https://github.com/EmbarkStudios/cargo-deny
#   license-checker — npm install -g license-checker
#
# Output:
#   Reports are written to license-reports/ (created if absent, gitignored).
#   Each ecosystem produces:
#     license-reports/go-licenses.csv
#     license-reports/go-violations.txt
#     license-reports/rust-deny.txt
#     license-reports/rust-violations.txt
#     license-reports/node-licenses.json
#     license-reports/node-violations.txt
#     license-reports/summary.txt
#
# Environment:
#   AUTO_INSTALL=1        Attempt to install missing tools automatically.
#   REPORT_DIR            Output directory (default: license-reports).
#   POLICY_FILE           Path to license-policy.json (default: license-policy.json).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_DIR="${REPORT_DIR:-${REPO_ROOT}/license-reports}"
POLICY_FILE="${POLICY_FILE:-${REPO_ROOT}/license-policy.json}"
AUTO_INSTALL="${AUTO_INSTALL:-0}"

# ── Parse flags ───────────────────────────────────────────────────────────────
RUN_GO=0; RUN_RUST=0; RUN_NODE=0
for arg in "$@"; do
  case "${arg}" in
    --go)   RUN_GO=1 ;;
    --rust) RUN_RUST=1 ;;
    --node) RUN_NODE=1 ;;
    --all)  RUN_GO=1; RUN_RUST=1; RUN_NODE=1 ;;
    *) echo "Unknown flag: ${arg}. Use --go, --rust, --node, or --all." >&2; exit 1 ;;
  esac
done
if [[ $((RUN_GO + RUN_RUST + RUN_NODE)) -eq 0 ]]; then
  RUN_GO=1; RUN_RUST=1; RUN_NODE=1
fi

pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; }
warn()  { printf '  [WARN] %s\n' "$*"; }
info()  { printf '  [INFO] %s\n' "$*"; }
skip()  { printf '  [SKIP] %s\n' "$*"; }

mkdir -p "${REPORT_DIR}"
SUMMARY="${REPORT_DIR}/summary.txt"
> "${SUMMARY}"

TOTAL_FAILURES=0

log_summary() { echo "$*" | tee -a "${SUMMARY}"; }

# ── Read policy ───────────────────────────────────────────────────────────────
if [[ ! -f "${POLICY_FILE}" ]]; then
  echo "ERROR: policy file not found: ${POLICY_FILE}" >&2
  exit 1
fi

# Build the allowed-licenses and disallowed-licenses lists from policy JSON.
ALLOWED=$(python3 -c "
import json, sys
p = json.load(open(sys.argv[1]))
print('\n'.join(p['allowed_licenses']))
" "${POLICY_FILE}")

DISALLOWED=$(python3 -c "
import json, sys
p = json.load(open(sys.argv[1]))
print('\n'.join(p['disallowed_licenses']))
" "${POLICY_FILE}")

AMBIGUOUS=$(python3 -c "
import json, sys
p = json.load(open(sys.argv[1]))
print('\n'.join(p.get('ambiguous_licenses', {}).get('list', [])))
" "${POLICY_FILE}")

VULN_FAIL_ON=$(python3 -c "
import json, sys
p = json.load(open(sys.argv[1]))
print(p['vulnerability_policy']['fail_on'])
" "${POLICY_FILE}")

# ── Helper: check a SPDX license identifier against policy ───────────────────
check_license_against_policy() {
  local license="$1"
  local pkg="$2"

  # Normalize: trim whitespace and common suffixes
  license="$(echo "${license}" | sed 's/[[:space:]]//g')"

  # Exact match against disallowed list
  if echo "${DISALLOWED}" | grep -qxF "${license}" 2>/dev/null; then
    echo "DISALLOWED"
    return
  fi

  # Exact match against allowed list
  if echo "${ALLOWED}" | grep -qxF "${license}" 2>/dev/null; then
    echo "ALLOWED"
    return
  fi

  # Ambiguous — needs manual review
  if echo "${AMBIGUOUS}" | grep -qxF "${license}" 2>/dev/null; then
    echo "AMBIGUOUS"
    return
  fi

  # Unknown — not in any list
  echo "UNKNOWN"
}

# ── GO LICENSE SCAN ───────────────────────────────────────────────────────────
scan_go() {
  log_summary ""
  log_summary "=== Go Dependencies ==="

  if ! command -v go-licenses >/dev/null 2>&1; then
    if [[ "${AUTO_INSTALL}" == "1" ]]; then
      info "Installing go-licenses ..."
      go install github.com/google/go-licenses@latest
    else
      warn "go-licenses not found. Install with: go install github.com/google/go-licenses@latest"
      warn "Set AUTO_INSTALL=1 to install automatically."
      log_summary "  [SKIP] go-licenses not installed"
      return
    fi
  fi

  local csv="${REPORT_DIR}/go-licenses.csv"
  local violations="${REPORT_DIR}/go-violations.txt"
  > "${violations}"

  info "Running go-licenses on ./... ..."
  # go-licenses csv writes: <module>,<license-url>,<license-type>
  if ! go-licenses csv ./... > "${csv}" 2>"${REPORT_DIR}/go-licenses-stderr.txt"; then
    warn "go-licenses exited non-zero; check ${REPORT_DIR}/go-licenses-stderr.txt"
  fi

  local count=0
  local fail_count=0
  local warn_count=0

  while IFS=',' read -r module _url spdx_id; do
    [[ -z "${module}" ]] && continue
    count=$((count + 1))
    result=$(check_license_against_policy "${spdx_id}" "${module}")
    case "${result}" in
      ALLOWED)    : ;;
      DISALLOWED)
        echo "VIOLATION: ${module} — ${spdx_id} (disallowed)" >> "${violations}"
        fail "Go: ${module} uses disallowed license: ${spdx_id}"
        fail_count=$((fail_count + 1))
        ;;
      AMBIGUOUS)
        echo "AMBIGUOUS: ${module} — ${spdx_id} (needs review)" >> "${violations}"
        warn "Go: ${module} uses ambiguous license: ${spdx_id} — add exception or to allowed list"
        fail_count=$((fail_count + 1))
        ;;
      UNKNOWN)
        echo "UNKNOWN: ${module} — ${spdx_id}" >> "${violations}"
        warn "Go: ${module} has unknown license identifier: '${spdx_id}'"
        warn_count=$((warn_count + 1))
        ;;
    esac
  done < "${csv}"

  log_summary "  Go: ${count} packages scanned, ${fail_count} violation(s), ${warn_count} warning(s)"

  if [[ "${fail_count}" -gt 0 ]]; then
    log_summary "  [FAIL] Go license policy violated — see ${violations}"
    TOTAL_FAILURES=$((TOTAL_FAILURES + fail_count))
  else
    log_summary "  [PASS] Go license policy satisfied"
  fi
}

# ── RUST LICENSE SCAN (cargo-deny) ───────────────────────────────────────────
scan_rust() {
  log_summary ""
  log_summary "=== Rust Dependencies ==="

  if ! command -v cargo-deny >/dev/null 2>&1; then
    if [[ "${AUTO_INSTALL}" == "1" ]]; then
      info "Installing cargo-deny ..."
      cargo install cargo-deny --locked
    else
      warn "cargo-deny not found. Install with: cargo install cargo-deny --locked"
      warn "Set AUTO_INSTALL=1 to install automatically."
      log_summary "  [SKIP] cargo-deny not installed"
      return
    fi
  fi

  local deny_out="${REPORT_DIR}/rust-deny.txt"
  local violations="${REPORT_DIR}/rust-violations.txt"
  > "${violations}"

  info "Running cargo deny check ..."
  local deny_exit=0
  cargo deny \
    --manifest-path "${REPO_ROOT}/simulator/Cargo.toml" \
    --config "${REPO_ROOT}/.cargo/deny.toml" \
    check 2>&1 | tee "${deny_out}" || deny_exit=$?

  # Extract violation lines from cargo-deny output
  grep -E '^\[' "${deny_out}" | grep -v '^\[INFO\]' >> "${violations}" 2>/dev/null || true

  if [[ "${deny_exit}" -ne 0 ]]; then
    local vcount
    vcount=$(grep -c '^\[ERROR\]\|\[DENY\]' "${deny_out}" 2>/dev/null || echo "?")
    log_summary "  [FAIL] Rust: cargo-deny found violations (${vcount} error(s)) — see ${deny_out}"
    TOTAL_FAILURES=$((TOTAL_FAILURES + 1))
  else
    log_summary "  [PASS] Rust license and advisory policy satisfied"
  fi
}

# ── NODE LICENSE SCAN (license-checker) ──────────────────────────────────────
scan_node() {
  log_summary ""
  log_summary "=== Node.js Dependencies ==="

  if ! command -v license-checker >/dev/null 2>&1; then
    if [[ "${AUTO_INSTALL}" == "1" ]]; then
      info "Installing license-checker ..."
      npm install -g license-checker --prefer-offline
    else
      warn "license-checker not found. Install with: npm install -g license-checker"
      warn "Set AUTO_INSTALL=1 to install automatically."
      log_summary "  [SKIP] license-checker not installed"
      return
    fi
  fi

  local json_out="${REPORT_DIR}/node-licenses.json"
  local violations="${REPORT_DIR}/node-violations.txt"
  > "${violations}"

  # Build comma-separated allowed list for --onlyAllow flag
  local allowed_csv
  allowed_csv=$(echo "${ALLOWED}" | tr '\n' ';' | sed 's/;$//')

  info "Running license-checker (production deps only) ..."
  local lc_exit=0
  license-checker \
    --start "${REPO_ROOT}" \
    --production \
    --json \
    --out "${json_out}" 2>"${REPORT_DIR}/node-licenses-stderr.txt" || lc_exit=$?

  if [[ "${lc_exit}" -ne 0 ]]; then
    warn "license-checker exited non-zero; check ${REPORT_DIR}/node-licenses-stderr.txt"
  fi

  # Parse the JSON output and check each package's license against policy
  local fail_count=0
  local warn_count=0
  local count=0

  if [[ -f "${json_out}" ]]; then
    python3 - "${json_out}" "${POLICY_FILE}" "${violations}" <<'PYEOF'
import json, sys

licenses_path = sys.argv[1]
policy_path   = sys.argv[2]
violations_path = sys.argv[3]

with open(licenses_path) as f:
    packages = json.load(f)
with open(policy_path) as f:
    policy = json.load(f)

allowed    = set(policy["allowed_licenses"])
disallowed = set(policy["disallowed_licenses"])
ambiguous  = set(policy.get("ambiguous_licenses", {}).get("list", []))

fail_count = 0
warn_count = 0
count = 0

violations = []
for pkg_name, info in packages.items():
    raw_license = info.get("licenses", "UNKNOWN")
    # license-checker may return "MIT AND ISC" or "(MIT OR Apache-2.0)"; split on AND/OR
    spdx_ids = [s.strip().strip("()") for s in raw_license.replace(" AND ", ";").replace(" OR ", ";").split(";")]
    count += 1
    for spdx_id in spdx_ids:
        spdx_id = spdx_id.strip()
        if spdx_id in disallowed:
            violations.append(f"VIOLATION: {pkg_name} — {spdx_id} (disallowed)")
            fail_count += 1
        elif spdx_id in ambiguous:
            violations.append(f"AMBIGUOUS: {pkg_name} — {spdx_id} (needs review)")
            fail_count += 1
        elif spdx_id not in allowed:
            violations.append(f"UNKNOWN: {pkg_name} — {spdx_id}")
            warn_count += 1

with open(violations_path, "w") as f:
    f.write("\n".join(violations) + ("\n" if violations else ""))

print(f"count={count}")
print(f"fail={fail_count}")
print(f"warn={warn_count}")
PYEOF

    counts_out=$(python3 - "${json_out}" "${POLICY_FILE}" "${violations}" 2>/dev/null || true)
    fail_count=$(echo "${counts_out}" | grep '^fail=' | cut -d= -f2 || echo 0)
    count=$(echo "${counts_out}" | grep '^count=' | cut -d= -f2 || echo 0)
    warn_count=$(echo "${counts_out}" | grep '^warn=' | cut -d= -f2 || echo 0)
  fi

  log_summary "  Node: ${count} packages scanned, ${fail_count:-0} violation(s), ${warn_count:-0} warning(s)"

  if [[ "${fail_count:-0}" -gt 0 ]]; then
    log_summary "  [FAIL] Node license policy violated — see ${violations}"
    TOTAL_FAILURES=$((TOTAL_FAILURES + fail_count))
  else
    log_summary "  [PASS] Node license policy satisfied"
  fi
}

# ── Run selected scans ────────────────────────────────────────────────────────
echo "License scan — policy: ${POLICY_FILE}"
echo "Reports: ${REPORT_DIR}"
echo ""

cd "${REPO_ROOT}"

[[ "${RUN_GO}"   -eq 1 ]] && scan_go
[[ "${RUN_RUST}" -eq 1 ]] && scan_rust
[[ "${RUN_NODE}" -eq 1 ]] && scan_node

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
log_summary ""
log_summary "=== Summary ==="
if [[ "${TOTAL_FAILURES}" -eq 0 ]]; then
  log_summary "Result: license policy satisfied across all scanned ecosystems."
  echo "Result: license policy satisfied."
  exit 0
else
  log_summary "Result: ${TOTAL_FAILURES} violation(s) found. See reports in ${REPORT_DIR}/"
  echo "Result: ${TOTAL_FAILURES} license violation(s) found." >&2
  echo "" >&2
  echo "Remediation:" >&2
  echo "  1. Review the violation(s) listed in ${REPORT_DIR}/*-violations.txt" >&2
  echo "  2. For each violation, either:" >&2
  echo "     a. Replace the dependency with one that uses an allowed license, OR" >&2
  echo "     b. Add a time-limited exception to license-policy.json with owner approval." >&2
  echo "  3. See docs/license-policy.md for the full exception process." >&2
  exit 1
fi
