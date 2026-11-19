#!/usr/bin/env bash
# health-report.sh — repository health and stale-code report
#
# Identifies stale TODOs, placeholder implementations, unreferenced docs,
# generated-file drift, and old compatibility shims. Designed to run in CI
# without blocking on every TODO; only stale suppressions cause a non-zero exit.
#
# Usage:
#   scripts/health-report.sh [--json] [--suppress <suppress-file>]
#
# Suppression file format: .glassbox-health-suppress.json (see example file)
# Set HEALTH_REPORT_OUTPUT_FORMAT=json to get machine-readable output.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

OUTPUT_FORMAT="${HEALTH_REPORT_OUTPUT_FORMAT:-text}"
SUPPRESS_FILE="${REPO_ROOT}/.glassbox-health-suppress.json"
FAIL_ON_STALE_SUPPRESS=true
EXIT_CODE=0

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) OUTPUT_FORMAT="json" ;;
    --suppress)
      shift
      SUPPRESS_FILE="$1"
      ;;
    --no-fail-stale) FAIL_ON_STALE_SUPPRESS=false ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
  shift
done

TODAY="$(date +%Y-%m-%d)"

# ── Suppression loader ────────────────────────────────────────────────────────
declare -A SUPPRESSED_PATTERNS  # key = "FILE:LINE" or pattern, value = expiry

load_suppressions() {
  if [[ ! -f "${SUPPRESS_FILE}" ]]; then
    return
  fi
  # Requires jq for JSON parsing; fall back gracefully if absent.
  if ! command -v jq &>/dev/null; then
    echo "[WARN] jq not found; suppression file ignored" >&2
    return
  fi
  # Each entry: { "pattern": "...", "owner": "...", "expires": "YYYY-MM-DD", "reason": "..." }
  while IFS= read -r entry; do
    pattern="$(echo "${entry}" | jq -r '.pattern')"
    expires="$(echo "${entry}" | jq -r '.expires // ""')"
    SUPPRESSED_PATTERNS["${pattern}"]="${expires}"
  done < <(jq -c '.[]' "${SUPPRESS_FILE}" 2>/dev/null)
}

is_suppressed() {
  local text="$1"
  for pattern in "${!SUPPRESSED_PATTERNS[@]}"; do
    if echo "${text}" | grep -qF "${pattern}" 2>/dev/null; then
      local expires="${SUPPRESSED_PATTERNS[${pattern}]}"
      if [[ -n "${expires}" && "${expires}" < "${TODAY}" ]]; then
        # Stale suppression — report it and keep scanning.
        echo "STALE_SUPPRESS: pattern='${pattern}' expired=${expires}" >&2
        if [[ "${FAIL_ON_STALE_SUPPRESS}" == "true" ]]; then
          EXIT_CODE=1
        fi
        return 1
      fi
      return 0
    fi
  done
  return 1
}

# ── Finding collectors ────────────────────────────────────────────────────────

declare -a FINDINGS  # array of "category|file|line|text"

add_finding() {
  local category="$1" file="$2" line="$3" text="$4"
  FINDINGS+=("${category}|${file}|${line}|${text}")
}

