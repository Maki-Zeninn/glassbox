# Reproducible Builds

Glassbox release artifacts are built to be reproducible: two independent builds
from the same source commit and the same toolchain produce byte-identical
binaries and archives. This lets anyone verify a downloaded artifact matches
what was published without trusting the CI environment.

## What "reproducible" means here

| Artifact | Status | Notes |
|----------|--------|-------|
| `glassbox-linux-amd64` | Reproducible | `-trimpath`, frozen `ldflags`, no `CGO` |
| `glassbox-linux-arm64` | Reproducible | Same |
| `glassbox-darwin-amd64` | Reproducible | Same |
| `glassbox-darwin-arm64` | Reproducible | Same |
| `glassbox-windows-amd64.exe` | Reproducible | Same |
| `*.tar.gz` | Reproducible | `--sort=name`, `--mtime=@EPOCH`, `--owner=0` |
| `*.zip` | Reproducible | `zip -X` strips metadata |
| `checksums.sha256` | Reproducible | `LC_ALL=C sort` for stable ordering |
| Rust simulator binary | Partial | Depends on `rust-toolchain.toml` pin |
| TypeScript `dist/` | Best-effort | `npm ci` + pinned `package-lock.json` |

## Toolchain pins

All toolchain versions are recorded in [`toolchain.json`](../toolchain.json)
at the repository root and enforced in CI.

| Ecosystem | Pin location | Current version |
|-----------|-------------|-----------------|
| Go | `go.mod` (`go` directive) + `setup-go` `go-version-file` | 1.24.x |
| Rust | [`rust-toolchain.toml`](../rust-toolchain.toml) | 1.86.0 |
| Node | `setup-node` `node-version` in CI + `toolchain.json` | 20.19.0 LTS |
| npm packages | `package-lock.json` (committed) | — |
| Go modules | `go.sum` (committed) | — |
| Cargo crates | `Cargo.lock` (committed) | — |

### Updating a toolchain pin

1. Change the version in the relevant file (`rust-toolchain.toml`, `toolchain.json`, CI workflow).
2. Run a clean build locally and verify outputs.
3. Update `go.sum` / `Cargo.lock` / `package-lock.json` as needed.
4. Commit all changes together with a note in the PR description.
5. Re-run the reproducibility check: `make reproducibility-check`.

## How reproducibility is achieved

### Go binaries

Three measures make Go binaries byte-identical across machines:

**`-trimpath`** removes the local module cache and workspace path from all
`runtime.Frame` data baked into the binary. Without this, `/home/runner/go/pkg`
and `/Users/alice/go/pkg` produce different binaries.

**Frozen `ldflags`** — Version, commit SHA, and build date are injected via
`-X` flags. The build date is derived from `SOURCE_DATE_EPOCH` (the Unix
timestamp of the HEAD commit), not from the wall clock, so it is the same in
every build of the same commit.

**`CGO_ENABLED=0`** disables the C toolchain entirely. CGO output includes host
paths and can vary between systems.

### Archives

**`tar --sort=name`** ensures entries are always in lexicographic order
regardless of filesystem readdir order (which varies between kernels and
filesystems).

**`--mtime=@SOURCE_DATE_EPOCH`** clamps every entry's modification time to the
commit timestamp. Without this, the mtime reflects when the file was written
during the build, which varies.

**`--owner=0 --group=0 --numeric-owner`** strips UID/GID names. A file owned
by `runner` on GitHub Actions vs `alice` locally would otherwise differ.

**`zip -X`** (`--no-dir-entries`) strips the "extra field" metadata that zip
normally records (OS type, timestamps in extended attributes) to produce a
deterministic archive.

### Checksums file

The checksums file is generated with `LC_ALL=C sort` so the line order is
alphabetic and locale-independent on all platforms.

### SOURCE_DATE_EPOCH

`SOURCE_DATE_EPOCH` is the Unix timestamp of the HEAD commit:

```bash
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)
```

