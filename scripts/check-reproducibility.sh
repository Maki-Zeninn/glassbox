#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# check-reproducibility.sh — verify that two clean builds from the same source
# produce byte-identical binaries and archives.
#
# Usage:
#   scripts/check-reproducibility.sh [target]
#
#   target   Go platform target to build (default: linux-amd64).
#            Supported: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64
#
# What it does:
#   1. Resolves SOURCE_DATE_EPOCH from the HEAD commit (or the environment).
#   2. Builds the Go binary twice in separate GOPATH/GOCACHE directories so
#      caches cannot carry state between builds.
#   3. Builds the tar.gz archive from each binary using the same reproducible
#      flags as the Makefile package target.
#   4. Computes SHA-256 of each binary and archive and compares them.
#   5. On mismatch, runs diffoscope (if installed) to show what changed, then
#      exits non-zero with an actionable error message.
#
# Exit codes:
#   0  Both builds produce identical output.
#   1  Outputs differ or a build step failed.
#
# Environment:
#   SOURCE_DATE_EPOCH   Clamped mtime for archives (default: git log -1 --format=%ct).
#   GLASSBOX_VERSION    Version string injected via ldflags (default: git describe).
#   GLASSBOX_COMMIT     Commit SHA (default: git rev-parse HEAD).
#   GLASSBOX_BUILD_DATE Build date injected via ldflags (default: derived from epoch).
#   KEEP_BUILD_DIRS     Set to "1" to keep temp dirs after the script exits.
#
# Requirements:
#   go (version from toolchain.json / go.mod), tar, sha256sum or shasum.
#   Optional: diffoscope (for detailed diff on mismatch).

set -euo pipefail

TARGET="${1:-linux-amd64}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; }
info()  { printf '  [INFO] %s\n' "$*"; }
warn()  { printf '  [WARN] %s\n' "$*"; }

FAILURES=0

# ── Resolve build inputs ──────────────────────────────────────────────────────
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${REPO_ROOT}" log -1 --format=%ct 2>/dev/null || echo "0")}"
GLASSBOX_VERSION="${GLASSBOX_VERSION:-$(git -C "${REPO_ROOT}" describe --tags --always --dirty 2>/dev/null || echo "dev")}"
GLASSBOX_COMMIT="${GLASSBOX_COMMIT:-$(git -C "${REPO_ROOT}" rev-parse HEAD 2>/dev/null || echo "unknown")}"
# Derive a deterministic build date from SOURCE_DATE_EPOCH so the date is
# identical across both builds (using wall-clock time would differ by milliseconds).
GLASSBOX_BUILD_DATE="${GLASSBOX_BUILD_DATE:-$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
  || date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
  || echo "1970-01-01T00:00:00Z")}"

# Map target to GOOS/GOARCH
case "${TARGET}" in
  linux-amd64)   GOOS=linux;   GOARCH=amd64 ;;
  linux-arm64)   GOOS=linux;   GOARCH=arm64 ;;
  darwin-amd64)  GOOS=darwin;  GOARCH=amd64 ;;
  darwin-arm64)  GOOS=darwin;  GOARCH=arm64 ;;
  windows-amd64) GOOS=windows; GOARCH=amd64 ;;
  *) echo "Unknown target: ${TARGET}. Supported: linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64" >&2; exit 1 ;;
esac
BINARY_NAME="glassbox-${TARGET}"
[[ "${GOOS}" == "windows" ]] && BINARY_NAME="${BINARY_NAME}.exe"
ARCHIVE_NAME="${BINARY_NAME}.tar.gz"
[[ "${GOOS}" == "windows" ]] && ARCHIVE_NAME="${BINARY_NAME%.exe}.zip"

echo "Reproducibility check"
echo "  Target             : ${TARGET} (${GOOS}/${GOARCH})"
echo "  SOURCE_DATE_EPOCH  : ${SOURCE_DATE_EPOCH}"
echo "  GLASSBOX_VERSION   : ${GLASSBOX_VERSION}"
echo "  GLASSBOX_COMMIT    : ${GLASSBOX_COMMIT:0:12}..."
echo "  GLASSBOX_BUILD_DATE: ${GLASSBOX_BUILD_DATE}"
echo ""

# ── Temp directories (isolated GOPATH + GOCACHE per build) ───────────────────
BUILD1="$(mktemp -d)"
BUILD2="$(mktemp -d)"

cleanup() {
  if [[ "${KEEP_BUILD_DIRS:-0}" != "1" ]]; then
    rm -rf "${BUILD1}" "${BUILD2}"
  else
    info "Build dirs kept: ${BUILD1}  ${BUILD2}"
  fi
}
trap cleanup EXIT

