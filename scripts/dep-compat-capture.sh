#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# scripts/dep-compat-capture.sh
#
# Captures golden-baseline JSON outputs from the deterministic Glassbox
# harness for each dependency group and output kind.
#
# Each capture invokes the harness with a pinned, deterministic test vector
# (canonical transaction hash, fixed ledger sequence, offline/mock mode) and
# writes the resulting JSON to a staging directory.
#
# Usage:
#   scripts/dep-compat-capture.sh [options]
#
# Options:
#   --dep-group GROUP   Only capture outputs for GROUP (stellar-sdk | soroban-host |
#                       crypto | rpc-client). Default: all groups.
#   --output-dir DIR    Directory to write captured JSON files into.
#                       Default: /tmp/depcompat-capture-<timestamp>
#   --update-golden     After successful capture, overwrite golden baselines at
#                       internal/depcompat/testdata/golden/
#   --glassbox-bin BIN  Path to the glassbox binary. Default: ./bin/glassbox
#   --sim-bin BIN       Path to the simulator binary.
#                       Default: ./simulator/target/release/glassbox-sim
#   --dry-run           Print what would be captured but do not execute.
#   -v, --verbose       Print each harness invocation.
#   -h, --help          Show this help and exit.
#
# Environment variables (override defaults):
#   GLASSBOX_BIN        Same as --glassbox-bin.
#   GLASSBOX_SIM_PATH   Same as --sim-bin.
#   DEP_COMPAT_OUTPUT   Same as --output-dir.
#
# Exit codes:
#   0   All captures succeeded.
#   1   One or more captures failed.
#   2   Usage error.

set -euo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOLDEN_DIR="${REPO_ROOT}/internal/depcompat/testdata/golden"
SCRIPT_VERSION="1.0"

# Canonical test vector — matches the constants in internal/testhelpers/cli.go
# and internal/trace/golden_fixtures_test.go.
CANONICAL_TX_HASH="5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
CANONICAL_CONTRACT_ID="CTESTAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
CANONICAL_LEDGER_SEQ="100"
CANONICAL_TIMESTAMP="2026-01-01T00:00:00Z"

# All supported dependency groups.
ALL_DEP_GROUPS=(stellar-sdk soroban-host crypto rpc-client)

# All output kinds.
ALL_OUTPUT_KINDS=(replay trace audit binding)

# ─── Defaults ─────────────────────────────────────────────────────────────────

GLASSBOX_BIN="${GLASSBOX_BIN:-${REPO_ROOT}/bin/glassbox}"
SIM_BIN="${GLASSBOX_SIM_PATH:-${REPO_ROOT}/simulator/target/release/glassbox-sim}"
OUTPUT_DIR="${DEP_COMPAT_OUTPUT:-/tmp/depcompat-capture-$(date +%s)}"
DEP_GROUP=""
UPDATE_GOLDEN=false
DRY_RUN=false
VERBOSE=false

# ─── Colour helpers ───────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}   $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $*" >&2; }

# ─── Argument parsing ─────────────────────────────────────────────────────────

usage() {
  grep '^# ' "${BASH_SOURCE[0]}" | sed 's/^# //' | sed -n '/^Usage:/,/^Exit codes:/p'
  exit 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dep-group)      DEP_GROUP="$2";       shift 2 ;;
    --output-dir)     OUTPUT_DIR="$2";      shift 2 ;;
    --update-golden)  UPDATE_GOLDEN=true;   shift   ;;
    --glassbox-bin)   GLASSBOX_BIN="$2";   shift 2 ;;
    --sim-bin)        SIM_BIN="$2";        shift 2 ;;
    --dry-run)        DRY_RUN=true;        shift   ;;
    -v|--verbose)     VERBOSE=true;        shift   ;;
    -h|--help)        usage ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

# Validate --dep-group if given.
# NOTE: We use DEP_GROUPS_TO_RUN to avoid colliding with bash's read-only
# GROUPS special variable (which expands to the current user's supplementary
# group IDs).
if [[ -n "${DEP_GROUP}" ]]; then
  valid=false
  for g in "${ALL_DEP_GROUPS[@]}"; do
    [[ "${g}" == "${DEP_GROUP}" ]] && valid=true && break
  done
  if ! "${valid}"; then
    fail "--dep-group must be one of: ${ALL_DEP_GROUPS[*]}"
    exit 2
  fi
  DEP_GROUPS_TO_RUN=("${DEP_GROUP}")
else
  DEP_GROUPS_TO_RUN=("${ALL_DEP_GROUPS[@]}")
fi

# ─── Version resolution ───────────────────────────────────────────────────────

