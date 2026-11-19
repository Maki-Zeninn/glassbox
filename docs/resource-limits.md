# Resource Limits for HTTP Response Fetching

## Overview

Large envelopes, ledger entries, or trace payloads can exhaust memory when fetched from a misbehaving endpoint. This module implements explicit and adjustable resource limits for HTTP response fetching to prevent memory exhaustion.

## Problem

When interacting with misbehaving or malicious RPC endpoints, oversized responses can:
- Exhaust available memory
- Cause application crashes
- Enable denial-of-service attacks
- Mask the root cause of failures with generic errors

## Solution

Implement per-response and aggregate fetch limits with:
- Bounded readers that enforce limits before buffering data
- Clear failure codes for different limit violations
- Configuration and CLI overrides for legitimate large transactions
- Diagnostics showing observed vs configured sizes
- Machine-readable error output that remains small

## Architecture

### Bounded Readers

The `BoundedReader` wraps an `io.Reader` and enforces a byte limit:
- Clamps read operations to never exceed limit+1 bytes
- Returns `PayloadTooLargeError` as soon as the limit is exceeded
- Tracks total bytes consumed for diagnostics

### Aggregate Tracking

The `AggregateTracker` tracks total bytes across all requests:
- Thread-safe tracking of cumulative bytes fetched
- Returns `AggregateLimitExceededError` when aggregate limit is exceeded
- Provides statistics (total bytes, request count, limit)

### Error Types

#### PayloadTooLargeError

Returned when a single response exceeds the per-response limit:
```go
type PayloadTooLargeError struct {
    Code        string // "ERR_PAYLOAD_TOO_LARGE"
    Operation  string // RPC operation name
    ReadBytes  int64  // Bytes actually read
    Limit      int64  // Configured limit
    Configured int64  // Original configured limit
}
```

#### AggregateLimitExceededError

Returned when total bytes across all requests exceed the aggregate limit:
```go
type AggregateLimitExceededError struct {
    Code             string // "ERR_AGGREGATE_LIMIT_EXCEEDED"
    TotalBytesRead  int64  // Total bytes across all requests
    AggregateLimit  int64  // Configured aggregate limit
    ConfiguredLimit int64  // Original configured limit
    RequestCount    int    // Number of requests made
}
```

## Default Limits

| Limit | Constant | Default | Type | Flag to raise it |
|-------|----------|---------|------|-----------------|
| Per-response payload | `DefaultResponsePayloadLimit` | 32 MiB | Hard cap | `--rpc-response-limit` |
| Aggregate fetch (session) | `DefaultAggregateFetchLimit` | 512 MiB | Hard cap | `--rpc-aggregate-limit` |
| Transaction envelope | `DefaultEnvelopeLimit` | 1 MiB | Hard cap | *(programmatic only)* |
| Ledger entries response | `DefaultLedgerEntriesLimit` | 1 MiB | Hard cap | *(programmatic only)* |

### Hard caps vs warning thresholds

Every limit in this table is a **hard cap**: when it is reached the request
is rejected immediately with a typed error and no partial data is returned.
There are currently no soft warning thresholds (values that log a warning but
continue).  If you need to understand how close a workload is to a limit
before it is hit, query the aggregate tracker stats programmatically:

```go
totalBytes, requestCount, limit := client.aggregateTracker.GetStats()
// Compare totalBytes against limit before issuing the next request.
```

## Identifying which limit caused a failure

Use the error symptom in the table below to map an observed failure to its
setting name, default value, and unit.

| Symptom | Error code | Setting constant | Default | Unit | Flag |
|---------|-----------|-----------------|---------|------|------|
| `rpc: <op> response exceeded size limit` | `ERR_PAYLOAD_TOO_LARGE` | `DefaultResponsePayloadLimit` | 32 | MiB | `--rpc-response-limit` |
| `rpc: aggregate fetch limit exceeded` | `ERR_AGGREGATE_LIMIT_EXCEEDED` | `DefaultAggregateFetchLimit` | 512 | MiB | `--rpc-aggregate-limit` |
| `rpc: envelope exceeded size limit` | `ERR_ENVELOPE_TOO_LARGE` | `DefaultEnvelopeLimit` | 1 | MiB | *(programmatic)* |
| `rpc: ledger entries response exceeded size limit` | `ERR_LEDGER_ENTRIES_TOO_LARGE` | `DefaultLedgerEntriesLimit` | 1 | MiB | *(programmatic)* |

