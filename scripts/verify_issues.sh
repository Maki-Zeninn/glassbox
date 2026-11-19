#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# verify_issues.sh — Verify GitHub issues and/or a local issues.md file.
#
# Usage:
#   ./scripts/verify_issues.sh [OPTIONS]
#
# Options:
#   --count N          Expected issue count (default: 120; override via
#                      GLASSBOX_ISSUE_COUNT env var)
#   --label LABEL      GitHub label to filter on (default: new_for_wave)
#   --repo OWNER/REPO  GitHub repository (default: pugsley76/glassbox)
#   --file PATH        Local issues.md to parse instead of (or alongside)
#                      the GitHub API (default: issues.md if it exists)
#   --export           Export issues to JSON after verification
#   --local-only       Skip GitHub API; only validate --file
#   --help             Show this message
#
# Environment variables (all optional):
#   GLASSBOX_ISSUE_COUNT   Override expected count without passing --count
#   GITHUB_TOKEN           Token used by gh CLI for authentication

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
EXPECTED_COUNT="${GLASSBOX_ISSUE_COUNT:-120}"
LABEL="new_for_wave"
REPO="pugsley76/glassbox"
ISSUES_FILE=""
EXPORT=false
LOCAL_ONLY=false

# Required sections in every issue body (no checkbox syntax required).
REQUIRED_SECTIONS=(
  "Description"
  "Work to be done"
  "Implementation procedure"
  "Acceptance criteria"
)

# ── Colour helpers ─────────────────────────────────────────────────────────────
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Colour

ok()   { echo -e "${GREEN}[OK]${NC}   $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --count)      EXPECTED_COUNT="$2"; shift 2 ;;
    --label)      LABEL="$2";          shift 2 ;;
    --repo)       REPO="$2";           shift 2 ;;
    --file)       ISSUES_FILE="$2";    shift 2 ;;
    --export)     EXPORT=true;         shift   ;;
    --local-only) LOCAL_ONLY=true;     shift   ;;
    --help|-h)
      sed -n '/^# verify_issues/,/^[^#]/{ s/^# \{0,1\}//; p }' "$0" | head -30
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Auto-detect issues.md if it exists in the repo root and no --file was given.
if [[ -z "$ISSUES_FILE" && -f "issues.md" ]]; then
  ISSUES_FILE="issues.md"
fi

FAIL_COUNT=0

# ── Banner ─────────────────────────────────────────────────────────────────────
echo "========================================="
echo " Glassbox Issue Verification Script"
echo "========================================="
echo " Expected count : ${EXPECTED_COUNT}"
echo " Label          : ${LABEL}"
if [[ "$LOCAL_ONLY" == "false" ]]; then
  echo " Repository     : ${REPO}"
fi
if [[ -n "$ISSUES_FILE" ]]; then
  echo " Local file     : ${ISSUES_FILE}"
fi
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# SECTION A — GitHub API verification (skipped with --local-only)
# ══════════════════════════════════════════════════════════════════════════════
if [[ "$LOCAL_ONLY" == "false" ]]; then

  # ── Prerequisite: gh CLI ────────────────────────────────────────────────────
  if ! command -v gh &>/dev/null; then
    fail "GitHub CLI (gh) not found. Install from https://cli.github.com/"
    echo "      Alternatively, run with --local-only to skip GitHub checks."
    exit 1
  fi
  ok "GitHub CLI found"

  # ── Prerequisite: authenticated ─────────────────────────────────────────────
  if ! gh auth status &>/dev/null; then
    fail "Not authenticated. Run: gh auth login"
    exit 1
  fi
  ok "Authenticated with GitHub"

  echo ""
  echo "Fetching issues with label '${LABEL}' from ${REPO}..."

  # Fetch up to 500 issues (enough for the 120-item backlog with headroom).
  ISSUES_JSON=$(gh issue list \
    --repo "$REPO" \
    --label "$LABEL" \
    --limit 500 \
    --json number,title,labels,body \
    2>&1) || {
      fail "Failed to fetch issues: $ISSUES_JSON"
      exit 1
  }

  ACTUAL_COUNT=$(echo "$ISSUES_JSON" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null \
    || echo "$ISSUES_JSON" | grep -c '"number"' || echo 0)

  echo "Found    : ${ACTUAL_COUNT} issues"
  echo "Expected : ${EXPECTED_COUNT} issues"

  if [[ "$ACTUAL_COUNT" -eq "$EXPECTED_COUNT" ]]; then
    ok "Issue count matches expected (${EXPECTED_COUNT})"
  else
    fail "Issue count mismatch — found ${ACTUAL_COUNT}, expected ${EXPECTED_COUNT}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi

  # ── Label check ─────────────────────────────────────────────────────────────
  echo ""
  echo "Verifying labels..."
  UNLABELLED=$(echo "$ISSUES_JSON" | python3 - <<'PYEOF' 2>/dev/null
import sys, json
data = json.load(sys.stdin)
label = "${LABEL}"
missing = [str(i["number"]) for i in data
           if not any(l["name"] == label for l in i.get("labels", []))]
if missing:
    print("Issues missing label: " + ", ".join(missing))
PYEOF
  )

  if [[ -z "$UNLABELLED" ]]; then
    ok "All issues have the '${LABEL}' label"
  else
    fail "$UNLABELLED"
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi

  # ── Duplicate title check ────────────────────────────────────────────────────
  echo ""
  echo "Checking for duplicate titles..."
  DUPES=$(echo "$ISSUES_JSON" | python3 - <<'PYEOF' 2>/dev/null
import sys, json
from collections import Counter
data = json.load(sys.stdin)
counts = Counter(i["title"].strip() for i in data)
dupes = [t for t, c in counts.items() if c > 1]
for d in dupes:
    print("  Duplicate title: " + d)
PYEOF
  )

  if [[ -z "$DUPES" ]]; then
    ok "No duplicate titles found"
  else
    fail "Duplicate titles detected:"
    echo "$DUPES"
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi

  # ── Section check (spot-check first 10 issues) ───────────────────────────────
  echo ""
  echo "Checking required sections in first 10 issues..."
  SECTION_ISSUES=$(echo "$ISSUES_JSON" | python3 - <<'PYEOF' 2>/dev/null
import sys, json, re
data = json.load(sys.stdin)
required = ["Description", "Work to be done", "Implementation procedure", "Acceptance criteria"]
problems = []
for issue in data[:10]:
    body = issue.get("body") or ""
    missing = [s for s in required if not re.search(r'(?i)##?\s*' + re.escape(s), body)]
    if missing:
        problems.append(f"  Issue #{issue['number']}: missing sections: {', '.join(missing)}")
for p in problems:
    print(p)
PYEOF
  )

  if [[ -z "$SECTION_ISSUES" ]]; then
    ok "All spot-checked issues contain required sections"
  else
    fail "Section check failed:"
    echo "$SECTION_ISSUES"
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi

  # ── Export ───────────────────────────────────────────────────────────────────
  if [[ "$EXPORT" == "true" ]]; then
    OUT="issues_export.json"
    echo "$ISSUES_JSON" > "$OUT"
    ok "Issues exported to ${OUT}"
  fi

