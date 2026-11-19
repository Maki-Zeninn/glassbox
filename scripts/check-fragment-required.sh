#!/usr/bin/env bash
# Copyright 2026 Glassbox Users
# SPDX-License-Identifier: Apache-2.0
#
# check-fragment-required.sh — determine whether a PR requires a changelog
# fragment based on changed file paths and an optional override mechanism.
#
# A fragment is REQUIRED when any changed file matches a user-visible surface:
#   - cmd/glassbox/**             (CLI commands and flags)
#   - internal/cmd/**             (CLI command implementations)
#   - internal/errors/**          (stable error codes)
#   - src/**                      (TypeScript/JS public API)
#   - docs/schema/**              (JSON schema definitions)
#   - .api-snapshots/**           (API snapshots — surface change)
#   - internal/apicompat/**       (API compat logic)
#   - integration/**              (integration test contracts)
#   - internal/audit/**           (audit protocol)
#   - internal/signer/**          (signing surface)
#   - internal/bindings/**        (bindings contract)
#   - internal/session/**         (session format)
#   - internal/manifest/**        (release manifest format)
#   - internal/sbom/**            (SBOM format)
#   - internal/depcompat/**       (dep compatibility)
#   - vscode-extension/src/**     (VS Code extension public API)
#
# A fragment is EXEMPT when ALL changed files fall under exempt prefixes:
#   - *_test.go files
#   - **/*_test.go
#   - testdata/**
#   - test/**
#   - tests/**
#   - docs/**  (pure doc changes — no schema/ subdirectory involvement)
#   - scripts/** (CI/tooling only)
#   - .github/** (CI config)
#   - .golangci.yml, .pre-commit-config.yaml, etc. (lint config)
#   - Makefile (build tooling)
#   - changelog/** (fragment itself)
#   - *.md (root-level markdown)
#   - go.sum (lockfile update — license scan handles this)
#
# Override mechanisms (in order of precedence, highest first):
#   1. File .changelog-override in the repo root with content "no-fragment"
#      — use for pure internal refactors or exceptional cases.
#   2. Environment variable CHANGELOG_SKIP=1
#      — set this in CI when the PR has the "no-fragment" GitHub label.
#   3. If the PR already adds a file to changelog/fragments/ among the
#      changed files, it is trivially satisfied.
#
# Usage:
#   scripts/check-fragment-required.sh [changed-files-file]
#
#   changed-files-file  — newline-separated list of changed files
#                         (default: read from git diff --name-only origin/HEAD)
#
# Exit codes:
#   0   Fragment not required (exempt change or override active or fragment present)
#   1   Fragment required but not found — PR must add a changelog fragment
#
# Environment:
#   CHANGELOG_SKIP=1        Override: skip fragment requirement (e.g. from label)
#   FRAG_DIR                Fragment directory (default: changelog/fragments)
#   BASE_BRANCH             Base branch for git diff (default: origin/HEAD)
#   DEBUG=1                 Print categorization of each changed file

set -euo pipefail

FRAG_DIR="${FRAG_DIR:-changelog/fragments}"
BASE_BRANCH="${BASE_BRANCH:-origin/HEAD}"
DEBUG="${DEBUG:-0}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${CYAN}[INFO]${NC}  $1"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $1"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $1" >&2; }
debug() { [[ "${DEBUG}" == "1" ]] && echo -e "  [DEBUG] $1" || true; }

# ── Override: CHANGELOG_SKIP env var ─────────────────────────────────────────
if [[ "${CHANGELOG_SKIP:-}" == "1" ]]; then
  ok "CHANGELOG_SKIP=1 — fragment requirement waived (label or manual override)"
  exit 0
fi

# ── Override: .changelog-override file ───────────────────────────────────────
if [[ -f ".changelog-override" ]]; then
  content="$(cat .changelog-override | tr -d '[:space:]')"
  if [[ "${content}" == "no-fragment" ]]; then
    ok ".changelog-override=no-fragment — fragment requirement waived"
    exit 0
  fi
