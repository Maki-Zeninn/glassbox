# Source Origin Classification

Glassbox classifies every DWARF-resolved source location into one of four
origin classes so trace viewers, formatters, and automation scripts can tell
user-authored code apart from machine-generated build output and external
dependencies — without parsing raw file paths manually.

---

## Origin classes

| Class | JSON value | Display label | Meaning |
|---|---|---|---|
| User | `"user"` | *(none)* | Developer-authored source under the project root. No label is added; user frames are the expected happy path. |
| Generated | `"generated"` | `[generated]` | Machine-produced build output: Rust WASM artifacts (`target/`, `.wasm`), macro-expanded code, proc-macro output. The file may not exist in the workspace. |
| External | `"external"` | `[external]` | Source from an external crate or dependency: Cargo registry paths (`.cargo/registry`), Cargo git checkouts, or any absolute path outside the project root. |
| Unknown | `"unknown"` | `[unknown origin]` | Classification could not be determined, or the path was empty. Used for backward compatibility with traces produced before this field existed. |

---

## SourceOrigin — in-process integer enum

In addition to the JSON-stable `OriginClass` string enum, the source-mapping
pipeline uses a `SourceOrigin` integer enum for internal link generation and
terminal output.  The two types are kept consistent by `ClassifySourcePath`.

| Constant | Value | Terminal prefix | Link generated |
|---|---|---|---|
| `OriginUnknownSource` | `0` | *(none)* | None |
| `OriginLocal` | `1` | `[local]` | None — file is on the developer's machine only |
| `OriginRegistry` | `2` | `[crates.io]` | docs.rs permalink for the exact crate version |
| `OriginGit` | `3` | `[git]` | GitHub permalink anchored to the pinned Cargo.lock commit |

`OriginClass` (the string enum used in JSON exports) remains the canonical
published schema value.  `SourceOrigin` is an implementation detail of the
Go source-mapping pipeline.

---

## Where it appears

### JSON trace export

Every `ExecutionState.source_ref` object in a trace JSON export carries an
`origin_class` field:

```json
{
  "source_ref": {
    "file": "src/lib.rs",
    "line": 42,
    "column": 8,
    "origin_class": "user"
  }
}
```

```json
{
  "source_ref": {
    "file": "target/wasm32-unknown-unknown/release/build/my_crate/out/generated.rs",
    "line": 5,
    "column": 1,
    "origin_class": "generated"
  }
}
```

The field is omitted when the value is `"user"` or empty (via `omitempty`) to
keep the common case compact. Parsers should treat a missing field as `"user"`.

### `FallbackResult` fields

`internal/sourcemap.FallbackResult` carries two new fields populated by
`MappingResolver.Resolve`:

| Field | Type | Purpose |
|---|---|---|
| `Origin` | `SourceOrigin` | In-process origin classification for the resolved file |
| `ExternalURL` | `string` | Web-accessible permalink; empty for local files |

`ExternalURL` is set for `OriginRegistry` (docs.rs URL) and `OriginGit`
(GitHub commit permalink).  It is always empty for `OriginLocal` and
`OriginUnknownSource` because local workspace files have no external URL.

### Terminal trace output

The terminal printer (via `MergeDebugSymbols` in `internal/trace/sourcemap.go`)
prepends the origin prefix to the source mapping summary line and prints the
external URL on a separate line when one was generated:

```
  Source mapping: [local] src/lib.rs:42 (confidence 100, exact_offset)
  Source mapping: [crates.io] .../serde-1.0.203/src/lib.rs:200 (confidence 72, function)
  External URL: https://docs.rs/crate/serde/1.0.203/source/src/lib.rs
  Source mapping: [git] .../soroban-sdk/.../src/token.rs:15 (confidence 100, exact_offset)
  External URL: https://github.com/stellar/rs-soroban-sdk/blob/a1b2c3.../src/token.rs
```

User frames (`OriginLocal`) and unknown frames produce no extra annotation.

### SARIF output (`artifactLocation.uri`)

When `SecurityWarning.ExternalURL` is non-empty the SARIF writer uses it as
`artifactLocation.uri` instead of the raw local file path.  This gives GitHub
Advanced Security, VS Code's SARIF viewer, and other SARIF consumers a
clickable, web-accessible link:

```json
{
  "physicalLocation": {
    "artifactLocation": {
      "uri": "https://docs.rs/crate/serde/1.0.203/source/src/lib.rs"
    },
    "region": { "startLine": 200 }
  }
}
```

For local workspace files the raw path is preserved, keeping the result
schema-valid while pointing the developer directly at their source.

### Trap diagnostics (`FormatTrapInfo`)

The same label appears on the `Location:` line of a trap report:

