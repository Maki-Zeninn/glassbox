# Regression Test Guide

This guide explains how to add a structured regression test when you file or
fix a bug in Glassbox. Following this workflow ensures every regression is:

- **reproducible** — a minimal fixture captures the exact failure input
- **attributable** — a comment links the test to the original issue or PR
- **categorised** — the failure class is named, not just "it errored"
- **self-contained** — no live networks, real keys, or secrets involved

---

## Quick start (5-step recipe)

```
1. File (or find) a GitHub issue that describes the bug.
2. Create a fixture file in test/regression/fixtures/<layer>/.
3. Copy internal/cmd/regression_example_test.go as a starting point.
4. Write the test: Arrange → Act → Assert → name the failure class.
5. Add the fixture filename and issue number to the issue description.
```

That is it. The sections below explain each step in detail.

---

## 1. File a GitHub issue

Every regression test must trace back to an issue. If one does not exist,
open one first. Use this section in the issue body:

```markdown
## Regression fixture

- **Fixture directory**: `test/regression/fixtures/<layer>/`
- **Suggested filename**: `<layer>_<scenario>_issue<N>.<ext>`
- **Failure class**: <one-line description, e.g. "CPU budget exhaustion silently ignored">
- **Canonical input**: <paste minimal JSON or command here>
```

The fix PR must include both the fixture file and a test referencing it.
Reviewers will reject a bug fix that lacks a regression test.

---

## 2. Choose the right layer and fixture directory

| Layer | Directory | What goes here |
|-------|-----------|----------------|
| RPC | `test/regression/fixtures/rpc/` | Stellar RPC response JSON stubs |
| Replay | `test/regression/fixtures/replay/` | Snapshot registries and ledger-state maps |
| Trace | `test/regression/fixtures/trace/` | Serialised ExecutionTrace JSON |
| Source map | `test/regression/fixtures/sourcemap/` | Minimal WASM stubs and alias JSON |
| Session | `test/regression/fixtures/session/` | Session record JSON and SQLite dumps |
| Audit | `test/regression/fixtures/audit/` | Payloads, signed logs, TEST-ONLY keys |
| CLI | `test/regression/fixtures/cli/` | Expected output fragments and env files |

Each directory has its own `README.md` with layer-specific naming rules. Read
it before creating your fixture.

---

## 3. Create a minimal fixture

### Naming

```
<layer>_<scenario-slug>_<issue-or-pr-slug>.<ext>
```

Examples:

```
rpc_gettransaction_notfound_issue150.json
trace_empty_steps_pr319.trace.json
session_missing_txhash_issue230.session.json
audit_empty_payload_rejected.payload.json
```

### Canonical stub values

Always use these stubs unless the test explicitly exercises field validation:

| Field | Value |
|-------|-------|
| Transaction hash | `5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab` |
| Envelope XDR | `AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=` |
| Network | `testnet` |
| Timestamp | `2026-01-01T00:00:00Z` |
| Session ID prefix | `sess_test_` |

These constants are also available as Go package-level identifiers in
`internal/testhelpers`:

```go
testhelpers.CanonicalTxHash
testhelpers.CanonicalNetwork
testhelpers.CanonicalEnvelopeXDR
testhelpers.CanonicalTimestamp
```

### Minimal reproduction rule

Include **only the fields required to hit the code path under test**. Omit or
zero-value every other field. A fixture that reproduces the failure in 5 fields
is better than one that copies a full real-world response.

The guide below shows a before/after pair for an RPC fixture.  The same
principle applies to every layer.

