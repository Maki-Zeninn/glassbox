# Security Finding Suppression

This document describes the suppression mechanism for security findings in Glassbox, which allows controlled suppression of repeated known findings without hiding newly changed or more severe issues.

## Overview

The suppression system provides a way to:
- Suppress repeated known findings with proper oversight
- Track ownership, reasons, and expiration for suppressions
- Ensure suppressed findings are still visible in reports
- Prevent suppression of findings that have changed (different fingerprint)
- Enforce expiration dates for temporary suppressions

## Suppression Record Structure

A suppression record contains:

- **Fingerprint**: Unique identifier for the finding (SHA-256 hash)
- **Scope**: Where the suppression applies (global, contract, path, transaction)
- **ScopeValue**: Contract ID, path, or transaction hash for scoped suppressions
- **Reason**: Explanation for why the finding is suppressed
- **Owner**: Person or team who approved the suppression
- **ExpiresAt**: When the suppression expires (zero for no expiration)
- **CreatedAt**: When the suppression record was created
- **Signature**: Optional signature for reviewed suppressions
- **Reviewer**: Person who reviewed the suppression (if signed)

## Suppression Scopes

### Global
Applies to all findings matching the fingerprint, regardless of context.

```go
record := &SuppressionRecord{
    Fingerprint: "abc123...",
    Scope:       ScopeGlobal,
    Reason:      "False positive in test fixture",
    Owner:       "security-team",
    CreatedAt:   time.Now().UTC(),
}
```

### Contract
Applies only to findings for a specific contract.

```go
record := &SuppressionRecord{
    Fingerprint: "abc123...",
    Scope:       ScopeContract,
    ScopeValue:  "contract123...",
    Reason:      "Known issue in contract",
    Owner:       "security-team",
    CreatedAt:   time.Now().UTC(),
}
```

### Path
Applies only to findings at a specific file path.

```go
record := &SuppressionRecord{
    Fingerprint: "abc123...",
    Scope:       ScopePath,
    ScopeValue:  "src/contract.rs",
    Reason:      "Test fixture with intentional pattern",
    Owner:       "security-team",
    CreatedAt:   time.Now().UTC(),
}
```

### Transaction
Applies only to findings for a specific transaction.

```go
record := &SuppressionRecord{
    Fingerprint: "abc123...",
    Scope:       ScopeTransaction,
    ScopeValue:  "tx123...",
    Reason:      "One-off exception",
    Owner:       "security-team",
    CreatedAt:   time.Now().UTC(),
}
```

## Fingerprinting

Findings are fingerprinted based on their key characteristics:

### Security Detector Findings
Fingerprint includes: type, severity, title, and evidence.

```go
fingerprint := ComputeFinding(finding)
```

### Secret Scanner Findings
Fingerprint includes: type, location, and context.

```go
fingerprint := ComputeSecretFinding(finding)
```

**Important**: If a finding changes (e.g., different evidence, different location), its fingerprint changes and it will no longer be suppressed. This ensures that changed findings reappear.

## Usage

### Creating a Suppression Registry

```go
registry := security.NewSuppressionRegistry()

// Add a suppression record
record := &SuppressionRecord{
    Fingerprint: "abc123...",
    Scope:       ScopeGlobal,
    Reason:      "False positive in test fixture",
    Owner:       "security-team",
    CreatedAt:   time.Now().UTC(),
    ExpiresAt:   time.Now().UTC().Add(30 * 24 * time.Hour), // 30 days
}
err := registry.Add(record)
```

### Loading Suppressions from JSON

```go
registry := security.NewSuppressionRegistry()
err := registry.AddFromJSON(suppressionsJSON)
```

### Using with Security Detector

```go
// Create detector with suppression
registry := security.NewSuppressionRegistry()
// ... add suppression records ...

detector := security.NewDetectorWithSuppression(registry, "contract123...")
findings := detector.Analyze(envelopeXdr, resultMetaXdr, events, logs)

// Get findings with suppression applied
result := detector.GetFindingsWithSuppression()
fmt.Printf("Active: %d, Suppressed: %d\n", 
    len(result.ActiveFindings), len(result.SuppressedFindings))
```

### Using with Secret Scanner

```go
// Create scanner with suppression
registry := security.NewSuppressionRegistry()
// ... add suppression records ...

scanner := security.NewSecretScannerWithSuppression(security.ModeOptIn, registry, "contract123...")
scanResult := scanner.ScanMap(data)

// Get findings with suppression applied
result := scanner.GetScanResultWithSuppression(scanResult)
fmt.Printf("Active secrets: %d, Suppressed: %d\n", 
    len(result.ActiveFindings), len(result.SuppressedFindings))
```

