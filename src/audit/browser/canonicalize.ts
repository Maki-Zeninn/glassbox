// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * canonicalize.ts — Pure canonicalization for audit log payloads.
 *
 * This module is browser-safe: it has no imports from Node.js built-ins
 * (no 'crypto', no 'fs', no 'path'). It can be bundled for the browser
 * without any polyfills.
 *
 * Algorithm: RFC 8785 / fast-json-stable-stringify-compatible key sort.
 * Keys are sorted lexicographically at every nesting level; arrays
 * preserve insertion order.
 */

/**
 * Produces a deterministic JSON string from an arbitrary value.
 * Keys at every level are sorted lexicographically.
 * NaN and Infinity are serialised as null (matching JSON.stringify semantics).
 */
export function canonicalStringify(value: unknown): string {
  return internalStringify(value);
}

function internalStringify(value: unknown): string {
  if (value === null || value === undefined) {
    return 'null';
  }

  if (typeof value === 'boolean') {
    return value ? 'true' : 'false';
  }

  if (typeof value === 'number') {
    if (!isFinite(value)) {
      // NaN / Infinity are not representable in JSON
      return 'null';
    }
    return String(value);
  }

  if (typeof value === 'string') {
    return JSON.stringify(value);
  }

  if (Array.isArray(value)) {
    const items = value.map((item) => internalStringify(item));
    return '[' + items.join(',') + ']';
  }

  if (typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    const keys = Object.keys(obj).sort();
    const pairs = keys.map(
      (k) => JSON.stringify(k) + ':' + internalStringify(obj[k])
    );
    return '{' + pairs.join(',') + '}';
  }

  // Functions, symbols, etc. — omit (JSON.stringify behaviour)
  return 'null';
}

/**
 * Computes the canonical hash input string for an audit log payload.
 * Mirrors the hashing logic in AuditLogger.generateLog so that browser
 * verification produces the same hash as the Node.js signer.
 */
export function buildAuditHashInput(
  trace: Record<string, unknown>,
  hardwareAttestation?: Record<string, unknown>
): string {
  const input = hardwareAttestation
    ? { trace, hardware_attestation: hardwareAttestation }
    : { trace };
  return canonicalStringify(input);
}
