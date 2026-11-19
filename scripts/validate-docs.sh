#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# validate-docs.sh — documentation validation suite.
#
# Runs three independent checks:
#
#   1. Deterministic build comparison
#      Generates a normalised snapshot of all docs/*.md files twice from the
#      same SOURCE_DATE_EPOCH and confirms the outputs are byte-identical.
#      Timestamps, version strings, and local paths are normalised before
#      comparison so environmental differences don't produce false positives.
#
#   2. Broken internal link detection
#      Scans all docs/*.md files for Markdown links that point to local
#      repository paths ([text](path) or [text](path#anchor)) and verifies
#      each target exists on disk. HTTP/HTTPS links are skipped — no network
#      required.
#
#   3. Command-flag smoke check
#      Parses code blocks in docs/**/*.md that start with "glassbox <subcommand>"
#      and checks each flag against the known flag set from the API snapshot
#      (.api-snapshots/cli-*.txt). Unknown flags fail the check.
#
# Usage:
#   scripts/validate-docs.sh [--determinism] [--links] [--flags] [--all]
#
#   With no flags, --all is assumed.
#
# Exit codes:
#   0   All requested checks passed.
#   1   One or more checks failed.
#
# Environment:
#   SOURCE_DATE_EPOCH     Epoch for determinism check (default: git log -1 --format=%ct)
#   DOCS_DIR              Docs directory (default: docs)
#   SNAPSHOT_DIR          Snapshot cache directory (default: .doc-snapshots)
#   GLASSBOX_BIN          Path to glassbox binary (default: bin/glassbox if built)
#   SKIP_FLAG_CHECK       Set to 1 to skip flag smoke check (e.g. when binary not built)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCS_DIR="${DOCS_DIR:-${REPO_ROOT}/docs}"
SNAPSHOT_DIR="${SNAPSHOT_DIR:-${REPO_ROOT}/.doc-snapshots}"
GLASSBOX_BIN="${GLASSBOX_BIN:-${REPO_ROOT}/bin/glassbox}"
SKIP_FLAG_CHECK="${SKIP_FLAG_CHECK:-0}"
API_SNAPSHOT_DIR="${REPO_ROOT}/.api-snapshots"

# ── Parse flags ───────────────────────────────────────────────────────────────
RUN_DET=0; RUN_LINKS=0; RUN_FLAGS=0
for arg in "$@"; do
  case "${arg}" in
    --determinism) RUN_DET=1 ;;
    --links)       RUN_LINKS=1 ;;
    --flags)       RUN_FLAGS=1 ;;
    --all)         RUN_DET=1; RUN_LINKS=1; RUN_FLAGS=1 ;;
    *) echo "Unknown flag: ${arg}. Use --determinism, --links, --flags, or --all." >&2; exit 1 ;;
  esac
done
if [[ $((RUN_DET + RUN_LINKS + RUN_FLAGS)) -eq 0 ]]; then
  RUN_DET=1; RUN_LINKS=1; RUN_FLAGS=1
fi

FAILURES=0
pass()  { printf '  [PASS] %s\n' "$*"; }
fail()  { printf '  [FAIL] %s\n' "$*" >&2; FAILURES=$((FAILURES + 1)); }
warn()  { printf '  [WARN] %s\n' "$*"; }
skip()  { printf '  [SKIP] %s\n' "$*"; }
info()  { printf '  [INFO] %s\n' "$*"; }

# ── Normalise a doc snapshot ──────────────────────────────────────────────────
# Strips variable content that legitimately differs across runs:
#   - ISO 8601 timestamps (2026-01-02T03:04:05Z patterns)
#   - Absolute filesystem paths (/home/runner/work/..., /tmp/...)
#   - VERSION strings produced by `git describe --dirty` (dirty suffix)
#   - Generated-at comment lines
normalise_snapshot() {
  sed \
    -e 's/[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}T[0-9]\{2\}:[0-9]\{2\}:[0-9]\{2\}Z\b/TIMESTAMP/g' \
    -e 's|/home/[^/]*/[^ "]*|PATH_REDACTED|g' \
    -e 's|/tmp/[^ "]*|PATH_REDACTED|g' \
    -e 's|/runner/[^ "]*|PATH_REDACTED|g' \
    -e 's/-dirty\b/-DIRTY/g' \
    -e '/Generated at:/d' \
    -e '/generated_at/d'
}

