#!/usr/bin/env bash
set -euo pipefail

# Aggregate one or more validation summary.json files into a quarterly compatibility report.
# Usage: scripts/generate-compat-report.sh <dir-with-summary-jsons> <output-md>

INPUT_DIR=${1:-ci-artifacts/validation}
OUTFILE=${2:-ci-artifacts/validation/compatibility_report.md}

echo "Generating compatibility report from: $INPUT_DIR -> $OUTFILE"

echo "# Quarterly Compatibility Report" > "$OUTFILE"
echo "Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$OUTFILE"
echo "" >> "$OUTFILE"

for f in $(find "$INPUT_DIR" -name summary.json -print 2>/dev/null); do
  echo "Processing $f"
  echo "## Run: $f" >> "$OUTFILE"
  python3 - <<PY >> "$OUTFILE"
import json,sys
f=sys.argv[1]
data=json.load(open(f))
for e in data:
    print('- %s: exit=%s fingerprint=%s' % (e.get('name'), e.get('exitcode'), e.get('fingerprint')))
PY
done

echo "Compatibility report written to $OUTFILE"
