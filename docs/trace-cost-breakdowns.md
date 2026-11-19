# Trace Cost Breakdowns

Glassbox can annotate contract-call trace states with resource cost information.

Cost annotations may come from observed simulator budget usage or from a gas
model JSON file:

```bash
glassbox trace execution.json --gas-model ./gas-model.json --export trace.md --export-format markdown
```

Each annotated trace node includes:

- `source`: `observed`, `gas_model_estimate`, or `node_delta`
- `cpu`: CPU instructions attributed to the call
- `memory_bytes`: memory attributed to the call
- `operations`: host operation count when available
- `estimated_fee`: gas-model fee estimate in stroops when available
- `breakdown`: component-level resource buckets

HTML and Markdown trace exports render both a compact cost summary and the
component breakdown for each annotated contract call.

---

## Worked example

The examples below use a two-operation trace to show what cost breakdown output
looks like in practice. The values are taken directly from the test fixtures in
`internal/trace/annotations_test.go` so they are deterministic and will not
drift.

### Trace fixture

Two states, one `contract_call` and one `host_function`:

```go
// From TestAnnotateExecutionCosts_FromBudgetUsage
tr := NewExecutionTrace("tx", 10)
tr.AddState(ExecutionState{
    Operation: "contract_call",
    EventType: EventTypeContractCall,
    ContractID: "C1",
    Function:  "transfer",
})
tr.AddState(ExecutionState{
    Operation: "host_function",
    EventType: EventTypeHostFunction,
    Function:  "require_auth",
})

AnnotateExecutionCosts(tr, &simulator.BudgetUsage{
    CPUInstructions:    1000,
    MemoryBytes:        2048,
    OperationsCount:    4,
    CPULimit:           10000,
    MemoryLimit:        8192,
    CPUUsagePercent:    10,
    MemoryUsagePercent: 25,
}, nil)
```

`AnnotateExecutionCosts` only attaches cost to `contract_call` steps.
`host_function` steps are left unannotated — the budget is attributed to the
contract invocation that triggered the host call, not the host call itself.

### Resulting `CostAnnotation` on step 0 (`transfer`)

```json
{
  "source": "observed",
  "cpu": 1000,
  "memory_bytes": 2048,
  "operations": 4,
  "limit_cpu": 10000,
  "limit_memory": 8192,
  "breakdown": [
    {
      "name": "cpu_instructions",
      "category": "cpu",
      "units": 1000,
      "total": 1000,
      "percent": 10
    },
    {
      "name": "memory_bytes",
      "category": "memory",
      "units": 2048,
      "total": 2048,
      "percent": 25
    },
    {
      "name": "operations",
      "category": "host",
      "units": 4,
      "total": 4
    }
  ]
}
```

Step 1 (`require_auth`) has `cost: null` — no annotation is attached.

### Unit glossary

| Field | Unit | Notes |
|---|---|---|
| `cpu` / `cpu_instructions` | Metered CPU instructions (dimensionless integer) | Stellar's Soroban budget counts abstract instructions, not wall-clock time. Larger values mean more compute budget consumed. |
| `memory_bytes` | Bytes | Linear memory allocated by the WASM module during this call. |
| `operations` | Host operation count | Each distinct host function invocation counts as one operation in the budget. Reported when the simulator provides `OperationsCount`. |
| `estimated_fee` | Stroops (1 XLM = 10 000 000 stroops) | Only present when a gas model is used (`source: gas_model_estimate`). Absent from `observed` annotations. |
| `percent` in breakdown components | Percentage of the protocol limit | `cpu_instructions.percent = 10` means 1 000 of the 10 000-instruction limit was used. Only set when the simulator supplies `CPUUsagePercent` / `MemoryUsagePercent`. |
| `limit_cpu` / `limit_memory` | Same units as `cpu` / `memory_bytes` | The protocol budget ceiling for this transaction. Present on `observed` annotations when the simulator reports limits. |

### Gas-model estimate variant

When no simulator budget is available, a gas model file can provide an
estimated fee. From `TestAnnotateExecutionCosts_FromGasModel`:

```go
AnnotateExecutionCosts(tr, nil, &gasmodel.GasModel{
    Version:  "v1",
    CPUCosts: []gasmodel.GasCost{{Name: "invoke_contract", Linear: 2, Const: 10}},
})
```

Resulting annotation on the `contract_call` step:

```json
{
  "source": "gas_model_estimate",
  "model_version": "v1",
  "estimated_fee": 12,
  "breakdown": [
    {
      "name": "invoke_contract",
      "category": "cpu",
      "unit_cost": 2,
      "total": 12
    }
  ]
}
```

`estimated_fee = Const + Linear = 10 + 2 = 12` stroops. The `cpu` and
`memory_bytes` top-level fields are **0** here because the gas model does not
observe actual simulator budget — it only estimates a fee. Do not interpret
zero `cpu` as "no CPU was used"; it means "CPU usage was not measured for this
annotation".

### Unavailable values

Some fields will always be absent or zero depending on how the trace was
captured:

| Field | When absent / zero | What it means |
|---|---|---|
| `estimated_fee` | `source: observed` | No gas model was provided; fee estimation requires `--gas-model`. |
| `cpu`, `memory_bytes` | `source: gas_model_estimate` | The gas model only estimates fees, not raw resource consumption. |
| `operations` | Simulator did not report `OperationsCount` | Host operation counting is optional in some simulator versions. |
| `percent` in breakdown | Simulator did not report usage percentages | Older simulator builds omit `CPUUsagePercent` / `MemoryUsagePercent`. |
| `limit_cpu`, `limit_memory` | `source: gas_model_estimate` or `node_delta` | Limits come from the simulator budget; they are not part of a gas model or node-delta annotation. |
| `model_version` | `source: observed` | No gas model was involved; the version field only identifies the gas model file, not the protocol version. |

### How the cost summary line is formatted

`FormatCostAnnotation` (in `internal/trace/annotations.go`) renders the
top-level fields as a compact one-liner:

```
source=observed, cpu=1000, memory=2048 bytes, operations=4
```

`FormatCostBreakdown` renders each component on its own line, sorted
alphabetically:

```
cpu_instructions [cpu]: units=1000 total=1000 10.00%
memory_bytes [memory]: units=2048 total=2048 25.00%
operations [host]: units=4 total=4
```

Both are verified by `TestTraceExportIncludesCommentsMetadataAndCost` in
`internal/trace/annotations_test.go`, which checks that `source=observed` and
`cpu_instructions` appear in a Markdown export of a trace carrying this
annotation.

No performance guarantee is implied by any value in a cost annotation.
Observed budget numbers reflect a single simulator run; they may vary across
protocol versions, simulator builds, and ledger state.