```
⊗ Trap Detected: memory_out_of_bounds

⚠ Error: memory access out of bounds

📌 Location: target/wasm32-unknown-unknown/release/my_contract.wasm:0  [generated]

🔧 Function: my_contract::transfer
```

---

## Classification rules

### `OriginClass` (JSON schema, `internal/sourcemap/origin.go`)

The `Classifier.Classify` method applies these rules in priority order.

| Priority | Pattern | Class |
|---|---|---|
| 1 | Path is empty | `unknown` |
| 2 | Contains `/.cargo/registry` or `/.cargo/git` | `external` |
| 3 | Ends with `.wasm` or contains `target/wasm32` | `generated` |
| 4 | Contains `/target/` or starts with `target/` | `generated` |
| 5 | Matches a caller-supplied `ExtraBuildDirs` entry | `generated` |
| 6 | Matches a caller-supplied `ExtraExternalPrefixes` entry | `external` |
| 7 | Absolute path outside configured `ProjectRoot` | `external` |
| 8 | Everything else | `user` |

### `SourceOrigin` (in-process enum, `ClassifySourcePath`)

`ClassifySourcePath(rawPath, workspaceRoot, cargoHome)` applies these rules:

| Priority | Pattern | `SourceOrigin` |
|---|---|---|
| 1 | Path is empty | `OriginUnknownSource` |
| 2 | Contains `/.cargo/git` or `/git/checkouts/` | `OriginGit` |
| 3 | Contains `/.cargo/registry` or `/registry/src/` | `OriginRegistry` |
| 4 | `cargoHome` prefix + `/git/` | `OriginGit` |
| 5 | `cargoHome` prefix + `/registry/` | `OriginRegistry` |
| 6 | Absolute path inside `workspaceRoot` | `OriginLocal` |
| 7 | Relative path (always workspace-relative) | `OriginLocal` |
| 8 | Absolute path outside `workspaceRoot` | `OriginUnknownSource` |
| 9 | No `workspaceRoot` configured | `OriginLocal` (conservative) |

---

## Synthetic path examples

The table below shows representative paths and the classification each receives.
All examples use a workspace root of `/project` and a Cargo home of `~/.cargo`.

### `OriginClass` (JSON schema output)

| Path | `OriginClass` | Why |
|---|---|---|
| `src/lib.rs` | `user` | Relative path — workspace-relative by convention |
| `/project/src/lib.rs` | `user` | Absolute path inside `ProjectRoot` |
| `/project/target/wasm32-unknown-unknown/release/contract.wasm` | `generated` | Contains `target/wasm32` |
| `/project/target/release/build/my_crate/out/generated.rs` | `generated` | Contains `/target/` |
| `target/debug/build/proc_macro/out/bindings.rs` | `generated` | Starts with `target/` |
| `/home/user/.cargo/registry/src/index.crates.io-abc/serde-1.0.203/src/lib.rs` | `external` | Contains `/.cargo/registry` |
| `/home/user/.cargo/git/checkouts/soroban-sdk-abc/def/src/lib.rs` | `external` | Contains `/.cargo/git` |
| `/usr/local/lib/rustlib/src/rust/library/std/src/lib.rs` | `external` | Absolute path outside `ProjectRoot` |
| *(empty string)* | `unknown` | Empty path |

### `SourceOrigin` (in-process enum, terminal + URL output)

| Path | `SourceOrigin` | Terminal prefix | External URL generated |
|---|---|---|---|
| `src/lib.rs` | `OriginLocal` | `[local]` | None |
| `/project/src/token.rs` | `OriginLocal` | `[local]` | None |
| `/home/user/.cargo/registry/src/index.crates.io-abc/serde-1.0.203/src/lib.rs` | `OriginRegistry` | `[crates.io]` | `https://docs.rs/crate/serde/1.0.203/source/src/lib.rs` |
| `/home/user/.cargo/git/checkouts/soroban-sdk-abc/def/src/token.rs` | `OriginGit` | `[git]` | `https://github.com/stellar/rs-soroban-sdk/blob/<commit>/src/token.rs` |
| `/opt/toolchain/src/core.rs` | `OriginUnknownSource` | *(none)* | None |

### `ExtraBuildDirs` and `ExtraExternalPrefixes` overrides

When the built-in heuristics do not cover your project layout you can supply
additional prefixes at classifier construction time.

```go
c := sourcemap.NewClassifier(sourcemap.ClassifierOptions{
    ProjectRoot:    "/project",
    ExtraBuildDirs: []string{
        "_generated/",          // proto-generated Go files
        "vendor/generated/",    // vendored codegen output
    },
    ExtraExternalPrefixes: []string{
        "/opt/vendor-sdk/",     // a fixed-location vendor SDK
    },
})
```

| Path | With overrides | Without overrides |
|---|---|---|
| `_generated/pb/api.pb.go` | `generated` | `user` |
| `vendor/generated/mocks.go` | `generated` | `user` |
| `/opt/vendor-sdk/src/client.rs` | `external` | `external` (via absolute-outside-root rule) |

