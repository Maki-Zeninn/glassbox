# LSP diagnostics

Glassbox can publish simulation results to a connected language client as
`textDocument/publishDiagnostics`, so a failure shows up as an inline editor
annotation on the Rust line responsible for it rather than as a line of terminal
output the developer has to map back by hand.

## What is published

| Finding | Severity | Code |
| ------- | -------- | ---- |
| WASM trap | `Error` | `glassbox.trap` |
| CPU cost above the threshold | `Warning` | `glassbox.resource-cost` |
| Deprecated host function call | `Hint` | `glassbox.deprecated-host-function` |

Every diagnostic is tagged `source: "glassbox"` so it is distinguishable from
rustc and clippy output.

The trap message combines the simulation error, the raw trap message and the
error code. The resource warning is annotated with the observed instruction
count, because "this is expensive" is only actionable next to the number.

## Library use

```go
// Pure translation — no socket needed, safe to unit test.
diags := lsp.DiagnosticsFromSimulation(resp, mappings, findings, lsp.Options{
    RootDir:               "/path/to/workspace",
    ResourceCostThreshold: 1_000_000,
})

// Publishing, bounded by a timeout.
publisher := lsp.NewDiagnosticsPublisher(client, lsp.Options{Timeout: 5 * time.Second})
err := publisher.Publish(ctx, resp, mappings, findings)
if errors.Is(err, lsp.ErrNoClient) {
    // No editor listening: carry on.
}
```

## Connecting

`lsp.Connect` speaks the LSP base protocol — `Content-Length: <n>\r\n\r\n`
framing with a JSON body — over either transport, chosen from the address:

| Address | Transport |
| ------- | --------- |
| `127.0.0.1:9000` | TCP |
| `/tmp/glassbox.sock` | Unix socket |

```go
client, err := lsp.Connect(ctx, address, 5*time.Second)
if err != nil {
    return err
}
defer client.Close()
```

`lsp.PublishAndClose` is the whole connect-publish-disconnect cycle in one call.

## Failure is always non-fatal

Diagnostics are a convenience. Every part of the client is written so a missing,
refused or wedged editor cannot delay or fail the command that is publishing:

- `Connect` is bounded by `DefaultTimeout` (5s) and by the caller's context.
- Writes carry an explicit deadline.
- `Publish` returns `ErrNoClient` when there is no connection, rather than
  panicking.
- Notifications are fire-and-forget, so a slow server costs one write.

A caller should treat any error from `Publish` as a warning to surface, not a
reason to fail the run.

## Source locations

Source paths are cleaned, made relative to `Options.RootDir` when they sit
inside it, then converted to `file://` URIs so they match the documents the
editor already has open. LSP lines and characters are zero-based, so a 1-based
source line of 42 becomes line 41; an unknown line clamps to the first line
rather than emitting an out-of-range position.

When the simulation carries no source location, the first mapped location from
the source map is used instead.

## Clearing

`Client.ClearDiagnostics` publishes an empty diagnostic list per URI, which is
how you tell the editor to drop annotations from a previous run. Publishing a
fresh set does not clear stale ones on files that are no longer reported, so a
long-lived session should clear the previous URIs first.
