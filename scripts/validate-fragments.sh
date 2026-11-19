#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# scripts/validate-fragments.sh — validate changelog fragment files.
#
# Checks every .toml file in changelog/fragments/ (excluding the example and
# README) and exits non-zero if any rule is violated.
#
# Rules enforced:
#   1. Required fields present: category, pr, summary, breaking, affects
#   2. category is one of the allowed values
#   3. pr is a positive integer unique within the pending fragment set
#   4. summary is non-empty and ≤ 120 characters
#   5. breaking=true fragments must have a non-empty migration_note
#   6. affects contains only recognised surface names
#   7. No duplicate pr values across fragments (duplicate detector)
#
# Usage:
#   scripts/validate-fragments.sh                   # validate all fragments
#   scripts/validate-fragments.sh path/to/frag.toml # validate one file
#
# Exit codes:
#   0  all fragments valid
#   1  one or more validation errors

set -euo pipefail

FRAG_DIR="${FRAG_DIR:-changelog/fragments}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

ok()   { echo -e "${GREEN}[OK]${NC}  $1"; }
fail() { echo -e "${RED}[FAIL]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }

VALID_CATEGORIES="cli schema security breaking fix performance"
VALID_AFFECTS="cli-flags json-output session-format go-api schema exit-codes protocol extension"
VALID_COMPATIBILITY="stable beta experimental deprecated removed"

errors=0
declare -A seen_prs   # pr_value -> filename for duplicate detection

# ── TOML field extraction ─────────────────────────────────────────────────────
# Minimal TOML parser: extracts a scalar value for a given key.
# Handles:  key = "string value"   and   key = integer   and   key = true/false
get_field() {
  local file="$1"
  local key="$2"
  grep -E "^${key}\s*=" "$file" 2>/dev/null \
    | head -1 \
    | sed -E 's/^[^=]+=\s*//' \
    | tr -d '"' \
    | tr -d "'" \
    | tr -d '\r' \
    | xargs
}

# Extract array field value (returns space-separated items, quotes stripped)
get_array() {
  local file="$1"
  local key="$2"
  grep -E "^${key}\s*=" "$file" 2>/dev/null \
    | head -1 \
    | sed -E 's/^[^=]+=\s*\[//' \
    | sed 's/\]//' \
    | tr ',' ' ' \
    | tr -d '"' \
    | tr -d "'" \
    | tr -d '\r' \
    | xargs
}

# ── Validate a single fragment ────────────────────────────────────────────────
validate_fragment() {
  local file="$1"
  local fname
  fname="$(basename "$file")"
  local file_errors=0

  # Skip the example placeholder and non-toml files
  if [[ "$fname" == "example-cli-flag.toml" || "$fname" == README* ]]; then
    return 0
  fi
  if [[ "$fname" != *.toml ]]; then
    return 0
  fi

  # 1. Required fields
  for field in category pr summary breaking affects; do
    val="$(get_field "$file" "$field")"
    if [[ -z "$val" ]]; then
      fail "$fname: missing required field '$field'"
      file_errors=$((file_errors + 1))
    fi
  done

  # 2. category
  local category
  category="$(get_field "$file" "category")"
  if [[ -n "$category" ]]; then
    found=0
    for valid in $VALID_CATEGORIES; do
      [[ "$category" == "$valid" ]] && found=1 && break
    done
    if [[ $found -eq 0 ]]; then
      fail "$fname: invalid category '$category' — must be one of: $VALID_CATEGORIES"
      file_errors=$((file_errors + 1))
    fi
  fi

  # 3. pr is a positive integer
  local pr
  pr="$(get_field "$file" "pr")"
  if [[ -n "$pr" ]]; then
    if ! [[ "$pr" =~ ^[1-9][0-9]*$ ]]; then
      fail "$fname: pr must be a positive integer, got '$pr'"
      file_errors=$((file_errors + 1))
    else
      # Duplicate detection
      if [[ -n "${seen_prs[$pr]+x}" ]]; then
        fail "$fname: duplicate pr=$pr already used in ${seen_prs[$pr]}"
        file_errors=$((file_errors + 1))
      else
        seen_prs[$pr]="$fname"
      fi
    fi
  fi

  # 4. summary length
  local summary
  summary="$(get_field "$file" "summary")"
  if [[ -n "$summary" ]]; then
    local len="${#summary}"
    if [[ $len -gt 120 ]]; then
      fail "$fname: summary is ${len} chars (max 120): ${summary:0:80}..."
      file_errors=$((file_errors + 1))
    fi
  fi

  # 5. breaking fragments need migration_note
  local breaking
  breaking="$(get_field "$file" "breaking")"
  if [[ "$breaking" == "true" ]]; then
    local note
    note="$(get_field "$file" "migration_note")"
    if [[ -z "$note" ]]; then
      fail "$fname: breaking=true requires a non-empty migration_note"
      file_errors=$((file_errors + 1))
    fi
  fi

  # 6. affects values
  local affects
  affects="$(get_array "$file" "affects")"
  for surface in $affects; do
    found=0
    for valid in $VALID_AFFECTS; do
      [[ "$surface" == "$valid" ]] && found=1 && break
    done
    if [[ $found -eq 0 ]]; then
      fail "$fname: unknown surface '$surface' in affects — must be one of: $VALID_AFFECTS"
      file_errors=$((file_errors + 1))
    fi
  done

  # 7. compatibility level (optional field — validate if present)
  local compat
  compat="$(get_field "$file" "compatibility")"
  if [[ -n "$compat" ]]; then
    found=0
    for valid in $VALID_COMPATIBILITY; do
      [[ "$compat" == "$valid" ]] && found=1 && break
    done
    if [[ $found -eq 0 ]]; then
      fail "$fname: invalid compatibility '$compat' — must be one of: $VALID_COMPATIBILITY"
      file_errors=$((file_errors + 1))
    fi
  fi

  if [[ $file_errors -eq 0 ]]; then
    ok "$fname"
  fi
  errors=$((errors + file_errors))
}

# ── Main ──────────────────────────────────────────────────────────────────────

if [[ $# -gt 0 ]]; then
  # Validate specific files passed as arguments
  for f in "$@"; do
    validate_fragment "$f"
  done
else
  # Validate all fragments in the directory
  if [[ ! -d "$FRAG_DIR" ]]; then
    warn "Fragment directory '$FRAG_DIR' does not exist — nothing to validate"
    exit 0
  fi

  count=0
  for f in "$FRAG_DIR"/*.toml; do
    [[ -f "$f" ]] || continue
    validate_fragment "$f"
    count=$((count + 1))
  done

  if [[ $count -eq 0 ]]; then
    warn "No fragment files found in $FRAG_DIR"
    exit 0
  fi
fi

echo ""
if [[ $errors -gt 0 ]]; then
  fail "$errors error(s) found. Fix fragments before releasing."
  exit 1
else
  ok "All fragments valid."
fi
