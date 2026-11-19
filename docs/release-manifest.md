# Signed Release Manifest

Every Glassbox release ships a `manifest.json` alongside the binary archives.
It lists every artifact exactly once with its SHA-256 hash, size, platform, and
kind, and is signed with an Ed25519 private key held only in CI. This lets
anyone verify a release without trusting the build environment.

## Manifest format

```json
{
  "schema_version": "1",
  "version":        "v1.2.3",
  "commit":         "e3b0c44298fc1c149afb4c8996fb92427ae41e4d",
  "build_date":     "2026-01-01T00:00:00Z",
  "sbom_ref":       "glassbox-v1.2.3.spdx.json",
  "artifacts": [
    {
      "name":     "glassbox-linux-amd64.tar.gz",
      "platform": "linux/amd64",
      "sha256":   "<64-hex-chars>",
      "size":     12345678,
      "kind":     "archive"
    },
    {
      "name":     "glassbox-darwin-arm64.tar.gz",
      "platform": "darwin/arm64",
      "sha256":   "<64-hex-chars>",
      "size":     11000000,
      "kind":     "archive"
    },
    {
      "name":  "checksums.sha256",
      "sha256": "<64-hex-chars>",
      "size":  512,
      "kind":  "checksums"
    },
    {
      "name":  "version.txt",
      "sha256": "<64-hex-chars>",
      "size":  64,
      "kind":  "metadata"
    }
  ],
  "provenance": {
    "signer_identity": "ci-pipeline",
    "key_id":          "<fingerprint>",
    "algorithm":       "ed25519"
  },
  "manifest_hash": "<64-hex-chars>",
  "signature":     "<128-hex-chars>",
  "public_key":    "<64-hex-chars>"
}
```

### Field reference

| Field | Description |
|-------|-------------|
| `schema_version` | Manifest format version. Currently `"1"`. |
| `version` | Semver release tag (e.g. `v1.2.3`). |
| `commit` | Full git commit SHA the release was built from. |
| `build_date` | RFC3339 UTC timestamp of the build. |
| `sbom_ref` | Filename of the SBOM artifact if one was generated, otherwise absent. |
| `artifacts[].name` | Bare filename (no directory). Every release file appears exactly once. |
| `artifacts[].platform` | Target OS/arch in `os/arch` form. Empty for platform-independent files. |
| `artifacts[].sha256` | Lower-case hex SHA-256 of the file. |
| `artifacts[].size` | File size in bytes. |
| `artifacts[].kind` | One of `archive`, `checksums`, `metadata`, `binary`. |
| `provenance.signer_identity` | Human-readable label for the signing entity. |
| `provenance.key_id` | Opaque key identifier (fingerprint, ARN, label). |
| `provenance.algorithm` | Signing algorithm — always `"ed25519"`. |
| `manifest_hash` | Hex SHA-256 of the canonical JSON of the manifest body (all fields above). This is the exact bytes that were signed. |
| `signature` | Hex Ed25519 signature over the raw bytes of `manifest_hash`. |
| `public_key` | Hex Ed25519 public key that produced `signature`. |

## How signing works

1. The CI `sign-manifest` job builds `cmd/generate-release-manifest` from
   source.
2. It scans `dist/release/`, hashes every file, and serialises the
   `ReleaseManifest` struct to canonical JSON (Go struct field order, no HTML
   escaping, sorted artifact list).
3. SHA-256 of that canonical JSON becomes `manifest_hash`.
4. The PKCS#8 PEM Ed25519 private key stored as the `MANIFEST_SIGNING_KEY`
   repository secret signs the raw `manifest_hash` bytes with `ed25519.Sign`.
5. The resulting `manifest.json` is verified immediately by
   `scripts/verify-manifest.sh` before being uploaded as a release artifact.

The private key is **never written to disk** by any script and is **never
embedded** in the manifest or repository.

## Offline verification

