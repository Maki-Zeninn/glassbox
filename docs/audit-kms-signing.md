# AWS KMS Audit Signing

Glassbox supports AWS Key Management Service (KMS) as an optional signing provider for audit logs, in addition to the built-in software and PKCS#11 providers.

## Overview

AWS KMS provides hardware-level security for audit log signatures. Private keys never leave AWS KMS, and signing operations are performed within the AWS cloud.

## Requirements

- AWS KMS key with Ed25519 key spec
- AWS credentials with `kms:Sign` and `kms:GetPublicKey` permissions
- AWS SDK for Go v2 (automatically included)

## AWS IAM Policy

Your IAM user/role needs the following permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowGlassboxSigning",
      "Effect": "Allow",
      "Action": [
        "kms:Sign",
        "kms:GetPublicKey",
        "kms:DescribeKey"
      ],
      "Resource": "arn:aws:kms:REGION:ACCOUNT_ID:key/KEY_ID"
    }
  ]
}
```

## Creating an Ed25519 KMS Key

```bash
aws kms create-key \
  --key-usage SIGN_VERIFY \
  --key-spec ED25519 \
  --description "Glassbox audit signing key"
```

Save the key ID or alias for use with Glassbox.

## Usage

### Via Environment Variables

```bash
export GLASSBOX_SIGNING_PROVIDER=aws-kms
export GLASSBOX_AWS_KMS_KEY_ID=alias/GlassboxAuditKey
export GLASSBOX_AWS_KMS_REGION=us-east-1

# Optional: AWS credentials (or use default credential chain)
export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY

glassbox audit:sign --payload-file data.json
```

### Via Config File

```toml
[audit]
signing_provider = "aws-kms"

[audit.kms]
key_id = "alias/GlassboxAuditKey"
region = "us-east-1"

# Optional: explicit credentials
# aws_access_key_id = "..."
# aws_secret_access_key = "..."
# aws_profile = "default"
```

### Via CLI Flags

```bash
glassbox audit:sign \
  --payload-file data.json \
  --signing-provider aws-kms \
  --audit-log-kms-key-id alias/GlassboxAuditKey \
  --audit-log-kms-region us-east-1
