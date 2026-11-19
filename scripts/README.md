# Scripts Directory

This directory contains utility scripts for testing, verification, and automation.

## Available Scripts

### `verify_issues.sh`

Automated verification script for GitHub issues and local `issues.md` files.

**Purpose**: Verifies that all issues are created correctly with proper labels,
required content sections, and no duplicate titles.  The expected issue count
is configurable to support different backlog wave sizes (default: **120**).

**Prerequisites**:
1. Install GitHub CLI: https://cli.github.com/
   ```bash
   # macOS
   brew install gh
   
   # Linux
   sudo apt install gh
   ```

2. Authenticate with GitHub:
   ```bash
   gh auth login
   ```

**Usage**:

```bash
# Basic verification (expects 120 issues with label new_for_wave)
./scripts/verify_issues.sh

# Override expected count at runtime (e.g. a 40-item wave)
./scripts/verify_issues.sh --count 40

# Override via environment variable (useful in CI)
GLASSBOX_ISSUE_COUNT=40 ./scripts/verify_issues.sh

# Verify and export fetched issues to JSON
./scripts/verify_issues.sh --export

# Validate only a local issues.md without network access
./scripts/verify_issues.sh --local-only --file issues.md

# Combine: check GitHub AND validate a local file
./scripts/verify_issues.sh --file issues.md
```

**Options**:

| Flag | Description | Default |
|------|-------------|---------|
| `--count N` | Expected issue count | `120` (or `$GLASSBOX_ISSUE_COUNT`) |
| `--label LABEL` | GitHub label to filter on | `new_for_wave` |
| `--repo OWNER/REPO` | Target repository | `pugsley76/glassbox` |
| `--file PATH` | Local issues.md to parse | auto-detected if `issues.md` exists |
| `--export` | Write fetched issues to `issues_export.json` | off |
| `--local-only` | Skip GitHub API; only validate `--file` | off |

**What it checks**:
- Issue count matches expected value
- All issues have the `new_for_wave` label
- No duplicate issue titles
- Required sections present in every issue (no checkbox syntax required):
  - `Description`
  - `Work to be done`
  - `Implementation procedure`
  - `Acceptance criteria`

**Troubleshooting**:

- **Error: GitHub CLI not installed**
  ```
  Install from: https://cli.github.com/
  ```

- **Error: Not authenticated**
  ```bash
  gh auth login
  ```

- **Error: Issue count mismatch**
  - Check if all issues were created
  - Override with `--count N` or `GLASSBOX_ISSUE_COUNT=N` for a different wave size
  - Verify the label name is correct

### `index_regression_fixtures.sh`

Builds and validates a repository-wide regression fixture index at
`test/regression/fixture_index.json`.

**Purpose**: Provides a single authoritative catalogue of every fixture across
all seven regression layers (`rpc`, `trace`, `sourcemap`, `session`, `audit`,
`replay`, `cli`).  The index records each fixture's path, layer, failure class,
issue/PR reference, schema version, and test name.  CI fails on orphaned
entries, duplicates, or missing issue references.

**Usage**:

```bash
# Regenerate the index
./scripts/index_regression_fixtures.sh

# Validate an existing index without regenerating (CI mode)
./scripts/index_regression_fixtures.sh --check-only

# Use a non-default fixtures directory
./scripts/index_regression_fixtures.sh --fixtures-dir my/fixtures

# Write index to a custom location
./scripts/index_regression_fixtures.sh --output /tmp/index.json
```

**What it checks**:
- Every fixture has a recognised layer name
- Every fixture filename contains an issue or PR reference (e.g. `issue123`)
- No duplicate fixture IDs within the same layer
- All indexed files exist on disk

---



If you prefer manual verification using GitHub API:

```bash
# Check issue count
curl -H "Authorization: token $GITHUB_TOKEN" \
  "https://api.github.com/repos/dotandev/glassbox/issues?labels=new_for_wave&per_page=100" \
  | jq 'length'

# Get all issues with label
curl -H "Authorization: token $GITHUB_TOKEN" \
  "https://api.github.com/repos/dotandev/glassbox/issues?labels=new_for_wave&per_page=100" \
  | jq '.[] | {number, title, labels: [.labels[].name]}'

# Export to file
curl -H "Authorization: token $GITHUB_TOKEN" \
  "https://api.github.com/repos/dotandev/glassbox/issues?labels=new_for_wave&per_page=100" \
  > issues.json
```

## Using GitHub CLI Directly

```bash
# List all issues with label
gh issue list --repo dotandev/glassbox --label new_for_wave

# Count issues
gh issue list --repo dotandev/glassbox --label new_for_wave --json number --jq 'length'

# View specific issue
gh issue view 123 --repo dotandev/glassbox

# Export issues to JSON
gh issue list --repo dotandev/glassbox --label new_for_wave --limit 100 \
  --json number,title,labels,body > issues_export.json
```

## Quick Start

1. **Install GitHub CLI** (if not already installed):
   ```bash
   brew install gh  # macOS
   ```

2. **Authenticate**:
   ```bash
   gh auth login
   ```

3. **Run verification**:
   ```bash
   cd /path/to/glassbox
   ./scripts/verify_issues.sh
   ```

4. **Check results**:
   - Green checkmarks ([OK]) = passed
   - Red X ([FAIL]) = failed
   - Script exits with code 0 on success, 1 on failure

## CI/CD Integration

To use this script in CI/CD pipelines:

```yaml
# GitHub Actions example
- name: Verify Issues
  run: |
    ./scripts/verify_issues.sh
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

## Adding New Scripts

When adding new scripts to this directory:

1. Make them executable:
   ```bash
   chmod +x scripts/your_script.sh
   ```

2. Add a shebang line:
   ```bash
   #!/bin/bash
   ```

3. Add copyright header:
   ```bash
   # Copyright 2026 Glassbox Users
   # SPDX-License-Identifier: Apache-2.0
   ```

4. Update this README with usage instructions

5. Add error handling:
   ```bash
   set -e  # Exit on error
   ```

## Support

For issues with scripts:
1. Check prerequisites are installed
2. Verify authentication
3. Check script permissions (`chmod +x`)
4. Review error messages
5. Open an issue on GitHub if problems persist
