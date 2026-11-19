# Architecture Data-Flow Diagram

This document describes the end-to-end data flow through Glassbox, from user
input to final output. It identifies ownership, component boundaries, and
failure propagation paths.

## High-level data flow

```mermaid
flowchart TB
    subgraph Input["Input Layer"]
        CLI["glassbox CLI\n(Go / Cobra)"]
        URI["glassbox:// URI\n(Deep Link)"]
        TS["TypeScript CLI\n(Node / Commander)"]
    end

    subgraph Config["Configuration"]
        TOML[".glassbox.toml\n(Config)"]
        ENV["Environment\nVariables"]
        Flags["CLI Flags"]
    end

    subgraph Discovery["Source Discovery"]
        Cache["Local Cache\n(~/.Glassbox/cache)"]
        Registry["stellar.expert\nRegistry"]
        GitHub["GitHub\nRetriever"]
        DWARF["DWARF Parser\n(Go + Rust)"]
        Alias["Source Alias\nMapping"]
    end

    subgraph RPC["RPC Layer"]
        Client["RPC Client\n(Go)"]
        Failover["Adaptive Failover\n(Weighted / Sticky)"]
        Endpoint1["RPC Endpoint 1"]
        Endpoint2["RPC Endpoint 2"]
        EndpointN["RPC Endpoint N"]
    end

    subgraph Snapshot["Snapshot System"]
        Fetcher["Ledger Fetcher"]
        Store["Snapshot Store\n(SQLite)"]
        Dedup["Deduplication"]
    end

    subgraph Simulator["Rust Simulator"]
        IPC["IPC Bridge\n(Go ↔ Rust)"]
        VM["Soroban VM\n(soroban-env-host)"]
        SM["Source Mapper\n(DWARF + Fallback)"]
        Events["Event Extractor"]
        Stack["Stack Trace\nCapture"]
    end

    subgraph Trace["Trace Engine"]
        Viewer["Interactive Viewer\n(Bubbletea TUI)"]
        Export["Export Engine\n(JSON / HTML)"]
        Flame["Flamegraph\n(pprof)"]
        Profile["CPU/Memory\nProfiling"]
    end

    subgraph Output["Output Layer"]
        Text["Terminal Output\n(Color / Plain)"]
        JSON["JSON Output\n(--json)"]
        SVG["Call Graph\n(--export-svg)"]
        TraceOut["Trace File\n(--trace-output)"]
        Audit["Audit Trail\n(Signed)"]
    end

    subgraph Observability["Observability"]
        Telemetry["Telemetry\n(Opt-in)"]
        Crash["Crash Reporting\n(Sentry)"]
        Metrics["Prometheus\nMetrics"]
    end

    CLI --> Flags
    CLI --> TOML
    CLI --> ENV
    URI --> CLI
    TS --> CLI

    Flags --> Discovery
    Flags --> RPC
    Flags --> Simulator

    RPC --> Failover
    Failover --> Endpoint1
    Failover --> Endpoint2
    Failover --> EndpointN

    RPC --> Fetcher
    Fetcher --> Store
    Store --> Dedup

    Fetcher --> IPC
    IPC --> VM
    VM --> SM
    VM --> Events
    VM --> Stack
    SM --> DWARF
    DWARF --> Cache
    DWARF --> Registry
    DWARF --> GitHub
    DWARF --> Alias

    Events --> Viewer
    Stack --> Viewer
    SM --> Viewer
    Viewer --> Export
    Export --> Text
    Export --> JSON
    Export --> SVG
    Export --> TraceOut
    Viewer --> Flame
    Viewer --> Profile

    CLI --> Telemetry
    CLI --> Crash
    CLI --> Metrics
```

## Component ownership

