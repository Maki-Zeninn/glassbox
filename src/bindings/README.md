# Command Schema Bindings

This directory contains **auto-generated** TypeScript artifacts derived from the
canonical Glassbox command schema defined in
[`internal/bindings/schema.go`](../../internal/bindings/schema.go).

> **Do not edit these files manually.**  Any manual change will be overwritten
> the next time the generator runs and will fail the CI drift check.

---

## Files

| File | Purpose |
|---|---|
| `command-schema.ts` | Full schema as a typed `const` — interfaces and runtime data |
| `command-types.ts` | Per-command `*Options` and `*Output` TypeScript interfaces |
| `command-validators.ts` | `validateCommandOptions` / `validateCommandOutput` with field-path diagnostics |
| `index.ts` | Barrel re-export of everything above |

---

## Usage

```typescript
import {
  GLASSBOX_COMMAND_SCHEMA,
  getCommandDefinition,
  validateCommandOptions,
  validateCommandOutput,
} from './src/bindings';

// Look up a command definition at runtime
const def = getCommandDefinition('debug');
console.log(def?.flags.map((f) => f.name));
// → ['network', 'rpc-url', 'xdr-file', ...]

// Validate input options before dispatching
const result = validateCommandOptions('debug', { network: 'testnet' });
if (!result.valid) {
  for (const err of result.errors) {
    console.error(err.path, err.message); // field-path diagnostics
  }
}

// Validate an external JSON payload (permissive mode accepts additive fields)
const outResult = validateCommandOutput(
  'debug',
  payload,
  { strict: false },
);
```

---

## Regenerating

Run the generator whenever `internal/bindings/schema.go` changes:

```bash
go run internal/bindings/cmd/generate_schema_main.go
# or
glassbox generate-schema --output src/bindings
```

Then commit the updated files:

```bash
git add src/bindings/
git commit -m "chore(bindings): regenerate TypeScript command schema"
```

---

## CI Drift Check

The `scripts/check-bindings-drift.sh` script regenerates the schema into a
temp directory and diffs it against the committed files:

```bash
bash scripts/check-bindings-drift.sh
```

Exit code 0 means the committed files are up-to-date; exit code 1 means
drift was detected.

The CI pipeline runs this check on every pull request.