### Ambiguous and uncertain cases

Some paths are genuinely ambiguous or cannot be classified with confidence.
These are not presented as definitive — the classifier applies the most
conservative interpretation rather than guessing.

| Path | Classification | Why it is uncertain |
|---|---|---|
| `/tmp/contract.wasm` | `generated` | `.wasm` suffix triggers the generated rule, but `/tmp/` is not inherently a build directory. The wasm rule takes priority. |
| `/project/vendor/some-crate/src/lib.rs` | `user` | Vendored source under `ProjectRoot` with no matching `ExtraExternalPrefixes` is classified as user code. Add the path to `ExtraExternalPrefixes` if you want it treated as external. |
| `../../external/sdk/src/lib.rs` | `user` | A relative path with `..` components is classified as user because `Classify` does not resolve relative paths against `ProjectRoot`. Use `filepath.Abs` before calling `Classify` when path traversal is possible. |
| *(no `workspaceRoot` configured)* | `OriginLocal` (conservative) | Without a workspace root, `ClassifySourcePath` cannot distinguish local from external absolute paths and defaults to `OriginLocal`. No external URL is generated. |

### User overrides

Overrides are applied at classifier construction time — there is no runtime
override mechanism that alters a classification after the fact.

**To reclassify a path as `generated`:** add its prefix to `ExtraBuildDirs`.

**To reclassify a path as `external`:** add its prefix to `ExtraExternalPrefixes`.

**To suppress `[external]` annotations for a vendored subtree you own:** do
not add it to `ExtraExternalPrefixes` — the default behaviour (user) will
apply for paths inside `ProjectRoot`.

If a path matches both `ExtraBuildDirs` and `ExtraExternalPrefixes`, the
`ExtraBuildDirs` rule wins (it is checked first).

---

## Cargo.lock parsing for git dependencies

`ExtractGitOrigin(sourcePath, cargoLockPath)` finds the `[[package]]` entry
in Cargo.lock whose `source` field is a `git+https://...` URL and whose crate
name appears in the git checkout directory embedded in `sourcePath`.

**Cargo.lock entry format parsed:**

```toml
[[package]]
name    = "soroban-sdk"
version = "21.7.6"
source  = "git+https://github.com/stellar/rs-soroban-sdk?rev=a1b2c3d4#a1b2c3d4e5f6..."
checksum = "abcdef..."
```

The function extracts:
- **`RepoURL`** — the bare HTTPS URL, e.g. `https://github.com/stellar/rs-soroban-sdk`
  (`.git` suffix is stripped; query string is removed).
- **`CommitHash`** — the 40-char SHA from the `#fragment`, or from `rev=` if
  no fragment is present, or from the first 40 chars of `checksum` as fallback.

The generated GitHub permalink has the form:

```
https://github.com/<owner>/<repo>/blob/<commit>/<rel-path-inside-checkout>
```

---

## External URL generation

| `SourceOrigin` | Generator | Example URL |
|---|---|---|
| `OriginLocal` | — | *(none)* |
| `OriginRegistry` | `docsRSURLForPath` | `https://docs.rs/crate/serde/1.0.203/source/src/lib.rs` |
| `OriginGit` | `gitHubURLForCargoGit` + `ExtractGitOrigin` | `https://github.com/stellar/rs-soroban-sdk/blob/a1b2c3.../src/lib.rs` |
| `OriginUnknownSource` | — | *(none)* |

### docs.rs URL format

Registry crate paths follow the Cargo layout:

```
<CARGO_HOME>/registry/src/<index>/<crate>-<version>/src/...
```

The generated URL follows docs.rs conventions:

```
https://docs.rs/crate/<crate>/<version>/source/src/<file>
```

---

## Using the classifier in Go

```go
import "github.com/dotandev/glassbox/internal/sourcemap"

// ── OriginClass (JSON schema, stable string values) ───────────────────────

// One-shot classification
class := sourcemap.ClassifyPath("/project/src/lib.rs", "/project")
// → sourcemap.OriginUser

// Reusable classifier (cheaper for many frames)
c := sourcemap.NewClassifier(sourcemap.ClassifierOptions{
    ProjectRoot:    "/project",
    ExtraBuildDirs: []string{"_generated/"},
})
class = c.Classify("_generated/tokens.rs")
// → sourcemap.OriginGenerated

// Display label (for existing OriginClass terminal output)
fmt.Println(class.Label()) // → "[generated]"

// ── SourceOrigin (in-process enum, link generation) ───────────────────────

// Classify with full context for link generation
origin := sourcemap.ClassifySourcePath(
    "/home/user/.cargo/registry/src/index.crates.io-abc/serde-1.0.203/src/lib.rs",
    "/project",
    "",  // cargoHome — empty to use CARGO_HOME env or default ~/.cargo
)
// → sourcemap.OriginRegistry

// Terminal prefix
fmt.Println(origin.OriginPrefix()) // → "[crates.io]"

// Generate the external URL (docs.rs or GitHub commit link)
extURL := sourcemap.ExternalURLForResult(filePath, workspaceRoot, cargoHome, cargoLockPath)
// → "https://docs.rs/crate/serde/1.0.203/source/src/lib.rs"

// ── Git dependency: extract repo + commit from Cargo.lock ─────────────────

info, err := sourcemap.ExtractGitOrigin(
    "/home/user/.cargo/git/checkouts/soroban-sdk-abc/def/src/lib.rs",
    "/project/Cargo.lock",
)
// info.RepoURL    → "https://github.com/stellar/rs-soroban-sdk"
// info.CommitHash → "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
```

