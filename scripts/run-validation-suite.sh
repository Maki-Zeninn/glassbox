#!/usr/bin/env bash
set -euo pipefail

# Validation suite runner
# - Executes a small set of deterministic journeys against the built
#   `glassbox` binary using fixtures in `test/validation/fixtures/`.
# - Produces `ci-artifacts/validation/summary.json`, per-journey logs, and
#   `report.md` suitable for CI artifact upload. Runs redaction if available.

GLASSBOX_BINARY=${GLASSBOX_BINARY:-bin/glassbox}
OUTDIR=${OUTDIR:-ci-artifacts/validation}
ARTIFACT_MAX_MB=${ARTIFACT_MAX_MB:-50}
mkdir -p "$OUTDIR"

SUMMARY_JSON="$OUTDIR/summary.json"
echo '[]' > "$SUMMARY_JSON"

run_cmd() {
  name="$1"; shift
  outfile="$OUTDIR/${name}.log"
  echo "=== RUN: $name ($(date -u +%Y-%m-%dT%H:%M:%SZ)) ===" > "$outfile"
  if "$@" >> "$outfile" 2>&1; then
    code=0
  else
    code=$?
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    fp=$(sha256sum "$outfile" | awk '{print $1}')
  else
    fp=$(shasum -a 256 "$outfile" | awk '{print $1}')
  fi

  SUMMARY_JSON="$SUMMARY_JSON" \
    RUN_NAME="$name" RUN_CODE="$code" RUN_FP="$fp" RUN_OUT="$outfile" \
    python3 - <<'PY'
import os,json
f=os.environ['SUMMARY_JSON']
try:
    a=json.load(open(f))
except Exception:
    a=[]
a.append({
    'name': os.environ['RUN_NAME'],
    'exitcode': int(os.environ['RUN_CODE']),
    'fingerprint': os.environ['RUN_FP'],
    'output': os.environ['RUN_OUT'],
})
json.dump(a, open(f,'w'), indent=2)
PY
}

echo "Validation runner: using binary: $GLASSBOX_BINARY"

# Canonical tx hash from regression guide
CANONICAL_TX=5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab

# 1) Transaction debug (dry-run path to validate inputs deterministically)
run_cmd debug "$GLASSBOX_BINARY" debug --dry-run --network testnet "$CANONICAL_TX" || true

# 2) Trace command (load a deterministic trace fixture)
run_cmd trace "$GLASSBOX_BINARY" trace test/validation/fixtures/trace/sample.trace.json || true

# 3) Profile: analyze a trace fixture and export JSON
run_cmd profile "$GLASSBOX_BINARY" profile test/validation/fixtures/trace/sample.trace.json --out-json "$OUTDIR/profile.json" || true

# 4) Session save plan (dry run of session save to exercise save validations)
run_cmd session_plan "$GLASSBOX_BINARY" session save --plan || true

# 5) Audit verify directory using test-only keys
run_cmd audit_verify_dir "$GLASSBOX_BINARY" audit:verify-dir --dir test/validation/fixtures/audit || true

# 6) WASM local replay dry-run: validates local WASM replay path
run_cmd wasm_replay "$GLASSBOX_BINARY" debug --wasm test/validation/fixtures/sourcemap/placeholder.wasm --demo || true

# 7) Protocol register dry-run (OS writes are not performed in dry-run)
run_cmd protocol_register "$GLASSBOX_BINARY" protocol:register --dry-run || true

# 8) Release verification: get version metadata
run_cmd version "$GLASSBOX_BINARY" version --json || true

# Run redaction if available before uploading
if [ -f scripts/redact-logs.sh ]; then
  echo "Running redact-logs.sh on $OUTDIR"
  bash scripts/redact-logs.sh "$OUTDIR" "$ARTIFACT_MAX_MB" || true
fi

# Produce a human-readable markdown report from the JSON summary
REPORT_MD="$OUTDIR/report.md"
python3 - <<'PY'
import json,os,time
f=os.environ.get('SUMMARY_JSON','ci-artifacts/validation/summary.json')
try:
    data=json.load(open(f))
except Exception:
    data=[]
out=[]
out.append('# Validation Suite Report')
out.append('Generated: %s' % time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()))
out.append('')
out.append('| Journey | Exit code | Fingerprint | Log |')
out.append('|---|---:|---|---|')
for e in data:
    name=e.get('name')
    code=e.get('exitcode')
    fp=e.get('fingerprint')
    logpath=e.get('output')
    out.append('| %s | %s | %s | %s |' % (name, code, fp, logpath))

open('%s' % ('%s' % (os.path.join(os.path.dirname(f),'report.md'))),'w').write('\n'.join(out))
print('Wrote report.md')
PY

echo "Validation run complete. Artifacts in: $OUTDIR"
echo "Summary: $SUMMARY_JSON"

exit 0