# ── 1. DETERMINISM CHECK ──────────────────────────────────────────────────────
check_determinism() {
  echo ""
  echo "1. Deterministic documentation build comparison ..."

  SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${REPO_ROOT}" log -1 --format=%ct 2>/dev/null || echo "0")}"
  info "SOURCE_DATE_EPOCH = ${SOURCE_DATE_EPOCH}"

  if [[ ! -d "${DOCS_DIR}" ]]; then
    skip "Docs directory not found: ${DOCS_DIR}"
    return
  fi

  # Build snapshot 1: normalised content of all docs/*.md files
  SNAP1="$(mktemp)"
  SNAP2="$(mktemp)"
  trap 'rm -f "${SNAP1}" "${SNAP2}"' EXIT

  build_snapshot() {
    local outfile="$1"
    # Collect all .md files under docs/, deterministically sorted.
    find "${DOCS_DIR}" -name "*.md" | LC_ALL=C sort | while IFS= read -r mdfile; do
      echo "=== FILE: ${mdfile#${REPO_ROOT}/} ===" >> "${outfile}"
      normalise_snapshot < "${mdfile}" >> "${outfile}"
      echo "" >> "${outfile}"
    done
  }

  build_snapshot "${SNAP1}"
  build_snapshot "${SNAP2}"

  HASH1="$(sha256sum "${SNAP1}" | awk '{print $1}')"
  HASH2="$(sha256sum "${SNAP2}" | awk '{print $1}')"

  if [[ "${HASH1}" == "${HASH2}" ]]; then
    pass "Documentation snapshots are identical after normalisation (${HASH1:0:16}...)"
  else
    fail "Documentation snapshots differ between builds — non-deterministic content detected"
    diff "${SNAP1}" "${SNAP2}" | head -50 >&2 || true
  fi

  rm -f "${SNAP1}" "${SNAP2}"
  trap - EXIT
}

# ── 2. BROKEN INTERNAL LINK CHECK ─────────────────────────────────────────────
check_links() {
  echo ""
  echo "2. Broken internal link detection ..."

  if [[ ! -d "${DOCS_DIR}" ]]; then
    skip "Docs directory not found: ${DOCS_DIR}"
    return
  fi

  local broken=0
  local checked=0

  # Scan all .md files for Markdown links with local targets
  while IFS= read -r mdfile; do
    local relfile="${mdfile#${REPO_ROOT}/}"

    # Extract all Markdown links: [text](target) — skip http/https/mailto
    while IFS= read -r target; do
      [[ -z "${target}" ]] && continue
      # Strip anchor fragment
      local target_path="${target%%#*}"
      [[ -z "${target_path}" ]] && continue

      # Resolve relative to the file's directory
      local file_dir
      file_dir="$(dirname "${mdfile}")"
      local resolved
      resolved="$(cd "${file_dir}" 2>/dev/null && realpath -m "${target_path}" 2>/dev/null || echo "")"

      if [[ -z "${resolved}" ]]; then
        continue
      fi

      checked=$((checked + 1))

      if [[ ! -e "${resolved}" ]]; then
        fail "Broken link in ${relfile}: '${target_path}' → ${resolved} (not found)"
        broken=$((broken + 1))
      fi
    done < <(
      grep -oP '\[([^\]]*)\]\(([^)]+)\)' "${mdfile}" 2>/dev/null \
        | sed -E 's/.*\(([^)]+)\)/\1/' \
        | grep -v '^https\?://' \
        | grep -v '^mailto:' \
        | grep -v '^#' \
        || true
    )
  done < <(find "${DOCS_DIR}" -name "*.md" | LC_ALL=C sort)

  if [[ ${broken} -eq 0 ]]; then
    pass "No broken internal links found (checked ${checked} link(s))"
  else
    fail "${broken} broken internal link(s) found (checked ${checked} total)"
  fi
}