### Formatting Reports

```go
formatter := security.NewReportFormatter(true) // Include suppressed findings

// Text report
report := formatter.FormatDetectorReport(result)

// JSON report
jsonReport, err := formatter.FormatDetectorJSON(result)

// Raw findings (without suppression)
rawJSON, err := formatter.FormatRawFindings(allFindings)
```

## Expiration

Suppressions can have expiration dates to ensure temporary suppressions don't persist indefinitely:

```go
record := &SuppressionRecord{
    Fingerprint: "abc123...",
    Scope:       ScopeGlobal,
    Reason:      "Temporary suppression during migration",
    Owner:       "security-team",
    CreatedAt:   time.Now().UTC(),
    ExpiresAt:   time.Now().UTC().Add(7 * 24 * time.Hour), // 7 days
}
```

Expired suppressions automatically stop applying. You can clean up expired records:

```go
count := registry.CleanupExpired()
fmt.Printf("Removed %d expired suppressions\n", count)
```

## Signed/Reviewed Suppressions

For high-risk suppressions, require a signature and reviewer:

```go
record := &SuppressionRecord{
    Fingerprint: "abc123...",
    Scope:       ScopeGlobal,
    Reason:      "Reviewed and approved as false positive",
    Owner:       "security-team",
    CreatedAt:   time.Now().UTC(),
    Signature:   "sig123...", // Cryptographic signature
    Reviewer:    "reviewer@example.com",
}
```

## Validation

Suppression records are validated before being added:

- Fingerprint cannot be empty
- Reason cannot be empty
- Owner cannot be empty
- CreatedAt cannot be zero
- Scope-specific requirements (e.g., scope_value for non-global scopes)
- Expiration must be in the future (if set)
- If signature is present, reviewer must also be present

## Reporting

### Text Report

```
Security Findings Report
========================

Active Findings: 1

  1. [HIGH] Test Finding
     Type: HEURISTIC_WARNING
     Evidence: evidence123
     Description: This is an active finding


Suppressed Findings: 1

  1. [MEDIUM] Suppressed Finding (SUPPRESSED)
     Type: VERIFIED_RISK
     Evidence: evidence456
     Suppression Reason: False positive
     Suppression Owner: security-team
     Expires: 2026-08-26T00:00:00Z
```

### JSON Report

```json
{
  "active_findings": [...],
  "suppressed_findings": [
    {
      "finding": {...},
      "record": {
        "fingerprint": "abc123...",
        "scope": "global",
        "reason": "False positive",
        "owner": "security-team",
        "expires_at": "2026-08-26T00:00:00Z",
        ...
      },
      "fingerprint": "abc123..."
    }
  ],
  "active_count": 1,
  "suppressed_count": 1
}
```

## Best Practices

1. **Always provide a reason**: Explain why a finding is being suppressed
2. **Set an expiration**: Use temporary suppressions when possible
3. **Use scoped suppressions**: Prefer contract/path scope over global when appropriate
4. **Review high-risk suppressions**: Require signature for suppressions of high-severity findings
5. **Clean up expired records**: Regularly remove expired suppressions
6. **Document suppressions**: Keep suppression records in version control
7. **Audit suppressions**: Review suppression records periodically

## Acceptance Criteria

The implementation meets the following acceptance criteria:

✅ **Suppression never removes findings from raw JSON**
- Raw JSON output includes all findings without suppression applied
- Suppression is applied only to formatted reports
- `FormatRawFindings()` and `FormatRawSecretFindings()` return all findings

✅ **Expired suppressions stop applying**
- `IsActive()` checks expiration before applying suppression
- `GetActive()` returns only non-expired records
- `CleanupExpired()` removes expired records from registry

✅ **Changed finding fingerprints reappear**
- Fingerprints are based on finding characteristics (type, severity, title, evidence)
- If a finding changes, its fingerprint changes and it's no longer suppressed
- This ensures that modified findings are not hidden

✅ **Reports show owner, reason, and expiry**
- Text reports include suppression metadata (owner, reason, expiration)
- JSON reports include full suppression record details
- Reviewer and signature information included when present

## Files

- `internal/security/suppression.go` - Core suppression implementation
- `internal/security/detector.go` - Extended with suppression support
- `internal/security/secret_scanner.go` - Extended with suppression support
- `internal/security/report.go` - Report formatting with suppression
- `internal/security/suppression_test.go` - Comprehensive tests