# Declared at file scope so they are visible inside main() after resolve_versions().
STELLAR_SDK_VER="unknown"
SOROBAN_HOST_VER="unknown"
ED25519_VER="unknown"
SHA2_VER="unknown"
GO_VER="unknown"
RUST_VER="unknown"

resolve_versions() {
  info "Resolving dependency versions..."

  STELLAR_SDK_VER="$(cd "${REPO_ROOT}" && \
    go list -m -json github.com/stellar/go-stellar-sdk 2>/dev/null \
    | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('Version','unknown'))" \
    2>/dev/null || echo unknown)"

  GO_VER="$(go version 2>/dev/null | awk '{print $3}' || echo unknown)"
  RUST_VER="$(rustc --version 2>/dev/null || echo unknown)"

  CARGO_LOCK="${REPO_ROOT}/simulator/Cargo.lock"
  if [[ -f "${CARGO_LOCK}" ]]; then
    _extract_cargo_version() {
      local pkg="$1"
      python3 -c "
import sys, re
lock = open('${CARGO_LOCK}').read()
m = re.search(r'name = \"' + re.escape('${pkg}') + r'\"\nversion = \"([^\"]+)\"', lock)
print(m.group(1) if m else 'unknown')
" 2>/dev/null || echo unknown
    }
    SOROBAN_HOST_VER="$(_extract_cargo_version soroban-env-host)"
    ED25519_VER="$(_extract_cargo_version ed25519-dalek)"
    SHA2_VER="$(_extract_cargo_version sha2)"
  fi

  cat <<EOF
  go-stellar-sdk  : ${STELLAR_SDK_VER}
  soroban-env-host: ${SOROBAN_HOST_VER}
  ed25519-dalek   : ${ED25519_VER}
  sha2            : ${SHA2_VER}
  Go              : ${GO_VER}
  Rust            : ${RUST_VER}
EOF
}

# ─── Capture helpers ──────────────────────────────────────────────────────────

# run_capture GROUP KIND OUT_FILE
# Invokes the appropriate harness command and writes the output JSON to OUT_FILE.
run_capture() {
  local grp="$1" knd="$2" out="$3"
  local cmd=()

  # All captures run offline against the deterministic test vector.
  case "${knd}" in
    replay)
      cmd=(
        "${GLASSBOX_BIN}" debug
        "--tx-hash=${CANONICAL_TX_HASH}"
        --offline --json
        "--dep-compat-group=${grp}"
        "--ledger-seq=${CANONICAL_LEDGER_SEQ}"
      )
      ;;
    trace)
      cmd=(
        "${GLASSBOX_BIN}" debug
        "--tx-hash=${CANONICAL_TX_HASH}"
        --offline --json
        "--dep-compat-group=${grp}"
        "--ledger-seq=${CANONICAL_LEDGER_SEQ}"
        --trace-output=/dev/stdout
      )
      ;;
    audit)
      cmd=(
        "${GLASSBOX_BIN}" "audit:sign"
        "--payload={\"input\":{},\"state\":{},\"events\":[],\"timestamp\":\"${CANONICAL_TIMESTAMP}\"}"
        --json
        "--dep-compat-group=${grp}"
      )
      ;;
    binding)
      cmd=(
        "${GLASSBOX_BIN}" "generate:bindings"
        "--contract-id=${CANONICAL_CONTRACT_ID}"
        --offline --json
        "--dep-compat-group=${grp}"
      )
      ;;
  esac

  if "${VERBOSE}"; then
    info "  Running: ${cmd[*]}"
  fi

  if "${DRY_RUN}"; then
    info "  [dry-run] Would write to: ${out}"
    echo '{"dry_run":true}' > "${out}"
    return 0
  fi

  local env_vars=(
    GLASSBOX_TELEMETRY=false
    GLASSBOX_SIM_PATH="${SIM_BIN}"
    GLASSBOX_DEP_COMPAT_CAPTURE=1
    GLASSBOX_FIXED_TIMESTAMP="${CANONICAL_TIMESTAMP}"
  )

  local tmp_out="${out}.tmp"
  if env "${env_vars[@]}" "${cmd[@]}" > "${tmp_out}" 2>&1; then
    mv "${tmp_out}" "${out}"
    return 0
  else
    local exit_code=$?
    # Wrap the raw output in an error sentinel JSON so compare can report it.
    python3 -c "
import json, sys
raw = open('${tmp_out}').read()
print(json.dumps({'capture_error': True, 'exit_code': ${exit_code}, 'output': raw[:4096]}))
" > "${out}" 2>/dev/null || echo '{"capture_error":true}' > "${out}"
    rm -f "${tmp_out}"
    return 1
  fi
}

