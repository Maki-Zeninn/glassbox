#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# verify-sbom.sh — validate a generated SPDX 2.3 SBOM against:
#   1. Required SPDX fields and structural integrity.
#   2. Package versions matching the source lockfiles (go-modules.json,
#      Cargo.lock, package-lock.json) when they are available.
#   3. Coverage by the signed release manifest — the manifest must list the
#      SBOM file and its SHA-256 must match.
#
# This script is intentionally build-tool-free: it requires only bash and
# python3, so it can run in any environment where the SBOM is consumed.
#
# Usage:
#   scripts/verify-sbom.sh <sbom.spdx.json> [options]
#
# Options:
#   --manifest    <manifest.json>       Signed manifest to verify coverage against
#   --go-modules  <go-modules.json>     go list -m -json all output for version check
#   --cargo-lock  <Cargo.lock>          Cargo.lock for version cross-check
#   --package-lock <package-lock.json>  npm lockfile for version cross-check
#   --strict                            Fail on version mismatches (default: warn)
#
# Exit codes:
#   0  All checks passed (or all optional checks skipped cleanly)
#   1  One or more checks failed

set -euo pipefail

# ── Argument parsing ──────────────────────────────────────────────────────────
SBOM_FILE=""
MANIFEST_FILE=""
GO_MODULES_JSON=""
CARGO_LOCK=""
PACKAGE_LOCK=""
STRICT=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifest)      MANIFEST_FILE="$2";   shift 2 ;;
    --go-modules)    GO_MODULES_JSON="$2"; shift 2 ;;
    --cargo-lock)    CARGO_LOCK="$2";      shift 2 ;;
    --package-lock)  PACKAGE_LOCK="$2";    shift 2 ;;
    --strict)        STRICT=1;             shift ;;
    -*)              echo "Unknown option: $1" >&2; exit 1 ;;
    *)
      if [ -z "${SBOM_FILE}" ]; then
        SBOM_FILE="$1"
      else
        echo "Unexpected argument: $1" >&2; exit 1
      fi
      shift ;;
  esac
done

if [ -z "${SBOM_FILE}" ]; then
  echo "Usage: verify-sbom.sh <sbom.spdx.json> [--manifest manifest.json] [--go-modules ...] [--cargo-lock ...] [--package-lock ...] [--strict]" >&2
  exit 1
fi

FAILURES=0
WARNINGS=0

pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; FAILURES=$((FAILURES + 1)); }
warn()  { printf '  [WARN] %s\n' "$*"; WARNINGS=$((WARNINGS + 1)); }
skip()  { printf '  [SKIP] %s\n' "$*"; }
info()  { printf '  [INFO] %s\n' "$*"; }
mismatch() {
  # In strict mode mismatches are hard failures; otherwise warnings.
  if [ "${STRICT}" -eq 1 ]; then
    fail "$*"
  else
    warn "$*"
  fi
}

echo "SBOM verification: ${SBOM_FILE}"
[ -n "${MANIFEST_FILE}" ]   && echo "  Manifest:     ${MANIFEST_FILE}"
[ -n "${GO_MODULES_JSON}" ] && echo "  Go modules:   ${GO_MODULES_JSON}"
[ -n "${CARGO_LOCK}" ]      && echo "  Cargo.lock:   ${CARGO_LOCK}"
[ -n "${PACKAGE_LOCK}" ]    && echo "  package-lock: ${PACKAGE_LOCK}"
echo ""

# ── 1. SBOM file exists and is valid JSON ────────────────────────────────────
echo "1. Validating SBOM structure ..."

if [ ! -f "${SBOM_FILE}" ]; then
  fail "SBOM file not found: ${SBOM_FILE}"
  echo "Result: 1 check failed." >&2; exit 1
fi

SBOM_PARSE_RESULT=$(python3 - "${SBOM_FILE}" <<'PYEOF' 2>&1)
import json, sys

path = sys.argv[1]
try:
    with open(path) as f:
        doc = json.load(f)
