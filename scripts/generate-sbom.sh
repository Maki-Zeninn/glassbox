#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# generate-sbom.sh — produce a versioned SPDX 2.3 JSON SBOM for a Glassbox
# release by collecting dependency information from all three ecosystems (Go
# modules, Cargo crates, npm packages) and delegating to the Go-based
# generate-sbom tool.
#
# Usage:
#   scripts/generate-sbom.sh [dist/release]
#
# Optional environment variables:
#   VERSION           Release version string (default: git describe)
#   COMMIT_SHA        Full git commit SHA   (default: git rev-parse HEAD)
#   SBOM_TOOL         Path to pre-built generate-sbom binary (built if absent)
#   GO_MODULES_JSON   Path to write go list output (default: /tmp/go-modules-<pid>.json)
#   CARGO_LOCK        Path to Cargo.lock (default: simulator/Cargo.lock)
#   PACKAGE_LOCK      Path to package-lock.json (default: package-lock.json)
#   DIST_DIR          Output directory (default: first argument, then dist/release)
#   SKIP_GO           Set to "1" to skip Go module collection
#   SKIP_CARGO        Set to "1" to skip Cargo.lock collection
#   SKIP_NPM          Set to "1" to skip package-lock.json collection
#
# Output:
#   <dist>/glassbox-<version>.spdx.json   — SPDX 2.3 SBOM
#
# The SBOM filename is printed to stdout on success so shell scripts and CI
# steps can capture it:
#   SBOM_FILE=$(bash scripts/generate-sbom.sh dist/release)
#
# The generate-sbom binary is the authoritative generator; this script is
# responsible only for:
#   1. Running `go list -m -json all` to produce the Go module inventory.
#   2. Locating the Cargo.lock and package-lock.json files.
#   3. Invoking the binary with all three inputs.
#   4. Confirming the output file is valid JSON before returning its path.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${1:-${DIST_DIR:-dist/release}}"

pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; exit 1; }
info()  { printf '  [INFO] %s\n' "$*"; }
warn()  { printf '  [WARN] %s\n' "$*"; }

# ── Resolve metadata ──────────────────────────────────────────────────────────
VERSION="${VERSION:-$(git -C "${REPO_ROOT}" describe --tags --always --dirty 2>/dev/null || echo "dev")}"
COMMIT_SHA="${COMMIT_SHA:-$(git -C "${REPO_ROOT}" rev-parse HEAD 2>/dev/null || echo "unknown")}"

# Strip a leading "v" for the output filename but keep VERSION intact for the
# SBOM document (the tool accepts "v1.2.3").
VERSION_BARE="${VERSION#v}"
SBOM_OUT="${DIST_DIR}/glassbox-${VERSION}.spdx.json"

info "SBOM generation"
info "  Version   : ${VERSION}"
info "  Commit    : ${COMMIT_SHA:0:12}..."
info "  Output    : ${SBOM_OUT}"
info ""

# ── Output directory ──────────────────────────────────────────────────────────
mkdir -p "${DIST_DIR}"

# ── Build generate-sbom tool if not already available ────────────────────────
SBOM_TOOL="${SBOM_TOOL:-}"
if [ -z "${SBOM_TOOL}" ]; then
  TOOL_BIN="${REPO_ROOT}/bin/generate-sbom"
  if [ ! -x "${TOOL_BIN}" ]; then
    info "Building generate-sbom tool ..."
    go build -o "${TOOL_BIN}" "${REPO_ROOT}/cmd/generate-sbom"
    pass "Tool built: ${TOOL_BIN}"
  else
    info "Using pre-built tool: ${TOOL_BIN}"
  fi
  SBOM_TOOL="${TOOL_BIN}"
fi

if [ ! -x "${SBOM_TOOL}" ]; then
  fail "generate-sbom binary not executable: ${SBOM_TOOL}"
fi

# ── Collect ecosystem inputs ──────────────────────────────────────────────────
EXTRA_FLAGS=()

# -- Go modules ---------------------------------------------------------------
SKIP_GO="${SKIP_GO:-0}"
if [ "${SKIP_GO}" != "1" ]; then
  GO_MODULES_JSON="${GO_MODULES_JSON:-$(mktemp "/tmp/go-modules-$$.json")}"
  info "Collecting Go modules (go list -m -json all) ..."
  if go list -m -json all > "${GO_MODULES_JSON}" 2>/dev/null; then
    MODULE_COUNT=$(grep -c '"Path"' "${GO_MODULES_JSON}" 2>/dev/null || echo "?")
    pass "Go modules collected: ${MODULE_COUNT} module(s) → ${GO_MODULES_JSON}"
    EXTRA_FLAGS+=(--go-modules "${GO_MODULES_JSON}")
  else
    warn "go list failed; skipping Go modules in SBOM"
  fi
