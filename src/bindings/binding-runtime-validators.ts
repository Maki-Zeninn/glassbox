// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0
//
// binding-runtime-validators.ts
//
// Runtime validators for all four external payload types that cross the
// Glassbox binding boundary:
//
//   1. Command inputs  (via validateCommandOptions from command-validators.ts)
//   2. Trace payloads  (validateTracePayload)
//   3. Audit records   (validateAuditRecord)
//   4. Session envelopes (validateSessionEnvelope)
//
// Every validator returns a RuntimeValidationResult with field-path diagnostics
// so callers can identify exactly which nested field failed and why.  Error
// codes are aligned with the Go stable codes in
// internal/bindings/runtime_validator.go so cross-language consumers share the
// same vocabulary.

/** Stable error codes for runtime validation failures. */
export type RuntimeValidationCode =
  | 'REQUIRED_FIELD_MISSING'
  | 'WRONG_TYPE'
  | 'INVALID_ENUM_VALUE'
  | 'INVALID_VALUE'
  | 'UNKNOWN_FIELD'
  | 'MUTUAL_EXCLUSION_VIOLATED';

/** A single field-level validation error with a dot-separated path. */
export interface RuntimeFieldError {
  /** Dot-separated path to the failing field, e.g. "trace.input.amount". */
  path: string;
  /** Human-readable explanation. */
  message: string;
  /** Stable code for automation. */
  code: RuntimeValidationCode;
}

/** Returned by every validator. */
export interface RuntimeValidationResult {
  valid: boolean;
  errors: RuntimeFieldError[];
}

/**
 * Validation mode controls how unknown fields are treated.
 *
 * - 'strict'     (default) — unknown fields produce UNKNOWN_FIELD errors.
 * - 'permissive' — unknown additive fields are silently ignored.  Use this
 *   when deserialising JSON from a newer Glassbox version that may contain
 *   additive fields not yet in this client's schema.
 */
export type ValidationMode = 'strict' | 'permissive';

// ─── Trace payload ─────────────────────────────────────────────────────────────

const TRACE_KNOWN_FIELDS = new Set(['input', 'state', 'events', 'timestamp', 'metadata']);

/**
 * Validates a Glassbox execution trace payload decoded from external JSON.
 *
 * Required fields: input (object), state (object), events (array), timestamp (ISO 8601).
 * Optional fields: metadata (object).
 */
export function validateTracePayload(
  raw: unknown,
  mode: ValidationMode = 'strict',
): RuntimeValidationResult {
  const errors: RuntimeFieldError[] = [];

  if (!isPlainObject(raw)) {
    return { valid: false, errors: [{ path: 'trace', message: 'must be a plain object', code: 'WRONG_TYPE' }] };
  }

  if (mode === 'strict') {
    for (const k of Object.keys(raw)) {
      if (!TRACE_KNOWN_FIELDS.has(k)) {
        errors.push({ path: 'trace.' + k, message: 'unknown field "' + k + '"', code: 'UNKNOWN_FIELD' });
      }
    }
  }

  requireObject(raw, 'input', 'trace.input', errors);
  if (isPlainObject(raw['input'])) {
    checkNoNaNInf(raw['input'], 'trace.input', errors);
  }

  requireObject(raw, 'state', 'trace.state', errors);
  if (isPlainObject(raw['state'])) {
    checkNoNaNInf(raw['state'], 'trace.state', errors);
  }

  requireArray(raw, 'events', 'trace.events', errors);

  requireISOTimestamp(raw, 'timestamp', 'trace.timestamp', errors);

  if ('metadata' in raw && raw['metadata'] !== null && raw['metadata'] !== undefined) {
    if (!isPlainObject(raw['metadata'])) {
      errors.push({ path: 'trace.metadata', message: 'must be a plain object when present', code: 'WRONG_TYPE' });
    }
  }

  return { valid: errors.length === 0, errors };
}

// ─── Audit record ──────────────────────────────────────────────────────────────

const AUDIT_KNOWN_FIELDS = new Set([
  'trace', 'hash', 'signature', 'algorithm', 'publicKey', 'signer', 'hardware_attestation',
]);
const VALID_ALGORITHMS = new Set(['Ed25519', 'PKCS11-Ed25519', 'KMS-Ed25519']);

/**
 * Validates a Glassbox signed audit record decoded from external JSON.
 *
 * Required fields: trace (object), hash (64-char hex), signature, algorithm,
 * publicKey, signer (object with provider).
 * Optional fields: hardware_attestation.
 *
 * The trace sub-object is validated recursively with the same mode.
 */