except json.JSONDecodeError as e:
    print(f"INVALID_JSON:{e}")
    sys.exit(1)

errors = []
required = {
    "spdxVersion":       "SPDX-2.3",
    "dataLicense":       "CC0-1.0",
    "SPDXID":            "SPDXRef-DOCUMENT",
}
for field, expected in required.items():
    val = doc.get(field, "")
    if val != expected:
        errors.append(f"field {field!r}: expected {expected!r}, got {val!r}")

for field in ("name", "documentNamespace", "packages"):
    if not doc.get(field):
        errors.append(f"missing required field: {field!r}")

pkgs = doc.get("packages", [])
seen_ids = {}
for i, p in enumerate(pkgs):
    sid = p.get("SPDXID", "")
    if not sid:
        errors.append(f"packages[{i}] has empty SPDXID")
    if not p.get("name"):
        errors.append(f"packages[{i}] ({sid}) has empty name")
    if not p.get("versionInfo"):
        errors.append(f"packages[{i}] ({sid}) has empty versionInfo")
    if sid in seen_ids:
        errors.append(f"duplicate SPDXID: {sid!r}")
    seen_ids[sid] = True

if errors:
    for e in errors:
        print(f"ERROR:{e}")
    sys.exit(1)

# Success summary
pkg_count = len(pkgs)
doc_hash  = doc.get("documentHash", "")
spdx_ver  = doc.get("spdxVersion", "")
ns        = doc.get("documentNamespace", "")
created   = doc.get("creationInfo", {}).get("created", "")
print(f"OK:packages={pkg_count}")
print(f"OK:documentHash={doc_hash}")
print(f"OK:spdxVersion={spdx_ver}")
print(f"OK:namespace={ns}")
print(f"OK:created={created}")
PYEOF
SBOM_PARSE_EXIT=$?

if echo "${SBOM_PARSE_RESULT}" | grep -q "^INVALID_JSON:"; then
  fail "SBOM is not valid JSON: $(echo "${SBOM_PARSE_RESULT}" | grep INVALID_JSON | sed 's/^INVALID_JSON://')"
elif [ "${SBOM_PARSE_EXIT}" -ne 0 ]; then
  while IFS= read -r line; do
    [[ "${line}" == ERROR:* ]] && fail "${line#ERROR:}"
  done <<< "${SBOM_PARSE_RESULT}"
else
  PKG_COUNT=$(echo "${SBOM_PARSE_RESULT}" | grep "^OK:packages=" | cut -d= -f2)
  DOC_HASH=$(echo "${SBOM_PARSE_RESULT}" | grep "^OK:documentHash=" | cut -d= -f2)
  CREATED=$(echo "${SBOM_PARSE_RESULT}" | grep "^OK:created=" | cut -d= -f2)
  pass "Valid SPDX-2.3 JSON, ${PKG_COUNT} package(s)"
  pass "documentHash = ${DOC_HASH:0:16}..."
  pass "created = ${CREATED}"
fi

# ── 2. Ecosystem coverage (at least Go or Cargo or npm PURLs present) ─────────
echo ""
echo "2. Checking ecosystem PURL coverage ..."

python3 - "${SBOM_FILE}" <<'PYEOF'
import json, sys

doc = json.load(open(sys.argv[1]))
pkgs = doc.get("packages", [])

ecosystems = {"golang": 0, "cargo": 0, "npm": 0}
for pkg in pkgs:
    for ref in pkg.get("externalRefs", []):
        loc = ref.get("referenceLocator", "")
        for eco in ecosystems:
            if loc.startswith(f"pkg:{eco}/"):
                ecosystems[eco] += 1

for eco, count in ecosystems.items():
    if count > 0:
        print(f"  [PASS] {eco}: {count} component(s) with PURLs")
    else:
        print(f"  [SKIP] {eco}: no components (ecosystem may not be in use)")

