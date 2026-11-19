# Bundle Size Troubleshooting

Large debug bundles and signed audit logs can be awkward to share or keep.
Before you delete anything from one of these files, read this guide. Both
formats are protected by checksums or signatures, so editing the file by hand
makes it fail verification.

## Size limits

**Glassbox enforces no size limit on bundles or audit logs.**

- `glassbox debug bundle create` writes the full manifest with
  `json.MarshalIndent` and does not check its size
  (`internal/bundle/bundle.go`, `SaveToFile`).
- `bundle.LoadFromFile` reads the whole file into memory and checks only the
  format version, required fields, and checksums. It has no size cap.

The only related limit is a **network** limit, not a file-size limit.
`simulator.MaxLedgerEntriesSizeBytes` (1 MiB) is the Soroban cap on the total
decoded size of a transaction's ledger entries. `glassbox dry-run` prints a
warning when the entries go over this cap (`internal/simulator/ledger_limits.go`).
A bundle that is much larger than 1 MiB because of `ledger_state` usually
means the transaction had a large footprint. It does not mean the bundle is
broken.

## What a bundle contains

`glassbox debug bundle create` has no flags to include or exclude members.
Every bundle contains the same members, and each has a SHA-256 checksum:

| Member | Checksum key | Can it be left out? |
|--------|--------------|---------------------|
| Transaction envelope XDR | `envelope_xdr` | No. Required for replay |
| Result metadata XDR | `result_meta_xdr` | No. Required for replay |
| Ledger state snapshot | `ledger_state` | No. Required for replay; this is usually the largest member |
| Provenance (version, tx hash, ledger, fetch time) | `provenance` | No |
| Network name and passphrase | `network` | No |

Bundles never contain RPC tokens, API keys, or private keys, so you do not
need to redact them for credentials.

## What a signed audit log contains

`glassbox debug --audit-key <pem>` produces a signed audit log. Its `payload`
contains `envelope_xdr`, `result_meta_xdr`, `events`, `logs`, and
`decoded_events` when event schemas are configured. An optional
`hardware_attestation` block may also be present. The signature covers the
payload and the attestation. If you remove or edit any of these fields after
signing, `glassbox audit:verify` will fail.

## Safe inspection sequence

1. **Verify before you change anything**, so you know the file is valid to
   begin with:

   ```bash
   glassbox debug bundle verify my-bundle.json --json
   glassbox audit:verify --audit-log audit.json --json
   ```

2. **Measure each member** without changing the file:

   ```bash
   jq '{envelope: (.transaction.envelope_xdr | length),
        result_meta: (.transaction.result_meta_xdr | length),
        ledger_entries: (.ledger_state | length),
        ledger_state_chars: (.ledger_state | tostring | length)}' my-bundle.json
   ```

   For audit logs:

   ```bash
   jq '.payload | {events: (.events | length), logs: (.logs | length),
                   decoded_events: ((.decoded_events // []) | length)}' audit.json
   ```

3. **Reduce size without changing the content.** Bundles are indented JSON
   with large base64 strings, so they compress well. Compress the file for
   transfer or storage, and decompress it before running
   `bundle verify` or `debug --bundle`:

   ```bash
   gzip -k my-bundle.json        # or: zstd my-bundle.json
   ```

4. **Leave out optional data at creation time.** Use these flags instead of
   editing the file afterwards:

   - `glassbox export`: leave off `--include-memory` (it is off by default) so
     the snapshot contains no Wasm linear-memory dump. Use `--plan` to see what
     will be written first.
   - `glassbox debug --audit-key`: decoded events are added only when event
     schemas are configured. Leave schemas out if you need only the raw
     `events`.

5. **Do not hand-edit signed or checksummed files.** If you need a smaller
   bundle, create a new one from the source transaction. If the size comes
   from the transaction's own footprint, no supported option can shrink the
   bundle without breaking offline replay.
