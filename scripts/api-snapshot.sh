#!/usr/bin/env bash
# Copyright (c) glassbox Authors.
# SPDX-License-Identifier: Apache-2.0
#
# scripts/api-snapshot.sh — API compatibility snapshot tool.
#
# Issue #597: Add API compatibility checks for generated artifacts.
#
# Generates or compares API snapshots for:
#   1. TypeScript exports (src/index.ts + src/audit/browser/index.ts)
#   2. CLI help output (glassbox --help and each sub-command)
#   3. Go public package symbols (go doc)
#   4. JSON schemas in docs/schema/
#
# Usage:
#   scripts/api-snapshot.sh generate   # write snapshots to .api-snapshots/
#   scripts/api-snapshot.sh check      # compare current vs stored snapshots
#   scripts/api-snapshot.sh update     # alias for generate (after intentional change)
#
# The check command exits non-zero and prints a diff when any snapshot differs.
# Run `scripts/api-snapshot.sh generate` to accept intentional changes, then
# review the diff in your PR.
#
# Environment:
#   GLASSBOX_BIN   — path to glassbox binary (default: ./glassbox)
#   SNAPSHOT_DIR   — directory for snapshots (default: .api-snapshots)

set -euo pipefail

# ─── Configuration ─────────────────────────────────────────────────────────────

GLASSBOX_BIN="${GLASSBOX_BIN:-./glassbox}"
SNAPSHOT_DIR="${SNAPSHOT_DIR:-.api-snapshots}"
TS_ENTRY="src/index.ts"
BROWSER_ENTRY="src/audit/browser/index.ts"
SCHEMA_DIR="docs/schema"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_ok()   { echo -e "${GREEN}[OK]${NC}  $1"; }
print_fail() { echo -e "${RED}[FAIL]${NC} $1"; }
print_info() { echo -e "${YELLOW}[INFO]${NC} $1"; }
print_head() { echo -e "${BLUE}=== $1 ===${NC}"; }

# ─── Helpers ───────────────────────────────────────────────────────────────────

# Normalise CLI help: strip version strings and binary paths that change between builds.
normalise_help() {
  sed \
    -e 's/glassbox version [0-9][^ ]*/glassbox version X.X.X/g' \
    -e 's|/[^ ]*glassbox\b|glassbox|g'
}

# Extract exported symbols from a TypeScript entry point.
# Reads 'export' lines (not re-exports of * from Node built-ins).
extract_ts_exports() {
  local file="$1"
  # Collect named exports and re-exported modules, sorted deterministically.
  grep -E '^export\s+(type\s+)?\{|^export\s+(type\s+)?(function|class|const|interface|enum)' \
    "$file" 2>/dev/null \
    | sed 's/\/\/.*//' \
    | tr -d '\r' \
    | sort \
    || true

  # Also collect 'export { ... } from' lines
  grep -E "^export\s+\{" "$file" 2>/dev/null \
    | sort \
    || true
}

# Extract public Go symbols from a package directory using go doc.
extract_go_symbols() {
  local pkg="$1"
  go doc "$pkg" 2>/dev/null \
    | grep -E '^(func|type|var|const) ' \
    | sort \
    || true
}

# ─── Generate snapshot for a single artefact ──────────────────────────────────

generate_ts_snapshot() {
  local name="$1"
  local file="$2"
  local out="${SNAPSHOT_DIR}/ts-${name}.txt"

  if [ ! -f "$file" ]; then
    print_info "TypeScript entry $file not found — skipping"
    return 0
  fi

  extract_ts_exports "$file" > "$out"
  print_ok "ts-${name}: $(wc -l < "$out") export lines"
}

generate_cli_snapshot() {
  local subcmd="$1"
  local out="${SNAPSHOT_DIR}/cli-${subcmd:-root}.txt"

  if [ ! -x "$GLASSBOX_BIN" ]; then
    print_info "glassbox binary not found at $GLASSBOX_BIN — skipping CLI snapshots"
    return 0
  fi

  if [ -z "$subcmd" ]; then
    "$GLASSBOX_BIN" --help 2>&1 | normalise_help > "$out"
  else
    "$GLASSBOX_BIN" "$subcmd" --help 2>&1 | normalise_help > "$out"
  fi
  print_ok "cli-${subcmd:-root}: $(wc -l < "$out") help lines"
}

generate_go_snapshot() {
  local pkg="$1"
  local label="$2"
  local out="${SNAPSHOT_DIR}/go-${label}.txt"

  if ! command -v go &>/dev/null; then
    print_info "go not found — skipping Go API snapshots"
    return 0
  fi

  extract_go_symbols "$pkg" > "$out"
  print_ok "go-${label}: $(wc -l < "$out") symbols"
}

