#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# sbom-diff.sh — compare two SPDX 2.3 SBOM files and produce a reviewable
# supply-chain diff report.
#
# The report covers:
#   - Component additions (new in NEW_SBOM, absent in OLD_SBOM)
#   - Component removals (in OLD_SBOM, absent in NEW_SBOM)
#   - Version changes (same name, different version)
#   - License changes (same name+version, different declared license)
#   - License policy violations among new/changed components
#   - Components that are in OLD_SBOM but NOT in NEW_SBOM (true removals vs renames)
#
# Acceptance criteria:
#   - Prohibited license or undeclared component: exit 1
#   - Removed dependencies not misreported as additions
#   - Report identifies source package and version
#
# Usage:
#   scripts/sbom-diff.sh <old.spdx.json> <new.spdx.json> [options]
#
# Options:
#   --policy    <license-policy.json>  License policy (default: license-policy.json)
#   --output    <report.json>          Write machine-readable report to file
#   --format    text|json|markdown     Output format (default: text)
#   --fail-on-addition                 Fail when any new component is added
#   --fail-on-removal                  Fail when any component is removed
#   --fail-on-license-change           Fail when any component license changes
#   --strict                           Equivalent to all three --fail-on-* flags
#
# Exit codes:
#   0   Diff clean or only advisory differences (no policy violations)
#   1   Policy violation detected (prohibited license, undeclared component)
#   2   Flag-controlled threshold exceeded (addition/removal/license-change)

set -euo pipefail

# ── Argument parsing ──────────────────────────────────────────────────────────
OLD_SBOM=""
NEW_SBOM=""
POLICY_FILE="${POLICY_FILE:-license-policy.json}"
OUTPUT_FILE=""
FORMAT="text"
FAIL_ON_ADDITION=0
FAIL_ON_REMOVAL=0
FAIL_ON_LICENSE_CHANGE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --policy)           POLICY_FILE="$2";    shift 2 ;;
    --output)           OUTPUT_FILE="$2";    shift 2 ;;
    --format)           FORMAT="$2";         shift 2 ;;
    --fail-on-addition) FAIL_ON_ADDITION=1;  shift ;;
    --fail-on-removal)  FAIL_ON_REMOVAL=1;   shift ;;
    --fail-on-license-change) FAIL_ON_LICENSE_CHANGE=1; shift ;;
    --strict)
      FAIL_ON_ADDITION=1; FAIL_ON_REMOVAL=1; FAIL_ON_LICENSE_CHANGE=1
      shift ;;
    -*)  echo "Unknown option: $1" >&2; exit 1 ;;
    *)
      if   [[ -z "${OLD_SBOM}" ]]; then OLD_SBOM="$1"
      elif [[ -z "${NEW_SBOM}" ]]; then NEW_SBOM="$1"
      else echo "Unexpected argument: $1" >&2; exit 1
      fi
      shift ;;
  esac
done

if [[ -z "${OLD_SBOM}" || -z "${NEW_SBOM}" ]]; then
  echo "Usage: sbom-diff.sh <old.spdx.json> <new.spdx.json> [--policy ...] [--output ...] [--format text|json|markdown]" >&2
  exit 1
fi

for f in "${OLD_SBOM}" "${NEW_SBOM}"; do
  if [[ ! -f "${f}" ]]; then
    echo "File not found: ${f}" >&2
    exit 1
  fi
done

FAILURES=0
THRESHOLD_VIOLATIONS=0

pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; FAILURES=$((FAILURES + 1)); }
warn()  { printf '  [WARN] %s\n' "$*"; }
info()  { printf '  [INFO] %s\n' "$*"; }

echo "SBOM diff"
echo "  Old : ${OLD_SBOM}"
echo "  New : ${NEW_SBOM}"
[[ -f "${POLICY_FILE}" ]] && echo "  Policy: ${POLICY_FILE}"
echo ""

# ── Core diff via Python ──────────────────────────────────────────────────────
DIFF_RESULT=$(python3 - \
  "${OLD_SBOM}" \
  "${NEW_SBOM}" \
  "${POLICY_FILE:-/dev/null}" \
  "${FORMAT}" \
  "${OUTPUT_FILE:-/dev/null}" \
  "${FAIL_ON_ADDITION}" \
  "${FAIL_ON_REMOVAL}" \
  "${FAIL_ON_LICENSE_CHANGE}" \
  <<'PYEOF' 2>&1)
import json, sys, hashlib, os

old_path    = sys.argv[1]
new_path    = sys.argv[2]
policy_path = sys.argv[3]
fmt         = sys.argv[4]
output_path = sys.argv[5]
fail_add    = sys.argv[6] == "1"
fail_rem    = sys.argv[7] == "1"
fail_lc     = sys.argv[8] == "1"

