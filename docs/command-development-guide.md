# Command Development Guide

This guide is the checklist and reference for anyone adding a new top-level command or subcommand to Glassbox. It derives every step from existing commands (`debug`, `version`, `stats`, `audit:sign`, `protocol:diagnose`) and from the conventions in `internal/cmd/`, `internal/clioutput/`, `internal/errors/`, and the integration test suite. A reviewer can use this document to accept or reject a PR for a new command.

---

## 1. Registration

Every command is a `*cobra.Command` value registered from an `init()` function in its own file under `internal/cmd/`.

```go
// internal/cmd/mycommand.go
// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import "github.com/spf13/cobra"

var myCmd = &cobra.Command{
    Use:     "mycommand <required-arg>",
    GroupID: "utility",          // one of: core, testing, management, development, utility
    Short:   "One-line summary (≤60 chars, no period)",
    Long:    myCommandLong,      // separate const for readability
    Example: myCommandExample,
    Args:    cobra.ExactArgs(1), // or cobra.NoArgs, cobra.RangeArgs, etc.
    PreRunE: validateMyCommand,
    RunE:    runMyCommand,
}

func init() {
    myCmd.Flags().StringVar(&myFlag, "flag-name", "", "Description (env: GLASSBOX_FLAG_NAME)")
    rootCmd.AddCommand(myCmd)
}
```

Naming rules:
- Lowercase, hyphen-separated for multi-word commands (`simulate-upgrade`).
- Hierarchical subcommands use a colon as the separator in `Use` (`audit:sign`), not a nested cobra subcommand, unless the group is large enough to warrant a parent group command.
- Keep `Short` under 60 characters. `Long` is the place for full context, accepted values, environment variable names, and gotchas.
- Always set `GroupID` — it controls where the command appears in `--help`.

### Root persistent flags

These flags are registered on the root command and inherited by its
subcommands. Their defaults below are the values registered with Cobra; some
flags document an automatic behavior when left at that default. Command-local
flags are intentionally not included. Hidden internal flags are omitted.

| Flag | Default | Purpose |
|---|---:|---|
| `--audit-log` | empty | Write a signed operation audit log |
| `--audit-log-provider` | empty | Select the audit signing provider |
| `--audit-log-software-private-key` | empty | Supply the software audit signing key |
| `--audit-log-pkcs11-module` | empty | Set the PKCS#11 module path |
| `--audit-log-pkcs11-pin` | empty | Supply the PKCS#11 PIN |
| `--audit-log-pkcs11-token-label` | empty | Select the PKCS#11 token |
| `--audit-log-pkcs11-key-label` | empty | Select the PKCS#11 key by label |
| `--audit-log-pkcs11-key-id` | empty | Select the PKCS#11 key by hex ID |
| `--audit-log-meta` | empty | Add audit metadata as `key=value` entries |
| `--deterministic-seed` | empty | Set the deterministic replay seed |
| `--deterministic` | `false` | Enable deterministic replay mode |
| `--rpc-response-limit` | `0` | Set the response limit; `0` uses the 32 MiB default |
| `--rpc-aggregate-limit` | `0` | Set the aggregate limit; `0` uses the 512 MiB default |
| `--allow-untrusted-plugins` | `false` | Allow community and untrusted plugins to load |
| `--timestamp` | `0` | Override the ledger header timestamp |
| `--window` | `0` | Set the simulation time window in seconds |
| `--profile` | `false` | Enable CPU and memory profiling |
| `--profile-format` | `html` | Choose `html` or `svg` profile output |
| `--log-level` | empty | Set log verbosity; empty leaves the configured level unchanged |
| `--verbose` | `false` | Enable debug-level output |
| `--no-color` | `false` | Disable ANSI color output |
| `--non-interactive` | `false` | Disable prompts and terminal controls; CI and pipes are also detected |
| `--telemetry` | `false` | Opt in to anonymized command usage telemetry |
| `--telemetry-anonymized` | `true` | Send telemetry in anonymized mode |
| `--version`, `-V` | `false` | Print the Glassbox version |

For example, a root persistent flag can be used with a subcommand:

```bash
glassbox --no-color debug <transaction-hash>
```

Options such as `debug --network` belong to that command, not this table.

### Help text requirements

The `Long` field and `Example` field are required, not optional. Help must cover:
- What the command does and when to use it.
- Every non-obvious flag, including which environment variables shadow it.
- At least one concrete `Example` invocation per distinct mode (network, local, JSON, dry-run).
- Non-interactive behavior (what changes in CI; see section 7).

---

## 2. Validation

All validation belongs in `PreRunE`, not in `RunE`. This ensures expensive operations (network calls, simulator spawns) never start on bad inputs. The helper functions in `internal/cmd/cmd_validation.go` cover the common cases.

```go
func validateMyCommand(cmd *cobra.Command, args []string) error {
    // 1. Mutually exclusive flags
    changed := map[string]bool{
        "flag-a": cmd.Flags().Changed("flag-a"),
        "flag-b": cmd.Flags().Changed("flag-b"),
    }
    if err := validateMutuallyExclusive(changed, "flag-a", "flag-b"); err != nil {
        return err
    }

    // 2. Enum flags — reject early with the valid values listed
    if myFormatFlag != "" {
        switch myFormatFlag {
        case "json", "text":
        default:
            return errors.WrapValidationError(fmt.Sprintf(
                "--format %q is not supported — must be one of: json, text", myFormatFlag,
            ))
        }
    }

    // 3. File existence
    if err := validateFilePath("my-file", myFileFlag); err != nil {
        return err
    }

    // 4. Network
    if err := validateNetwork(networkFlag); err != nil {
        return err
    }

    return nil
}
```

Validation rules:
- Every error must be an `ErstError` created via one of the `errors.Wrap*` helpers. Bare `fmt.Errorf` produces no stable error code and no Hint.
- Include the invalid value and the set of accepted values in the message so users do not have to re-read the help.
- File-path validation (`validateFilePath`) checks existence and readability. Add a remediation note for missing files (e.g. "Build your contract first").
- Reject null bytes in any flag that feeds a file path (use `validateNoNullBytes` or `ValidateDebugInputPaths` as a model).
- **The invariant: zero external I/O may occur before `PreRunE` returns.** No network calls, no simulator spawns, no file writes.

### Available validation helpers (cmd_validation.go)

| Helper | Purpose |
|--------|---------|
| `validateNetwork(network string)` | Accepts `testnet`, `mainnet`, `futurenet`, `standalone`, `public`; empty OK |
| `validateFilePath(flag, path string)` | Checks file exists and is readable; empty OK (optional) |
| `validatePositiveInt(flag string, val int)` | Rejects negative integers |
| `validateMutuallyExclusive(set, flags...)` | Errors when >1 flag is set |
| `validateExactlyOne(set, flags...)` | Errors when 0 or >1 flag is set |
| `validateAtLeastOne(set, flags...)` | Errors when no flag is set |
| `validateEnum(context, value string, allowed []string)` | Rejects values outside the allowed list |
| `validateRPCURL(flag, url string)` | Verifies `http://` or `https://` prefix |
| `validateNonEmptyString(flag, value string)` | Rejects empty or whitespace-only strings |
| `validateOutputDir(flag, dir string)` | Verifies/creates output directory |
| `validateNoNullBytes(flag, value string)` | Rejects paths containing null bytes |

### Adding a new validation helper

When a validation pattern does not exist in `cmd_validation.go`, add it there (not inline in `RunE`) and write a unit test for it. Follow the naming convention: `validateFoo(flag string, val T) error`.

---

## 3. Error handling

Every error returned by `RunE` must be an `*errors.ErstError`. The exit code, JSON shape, and hint all derive from the error's `Code` field — nothing else.

```go
// wrapping a known failure class
return errors.WrapRPCConnectionFailed(err)

// wrapping validation
return errors.WrapValidationError("--limit must be positive")

// wrapping with a custom hint
return &errors.ErstError{
    Code:    errors.ErstSimulationFailed,
    Message: fmt.Sprintf("simulation failed for tx %s: %v", txHash, err),
    OrigErr: err,
    Hint:    "Check the contract logic and retry with --verbose for a full trace.",
}
```

