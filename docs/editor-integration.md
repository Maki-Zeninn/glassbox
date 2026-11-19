# Editor and Deep-Link Integration

Glassbox connects execution traces, source locations, and debugging sessions
directly to your editor so you do not have to copy paths or hashes manually.
This document covers:

- The `glassbox://trace/` URI scheme for navigating to a specific trace step
- The VS Code extension commands for trace-to-source navigation
- The CLI deep-link dispatch protocol
- Failure handling when a target cannot be resolved

---

## URI scheme

Glassbox extends its existing `glassbox://debug/` scheme with a second host
segment, `trace`, for navigating to a specific step within a loaded trace:

```
glassbox://trace/<txhash>/step/<N>?network=<network>[&file=<path>][&line=<n>][&col=<n>][&view=<mode>]
```

### Components

| Segment / Parameter | Required | Description |
|---|---|---|
| `trace` | yes | Fixed host. Distinguishes trace-step navigation from debug launch. |
| `<txhash>` | yes | 64-character hex transaction hash. |
| `/step/<N>` | yes | Zero-based step index within the execution trace. |
| `network` | yes | One of: `testnet`, `mainnet`, `futurenet`. |
| `file` | no | URL-encoded source file path for direct navigation. |
| `line` | no | 1-based source line number. |
| `col` | no | 1-based column number. |
| `view` | no | Panel to open: `trace`, `source`, `flamegraph`, `events`. |

### Examples

```
# Open step 3 of a testnet trace
glassbox://trace/5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab/step/3?network=testnet

# Open step 3 and navigate to its source location
glassbox://trace/5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab/step/3?network=testnet&file=src%2Flib.rs&line=42

# Open step 3 with the flamegraph panel active
glassbox://trace/5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab/step/3?network=testnet&view=flamegraph
```

### Error messages

| Problem | Error |
|---|---|
| Empty URI | `protocol URI must not be empty` |
| Wrong host | `invalid protocol host "debug": expected "trace" or "debug"` |
| Missing/empty hash | `invalid transaction hash "": must be a 64-character hex string` |
| Missing step | `trace URI is missing required path segment: /step/<N>` |
| Non-numeric step | `invalid step index "abc": must be a non-negative integer` |
| Negative step | `invalid step index "-1": must be a non-negative integer` |
| Missing network | `missing required query parameter: network` |
| Invalid network | `invalid network "staging": must be one of testnet, mainnet, futurenet` |

---

## CLI dispatch

When the OS dispatches a `glassbox://trace/` link (or you invoke it manually),
the `protocol:handle` command parses the URI and re-invokes the appropriate
command:

```bash
# Deep link dispatches to:
glassbox trace --step 3 --network testnet <txhash>

# With a source file hint:
glassbox trace --step 3 --network testnet --source-file src/lib.rs --source-line 42 <txhash>
```

You can also dispatch trace links manually from the command line:

```bash
glassbox protocol:handle "glassbox://trace/<hash>/step/3?network=testnet"
```

---

## VS Code extension commands

The extension registers the following commands for trace navigation. All are
accessible from the Command Palette (`Ctrl+Shift+P` / `Cmd+Shift+P`).

| Command ID | Title | Description |
|---|---|---|
| `glassbox.openTraceStep` | Glassbox: Open Trace Step | Jump to a step by index and navigate to its source location if available. |
| `glassbox.openSourceLocation` | Glassbox: Open Source Location | Open a file at a specific line/column from a trace frame. |
| `glassbox.triggerDebug` | Glassbox: Debug Transaction | Start a debug session for a transaction hash. |
| `glassbox.selectTraceStep` | Glassbox: Select Trace Step | Show the raw JSON for a selected trace step. |
| `Glassbox.nextTraceStep` | Glassbox: Next Trace Step | Advance to the next step and navigate to its source. |
| `Glassbox.prevTraceStep` | Glassbox: Previous Trace Step | Go back one step and navigate to its source. |

### Navigating from a trace step to source

When a `TraceStep` contains a `source_ref` field, the extension resolves it to
a workspace file and opens it at the correct line:

```typescript
// The extension calls glassbox.openSourceLocation internally after step selection.
// You can also invoke it directly from the command palette with a source location.
{
  "file": "src/lib.rs",
  "line": 42,
  "column": 8
}
```

