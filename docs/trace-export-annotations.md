# Trace Export Annotations

Glassbox carries two kinds of annotation with a trace:

- **Session annotations** — free-form notes and key/value metadata that describe
  the export as a whole (`--comment`, `--meta`).
- **Reviewer comments** — durable review notes anchored to a specific step or
  source location, each with an author, severity, and resolution state. These
  are imported and exported as a portable file (`--annotations`,
  `--export-annotations`) so a review can be handed between people.

```bash
glassbox trace execution.json \
  --export trace.html \
  --comment "Reviewed with Alice" \
  --meta session=payroll-bug \
  --meta network=testnet
```

Supported session annotation fields:

- `comments`: free-form notes, repeatable with `--comment`
- `session_metadata`: key/value metadata supplied with `--meta key=value`
- `generated_at`: timestamp added when annotations are merged into exports

Annotations are included in HTML, Markdown, and plain-text trace artifacts and
preserved in JSON trace exports under the `annotations` object.

---

## Reviewer comments

A reviewer comment is attached to something specific in the trace, so an export
can show it next to what it is about.

```bash
# Import a review file and render it into a shareable report
glassbox trace --annotations review.json \
  --export report.md --format markdown execution.json

# Export the trace's comments to hand to another reviewer
glassbox trace --export-annotations review.json execution.json

# Fail the run if any comment targets something missing from the trace
glassbox trace --annotations review.json --annotations-strict execution.json
```

### Comment fields