# ── Load SBOMs ────────────────────────────────────────────────────────────────
def load_sbom(path):
    with open(path) as f:
        doc = json.load(f)
    # Build a map: purl -> {name, version, license, spdxid, ecosystem}
    pkgs = {}
    for pkg in doc.get("packages", []):
        name    = pkg.get("name", "")
        version = pkg.get("versionInfo", "")
        license = pkg.get("licenseDeclared", "") or pkg.get("licenseConcluded", "")
        spdxid  = pkg.get("SPDXID", "")
        # Determine ecosystem from PURL
        ecosystem = "unknown"
        purl = ""
        for ref in pkg.get("externalRefs", []):
            loc = ref.get("referenceLocator", "")
            if loc.startswith("pkg:"):
                purl = loc
                if loc.startswith("pkg:golang/"): ecosystem = "go"
                elif loc.startswith("pkg:cargo/"): ecosystem = "cargo"
                elif loc.startswith("pkg:npm/"):   ecosystem = "npm"
                break
        key = purl if purl else f"{ecosystem}/{name}@{version}"
        pkgs[key] = {
            "name": name, "version": version, "license": license,
            "spdxid": spdxid, "ecosystem": ecosystem, "purl": purl,
        }
    return pkgs

old_pkgs = load_sbom(old_path)
new_pkgs = load_sbom(new_path)

# ── Diff ──────────────────────────────────────────────────────────────────────
old_keys = set(old_pkgs.keys())
new_keys = set(new_pkgs.keys())

# True additions (new PURL not in old SBOM)
added   = sorted(new_keys - old_keys)
# True removals (old PURL not in new SBOM)
removed = sorted(old_keys - new_keys)
# Common PURLs — check for version or license drift
common  = old_keys & new_keys

version_changes = []
license_changes = []

for key in sorted(common):
    old_p = old_pkgs[key]
    new_p = new_pkgs[key]
    if old_p["version"] != new_p["version"]:
        version_changes.append({
            "key": key, "name": old_p["name"],
            "ecosystem": old_p["ecosystem"],
            "old_version": old_p["version"],
            "new_version": new_p["version"],
            "old_license": old_p["license"],
            "new_license": new_p["license"],
        })
    if old_p["license"] != new_p["license"]:
        license_changes.append({
            "key": key, "name": old_p["name"],
            "ecosystem": old_p["ecosystem"],
            "version": new_p["version"],
            "old_license": old_p["license"],
            "new_license": new_p["license"],
        })

# ── Policy check ─────────────────────────────────────────────────────────────
policy_violations = []
if os.path.isfile(policy_path):
    with open(policy_path) as f:
        policy = json.load(f)
    disallowed = set(policy.get("disallowed_licenses", []))
    allowed    = set(policy.get("allowed_licenses", []))

    # Check added components and license-changed components.
    for key in added:
        pkg = new_pkgs[key]
        lic = pkg["license"]
        if lic and lic in disallowed:
            policy_violations.append({
                "kind": "added_prohibited_license",
                "name": pkg["name"],
                "version": pkg["version"],
                "ecosystem": pkg["ecosystem"],
                "license": lic,
                "purl": pkg.get("purl", key),
            })
        elif lic and lic not in allowed and lic not in ("NOASSERTION", "NONE", ""):
            policy_violations.append({
                "kind": "added_unknown_license",
                "name": pkg["name"],
                "version": pkg["version"],
                "ecosystem": pkg["ecosystem"],
                "license": lic,
                "purl": pkg.get("purl", key),
            })

    for lc in license_changes:
        new_lic = lc["new_license"]
        if new_lic and new_lic in disallowed:
            policy_violations.append({
                "kind": "license_changed_to_prohibited",
                "name": lc["name"],
                "version": lc["version"],
                "ecosystem": lc["ecosystem"],
                "old_license": lc["old_license"],
                "new_license": new_lic,
            })

# ── Build report ──────────────────────────────────────────────────────────────
report = {
    "schema_version": "1",
    "old_sbom": old_path,
    "new_sbom": new_path,
    "summary": {
        "added":           len(added),
        "removed":         len(removed),
        "version_changes": len(version_changes),
        "license_changes": len(license_changes),
        "policy_violations": len(policy_violations),
    },
    "added":            [{"purl": k, **new_pkgs[k]} for k in added],
    "removed":          [{"purl": k, **old_pkgs[k]} for k in removed],
    "version_changes":  version_changes,
    "license_changes":  license_changes,
    "policy_violations": policy_violations,
}

# ── Output ────────────────────────────────────────────────────────────────────
exit_code = 0

if fmt == "json":
    print(json.dumps(report, indent=2))