else
  info "Skipping Go modules (SKIP_GO=1)"
fi

# -- Cargo.lock ---------------------------------------------------------------
SKIP_CARGO="${SKIP_CARGO:-0}"
if [ "${SKIP_CARGO}" != "1" ]; then
  CARGO_LOCK="${CARGO_LOCK:-${REPO_ROOT}/simulator/Cargo.lock}"
  if [ -f "${CARGO_LOCK}" ]; then
    CRATE_COUNT=$(grep -c '^\[\[package\]\]' "${CARGO_LOCK}" 2>/dev/null || echo "?")
    info "Cargo.lock found: ${CRATE_COUNT} package(s) in ${CARGO_LOCK}"
    EXTRA_FLAGS+=(--cargo-lock "${CARGO_LOCK}")
  else
    warn "Cargo.lock not found at ${CARGO_LOCK}; skipping Cargo crates in SBOM"
  fi
else
  info "Skipping Cargo.lock (SKIP_CARGO=1)"
fi

# -- package-lock.json --------------------------------------------------------
SKIP_NPM="${SKIP_NPM:-0}"
if [ "${SKIP_NPM}" != "1" ]; then
  PACKAGE_LOCK="${PACKAGE_LOCK:-${REPO_ROOT}/package-lock.json}"
  if [ -f "${PACKAGE_LOCK}" ]; then
    NPM_COUNT=$(python3 -c "
import json, sys
d = json.load(open(sys.argv[1]))
pkgs = d.get('packages', d.get('dependencies', {}))
print(sum(1 for k in pkgs if k and k != '.'))
" "${PACKAGE_LOCK}" 2>/dev/null || echo "?")
    info "package-lock.json found: ${NPM_COUNT} package(s) in ${PACKAGE_LOCK}"
    EXTRA_FLAGS+=(--package-lock "${PACKAGE_LOCK}")
  else
    warn "package-lock.json not found at ${PACKAGE_LOCK}; skipping npm packages in SBOM"
  fi
else
  info "Skipping package-lock.json (SKIP_NPM=1)"
fi

# Require at least one input to have been collected.
if [ "${#EXTRA_FLAGS[@]}" -eq 0 ]; then
  fail "No ecosystem inputs collected. At least one of go list, Cargo.lock, or package-lock.json must be available."
fi

# ── Run generate-sbom ─────────────────────────────────────────────────────────
info ""
info "Running generate-sbom ..."

"${SBOM_TOOL}" \
  --version     "${VERSION}" \
  --commit      "${COMMIT_SHA}" \
  --tool-version "${VERSION}" \
  --output      "${SBOM_OUT}" \
  --verify \
  "${EXTRA_FLAGS[@]}" >&2

# ── Confirm output is valid JSON ──────────────────────────────────────────────
if ! python3 -c "import json; json.load(open('${SBOM_OUT}'))" 2>/dev/null; then
  fail "SBOM output is not valid JSON: ${SBOM_OUT}"
fi

# Spot-check required SPDX fields.
SPDX_VER=$(python3 -c "
import json, sys
d = json.load(open(sys.argv[1]))
print(d.get('spdxVersion', ''))
" "${SBOM_OUT}" 2>/dev/null || echo "")

if [ "${SPDX_VER}" != "SPDX-2.3" ]; then
  fail "SBOM spdxVersion is '${SPDX_VER}', expected 'SPDX-2.3'"
fi

PKG_COUNT=$(python3 -c "
import json, sys
d = json.load(open(sys.argv[1]))
print(len(d.get('packages', [])))
" "${SBOM_OUT}" 2>/dev/null || echo "0")

DOC_HASH=$(python3 -c "
import json, sys
d = json.load(open(sys.argv[1]))
print(d.get('documentHash', ''))
" "${SBOM_OUT}" 2>/dev/null || echo "")

pass "SBOM valid: spdxVersion=SPDX-2.3, packages=${PKG_COUNT}, documentHash=${DOC_HASH:0:16}..."

# ── Cleanup temp files ────────────────────────────────────────────────────────
if [ -n "${GO_MODULES_JSON:-}" ] && [[ "${GO_MODULES_JSON}" == /tmp/* ]]; then
  rm -f "${GO_MODULES_JSON}"
fi

# ── Print the output path to stdout for capture by callers ───────────────────
# Everything above was written to stderr so only this line goes to stdout.
echo "${SBOM_OUT}"
