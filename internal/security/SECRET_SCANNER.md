# Secret Scanner

The secret scanner detects and prevents accidental leakage of sensitive data (API keys, tokens, private keys, PEM data) in trace and session exports.

## Overview

The secret scanner provides configurable detection of common secret formats with two operational modes:
- **Opt-in mode**: Warns when secrets are detected but allows the export to proceed
- **Strict mode**: Blocks exports when secrets are detected

## Detected Secret Types

The scanner detects the following secret patterns:

### API Keys
- `api_key: <value>` or `apikey: <value>` patterns
- Generic long keys (32+ alphanumeric characters)

### Bearer Tokens
- `Bearer <token>` prefix (case-insensitive)
- `bearer_token: <value>` patterns

### Private Keys
- PEM headers: `-----BEGIN RSA PRIVATE KEY-----`, `-----BEGIN EC PRIVATE KEY-----`, etc.
- Hex-encoded private keys (64 hex chars) in fields containing "private"

### PEM Data
- PEM-encoded certificates and keys
- Multi-line base64-encoded data resembling PEM format

### AWS Keys
- `aws_access_key_id: <value>` patterns
- `aws_secret_access_key: <value>` patterns
- 40-character secret keys in "secret" fields

### GitHub Tokens
- GitHub PATs: `ghp_` followed by 36 alphanumeric characters
- GitHub app tokens: `gho_`, `ghu_`, `ghs_`, `ghr_` prefixes

### JWT Tokens
- Three-part base64-encoded tokens (header.payload.signature)

## Usage

### Trace Export

```bash
# Enable secret scanning in opt-in mode (warn only)
glassbox trace --export trace.html --secret-scan-mode opt-in execution.json

# Enable secret scanning in strict mode (block on secrets)
glassbox trace --export trace.html --secret-scan-mode strict execution.json

# Add overrides for test fixtures
glassbox trace --export trace.html --secret-scan-mode strict \
  --secret-scan-override test.fixture.key --secret-scan-override mock.api_key \
  execution.json
```

### Session Export

```bash
# Enable secret scanning for session share
glassbox session share --secret-scan-mode strict

# Export with opt-in mode
glassbox session share --secret-scan-mode opt-in --output session.gbx

# Add overrides for known test data
glassbox session share --secret-scan-mode strict \
  --secret-scan-override test.credentials \
  --output session.gbx
```

## Scanner Modes

### Opt-In Mode (`--secret-scan-mode opt-in`)
- Scans export data for secrets
- Prints warnings to stderr when secrets are detected
- Allows the export to proceed
- Default behavior when mode is not specified

### Strict Mode (`--secret-scan-mode strict`)
- Scans export data for secrets
- Blocks the export if any secrets are detected
- Prints detailed error message with secret locations
- Requires explicit action to proceed (remove secrets or add overrides)

## Override Mechanism

The override mechanism allows intentional test fixtures to bypass secret detection:

```bash
# Mark specific paths as safe
--secret-scan-override test.fixture.api_key
--secret-scan-override mock.credentials.token
--secret-scan-override fixtures.private_key
```

Overrides are:
- Explicit and auditable (visible in command history)
- Path-specific (only affects the specified location)
- Intended for test fixtures and mock data only

## Scanned Data

### Trace Exports
The scanner scans:
- Session metadata (`--meta` flags)
- Comments (`--comment` flags)
- Reviewer comments (from annotation files)

### Session Exports
The scanner scans:
- Session fields: `pinned_endpoint`, `horizon_url`
- Session annotations (if present)

## Error Messages

When secrets are detected, the scanner provides:

```
Secret scan detected 2 potential secret(s) in export data
Detected secrets:
  1. [API_KEY] at session_metadata.api_key
     Context: sk-****qrstuvwxyz
  2. [BEARER_TOKEN] at comments[0]
     Context: bearer ****

Export blocked due to strict mode.
To allow this export:
  1. Remove the secret from the data, or
  2. Use opt-in mode instead of strict mode, or
  3. Add an explicit override for this location if it's a test fixture
```

## Security Properties

### Value Redaction
- Secret values are never printed in full
- Context is masked with asterisks (`****`)
- Only location and type information is exposed

### Bounded Scanning
- Scanner operates on export-time data only
- Does not scan source files or trace binary data
- Configurable patterns prevent false positives

### Audit Trail
- All overrides are explicit command-line flags
- Scanner mode is visible in command history
- Detection results are logged to stderr

## Integration Points

The secret scanner is integrated into:

1. **Trace Export** (`internal/trace/export.go`)
   - Called during `ExportExecutionTraceWithOptions`
   - Scans `ExportOptions` before file generation

2. **Session Export** (`internal/session/archive.go`)
   - Called during `ExportArchiveWithOptions`
   - Scans session data before archive creation

3. **CLI Commands** (`internal/cmd/trace.go`, `internal/cmd/share_session.go`)
   - Flags: `--secret-scan-mode`, `--secret-scan-override`
   - Validation in command handlers

## Testing

Run secret scanner tests:

```bash
go test ./internal/security/secret_scanner_test.go ./internal/security/secret_scanner.go -v
```

Test coverage includes:
- Pattern detection for each secret type
- Override mechanism
- Mode switching (opt-in vs strict)
- Context masking
- False positive handling

## Limitations

- **Heuristic-based**: Patterns may produce false positives
- **Pattern matching**: Limited to known secret formats
- **No runtime analysis**: Does not analyze execution traces
- **Base64 detection**: May flag legitimate base64 data as PEM

## Best Practices

1. **Enable strict mode in CI/CD**: Prevent accidental secret leaks in automated pipelines
2. **Use opt-in mode locally**: Allow developers to review warnings before blocking
3. **Document overrides**: Comment test fixtures that require overrides
4. **Review warnings**: Investigate all secret scan warnings, even in opt-in mode
5. **Rotate exposed secrets**: If a secret was nearly exported, consider rotation

## Future Enhancements

- [ ] Custom pattern configuration file
- [ ] Integration with environment variable scanning
- [ ] Machine learning-based secret detection
- [ ] Automatic redaction of detected secrets
- [ ] Per-field sensitivity levels
- [ ] Integration with secret management systems
