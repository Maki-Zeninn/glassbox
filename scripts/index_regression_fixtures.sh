#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# index_regression_fixtures.sh — Build and validate a repository-wide regression fixture index.
#
# Usage:
#   ./scripts/index_regression_fixtures.sh [OPTIONS]
#
# Options:
#   --fixtures-dir DIR   Root directory containing fixture layers (default: test/regression/fixtures)
#   --output FILE        Write the index JSON to FILE (default: test/regression/fixture_index.json)
#   --check-only         Validate an existing index without regenerating (requires --output)
#   --help               Show this message
#
# Each fixture entry in the index contains:
#   path          - path relative to the repo root
#   layer         - rpc | trace | sourcemap | session | audit | replay | cli
#   failure_class - extracted from the filename slug
#   issue_ref     - issue or PR number extracted from the filename (e.g. issue123, pr456)
#   schema_version- from fixture JSON field "schema_version" or "version" (if present)
#   test_name     - derived from filename without extension
#
# CI integration: exits non-zero if any fixture lacks a valid layer, a resolvable issue
# reference, or if duplicate fixture IDs are detected.

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

FIXTURES_DIR="${REPO_ROOT}/test/regression/fixtures"
OUTPUT_FILE="${REPO_ROOT}/test/regression/fixture_index.json"
CHECK_ONLY=false

# ── Colour helpers ─────────────────────────────────────────────────────────────
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

ok()   { echo -e "${GREEN}[OK]${NC}   $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --fixtures-dir) FIXTURES_DIR="$2"; shift 2 ;;
    --output)       OUTPUT_FILE="$2";  shift 2 ;;
    --check-only)   CHECK_ONLY=true;   shift   ;;
    --help|-h)
      sed -n '/^# index_regression_fixtures/,/^[^#]/{ s/^# \{0,1\}//; p }' "$0" | head -30
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Require python3
if ! command -v python3 &>/dev/null; then
  fail "python3 is required but was not found"
  exit 1
fi

echo "========================================="
echo " Regression Fixture Index"
echo "========================================="
echo " Fixtures dir : ${FIXTURES_DIR}"
echo " Index output : ${OUTPUT_FILE}"
echo ""

if [[ "$CHECK_ONLY" == "true" ]]; then
  echo "[check-only] Validating existing index: ${OUTPUT_FILE}"
  if [[ ! -f "$OUTPUT_FILE" ]]; then
    fail "Index file not found: ${OUTPUT_FILE}"
    exit 1
  fi
fi

FAIL_COUNT=0

python3 - \
  "${FIXTURES_DIR}" \
  "${OUTPUT_FILE}" \
  "${REPO_ROOT}" \
  "${CHECK_ONLY}" \
  <<'PYEOF'
import sys
import os
import re
import json
from collections import Counter
from pathlib import Path

fixtures_dir = Path(sys.argv[1])
output_file  = Path(sys.argv[2])
repo_root    = Path(sys.argv[3])
check_only   = sys.argv[4].lower() == "true"

# Valid layers match the subdirectory names.
VALID_LAYERS = {"rpc", "trace", "sourcemap", "session", "audit", "replay", "cli"}

# Pattern to extract an issue or PR reference from a filename stem.
ISSUE_RE = re.compile(r'(?:issue|pr)(\d+)', re.IGNORECASE)

def extract_fields(filepath: Path, layer: str) -> dict:
    """Extract metadata from a fixture file."""
    stem = filepath.stem
    # Handle double extensions like foo.trace.json -> stem = "foo.trace"
    while "." in stem:
        stem = stem.rsplit(".", 1)[0]

    test_name = stem

    # Issue/PR reference
    m = ISSUE_RE.search(stem)
    issue_ref = m.group(0).lower() if m else ""

    # Failure class: everything up to the issue/PR slug (strip trailing _)
    failure_class = ISSUE_RE.sub("", stem).rstrip("_").lstrip("_")
    # Replace underscores with spaces for readability
    failure_class = failure_class.replace("_", " ").strip()

    # Schema version from file content (best-effort)
    schema_version = ""
    if filepath.suffix in (".json",):
        try:
            with open(filepath, encoding="utf-8") as fh:
                data = json.load(fh)
            schema_version = str(
                data.get("schema_version") or
                data.get("version") or
                data.get("schemaVersion") or
                ""
            )
        except Exception:
            pass

    rel_path = str(filepath.relative_to(repo_root))

    return {
        "path":           rel_path,
        "layer":          layer,
        "failure_class":  failure_class,
        "issue_ref":      issue_ref,
        "schema_version": schema_version,
        "test_name":      test_name,
    }

def colour(code, msg):
    return f"\033[{code}m{msg}\033[0m"

