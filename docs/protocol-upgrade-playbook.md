# Stellar Protocol Upgrade Playbook

This playbook guides maintainers through evaluating, integrating, and releasing
support for a new Stellar protocol version. Protocol changes can affect RPC data
shapes, host function behaviour, XDR definitions, resource limits, and
source-mapping assumptions. Following this checklist end-to-end ensures that
unsupported behaviour is explicit, fixtures and documentation are updated
together, and users receive clear release notes describing every user-visible
change.

---

## 1. Discovery phase

Before writing any code, understand what has changed.

### 1.1 Obtain the protocol specification

- [ ] Locate the CAP (Core Advancement Proposal) for the new protocol version on
  the [Stellar developer documentation](https://developers.stellar.org/) or the
  [stellar/stellar-protocol](https://github.com/stellar/stellar-protocol) repository.
- [ ] Download the updated XDR schema (`.x` files) from
  [stellar/stellar-xdr](https://github.com/stellar/stellar-xdr) for the target
  protocol version.
- [ ] Review the host-function changelog for additions, removals, or
  re-numbered opcodes that affect WASM execution semantics.

### 1.2 Identify affected Glassbox surfaces

Run the protocol-registration diagnostic to list everything that is
version-gated:

```bash
glassbox protocol diagnose --protocol <N>
```

Check each affected surface against the compatibility matrix at
`docs/compatibility-matrix.md`. Record which rows need a new entry.

### 1.3 Identify breaking vs. additive changes

| Change type | Action required |
|-------------|-----------------|
| New host function (additive) | Add XDR snapshot, update fixture |
| Removed / renamed host function | Deprecation cycle + migration note |
| Changed resource-limit constants | Update `gasmodel` constants, re-run budget tests |
| Changed XDR type | Regenerate XDR snapshot, update golden fixtures |
| Changed RPC field | Update `rpc` response structs, add JSON schema test |
| Changed source-mapping assumption | Update `sourcemap` fallback pipeline, re-run explain tests |

---

## 2. Capability negotiation

Glassbox gates protocol-specific behaviour behind the `protocolreg` package.
Every new host function or behaviour change must be registered before use.

### 2.1 Register the new protocol version

In `internal/protocolreg/`, add the new version constant and its capability set:

```go
// Example: registering protocol N
const ProtocolN = ProtocolVersion(N)

func init() {
    Register(ProtocolN, Capabilities{
        HostFunctions: []string{"soroban_host_function_new", ...},
        ResourceLimits: ResourceLimitSet{
            MaxEntryBytes:       4096,
            MaxReadLedgerEntries: 40,
            // ...
        },
    })
}
```

Run the conflict check to catch any duplicate registrations:

```bash
go test ./internal/protocolreg/...
```

### 2.2 Capability negotiation in the simulator

The simulator queries `protocolreg.CapabilitiesFor(version)` at request time.
When a capability is absent for the negotiated version, the simulator must
return an explicit `UnsupportedProtocolVersion` error — never silently degrade.

Verify the negotiation path by running:

```bash
glassbox protocol diagnose --protocol <N> --json | jq '.capabilities'
```

---

## 3. XDR and RPC updates

### 3.1 Regenerate the XDR snapshot

```bash
scripts/generate-snapshot.sh --protocol <N>
go generate ./internal/xdr/...
```

Commit the new snapshot file alongside the code change so reviewers can diff
the generated output.

### 3.2 Update response structs

Edit `internal/rpc/` types that model ledger entries, transaction results, or
event payloads changed by the new protocol. Add JSON struct tags for any new
fields.

### 3.3 Add a schema regression test

Create a test in `integration/` or `internal/rpc/` that:
1. Loads the new XDR snapshot.
2. Decodes a representative transaction (synthetic or captured — see §4).
3. Asserts that every expected field is present and correctly typed.

---

## 4. Fixture and synthetic transaction requirements

**Every supported protocol version must have at least one representative
transaction in the test corpus.** A transaction is representative when it
exercises the primary new capability of the protocol version (e.g. a contract
call using the new host function).

### 4.1 Capturing a real transaction

```bash
glassbox debug --network testnet <tx-hash> --json > testdata/protocol_<N>_example.json
```

Verify the captured trace is deterministic:

```bash
glassbox regression run testdata/protocol_<N>_example.json
```

### 4.2 Creating a synthetic transaction (when testnet is unavailable)

Use the simulator's test-generation tool:

```bash
go run ./internal/testgen/... --protocol <N> --output testdata/protocol_<N>_synthetic.json
```

Synthetic transactions must be committed with a comment in the file describing:
- Which capability they exercise.
- Whether they are synthetic or captured.
- The protocol version they target.

### 4.3 Register the fixture

Add the new fixture to the regression suite in `internal/simulator/regression_tests/`:

```go
{
    Name:     "protocol_N_representative",
    TraceFile: "testdata/protocol_N_example.json",
    Protocol: protocolreg.ProtocolN,
},
```

---

## 5. Source-mapping considerations

Protocol changes that affect WASM execution order, host-function numbering, or
debug-section layout can break DWARF offset assumptions.

### 5.1 Re-run explain mode on existing fixtures

```bash
for trace in testdata/protocol_*.json; do
  glassbox sourcemap explain \
    --wasm "$(jq -r '.wasm_path' "${trace}")" \
    --addr "$(jq -r '.failure_address' "${trace}")"
done
```

Any stage change (e.g. `full_dwarf` → `partial_dwarf`) in an existing fixture
is a regression that must be investigated before release.

### 5.2 Update the fallback pipeline if needed

If the new protocol version changes how DWARF sections are embedded (e.g. a
new custom section name), update `internal/sourcemap/fallback.go` accordingly
and add a targeted test in `internal/sourcemap/explain_test.go`.

---

## 6. Compatibility matrix update

Open `docs/compatibility-matrix.md` and:

- [ ] Add a row for any new CLI flag or JSON field introduced for this protocol.
- [ ] Update the `Since` column for any existing surface that gains new values.
- [ ] Mark deprecated surfaces as `deprecated` with a removal target version.
- [ ] Add a row under the "Protocol versions" section:

```markdown
| vN | Supported | YYYY-MM-DD | brief description of what changed |
```

---

## 7. Rollback plan

Document the rollback procedure **before** merging the protocol support PR.

### 7.1 Feature flag

Gate new protocol behaviour behind `--protocol <N>` and the
`protocolreg.IsSupported` check so users on earlier protocol versions are
unaffected.

### 7.2 Revert path

If the release causes a regression:

1. Revert the protocol-registration commit (`git revert <sha>`).
2. Release a patch containing only the revert.
3. The `UnsupportedProtocolVersion` guard ensures old sessions degrade
   gracefully rather than panicking.

### 7.3 Verification after rollback

Run the full regression suite against the last-known-good snapshot:

```bash
glassbox regression run testdata/protocol_<N-1>_example.json
```

---

## 8. Release notes

The release notes for a protocol upgrade **must** include:

- [ ] A one-sentence summary of what the protocol version changes.
- [ ] A list of every new CLI flag, JSON field, or exit code added.
- [ ] Any source-mapping accuracy changes (quality level changes are
  user-visible because they affect the confidence score shown in output).
- [ ] Migration instructions for users whose contracts depend on any changed
  host function.
- [ ] A pointer to the relevant CAP.

Add a `changelog/` fragment using the standard format:

```bash
echo "feat: add protocol v<N> support (CAP-XXXX)" \
  > changelog/protocol_v<N>_support.md
```

---

## Quick reference checklist

```
[ ] 1. Read CAP and download new XDR schema
[ ] 2. Run `glassbox protocol diagnose --protocol <N>`
[ ] 3. Register protocol version in protocolreg
[ ] 4. Regenerate XDR snapshot
[ ] 5. Update RPC structs and add schema test
[ ] 6. Capture or synthesise representative transaction
[ ] 7. Add fixture to regression suite
[ ] 8. Re-run sourcemap explain on existing fixtures
[ ] 9. Update compatibility-matrix.md
[ ] 10. Document rollback plan
[ ] 11. Write release notes fragment
[ ] 12. Open PR; link to CAP and this playbook section
```