Check the `code` field in the JSON error output first — it is the most
reliable discriminator when multiple limits are configured.

## Per-category examples

Each example below changes **only the limit relevant to that category**.
Run these against a real or mock RPC endpoint.

### Per-response limit — large `getLedgerEntries` response

Symptom: a contract with a large state footprint causes
`ERR_PAYLOAD_TOO_LARGE` on `getLedgerEntries`.

```bash
# Raise the per-response limit to 64 MiB (leave aggregate at its default).
glassbox debug --rpc-response-limit 67108864 <tx-hash>
```

Programmatic equivalent:

```go
client, _ := rpc.NewClient(
    rpc.WithNetwork(rpc.Testnet),
    rpc.WithResponsePayloadLimit(64 * 1024 * 1024), // 64 MiB
)
```

### Aggregate limit — long debugging session with many requests

Symptom: after several `debug` or `regression-test` invocations,
`ERR_AGGREGATE_LIMIT_EXCEEDED` fires mid-session.

```bash
# Raise the aggregate limit to 1 GiB (leave per-response at its default).
glassbox debug --rpc-aggregate-limit 1073741824 <tx-hash>
```

Programmatic equivalent:

```go
client, _ := rpc.NewClient(
    rpc.WithNetwork(rpc.Testnet),
    rpc.WithAggregateFetchLimit(1024 * 1024 * 1024), // 1 GiB
)
```

### Envelope limit — unusually large transaction envelope

Symptom: `ERR_ENVELOPE_TOO_LARGE` when submitting a multi-operation
transaction whose XDR encoding exceeds 1 MiB.

This limit is not yet exposed as a CLI flag.  Raise it programmatically:

```go
client, _ := rpc.NewClient(
    rpc.WithNetwork(rpc.Testnet),
    rpc.WithEnvelopeLimit(4 * 1024 * 1024), // 4 MiB
)
```

### Ledger entries limit — contract with wide state footprint

Symptom: `ERR_LEDGER_ENTRIES_TOO_LARGE` when the `getLedgerEntries`
response for a contract with many storage slots exceeds 1 MiB.

This limit is not yet exposed as a CLI flag.  Raise it programmatically:

```go
client, _ := rpc.NewClient(
    rpc.WithNetwork(rpc.Testnet),
    rpc.WithLedgerEntriesLimit(8 * 1024 * 1024), // 8 MiB
)
```

## Error Messages

### Per-Response Limit Exceeded

```
rpc: getLedgerEntries response exceeded size limit: read 40.0 MiB (41943040 bytes), limit 32.0 MiB (33554432 bytes) — reduce the request scope (fewer ledger keys, smaller footprint) or raise the limit with --rpc-response-limit
```

### Aggregate Limit Exceeded

```
rpc: aggregate fetch limit exceeded: read 600.0 MiB (629145600 bytes) across 15 requests, limit 512.0 MiB (536870912 bytes) — reduce the total number of operations or raise the limit with --rpc-aggregate-limit
```

## JSON Error Output

Errors are serialized to compact JSON for machine-readable handling:

```json
{
  "code": "ERR_PAYLOAD_TOO_LARGE",
  "operation": "getLedgerEntries",
  "read_bytes": 41943040,
  "limit": 33554432,
  "configured_limit": 33554432
}
```

```json
{
  "code": "ERR_AGGREGATE_LIMIT_EXCEEDED",
  "total_bytes_read": 629145600,
  "aggregate_limit": 536870912,
  "configured_limit": 536870912,
  "request_count": 15
}
```

**Note**: JSON output is intentionally compact (< 500 bytes) and does not include response content.

## Error Codes

| Code | Description |
|------|-------------|
| `ERR_PAYLOAD_TOO_LARGE` | Single response exceeded per-response limit |
| `ERR_AGGREGATE_LIMIT_EXCEEDED` | Total bytes exceeded aggregate limit |
| `ERR_ENVELOPE_TOO_LARGE` | Transaction envelope exceeded envelope limit |
| `ERR_LEDGER_ENTRIES_TOO_LARGE` | Ledger entries response exceeded entries limit |

## Operational Tuning

### When to increase a limit

Increase a specific limit when you have identified it as the cause of a
failure (use the symptom table above to confirm).  Raise only the relevant
limit — leave the others at their defaults so the remaining protections stay
in place.

