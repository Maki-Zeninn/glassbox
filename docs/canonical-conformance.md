# Cross-Language Canonical JSON Conformance Tests

Glassbox uses deterministic, canonical JSON serialisation for audit log hashing
and signature verification. Go and TypeScript both hash the same logical payload
and must produce byte-identical output. This document explains the conformance
test infrastructure that enforces that guarantee.

## Why this matters

An audit log is signed over a canonical hash. If Go and TypeScript canonicalize
a payload differently — even by a single byte — the TypeScript verifier will
reject a log produced by the Go signer, or vice versa. The conformance corpus
makes this class of bug impossible to introduce silently.

## Corpus location

```
testdata/canonical-conformance/corpus.json
```

The corpus is language-neutral JSON. Every test case has:

| Field | Description |
|---|---|
| `id` | Unique fixture name used in test output |
| `description` | Human-readable explanation of what property is tested |
| `input` | The raw JSON value to canonicalize |
| `canonical` | The expected byte-exact canonical string |
| `sha256` | SHA-256 hex digest of the `canonical` string encoded as UTF-8 |
| `notes` | Optional implementation notes |

## What the corpus covers

| Case ID | Property tested |
|---|---|
| `basic-key-ordering` | Top-level object keys sorted lexicographically |
| `nested-key-ordering` | Recursive key sorting at every nesting depth |
| `no-whitespace` | No whitespace between tokens |
| `array-insertion-order` | Arrays preserve insertion order (not sorted) |
| `null-value` | `null` values serialise as JSON `null` |
| `boolean-values` | `true` / `false` preserved |
| `integer-numbers` | Integers have no trailing `.0` |
| `float-numbers` | IEEE 754 double-precision floating point |
| `unicode-strings` | Non-ASCII characters (emoji, CJK, Arabic) preserved as UTF-8 |
| `empty-object` | `{}` |
| `empty-array` | `{"items":[]}` |
| `deeply-nested` | Key sorting at all nesting levels simultaneously |
| `audit-payload-typical` | Realistic AuditLogger payload |
| `array-of-objects` | Mixed arrays: element order preserved, object keys sorted |
| `mixed-types-in-array` | Arrays may contain different JSON types |
| `string-escaping-basics` | Backslash, quote, newline, tab per JSON spec |

### Invalid cases

Cases in the `invalid` array must be **rejected** by both runtimes before
serialisation:

| Case ID | Reason |
|---|---|
| `nan-value` | NaN cannot be expressed in JSON |
| `infinity-value` | Infinity cannot be expressed in JSON |
| `circular-reference` | Circular references cannot be serialised |

## Running the conformance suite

### Go

```bash
# Run only the conformance tests
go test ./internal/cmd/... -run TestCanonicalConformance -v

# Run corpus integrity check (verifies corpus.json is self-consistent)
go test ./internal/cmd/... -run TestCanonicalConformance_CorpusIntegrity -v
```

### TypeScript / Jest

```bash
# Run only the conformance tests
npx jest src/audit/__tests__/canonical-conformance.spec.ts --verbose

# Or via npm
npm test -- --testPathPattern=canonical-conformance
```

### Both at once (CI)

```bash
go test ./internal/cmd/... -run TestCanonicalConformance && \
  npx jest src/audit/__tests__/canonical-conformance.spec.ts
```

## Failure output

Both test suites are designed to emit **only safe diagnostic information** on
failure. Raw payload values are never printed. Failure messages contain:

- The fixture `id`
- The first 16 characters of the computed and expected SHA-256 hashes
- A reminder to enable debug logging if the full canonical string is needed

Example failure:

```
--- FAIL: TestCanonicalConformance_ValidCases/audit-payload-typical
    canonical_conformance_test.go:107:
        [audit-payload-typical] SHA-256 hash mismatch
          fixture hash: d95ddcc344dbee5…
          computed: 1a2b3c4d5e6f7a8b…
```

## Adding a new fixture

1. Add a new entry to the `cases` array in `testdata/canonical-conformance/corpus.json`.
2. Set `sha256` to `"placeholder"` initially.
3. Run the corpus integrity test to get the correct hash:
   ```bash
   go test ./internal/cmd/... -run TestCanonicalConformance_CorpusIntegrity -v 2>&1 | grep "recomputed"
   ```
4. Update the `sha256` field with the printed value.
5. Run both test suites to confirm the new fixture passes.

Do **not** duplicate a fixture `id`. The tests will fail on duplicate IDs.

## Canonicalization implementations

### Go

Located in `internal/cmd/canonical.go`. The function `canonicalJSON(v interface{})` accepts
any Go value, sorts all map keys recursively, and returns compact JSON bytes with no
trailing newline.

Key properties:
- Keys sorted with `sort.Strings`
- No HTML escaping (`SetEscapeHTML(false)`)
- No indentation
- Unicode preserved as raw UTF-8 bytes

### TypeScript

Uses `fast-json-stable-stringify` (npm package). Called as `stringify(value)` in
`src/audit/AuditLogger.ts` and `src/audit/AuditVerifier.ts`.

Key properties:
- Keys sorted lexicographically at every level
- Unicode preserved as raw UTF-8 (no `\uXXXX` escaping beyond JSON minimum)
- No whitespace

Both implementations produce byte-identical output for all corpus cases.

## Related documentation

- [Audit Canonicalization](audit-canonicalization.md) — canonicalization design and rationale
- [Audit Log Signing](audit-signing.md) — how signed logs are produced and verified
- [Binding Validation](binding-validation.md) — ABI binding generation and staleness detection