#### Before — copied from a real response

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "hash":           "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
    "status":         "SUCCESS",
    "ledger":         50023190,
    "createdAt":      "1751234567",
    "feeCharged":     "100",
    "applicationOrder": 1,
    "feeBump":        false,
    "envelopeXdr":    "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
    "resultXdr":      "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
    "resultMetaXdr":  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
    "operationResults": [],
    "txHash":         "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
  }
}
```

That fixture has 11 fields.  The bug under test is that a missing `status`
field triggers a panic instead of a validation error.  Only two fields are
needed to reach that code path:

#### After — trimmed to the fields that matter

```json
{
  "_comment": "Reproduces issue #NNN: missing status field causes panic instead of BoundaryError.",
  "_failure_class": "panic-on-bad-input",
  "_issue": "NNN",
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
  }
}
```

The irrelevant fields (`ledger`, `createdAt`, `feeCharged`, etc.) are dropped
entirely.  The only remaining fields are those that exercise the `status`
validation path.

**The guiding question is relevance, not size.**  Ask: "if I remove this
field, does the test still hit the same code path?"  Remove it if yes.  Keep
it if no, or if it provides context that makes the fixture self-documenting.

### When larger fixtures are justified

Binary fixtures (e.g. minimal WASM stubs in `fixtures/sourcemap/`) are
justified when the code under test is a binary parser and a smaller input
would not trigger the same parser branch.  Some situations that legitimately
produce larger fixtures:

- **Binary format coverage** — a WASM section header that triggers a specific
  opcode decoder must contain all the preceding header bytes to reach that
  decoder.  Trimming them would produce an invalid input that the parser
  rejects earlier.

- **Multi-part schemas** — an XDR-encoded footprint that exercises overlap
  detection must contain at least two ledger keys; a single-key fixture would
  not reach the comparison code.

- **Signed audit logs** — a signed log fixture must retain the full canonical
  envelope because any structural change invalidates the signature.  In that
  case, document the regeneration command in `_regenerate` and keep the
  original signature intact.

For these cases, add a `_size_justification` comment to the fixture:

```json
{
  "_size_justification": "Binary WASM fixture: all preceding section headers are required to reach the custom-section decoder under test.",
  ...
}
```

Binary fixtures in `fixtures/sourcemap/` should also include a companion
`.md` file explaining what contract behaviour the WASM encodes, because binary
content is opaque in code review.

### Secret avoidance

Fixtures must **never** contain:

- Real private keys, PKCS#8 PEM blobs, HSM PINs, or KMS key ARNs
- Real transaction hashes from mainnet or testnet
- Real contract IDs from deployed contracts
- Bearer tokens, API keys, or RPC credentials

Keys in `test/regression/fixtures/audit/` must use the `testonly_` prefix in
both the filename and a comment at the top of the file.

---

## 4. Write the test

### Copyable template

The snippet below is a self-contained, one-layer template you can copy and
fill in.  It is intentionally minimal so it compiles from day one, even before
the fixture is wired to a real code path.

**Fixture file** (`test/regression/fixtures/rpc/rpc_template_issueNNN.json`):

```json
{
  "_comment": "Template: replace this comment with the original failure description. (Closes #NNN.)",
  "_failure_class": "<failure-class-slug>",
  "_issue": "NNN",
  "_template": true,
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32600,
    "message": "<describe the error the server returns>"
  }
}
```

Mark the fixture with `"_template": true` until an actual test consumes it.
Remove the key (or set it to `false`) when the real test is wired up.

**Test file** (`internal/cmd/rpc_template_regression_test.go`):

```go
// TestRegression_RPC_Template_IssueNNN verifies that <describe expected behaviour>.
//
// Original failure: <describe what the user saw>.  (Closes #NNN.)
func TestRegression_RPC_Template_IssueNNN(t *testing.T) {
    // Failure class: <failure-class-slug>

    // 1. Arrange — build the minimal fixture.
    //    Replace NotFound() with the builder method that matches your failure.
    fixture := testhelpers.NewRPCFixture().NotFound().Build()

    // 2. Act — exercise the code path under test.
    //    Replace this placeholder with the real function call.
    err := fmt.Errorf("transaction not found: %s", testhelpers.CanonicalTxHash)

    // 3. Assert — verify the exact failure class was reproduced.
    if err == nil {
        t.Fatal("<what must be true> must return an error, got nil")
    }
    if !strings.Contains(strings.ToLower(err.Error()), "<expected keyword>") {
        t.Errorf("error should mention '<expected keyword>', got: %q", err.Error())
    }
    _ = fixture // suppress unused-variable error until Step 2 is wired
}
```

**Naming**: follow `TestRegression_<Layer>_<Scenario>_<Observation>` exactly.
Every test in this repository that starts with `TestRegression_` is picked up
by the `go-regression` CI job.

**Issue link**: the `// (Closes #NNN.)` comment in the test and the `_issue`
field in the fixture must agree.  CI validates that no fixture has an empty
`_issue` field once `_template` is removed.

### File location

Unit and package-level regression tests live in the same package as the code
under test, named `*_regression_test.go` or appended to an existing
`regression_*_test.go` file.

Integration tests that require a built binary live in `integration/`.

### Template

Use `internal/cmd/regression_example_test.go` as the canonical starting point.
Copy the file, rename the functions, and fill in the details.

Every test must follow the **Arrange → Act → Assert** structure and carry a
`// Failure class:` comment explaining the category:

```go
// TestRegression_<Layer>_<Scenario>_<Observation> verifies that ...
//
// Original failure: <what the user saw / what was broken>. (Closes #<N>.)
func TestRegression_<Layer>_<Scenario>_<Observation>(t *testing.T) {
    // Failure class: <one-line category, e.g. "budget exhaustion silently swallowed">

    // 1. Arrange
    fixture := testhelpers.New<Layer>Fixture().<Options>().Build()

    // 2. Act
    result, err := SomeFunctionUnderTest(fixture)

    // 3. Assert
    if err == nil {
        t.Fatal("expected an error, got nil")
    }
    if !strings.Contains(err.Error(), "<expected substring>") {
        t.Errorf("error should mention '<expected substring>', got: %q", err.Error())
    }
}
```

### Using the testhelpers builders

All layer builders live in `internal/testhelpers/`. Import the package and use
fluent chaining:

```go
import "github.com/dotandev/glassbox/internal/testhelpers"

// RPC fixture
resp := testhelpers.NewRPCFixture().NotFound().Build()

// Simulator request
req := testhelpers.NewSimRequestFixture().
    WithEnvelope(myXDR).
    WithLedgerEntry("key1", "value1").
    Build()

// Simulator response with budget exhaustion
failResp := testhelpers.NewSimResponseFixture().
    WithError("ExceededInstructions").
    WithBudgetExhausted().
    Build()

// Trace with two events
tr := testhelpers.NewTraceFixture().
    AddContractCallEvent("CTEST...", "transfer").
    AddErrorEvent("CTEST...", "panic: out of resources").
    Build()

// Session missing TxHash (triggers validation failure)
sess := testhelpers.NewSessionFixture().MissingTxHash().Build()

// Audit payload
payload := testhelpers.NewAuditPayloadFixture().BuildString()
```

### Asserting on the failure class

Do not assert only that an error was returned. Assert that the **specific
failure class** was reproduced:

```go
// Bad — too generic
if err == nil {
    t.Fatal("expected error")
}

// Good — names the category
if err == nil {
    t.Fatal("CPU budget exhaustion must return an error, not success")
}
if !strings.Contains(strings.ToLower(err.Error()), "budget") {
    t.Errorf("error should mention 'budget' for CPU exhaustion, got: %q", err.Error())
}
```

### Running only regression tests

```bash
# Run all regression tests in internal/cmd
go test -run TestRegression ./internal/cmd/...

# Run a specific test
go test -run TestRegression_Session_MissingTxHash ./internal/cmd/...

# Run all integration regression tests
go test -run TestRegression ./integration/...
```

---

## 5. Update the issue

Once the PR is merged, add the fixture path and test name to the issue:

```
## Regression (fixed in PR #<N>)

- **Test**: `TestRegression_<Layer>_<Scenario>_<Observation>` in `internal/cmd/regression_example_test.go`
- **Fixture**: `test/regression/fixtures/<layer>/<filename>`
```

---

## Failure classes

Use one of the following standard failure-class names in the `// Failure class:`
comment. If none fits, add a new one and describe it here.

| Class | Description |
|-------|-------------|
| `rpc-not-found-masquerade` | NOT_FOUND presented as a connectivity error |
| `rpc-error-normalization` | RPC error envelope parsed/normalised incorrectly (wrong code, leaked body, wrong category) |
| `budget-exhaustion-silent` | CPU/memory exhaustion swallowed without a diagnostic |
| `empty-trace-silent-export` | Zero-step trace written to disk without an error |
| `late-validation` | Flag/input validated after an expensive operation instead of at startup |
| `missing-field-raw-error` | Required field absent but returned as a DB/marshal error instead of a user message |
| `empty-payload-signed` | Empty or blank input passed to signing without rejection |
| `stale-command-reference` | CLI example in docs uses a removed or renamed command/flag |
| `panic-on-bad-input` | Any `panic` reached from user-supplied input |
| `missing-remediation-hint` | Error returned with no `Fix:` or `Hint:` guidance |

---

## Checklist for reviewers

When reviewing a PR that fixes a bug, verify:

- [ ] A `TestRegression_*` test is present that reproduces the original failure
- [ ] The test has a `// Failure class:` comment
- [ ] The fixture file is in `test/regression/fixtures/<layer>/` and follows the naming convention
- [ ] No real secrets, hashes, or credentials appear in fixtures
- [ ] The test asserts on the failure category, not just "error != nil"
- [ ] The issue body has been updated with the fixture path and test name

---

## Related files

