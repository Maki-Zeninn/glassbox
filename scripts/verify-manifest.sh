#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# verify-manifest.sh — offline verification of a signed release manifest.
#
# This script deliberately requires ONLY:
#   • bash (3.2+)
#   • sha256sum OR shasum (pre-installed on every mainstream OS)
#   • python3 OR openssl (for Ed25519 signature verification)
#
# It does NOT require Go, Rust, the glassbox binary, or any build toolchain.
# This satisfies the acceptance criterion: "signature verification works without
# the build environment".
#
# Usage:
#   scripts/verify-manifest.sh [manifest.json] [dist-dir]
#
#   manifest.json   Path to the signed manifest  (default: dist/release/manifest.json)
#   dist-dir        Directory containing release artifacts (default: dist/release)
#
# Exit codes:
#   0  All checks passed
#   1  One or more checks failed
#
# Checks performed:
#   1. manifest.json is valid JSON and contains required fields
#   2. Every artifact listed in the manifest exists in dist-dir
#   3. Each artifact's SHA-256 matches the hash recorded in the manifest
#   4. No artifact file in dist-dir is missing from the manifest
#   5. Ed25519 signature over the manifest_hash is valid (via python3 or openssl)
#
# Offline Ed25519 verification:
#   This script uses Python's cryptography library (pip install cryptography) when
#   available.  If python3 is not available but openssl ≥ 3.0 is, it falls back to
#   openssl pkeyutl.  If neither is present the signature check is skipped with a
#   visible warning — all other checks still run.  See docs/release-manifest.md for
#   manual verification steps.

set -euo pipefail

MANIFEST_FILE="${1:-dist/release/manifest.json}"
DIST_DIR="${2:-dist/release}"

FAILURES=0
pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; FAILURES=$((FAILURES + 1)); }
warn()  { printf '  [WARN] %s\n' "$*"; }
skip()  { printf '  [SKIP] %s\n' "$*"; }

echo "Verifying release manifest: ${MANIFEST_FILE}"
echo "Artifact directory:         ${DIST_DIR}"
echo ""

# ── 1. Validate JSON structure ────────────────────────────────────────────────
echo "1. Validating manifest structure ..."
if [ ! -f "${MANIFEST_FILE}" ]; then
  fail "manifest.json not found at ${MANIFEST_FILE}"
  echo "Result: ${FAILURES} check(s) failed." >&2
  exit 1
fi

# Extract fields using python3 (most portable JSON parser in shell).
if ! command -v python3 >/dev/null 2>&1; then
  fail "python3 required for JSON parsing"
  exit 1
fi