Legitimate reasons to raise a limit:
- A contract with large on-chain state causes `getLedgerEntries` to exceed the
  per-response limit.
- A long debugging session with many RPC calls approaches the aggregate limit.
- An unusual multi-operation transaction has an envelope larger than 1 MiB.

### Safety guidelines

- **Raise limits selectively.** Change the one limit that matches the error
  code in the failure.  Raising all limits at once removes the protection they
  provide without addressing the underlying scope.
- **A zero value is unlimited.** Passing `0` to `WithResponsePayloadLimit` or
  `--rpc-response-limit 0` disables that limit entirely.  This is appropriate
  only for controlled local testing; never use it against untrusted endpoints.
- **Prefer smaller request scope first.** Before raising a limit, try reducing
  the number of ledger keys in the footprint or splitting a batch into smaller
  chunks.  A smaller scope is faster and safer than a higher limit.


## Implementation Details

### BoundedReader Behavior

- Reads are clamped to never exceed `limit + 1` bytes
- Error is returned immediately when limit is exceeded
- `BytesRead()` returns total bytes consumed (may exceed limit by 1)
- Zero limit is treated as unlimited (equivalent to `io.ReadAll`)

### AggregateTracker Behavior

- Thread-safe via mutex locking
- Records each request's byte count
- Checks aggregate limit after each request
- Provides real-time statistics via `GetStats()`

### Integration with HTTP Layer

The bounded reader is integrated into the HTTP response body reading:

```go
func (c *Client) readResponseBody(body io.Reader, op string) ([]byte, error) {
    limit := c.ResponsePayloadLimit
    if limit <= 0 {
        limit = DefaultResponsePayloadLimit
    }
    
    var tracker *AggregateTracker
    if c.aggregateTracker != nil {
        tracker = c.aggregateTracker
    }
    
    return ReadBoundedWithTracker(body, limit, op, tracker)
}
```

## Testing

### Test Coverage

Tests cover:
- Exact boundary behavior (responses at exactly the limit)
- One-byte-over scenarios (responses exceeding limit by 1 byte)
- Compressed responses (gzip-compressed payloads)
- Multiple requests with aggregate tracking
- Concurrent access safety
- JSON serialization and size limits
- Error message formatting
- Default limit values

### Running Tests

```bash
# Run all bounded reader tests
go test ./internal/rpc -run BoundedReader

# Run aggregate tracking tests
go test ./internal/rpc -run Aggregate

# Run compression tests
go test ./internal/rpc -run Compressed
```

## Performance Considerations

- **Memory**: Bounded readers add minimal overhead (single allocation for limit tracking)
- **CPU**: Hashing and comparison are O(1) per read operation
- **Locking**: Aggregate tracker uses mutex for thread safety (negligible for typical workloads)
- **Network**: Limits are enforced before full response buffering (no unnecessary data transfer)

## Security Considerations

### Memory Exhaustion Prevention

- Limits are enforced before data is fully buffered
- Bounded readers clamp individual read operations
- Aggregate tracking prevents cumulative memory exhaustion

### Response Content Exclusion

- Error messages do not include response content
- JSON output is limited to metadata (byte counts, limits)
- Remediation hints guide users without exposing sensitive data

### Denial-of-Service Mitigation

- Per-response limits prevent single large responses
- Aggregate limits prevent many small responses
- Limits are applied before buffering (early rejection)

## Troubleshooting

### Common Issues

**Issue**: Legitimate large transaction is rejected
- **Solution**: Increase per-response limit with `--rpc-response-limit`

**Issue**: Aggregate limit exceeded during batch operations
- **Solution**: Increase aggregate limit with `--rpc-aggregate-limit` or reduce batch size

**Issue**: Limits are too restrictive for specific use case
- **Solution**: Adjust limits via CLI flags or programmatic configuration

**Issue**: Unclear which limit was exceeded
- **Solution**: Check error code and message for specific limit type

### Diagnostics

Query aggregate statistics:

```go
totalBytes, requestCount, limit := client.aggregateTracker.GetStats()
fmt.Printf("Total: %d bytes across %d requests (limit: %d)", 
    totalBytes, requestCount, limit)
```

## Future Enhancements

Potential improvements:
- Add per-operation specific limits (e.g., lower limit for getHealth)
- Implement adaptive limits based on historical usage
- Add metrics/telemetry for limit violations
- Provide UI integration for limit configuration
- Add warnings when approaching aggregate limit