| File | Purpose |
|------|---------|
| `internal/cmd/regression_example_test.go` | Canonical template with one example per layer |
| `internal/testhelpers/` | Fixture builder package |
| `docs/printer-golden-tests.md` | Golden tests comparing trace printers across output modes |
| `test/regression/FIXTURES.md` | Naming rules, stub values, secret avoidance |
| `test/regression/fixtures/<layer>/README.md` | Layer-specific rules |
| `scripts/check-readme-commands.sh` | Detects stale command references in README |

---

## Complete worked examples

The three sections below show a passing end-to-end regression test for each
layer — RPC, replay, and CLI. Each example:

- Links to an imaginary issue number.
- Uses only canonical stub values from `internal/testhelpers`.
- Names the failure class in a `// Failure class:` comment.
- Runs without live services or secrets.

Copy whichever example is closest to your bug and adjust the fixture and
assertion to match the actual failure.

---

### Example A — RPC: `NOT_FOUND` masked as a connectivity error

**Issue:** `#150` — `glassbox debug` reports "connection refused" when a
transaction hash does not exist on the network, instead of "transaction not
found".

**Fixture file:** `test/regression/fixtures/rpc/rpc_gettransaction_notfound_issue150.json`

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32600,
    "message": "Transaction not found"
  }
}
```

**Test file:** `internal/cmd/regression_example_test.go`

```go
// TestRegression_RPC_NotFound_MasqueradesAsConnectivity verifies that an RPC
// NOT_FOUND response produces a TRANSACTION_NOT_FOUND error, not a generic
// connectivity error.
//
// Original failure: debug reported "connection refused" for missing txhash. (Closes #150.)
func TestRegression_RPC_NotFound_MasqueradesAsConnectivity(t *testing.T) {
    // Failure class: rpc-not-found-masquerade

    // 1. Arrange
    resp := testhelpers.NewRPCFixture().NotFound().Build()
    mock := testhelpers.NewMockRPCServer(t, resp)
    defer mock.Close()

    // 2. Act
    _, err := debugWithRPC(t, mock.URL, testhelpers.CanonicalTxHash, testhelpers.CanonicalNetwork)

    // 3. Assert
    if err == nil {
        t.Fatal("expected TRANSACTION_NOT_FOUND error, got nil")
    }
    if !strings.Contains(strings.ToLower(err.Error()), "not found") {
        t.Errorf("error must mention 'not found', got: %q", err.Error())
    }
    if strings.Contains(strings.ToLower(err.Error()), "connection refused") {
        t.Errorf("error must not claim 'connection refused' for a NOT_FOUND response, got: %q", err.Error())
    }
}
```

**Run it:**

```bash
go test -run TestRegression_RPC_NotFound ./internal/cmd/...
```

**CI job:** `go-regression` in `.github/workflows/ci-test.yml`.

---

### Example B — Replay: budget exhaustion swallowed silently

**Issue:** `#319` — `glassbox debug --load-snapshots` completes with exit 0
when the replayed transaction hits the CPU budget limit. No error, no warning.

**Fixture file:** `test/regression/fixtures/replay/replay_budget_exhausted_issue319.json`

```json
{
  "schema_version": 1,
  "glassbox_version": "0.0.0-test",
  "created_at": "2026-01-01T00:00:00Z",
  "tx_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "network": "testnet",
  "entries": [
    {
      "timestamp": "2026-01-01T00:00:00Z",
      "content_hash": "e3b0c44298fc1c149afbf4c8996fb924",
      "snapshot": {
        "ledger_entries": {},
        "ledger_sequence": 1000
      }
    }
  ]
}
```

**Test file:** `internal/cmd/regression_example_test.go`

```go
// TestRegression_Replay_BudgetExhaustion_SilentlySwallowed verifies that a
// replayed transaction that exhausts CPU budget returns an error instead of
// silently succeeding.
//
// Original failure: --load-snapshots returned exit 0 on budget exhaustion. (Closes #319.)
func TestRegression_Replay_BudgetExhaustion_SilentlySwallowed(t *testing.T) {
    // Failure class: budget-exhaustion-silent

    // 1. Arrange
    simResp := testhelpers.NewSimResponseFixture().
        WithError("ExceededInstructions").
        WithBudgetExhausted().
        Build()
    runner := testhelpers.NewMockRunner(t, simResp)

    // 2. Act
    _, err := replayWithRunner(t, runner, testhelpers.CanonicalTxHash)

    // 3. Assert
    if err == nil {
        t.Fatal("CPU budget exhaustion must return an error, not success (exit 0)")
    }
    if !strings.Contains(strings.ToLower(err.Error()), "budget") {
        t.Errorf("error should mention 'budget' for CPU exhaustion, got: %q", err.Error())
    }
}
```

