#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# scripts/dep-compat-compare.sh
#
# Compares freshly captured dependency-compatibility outputs against stored
# golden baselines and produces a CompatReport JSON file.
#
# Diffs are classified as:
#   none       — output matches the golden file exactly.
#   expected   — only schema-format fields changed (new optional fields,
#                version/timestamp strings, key reordering).
#   unexpected — value changes, missing required fields, or type mismatches.
#
# Usage:
#   scripts/dep-compat-compare.sh [options]
#
# Options:
#   --captured-dir DIR  Directory containing captured JSON files from
#                       dep-compat-capture.sh. Required.
#   --golden-dir DIR    Directory containing golden baseline files.
#                       Default: internal/depcompat/testdata/golden
#   --dep-group GROUP   Only compare GROUP. Default: all groups.
#   --report-file FILE  Path to write the JSON CompatReport.
#                       Default: <captured-dir>/compat-report.json
#   --summary-file FILE Path to write the Markdown job summary.
#                       Default: <captured-dir>/compat-summary.md
#   --fail-on-unexpected
#                       Exit non-zero when any unexpected diffs are found.
#                       (Always the case in scheduled CI.)
#   --fail-on-error     Exit non-zero when any capture produced an error.
#   -v, --verbose       Print per-field diff details to stdout.
#   -h, --help          Show this help and exit.
#
# Exit codes:
#   0   All outputs matched or only expected diffs found.
#   1   Unexpected diffs found (when --fail-on-unexpected) or errors found
#       (when --fail-on-error), or a usage error occurred.

set -euo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_VERSION="1.0"

ALL_DEP_GROUPS=(stellar-sdk soroban-host crypto rpc-client)
ALL_OUTPUT_KINDS=(replay trace audit binding)

# Keys whose value changes are always expected (schema / format metadata).
EXPECTED_KEYS=(schema_version version spec_version format_version captured_at dep_versions)

# JSON path patterns whose value changes are expected (timestamps).
EXPECTED_PATH_PATTERNS=(generated_at created_at updated_at timestamp captured_at)

# ─── Defaults ─────────────────────────────────────────────────────────────────

CAPTURED_DIR=""
GOLDEN_DIR="${REPO_ROOT}/internal/depcompat/testdata/golden"
DEP_GROUP=""
REPORT_FILE=""
SUMMARY_FILE=""
FAIL_ON_UNEXPECTED=false
FAIL_ON_ERROR=false
VERBOSE=false

# ─── Colour helpers ───────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}   $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $*" >&2; }

# ─── Argument parsing ─────────────────────────────────────────────────────────

usage() {
  sed -n '/^# Usage:/,/^# Exit codes:/{ /^# /{ s/^# //; p } }' "${BASH_SOURCE[0]}"
  exit 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --captured-dir)         CAPTURED_DIR="$2";      shift 2 ;;
    --golden-dir)           GOLDEN_DIR="$2";        shift 2 ;;
    --dep-group)            DEP_GROUP="$2";         shift 2 ;;
    --report-file)          REPORT_FILE="$2";       shift 2 ;;
    --summary-file)         SUMMARY_FILE="$2";      shift 2 ;;
    --fail-on-unexpected)   FAIL_ON_UNEXPECTED=true; shift  ;;
    --fail-on-error)        FAIL_ON_ERROR=true;     shift   ;;
    -v|--verbose)           VERBOSE=true;           shift   ;;
    -h|--help)              usage ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

if [[ -z "${CAPTURED_DIR}" ]]; then
  fail "--captured-dir is required"
  exit 1
fi

REPORT_FILE="${REPORT_FILE:-${CAPTURED_DIR}/compat-report.json}"
SUMMARY_FILE="${SUMMARY_FILE:-${CAPTURED_DIR}/compat-summary.md}"

if [[ -n "${DEP_GROUP}" ]]; then
  GROUPS=("${DEP_GROUP}")
else
  GROUPS=("${ALL_DEP_GROUPS[@]}")
fi

# ─── JSON comparison engine (pure Python, no extra dependencies) ───────────────