```

## Supported Key Identifiers

| Format | Example |
|--------|---------|
| Key alias | `alias/GlassboxAuditKey` |
| Key ARN | `arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-123456789012` |
| Key ID | `12345678-1234-1234-1234-123456789012` |

## Credential Precedence

AWS credentials are resolved in the following order:

1. Explicit credentials in config (`aws_access_key_id` / `aws_secret_access_key`)
2. AWS profile from config (`aws_profile`)
3. Default credential chain (environment → config file → EC2 role → ECS task)

## Error Handling

Every KMS failure is wrapped with a fixed prefix and the underlying AWS SDK
error is appended:

| Implementation | Operation | Error prefix |
|----------------|-----------|--------------|
| Go (`internal/signer/kms.go`) | AWS config load | `failed to create AWS config:` |
| Go | Sign | `KMS sign request failed:` |
| Go | GetPublicKey | `failed to get public key from KMS:` |
| TS (`src/audit/signing/kmsSigner.ts`) | Sign | `KMS signing failed:` |
| TS | GetPublicKey | `KMS GetPublicKey failed:` |

### Failure reference

Use the symptom to pick the first check. Local checks need no AWS access;
cloud-side checks require someone with access to the key's account.

| Symptom (error text) | Likely cause | First check | Side |
|----------------------|--------------|-------------|------|
| `AWS KMS provider requires kms_key_id in Extra config` | Key ID not configured (Go) | Set `GLASSBOX_AWS_KMS_KEY_ID` or `[audit.kms] key_id` | Local |
| `KMS signer: GLASSBOX_KMS_KEY_ID is required` / `KMS signer: AWS_REGION is required` | Required env var missing (TS) | Export the variable named in the message | Local |
| `KMS key ID must be an alias (alias/name), ARN, or key ID` / `invalid KMS ARN format` | Malformed key identifier | Compare the format to [Supported Key Identifiers](#supported-key-identifiers) | Local |
| `AWS access key provided but secret key missing` | Half of a static credential pair configured | Set both `aws_access_key_id` and `aws_secret_access_key`, or neither | Local |
| `kms provider selected but optional dependency @aws-sdk/client-kms is not installed` | Optional TS dependency absent | `npm install @aws-sdk/client-kms` | Local |
| `failed to create AWS config: ...` | Profile or shared config file cannot be loaded | Check the profile name exists in `~/.aws/config` | Local |
| `... NoCredentialProviders` / `failed to refresh cached credentials` / `ExpiredToken` | No usable credentials in the chain, or expired session credentials | Re-authenticate (SSO login, refresh session); check [Credential Precedence](#credential-precedence) | Local |
| `... dial tcp` / `i/o timeout` / `no such host` | Network, proxy, or VPC endpoint cannot reach KMS | Confirm the region is correct and the KMS endpoint is reachable from this host | Local / network |
| `... AccessDeniedException` | IAM policy or key policy does not allow `kms:Sign` / `kms:GetPublicKey` | Compare the [IAM policy](#aws-iam-policy) to the key policy for the calling principal | Cloud |
| `... NotFoundException` | Key or alias does not exist in this region/account | Confirm the region; aliases are per region | Cloud |
| `... DisabledException` / `KMSInvalidStateException` | Key disabled or pending deletion | Ask the key owner to check the key state | Cloud |
| `... InvalidKeyUsageException` / `UnsupportedOperationException` | Key is not `SIGN_VERIFY`, or the signing algorithm does not match the key spec | Go signs with `ECDSA_SHA_512`; TS defaults to `ECDSA_SHA_256` (`GLASSBOX_KMS_SIGNING_ALGORITHM`) | Cloud |
| `... ThrottlingException` | Request rate above the KMS quota | Lower the signing rate; see [Retry Behavior](#retry-behavior) | Cloud |
| `KMS signing failed: response contained no Signature field` / `KMS GetPublicKey: response contained no PublicKey field` | KMS returned a success with an empty body (TS check) | Retry once; if it keeps happening, capture the AWS request ID | Cloud |

### Sharing errors safely

The wrapped error contains the full AWS SDK message, which can include the key
ARN, account ID, and the IAM principal ARN. Before posting logs in an issue or
chat:

- Replace key ARNs, key IDs, account IDs and principal ARNs with placeholders
  such as `arn:aws:kms:REGION:ACCOUNT_ID:key/KEY_ID`.
- Never paste `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`,
  or the `[audit.kms]` credential fields.
- Do not share request payloads or digests sent to `Sign`, or debug wire
  logs (for example SDK request logging), because they carry signed headers.
- The AWS request ID alone is safe to share and is enough for AWS support to
  trace a call.

## Retry Behavior

Glassbox has no KMS-specific retry logic. Neither `KMSSigner.Sign` (Go) nor
`KmsSigner.sign` (TS) retries in its own code. Each issues one `Sign` call and
returns the wrapped error on failure. `tests/kmsSigner.test.ts` asserts that a
rejected `Sign` call surfaces immediately as `KMS signing failed: ...`.

The only retries come from the AWS SDK client, which both implementations
create with default settings:

| | Go (`aws-sdk-go-v2`, `config.LoadDefaultConfig`) | TS (`@aws-sdk/client-kms`, `new KMSClient({ region })`) |
|---|---|---|
| Retry mode | `standard` | `standard` |
| Max attempts | 3 (1 initial + 2 retries) | 3 (1 initial + 2 retries) |
| Delay | Exponential backoff with jitter, capped at 20 s | Exponential backoff with jitter, capped at 20 s |
| Retried | Throttling errors, transient network/connection errors, HTTP 5xx | Throttling errors, transient network/connection errors, HTTP 5xx |
| Not retried | `AccessDeniedException`, `NotFoundException`, `DisabledException`, `KMSInvalidStateException`, `InvalidKeyUsageException`, validation errors and other 4xx client errors | Same |

The SDK's standard settings can be overridden through the usual AWS
environment, without changing Glassbox:

- `AWS_MAX_ATTEMPTS`: total attempts, including the first.
- `AWS_RETRY_MODE`: `standard` or `adaptive`.

Operational notes:

- A failed signing call can take up to roughly the sum of two backoff delays
  plus three request timeouts before the error is returned.
- The Go signer uses `context.Background()`, so there is no Glassbox-level
  deadline on a KMS call. Cancelling the CLI process is the only way to stop it
  early.
- `PublicKey()` (Go) calls `GetPublicKey` on every invocation and follows
  the same SDK retry rules.
- Missing `Signature`/`PublicKey` fields in a successful response (TS) are
  terminal and are not retried.

## Verification

To verify KMS signing is working:

```bash
# Sign an audit log
glassbox audit:sign --payload-file data.json --audit-log signed-audit.json

# Verify the signature
glassbox audit:verify --audit-log signed-audit.json
```