It is exported in the Makefile and passed as an environment variable in CI so
every tool that respects it (tar, zip, Rust's cargo, some Node tools)
automatically uses the same value.

Reference: <https://reproducible-builds.org/docs/source-date-epoch/>

## Local verification procedure

### Quick check (one target)

```bash
# Verify linux/amd64 is reproducible on your machine
make reproducibility-check

# Or specify a different target
bash scripts/check-reproducibility.sh linux-arm64
```

Expected output when reproducible:

```
Reproducibility check
  Target             : linux-amd64 (linux/amd64)
  SOURCE_DATE_EPOCH  : 1700000000
  GLASSBOX_VERSION   : v1.2.3
  ...

1. First build ...
2. Second build ...
3. Comparing outputs ...
  [PASS] Binary identical: 4f3e2a1b...
  [PASS] Archive identical: 9c8d7e6f...

Result: builds are reproducible for target linux-amd64.
  Binary hash : 4f3e2a1b...
  Archive hash: 9c8d7e6f...
```

### Reproduce a published release

To verify that a published release artifact matches a build you run yourself:

```bash
# 1. Check out the exact release tag
git checkout v1.2.3

# 2. Confirm SOURCE_DATE_EPOCH from the tag commit
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)
echo "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}"

# 3. Build with the same inputs used by CI
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
  go build \
    -trimpath \
    -ldflags "-s -w \
      -X 'github.com/dotandev/glassbox/internal/version.Version=v1.2.3' \
      -X 'github.com/dotandev/glassbox/internal/version.CommitSHA=$(git rev-parse HEAD)' \
      -X 'github.com/dotandev/glassbox/internal/version.BuildDate=$(date -u -d @${SOURCE_DATE_EPOCH} +%Y-%m-%dT%H:%M:%SZ)'" \
    -o glassbox-linux-amd64 \
    ./cmd/glassbox

# 4. Create the reproducible archive
tar --sort=name --owner=0 --group=0 --numeric-owner \
    --mtime="@${SOURCE_DATE_EPOCH}" \
    -czf glassbox-linux-amd64.tar.gz glassbox-linux-amd64

# 5. Compare against the published release
curl -LO https://github.com/dotandev/glassbox/releases/download/v1.2.3/glassbox-linux-amd64.tar.gz
sha256sum glassbox-linux-amd64.tar.gz
sha256sum glassbox-linux-amd64.tar.gz  # should match

# 6. Cross-check against the signed manifest
curl -LO https://github.com/dotandev/glassbox/releases/download/v1.2.3/manifest.json
python3 -c "
import json
m = json.load(open('manifest.json'))
for a in m['artifacts']:
    if a['name'] == 'glassbox-linux-amd64.tar.gz':
        print('Expected:', a['sha256'])
        break
"
```

## CI reproducibility job

The `reproducibility` job in `release.yml` runs on every release workflow
trigger. It:

1. Builds `glassbox-linux-amd64` twice using completely separate `GOPATH` and
   `GOCACHE` directories.
2. Creates the reproducible tar.gz archive from each binary.
3. Compares SHA-256 hashes of both binaries and both archives.
4. Fails the workflow if any hash differs.
5. Uploads both build outputs as an artifact on failure so maintainers can run
   `diffoscope` against them.

## Troubleshooting

### Binary hashes differ

Work through this checklist:

1. **`-trimpath` missing** — Check `GO_BUILD_FLAGS` in the Makefile and the
   `go build` command in CI. Every invocation must include `-trimpath`.

2. **Wall-clock `BUILD_DATE`** — The build date must be derived from
   `SOURCE_DATE_EPOCH`, not `date -u`. Look for `$(shell date ...)` in the
   Makefile without the epoch derivation.

3. **Non-deterministic code** — Search for `time.Now()` calls in files
   compiled into the binary (not just test files):
   ```bash
   grep -rn 'time\.Now()' internal/ cmd/ --include='*.go'
   ```
   Any `time.Now()` baked into a build artifact will differ between runs.

4. **Map iteration order** — Go maps iterate in random order. Any code that
   serialises a `map[string]...` without sorting will produce different output.
   The canonical JSON path already sorts; check for other serialisation points.

5. **Embedded random data** — Look for `rand.Read` or `uuid.New` outside test
   code.

6. **Different Go toolchain** — Confirm both builds use the exact same Go
   version. A minor version difference (1.24.3 vs 1.24.4) can change binary
   content.

### Archive hashes differ but binary hashes match

1. **`--sort=name` not set** — Filesystem readdir order is non-deterministic.
   Always use `tar --sort=name`.

2. **`--mtime` not set or differs** — Both builds must use the same
   `SOURCE_DATE_EPOCH` value.

3. **`--owner`/`--group` not set** — UID/GID names differ between machines.
   Always use `--owner=0 --group=0 --numeric-owner`.

4. **`zip` extra fields** — Use `zip -X` to strip metadata.

### Using diffoscope for detailed analysis

```bash
# Install
pip install diffoscope   # or: apt install diffoscope

# Run with KEEP_BUILD_DIRS=1 to preserve temp directories
KEEP_BUILD_DIRS=1 bash scripts/check-reproducibility.sh linux-amd64

# Script prints the temp dir paths; then:
diffoscope /tmp/tmp.XXXXX/glassbox-linux-amd64 /tmp/tmp.YYYYY/glassbox-linux-amd64
```

`diffoscope` will recursively disassemble the ELF binary and show exactly which
section differs and why.

## Known non-reproducible artifacts

| Artifact | Reason | Impact |
|----------|--------|--------|
| Rust simulator (`glassbox-sim`) | `cargo` doesn't fully honour `SOURCE_DATE_EPOCH` in all versions | Not shipped as a standalone release artifact; embedded in Docker image only |
| TypeScript `dist/` | `tsc` output may embed source map paths | JS artifacts are not currently in the signed manifest |

These are tracked and will be addressed in future releases. The Go binaries —
the primary release artifacts — are fully reproducible.