# Scan Go and Rust source for TODO/FIXME/HACK/PLACEHOLDER/XXX markers.
scan_code_markers() {
  local patterns='TODO|FIXME|HACK|PLACEHOLDER|XXX|WORKAROUND'

  while IFS= read -r match; do
    local file line text
    file="$(echo "${match}" | cut -d: -f1)"
    line="$(echo "${match}" | cut -d: -f2)"
    text="$(echo "${match}" | cut -d: -f3-)"

    # Skip generated files and vendor directories.
    case "${file}" in
      */vendor/*|*/testdata/*|*_gen.go|*.pb.go) continue ;;
    esac

    if is_suppressed "${file}:${line}"; then continue; fi
    if is_suppressed "${text}"; then continue; fi

    add_finding "code_marker" "${file}" "${line}" "${text}"
  done < <(
    grep -rn --include="*.go" --include="*.rs" -E "${patterns}" "${REPO_ROOT}" 2>/dev/null \
      | grep -v "^Binary" \
      | sed 's/[[:space:]]*$//' \
      | head -500
  )
}

# Detect Go files that export placeholder functions (empty body with a comment).
scan_placeholder_implementations() {
  while IFS= read -r match; do
    local file line text
    file="$(echo "${match}" | cut -d: -f1)"
    line="$(echo "${match}" | cut -d: -f2)"
    text="$(echo "${match}" | cut -d: -f3-)"
    case "${file}" in */vendor/*|*/testdata/*) continue ;; esac
    if is_suppressed "${file}:${line}"; then continue; fi
    add_finding "placeholder_impl" "${file}" "${line}" "${text}"
  done < <(
    grep -rn --include="*.go" -E 'panic\("(not implemented|TODO|unimplemented)' "${REPO_ROOT}" 2>/dev/null \
      | head -200
  )
}

# Identify docs that are not referenced by any other file.
scan_unreferenced_docs() {
  local docs_dir="${REPO_ROOT}/docs"
  if [[ ! -d "${docs_dir}" ]]; then return; fi

  while IFS= read -r doc; do
    local basename
    basename="$(basename "${doc}" .md)"
    # Skip index files and the main README.
    case "${basename}" in README|index|CHANGES_QUICK_REFERENCE) continue ;; esac

    if is_suppressed "${doc}"; then continue; fi

    # Check whether any Go/Markdown/shell file references this doc by name.
    if ! grep -rqF "${basename}" "${REPO_ROOT}" \
        --include="*.go" --include="*.md" --include="*.sh" \
        --exclude-dir=".git" 2>/dev/null; then
      local relpath
      relpath="${doc#${REPO_ROOT}/}"
      add_finding "unreferenced_doc" "${relpath}" "0" "docs file '${basename}.md' is not referenced anywhere"
    fi
  done < <(find "${docs_dir}" -name "*.md" -type f)
}

# Detect generated Go files whose source is out of date (mtime heuristic).
scan_generated_drift() {
  while IFS= read -r genfile; do
    local srcfile
    # Look for a corresponding .go source with the same base name minus _gen.
    srcfile="${genfile/_gen.go/.go}"
    if [[ ! -f "${srcfile}" ]]; then continue; fi
    if [[ "${genfile}" -ot "${srcfile}" ]]; then
      local relpath
      relpath="${genfile#${REPO_ROOT}/}"
      if is_suppressed "${relpath}"; then continue; fi
      add_finding "generated_drift" "${relpath}" "0" \
        "generated file may be outdated (source '$(basename "${srcfile}")' is newer)"
    fi
  done < <(find "${REPO_ROOT}" -name "*_gen.go" -not -path "*/vendor/*" -not -path "*/.git/*")
}

# Detect compatibility shims marked for removal.
scan_compat_shims() {
  while IFS= read -r match; do
    local file line text
    file="$(echo "${match}" | cut -d: -f1)"
    line="$(echo "${match}" | cut -d: -f2)"
    text="$(echo "${match}" | cut -d: -f3-)"
    case "${file}" in */vendor/*|*/testdata/*) continue ;; esac
    if is_suppressed "${file}:${line}"; then continue; fi
    add_finding "compat_shim" "${file}" "${line}" "${text}"
  done < <(
    grep -rn --include="*.go" \
      -E '(deprecated|compat|legacy|backcompat|remove[[:space:]]after|drop[[:space:]]in)[^"]*' \
      "${REPO_ROOT}" 2>/dev/null \
      | grep -iv "vendor\|_test.go\|\.pb\.go" \
      | head -200
  )
}

# ── Report formatters ─────────────────────────────────────────────────────────

print_text_report() {
  local total="${#FINDINGS[@]}"
  echo "=== Glassbox Repository Health Report ==="
  echo "Date: ${TODAY}"
  echo "Total findings: ${total}"
  echo ""

  if [[ ${total} -eq 0 ]]; then
    echo "[OK] No health findings."
    return
  fi

  declare -A by_category
  for finding in "${FINDINGS[@]}"; do
    local cat="${finding%%|*}"
    by_category["${cat}"]="${by_category[${cat}]:-}${finding}\n"
  done

  for cat in "${!by_category[@]}"; do
    echo "── ${cat} ──────────────────────────────────"
    while IFS='|' read -r _ file line text; do
      printf "  %s:%s  %s\n" "${file}" "${line}" "${text}"
    done < <(printf '%b' "${by_category[${cat}]}")
    echo ""
  done
}

print_json_report() {
  if ! command -v jq &>/dev/null; then
    echo '{"error":"jq not available; run with text output"}' >&2
    return 1
  fi

  local json_findings="["
  local first=true
  for finding in "${FINDINGS[@]}"; do
    local cat file line text
    IFS='|' read -r cat file line text <<< "${finding}"
    [[ "${first}" == "false" ]] && json_findings+=","
    first=false
    json_findings+="$(jq -n \
      --arg cat "${cat}" \
      --arg file "${file}" \
      --argjson line "${line}" \
      --arg text "${text}" \
      '{"category":$cat,"file":$file,"line":$line,"text":$text}')"
  done
  json_findings+="]"

  jq -n \
    --arg date "${TODAY}" \
    --argjson total "${#FINDINGS[@]}" \
    --argjson findings "${json_findings}" \
    '{"date":$date,"total":$total,"findings":$findings}'
}

# ── Main ──────────────────────────────────────────────────────────────────────

cd "${REPO_ROOT}"
load_suppressions
scan_code_markers
scan_placeholder_implementations
scan_unreferenced_docs
scan_generated_drift
scan_compat_shims

if [[ "${OUTPUT_FORMAT}" == "json" ]]; then
  print_json_report
else
  print_text_report
fi

exit "${EXIT_CODE}"