ok   = lambda m: print(colour("0;32", "[OK]  ") + " " + m)
fail = lambda m: print(colour("0;31", "[FAIL]") + " " + m)
warn = lambda m: print(colour("1;33", "[WARN]") + " " + m)

errors = 0
entries = []

if not fixtures_dir.exists():
    fail(f"Fixtures directory not found: {fixtures_dir}")
    sys.exit(1)

# ── Collect entries ────────────────────────────────────────────────────────────
for layer_dir in sorted(fixtures_dir.iterdir()):
    if not layer_dir.is_dir():
        continue
    layer = layer_dir.name
    if layer not in VALID_LAYERS:
        warn(f"Skipping unexpected directory: {layer_dir}")
        continue

    for filepath in sorted(layer_dir.rglob("*")):
        if filepath.is_dir():
            continue
        if filepath.name == "README.md":
            continue
        entry = extract_fields(filepath, layer)
        entries.append(entry)

ok(f"Collected {len(entries)} fixture(s) across {len(VALID_LAYERS)} layers")

# ── Validation ────────────────────────────────────────────────────────────────
print("")
print("Running validation checks...")

# 1. Every entry must have a valid layer.
bad_layer = [e for e in entries if e["layer"] not in VALID_LAYERS]
if bad_layer:
    for e in bad_layer:
        fail(f"Invalid layer '{e['layer']}' for fixture: {e['path']}")
    errors += len(bad_layer)
else:
    ok("All fixtures have a valid layer")

# 2. Every entry should have an issue reference (warn but do not fail).
no_ref = [e for e in entries if not e["issue_ref"]]
if no_ref:
    for e in no_ref:
        warn(f"No issue/PR reference in filename: {e['path']}")
    # Count as failures for CI compliance
    errors += len(no_ref)
else:
    ok("All fixtures have an issue or PR reference in their filename")

# 3. Duplicate test_name within the same layer should fail CI.
dupe_keys = [(e["layer"], e["test_name"]) for e in entries]
dupe_counts = Counter(dupe_keys)
dupes = {k: c for k, c in dupe_counts.items() if c > 1}
if dupes:
    for (layer, name), count in sorted(dupes.items()):
        fail(f"Duplicate fixture ID '{name}' in layer '{layer}' appears {count} times")
    errors += len(dupes)
else:
    ok("No duplicate fixture IDs detected")

# 4. Referenced files must exist (sanity check).
missing_files = [e for e in entries if not (repo_root / e["path"]).exists()]
if missing_files:
    for e in missing_files:
        fail(f"File not found: {e['path']}")
    errors += len(missing_files)
else:
    ok("All indexed files exist on disk")

# ── Output ────────────────────────────────────────────────────────────────────
index = {
    "generated_by": "scripts/index_regression_fixtures.sh",
    "fixture_count": len(entries),
    "layers": sorted(list({e["layer"] for e in entries})),
    "fixtures": entries,
}

if not check_only:
    output_file.parent.mkdir(parents=True, exist_ok=True)
    with open(output_file, "w", encoding="utf-8") as fh:
        json.dump(index, fh, indent=2)
        fh.write("\n")
    ok(f"Index written to {output_file}")
else:
    # In check-only mode, compare existing index against fresh scan.
    if output_file.exists():
        with open(output_file, encoding="utf-8") as fh:
            existing = json.load(fh)
        existing_paths = {e["path"] for e in existing.get("fixtures", [])}
        scanned_paths  = {e["path"] for e in entries}
        orphaned = existing_paths - scanned_paths
        new_unindexed = scanned_paths - existing_paths
        if orphaned:
            for p in sorted(orphaned):
                fail(f"Orphaned index entry (file removed): {p}")
            errors += len(orphaned)
        if new_unindexed:
            for p in sorted(new_unindexed):
                fail(f"Unindexed fixture (run without --check-only to regenerate): {p}")
            errors += len(new_unindexed)
        if not orphaned and not new_unindexed:
            ok("Existing index is up to date")
    else:
        fail(f"Index file not found for check-only mode: {output_file}")
        errors += 1

print("")
if errors == 0:
    print(colour("0;32", "[OK]") + "   All fixture index checks passed.")
else:
    print(colour("0;31", "[FAIL]") + f" {errors} check(s) failed.")
sys.exit(errors)
PYEOF

FAIL_COUNT=$?

echo ""
echo "========================================="
echo " Summary"
echo "========================================="
if [[ "$FAIL_COUNT" -eq 0 ]]; then
  ok "Fixture index complete."
  exit 0
else
  fail "${FAIL_COUNT} check(s) failed."
  exit 1
fi
PYEOF