fi   # end LOCAL_ONLY check

# ══════════════════════════════════════════════════════════════════════════════
# SECTION B — Local issues.md parser
# ══════════════════════════════════════════════════════════════════════════════
if [[ -n "$ISSUES_FILE" ]]; then

  echo ""
  echo "========================================="
  echo " Parsing local file: ${ISSUES_FILE}"
  echo "========================================="

  if [[ ! -f "$ISSUES_FILE" ]]; then
    fail "File not found: ${ISSUES_FILE}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
  else
    python3 - "$ISSUES_FILE" "$EXPECTED_COUNT" <<'PYEOF'
import sys, re

issues_file  = sys.argv[1]
expected_cnt = int(sys.argv[2])

required_sections = [
    "Description",
    "Work to be done",
    "Implementation procedure",
    "Acceptance criteria",
]

# A heading-2 line like "## Title" starts a new issue.
# We treat everything between two H2 headings as one issue body.
heading2_re = re.compile(r'^##\s+(.+)$', re.MULTILINE)
section_re  = re.compile(r'(?i)^##\s+(.+)$', re.MULTILINE)

with open(issues_file, encoding="utf-8") as fh:
    content = fh.read()

# Split on H2 boundaries to get individual issue blocks.
parts   = heading2_re.split(content)
# parts = [pre-text, title1, body1, title2, body2, ...]
issues  = []
for i in range(1, len(parts) - 1, 2):
    issues.append({"title": parts[i].strip(), "body": parts[i + 1] if i + 1 < len(parts) else ""})

fail_count = 0

# --- Count ---
print(f"Found    : {len(issues)} issues in {issues_file}")
print(f"Expected : {expected_cnt} issues")
if len(issues) == expected_cnt:
    print(f"\033[0;32m[OK]\033[0m   Issue count matches ({expected_cnt})")
else:
    print(f"\033[0;31m[FAIL]\033[0m Issue count mismatch — found {len(issues)}, expected {expected_cnt}")
    fail_count += 1

# --- Duplicate titles ---
from collections import Counter
title_counts = Counter(i["title"] for i in issues)
dupes = [t for t, c in title_counts.items() if c > 1]
if dupes:
    print(f"\033[0;31m[FAIL]\033[0m Duplicate titles:")
    for d in dupes:
        print(f"       {d!r}")
    fail_count += 1
else:
    print(f"\033[0;32m[OK]\033[0m   No duplicate titles")

# --- Required sections ---
section_failures = []
for idx, issue in enumerate(issues, start=1):
    body = issue["body"]
    missing = []
    for sec in required_sections:
        if not re.search(r'(?i)(?:^|\n)#+ *' + re.escape(sec), body):
            missing.append(sec)
    if missing:
        section_failures.append((idx, issue["title"], missing))

if section_failures:
    print(f"\033[0;31m[FAIL]\033[0m Missing sections in {len(section_failures)} issue(s):")
    for num, title, missing in section_failures:
        print(f"       Issue {num} ({title!r}): {', '.join(missing)}")
    fail_count += 1
else:
    print(f"\033[0;32m[OK]\033[0m   All issues contain required sections")

sys.exit(fail_count)
PYEOF
    LOCAL_EXIT=$?
    FAIL_COUNT=$((FAIL_COUNT + LOCAL_EXIT))
  fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# Summary
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "========================================="
echo " Verification Summary"
echo "========================================="
if [[ "$FAIL_COUNT" -eq 0 ]]; then
  ok "All verifications passed!"
  exit 0
else
  fail "${FAIL_COUNT} check(s) failed."
  exit 1
fi