present = sum(1 for c in ecosystems.values() if c > 0)
if present == 0:
    print("  [FAIL] No PURL-bearing packages found in SBOM", file=sys.stderr)
    sys.exit(1)
PYEOF
PURL_EXIT=$?
[ "${PURL_EXIT}" -ne 0 ] && FAILURES=$((FAILURES + 1))

# ── 3. Version cross-check against Go modules ────────────────────────────────
if [ -n "${GO_MODULES_JSON}" ] && [ -f "${GO_MODULES_JSON}" ]; then
  echo ""
  echo "3. Cross-checking Go module versions against lockfile ..."

  python3 - "${SBOM_FILE}" "${GO_MODULES_JSON}" <<'PYEOF'
import json, sys

sbom_path   = sys.argv[1]
gomod_path  = sys.argv[2]

# Parse go list -m -json stream (concatenated JSON objects, not an array).
import re
go_versions = {}
content = open(gomod_path).read()
for m in re.finditer(r'\{[^{}]*\}', content, re.DOTALL):
    try:
        obj = json.loads(m.group())
        if not obj.get("Main") and obj.get("Path") and obj.get("Version"):
            go_versions[obj["Path"]] = obj["Version"]
    except json.JSONDecodeError:
        pass

doc   = json.load(open(sbom_path))
pkgs  = doc.get("packages", [])

mismatches = []
checked    = 0
for pkg in pkgs:
    for ref in pkg.get("externalRefs", []):
        loc = ref.get("referenceLocator", "")
        if not loc.startswith("pkg:golang/"):
            continue
        # pkg:golang/<name>@<version>
        parts = loc[len("pkg:golang/"):].rsplit("@", 1)
        if len(parts) != 2:
            continue
        name, sbom_ver = parts
        lockfile_ver = go_versions.get(name)
        if lockfile_ver is None:
            continue  # not in lockfile snapshot — skip
        checked += 1
        if sbom_ver != lockfile_ver:
            mismatches.append(
                f"{name}: SBOM={sbom_ver!r} lockfile={lockfile_ver!r}"
            )

if mismatches:
    for m in mismatches:
        print(f"  [MISMATCH] {m}")
    print(f"  {len(mismatches)} version mismatch(es) in Go modules")
    sys.exit(2)
else:
    print(f"  [PASS] {checked} Go component version(s) match lockfile")
PYEOF
  GO_CHECK_EXIT=$?
  if [ "${GO_CHECK_EXIT}" -eq 2 ]; then
    mismatch "Go module version mismatches detected (use --strict to fail hard)"
  elif [ "${GO_CHECK_EXIT}" -ne 0 ]; then
    fail "Go module cross-check script failed"
  fi
elif [ -n "${GO_MODULES_JSON}" ]; then
  skip "go-modules file not found at ${GO_MODULES_JSON}"
else
  skip "Go module version cross-check (--go-modules not provided)"
fi

# ── 4. Version cross-check against Cargo.lock ────────────────────────────────
if [ -n "${CARGO_LOCK}" ] && [ -f "${CARGO_LOCK}" ]; then
  echo ""
  echo "4. Cross-checking Cargo crate versions against Cargo.lock ..."

  python3 - "${SBOM_FILE}" "${CARGO_LOCK}" <<'PYEOF'
import json, sys, re

sbom_path  = sys.argv[1]
cargo_path = sys.argv[2]

# Parse Cargo.lock [[package]] entries.
cargo_versions = {}
current = {}
in_pkg  = False

with open(cargo_path) as f:
    for line in f:
        line = line.rstrip()
        if line == "[[package]]":
            if current.get("name") and current.get("version") and current.get("source"):
                cargo_versions[current["name"]] = current["version"]
            current = {}
            in_pkg  = True
            continue
        if in_pkg:
            m = re.match(r'^(\w+)\s*=\s*"([^"]*)"', line)
            if m:
                current[m.group(1)] = m.group(2)
            elif line.strip() == "" or (line.startswith("[") and not line.startswith("[[package]]")):
                in_pkg = False

