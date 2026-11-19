# Release Provenance and Attestation

Every Glassbox release artifact maps to an exact source revision, Go toolchain
version, and CI workflow run. This page explains what provenance data is
recorded, how it is signed and embedded in the release manifest, and how to
verify it offline without access to the build environment.

---

## What provenance is recorded

Every `manifest.json` produced by the release pipeline contains a
`build_provenance` block alongside the artifact list and Ed25519 signature:

```json
{
  "schema_version": "1",
  "version": "v1.2.3",
  "commit": "<40-char sha>",
  "build_date": "2026-01-01T00:00:00Z",
  "build_provenance": {
    "source_repository": "https://github.com/dotandev/glassbox",
    "source_ref":        "refs/tags/v1.2.3",
    "commit_sha":        "<40-char sha>",
    "go_version":        "go1.26.0 linux/amd64",
    "build_runner_os":   "ubuntu-24.04",
    "build_workflow":    ".github/workflows/release.yml",
    "build_run_id":      "12345678901",
    "source_date_epoch": 1700000000
  },
  "artifacts": [ ... ],
  "manifest_hash": "<sha256 of canonical json>",
  "signature":     "<ed25519 hex>",
  "public_key":    "<ed25519 public key hex>"
}
```

| Field | What it tells you |
|---|---|
| `source_repository` | The exact GitHub repository the code was fetched from |
| `source_ref` | The Git ref — confirms a tag release vs a branch build |
| `commit_sha` | The commit that was compiled — cross-check with `git log` |
| `go_version` | The Go toolchain used — lets you reproduce the build |
| `build_runner_os` | The OS the CI runner used |
| `build_workflow` | The workflow file path in the repository |
| `build_run_id` | The GitHub Actions run ID — use it to view build logs |
| `source_date_epoch` | Unix timestamp used for reproducible archives |

All provenance fields are included in `manifest_hash` and covered by the
Ed25519 signature, so any tampering with the provenance block invalidates the
signature.

---

## Offline verification

Verification requires only **bash**, **python3**, and optionally the
[`cryptography`](https://pypi.org/project/cryptography/) Python package for the
Ed25519 signature check. No Go toolchain or network access is needed.

### Step 1 — Download the release artifacts

```bash
VERSION=v1.2.3
REPO=dotandev/glassbox

curl -LO "https://github.com/${REPO}/releases/download/${VERSION}/manifest.json"
curl -LO "https://github.com/${REPO}/releases/download/${VERSION}/glassbox-linux-amd64.tar.gz"
curl -LO "https://github.com/${REPO}/releases/download/${VERSION}/checksums.sha256"
```

### Step 2 — Verify the manifest

```bash
# Download the verification script from the same tag.
curl -LO "https://raw.githubusercontent.com/${REPO}/refs/tags/${VERSION}/scripts/verify-manifest.sh"

# Install the Ed25519 verification library (once, optional but recommended).
pip install cryptography

# Run the verifier.
bash verify-manifest.sh manifest.json .
```

Expected output:

```
1. Validating manifest structure ...
  [PASS] field present: manifest_hash = abcdef0123456789...
  [PASS] field present: signature = ...
  [PASS] field present: public_key = ...
  [PASS] schema_version=1
2. Checking artifact presence ...
  [PASS] glassbox-linux-amd64.tar.gz present
  ...
3. Verifying artifact SHA-256 hashes ...
  [PASS] glassbox-linux-amd64.tar.gz
  ...
4. Checking for unlisted artifacts ...
  [PASS] no unlisted artifact files found
5. Verifying Ed25519 signature ...
  [PASS] manifest_hash matches re-derived hash
  [PASS] Ed25519 signature valid
Result: all manifest verification checks passed.
```

### Step 3 — Verify provenance fields

```bash
python3 - manifest.json <<'EOF'
import json, sys
m = json.load(open(sys.argv[1]))
bp = m.get("build_provenance", {})
print("Source repository :", bp.get("source_repository"))
print("Source ref        :", bp.get("source_ref"))
print("Commit SHA        :", bp.get("commit_sha"))
print("Go version        :", bp.get("go_version"))
print("Build workflow    :", bp.get("build_workflow"))
print("Build run ID      :", bp.get("build_run_id"))
EOF
```

Cross-check the `commit_sha` against the published release tag:

```bash
git ls-remote https://github.com/dotandev/glassbox refs/tags/v1.2.3
# Output should contain the same commit SHA as in build_provenance.commit_sha
```

### Step 4 — Verify the SBOM

```bash
SBOM="glassbox-${VERSION}.spdx.json"
curl -LO "https://github.com/${REPO}/releases/download/${VERSION}/${SBOM}"
curl -LO "https://raw.githubusercontent.com/${REPO}/refs/tags/${VERSION}/scripts/verify-sbom.sh"

bash verify-sbom.sh "${SBOM}" --manifest manifest.json
```

---

## Reproducibility check

The release binary is byte-stable across two independent builds from the same
source and toolchain. To reproduce a binary locally:

```bash
# 1. Check out the exact release commit.
git checkout v1.2.3

# 2. Set the same SOURCE_DATE_EPOCH as recorded in build_provenance.
export SOURCE_DATE_EPOCH=<value from manifest build_provenance.source_date_epoch>

# 3. Build with the same Go version.
# Install go1.26.0 via https://go.dev/dl/ if needed.
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath \
    -ldflags "-s -w \
      -X 'github.com/dotandev/glassbox/internal/version.Version=v1.2.3' \
      -X 'github.com/dotandev/glassbox/internal/version.CommitSHA=<commit_sha>' \
      -X 'github.com/dotandev/glassbox/internal/version.BuildDate=<build_date>'" \
    -o glassbox-linux-amd64 \
    ./cmd/glassbox

# 4. Compare SHA-256.
sha256sum glassbox-linux-amd64
# Should match the hash in checksums.sha256 or manifest.json artifacts list.
```

Or use the automated script:

```bash
make reproducibility-check
```

---

## Public key

The Ed25519 public key is embedded in every `manifest.json` as the `public_key`
field (hex-encoded). This means you do **not** need a separate key file to
verify the signature — the manifest is self-contained.

The public key is also committed to the repository at
`docs/release-signing-pubkey.txt` so it can be independently retrieved from the
source tree.

To extract the public key from a manifest:

```bash
python3 -c "import json; print(json.load(open('manifest.json'))['public_key'])"
```

---

## Viewing the build run

Given the `build_run_id` from the manifest, you can navigate directly to the
GitHub Actions run that produced the release:

```
https://github.com/dotandev/glassbox/actions/runs/<build_run_id>
```

This gives you access to the full build logs, uploaded artifacts, and the
exact environment that was used.

---

## Fixture tests

The manifest package includes test fixtures for missing and tampered provenance:

```bash
go test ./internal/manifest/... -v -run TestProvenance
go test ./cmd/generate-release-manifest/... -v -run TestBuildProvenance
```

---

## See also

- [release-manifest.md](./release-manifest.md) — full manifest format reference
- [sbom.md](./sbom.md) — SBOM generation and verification
- [ci-artifacts.md](./ci-artifacts.md) — CI artifact retrieval guide
- [reproducible-builds.md](./reproducible-builds.md) — determinism checks