| Field | Required | Description |
| --- | --- | --- |
| `id` | yes | Unique within a trace. Re-importing an existing `id` **replaces** that comment, so imports are idempotent. |
| `target` | yes | What the comment is about — see [Targets](#targets). |
| `author` | yes | Who wrote it. Max 128 bytes. |
| `body` | yes | The comment text. Max 10,000 bytes. |
| `severity` | no | `info` (default), `warning`, or `critical`. |
| `resolution` | no | `open` (default), `resolved`, or `wontfix`. |
| `created_at` | yes | RFC 3339 timestamp. |
| `updated_at` | no | RFC 3339 timestamp; must not precede `created_at`. |

### Targets

| Kind | Anchor fields | Resolves to |
| --- | --- | --- |
| `step` | `step_id` | The step with that stable ID |
| `source` | `source_file`, `source_line` | The first step mapping to that location |
| `trace` | none | The trace as a whole; always resolves |

**Stable step IDs.** A step's position in the trace is not a durable anchor —
verbosity filtering rewrites state fields and schema migration copies states
between envelope versions. A step ID is therefore derived from the fields that
survive both operations (step number, operation, event type, contract ID, and
function) and looks like `step-3-1a2b3c4d`. Every export publishes the step ID
alongside the step, so you can copy one straight out of a report:

```markdown
## Step 1: host_function

- **Step ID:** `step-1-9f2c41ab`
```

If a step's identity genuinely changes, its ID changes with it and any comment
pointing at the old ID is reported as dangling rather than silently rebound to
a different step.

### Annotation file format

`--export-annotations` writes this envelope:

```json
{
  "schema_version": "1.0",
  "generated_at": "2026-07-25T09:30:00Z",
  "transaction_hash": "sha256:8f14e45fceea167a5a36dedd4bea2543...",
  "comments": [
    {
      "id": "c1",
      "target": { "kind": "step", "step_id": "step-1-9f2c41ab" },
      "author": "alice",
      "body": "require_auth is called twice on this path",
      "severity": "critical",
      "resolution": "open",
      "created_at": "2026-07-25T09:30:00Z"
    },
    {
      "id": "c2",
      "target": { "kind": "source", "source_file": "src/lib.rs", "source_line": 42 },
      "author": "bob",
      "body": "this allocates on every call",
      "severity": "warning",
      "resolution": "resolved",
      "created_at": "2026-07-25T09:31:00Z"
    }
  ]
}
```

`transaction_hash` is a SHA-256 fingerprint, not the raw hash, so the file is
safe to share. It records which trace the review was written against and is
advisory only — applying a review to a re-run of the same transaction is a
supported workflow, so a mismatch never blocks an import.

`--annotations` also accepts a bare JSON array of comments for hand-written
files, which is treated as schema version `1.0`:

```json
[
  { "id": "c1", "author": "alice", "body": "check this",
    "target": { "kind": "trace" }, "created_at": "2026-07-25T09:30:00Z" }
]
```

Omitted `severity` and `resolution` default to `info` and `open`.

### Round trips

Importing a file and exporting it again returns exactly the comments that went
in, byte for byte. Comments are written in a deterministic order — by step
position, then severity, then creation time, then ID — so an annotation file
checked into version control does not churn between exports.

Every comment survives the round trip, **including ones whose target no longer
resolves**. Dropping them would destroy review history that is still valid
against the trace it was written for.

---

## Dangling references

A comment's target can stop resolving when the trace it is applied to is not
the trace it was written against, or when the trace has been transformed:

- **Verbosity filtering.** `--trace-verbosity summary` strips `source_file` and
  `source_line` from every step, so `source`-anchored comments no longer
  resolve. `step`-anchored comments are unaffected — this is the main reason to
  prefer them.
- **Schema migration.** Migrating a trace between format versions preserves
  step IDs, so comments continue to resolve.
- **Wrong trace.** Applying a review file to an unrelated trace leaves most or
  all targets unresolved.

Dangling references are **reported, never dropped**, and one broken anchor
never invalidates the comments around it:

```
Warning: annotation "c2" by bob targets src/lib.rs:42 which is not present in
this trace: source locations were stripped from this trace (verbosity 'summary'
removes source_file and source_line); re-run with --trace-verbosity normal to
resolve source-anchored comments
Imported 4 annotation(s) from review.json (3 resolved, 1 dangling)
```

Exports render these in a dedicated section that states the unresolved target
and the reason, so a reader can see what was reviewed even when the anchor is
gone.

Pass `--annotations-strict` to turn dangling references into a hard failure —
useful in CI, where a review file drifting away from its trace should break the
build rather than produce a quietly incomplete report.

---

## Where comments appear in exports

| Format | Per-step comments | Trace-level comments | Dangling comments |
| --- | --- | --- | --- |
| HTML | Inside the step's `<details>` block, with severity and resolution badges | Header section | Header section, with the reason |
| Markdown | `### Reviewer comments on step N` under the step | `## Reviewer Comments (whole trace)` | `## Reviewer Comments With Unresolved Targets` |
| Text | Indented under the step | Header section | Header section, with the reason |
| JSON | `annotations.reviewer_comments`, each with its `target` object | same | same |

Comment content is HTML-escaped in the HTML export.

---

## Limits

Limits bound the collaboration metadata that travels with an artifact, so a
malformed or hostile annotation file cannot turn a trace export into an
unbounded payload.

| Limit | Value |
| --- | --- |
| Comments per trace (free-form) | 100 |
| Reviewer comments per trace | 100 |
| Comment body length | 10,000 bytes |
| Author length | 128 bytes |
| Comment ID length | 128 bytes |
| Target source path length | 4,096 bytes |

Reviewer comment limits are enforced at three points: when an annotation file
is loaded, when comments are attached to a trace, and at export time. Exceeding
a limit fails the whole operation rather than truncating, and the trace is left
untouched so a rejected import cannot partially apply.

---

## Validation

All annotation flags are validated in `PreRunE` before the trace file is loaded,
so every error is surfaced in a single pass.

### `--comment`

**Validation:**
- Value must not be empty or whitespace-only

**Error example:**
```
--comment value at position 0 is empty or whitespace-only
  Fix: provide non-empty comment text or omit the empty --comment flag
```

**Limits (enforced at export time):**
- Maximum 100 comments per trace export
- Maximum 10,000 characters per individual comment

### `--meta key=value`

**Validation:**
- Every value must be in `key=value` format (contains at least one `=`)
- Key must not be empty or whitespace-only

**Error example:**
```
--meta value "no-equals-sign" is not in key=value format
  Fix: supply metadata as key=value pairs, e.g. --meta env=testnet --meta version=1.2
```

Values containing `=` are parsed correctly — only the first `=` is used as a
separator, so `--meta filter=type=contract_call` produces `filter` → `type=contract_call`.

### `--annotations`

**Validation:**
- Path must exist and be readable (checked in `PreRunE`)
- File must parse as the envelope form or a bare comment array
- `schema_version` must be supported (currently `1.0`)
- Every comment must pass field validation and stay within limits
- Comment IDs must be unique within a single file

An invalid entry names the offending comment rather than failing anonymously:

```
annotation file "review.json" entry 2 is invalid: comment "c3" has unknown
severity "urgent"
  Fix: use one of "info", "warning", or "critical"
```

### `--export-annotations`

**Validation:**
- Must be a file path, not a directory

An existing file is overwritten with a warning; pass `--force` to suppress it.

### `--annotations-strict`

Requires `--annotations` or `--export-annotations`; on its own it has nothing
to check and is rejected in `PreRunE`.

---

## Multiple validation errors

When `--comment` and `--meta` failures are combined with other flag errors, all
failures are numbered and reported together:

```
2 trace command validation error(s):
  1. --meta value "no-equals" is not in key=value format
     Fix: supply metadata as key=value pairs, e.g. --meta env=testnet --meta version=1.2
  2. --export "./traces/" looks like a directory path; provide a full file path
     Fix: specify a filename (e.g. --export ./traces/output.html)
```
