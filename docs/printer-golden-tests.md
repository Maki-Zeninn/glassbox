# Printer Golden Tests

The Text, Markdown, JSON, and HTML trace printers can drift in *meaning* even
while each compiles and keeps its own isolated tests green — a field renamed in
one template, silently dropped in another, formatted differently in a third.
The printer golden tests catch this by rendering one set of canonical trace
fixtures through **every** supported output mode and comparing the results
against checked-in golden files.

## What is covered

| Piece | Location |
|-------|----------|
| Canonical fixtures | `internal/trace/golden_fixtures_test.go` |
| Golden tests | `internal/trace/printer_golden_test.go` |
| Golden files | `internal/trace/testdata/printer_golden/*.golden` |

**Output modes** (one golden file per fixture × mode):

- `terminal` — the interactive tree printer (`PrintExecutionTrace`, color off, width 100)
- `text` — plain-text export (`GenerateTracePlainTextWithOptions`)
- `markdown` — Markdown export (`GenerateTraceMarkdownWithOptions`)
- `html` — HTML export (`GenerateTraceHTMLWithOptions`)
- `json` — JSON export (mirrors the `json` branch of `ExportExecutionTraceWithOptions`)

**Fixtures** (one per semantic category from issue #547):

| Fixture | Exercises |
|---------|-----------|
| `calls` | Contract calls, host functions, auth steps, return values |
| `errors` | Failed calls, traps, final failure status |
| `source_locations` | Source file/line and GitHub links (plus an unmapped step) |
| `gas` | Cost annotations and per-component breakdowns |
| `comments` | Trace comments and session metadata |
| `empty` | Zero-step traces and their diagnostic messaging |

Beyond byte comparison, `TestPrinterGolden_SemanticConsistency` asserts that
each fixture's semantic tokens (hashes, function names, errors, cost values,
comments, …) survive into every mode that is supposed to carry them, and
`TestPrinterGolden_ExportPathCoversAllFormats` +
`TestPrinterGolden_NoOrphanGoldens` gate coverage: a format added to
`trace.SupportedExportFormats()` fails CI until it has a renderer and golden
files, and goldens orphaned by a renamed fixture fail CI until deleted.

## Running the tests

```bash
go test ./internal/trace -run TestPrinterGolden
```

## Updating goldens after an intentional formatting change

1. Make your printer/template change.
2. Regenerate the golden files:

   ```bash
   go test ./internal/trace -run TestPrinterGolden -update
   ```

3. Inspect `git diff internal/trace/testdata/printer_golden/` — **the golden
   diff is the review artifact**. Every changed line should be a change you
   intended; anything else is drift the tests just caught.
4. Commit the golden files together with the code change so reviewers see the
   output change alongside its cause.

Never hand-edit a `.golden` file; always regenerate with `-update`.

## Normalization policy

Golden comparison must be deterministic, so:

- **Fixtures pin all volatile fields at build time.** `AddState` stamps
  wall-clock timestamps on states and snapshots; the fixture builder
  (`newGoldenTrace`) overwrites them with fixed times derived from the
  canonical `2026-01-01T00:00:00Z` stub.
- **Comparison normalizes only line endings** (CRLF → LF), the one
  environment-volatile byte sequence left. `.golden` files are also pinned to
  LF via `.gitattributes`.

Do **not** widen normalization (e.g. scrubbing hashes or numbers): masking
stable fields hides exactly the drift these tests exist to catch. If you add a
fixture containing a genuinely volatile field, pin it in the builder instead.

## Adding a fixture or output mode

- **New fixture**: add it to `goldenFixtures()` with `commonTokens` (must
  appear in every mode) and `exportTokens` (export formats only — the terminal
  printer intentionally omits source locations and annotations), run `-update`,
  and commit the new goldens. Follow the stub-value and secret-avoidance rules
  in [regression-test-guide.md](regression-test-guide.md).
- **New output mode**: add it to `trace.SupportedExportFormats()`, wire the
  export switch, add a case in `renderGoldenMode`, then `-update`. CI fails
  until all three are done.