| Component | Package | Language | Responsibility |
|-----------|---------|----------|----------------|
| CLI framework | `cmd/glassbox`, `internal/cmd/` | Go | Argument parsing, flag validation, command dispatch |
| Deep link handler | `internal/deeplink/`, `internal/protocolreg/` | Go | Parse `glassbox://` URIs, register OS protocol handler |
| Configuration | `internal/config/` | Go | Load TOML config, env vars, flag merging |
| Source discovery | `internal/sourcemap/`, `internal/dwarf/`, `internal/cache/` | Go | DWARF parsing, cache, registry, GitHub fallback |
| RPC client | `internal/rpc/` | Go | Adaptive failover, endpoint health tracking |
| Snapshot system | `internal/snapshot/`, `internal/db/` | Go | Ledger state capture, SQLite storage, deduplication |
| IPC bridge | `internal/ipc/`, `internal/bridge/` | Go ↔ Rust | Request/response serialization, process management |
| Soroban VM | `simulator/src/vm.rs`, `simulator/src/host.rs` | Rust | Contract execution via `soroban-env-host` |
| Source mapper | `simulator/src/source_mapper.rs` | Rust | DWARF resolution, fallback pipeline |
| Event extractor | `simulator/src/events.rs` | Rust | Diagnostic and contract event capture |
| Stack trace | `simulator/src/stack_trace.rs` | Rust | WASM stack trace capture on traps |
| Trace viewer | `internal/trace/` | Go | Interactive TUI with search, navigation |
| Export engine | `internal/trace/` | Go | JSON, HTML, SVG trace export |
| Profiling | `internal/profile/` | Go | CPU/memory flamegraph via pprof |
| Telemetry | `internal/telemetry/` | Go | Opt-in, privacy-sanitized usage data |
| Crash reporting | `internal/crashreport/` | Go | Sentry integration, custom crash endpoint |
| Metrics | `internal/metrics/` | Go | Prometheus metrics for RPC health |
| TypeScript CLI | `src/` | TypeScript | Node.js CLI layer, Commander.js |
| VS Code extension | `vscode-extension/` | TypeScript | Editor integration |

## Synchronous vs asynchronous boundaries

```mermaid
flowchart LR
    subgraph Sync["Synchronous"]
        A["CLI dispatch"] --> B["Flag validation"]
        B --> C["Config load"]
        C --> D["Source discovery"]
        D --> E["RPC fetch"]
    end

    subgraph Async["Asynchronous"]
        E --> F["IPC request"]
        F --> G["VM execution"]
        G --> H["Event collection"]
        H --> I["Source mapping"]
    end

    subgraph Callback["Callback / Stream"]
        I --> J["Trace rendering"]
        J --> K["Export write"]
    end

    style Sync fill:#1a1a2e,stroke:#4cc9f0
    style Async fill:#1a1a2e,stroke:#f72585
    style Callback fill:#1a1a2e,stroke:#7209b7
```

**Synchronous:** Flag validation, config loading, source discovery, and RPC
fetch happen sequentially before any simulation begins. Failures are immediate
and return `ErstError` with a stable code.

**Asynchronous:** The IPC request to the Rust simulator, VM execution, and
event collection happen in a subprocess. The Go process reads stdout for
structured JSON responses.

**Callback/stream:** Trace rendering and export happen after simulation
completes. The interactive viewer uses Bubbletea's event loop for real-time
updates.

## Failure propagation

```mermaid
flowchart TD
    E1["RPC failure"] -->|"ERST_RPC_CONNECTION_FAILED"| F1["Hint: check endpoint"]
    E2["RPC timeout"] -->|"ERST_RPC_TIMEOUT"| F2["Hint: try --rpc-url"]
    E3["TX not found"] -->|"ERST_TRANSACTION_NOT_FOUND"| F3["Hint: verify network"]
    E4["Simulator crash"] -->|"ERST_SIMULATOR_CRASH"| F4["Hint: rebuild binary"]
    E5["Sim logic error"] -->|"ERST_SIMULATION_LOGIC_ERROR"| F5["Hint: check trace"]
    E6["Source missing"] -->|"ERST_SOURCE_DISCOVERY_FAILED"| F6["Hint: --contract-source"]
    E7["Config error"] -->|"ERST_CONFIG_ERROR"| F7["Hint: glassbox doctor"]
    E8["Validation fail"] -->|"ERST_VALIDATION_FAILED"| F8["Hint: check flags"]

    E1 & E2 -->|"All fail"| ALL["ERST_ALL_RPC_FAILED"]
    ALL -->|"Fallback"| DIAG["glassbox doctor --bundle"]

    style ALL fill:#d00000
    style DIAG fill:#3a0ca3
```

Every failure path produces a structured `ErstError` with:
- **Code**: stable string for automation (e.g. `RPC_CONNECTION_FAILED`)
- **Message**: human-readable description
- **Hint**: actionable remediation

