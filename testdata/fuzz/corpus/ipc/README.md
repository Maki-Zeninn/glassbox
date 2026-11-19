# IPC Message Fuzz Corpus

Seed corpus for the IPC fuzz targets in `internal/ipc/ipc_fuzz_test.go`.

## Targets

- `FuzzUnmarshalSimulationRequest` — JSON simulation request parsing
- `FuzzUnmarshalSimulationResponse` — JSON simulation response parsing
- `FuzzUnmarshalHandshakeRequest` — JSON handshake request parsing
- `FuzzIPCErrorClassification` — error code and message classification

## Coverage targets

- Well-formed JSON for each message type
- Empty / null / non-object inputs
- Overlong field values
- Deeply nested JSON (stack-overflow guard)
- Error codes with ambiguous message heuristics

## Adding regression inputs

Copy minimised crash inputs here as `regression_<issue>.bin`.
