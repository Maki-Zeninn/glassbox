# WASM Validation & Source-Map Diagnostics Roadmap

This document tracks four related diagnostics issues: safer WASM parsing ahead
of source mapping, better handling of generated/macro Rust code in source
locations, an explain mode for source-map resolution, and versioned session
persistence. It complements [source-mapping.md](source-mapping.md), which
describes the existing discovery/resolution pipeline these issues build on.

| Issue | Title                                             | Status                |
|-------|----------------------------------------------------|------------------------|
| #556  | WASM validation before source mapping              | **Implemented**       |
| #555  | Source-map diagnostics for generated Rust code      | Planned, not yet built |
| #557  | Source-map explain mode                             | Planned, not yet built |
| #558  | Session format version migration                    | Planned, not yet built |

## #556 — WASM validation before source mapping (implemented)

Corrupt or hostile WASM binaries used to reach DWARF parsing, dead-code
elimination, or WAT disassembly with no shared bounds checking, letting a bad
file trigger opaque parser errors or unbounded work. `internal/wasmvalidate`
now provides a single bounded, non-panicking pre-validation pass used by every
WASM entry point in the codebase.

### What it checks

`wasmvalidate.Validate(data, limits)` performs one bounded pass over a
module's bytes:

- **Header** — magic bytes and version.
- **Section table** — every section's declared size is checked against the
  remaining module bytes before it's trusted; a section count ceiling caps
  pathologically fragmented modules.
- **Function indices** — the function section's declared count is
  cross-checked against the code section's actual body count, and the total
  function count (imports + defined) is capped.
- **Debug sections** — each `.debug_*` custom section's size is capped
  independently, since local debug builds can otherwise carry very large
  debug payloads.

Every failure is classified as one of two kinds, both reported with a
field-specific description and remediation hint:

- **Structural** — the bytes don't form a well-formed WASM module (bad
  magic/version, truncated or overlapping sections, malformed varints,
  inconsistent function/code counts). These always fail validation.
- **Policy** — the module is well-formed but exceeds a configured bound
  (module size, section count, debug-section size, function count).

```go
report := wasmvalidate.Validate(data, wasmvalidate.DefaultLimits())
if !report.OK {
    for _, issue := range report.Issues {
        fmt.Printf("[%s] %s: %s\n", issue.Class, issue.Field, issue.Description)
    }
}
```

### Where it's wired in

- **`internal/dwarf`** — `parseWASM` validates before extracting `.debug_*`
  sections, so a corrupt or oversized debug section is rejected before
  reaching `debug/dwarf.New`. The section-table walk itself
  (`parseWASMSections`) now uses `wasmvalidate.Sections`, replacing a
  silent `break` on truncation with an explicit error.
- **`internal/wasmopt`** — `parseSections` (used by dead-code elimination)
  now walks sections via `wasmvalidate.Sections` instead of its own ad hoc
  bounds check.
- **`internal/wat`** — `Disassembler.IsValidWasm` additionally rejects
  structurally corrupt section tables before the fallback disassembler runs,
  while intentionally not enforcing size policy there (it's a best-effort
  debug aid, not the primary gate).
- **`internal/sourcemap`** — `DiscoverLocalSymbols` validates each candidate
  `.wasm` file before hashing/indexing it, adding failures to
  `DiscoveryResult.Warnings`. `RunSourceMapPreflight` validates every
  discovered `.wasm` file and reports structural failures as `error`-severity
  and policy failures as `warning`-severity `PreflightIssue`s.

Valid, well-formed modules are unaffected — `Validate` only adds diagnostics
on the failure paths.

### Tests

- `internal/wasmvalidate/validate_test.go` — table tests for every bound
  (truncated section, malformed varint, oversized module, too many sections,
  function/code count mismatch, too many functions, oversized debug section).
- `internal/wasmvalidate/fuzz_test.go` — `FuzzValidate`, seeded with
  truncated and internally-inconsistent modules, asserts `Validate` never
  panics.

Run locally:

```bash
go test ./internal/wasmvalidate/...
go test -fuzz=FuzzValidate -fuzztime=30s ./internal/wasmvalidate/
```

---

## #555 — Source-map diagnostics for generated Rust code (planned)

**Goal:** detect generated paths (cargo build output, macro expansions) and
inline-call-site DWARF metadata, add an "origin chain" to the mapping model,
and update printers to show generated vs. user source context without false
links.

**Design direction:**
- A new `dwarf.ClassifyOrigin(loc, projectRoot)` applies path heuristics
  (`target/` build output → generated, outside project root → external
  crate, else → user) to classify each frame in an inlined call chain.
- `internal/trace/trap.go`'s `InlinedFrame`/`TrapInfo` types gain origin
  fields and a `ResolvedUserLocation`, populated by
  `TrapDetector.resolveInlinedChain`. This path is currently **dead code** —
  `internal/cmd/trace.go` never constructs a WASM-aware detector — so wiring
  that wasm-data flow through is part of this issue, not just adding fields.
- `FormatTrapInfo` (text) and new JSON tags on `TrapInfo` (JSON) both need to
  show origin without printing a source excerpt for generated paths that
  don't exist on disk.

**Scope decision:** classification is path-heuristic only for v1 (no DWARF
producer-string/macro-marker parsing), validated with hand-built DWARF test
structs rather than a real compiled macro fixture.

## #557 — Source-map explain mode (planned)

**Goal:** an opt-in report showing every candidate artifact/hash/path
considered when resolving a source location, with rejection reasons and a
final confidence — without dumping secrets or full source content.

**Design direction:**
- A new `sourcemap.Explanation`/`Candidate` type and `Resolver.ResolveExplain`
  that threads a structured trace through the existing cache → registry →
  GitHub → override pipeline in `resolver.go`.
- Exposed via a new `glassbox sourcemap explain <contract-id>` command and an
  `--explain-sourcemap` flag on `glassbox debug --dry-run`, in both text and
  JSON.
- No field on `Explanation` ever holds full source text or raw binary bytes —
  enforced by the type's shape, not by redaction logic.

## #558 — Session format version migration (planned)

**Goal:** version session envelopes, migrate older stored sessions
deterministically, and preserve unknown/additive fields on round trip.

**Design direction:**
- `internal/session/schema.go` already has most of the scaffolding
  (`SchemaVersion`, `UpgradeSessionData`, `ValidateSchemaVersion`); this issue
  converts the single inline v0→v1 migration into a proper dispatch table
  (`map[int]migrationFunc`) so future migrations are one registration, not an
  edit to the upgrade function.
- Closes a real gap: `glassbox session import` currently skips schema
  validation/upgrade entirely (unlike `glassbox session load`), so importing
  an old-schema archive persists un-migrated data.
- Unknown-field preservation is scoped to the archive (`.gbx`) JSON round
  trip only, via a `Data.Extra map[string]json.RawMessage` field and custom
  `MarshalJSON`/`UnmarshalJSON` — not the SQLite store, which maps fields to
  named columns with no extra-fields column.

---

*Design details for #555/#557/#558 above reflect the current implementation
plan and may change during implementation; #556 reflects the code as merged.*
