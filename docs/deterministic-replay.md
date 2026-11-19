# Deterministic Replay Implementation

## Overview

This document describes the deterministic replay implementation for the glassbox simulator, which enables reproducible simulation results across invocations, machines, and supported platforms.

## Problem Statement

Replay results must be reproducible across invocations, machines, and supported platforms. Any implicit randomness in simulator state, generated identifiers, timestamps, or ordering can make a failed transaction impossible to compare.

## Implementation

### Seed Provider

The seed provider manages deterministic random number generation:

- **Go**: `internal/deterministic/seed.go`
- **Rust**: `simulator/src/deterministic.rs`

Features:
- Thread-safe seed storage with optional override
- Hex-encoded seed input/output (32 bytes)
- Opt-in deterministic mode (disabled by default for security)
- Global seed provider for cross-component consistency

### Clock Provider

The clock provider manages deterministic time values:

- **Go**: `internal/deterministic/clock.go`
- **Rust**: `simulator/src/deterministic.rs`

Features:
- Fixed time override for deterministic replay
- Time offset from real time
- Clock advancement for simulation steps
- Global clock provider for cross-component consistency

### Integration Points

#### Rust Simulator

Modified files:
- `simulator/src/hsm/mock.rs` - Uses deterministic seed for signing key generation and failure rate simulation
- `simulator/src/hsm/software.rs` - Uses deterministic seed for signing key generation

#### Go CLI

Modified files:
- `internal/cmd/root.go` - Added `--deterministic-seed` and `--deterministic` flags
- `internal/simulator/response.go` - Added `DeterministicSeed` field to `SimulationResponse`
- `internal/types/simulation.go` - Added `DeterministicSeed` field to `SimulationMetadata`

## Usage

### Enabling Deterministic Mode

#### CLI Flags

```bash
# Enable deterministic mode with auto-generated seed
glassbox debug --deterministic <tx-hash>

# Enable deterministic mode with specific seed
glassbox debug --deterministic-seed 0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20 <tx-hash>
```

#### Programmatic (Go)

```go
import "github.com/dotandev/glassbox/internal/deterministic"

// Enable deterministic mode with specific seed
deterministic.SetGlobalSeedFromHex("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
deterministic.EnableDeterministicMode()

// Or generate a random seed
deterministic.GlobalSeed().GenerateSeed()
deterministic.EnableDeterministicMode()
```

#### Programmatic (Rust)

```rust
use glassbox::deterministic::{set_global_seed_from_hex, enable_deterministic_mode};

// Enable deterministic mode with specific seed
set_global_seed_from_hex("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20").unwrap();
enable_deterministic_mode();
```

### Recording Seeds

The effective seed is automatically recorded in:

1. **SimulationResponse** - `deterministic_seed` field in JSON output
2. **SimulationMetadata** - `deterministic_seed` field for session metadata

### Reproducing Results

To reproduce a simulation:

1. Run simulation with deterministic mode enabled
2. Extract the `deterministic_seed` from the response
3. Re-run with the same seed using `--deterministic-seed <seed>`

## Acceptance Criteria

### ✅ Identical Inputs and Seed Produce Byte-Equivalent Normalized Replay Output

The implementation ensures that:
- Same seed produces identical random values across runs
- Fixed clock produces identical timestamps across runs
- HSM signing keys are deterministic when seed is set
- Failure rate simulation is deterministic when seed is set

### ✅ Variable Wall-Clock Fields Are Isolated and Documented

The clock provider isolates wall-clock dependencies:
- `ClockProvider.Now()` returns fixed time when set
- `ClockProvider.Unix()` returns fixed timestamp when set
- Time offsets allow controlled advancement
- All time-dependent code uses the clock provider

### ✅ Changed Seed Is Visible in Metadata and Does Not Alter Unrelated Canonical Fields

The implementation ensures:
- Seed is recorded in `SimulationResponse.DeterministicSeed`
- Seed is recorded in `SimulationMetadata.DeterministicSeed`
- Seed changes only affect random number generation
- Canonical fields (ledger entries, events) remain unchanged unless directly affected by random values

## Testing

### Unit Tests

- **Go**: `internal/deterministic/seed_test.go`, `internal/deterministic/clock_test.go`
- **Rust**: `simulator/src/deterministic.rs` (test module)

### Integration Tests

- **Go**: `internal/deterministic/deterministic_test.go`, `internal/deterministic/integration_test.go`
- **Rust**: `simulator/src/deterministic_test.rs`

### Test Coverage

Tests verify:
- Same seed produces same output
- Different seeds produce different output
- Clock determinism
- Global state consistency
- Seed recording and retrieval
- Disabled mode non-determinism
- Cross-run consistency
- Full replay workflow

## Security Considerations

- Deterministic mode is **opt-in** (disabled by default)
- Production defaults remain secure and non-deterministic where protocol requires
- Seeds are 32 bytes (256 bits) for cryptographic strength
- Seed values are logged in metadata for auditability

## Future Work

Potential enhancements:
- Add deterministic mode to more simulator components
- Implement seeded PRNG for better random distribution
- Add time travel debugging with clock provider
- Cross-language fixture validation with known test vectors