**Run it:**

```bash
go test -run TestRegression_Replay_BudgetExhaustion ./internal/cmd/...
```

---

### Example C — CLI: stale `--format` value not rejected at startup

**Issue:** `#230` — `glassbox debug --format yaml` prints an internal marshal
error after the expensive simulation step instead of rejecting the flag at
startup with a clear message.

**Fixture file:** `test/regression/fixtures/cli/cli_format_yaml_rejected_issue230.env`

```sh
GLASSBOX_NETWORK=testnet
GLASSBOX_RPC_URL=http://127.0.0.1:0
```

**Test file:** `internal/cmd/regression_example_test.go`

```go
// TestRegression_CLI_InvalidFormat_RejectedAtStartup verifies that an
// unsupported --format value is validated before any simulation work begins.
//
// Original failure: --format yaml triggered a marshal error after simulation. (Closes #230.)
func TestRegression_CLI_InvalidFormat_RejectedAtStartup(t *testing.T) {
    // Failure class: late-validation

    ctx := context.Background()
    binaryPath := testhelpers.RequireBinary(t)

    // 1. Arrange
    fixture := testhelpers.NewCLIFixture(binaryPath,
        "debug",
        "--wasm", "test/regression/fixtures/cli/minimal.wasm",
        "--format", "yaml",        // unsupported format
        testhelpers.CanonicalTxHash,
    ).
        WithEnv("GLASSBOX_NETWORK", testhelpers.CanonicalNetwork).
        ExpectFailure()

    // 2. Act + 3. Assert
    stdout, stderr, _ := fixture.Run(t, ctx)
    combined := stdout + stderr

    if !strings.Contains(combined, "yaml") {
        t.Errorf("error should mention the unsupported format 'yaml', got: %q", combined)
    }
    if !strings.Contains(strings.ToLower(combined), "format") {
        t.Errorf("error should mention '--format', got: %q", combined)
    }
    // Must fail before any simulation (no RPC connection attempted)
    if strings.Contains(combined, "Connecting") || strings.Contains(combined, "Simulating") {
        t.Errorf("flag validation must fire before simulation begins, got: %q", combined)
    }
}
```

**Run it:**

```bash
go test -run TestRegression_CLI_InvalidFormat ./integration/...
```

---

## Validating examples and links in CI

Two scripts catch stale examples automatically:

```bash
# Check that every command shown in docs/ exists in the built binary
scripts/check-readme-commands.sh

# Check that every API snapshot is current
scripts/api-snapshot.sh check
```

Both run in the `ci-gate` job. A failing link is a blocking CI failure.

To update after an intentional CLI or API change:

```bash
scripts/api-snapshot.sh generate
go test ./internal/apicompat/... -update
git diff .api-snapshots/ internal/apicompat/testdata/
```

---

## Choosing focused test commands

| Goal | Command |
|------|---------|
| Run every regression test | `go test -run TestRegression ./...` |
| Run one layer | `go test -run TestRegression_RPC ./internal/cmd/...` |
| Run a single test | `go test -run TestRegression_CLI_InvalidFormat ./integration/...` |
| Run with race detector | `go test -race -run TestRegression ./internal/cmd/...` |
| Run integration regressions | `go test -run TestRegression ./integration/...` |

Always use `go test -run TestRegression` (not `-run Test`) so the faster
unit tests and the slower regression tests can be isolated in CI.

---

## Updating changelog and compatibility artifacts

When a regression fix adds a new failure-class or changes a CLI surface:

1. Add a fragment to `changelog/fragments/`:
   ```bash
   cp changelog/fragments/example-cli-flag.toml \
      changelog/fragments/<issue-id>-<slug>.toml
   # Edit: set category, pr, summary, affects, breaking
   ```

2. If the fix changes a JSON output field or exit code, update the affected
   snapshot:
   ```bash
   scripts/api-snapshot.sh generate   # regenerates .api-snapshots/
   git add .api-snapshots/
   ```

3. If the fix changes an exported Go type, regenerate the Go API snapshot:
   ```bash
   go test ./internal/apicompat/... -update
   git add internal/apicompat/testdata/
   ```

4. Add a row to `docs/compatibility-matrix.md` if the fix introduces a new
   stable guarantee (e.g. "exit 2 on invalid --format").