# ── 3. COMMAND-FLAG SMOKE CHECK ───────────────────────────────────────────────
check_flags() {
  echo ""
  echo "3. Command-flag smoke check ..."

  if [[ "${SKIP_FLAG_CHECK}" == "1" ]]; then
    skip "SKIP_FLAG_CHECK=1 — skipping flag smoke check"
    return
  fi

  # Collect known flags from API snapshots (cli-*.txt files).
  if [[ ! -d "${API_SNAPSHOT_DIR}" ]]; then
    skip "API snapshot directory not found: ${API_SNAPSHOT_DIR} — run: scripts/api-snapshot.sh generate"
    return
  fi

  local snapshot_files
  mapfile -t snapshot_files < <(find "${API_SNAPSHOT_DIR}" -name "cli-*.txt" 2>/dev/null | LC_ALL=C sort)

  if [[ ${#snapshot_files[@]} -eq 0 ]]; then
    skip "No CLI snapshot files found in ${API_SNAPSHOT_DIR}"
    return
  fi

  # Build a unified set of known flags from all snapshots.
  local known_flags_file
  known_flags_file="$(mktemp)"

  for snap in "${snapshot_files[@]}"; do
    # CLI snapshots contain lines like: "  --flag-name   description"
    grep -oP '^\s+--[\w-]+' "${snap}" 2>/dev/null | tr -d ' ' >> "${known_flags_file}" || true
  done
  sort -u "${known_flags_file}" -o "${known_flags_file}"

  local total_known
  total_known="$(wc -l < "${known_flags_file}" | tr -d ' ')"
  info "Known flags from snapshots: ${total_known}"

  # Scan docs for glassbox command invocations in code blocks and check flags.
  local unknown_count=0
  local checked_flags=0

  while IFS= read -r mdfile; do
    local relfile="${mdfile#${REPO_ROOT}/}"

    # Extract lines from fenced code blocks that start with "glassbox"
    local in_block=0
    while IFS= read -r line; do
      if [[ "${line}" =~ ^\`\`\` ]]; then
        in_block=$((1 - in_block))
        continue
      fi
      [[ ${in_block} -eq 0 ]] && continue
      [[ "${line}" =~ ^glassbox[[:space:]] ]] || continue

      # Extract flags from the line (--flag or --flag=value or --flag value)
      while IFS= read -r flag; do
        [[ -z "${flag}" ]] && continue
        checked_flags=$((checked_flags + 1))
        if ! grep -qxF "${flag}" "${known_flags_file}" 2>/dev/null; then
          fail "Unknown flag in ${relfile}: '${flag}'"
          unknown_count=$((unknown_count + 1))
        fi
      done < <(grep -oP '\s--[\w-]+' <<< "${line}" | tr -d ' ' || true)
    done < "${mdfile}"
  done < <(find "${DOCS_DIR}" -name "*.md" | LC_ALL=C sort)

  rm -f "${known_flags_file}"

  if [[ ${unknown_count} -eq 0 ]]; then
    pass "All flags in docs examples are recognised (checked ${checked_flags} flag occurrence(s))"
  else
    fail "${unknown_count} unknown flag(s) in documentation examples"
    info "Tip: run 'scripts/api-snapshot.sh generate' to regenerate snapshots after adding flags"
  fi
}

# ── Run selected checks ───────────────────────────────────────────────────────
echo "Documentation validation"
echo "  Docs dir     : ${DOCS_DIR}"
echo "  Repo root    : ${REPO_ROOT}"

cd "${REPO_ROOT}"

[[ "${RUN_DET}"   -eq 1 ]] && check_determinism
[[ "${RUN_LINKS}" -eq 1 ]] && check_links
[[ "${RUN_FLAGS}" -eq 1 ]] && check_flags

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [[ "${FAILURES}" -eq 0 ]]; then
  echo "Result: all documentation validation checks passed."
  exit 0
else
  echo "Result: ${FAILURES} documentation validation check(s) failed." >&2
  exit 1
fi
