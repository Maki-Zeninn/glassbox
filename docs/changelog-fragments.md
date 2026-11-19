# Changelog Fragments

Every PR that makes a user-facing change must include a changelog fragment. The
fragment is a TOML file in `changelog/fragments/`. The `make changelog-generate`
target assembles all fragments into `CHANGELOG.md` before a release.

---

## When to write a fragment

Write a fragment when your PR:

- Adds, renames, or removes a CLI flag or subcommand.
- Changes the shape of any JSON output field.
- Changes the session file format or schema.
- Adds, changes, or removes a public Go symbol in a tracked package.
- Changes exit codes or error codes.
- Fixes a bug that was visible to users.
- Improves performance in a user-observable way.
- Introduces a security fix.

You do not need a fragment for:

- Pure documentation changes.
- Internal refactors with no user-visible effect.
- Test-only changes.
- CI configuration changes.

---

## Fragment format

Create a file at `changelog/fragments/<pr-number>-<slug>.toml`.

```toml
# category: one of cli, schema, security, breaking, fix, performance
category = "cli"

# pr: your GitHub PR number (must be unique across all pending fragments)
pr = 612

# summary: one sentence, present-tense, user-facing, ≤ 120 characters
summary = "added --progress-json flag to debug for structured NDJSON progress events"

# details: optional prose that appears beneath the summary in the changelog
# Leave empty ("") to use summary only
details = "Scripts and CI pipelines can parse events to correlate failures to phases. See docs/progress-events.md for the event schema."

# breaking: true only when users must change their invocation or scripts
breaking = false

# migration_note: required when breaking = true
# Describe what users must do; link to MIGRATION_GUIDE.md if applicable
migration_note = ""

# affects: which compatibility-matrix surfaces this change touches
# Valid values: cli-flags, json-output, session-format, go-api, schema,
#               exit-codes, protocol, extension
affects = ["cli-flags"]

# compatibility: stability level of the affected surface after this change.
# Valid values: stable, beta, experimental, deprecated, removed
# Optional — omit when no stability-level change is intended.
# compatibility = "stable"
```

---

## Categories

| Category | Use for |
|----------|---------|
| `breaking` | Any change that requires users to update scripts or code |
| `security` | Security fixes and security-related improvements |
| `cli` | New or changed CLI flags, commands, output text |
| `schema` | JSON output field changes, schema version bumps, session format changes |
| `fix` | Bug fixes visible to users |
| `performance` | User-observable speed or memory improvements |

A fragment always has exactly one category. If a change spans multiple
categories (e.g. a CLI flag change that is also a breaking change), use
`breaking`.

---

## Validation rules

`make changelog-check` (and CI on every PR) enforces:

1. All required fields are present: `category`, `pr`, `summary`, `breaking`, `affects`.
2. `category` is one of the valid values above.
3. `pr` is a positive integer.
4. No two pending fragments share the same `pr` value.
5. `summary` is ≤ 120 characters.
6. `breaking = true` fragments must have a non-empty `migration_note`.
7. Every value in `affects` is a recognised surface name.
8. If `compatibility` is present, it must be one of: `stable`, `beta`, `experimental`, `deprecated`, `removed`.

### CI enforcement

In addition to `make changelog-check`, the `changelog-fragment-check` workflow
runs on every PR and detects whether a fragment is required based on which
files were changed:

**Fragment required** when any changed file touches a user-visible surface:
- `cmd/glassbox/**`, `internal/cmd/**` — CLI commands and flags
- `internal/errors/**` — stable error codes
- `src/**` — TypeScript/JS public API
- `docs/schema/**` — JSON schema files
- `.api-snapshots/**` — API compatibility snapshots
- `internal/audit/**`, `internal/signer/**` — audit/signing protocols
- `internal/bindings/**` — bindings contract
- `internal/session/**` — session format
- `internal/manifest/**`, `internal/sbom/**` — release artifact formats
- `vscode-extension/src/**` — VS Code extension API

**Fragment not required** (automatically exempt):
- `*_test.go`, `testdata/`, `test/`, `tests/` — test-only changes
- `docs/**` (except `docs/schema/`) — pure documentation
- `scripts/**`, `.github/**`, `Makefile`, `go.sum` — CI and tooling
- `internal/` packages not on a user-visible surface — internal refactors

**Override mechanisms** (when a change is truly internal but touches an
otherwise-tracked path):
- Add the label `no-fragment` to the PR in GitHub
- Commit a `.changelog-override` file containing `no-fragment` to the PR branch

---

## Generating the changelog

Before a release:

```bash
# 1. Validate all fragments
make changelog-check

# 2. Preview what will be generated
make changelog-dry-run

# 3. Generate and prepend to CHANGELOG.md
make changelog-generate VERSION=v1.3.0

# 4. Archive the consumed fragments
git mv changelog/fragments/*.toml changelog/released/v1.3.0/

# 5. Commit CHANGELOG.md and the archived fragments together
git add CHANGELOG.md changelog/
git commit -m "release: prepare v1.3.0 changelog"
```

The generation is deterministic: the same set of fragments always produces the
same output, so the changelog can be reproduced from a clean checkout.

---

## Breaking changes

When `breaking = true`:

1. The fragment is placed in the **Breaking Changes** section of the changelog,
   ahead of all other categories.
2. A highlighted warning block is appended to the release section pointing to
   `MIGRATION_GUIDE.md`.
3. `migration_note` is printed inline below the summary.

Every breaking change also requires:

- An entry in `docs/compatibility-matrix.md` (update the stability level to
  `deprecated` or `removed` as appropriate).
- An entry in `MIGRATION_GUIDE.md` with before/after examples.
- Regenerated API snapshot files (the CI snapshot check will fail until they
  are updated).

---

## Released fragments

Once fragments are consumed into a release they are moved to
`changelog/released/<version>/` and are no longer processed by the generation
script. This keeps the pending set small and makes it easy to audit what went
into each release.