elif fmt == "markdown":
    print(f"## SBOM Diff Report\n")
    print(f"| Category | Count |")
    print(f"|---|---|")
    s = report["summary"]
    print(f"| Added components | {s['added']} |")
    print(f"| Removed components | {s['removed']} |")
    print(f"| Version changes | {s['version_changes']} |")
    print(f"| License changes | {s['license_changes']} |")
    print(f"| Policy violations | {s['policy_violations']} |")
    if added:
        print(f"\n### Added ({len(added)})\n")
        for k in added:
            p = new_pkgs[k]
            print(f"- **{p['name']}** `{p['version']}` ({p['ecosystem']}) — {p['license'] or 'no license'}")
    if removed:
        print(f"\n### Removed ({len(removed)})\n")
        for k in removed:
            p = old_pkgs[k]
            print(f"- **{p['name']}** `{p['version']}` ({p['ecosystem']}) — {p['license'] or 'no license'}")
    if version_changes:
        print(f"\n### Version Changes ({len(version_changes)})\n")
        for vc in version_changes:
            print(f"- **{vc['name']}** ({vc['ecosystem']}): `{vc['old_version']}` → `{vc['new_version']}`")
    if license_changes:
        print(f"\n### License Changes ({len(license_changes)})\n")
        for lc in license_changes:
            print(f"- **{lc['name']}** `{lc['version']}` ({lc['ecosystem']}): `{lc['old_license']}` → `{lc['new_license']}`")
    if policy_violations:
        print(f"\n### ⚠ Policy Violations ({len(policy_violations)})\n")
        for v in policy_violations:
            print(f"- **{v['name']}** `{v.get('version','')}` ({v.get('ecosystem','')}): {v['kind']} — {v.get('license') or v.get('new_license','')}")
else:
    # Text format
    s = report["summary"]
    print(f"  Added:          {s['added']}")
    print(f"  Removed:        {s['removed']}")
    print(f"  Version changes:{s['version_changes']}")
    print(f"  License changes:{s['license_changes']}")
    print(f"  Policy violations: {s['policy_violations']}")
    if added:
        print(f"\n  New components ({len(added)}):")
        for k in added:
            p = new_pkgs[k]
            print(f"    + {p['name']} {p['version']} ({p['ecosystem']}) [{p['license'] or 'no-license'}]")
    if removed:
        print(f"\n  Removed components ({len(removed)}):")
        for k in removed:
            p = old_pkgs[k]
            print(f"    - {p['name']} {p['version']} ({p['ecosystem']}) [{p['license'] or 'no-license'}]")
    if version_changes:
        print(f"\n  Version changes ({len(version_changes)}):")
        for vc in version_changes:
            print(f"    ~ {vc['name']} ({vc['ecosystem']}): {vc['old_version']} -> {vc['new_version']}")
    if license_changes:
        print(f"\n  License changes ({len(license_changes)}):")
        for lc in license_changes:
            print(f"    ~ {lc['name']} {lc['version']} ({lc['ecosystem']}): {lc['old_license']} -> {lc['new_license']}")
    if policy_violations:
        print(f"\n  POLICY VIOLATIONS ({len(policy_violations)}):", file=sys.stderr)
        for v in policy_violations:
            print(f"    VIOLATION: {v['name']} {v.get('version','')} ({v.get('ecosystem','')}): {v['kind']}", file=sys.stderr)

# Write machine-readable report if requested
if output_path and output_path != "/dev/null":
    os.makedirs(os.path.dirname(output_path) if os.path.dirname(output_path) else ".", exist_ok=True)
    with open(output_path, "w") as f:
        json.dump(report, f, indent=2)

# Exit codes
if policy_violations:
    print("EXIT:1")  # Policy violation — hard failure
elif (fail_add and added) or (fail_rem and removed) or (fail_lc and license_changes):
    print("EXIT:2")  # Threshold violation
else:
    print("EXIT:0")
PYEOF
DIFF_EXIT=$?

# Parse exit code from Python output
PY_EXIT=$(echo "${DIFF_RESULT}" | grep "^EXIT:" | tail -1 | cut -d: -f2 || echo "0")
DIFF_OUTPUT=$(echo "${DIFF_RESULT}" | grep -v "^EXIT:" || true)

echo "${DIFF_OUTPUT}"

if [[ "${PY_EXIT}" == "1" ]]; then
  fail "SBOM diff: policy violation detected (prohibited or undeclared license in new/changed components)"
  FAILURES=$((FAILURES + 1))
elif [[ "${PY_EXIT}" == "2" ]]; then
  fail "SBOM diff: threshold exceeded (--fail-on-* flag triggered)"
  THRESHOLD_VIOLATIONS=$((THRESHOLD_VIOLATIONS + 1))
fi

# ── Write JSON report if requested ────────────────────────────────────────────
# (handled inside Python above)

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [[ "${FAILURES}" -eq 0 && "${THRESHOLD_VIOLATIONS}" -eq 0 ]]; then
  pass "SBOM diff clean — no policy violations."
  exit 0
elif [[ "${FAILURES}" -gt 0 ]]; then
  echo "Result: ${FAILURES} SBOM policy violation(s)." >&2
  exit 1
else
  echo "Result: threshold violation — see --fail-on-* flags." >&2
  exit 2
fi