See [stable-error-codes.md](./stable-error-codes.md) for the full catalogue.

## Data transformation pipeline

```mermaid
flowchart LR
    A["Transaction\nEnvelope XDR"] -->|"Decode"| B["Ledger\nState"]
    B -->|"Fetch"| C["Snapshot\n(JSON)"]
    C -->|"Serialize"| D["Simulation\nRequest"]
    D -->|"IPC"| E["Soroban VM\nExecution"]
    E -->|"Extract"| F["Diagnostic\nEvents"]
    E -->|"Resolve"| G["Source\nMapping"]
    E -->|"Capture"| H["Stack\nTrace"]
    F & G & H -->|"Compose"| I["Execution\nTrace"]
    I -->|"Validate"| J["Schema\nCheck"]
    J -->|"Render"| K["Terminal /\nJSON / HTML"]
```

| Stage | Input | Output | Package |
|-------|-------|--------|---------|
| Decode | Envelope XDR (base64) | Go structs | `internal/rpc/` |
| Fetch | Transaction hash | Ledger entries | `internal/rpc/` |
| Snapshot | Ledger entries | JSON file | `internal/snapshot/` |
| Serialize | Go structs | JSON request | `internal/ipc/` |
| Execute | JSON request | VM state + events | `simulator/src/` |
| Extract | VM state | DiagnosticEvent[] | `simulator/src/events.rs` |
| Resolve | WASM address | SourceLocation | `simulator/src/source_mapper.rs` |
| Capture | VM stack | StackFrame[] | `simulator/src/stack_trace.rs` |
| Compose | Events + locations + frames | ExecutionTrace | `internal/trace/` |
| Validate | ExecutionTrace | Schema-valid trace | `internal/trace/` |
| Render | Valid trace | Terminal/JSON/HTML | `internal/trace/` |

## Component boundaries

```mermaid
flowchart TB
    subgraph GoProcess["Go Process"]
        CLI["CLI / Commands"]
        Config["Config"]
        RPC["RPC Client"]
        Trace["Trace Engine"]
        IPC_Out["IPC Sender"]
    end

    subgraph RustProcess["Rust Process (glassbox-sim)"]
        IPC_In["IPC Receiver"]
        VM["Soroban VM"]
        Mapper["Source Mapper"]
        Events["Event Extractor"]
    end

    subgraph External["External"]
        Network["Stellar Network\n(RPC)"]
        Cache_Dir["Local Cache\n(~/.Glassbox/)"]
        FS["Filesystem\n(WASM, Sources)"]
    end

    CLI <--> Config
    CLI <--> RPC
    CLI <--> Trace
    CLI <--> IPC_Out

    IPC_Out <-->|"JSON over\nstdio"| IPC_In

    IPC_In <--> VM
    VM <--> Mapper
    VM <--> Events

    RPC <--> Network
    Mapper <--> FS
    Config <--> Cache_Dir
    Trace <--> FS
```

**Boundary rules:**
- Go and Rust communicate exclusively via **JSON over stdin/stdout** (IPC).
- The Rust simulator is a **stateless subprocess** — it receives a request and
  returns a response.
- Source mapping in Go handles **discovery and caching**; the Rust side handles
  **DWARF resolution** during execution.
- All network I/O happens in Go; the Rust process is offline-only.

## Updating this diagram

When adding or modifying IPC messages, schema fields, or component boundaries:

1. Update the relevant Mermaid diagram section above.
2. Ensure diagram terminology matches code (package names, struct names).
3. Include the diagram in architecture review checklists for PRs that change
   IPC or schema contracts.
4. Run `mermaid-cli` locally to validate rendering:
   ```sh
   npx @mermaid-js/mermaid-cli mmdc -i docs/architecture-dataflow.md -o /dev/null
   ```

## See also

- [Trace schema](./schema/trace-schema.md) — execution trace formal specification
- [Schema registry](./schema/README.md) — JSON Schema definitions
- [Debug command](./debug-command.md) — CLI reference
- [Source mapping](./source-mapping.md) — DWARF resolution pipeline
- [Stable error codes](./stable-error-codes.md) — error code catalogue
- [Diagnostics bundle](./diagnostics-bundle.md) — portable diagnostics
- [Operator runbook](./operator-runbook.md) — symptom-based troubleshooting