# compare_json GOLDEN_FILE ACTUAL_FILE GROUP KIND
# Outputs a JSON object: {"class": "none|expected|unexpected", "diffs": [...]}
compare_json() {
  local golden="$1" actual="$2" group="$3" kind="$4"
  python3 - "${golden}" "${actual}" "${group}" "${kind}" <<'PYEOF'
import sys, json, re

golden_path = sys.argv[1]
actual_path = sys.argv[2]
dep_group   = sys.argv[3]
output_kind = sys.argv[4]

EXPECTED_KEYS = {"schema_version", "version", "spec_version", "format_version",
                 "captured_at", "dep_versions", "generated_at", "updated_at",
                 "created_at"}
TIMESTAMP_PATTERN = re.compile(r'(timestamp|created_at|updated_at|generated_at|captured_at)')

def classify_key(key):
    """Return 'expected' if this key's value change is always acceptable."""
    k = key.lower().lstrip("$.")
    k = re.sub(r'\[\d+\]', '', k).split(".")[-1]
    return k in EXPECTED_KEYS or bool(TIMESTAMP_PATTERN.search(k))

def encode(v):
    return json.dumps(v, separators=(',', ':'))

def diff(path, golden, actual, out):
    if type(golden) != type(actual):
        out.append({
            "json_path": path,
            "golden_value": encode(golden),
            "actual_value": encode(actual),
            "class": "unexpected",
            "reason": f"type changed at {path}: {type(golden).__name__} → {type(actual).__name__}"
        })
        return

    if isinstance(golden, dict):
        all_keys = sorted(set(list(golden.keys()) + list(actual.keys())))
        for k in all_keys:
            child = f"{path}.{k}"
            if k not in actual:
                out.append({
                    "json_path": child,
                    "golden_value": encode(golden[k]),
                    "class": "unexpected",
                    "reason": f"required field {child!r} missing from actual output"
                })
            elif k not in golden:
                out.append({
                    "json_path": child,
                    "actual_value": encode(actual[k]),
                    "class": "expected",
                    "reason": f"new field {child!r} in actual output (schema addition)"
                })
            else:
                diff(child, golden[k], actual[k], out)

    elif isinstance(golden, list):
        if len(golden) != len(actual):
            cls = "expected" if len(actual) > len(golden) else "unexpected"
            reason = (
                f"array at {path} grew from {len(golden)} to {len(actual)} elements"
                if cls == "expected"
                else f"array at {path} shrank from {len(golden)} to {len(actual)} elements"
            )
            out.append({
                "json_path": path,
                "golden_value": encode(len(golden)),
                "actual_value": encode(len(actual)),
                "class": cls,
                "reason": reason
            })
        for i, (g, a) in enumerate(zip(golden, actual)):
            diff(f"{path}[{i}]", g, a, out)

    else:
        if golden != actual:
            cls = "expected" if classify_key(path) else "unexpected"
            reason = (
                f"timestamp/schema field {path!r} changed between runs"
                if cls == "expected"
                else f"value changed at {path}: {encode(golden)} → {encode(actual)}"
            )
            out.append({
                "json_path": path,
                "golden_value": encode(golden),
                "actual_value": encode(actual),
                "class": cls,
                "reason": reason
            })

try:
    g = json.loads(open(golden_path).read())
except Exception as e:
    print(json.dumps({"class": "unexpected", "error": f"parse golden: {e}", "diffs": []}))
    sys.exit(0)

try:
    a = json.loads(open(actual_path).read())
except Exception as e:
    print(json.dumps({"class": "unexpected", "error": f"parse actual: {e}", "diffs": []}))
    sys.exit(0)

diffs = []
diff("$", g, a, diffs)

aggregate = "none"
if any(d["class"] == "unexpected" for d in diffs):
    aggregate = "unexpected"
elif diffs:
    aggregate = "expected"

print(json.dumps({"class": aggregate, "diffs": diffs}, indent=2))
PYEOF
}

# ─── Main ─────────────────────────────────────────────────────────────────────