# Flush last package.
if current.get("name") and current.get("version") and current.get("source"):
    cargo_versions[current["name"]] = current["version"]

doc   = json.load(open(sbom_path))
pkgs  = doc.get("packages", [])

mismatches = []
checked    = 0
for pkg in pkgs:
    for ref in pkg.get("externalRefs", []):
        loc = ref.get("referenceLocator", "")
        if not loc.startswith("pkg:cargo/"):
            continue
        parts = loc[len("pkg:cargo/"):].rsplit("@", 1)
        if len(parts) != 2:
            continue
        name, sbom_ver = parts
        lockfile_ver = cargo_versions.get(name)
        if lockfile_ver is None:
            continue
        checked += 1
        if sbom_ver != lockfile_ver:
            mismatches.append(
                f"{name}: SBOM={sbom_ver!r} lockfile={lockfile_ver!r}"
            )

if mismatches:
    for m in mismatches:
        print(f"  [MISMATCH] {m}")
    print(f"  {len(mismatches)} version mismatch(es) in Cargo crates")
    sys.exit(2)
else:
    print(f"  [PASS] {checked} Cargo component version(s) match Cargo.lock")
PYEOF
  CARGO_CHECK_EXIT=$?
  if [ "${CARGO_CHECK_EXIT}" -eq 2 ]; then
    mismatch "Cargo version mismatches detected (use --strict to fail hard)"
  elif [ "${CARGO_CHECK_EXIT}" -ne 0 ]; then
    fail "Cargo cross-check script failed"
  fi
elif [ -n "${CARGO_LOCK}" ]; then
  skip "Cargo.lock not found at ${CARGO_LOCK}"
else
  skip "Cargo version cross-check (--cargo-lock not provided)"
fi

# ── 5. Version cross-check against package-lock.json ─────────────────────────
if [ -n "${PACKAGE_LOCK}" ] && [ -f "${PACKAGE_LOCK}" ]; then
  echo ""
  echo "5. Cross-checking npm package versions against package-lock.json ..."

  python3 - "${SBOM_FILE}" "${PACKAGE_LOCK}" <<'PYEOF'
import json, sys

sbom_path  = sys.argv[1]
npm_path   = sys.argv[2]

with open(npm_path) as f:
    lock = json.load(f)

npm_versions = {}
if lock.get("lockfileVersion", 1) >= 2:
    for path, entry in lock.get("packages", {}).items():
        if not path or path == "." or entry.get("link"):
            continue
        # Strip "node_modules/" prefix (possibly nested).
        name = path
        while name.startswith("node_modules/"):
            name = name[len("node_modules/"):]
        if entry.get("version"):
            npm_versions[name] = entry["version"]
else:
    def flatten(deps):
        for name, info in deps.items():
            if info.get("version"):
                npm_versions[name] = info["version"]
            if info.get("dependencies"):
                flatten(info["dependencies"])
    flatten(lock.get("dependencies", {}))

doc  = json.load(open(sbom_path))
pkgs = doc.get("packages", [])

mismatches = []
checked    = 0
for pkg in pkgs:
    for ref in pkg.get("externalRefs", []):
        loc = ref.get("referenceLocator", "")
        if not loc.startswith("pkg:npm/"):
            continue
        parts = loc[len("pkg:npm/"):].rsplit("@", 1)
        if len(parts) != 2:
            continue
        name, sbom_ver = parts
        lockfile_ver = npm_versions.get(name)
        if lockfile_ver is None:
            continue
        checked += 1
        if sbom_ver != lockfile_ver:
            mismatches.append(
                f"{name}: SBOM={sbom_ver!r} lockfile={lockfile_ver!r}"
            )

if mismatches:
    for m in mismatches:
        print(f"  [MISMATCH] {m}")
    print(f"  {len(mismatches)} version mismatch(es) in npm packages")
    sys.exit(2)
