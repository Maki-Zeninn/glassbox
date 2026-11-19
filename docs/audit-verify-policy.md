# Audit verification policy levels

`glassbox audit:verify-dir` verifies that an audit log **directory** is
tamper-evident as a whole, not just that each file is internally valid.

## The problem

Per-file signature verification answers "is this file valid on its own?". An
adversary with write access to the audit log directory does not need to forge
anything: they can delete an entry, reorder two, or splice an unsigned file
between two legitimate ones. Every remaining file still verifies individually.
Only the **sequence** reveals the tampering, via the hash chain that links each
segment to its predecessor.

## Policy levels

A chain irregularity is not always equally serious, so the response is a policy
decision:

| Level | Chain irregularities | Signature failure |
| ----- | -------------------- | ----------------- |
| `strict` (default) | fatal | fatal |
| `warn` | reported, exit 0 | fatal |
| `permissive` | reported, exit 0 | fatal |

**Signature verification is fatal at every level.** A broken chain link means
the collection may have been rearranged; a bad signature means a file is not
authentic, which is a different and more serious problem.

The default is `strict` because failing loudly is the safe behaviour for an
audit tool, and because an unrecognised level in a config file must not silently
downgrade an audit to advisory-only.

## Usage

```bash
# Fail on any chain irregularity
glassbox audit:verify-dir --dir ~/.glassbox/audit --policy strict

# Report irregularities but do not fail
glassbox audit:verify-dir --dir ~/.glassbox/audit --policy warn

# Only signature failures are fatal
glassbox audit:verify-dir --dir ~/.glassbox/audit --policy permissive
```

`--policy` composes with the existing `--require-chain` flag: `require_chain`
turns a broken link into a *reported issue* governed by the policy level, while
omitting it leaves the link as an informational note.

## Issue kinds

`PolicyEnforcer` classifies each irregularity so the levels can be applied to
kinds rather than to a single pass/fail boolean:

| Kind | Meaning |
| ---- | ------- |
| `sequence_gap` | a segment is missing from the ordered sequence |
| `broken_chain_link` | `previous_segment_hash` does not match the previous segment |
| `unsigned_file` | a file in the directory has no manifest |
| `missing_manifest` | a segment file is present but its manifest is absent or unparseable |
| `structural` | a segment body is missing, empty or corrupt |
| `policy_violation` | a configured `DirPolicy` constraint was violated |
| `signature_failure` | cryptographic signature verification failed |

## Library

```go
enforcer := audit.NewPolicyEnforcer(audit.PolicyWarn)
result, err := audit.VerifyDirectoryWithPolicy(dir, policy)
if err != nil {
    return err
}

outcome := enforcer.EnforceResult(result)
for _, issue := range outcome.Issues {
    fmt.Printf("%s: %s\n", issue.Kind, issue.Detail)
}
if outcome.Fatal {
    os.Exit(1)
}
```

`EnforceResult` adapts the existing `DirPolicyResult` into the ordered issue
list, so no existing verification code changes behaviour. `PolicyOutcome.Level`
echoes the level that produced the decision, so a report records how it was
reached.