Adding a new error code requires five steps (see `docs/stable-error-codes.md`):
1. Add the constant to `internal/errors/glassbox_error_code.go`.
2. Add it to `codeToSentinel`.
3. Add a sentinel error to `errors.go`.
4. Update the table in `docs/stable-error-codes.md`.
5. Assign the exit-code bucket in `internal/cmd/exitcode.go` (`userErrorCodes` or `configErrorCodes`; defaults to `ExitInternalError`).

Hints are printed separately from the error message and never appear in `error.Error()`. Add a hint whenever the recovery action is not obvious from the message alone.

---

## 4. Output formats

Every command that produces structured output must support both text and JSON modes. JSON is gated by `--json` or `--format json`. Use `clioutput.WantsJSON(jsonFlag, formatFlag)` to check.

### Success output

```go
type MyOutput struct {
    Field string `json:"field"`
    Count int    `json:"count"`
}

result := MyOutput{Field: "value", Count: 3}

if clioutput.WantsJSON(jsonFlag, formatFlag) {
    return clioutput.Write(cmd.OutOrStdout(), "mycommand", result)
}
// text path
fmt.Fprintf(cmd.OutOrStdout(), "field: %s\ncount: %d\n", result.Field, result.Count)
return nil
```

The JSON envelope is always:

```json
{
  "schema_version": "1.0",
  "glassbox_version": "...",
  "generated_at": "...",
  "command": "mycommand",
  "data": { ... }
}
```

### Error output (JSON mode)

Errors in JSON mode are written by `main.go`'s `run()` function automatically via `clioutput.WriteError`. Do not write error JSON from inside `RunE` — return the `ErstError` and let the root handler emit the envelope.

The error envelope shape is:

```json
{
  "schema_version": "1.0",
  "glassbox_version": "...",
  "generated_at": "...",
  "command": "mycommand",
  "error": {
    "code": "VALIDATION_FAILED",
    "severity": "error",
    "message": "...",
    "remediation": "...",
    "context": { "flag": "value" }
  }
}
```

### Format flag validation

If your command accepts `--format`, validate it in `PreRunE` before any I/O:

```go
if cmd.Flags().Changed("format") {
    switch strings.ToLower(formatFlag) {
    case "json", "text":
    default:
        return errors.WrapValidationError(fmt.Sprintf(
            "invalid --format %q — must be one of: json, text", formatFlag,
        ))
    }
}
```

### Output routing

Always write to `cmd.OutOrStdout()` (not `os.Stdout`) and errors to `cmd.ErrOrStderr()` (not `os.Stderr`). This makes tests that capture output work correctly.

Progress messages and hints go to stderr. The final structured payload goes to stdout.

---

## 5. Cancellation

Commands must respect `cmd.Context()` so SIGINT and SIGTERM produce a clean exit code 130 instead of a goroutine dump.

```go
func runMyCommand(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context() // wired to signal.NotifyContext in root.go

    result, err := doWork(ctx) // pass ctx to every blocking call
    if err != nil {
        if ctx.Err() != nil {
            // The error is a cancellation — wrap so IsInterrupted detects it.
            return cmd.ErrOrStderr() // handled by run() in main.go
        }
        return errors.WrapRPCConnectionFailed(err)
    }
    ...
}
```

`IsInterrupted(err)` in `internal/cmd/` detects `context.Canceled` and `context.DeadlineExceeded` and maps them to exit code 130. You do not need to handle this manually — just propagate `ctx` to every blocking call and return errors as-is.

For long operations, emit a `--progress-json` event before each phase (see `internal/progress/`). This lets CI pipelines correlate failures to phases without parsing human-readable output.

---

## 6. Telemetry

Telemetry is opt-in. Do not emit spans or attributes by default. The telemetry gate is checked in `PersistentPreRunE` in `root.go` via `TelemetryFlag`.

For a per-command span:

```go
tracer := telemetry.GetTracer()
ctx, span := tracer.Start(ctx, "mycommand")
span.SetAttributes(
    telemetry.Attr("network", networkFlag),
    // Never include raw tx hashes, keys, or paths in telemetry attributes.
    // Use telemetry.Fingerprint() for values derived from user input.
)
defer span.End()
```

Telemetry rules:
- No raw transaction hashes, contract IDs, keys, tokens, or file paths in attributes. Use `telemetry.Fingerprint(value)` to emit a non-reversible 32-char hex prefix for aggregation.
- Command names are sanitized before emission (alphanumeric + dash/colon/underscore, max 64 chars). Subcommand names inherit this.
- Export failures are swallowed silently by `silentSpanExporter` — telemetry must never block or error the primary path.
- Record command usage via `telemetry.RecordCommandUsage(ctx, cmd.CommandPath())` if the command is in `PersistentPreRunE`'s telemetry block.

---

## 7. Non-interactive and no-color

Commands that show spinners, prompts, or interactive TUI elements must check `termctx.IsNonInteractive()` before doing so, and fall back to plain output.

```go
tc := termctx.New(termctx.Options{})
if tc.IsInteractive() {
    // show spinner
} else {
    fmt.Fprintln(cmd.OutOrStdout(), "working...")
}
```

Non-interactive mode is auto-detected from CI environment variables and pipe/redirect detection. Users can also force it with `--non-interactive`. Your command gets this for free from `PersistentPreRunE` — just query `termctx.IsNonInteractive()` at the point where you would display interactive UI.

Non-interactive mode does not mean “answer yes.” A command that needs
confirmation either requires its explicit confirmation flag or returns an
actionable error. Use the command-specific flag only when the job is intended
to perform the operation:

```bash
glassbox --non-interactive cache clear --force
glassbox --non-interactive doctor --fix --yes
glassbox --non-interactive update --yes
```

`--force` authorizes cache deletion, `doctor --fix --yes` skips fix prompts,
and `update --yes` proceeds with the update. Without those flags, non-interactive
cache, doctor, and update confirmation paths fail rather than silently
approving the action. See [cache eviction](cache-eviction.md) for cache-specific
behavior.

Color output follows the same pattern. Call `visualizer.SetNoColor(true)` only if you add a new color subsystem; all built-in helpers already check the global flag.

---

## 8. Security considerations

Before writing a new command, check:

- Does it accept file paths? Validate with `validateFilePath` and reject null bytes.
- Does it accept string values that feed shell or OS calls? Quote and escape; prefer argument arrays over string concatenation.
- Does it log or print anything that could contain tokens, keys, or passwords? Apply `security.RedactSensitiveFlags` to any flag map before logging.
- Does it write to a file the user specifies? Validate the parent directory exists and the target is not a directory (`ValidateDebugOutputPaths` is the model).
- Does it call external processes? Pass `ctx` so SIGINT propagates cleanly.
- Does it surface RPC URLs? Strip `user:password@` userinfo before displaying (see `config show` for the pattern).

The `--audit-log` path is for audit trail output, not for debug logs. Do not add new uses of raw `os.Stdout` for sensitive data.

---

## 9. Tests

Three layers of tests are required for every new command.

### Unit tests (`internal/cmd/mycommand_test.go`)

Test at minimum:
- Validation: one test per distinct validation rule in `PreRunE`.
- Output: one test for text output, one for JSON output. Capture `cmd.OutOrStdout()` by calling `cmd.SetOut(&buf)`.
- Error code: verify the returned error is an `*ErstError` with the expected `Code`.

```go
func TestMyCommand_ValidationError(t *testing.T) {
    cmd := rootCmd
    buf := &bytes.Buffer{}
    cmd.SetOut(buf)
    cmd.SetArgs([]string{"mycommand", "--format", "yaml"}) // invalid
    err := cmd.Execute()
    if err == nil {
        t.Fatal("expected error for invalid --format")
    }
    var erstErr *errors.ErstError
    if !errors.As(err, &erstErr) || erstErr.Code != errors.ErstValidationFailed {
        t.Errorf("expected VALIDATION_FAILED, got %v", err)
    }
}
```

### Integration tests (`integration/`)