export function validateAuditRecord(
  raw: unknown,
  mode: ValidationMode = 'strict',
): RuntimeValidationResult {
  const errors: RuntimeFieldError[] = [];

  if (!isPlainObject(raw)) {
    return { valid: false, errors: [{ path: 'audit', message: 'must be a plain object', code: 'WRONG_TYPE' }] };
  }

  if (mode === 'strict') {
    for (const k of Object.keys(raw)) {
      if (!AUDIT_KNOWN_FIELDS.has(k)) {
        errors.push({ path: 'audit.' + k, message: 'unknown field "' + k + '"', code: 'UNKNOWN_FIELD' });
      }
    }
  }

  // trace (object, validated recursively)
  if (!('trace' in raw)) {
    errors.push({ path: 'audit.trace', message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (!isPlainObject(raw['trace'])) {
    errors.push({ path: 'audit.trace', message: 'must be an object', code: 'WRONG_TYPE' });
  } else {
    const inner = validateTracePayload(raw['trace'], mode);
    errors.push(...inner.errors);
  }

  // hash (64-char hex)
  requireNonEmptyString(raw, 'hash', 'audit.hash', errors);
  if (typeof raw['hash'] === 'string' && raw['hash'].length > 0 && raw['hash'].length !== 64) {
    errors.push({ path: 'audit.hash', message: 'must be a 64-character hex SHA-256 digest, got length ' + raw['hash'].length, code: 'INVALID_VALUE' });
  }

  requireNonEmptyString(raw, 'signature', 'audit.signature', errors);

  // algorithm
  if (!('algorithm' in raw)) {
    errors.push({ path: 'audit.algorithm', message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (typeof raw['algorithm'] !== 'string') {
    errors.push({ path: 'audit.algorithm', message: 'must be a string', code: 'WRONG_TYPE' });
  } else if (!VALID_ALGORITHMS.has(raw['algorithm'])) {
    errors.push({ path: 'audit.algorithm', message: 'unsupported algorithm "' + raw['algorithm'] + '"; expected one of ' + [...VALID_ALGORITHMS].join(', '), code: 'INVALID_ENUM_VALUE' });
  }

  requireNonEmptyString(raw, 'publicKey', 'audit.publicKey', errors);

  // signer
  if (!('signer' in raw)) {
    errors.push({ path: 'audit.signer', message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (!isPlainObject(raw['signer'])) {
    errors.push({ path: 'audit.signer', message: 'must be an object', code: 'WRONG_TYPE' });
  } else {
    if (!('provider' in raw['signer'])) {
      errors.push({ path: 'audit.signer.provider', message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
    } else if (typeof raw['signer']['provider'] !== 'string') {
      errors.push({ path: 'audit.signer.provider', message: 'must be a string', code: 'WRONG_TYPE' });
    }
  }

  return { valid: errors.length === 0, errors };
}

// ─── Session envelope ──────────────────────────────────────────────────────────

const SESSION_KNOWN_FIELDS = new Set([
  'session_id', 'version', 'created_at', 'network', 'tx_hash',
  'status', 'snapshot', 'trace', 'metadata',
]);
const VALID_NETWORKS = new Set(['mainnet', 'testnet', 'futurenet']);
const VALID_STATUSES = new Set(['success', 'failed', 'pending']);

/**
 * Validates a Glassbox session envelope decoded from external JSON.
 *
 * Required fields: session_id, version, created_at, network, status.
 * Optional fields: tx_hash, snapshot, trace (validated recursively), metadata.
 */
export function validateSessionEnvelope(
  raw: unknown,
  mode: ValidationMode = 'strict',
): RuntimeValidationResult {
  const errors: RuntimeFieldError[] = [];

  if (!isPlainObject(raw)) {
    return { valid: false, errors: [{ path: 'session', message: 'must be a plain object', code: 'WRONG_TYPE' }] };
  }

  if (mode === 'strict') {
    for (const k of Object.keys(raw)) {
      if (!SESSION_KNOWN_FIELDS.has(k)) {
        errors.push({ path: 'session.' + k, message: 'unknown field "' + k + '"', code: 'UNKNOWN_FIELD' });
      }
    }
  }

  requireNonEmptyString(raw, 'session_id', 'session.session_id', errors);
  requireNonEmptyString(raw, 'version', 'session.version', errors);
  requireISOTimestamp(raw, 'created_at', 'session.created_at', errors);

  // network
  if (!('network' in raw)) {
    errors.push({ path: 'session.network', message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (typeof raw['network'] !== 'string') {
    errors.push({ path: 'session.network', message: 'must be a string', code: 'WRONG_TYPE' });
  } else if (!VALID_NETWORKS.has(raw['network'])) {
    errors.push({ path: 'session.network', message: 'unknown network "' + raw['network'] + '"; expected mainnet, testnet, or futurenet', code: 'INVALID_ENUM_VALUE' });
  }

  // status
  if (!('status' in raw)) {
    errors.push({ path: 'session.status', message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (typeof raw['status'] !== 'string') {
    errors.push({ path: 'session.status', message: 'must be a string', code: 'WRONG_TYPE' });
  } else if (!VALID_STATUSES.has(raw['status'])) {
    errors.push({ path: 'session.status', message: 'unknown status "' + raw['status'] + '"; expected success, failed, or pending', code: 'INVALID_ENUM_VALUE' });
  }

  // optional trace
  if ('trace' in raw && raw['trace'] !== null && raw['trace'] !== undefined) {
    if (!isPlainObject(raw['trace'])) {
      errors.push({ path: 'session.trace', message: 'must be an object when present', code: 'WRONG_TYPE' });
    } else {
      const inner = validateTracePayload(raw['trace'], mode);
      errors.push(...inner.errors);
    }
  }

  return { valid: errors.length === 0, errors };
}

// ─── Convenience: deserialise and validate ─────────────────────────────────────

/**
 * Parse a JSON string and validate it as a trace payload.
 * Returns both the parsed object and the validation result.
 */
export function parseAndValidateTrace(
  json: string,
  mode: ValidationMode = 'strict',
): { raw: unknown; result: RuntimeValidationResult } {
  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch (e) {
    return { raw: undefined, result: { valid: false, errors: [{ path: '', message: 'invalid JSON: ' + String(e), code: 'WRONG_TYPE' }] } };
  }
  return { raw, result: validateTracePayload(raw, mode) };
}

/**
 * Parse a JSON string and validate it as an audit record.
 */
export function parseAndValidateAuditRecord(
  json: string,
  mode: ValidationMode = 'strict',
): { raw: unknown; result: RuntimeValidationResult } {
  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch (e) {
    return { raw: undefined, result: { valid: false, errors: [{ path: '', message: 'invalid JSON: ' + String(e), code: 'WRONG_TYPE' }] } };
  }
  return { raw, result: validateAuditRecord(raw, mode) };
}

/**
 * Parse a JSON string and validate it as a session envelope.
 */
export function parseAndValidateSessionEnvelope(
  json: string,
  mode: ValidationMode = 'strict',
): { raw: unknown; result: RuntimeValidationResult } {
  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch (e) {
    return { raw: undefined, result: { valid: false, errors: [{ path: '', message: 'invalid JSON: ' + String(e), code: 'WRONG_TYPE' }] } };
  }
  return { raw, result: validateSessionEnvelope(raw, mode) };
}

// ─── Internal helpers ──────────────────────────────────────────────────────────

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function requireObject(
  obj: Record<string, unknown>,
  key: string,
  path: string,
  errors: RuntimeFieldError[],
): void {
  if (!(key in obj)) {
    errors.push({ path, message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (!isPlainObject(obj[key])) {
    errors.push({ path, message: 'must be a plain object', code: 'WRONG_TYPE' });
  }
}

function requireArray(
  obj: Record<string, unknown>,
  key: string,
  path: string,
  errors: RuntimeFieldError[],
): void {
  if (!(key in obj)) {
    errors.push({ path, message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (!Array.isArray(obj[key])) {
    errors.push({ path, message: 'must be an array', code: 'WRONG_TYPE' });
  }
}

function requireNonEmptyString(
  obj: Record<string, unknown>,
  key: string,
  path: string,
  errors: RuntimeFieldError[],
): void {
  if (!(key in obj)) {
    errors.push({ path, message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
  } else if (typeof obj[key] !== 'string') {
    errors.push({ path, message: 'must be a string', code: 'WRONG_TYPE' });
  } else if ((obj[key] as string).trim() === '') {
    errors.push({ path, message: 'must not be empty', code: 'INVALID_VALUE' });
  }
}

function requireISOTimestamp(
  obj: Record<string, unknown>,
  key: string,
  path: string,
  errors: RuntimeFieldError[],
): void {
  if (!(key in obj)) {
    errors.push({ path, message: 'required field missing', code: 'REQUIRED_FIELD_MISSING' });
    return;
  }
  if (typeof obj[key] !== 'string' || (obj[key] as string).trim() === '') {
    errors.push({ path, message: 'must be a non-empty string', code: 'WRONG_TYPE' });
    return;
  }
  const d = new Date(obj[key] as string);
  if (isNaN(d.getTime())) {
    errors.push({ path, message: 'not a valid ISO 8601 timestamp: "' + obj[key] + '"', code: 'INVALID_VALUE' });
  }
}

function checkNoNaNInf(
  value: unknown,
  path: string,
  errors: RuntimeFieldError[],
): void {
  if (typeof value === 'number') {
    if (isNaN(value)) {
      errors.push({ path, message: 'NaN cannot be serialised to JSON', code: 'INVALID_VALUE' });
    } else if (!isFinite(value)) {
      errors.push({ path, message: 'Infinity cannot be serialised to JSON', code: 'INVALID_VALUE' });
    }
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, i) => checkNoNaNInf(item, path + '[' + i + ']', errors));
    return;
  }
  if (isPlainObject(value)) {
    for (const [k, v] of Object.entries(value)) {
      checkNoNaNInf(v, path ? path + '.' + k : k, errors);
    }
  }
}
