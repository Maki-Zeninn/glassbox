#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# scripts/generate-changelog.sh — generate CHANGELOG.md from fragment files.
#
# Reads every .toml file in changelog/fragments/ (excluding the example),
# validates them via validate-fragments.sh, then writes a categorised release
# section to CHANGELOG.md (or stdout with --dry-run).
#
# Category order in output:
#   breaking → security → cli → schema → fix → performance
#
# Usage:
#   scripts/generate-changelog.sh                      # append to CHANGELOG.md
#   scripts/generate-changelog.sh --dry-run            # print to stdout only
#   scripts/generate-changelog.sh --version v1.2.3     # override version tag
#   scripts/generate-changelog.sh --output FILE        # write to a custom file
#
# After a successful release the consumed fragments should be archived:
#   git mv changelog/fragments/*.toml changelog/released/<version>/
#
# Exit codes:
#   0  success
#   1  fragment validation failed or other error

set -euo pipefail

FRAG_DIR="${FRAG_DIR:-changelog/fragments}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHANGELOG_FILE="CHANGELOG.md"
DRY_RUN=false
VERSION=""
OUTPUT_FILE=""

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)    DRY_RUN=true ;;
    --version)    VERSION="$2"; shift ;;
    --output)     OUTPUT_FILE="$2"; shift ;;
    -h|--help)
      echo "Usage: $0 [--dry-run] [--version TAG] [--output FILE]"
      exit 0 ;;
    *) echo "Unknown argument: $1"; exit 1 ;;
  esac
  shift
done

if [[ -n "$OUTPUT_FILE" ]]; then
  CHANGELOG_FILE="$OUTPUT_FILE"
fi

# ── Validate fragments first ──────────────────────────────────────────────────
"${SCRIPT_DIR}/validate-fragments.sh" || exit 1

# ── Helpers ───────────────────────────────────────────────────────────────────
get_field() {
  local file="$1" key="$2"
  grep -E "^${key}\s*=" "$file" 2>/dev/null \
    | head -1 \
    | sed -E 's/^[^=]+=\s*//' \
    | tr -d '"' | tr -d "'" | tr -d '\r' | xargs
}

get_array() {
  local file="$1" key="$2"
  grep -E "^${key}\s*=" "$file" 2>/dev/null \
    | head -1 \
    | sed -E 's/^[^=]+=\s*\[//' | sed 's/\]//' \
    | tr ',' ' ' | tr -d '"' | tr -d "'" | tr -d '\r' | xargs
}

# ── Resolve version ───────────────────────────────────────────────────────────
if [[ -z "$VERSION" ]]; then
  VERSION="$(git describe --tags --abbrev=0 2>/dev/null || echo "unreleased")"
fi
DATE="$(date -u +"%Y-%m-%d")"

# ── Collect fragments by category ────────────────────────────────────────────
declare -A cat_lines  # category -> newline-separated bullet lines

CATEGORY_ORDER="breaking security cli schema fix performance"
for cat in $CATEGORY_ORDER; do
  cat_lines[$cat]=""
done

fragment_count=0
for f in "$FRAG_DIR"/*.toml; do
  [[ -f "$f" ]] || continue
  fname="$(basename "$f")"
  [[ "$fname" == "example-cli-flag.toml" ]] && continue

  category="$(get_field "$f" "category")"
  pr="$(get_field "$f" "pr")"
  summary="$(get_field "$f" "summary")"
  details="$(get_field "$f" "details")"
  breaking="$(get_field "$f" "breaking")"
  migration_note="$(get_field "$f" "migration_note")"

  # Build bullet line
  line="- ${summary} ([#${pr}](https://github.com/dotandev/glassbox/pull/${pr}))"
  if [[ -n "$details" ]]; then
    line="${line}
  ${details}"
  fi
  if [[ "$breaking" == "true" && -n "$migration_note" ]]; then
    line="${line}
  **Migration:** ${migration_note}"
  fi

  # Append to category bucket (newline-separated)
  if [[ -n "${cat_lines[$category]+x}" && -n "${cat_lines[$category]}" ]]; then
    cat_lines[$category]="${cat_lines[$category]}
${line}"
  else
    cat_lines[$category]="${line}"
  fi

  fragment_count=$((fragment_count + 1))
done

if [[ $fragment_count -eq 0 ]]; then
  echo "[INFO] No fragments found — nothing to generate"
  exit 0
fi

# ── Category display names ────────────────────────────────────────────────────
declare -A cat_heading
cat_heading[breaking]="### Breaking Changes"
cat_heading[security]="### Security"
cat_heading[cli]="### CLI"
cat_heading[schema]="### Schema & Output"
cat_heading[fix]="### Bug Fixes"
cat_heading[performance]="### Performance"

# ── Build the release section ─────────────────────────────────────────────────
section=""
section+="## [${VERSION}] — ${DATE}"$'\n'
section+=$'\n'

has_breaking=false
for cat in $CATEGORY_ORDER; do
  [[ -z "${cat_lines[$cat]}" ]] && continue
  [[ "$cat" == "breaking" ]] && has_breaking=true
  section+="${cat_heading[$cat]}"$'\n'
  section+=$'\n'
  section+="${cat_lines[$cat]}"$'\n'
  section+=$'\n'
done

if $has_breaking; then
  section+="---"$'\n'
  section+=$'\n'
  section+="> **⚠ Breaking changes in this release.** Review the entries marked"$'\n'
  section+="> **Migration:** above and consult [MIGRATION_GUIDE.md](MIGRATION_GUIDE.md)."$'\n'
  section+=$'\n'
fi

# ── Output ────────────────────────────────────────────────────────────────────
if $DRY_RUN; then
  echo "── dry-run: generated release section ──"
  echo ""
  echo "$section"
  echo "── end dry-run ──"
  echo ""
  echo "[INFO] $fragment_count fragment(s) consumed (dry-run, no files written)"
  exit 0
fi

# Prepend the new section below the first line (the # Changelog header) if the
# file exists; otherwise create a fresh CHANGELOG.md.
if [[ -f "$CHANGELOG_FILE" ]]; then
  # Insert after the first line
  head -1 "$CHANGELOG_FILE" > "${CHANGELOG_FILE}.tmp"
  echo "" >> "${CHANGELOG_FILE}.tmp"
  echo "$section" >> "${CHANGELOG_FILE}.tmp"
  tail -n +2 "$CHANGELOG_FILE" >> "${CHANGELOG_FILE}.tmp"
  mv "${CHANGELOG_FILE}.tmp" "$CHANGELOG_FILE"
else
  {
    echo "# Changelog"
    echo ""
    echo "$section"
  } > "$CHANGELOG_FILE"
fi

echo "[OK] ${fragment_count} fragment(s) written to ${CHANGELOG_FILE} as ${VERSION}"
echo ""
echo "Next steps:"
echo "  1. Review ${CHANGELOG_FILE}"
echo "  2. Archive fragments: git mv changelog/fragments/*.toml changelog/released/${VERSION}/"
echo "  3. Commit both files in the release PR"