Add a case to `TestErstBinaryFullCLISurface` for `mycommand --help`. Add a dedicated function for behavioral contracts:

```go
func TestMyCommand_ExitCodeOnValidationFailure(t *testing.T) {
    _, stderr, err := runErst(t, "mycommand", "--format", "yaml")
    if exitCode(err) != 1 { // ExitUserError
        t.Errorf("expected exit 1 for validation failure, got %d", exitCode(err))
    }
    assertNotContains(t, "stderr", stderr, "panic")
    assertNotContains(t, "stderr", stderr, "goroutine")
    assertContains(t, "stderr", stderr, "yaml")
}
```

### Regression tests (if fixing a bug)

Follow `docs/regression-test-guide.md`. Create a fixture in `test/regression/fixtures/<layer>/`, name it `<layer>_<scenario>_issue<N>.<ext>`, and add a `TestRegression_*` test with a `// Failure class:` comment.

---

## 10. Documentation

A new command requires a documentation file at `docs/mycommand.md` before the PR merges. Minimum sections:

1. Synopsis — all invocation forms.
2. Arguments — positional args with validation rules stated explicitly.
3. Flags table — default, description, environment variable (if any).
4. Validation & Dry-Run — what `--dry-run` checks (if applicable), and which mutually exclusive combinations are rejected.
5. Output — text and JSON shapes. Include example JSON envelope for both success and error.
6. Error reference table — map every user-visible failure to its message, following the pattern in `docs/debug-command.md`.
7. Examples — at least one example per distinct mode.
8. Non-interactive / CI notes — what changes in a CI environment.

Add an entry in `docs/stable-error-codes.md` for every new `ErstErrorCode` the command introduces.

Add the command to the surface test in `integration/cli_surface_integration_test.go` with a `--help` case.

---

## 11. Release checklist

Before marking the PR ready for review, confirm:

- [ ] `PreRunE` validates all flags before any I/O.
- [ ] Every error is an `ErstError` with a stable `Code`.
- [ ] A new `ErstErrorCode` (if added) appears in the stable-error-codes catalogue and has an exit-code bucket assignment.
- [ ] `--json` / `--format json` output uses `clioutput.Write`.
- [ ] Output is written to `cmd.OutOrStdout()`, not `os.Stdout`.
- [ ] `cmd.Context()` is passed to every blocking call.
- [ ] No raw hashes, tokens, or keys appear in telemetry attributes.
- [ ] Non-interactive mode is handled (no spinners or prompts without a TTY check).
- [ ] `--no-color` / `NO_COLOR` is respected via the global visualizer flag (no manual ANSI in new code).
- [ ] Unit tests cover validation, success output, and error code.
- [ ] Integration test covers `--help` and at least one error exit code.
- [ ] `docs/mycommand.md` is present with all required sections.
- [ ] New `ErstErrorCode`s are documented in `docs/stable-error-codes.md`.
- [ ] `TestErstBinaryFullCLISurface` includes a `--help` case.
- [ ] `docs/compatibility-matrix.md` updated for every new or changed CLI flag, JSON field, or Go export.
- [ ] A changelog fragment added to `changelog/fragments/` (`make changelog-check` passes).
- [ ] If any surface is deprecated or removed: migration note in `MIGRATION_GUIDE.md` and `breaking = true` in the fragment.

---

## Quick reference: key packages

| Package | Purpose |
|---------|---------|
| `internal/errors` | `ErstError`, `Wrap*` helpers, sentinel errors |
| `internal/cmd/exitcode.go` | Maps error codes to exit codes |
| `internal/cmd/cmd_validation.go` | Shared validation helpers |
| `internal/clioutput` | JSON envelope (`Write`, `WriteError`, `WantsJSON`) |
| `internal/telemetry` | Spans, `RecordCommandUsage`, `Fingerprint` |
| `internal/progress` | `--progress-json` NDJSON emitter |
| `internal/termctx` | Non-interactive detection |
| `internal/visualizer` | Color helpers, `SetNoColor` |
| `internal/config` | `config.Load()`, `config.DefaultConfig()` |
| `internal/testhelpers` | Fixture builders for unit and regression tests |
