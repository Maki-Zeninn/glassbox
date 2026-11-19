#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# verify-release.sh — smoke-test release artifacts
#
# Usage: scripts/verify-release.sh [dist/release]
#
# Checks:
#   1. Each expected binary exists and is non-empty.
#   2. The native binary (matching the current OS/arch) executes and
#      prints version information.
#   3. SHA-256 checksums file exists and all listed files verify.
#   4. version.txt contains non-empty version, commit, and build_date fields.

set -euo pipefail

DIST_DIR="${1:-dist/release}"

pass() { printf '  [PASS] %s\n' "$*"; }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAILURES=$((FAILURES + 1)); }

FAILURES=0

echo "Verifying release artifacts in: ${DIST_DIR}"
echo ""

# ── 1. Expected binaries exist ────────────────────────────────────────────────
echo "1. Checking binary presence..."
EXPECTED=(
  "glassbox-linux-amd64"
  "glassbox-linux-arm64"
  "glassbox-darwin-amd64"
  "glassbox-darwin-arm64"
  "glassbox-windows-amd64.exe"
)
for bin in "${EXPECTED[@]}"; do
  path="${DIST_DIR}/${bin}"
  if [ -f "${path}" ] && [ -s "${path}" ]; then
    size=$(wc -c < "${path}")
    pass "${bin} (${size} bytes)"
  else
    fail "${bin} missing or empty"
  fi
done

# ── 2. Per-artifact smoke tests ───────────────────────────────────────────────
#
# Every supported artifact is exercised, not just the native one: startup and
# a set of offline, network-free commands (version, help, demo, dry-run, and
# JSON output) are run for each binary. A binary is only ever [FAIL]ed when a
# runner capable of executing it was actually available; when no runner
# exists for a given artifact's platform, its commands are reported as
# [SKIP-UNAVAILABLE] so a missing emulator/runner is never conflated with a
# real regression. Metadata (artifact, platform, command, exit code, and a
# short output excerpt) is recorded in SMOKE_LOG for the release record.
echo ""
echo "2. Smoke-testing every supported binary..."

HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
HOST_ARCH="$(uname -m)"
case "${HOST_ARCH}" in
  x86_64)  HOST_ARCH="amd64" ;;
  aarch64|arm64) HOST_ARCH="arm64" ;;
  *) HOST_ARCH="amd64" ;;
esac

SMOKE_LOG="${DIST_DIR}/smoke-results.log"
: > "${SMOKE_LOG}" 2>/dev/null || SMOKE_LOG="/dev/null"

# runner_for_artifact prints, on stdout, the command prefix (possibly via an
# emulator) needed to execute a given binary on this host, or nothing if no
# runner is available. artifact_os/artifact_arch describe the target.
runner_for_artifact() {
  artifact_os="$1"
  artifact_arch="$2"
  bin_path="$3"

  if [ "${artifact_os}" = "${HOST_OS}" ] && [ "${artifact_arch}" = "${HOST_ARCH}" ]; then
    printf '%s' "${bin_path}"
    return 0
  fi

  if [ "${artifact_os}" = "windows" ] && command -v wine >/dev/null 2>&1; then
    printf 'wine %s' "${bin_path}"
    return 0
  fi

  if [ "${artifact_os}" = "linux" ] && [ "${HOST_OS}" = "linux" ]; then
    case "${artifact_arch}" in
      amd64) emu=qemu-x86_64-static ;;
      arm64) emu=qemu-aarch64-static ;;
      *) emu="" ;;
    esac
    if [ -n "${emu}" ] && command -v "${emu}" >/dev/null 2>&1; then
      printf '%s %s' "${emu}" "${bin_path}"
      return 0
    fi
  fi

  return 1
}