generate_schema_snapshot() {
  local out="${SNAPSHOT_DIR}/schemas.txt"

  if [ ! -d "$SCHEMA_DIR" ]; then
    print_info "schema directory $SCHEMA_DIR not found — skipping"
    return 0
  fi

  # List all schema files with their SHA-256 checksums (deterministic)
  find "$SCHEMA_DIR" -name "*.json" -o -name "*.yaml" -o -name "*.yml" \
    | sort \
    | xargs sha256sum 2>/dev/null \
    | awk '{print $1, $2}' \
    > "$out"
  print_ok "schemas: $(wc -l < "$out") files"
}

# ─── Commands ─────────────────────────────────────────────────────────────────

cmd_generate() {
  print_head "Generating API snapshots → ${SNAPSHOT_DIR}/"
  mkdir -p "$SNAPSHOT_DIR"

  # TypeScript exports
  generate_ts_snapshot "main" "$TS_ENTRY"
  generate_ts_snapshot "browser" "$BROWSER_ENTRY"

  # CLI help snapshots
  local cli_subcmds=(
    "" "debug" "audit:sign" "audit:verify" "session" "cache" "version"
    "protocol:register" "protocol:handle"
  )
  for subcmd in "${cli_subcmds[@]}"; do
    generate_cli_snapshot "$subcmd"
  done

  # Go public packages
  local go_pkg_root="github.com/dotandev/glassbox"
  local go_pkgs=(
    "internal/audit" "internal/rpc" "internal/simulator"
    "internal/signer" "internal/snapshot" "internal/session"
    "internal/trace" "internal/errors"
  )
  for pkg in "${go_pkgs[@]}"; do
    local label="${pkg//\//-}"
    generate_go_snapshot "${go_pkg_root}/${pkg}" "$label"
  done

  # JSON schemas
  generate_schema_snapshot

  echo ""
  print_ok "Snapshots written to ${SNAPSHOT_DIR}/"
  echo ""
  echo "  To check for regressions:   scripts/api-snapshot.sh check"
  echo "  To accept intentional changes: scripts/api-snapshot.sh generate"
}

cmd_check() {
  print_head "Checking API snapshots against ${SNAPSHOT_DIR}/"

  if [ ! -d "$SNAPSHOT_DIR" ]; then
    print_fail "Snapshot directory '${SNAPSHOT_DIR}' does not exist."
    echo "  Run: scripts/api-snapshot.sh generate"
    exit 1
  fi

  # Generate fresh snapshots into a temp directory, then diff
  local tmp
  tmp="$(mktemp -d)"
  trap "rm -rf '$tmp'" EXIT

  SNAPSHOT_DIR="$tmp" cmd_generate > /dev/null 2>&1

  local failures=0

  # Compare each generated snapshot against the stored one
  for new_snap in "$tmp"/*.txt; do
    local name
    name="$(basename "$new_snap")"
    local stored="${SNAPSHOT_DIR}/${name}"

    if [ ! -f "$stored" ]; then
      print_fail "${name}: stored snapshot does not exist (run 'generate' first)"
      failures=$((failures + 1))
      continue
    fi

    if ! diff -u "$stored" "$new_snap" > /dev/null 2>&1; then
      print_fail "${name}: API changed — diff:"
      diff -u "$stored" "$new_snap" | head -60 || true
      echo ""
      echo "  To accept: scripts/api-snapshot.sh generate"
      echo "  Include a migration note in your PR description."
      failures=$((failures + 1))
    else
      print_ok "${name}: unchanged"
    fi
  done

  echo ""
  if [ "$failures" -gt 0 ]; then
    print_fail "$failures snapshot(s) changed. See diffs above."
    exit 1
  else
    print_ok "All API snapshots match."
  fi
}

# ─── Entry point ──────────────────────────────────────────────────────────────

case "${1:-generate}" in
  generate|update)
    cmd_generate
    ;;
  check)
    cmd_check
    ;;
  help|--help|-h)
    cat <<EOF
Usage: scripts/api-snapshot.sh <command>

Commands:
  generate   Write API snapshots to ${SNAPSHOT_DIR}/
  check      Compare current API against stored snapshots; exit 1 on diff
  update     Alias for generate

Environment:
  GLASSBOX_BIN   Path to glassbox binary (default: ./glassbox)
  SNAPSHOT_DIR   Snapshot directory    (default: .api-snapshots)

Snapshot files (${SNAPSHOT_DIR}/):
  ts-main.txt       TypeScript exports from src/index.ts
  ts-browser.txt    TypeScript exports from src/audit/browser/index.ts
  cli-*.txt         CLI --help output for each sub-command
  go-*.txt          Go public package symbols
  schemas.txt       SHA-256 checksums of docs/schema/*.json

Updating snapshots:
  1. Make your intentional API change.
  2. Run: scripts/api-snapshot.sh generate
  3. Review the diff: git diff .api-snapshots/
  4. Add a migration note to your PR description.
  5. Commit .api-snapshots/ alongside the code change.
EOF
    ;;
  *)
    echo "Unknown command: $1"
    echo "Usage: scripts/api-snapshot.sh [generate|check|update|help]"
    exit 1
    ;;
esac