### `MappingResolver` with origin classification

`NewMappingResolver` now accepts functional options to supply Cargo context.
After `Resolve`, `FallbackResult.Origin` and `FallbackResult.ExternalURL` are
populated automatically:

```go
resolver := sourcemap.NewMappingResolver(
    projectRoot,
    externalRepoRegistry,
    sourcemap.WithCargoLockPath("/project/Cargo.lock"),
    sourcemap.WithCargoHome("/home/user/.cargo"),
)
result := resolver.Resolve(wasmData, addr)

// result.Origin is one of OriginLocal / OriginRegistry / OriginGit / OriginUnknownSource
// result.ExternalURL is "" for local files, docs.rs URL for registry, GitHub URL for git
fmt.Printf("  %s %s\n", result.Origin.OriginPrefix(), result.UserSummary())
if result.ExternalURL != "" {
    fmt.Println("  External URL:", result.ExternalURL)
}
```

### Populating `SourceRef.OriginClass` during source mapping

```go
ref := &trace.SourceRef{
    File:        resolvedFile,
    Line:        resolvedLine,
    Column:      resolvedCol,
    OriginClass: string(sourcemap.ClassifyPath(resolvedFile, projectRoot)),
}
```

### Populating `TrapInfo.OriginClass`

```go
trap.OriginClass = string(sourcemap.ClassifyPath(trap.SourceLocation.File, projectRoot))
```

---

## TypeScript (VS Code extension)

The same logic is implemented in `vscode-extension/src/sourceMap.ts`:

```typescript
import { classifyPath, originLabelText } from './sourceMap';

const origin = classifyPath(frame.file, workspaceRoot);
const label  = originLabelText(origin); // "[generated]", "[external]", or ""
```

The extension uses the classification to:
- Annotate generated and external steps in the `Glassbox: Open Source Location`
  command with a VS Code information message before navigating.
- Preserve navigation to all frames — generated and external frames are
  resolved and opened, not hidden.

---

## Implementation files

| File | Purpose |
|---|---|
| `internal/sourcemap/origin.go` | `OriginClass` + `SourceOrigin` types; `Classifier`; `ClassifyPath`; `ClassifySourcePath`; `ExtractGitOrigin`; `ExternalURLForResult`; `docsRSURLForPath`; `gitHubURLForCargoGit` |
| `internal/sourcemap/origin_test.go` | Tests for `OriginClass` / `Classifier` / `ClassifyPath` |
| `internal/sourcemap/origin_source_test.go` | Tests for `SourceOrigin`, `ClassifySourcePath`, `ExtractGitOrigin`, URL generation |
| `internal/sourcemap/fallback.go` | `FallbackResult.Origin` and `FallbackResult.ExternalURL` fields |
| `internal/sourcemap/mapping_resolver.go` | `attachOrigin()` — populates `Origin` + `ExternalURL` after each `Resolve` call |
| `internal/trace/sourcemap.go` | Terminal printer — renders `[local]`/`[crates.io]`/`[git]` prefix and `External URL:` line |
| `internal/sarif/rules.go` | `SecurityWarning.ExternalURL` field |
| `internal/sarif/families.go` | `physicalLocation()` uses `ExternalURL` as `artifactLocation.uri` when present |
| `internal/sarif/external_url_test.go` | SARIF ExternalURL propagation tests |
| `internal/trace/splitpane.go` | `SourceRef.OriginClass` field definition |
| `internal/trace/formatter.go` | `sourceRefOriginLabel` helper; label in formatter output |
| `internal/trace/trap.go` | `TrapInfo.OriginClass` field; label in `FormatTrapInfo` |
| `internal/ui/trace_view.go` | Label in detail-pane `source` row |
| `vscode-extension/src/sourceMap.ts` | `classifyPath`, `originLabelText` for the extension |
| `docs/json-output-automation.md` | `origin_class` field in the JSON schema reference |