# run_smoke_command executes one offline command against one artifact and
# records PASS/FAIL/SKIP-UNAVAILABLE. It never requires network access,
# credentials, or a deployed contract — every command below is a local,
# read-only or dry-run operation.
run_smoke_command() {
  artifact="$1"; shift
  label="$1"; shift
  # remaining args: the command to run

  set +e
  cmd_output=$("$@" 2>&1)
  cmd_exit=$?
  set -e

  excerpt=$(printf '%s' "${cmd_output}" | head -1 | cut -c1-120)
  printf 'artifact=%s command=%s exit=%d output=%q\n' "${artifact}" "${label}" "${cmd_exit}" "${excerpt}" >> "${SMOKE_LOG}"

  if [ "${cmd_exit}" -eq 0 ] && [ -n "${cmd_output}" ]; then
    pass "${artifact} ${label}: ${excerpt}"
    return 0
  fi
  fail "${artifact} ${label} failed (exit ${cmd_exit}): ${excerpt}"
  return 1
}

for bin in "${EXPECTED[@]}"; do
  bin_path="${DIST_DIR}/${bin}"
  if [ ! -f "${bin_path}" ]; then
    # Already reported missing in step 1; nothing more to smoke-test.
    continue
  fi

  # Derive platform from the artifact filename glassbox-<os>-<arch>[.exe].
  rest="${bin#glassbox-}"
  rest="${rest%.exe}"
  artifact_os="${rest%-*}"
  artifact_arch="${rest##*-}"

  chmod +x "${bin_path}" 2>/dev/null || true

  if ! runner=$(runner_for_artifact "${artifact_os}" "${artifact_arch}" "${bin_path}"); then
    echo "  [SKIP-UNAVAILABLE] ${bin}: no runner/emulator on this host for ${artifact_os}/${artifact_arch}"
    printf 'artifact=%s command=* exit=SKIP-UNAVAILABLE reason=%q\n' "${bin}" "no runner for ${artifact_os}/${artifact_arch}" >> "${SMOKE_LOG}"
    continue
  fi

  # shellcheck disable=SC2206
  runner_args=(${runner})

  run_smoke_command "${bin}" "version"  "${runner_args[@]}" --version || true
  run_smoke_command "${bin}" "help"     "${runner_args[@]}" --help || true
  run_smoke_command "${bin}" "demo"     "${runner_args[@]}" debug --demo || true
  run_smoke_command "${bin}" "dry-run"  "${runner_args[@]}" debug --dry-run --help || true
  run_smoke_command "${bin}" "json"     "${runner_args[@]}" version --json || true
done

# ── 3. Checksums verify ───────────────────────────────────────────────────────
echo ""
echo "3. Verifying checksums..."
CHECKSUM_FILE="${DIST_DIR}/checksums.sha256"
if [ ! -f "${CHECKSUM_FILE}" ]; then
  fail "checksums.sha256 not found"
else
  if command -v sha256sum >/dev/null 2>&1; then
    if (cd "${DIST_DIR}" && sha256sum --check checksums.sha256 --quiet 2>&1); then
      pass "all checksums verified (sha256sum)"
    else
      fail "checksum verification failed"
    fi
  elif command -v shasum >/dev/null 2>&1; then
    if (cd "${DIST_DIR}" && shasum -a 256 --check checksums.sha256 --quiet 2>&1); then
      pass "all checksums verified (shasum)"
    else
      fail "checksum verification failed"
    fi
  else
    echo "  [SKIP] no sha256sum or shasum available"
  fi
fi

# ── 4. version.txt ────────────────────────────────────────────────────────────
echo ""
echo "4. Checking version metadata..."
VERSION_FILE="${DIST_DIR}/version.txt"
if [ ! -f "${VERSION_FILE}" ]; then
  fail "version.txt not found"
else
  version=$(grep '^version=' "${VERSION_FILE}" | cut -d= -f2)
  commit=$(grep '^commit=' "${VERSION_FILE}" | cut -d= -f2)
  build_date=$(grep '^build_date=' "${VERSION_FILE}" | cut -d= -f2)

  [ -n "${version}" ]    && pass "version=${version}"    || fail "version field empty"
  [ -n "${commit}" ]     && pass "commit=${commit}"      || fail "commit field empty"
  [ -n "${build_date}" ] && pass "build_date=${build_date}" || fail "build_date field empty"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [ "${FAILURES}" -eq 0 ]; then
  echo "Result: all release verification checks passed."
  exit 0
else
  echo "Result: ${FAILURES} check(s) failed." >&2
  exit 1
fi
