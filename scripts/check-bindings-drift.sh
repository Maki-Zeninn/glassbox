#!/usr/bin/env bash
# scripts/check-bindings-drift.sh
#
# Detects drift between the canonical Go command schema and the committed
# TypeScript artifacts under src/bindings/.
#
# This script performs byte-level comparison to ensure deterministic generation
# across platforms and environments. Line endings are normalized to LF.
#
# Usage: scripts/check-bindings-drift.sh [--output-dir DIR]
#
# Exit codes:
#   0  Committed TypeScript files match freshly generated output
#   1  Drift detected — run `glassbox generate-schema --output src/bindings` to fix
#   2  Build or generation error
set -euo pipefail

OUTPUT_DIR="${1:-src/bindings}"
TMPDIR_WORK="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_WORK"' EXIT

echo "Checking TypeScript schema bindings drift in: $OUTPUT_DIR"
echo ""

# Re-generate into a temp directory.
if ! go run internal/bindings/cmd/generate_schema_main.go --output "$TMPDIR_WORK" 2>&1; then
  echo ""
  echo "[ERROR] Schema generation failed. Fix compilation errors and retry."
  exit 2
fi

DRIFT=0
for f in command-schema.ts command-types.ts command-validators.ts index.ts; do
  committed="$OUTPUT_DIR/$f"
  fresh="$TMPDIR_WORK/$f"
  if [ ! -f "$committed" ]; then
    echo "  MISSING  $f  (run: glassbox generate-schema --output $OUTPUT_DIR)"
    DRIFT=1
    continue
  fi

  # Normalize line endings for cross-platform comparison
  if command -v dos2unix >/dev/null 2>&1; then
    dos2unix -q "$committed" "$fresh" 2>/dev/null || true
  fi

  if ! diff -q "$committed" "$fresh" > /dev/null 2>&1; then
    echo "  DRIFT    $f"
    diff --unified=3 "$committed" "$fresh" || true
    DRIFT=1
  else
    echo "  OK       $f"
  fi
done

echo ""
if [ "$DRIFT" -eq 0 ]; then
  echo "[OK] All TypeScript schema bindings are up-to-date and byte-stable."
  exit 0
else
  echo "[DRIFT] Bindings are out of date."
  echo "Fix: go run internal/bindings/cmd/generate_schema_main.go && git add $OUTPUT_DIR && git commit"
  exit 1
fi