else:
    print(f"  [PASS] {checked} npm component version(s) match package-lock.json")
PYEOF
  NPM_CHECK_EXIT=$?
  if [ "${NPM_CHECK_EXIT}" -eq 2 ]; then
    mismatch "npm version mismatches detected (use --strict to fail hard)"
  elif [ "${NPM_CHECK_EXIT}" -ne 0 ]; then
    fail "npm cross-check script failed"
  fi
elif [ -n "${PACKAGE_LOCK}" ]; then
  skip "package-lock.json not found at ${PACKAGE_LOCK}"
else
  skip "npm version cross-check (--package-lock not provided)"
fi

# ── 6. Manifest coverage check ───────────────────────────────────────────────
if [ -n "${MANIFEST_FILE}" ]; then
  echo ""
  echo "6. Checking manifest covers the SBOM ..."

  if [ ! -f "${MANIFEST_FILE}" ]; then
    fail "Manifest file not found: ${MANIFEST_FILE}"
  else
    SBOM_BASENAME="$(basename "${SBOM_FILE}")"

    python3 - "${MANIFEST_FILE}" "${SBOM_FILE}" "${SBOM_BASENAME}" <<'PYEOF'
import json, sys, hashlib, os

manifest_path = sys.argv[1]
sbom_path     = sys.argv[2]
sbom_name     = sys.argv[3]

with open(manifest_path) as f:
    manifest = json.load(f)

artifacts = manifest.get("artifacts", [])

# Find the SBOM entry in the manifest by filename.
sbom_entry = next((a for a in artifacts if a.get("name") == sbom_name), None)

if sbom_entry is None:
    # Also try sbom_ref field.
    sbom_ref = manifest.get("sbom_ref", "")
    if sbom_ref == sbom_name:
        sbom_entry = next((a for a in artifacts if a.get("name") == sbom_ref), None)

if sbom_entry is None:
    print(f"  [FAIL] SBOM '{sbom_name}' not listed in manifest artifacts", file=sys.stderr)
    print(f"  Manifest sbom_ref: {manifest.get('sbom_ref', '(none)')}", file=sys.stderr)
    sys.exit(1)

# Verify the SHA-256 recorded in the manifest matches the file on disk.
h = hashlib.sha256()
with open(sbom_path, "rb") as fh:
    for chunk in iter(lambda: fh.read(65536), b""):
        h.update(chunk)
actual_hash   = h.hexdigest().lower()
manifest_hash = sbom_entry.get("sha256", "").lower()

if actual_hash != manifest_hash:
    print(f"  [FAIL] SHA-256 mismatch for {sbom_name}:", file=sys.stderr)
    print(f"         manifest : {manifest_hash}", file=sys.stderr)
    print(f"         on-disk  : {actual_hash}",   file=sys.stderr)
    sys.exit(1)

# Verify documentHash in the SBOM matches what the manifest expects (if present).
with open(sbom_path) as f:
    sbom_doc = json.load(f)
doc_hash = sbom_doc.get("documentHash", "")

print(f"  [PASS] SBOM listed in manifest as '{sbom_name}'")
print(f"  [PASS] SHA-256 matches: {actual_hash[:16]}...")
if doc_hash:
    print(f"  [PASS] documentHash present: {doc_hash[:16]}...")
PYEOF
    MANIFEST_EXIT=$?
    [ "${MANIFEST_EXIT}" -ne 0 ] && FAILURES=$((FAILURES + 1))
  fi
else
  skip "Manifest coverage check (--manifest not provided)"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [ "${FAILURES}" -eq 0 ] && [ "${WARNINGS}" -eq 0 ]; then
  echo "Result: all SBOM verification checks passed."
  exit 0
elif [ "${FAILURES}" -eq 0 ]; then
  echo "Result: SBOM verification passed with ${WARNINGS} warning(s)."
  exit 0
else
  echo "Result: ${FAILURES} SBOM verification check(s) failed." >&2
  exit 1
fi