No Go installation, no `glassbox` binary, and no CI access are required.
You only need **bash**, **python3**, and optionally the
[`cryptography`](https://pypi.org/project/cryptography/) Python package for
the Ed25519 signature step.

### Quick start

```bash
# 1. Download the release artifacts for your platform, plus the manifest
VERSION=v1.2.3
BASE="https://github.com/dotandev/glassbox/releases/download/${VERSION}"

curl -LO "${BASE}/glassbox-linux-amd64.tar.gz"
curl -LO "${BASE}/checksums.sha256"
curl -LO "${BASE}/version.txt"
curl -LO "${BASE}/manifest.json"

# 2. Download the verification script (no build tools needed)
curl -LO "https://raw.githubusercontent.com/dotandev/glassbox/main/scripts/verify-manifest.sh"

# 3. Install the Ed25519 library (once)
pip install cryptography

# 4. Run offline verification
bash verify-manifest.sh manifest.json .
```

Expected output when everything is clean:

```
Verifying release manifest: manifest.json
Artifact directory:         .

1. Validating manifest structure ...
  [PASS] field present: manifest_hash = 4f3e2a1b...
  [PASS] field present: signature = 9c8d7e6f...
  [PASS] field present: public_key = 1a2b3c4d...
  [PASS] field present: version = v1.2.3
  [PASS] schema_version=1

2. Checking artifact presence ...
  [PASS] glassbox-linux-amd64.tar.gz present
  [PASS] checksums.sha256 present
  [PASS] version.txt present
  [PASS] 3 artifact(s) listed in manifest

3. Verifying artifact SHA-256 hashes ...
  [PASS] glassbox-linux-amd64.tar.gz
  [PASS] checksums.sha256
  [PASS] version.txt

4. Checking for unlisted artifacts ...
  [PASS] no unlisted artifact files found

5. Verifying Ed25519 signature ...
  [PASS] manifest_hash matches re-derived hash
  [PASS] Ed25519 signature valid

Result: all manifest verification checks passed.
```

### What each check catches

| Check | What it detects |
|-------|----------------|
| Structure validation | Missing or malformed JSON fields in `manifest.json` |
| Artifact presence | A listed file was not downloaded or is empty |
| SHA-256 per file | File was silently corrupted or substituted after signing |
| No unlisted files | Files present locally but missing from the manifest (incomplete manifest) |
| Ed25519 signature | `manifest.json` itself was altered after signing; wrong public key |

### Manual Ed25519 verification (without python3 cryptography)

If you cannot install the `cryptography` package, verify the signature with
`openssl` (version 3.0+):

```bash
# 1. Extract the public key bytes from the manifest
python3 -c "
import json, binascii, sys
d = json.load(open('manifest.json'))
print(binascii.unhexlify(d['public_key']).hex())
" > pub_hex.txt

# 2. Write the DER-encoded SubjectPublicKeyInfo for the Ed25519 key
python3 -c "
import json, binascii
d = json.load(open('manifest.json'))
pub = binascii.unhexlify(d['public_key'])
# Ed25519 SPKI prefix: 30 2a 30 05 06 03 2b 65 70 03 21 00
prefix = bytes.fromhex('302a300506032b6570032100')
open('pub.der', 'wb').write(prefix + pub)
"
openssl pkey -inform DER -pubin -in pub.der -out pub.pem

# 3. Write the signature bytes
python3 -c "
import json, binascii
d = json.load(open('manifest.json'))
open('sig.bin', 'wb').write(binascii.unhexlify(d['signature']))
"

# 4. Write the message (raw hash bytes, not hex)
python3 -c "
import json, binascii
d = json.load(open('manifest.json'))
open('hash.bin', 'wb').write(binascii.unhexlify(d['manifest_hash']))
"

# 5. Verify with openssl
openssl pkeyutl -verify -pubin \
  -inkey pub.pem \
  -sigfile sig.bin \
  -in hash.bin \
  -pkeyopt digest:none
# Output: Signature Verified Successfully
```

## Key management

### Generating the signing key

```bash
# Generate an Ed25519 key pair (requires openssl 3.0+)
openssl genpkey -algorithm ed25519 -out release-key.pem

# Extract the public key for distribution / verification
openssl pkey -in release-key.pem -pubout -out release-key-pub.pem

# View public key fingerprint
openssl pkey -in release-key-pub.pem -pubin -text -noout
```

### Storing the key securely

- Store `release-key.pem` as a **GitHub Actions repository secret** named
  `MANIFEST_SIGNING_KEY` (Settings → Secrets and variables → Actions → New
  repository secret).
- **Never commit** `release-key.pem` to the repository.
- Rotate the key by generating a new pair, updating the secret, and noting the
  rotation in release notes so downstream verifiers can update their trusted key.
- The **public key** (`public_key` field in every `manifest.json`) can be
  committed to the repository or published on the project website so verifiers
  always have a known-good reference.

### Pinning a trusted public key

To automatically detect key rotation or compromise, pin the expected public key
in your verification workflow:

```bash
EXPECTED_PUB="1a2b3c4d..."   # paste the hex public key from a trusted release

ACTUAL_PUB=$(python3 -c "import json; d=json.load(open('manifest.json')); print(d['public_key'])")

if [ "${ACTUAL_PUB}" != "${EXPECTED_PUB}" ]; then
  echo "ERROR: public key mismatch — possible key rotation or tampering"
  exit 1
fi
echo "Public key matches pinned value"
```

## Failure modes

| Failure | Cause | Resolution |
|---------|-------|------------|
| `manifest_hash mismatch` | `manifest.json` was edited after signing | Re-download `manifest.json` from the official release page |
| `Ed25519 signature INVALID` | Manifest or key tampered | Obtain manifest from a trusted source; verify public key fingerprint |
| `<file> missing or empty` | Artifact not downloaded | Download the missing file from the release page |
| `SHA-256 mismatch for <file>` | File corrupted in transit or substituted | Re-download the file and re-verify |
| `artifact not listed in manifest` | Local file not covered by manifest | Cross-check the release page; do not use uncovered files |
| `MANIFEST_SIGNING_KEY is not set` (CI) | Secret not configured | Add the key to Settings → Secrets → Actions |

## Integration with CI

The `GLASSBOX_MANIFEST_SIGNING_KEY` environment variable (or `--signing-key`
flag) is the only credential needed. It is injected from the
`MANIFEST_SIGNING_KEY` repository secret and is never printed in logs.

To add manifest signing to a custom pipeline:

```bash
# Build and package first
make package

# Sign (key comes from environment)
export GLASSBOX_MANIFEST_SIGNING_KEY="$(cat /path/to/release-key.pem)"
bash scripts/sign-manifest.sh dist/release

# Verify offline
bash scripts/verify-manifest.sh dist/release/manifest.json dist/release
```
