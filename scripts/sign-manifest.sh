#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# sign-manifest.sh — build the generate-release-manifest tool, generate a
# signed manifest for the release artifacts, verify it, and write it to
# dist/release/manifest.json.
#
# Usage:
#   scripts/sign-manifest.sh [dist/release]
#
# Required environment variables:
#   GLASSBOX_MANIFEST_SIGNING_KEY   PKCS#8 PEM Ed25519 private key (literal PEM
#                                   or a file path). Never commit this value.
#
# Optional environment variables:
#   VERSION           Release version string (default: git describe)
#   COMMIT_SHA        Full git commit SHA   (default: git rev-parse HEAD)
#   BUILD_DATE        RFC3339 UTC timestamp (default: current time)
#   SBOM_REF          Filename of the SBOM artifact in the release (optional)
#   SIGNER_IDENTITY   Human-readable identity stored in manifest provenance
#   KEY_ID            Key identifier stored in manifest provenance
#   MANIFEST_TOOL     Path to generate-release-manifest binary (built if absent)
#
# Output:
#   <dist>/manifest.json — signed manifest (added to the release archive list)
#
# Verification:
#   The script verifies the manifest immediately after writing it. Any failure
#   exits non-zero so CI catches a bad manifest before publication.
#
# Offline verification (no build environment needed):
#   See scripts/verify-manifest.sh and docs/release-manifest.md.

set -euo pipefail

DIST_DIR="${1:-dist/release}"

pass() { printf '  [PASS] %s\n' "$*"; }
fail() { printf '  [FAIL] %s\n' "$*" >&2; exit 1; }
info() { printf '  [INFO] %s\n' "$*"; }

# ── Resolve metadata ──────────────────────────────────────────────────────────
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
COMMIT_SHA="${COMMIT_SHA:-$(git rev-parse HEAD 2>/dev/null || echo "unknown")}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
SBOM_REF="${SBOM_REF:-}"
SIGNER_IDENTITY="${SIGNER_IDENTITY:-ci-pipeline}"
KEY_ID="${KEY_ID:-}"

echo "Generating signed release manifest"
echo "  Version    : ${VERSION}"
echo "  Commit     : ${COMMIT_SHA}"
echo "  Build date : ${BUILD_DATE}"
echo "  Dist dir   : ${DIST_DIR}"
echo ""

# ── Validate prerequisites ────────────────────────────────────────────────────
if [ -z "${GLASSBOX_MANIFEST_SIGNING_KEY:-}" ]; then
  fail "GLASSBOX_MANIFEST_SIGNING_KEY is not set. Provide a PKCS#8 PEM Ed25519 private key."
fi

if [ ! -d "${DIST_DIR}" ]; then
  fail "Dist directory not found: ${DIST_DIR}"
fi

# ── Build the manifest tool if not already available ─────────────────────────
MANIFEST_TOOL="${MANIFEST_TOOL:-}"
if [ -z "${MANIFEST_TOOL}" ]; then
  TOOL_BIN="$(mktemp -d)/generate-release-manifest"
  info "Building generate-release-manifest tool ..."
  go build -o "${TOOL_BIN}" ./cmd/generate-release-manifest
  MANIFEST_TOOL="${TOOL_BIN}"
  pass "Tool built: ${TOOL_BIN}"
fi

if [ ! -x "${MANIFEST_TOOL}" ]; then
  fail "manifest tool not executable: ${MANIFEST_TOOL}"
fi

# ── Generate and sign ─────────────────────────────────────────────────────────
MANIFEST_OUT="${DIST_DIR}/manifest.json"

info "Running generate-release-manifest ..."

EXTRA_FLAGS=()
if [ -n "${SBOM_REF}" ]; then
  EXTRA_FLAGS+=(--sbom-ref "${SBOM_REF}")
fi
if [ -n "${SIGNER_IDENTITY}" ]; then
  EXTRA_FLAGS+=(--signer-identity "${SIGNER_IDENTITY}")
fi
if [ -n "${KEY_ID}" ]; then
  EXTRA_FLAGS+=(--key-id "${KEY_ID}")
fi

# ── Build provenance flags ────────────────────────────────────────────────────
# These are populated from environment variables set by CI (see release.yml).
# All are optional; omit silently when not set.
if [ -n "${BUILD_SOURCE_REPOSITORY:-}" ]; then
  EXTRA_FLAGS+=(--build-source-repository "${BUILD_SOURCE_REPOSITORY}")
fi
if [ -n "${BUILD_SOURCE_REF:-}" ]; then
  EXTRA_FLAGS+=(--build-source-ref "${BUILD_SOURCE_REF}")
fi
if [ -n "${BUILD_GO_VERSION:-}" ]; then
  EXTRA_FLAGS+=(--build-go-version "${BUILD_GO_VERSION}")
fi
if [ -n "${BUILD_RUNNER_OS:-}" ]; then
  EXTRA_FLAGS+=(--build-runner-os "${BUILD_RUNNER_OS}")
fi
if [ -n "${BUILD_WORKFLOW:-}" ]; then
  EXTRA_FLAGS+=(--build-workflow "${BUILD_WORKFLOW}")
fi
if [ -n "${BUILD_RUN_ID:-}" ]; then
  EXTRA_FLAGS+=(--build-run-id "${BUILD_RUN_ID}")
fi

"${MANIFEST_TOOL}" \
  --dist        "${DIST_DIR}" \
  --version     "${VERSION}" \
  --commit      "${COMMIT_SHA}" \
  --build-date  "${BUILD_DATE}" \
  --output      "${MANIFEST_OUT}" \
  --verify \
  "${EXTRA_FLAGS[@]}"

pass "Manifest written and verified: ${MANIFEST_OUT}"

# ── Summarise artifact list ───────────────────────────────────────────────────
echo ""
echo "Artifacts listed in manifest:"
# Use python3 if available for pretty-print; fall back to grep.
if command -v python3 >/dev/null 2>&1; then
  python3 - "${MANIFEST_OUT}" <<'PYEOF'
import json, sys
with open(sys.argv[1]) as f:
    sm = json.load(f)
for a in sm.get("artifacts", []):
    print(f"  {a['sha256'][:16]}...  {a['name']}  ({a.get('kind','?')})")
print(f"\n  manifest_hash: {sm.get('manifest_hash','')[:32]}...")
print(f"  public_key:    {sm.get('public_key','')[:16]}...")
PYEOF
else
  grep '"name"' "${MANIFEST_OUT}" | sed 's/.*"name": *"\(.*\)".*/  \1/'
fi

echo ""
echo "Result: signed manifest ready at ${MANIFEST_OUT}"