main() {
  echo "dep-compat-compare.sh v${SCRIPT_VERSION}"
  echo "Captured dir : ${CAPTURED_DIR}"
  echo "Golden dir   : ${GOLDEN_DIR}"
  echo "Report file  : ${REPORT_FILE}"
  echo "Summary file : ${SUMMARY_FILE}"
  echo ""

  local run_id="${GITHUB_RUN_ID:-local-$(date +%s)}"
  local generated_at
  generated_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

  # Collect per-output results into a JSON array.
  local results_json="["
  local first=true
  local total=0 matched=0 expected_cnt=0 unexpected_cnt=0 errored_cnt=0

  for group in "${GROUPS[@]}"; do
    for kind in "${ALL_OUTPUT_KINDS[@]}"; do
      total=$((total + 1))

      golden_file="${GOLDEN_DIR}/${group}-${kind}.golden.json"
      actual_file="${CAPTURED_DIR}/${group}-${kind}.json"

      local class="unexpected" error_msg="" diff_json='[]'

      # Check if actual file was a capture error sentinel.
      if [[ -f "${actual_file}" ]]; then
        is_error="$(python3 -c "import json; d=json.loads(open('${actual_file}').read()); print(d.get('capture_error',False))" 2>/dev/null || echo False)"
        if [[ "${is_error}" == "True" ]]; then
          error_msg="$(python3 -c "import json; d=json.loads(open('${actual_file}').read()); print(d.get('output','capture failed')[:200])" 2>/dev/null || echo 'capture failed')"
          errored_cnt=$((errored_cnt + 1))
          fail "  ${group}/${kind}: CAPTURE ERROR"
        else
          # Run the comparison.
          cmp_result="$(compare_json "${golden_file}" "${actual_file}" "${group}" "${kind}" 2>/dev/null || echo '{"class":"unexpected","error":"compare failed","diffs":[]}')"
          class="$(echo "${cmp_result}" | python3 -c "import json,sys; print(json.load(sys.stdin)['class'])")"
          diff_json="$(echo "${cmp_result}" | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('diffs',[])))")"
          error_msg="$(echo "${cmp_result}" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',''))")"

          case "${class}" in
            none)        matched=$((matched + 1));       ok   "  ${group}/${kind}: PASS (no diff)" ;;
            expected)    expected_cnt=$((expected_cnt + 1)); warn "  ${group}/${kind}: EXPECTED DIFF" ;;
            unexpected)  unexpected_cnt=$((unexpected_cnt + 1)); fail "  ${group}/${kind}: UNEXPECTED DIFF" ;;
          esac

          if "${VERBOSE}" && [[ "${class}" != "none" ]]; then
            echo "${diff_json}" | python3 -c "
import json,sys
diffs = json.load(sys.stdin)
for d in diffs:
    print(f'    [{d[\"class\"]}] {d[\"json_path\"]}: {d.get(\"golden_value\",\"\")} → {d.get(\"actual_value\",\"\")}')
    print(f'      {d.get(\"reason\",\"\")}')
"
          fi
        fi
      else
        error_msg="captured file not found: ${actual_file}"
        errored_cnt=$((errored_cnt + 1))
        fail "  ${group}/${kind}: MISSING"
      fi

      # Build result JSON object.
      error_json="$(python3 -c "import json; print(json.dumps(${error_msg@Q}))" 2>/dev/null || echo '""')"
      result_obj="$(python3 - "${group}" "${kind}" "${golden_file}" "${actual_file}" \
        "${class}" "${diff_json}" "${error_msg}" <<'PYEOF'
import json, sys
group       = sys.argv[1]
kind        = sys.argv[2]
golden_file = sys.argv[3]
actual_file = sys.argv[4]
cls         = sys.argv[5]
diffs_raw   = sys.argv[6]
error_msg   = sys.argv[7]
try:
    diffs = json.loads(diffs_raw)
except Exception:
    diffs = []
obj = {
    "dep_group":     group,
    "output_kind":   kind,
    "golden_file":   golden_file,
    "captured_file": actual_file,
    "class":         cls,
    "diffs":         diffs,
}
if error_msg:
    obj["error"] = error_msg
print(json.dumps(obj))
PYEOF
)"
      if ! "${first}"; then
        results_json+=","
      fi
      first=false
      results_json+="${result_obj}"
    done
  done

  results_json+="]"

  # Build summary object.
  has_unexpected=$(python3 -c "print('true' if ${unexpected_cnt} > 0 else 'false')")
  has_errors=$(python3 -c "print('true' if ${errored_cnt} > 0 else 'false')")

  summary_json="$(python3 - "${total}" "${matched}" "${expected_cnt}" \
    "${unexpected_cnt}" "${errored_cnt}" <<'PYEOF'
import json, sys
t, m, e, u, err = int(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5])
print(json.dumps({
    "total_outputs":       t,
    "outputs_matched":     m,
    "outputs_expected":    e,
    "outputs_unexpected":  u,
    "outputs_errored":     err,
    "has_unexpected_diffs": u > 0,
    "has_errors":           err > 0,
}))
PYEOF
)"

  # Assemble the CompatReport JSON.
  python3 - "${run_id}" "${generated_at}" "${DEP_GROUP}" \
    "${results_json}" "${summary_json}" "${REPORT_FILE}" <<'PYEOF'
import json, sys
run_id       = sys.argv[1]
generated_at = sys.argv[2]
dep_group    = sys.argv[3]
results_raw  = sys.argv[4]
summary_raw  = sys.argv[5]
out_path     = sys.argv[6]

report = {
    "schema_version": "1.0",
    "run_id":         run_id,
    "generated_at":   generated_at,
    "results":        json.loads(results_raw),
    "summary":        json.loads(summary_raw),
}
if dep_group:
    report["dep_group"] = dep_group

with open(out_path, "w") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"Report written to: {out_path}")
PYEOF

  # Generate Markdown summary.
  python3 - "${REPORT_FILE}" "${SUMMARY_FILE}" <<'PYEOF'