The resolver follows this priority:

1. `file` is an absolute path → open directly.
2. `file` is relative → resolve against the first open workspace folder.
3. `file` is under `target/` or a known build directory → marked as generated;
   a warning is shown and the file is opened only if it exists on disk.
4. `file` cannot be resolved → an information message is shown with the raw
   path and the step JSON is displayed in a side panel as a fallback.

### Handling unresolved targets gracefully

The extension never throws when a source location cannot be resolved. Instead:

- A VS Code information message appears:
  `Glassbox: source not found for step N (src/generated/contract.rs) — showing step JSON instead`
- The step's JSON is opened in a read-only side panel so you can inspect the
  raw data.
- Navigation continues: next/prev step commands keep working.

---

## Workflow: trace event → source file

### From the VS Code extension

1. Load a trace via **Glassbox: Debug Transaction** or the tree view.
2. Click any step in the **Glassbox Traces** panel.
3. If the step has a `source_ref`, the source file opens beside the trace tree
   at the correct line. Generated or external frames are annotated with a
   `[generated]` or `[external]` label in the step JSON panel.
4. Use `Glassbox.nextTraceStep` / `Glassbox.prevTraceStep` to walk through
   steps with the source pane following along.

### From a terminal deep link

```bash
# Open the VS Code extension and jump to step 5 of a trace
glassbox://trace/5c0a1234.../step/5?network=testnet&view=source

# Equivalent CLI command (if the extension server is not running)
glassbox trace --print --step 5 --network testnet 5c0a1234...
```

### From CI or external tools

External tools (CI dashboards, Stellar Explorer) can generate a deep link from
a transaction hash and step index:

```javascript
// JavaScript example — construct a navigation deep link
function traceStepLink(txHash, stepIndex, network = 'testnet') {
  return `glassbox://trace/${txHash}/step/${stepIndex}?network=${network}`;
}
```

---

## Source map integration

The VS Code extension uses `sourceMap.ts` to convert raw CLI frame data into
VS Code `Position` and `Uri` objects:

```typescript
import { resolveFrameLocation } from './sourceMap';

// Called when a step is selected in the tree view
const location = resolveFrameLocation(step.source_ref, workspaceRoot);
if (location) {
  const docUri = vscode.Uri.parse(location.uri);
  const pos = new vscode.Position(location.line, location.column);
  await vscode.window.showTextDocument(docUri, {
    selection: new vscode.Range(pos, pos),
    viewColumn: vscode.ViewColumn.Beside,
  });
}
```

### Generated and external frames

Frames marked `generated: true` (from `target/` build directories) or
`external: true` (from outside the workspace) are resolved but displayed with
an additional label so developers immediately see the origin without having to
inspect paths manually:

```
src/lib.rs:42          → user source     (navigates directly)
target/.../foo.rs:10   → [generated]     (opens with warning if file exists)
~/.cargo/registry/...  → [external]      → (shows info message, opens step JSON)
```

See `docs/source-origin-classification.md` for how origin classes are assigned.

---

## Extension activation requirements

The extension activates when:

- The workspace contains a `glassbox.toml` or `glassbox-build-manifest.json`
  file, **or**
- A `glassbox://` deep link is dispatched to VS Code, **or**
- The user runs any `Glassbox:` command from the Command Palette.

The extension connects to `127.0.0.1:8080` (the local Glassbox daemon). If
the daemon is not running, commands that require a live trace display a
connection error with a fix hint:

```
Glassbox: Cannot connect to local daemon (127.0.0.1:8080).
Start the daemon with: glassbox serve
Or debug a transaction directly: glassbox debug <txhash>
```

---

## Related files

| File | Purpose |
|---|---|
| `vscode-extension/src/extension.ts` | Extension entry point; command registrations |
| `vscode-extension/src/sourceMap.ts` | Frame → VS Code URI/Position conversion |
| `vscode-extension/src/traceTreeView.ts` | Trace step tree view data provider |
| `internal/protocolreg/uri.go` | `ParseDebugURI` and `ParseTraceStepURI` parsers |
| `internal/cmd/protocol.go` | `protocol:handle` dispatch logic |
| `docs/adr/deeplink-parameters.md` | Deep link parameter semantics ADR |