# ── Build function ────────────────────────────────────────────────────────────
build_once() {
  local dir="$1"
  local label="$2"
  local gopath="${dir}/gopath"
  local gocache="${dir}/gocache"
  local out_bin="${dir}/${BINARY_NAME}"
  local out_archive="${dir}/${ARCHIVE_NAME}"

  mkdir -p "${gopath}" "${gocache}"

  info "Build ${label}: ${out_bin}"

  GOPATH="${gopath}" \
  GOCACHE="${gocache}" \
  GOOS="${GOOS}" \
  GOARCH="${GOARCH}" \
  CGO_ENABLED=0 \
  SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    go build \
      -trimpath \
      -ldflags "-s -w \
        -X 'github.com/dotandev/glassbox/internal/version.Version=${GLASSBOX_VERSION}' \
        -X 'github.com/dotandev/glassbox/internal/version.CommitSHA=${GLASSBOX_COMMIT}' \
        -X 'github.com/dotandev/glassbox/internal/version.BuildDate=${GLASSBOX_BUILD_DATE}'" \
      -o "${out_bin}" \
      "${REPO_ROOT}/cmd/glassbox"

  info "Archive ${label}: ${out_archive}"
  if [[ "${GOOS}" == "windows" ]]; then
    (cd "${dir}" && zip --no-dir-entries -X -9 "${ARCHIVE_NAME}" "${BINARY_NAME}")
  else
    tar --sort=name \
        --owner=0 --group=0 --numeric-owner \
        --mtime="@${SOURCE_DATE_EPOCH}" \
        -czf "${out_archive}" \
        -C "${dir}" "${BINARY_NAME}"
  fi
}

# ── Run two builds ────────────────────────────────────────────────────────────
echo "1. First build ..."
build_once "${BUILD1}" "1"

echo ""
echo "2. Second build ..."
build_once "${BUILD2}" "2"

# ── Compare ───────────────────────────────────────────────────────────────────
echo ""
echo "3. Comparing outputs ..."

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# Binary comparison
HASH1_BIN="$(sha256 "${BUILD1}/${BINARY_NAME}")"
HASH2_BIN="$(sha256 "${BUILD2}/${BINARY_NAME}")"
if [[ "${HASH1_BIN}" == "${HASH2_BIN}" ]]; then
  pass "Binary identical: ${HASH1_BIN:0:16}..."
else
  fail "Binary DIFFERS"
  echo "    build1: ${HASH1_BIN}" >&2
  echo "    build2: ${HASH2_BIN}" >&2
  FAILURES=$((FAILURES + 1))
fi

# Archive comparison
HASH1_ARC="$(sha256 "${BUILD1}/${ARCHIVE_NAME}")"
HASH2_ARC="$(sha256 "${BUILD2}/${ARCHIVE_NAME}")"
if [[ "${HASH1_ARC}" == "${HASH2_ARC}" ]]; then
  pass "Archive identical: ${HASH1_ARC:0:16}..."
else
  fail "Archive DIFFERS"
  echo "    build1: ${HASH1_ARC}" >&2
  echo "    build2: ${HASH2_ARC}" >&2
  FAILURES=$((FAILURES + 1))
fi

# ── Diffoscope detail on mismatch ─────────────────────────────────────────────
if [[ "${FAILURES}" -gt 0 ]]; then
  echo ""
  if command -v diffoscope >/dev/null 2>&1; then
    echo "Running diffoscope to identify non-deterministic content ..."
    diffoscope "${BUILD1}/${BINARY_NAME}" "${BUILD2}/${BINARY_NAME}" || true
  else
    warn "diffoscope not installed — install it for detailed diff output:"
    warn "  pip install diffoscope   OR   apt install diffoscope"
    warn "  Then re-run: KEEP_BUILD_DIRS=1 $0 ${TARGET}"
    warn "  And compare: diffoscope ${BUILD1}/${BINARY_NAME} ${BUILD2}/${BINARY_NAME}"
  fi
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [[ "${FAILURES}" -eq 0 ]]; then
  echo "Result: builds are reproducible for target ${TARGET}."
  echo "  Binary hash : ${HASH1_BIN}"
  echo "  Archive hash: ${HASH1_ARC}"
  exit 0
else
  echo "Result: ${FAILURES} reproducibility check(s) failed for target ${TARGET}." >&2
  echo "" >&2
  echo "Troubleshooting steps:" >&2
  echo "  1. Confirm SOURCE_DATE_EPOCH is set identically in both builds." >&2
  echo "     Current value: ${SOURCE_DATE_EPOCH}" >&2
  echo "  2. Ensure -trimpath is present in all go build invocations." >&2
  echo "  3. Check for embedded timestamps: grep -r 'time.Now()' internal/" >&2
  echo "  4. Check for random map iteration in JSON serialisation paths." >&2
  echo "  5. See docs/reproducible-builds.md for the full troubleshooting guide." >&2
  exit 1
fi