fi

# ── Collect changed files ─────────────────────────────────────────────────────
if [[ $# -gt 0 && -f "$1" ]]; then
  mapfile -t CHANGED < "$1"
else
  # Derive from git diff against base branch.
  if ! git rev-parse --git-dir >/dev/null 2>&1; then
    warn "Not inside a git repository; cannot determine changed files."
    warn "Pass a file containing changed paths as the first argument."
    exit 0
  fi
  mapfile -t CHANGED < <(git diff --name-only "${BASE_BRANCH}" 2>/dev/null || git diff --name-only HEAD~1 2>/dev/null || true)
fi

if [[ ${#CHANGED[@]} -eq 0 ]]; then
  ok "No changed files detected — nothing to check."
  exit 0
fi

# ── Check: fragment already added in this PR ─────────────────────────────────
for f in "${CHANGED[@]}"; do
  if [[ "${f}" == changelog/fragments/*.toml && "${f}" != *"example-cli-flag.toml"* ]]; then
    debug "Fragment file found in changed set: $f"
    ok "Changelog fragment added: ${f}"
    exit 0
  fi
done

# ── Path classification patterns ──────────────────────────────────────────────

# Returns 0 if the path matches a user-visible surface
is_user_visible() {
  local p="$1"
  local patterns=(
    "cmd/glassbox/"
    "internal/cmd/"
    "internal/errors/"
    "src/"
    "docs/schema/"
    ".api-snapshots/"
    "internal/apicompat/"
    "integration/"
    "internal/audit/"
    "internal/signer/"
    "internal/bindings/"
    "internal/session/"
    "internal/manifest/"
    "internal/sbom/"
    "internal/depcompat/"
    "vscode-extension/src/"
  )
  for pattern in "${patterns[@]}"; do
    if [[ "${p}" == ${pattern}* ]]; then
      return 0
    fi
  done
  return 1
}

# Returns 0 if the path is exempt (internal/test/tooling only)
is_exempt() {
  local p="$1"

  # Test files in any package
  if [[ "${p}" == *"_test.go" ]]; then return 0; fi
  if [[ "${p}" == *"_fuzz.go" ]]; then return 0; fi

  # Test data directories
  if [[ "${p}" == testdata/* || "${p}" == */testdata/* ]]; then return 0; fi
  if [[ "${p}" == test/* || "${p}" == tests/* ]]; then return 0; fi

  # CI and tooling
  if [[ "${p}" == .github/* ]]; then return 0; fi
  if [[ "${p}" == scripts/* ]]; then return 0; fi
  if [[ "${p}" == changelog/* ]]; then return 0; fi

  # Build/lint config
  case "${p}" in
    Makefile|go.sum|go.mod|.golangci.yml|.pre-commit-config.yaml|rust-toolchain.toml|.cargo/*|Dockerfile|docker-compose*.yml)
      return 0 ;;
  esac

  # Root-level markdown and config files
  if [[ "${p}" == *.md && "${p}" != */* ]]; then return 0; fi
  if [[ "${p}" == *.toml && "${p}" != */* && "${p}" != *.lock ]]; then return 0; fi

  # Pure documentation (but NOT docs/schema/ — schema changes are user-visible)
  if [[ "${p}" == docs/* && "${p}" != docs/schema/* ]]; then return 0; fi

  # License / contrib / CI housekeeping
  case "${p}" in
    LICENSE|.all-contributorsrc|.dockerignore|.gitattributes|.gitignore|.golangci.yml)
      return 0 ;;
  esac

  # Internal packages not on any user-visible surface
  local internal_only=(
    "internal/analytics/"
    "internal/analyzer/"
    "internal/authtrace/"
    "internal/bridge/"
    "internal/bundle/"
    "internal/cache/"
    "internal/clioutput/"
    "internal/compare/"
    "internal/config/"
    "internal/crashreport/"
    "internal/daemon/"
    "internal/db/"
    "internal/dce/"
    "internal/decenstorage/"
    "internal/decoder/"
    "internal/deeplink/"
    "internal/demangle/"
    "internal/deterministic/"
    "internal/diagnostics/"
    "internal/dwarf/"
    "internal/e2eharness/"
    "internal/endpoints/"
    "internal/eventbus/"
    "internal/fuzz/"
    "internal/gasmodel/"
    "internal/health/"
    "internal/heuristic/"
    "internal/httpclient/"
    "internal/ipc/"
    "internal/localization/"
    "internal/logger/"
    "internal/lsp/"
    "internal/lto/"
    "internal/metrics/"
    "internal/obsvalidate/"
    "internal/offline/"
    "internal/pathutil/"
    "internal/perfmetrics/"
    "internal/plan/"
    "internal/plugin/"
    "internal/profile/"
    "internal/progress/"
    "internal/protocolreg/"
    "internal/redaction/"
    "internal/replay/"
    "internal/report/"
    "internal/rpc/"
    "internal/security/"
    "internal/secutil/"
    "internal/shell/"
    "internal/shutdown/"
    "internal/simulator/"
    "internal/snapshot/"
    "internal/sourcemap/"
    "internal/telemetry/"
    "internal/termctx/"
    "internal/terminal/"
    "internal/testgen/"
    "internal/testhelpers/"
    "internal/tokenflow/"
    "internal/trace/"
    "internal/types/"
    "internal/ui/"
    "internal/updater/"
    "internal/version/"
    "internal/visualizer/"
    "internal/wasmopt/"
    "internal/wasmvalidate/"
    "internal/wat/"
    "internal/watch/"
    "internal/webhook/"
    "internal/wizard/"
  )
  for ip in "${internal_only[@]}"; do
    if [[ "${p}" == ${ip}* ]]; then return 0; fi
  done

  return 1
}

# ── Classify every changed file ───────────────────────────────────────────────
user_visible_count=0
exempt_count=0
unclassified=()

for f in "${CHANGED[@]}"; do
  [[ -z "${f}" ]] && continue
  if is_user_visible "${f}"; then
    debug "user-visible: ${f}"
    user_visible_count=$((user_visible_count + 1))
  elif is_exempt "${f}"; then
    debug "exempt: ${f}"
    exempt_count=$((exempt_count + 1))
  else
    debug "unclassified (treated as user-visible): ${f}"
    unclassified+=("${f}")
    user_visible_count=$((user_visible_count + 1))
  fi
done

info "Changed files: ${#CHANGED[@]} total, ${user_visible_count} user-visible, ${exempt_count} exempt"

# ── Decision ──────────────────────────────────────────────────────────────────
if [[ ${user_visible_count} -eq 0 ]]; then
  ok "All changes are internal/test/tooling — no changelog fragment required."
  exit 0
fi

# Fragment is required but not present.
echo ""
fail "This PR touches user-visible surfaces but no changelog fragment was found."
echo ""
echo "  User-visible changes detected in:"
for f in "${CHANGED[@]}"; do
  [[ -z "${f}" ]] && continue
  if is_user_visible "${f}" || [[ " ${unclassified[*]} " == *" ${f} "* ]]; then
    echo "    ${f}"
  fi
done
echo ""
echo "  To fix:"
echo "    1. Create changelog/fragments/<pr-number>-<slug>.toml"
echo "       See docs/changelog-fragments.md for the format."
echo "    2. Run:  make changelog-check"
echo ""
echo "  To waive for pure internal changes (reviewers must approve):"
echo "    Option A: Add label 'no-fragment' to this PR (sets CHANGELOG_SKIP=1 in CI)"
echo "    Option B: Add .changelog-override containing only the text: no-fragment"
echo ""
exit 1
