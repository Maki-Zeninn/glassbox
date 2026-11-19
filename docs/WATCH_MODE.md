# Watch Mode for Debug Sessions

Glassbox provides a watch mode to rapidly iterate on your smart contracts by automatically rerunning the debug session whenever relevant files change.

## Usage

Enable the feature by using the `--watch-files` flag on the debug command:

```bash
glassbox debug <transaction-hash> --watch-files
```

If you have configured a local source directory via `--contract-source`, the watcher will automatically track it for changes:

```bash
glassbox debug <transaction-hash> --contract-source ./contracts/my_contract --watch-files
```

## How It Works

1. **Directories Watched:** By default, it will watch the current working directory (`.`), tracking configuration changes. If `--contract-source` is provided, that directory will also be recursively monitored.
2. **Debouncing:** File system events are debounced by 500ms. This prevents multiple rapid changes (e.g., from an IDE saving all files simultaneously or running a build tool) from triggering multiple redundant debug session reruns.
3. **Execution Loop:** When a change is detected and the debounce period elapses, the watcher transparently tears down the previous run, re-evaluates the inputs, and reruns the entire debug simulation, retaining all other command line flags and context (such as `--network`, `--json`, or OpenTelemetry settings).

## Distinguishing from Network Watching

Glassbox has two distinct forms of watching:

- `--watch-files#: Monitors local source and configuration file changes (this document).
- `--watch`: Polls the Stellar working waiting for a specific transaction hash to finalize on-chain *before* attempting the initial debug run. (Often used right after submission).

You can combine them, though they serve different phases of the lifecycle.

## Troubleshooting Watch Mode

Watch mode has three distinct outcomes that differ from a normal debug run: **pending**, **timeout**, and **cancellation**. The table below maps each symptom to a safe check and a bounded recovery step.

| Symptom | What it means | Safe check | Bounded recovery |
| -------- | ------------- | ---------- | --------------- |
| Watcher stays **pending** after a file save | Event is still inside the 500ms debounce window, or the changed file is outside the watched directories | Confirm the edited path is under `.` or the `--contract-source` directory; wait at least 500ms for the debounce to elapse | Save again inside a watched directory, or restart with `--contract-source ./contracts/my_contract --watch-files` pointing at the correct path |
| Watcher stays **pending** with no file events at all | Watched directory is empty or the watcher was started from the wrong working directory | Run `glassbox debug --help` and confirm the `--watch-files` and `--contract-source` flags are active; check the startup log for the directories being watched | Restart from the project root with an explicit `--contract-source` path rather than relying on the default `.` |
| Watcher reports a **timeout** waiting for the network transaction to finalize | The `--watch` polling phase ran out of its bounded wait before the transaction was found on-chain | Confirm the transaction hash and `--network` value match the submission target; review the command help for the current `--watch` and timeout flags | Retry once with a shorter bounded wait, for example `--watch --watch-timeout 30s --watch-interval 2s`, then stop and inspect the transaction status instead of polling indefinitely |
| Watcher stops with an **interruption** / cancellation message during a rerun | The run was cancelled by a signal (for example Ctrl+C) or by tearing down the previous run when a new file change arrived; this is not a timeout and no result was produced for that run | Verify the interruption was intentional (Ctrl+C or a subsequent save); confirm the watcher process is still running if you expected a rerun | If you meant to stop, nothing else is needed; otherwise restart with `--watch-files` and let the debounce window complete before saving again |

## Bounded Retry Examples

Always bound the network wait so a missing transaction fails fast instead of polling forever:

```bash
glassbox debug <transaction-hash> --watch --watch-timeout 30s --watch-interval 2s
glassbox debug <transaction-hash> --contract-source ./contracts/my_contract --watch-files
glassbox debug <transaction-hash> --contract-source ./contracts/my_contract --watch-files --watch --watch-timeout 30s --watch-interval 2s
```

The `--watch-timeout` and `--watch-interval` flags bound the network wait; they do not change the file watcher's debounce behavior. Always confirm the flag names and defaults against `glassbox debug --help` for your installed version.