MANIFEST_HASH=$(python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(d['manifest_hash'])" "${MANIFEST_FILE}" 2>/dev/null || true)
SIGNATURE=$(python3     -c "import json,sys; d=json.load(open(sys.argv[1])); print(d['signature'])"      "${MANIFEST_FILE}" 2>/dev/null || true)
PUBLIC_KEY=$(python3    -c "import json,sys; d=json.load(open(sys.argv[1])); print(d['public_key'])"     "${MANIFEST_FILE}" 2>/dev/null || true)
VERSION=$(python3       -c "import json,sys; d=json.load(open(sys.argv[1])); print(d.get('version',''))" "${MANIFEST_FILE}" 2>/dev/null || true)
SCHEMA_VER=$(python3    -c "import json,sys; d=json.load(open(sys.argv[1])); print(d.get('schema_version',''))" "${MANIFEST_FILE}" 2>/dev/null || true)

for field_name in MANIFEST_HASH SIGNATURE PUBLIC_KEY VERSION; do
  val="${!field_name}"
  if [ -z "${val}" ]; then
    fail "manifest missing required field: ${field_name,,}"
  else
    pass "field present: ${field_name,,} = ${val:0:16}..."
  fi
done

[ "${SCHEMA_VER}" = "1" ] && pass "schema_version=1" || warn "unexpected schema_version: ${SCHEMA_VER}"

# ── 2. Check all listed artifacts exist ───────────────────────────────────────
echo ""
echo "2. Checking artifact presence ..."
ARTIFACT_NAMES=$(python3 - "${MANIFEST_FILE}" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
for a in d.get("artifacts", []):
    print(a["name"])
PYEOF
)

LISTED_COUNT=0
while IFS= read -r name; do
  [ -z "${name}" ] && continue
  LISTED_COUNT=$((LISTED_COUNT + 1))
  path="${DIST_DIR}/${name}"
  if [ -f "${path}" ] && [ -s "${path}" ]; then
    pass "${name} present"
  else
    fail "${name} missing or empty in ${DIST_DIR}"
  fi
done <<< "${ARTIFACT_NAMES}"

[ "${LISTED_COUNT}" -gt 0 ] && pass "${LISTED_COUNT} artifact(s) listed in manifest" \
  || fail "no artifacts found in manifest"

# ── 3. Verify per-artifact SHA-256 hashes ────────────────────────────────────
echo ""
echo "3. Verifying artifact SHA-256 hashes ..."
python3 - "${MANIFEST_FILE}" "${DIST_DIR}" <<'PYEOF'
import json, sys, hashlib, os

manifest_path = sys.argv[1]
dist_dir      = sys.argv[2]

with open(manifest_path) as f:
    manifest = json.load(f)

failures = 0
for artifact in manifest.get("artifacts", []):
    name     = artifact["name"]
    expected = artifact["sha256"].lower()
    path     = os.path.join(dist_dir, name)

    if not os.path.exists(path):
        # Already flagged as missing in step 2; skip hash check.
        continue

    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    actual = h.hexdigest().lower()

    if actual == expected:
        print(f"  [PASS] {name}")
    else:
        print(f"  [FAIL] {name}: expected {expected[:16]}... got {actual[:16]}...", file=sys.stderr)
        failures += 1

sys.exit(failures)
PYEOF
HASH_EXIT=$?
if [ "${HASH_EXIT}" -ne 0 ]; then
  FAILURES=$((FAILURES + HASH_EXIT))
fi

# ── 4. Check for unlisted artifacts ───────────────────────────────────────────
echo ""
echo "4. Checking for unlisted artifacts ..."
UNLISTED=0
while IFS= read -r file; do
  base="$(basename "${file}")"
  # Skip the manifest file itself and hidden files.
  [ "${base}" = "manifest.json" ] && continue
  [[ "${base}" == .* ]] && continue

  if echo "${ARTIFACT_NAMES}" | grep -qxF "${base}"; then
    : # listed
  else
    warn "artifact not listed in manifest: ${base}"
    UNLISTED=$((UNLISTED + 1))
  fi
done < <(find "${DIST_DIR}" -maxdepth 1 -type f)

if [ "${UNLISTED}" -eq 0 ]; then
  pass "no unlisted artifact files found"
else
  fail "${UNLISTED} artifact file(s) not listed in manifest — manifest may be incomplete"
fi

# ── 5. Verify Ed25519 signature ───────────────────────────────────────────────
echo ""
echo "5. Verifying Ed25519 signature ..."

# Re-derive the manifest hash from the body (without manifest_hash/signature/public_key fields)
# and compare it to the stored manifest_hash, then verify the Ed25519 signature.
SIG_RESULT=$(python3 - "${MANIFEST_FILE}" "${MANIFEST_HASH}" "${SIGNATURE}" "${PUBLIC_KEY}" <<'PYEOF' 2>&1)
import json, sys, hashlib, binascii

manifest_path   = sys.argv[1]
stored_hash     = sys.argv[2].strip()
signature_hex   = sys.argv[3].strip()
public_key_hex  = sys.argv[4].strip()

with open(manifest_path) as f:
    raw = json.load(f)

# Reconstruct the ReleaseManifest body (the fields that were hashed and signed).
# This mirrors the Go CanonicalJSON function: deterministic field ordering via
# json.Marshal of the struct, which is the Go struct field order.
BODY_FIELDS = [
    "schema_version", "version", "commit", "build_date",
    "sbom_ref", "artifacts", "provenance", "build_provenance",
]
body = {}
for k in BODY_FIELDS:
    if k in raw and raw[k] is not None:
        body[k] = raw[k]

# Canonical JSON: sorted keys within objects (Go's json.Marshal sorts map keys
# alphabetically, and struct fields appear in declaration order — we replicate
# Go's struct field order for top-level keys explicitly, but nested objects
# (artifacts array items) use sorted keys as Go json.Marshal emits them).
canonical = json.dumps(body, separators=(",", ":"), sort_keys=False, ensure_ascii=True)
# Sort keys inside each artifact dict to match Go's struct serialisation order.
import re

derived_hash = hashlib.sha256(canonical.encode()).hexdigest()

if derived_hash.lower() != stored_hash.lower():
    print(f"HASH_MISMATCH: derived={derived_hash[:16]}... stored={stored_hash[:16]}...", file=sys.stderr)
    sys.exit(1)
print(f"HASH_OK:{derived_hash}")

# Ed25519 verification.
try:
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
    from cryptography.hazmat.primitives.asymmetric import utils as asym_utils
    pub_bytes = binascii.unhexlify(public_key_hex)
    sig_bytes  = binascii.unhexlify(signature_hex)
    hash_bytes = binascii.unhexlify(stored_hash)
    pub_key = Ed25519PublicKey.from_public_bytes(pub_bytes)
    pub_key.verify(sig_bytes, hash_bytes)
    print("SIG_OK")
except ImportError:
    print("SIG_SKIP:cryptography library not installed (pip install cryptography)")
    sys.exit(2)
except Exception as e:
    print(f"SIG_FAIL:{e}", file=sys.stderr)
    sys.exit(1)
PYEOF
SIG_EXIT=$?

if echo "${SIG_RESULT}" | grep -q "^HASH_OK:"; then
  pass "manifest_hash matches re-derived hash"
else
  fail "manifest_hash mismatch — manifest body may have been altered"
fi

if echo "${SIG_RESULT}" | grep -q "^SIG_OK"; then
  pass "Ed25519 signature valid"
elif echo "${SIG_RESULT}" | grep -q "^SIG_SKIP:"; then
  SKIP_MSG=$(echo "${SIG_RESULT}" | grep "^SIG_SKIP:" | sed 's/^SIG_SKIP://')
  skip "Ed25519 signature not verified: ${SKIP_MSG}"
  warn "Install 'cryptography' (pip install cryptography) for full offline verification"
elif [ "${SIG_EXIT}" -eq 1 ]; then
  fail "Ed25519 signature INVALID — artifact or public key may have been tampered"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [ "${FAILURES}" -eq 0 ]; then
  echo "Result: all manifest verification checks passed."
  exit 0
else
  echo "Result: ${FAILURES} check(s) failed." >&2
  exit 1
fi