# inject_metadata GROUP KIND OUT_FILE
# Injects dep_group, output_kind, captured_at, and version metadata into
# the captured JSON file using python3.
inject_metadata() {
  local grp="$1" knd="$2" file="$3"
  python3 -c "
import json, sys
from datetime import datetime, timezone

path, dep_group, output_kind = '$file', '$grp', '$knd'
sdk_ver, soroban_ver, ed25519_ver, sha2_ver, go_ver, rust_ver = \
    '${STELLAR_SDK_VER}', '${SOROBAN_HOST_VER}', '${ED25519_VER}', \
    '${SHA2_VER}', '${GO_VER}', '${RUST_VER}'

try:
    data = json.loads(open(path).read())
except Exception:
    data = {}

data.setdefault('schema_version', '1')
data['dep_group']   = dep_group
data['output_kind'] = output_kind
data['captured_at'] = datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
data['dep_versions'] = {
    'stellar_sdk':  sdk_ver,
    'soroban_host': soroban_ver,
    'ed25519_dalek': ed25519_ver,
    'sha2':         sha2_ver,
    'go':           go_ver,
    'rust':         rust_ver,
}

with open(path, 'w') as f:
    json.dump(data, f, indent=2, sort_keys=True)
    f.write('\n')
" 2>/dev/null || warn "  inject_metadata failed for ${grp}/${knd}"
}

# ─── Main ─────────────────────────────────────────────────────────────────────

main() {
  echo "dep-compat-capture.sh v${SCRIPT_VERSION}"
  echo "Repo root : ${REPO_ROOT}"
  echo "Output dir: ${OUTPUT_DIR}"
  echo ""

  mkdir -p "${OUTPUT_DIR}"
  resolve_versions
  echo ""

  # Check prerequisites (only when not dry-running).
  local stub_mode=false
  if ! "${DRY_RUN}"; then
    if [[ ! -x "${GLASSBOX_BIN}" ]]; then
      warn "glassbox binary not found at ${GLASSBOX_BIN}."
      warn "Run 'make build' first, or pass --glassbox-bin."
      warn "Continuing in stub-capture mode (metadata-only JSON)."
      stub_mode=true
    fi
  fi

  local failures=0
  local total=0

  for grp in "${DEP_GROUPS_TO_RUN[@]}"; do
    info "Capturing group: ${grp}"
    for knd in "${ALL_OUTPUT_KINDS[@]}"; do
      total=$((total + 1))
      local out="${OUTPUT_DIR}/${grp}-${knd}.json"

      if "${stub_mode}"; then
        # Produce a minimal stub so the compare script can run without a binary.
        cat > "${out}" <<STUBEOF
{
  "schema_version": "1",
  "stub": true,
  "dep_group": "${grp}",
  "output_kind": "${knd}"
}
STUBEOF
        inject_metadata "${grp}" "${knd}" "${out}"
        ok "  ${grp}/${knd} -> stub"
        continue
      fi

      if run_capture "${grp}" "${knd}" "${out}"; then
        inject_metadata "${grp}" "${knd}" "${out}"
        ok "  ${grp}/${knd} -> ${out}"
      else
        fail "  ${grp}/${knd} FAILED"
        failures=$((failures + 1))
      fi
    done
  done

  echo ""
  info "Capture complete: $((total - failures))/${total} outputs captured."

  # Optionally overwrite golden baselines.
  if "${UPDATE_GOLDEN}"; then
    echo ""
    info "Updating golden baselines in ${GOLDEN_DIR}..."
    mkdir -p "${GOLDEN_DIR}"
    for grp in "${DEP_GROUPS_TO_RUN[@]}"; do
      for knd in "${ALL_OUTPUT_KINDS[@]}"; do
        local src="${OUTPUT_DIR}/${grp}-${knd}.json"
        local dst="${GOLDEN_DIR}/${grp}-${knd}.golden.json"
        if [[ -f "${src}" ]]; then
          cp "${src}" "${dst}"
          ok "  Updated: ${dst}"
        else
          warn "  Missing source: ${src}"
        fi
      done
    done
    echo ""
    info "Golden baselines updated. Review the diff with:"
    info "  git diff internal/depcompat/testdata/golden/"
    info "Commit the changes only after verifying the new baselines are correct."
  fi

  if [[ "${failures}" -gt 0 ]]; then
    fail "${failures} capture(s) failed. See ${OUTPUT_DIR} for details."
    exit 1
  fi

  echo ""
  ok "All captures succeeded. Output directory: ${OUTPUT_DIR}"
}

main "$@"