import json, sys
from datetime import datetime

report = json.loads(open(sys.argv[1]).read())
out_path = sys.argv[2]
s = report["summary"]

badge = (
    "![FAIL](https://img.shields.io/badge/compat-FAIL-red)"
    if s["has_unexpected_diffs"] or s["has_errors"]
    else (
        "![WARN](https://img.shields.io/badge/compat-WARN-yellow)"
        if s["outputs_expected"] > 0
        else "![PASS](https://img.shields.io/badge/compat-PASS-brightgreen)"
    )
)

lines = [
    f"## Dependency Compatibility Report {badge}",
    "",
    f"| Field | Value |",
    f"|---|---|",
    f"| Run ID | `{report['run_id']}` |",
    f"| Generated | {report['generated_at']} |",
    f"| Dep Group | `{report.get('dep_group', 'all groups')}` |",
    "",
    "### Summary",
    "",
    "| Metric | Count |",
    "|---|---|",
    f"| Total outputs tested | {s['total_outputs']} |",
    f"| Matched (no diff) | {s['outputs_matched']} |",
    f"| Expected diffs | {s['outputs_expected']} |",
    f"| Unexpected diffs | **{s['outputs_unexpected']}** |",
    f"| Errors | **{s['outputs_errored']}** |",
    "",
    "### Results",
    "",
    "| Dep Group | Output Kind | Status | Diffs |",
    "|---|---|---|---|",
]

STATUS_MAP = {
    "none":       ":white_check_mark: PASS",
    "expected":   ":warning: EXPECTED",
    "unexpected": ":x: FAIL",
}

for r in report["results"]:
    if r.get("error"):
        status = ":x: ERROR"
        diff_summary = f"ERROR: {r['error'][:100]}"
    else:
        status = STATUS_MAP.get(r["class"], r["class"])
        diffs = r.get("diffs", [])
        if not diffs:
            diff_summary = "-"
        else:
            unexp = sum(1 for d in diffs if d["class"] == "unexpected")
            diff_summary = f"{len(diffs)} total, {unexp} unexpected"
    lines.append(f"| `{r['dep_group']}` | `{r['output_kind']}` | {status} | {diff_summary} |")

lines.append("")

# Detail sections for non-matching results.
for r in report["results"]:
    if r["class"] == "none" and not r.get("error"):
        continue
    lines.append(f"#### `{r['dep_group']}/{r['output_kind']}` Diffs")
    lines.append("")
    if r.get("error"):
        lines.append(f"> [!ERROR]")
        lines.append(f"> {r['error']}")
        lines.append("")
        continue
    diffs = r.get("diffs", [])
    if not diffs:
        lines.append("_No diffs._")
        lines.append("")
        continue
    lines.append("| JSON Path | Golden | Actual | Class | Reason |")
    lines.append("|---|---|---|---|---|")
    for d in diffs:
        path    = str(d.get("json_path","")).replace("|","\\|")
        golden  = str(d.get("golden_value","")).replace("|","\\|")
        actual  = str(d.get("actual_value","")).replace("|","\\|")
        cls     = d.get("class","")
        reason  = str(d.get("reason","")).replace("|","\\|")
        lines.append(f"| `{path}` | `{golden}` | `{actual}` | {cls} | {reason} |")
    lines.append("")

# No-publish notice.
lines += [
    "---",
    "> **Note**: This report is a review artifact only.",
    "> The scheduled job does not publish dependency updates automatically.",
    "> To accept expected changes, run:",
    "> ```bash",
    "> scripts/dep-compat-capture.sh --update-golden",
    "> ```",
    "> then review the diff and commit.",
]

with open(out_path, "w") as f:
    f.write("\n".join(lines) + "\n")
print(f"Markdown summary written to: {out_path}")
PYEOF

  echo ""
  echo "=== Compatibility Summary ==="
  echo "  Total   : ${total}"
  echo "  Matched : ${matched}"
  echo "  Expected: ${expected_cnt}"
  echo "  FAIL    : ${unexpected_cnt}"
  echo "  Errors  : ${errored_cnt}"
  echo ""

  # Determine exit code.
  local exit_code=0
  if "${FAIL_ON_UNEXPECTED}" && [[ "${unexpected_cnt}" -gt 0 ]]; then
    fail "Exiting non-zero: ${unexpected_cnt} unexpected diff(s) detected."
    exit_code=1
  fi
  if "${FAIL_ON_ERROR}" && [[ "${errored_cnt}" -gt 0 ]]; then
    fail "Exiting non-zero: ${errored_cnt} capture error(s) detected."
    exit_code=1
  fi

  return "${exit_code}"
}

main "$@"
