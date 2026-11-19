// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * browser-audit-verify.test.ts
 *
 * Tests for the browser-safe audit verification bundle.
 * These tests run under Jest / Node.js, which exposes globalThis.crypto
 * (Node 16+) so the Web Crypto path is exercised.
 *
 * Issue #595: Add browser-safe audit verification bundle
 */

import { generateKeyPairSync, sign as nodeSign, createHash } from 'crypto';
import stringify from 'fast-json-stable-stringify';

import {
  verifyAuditLogBrowser,
  getProviderCapabilities,
  canonicalStringify,
  buildAuditHashInput,
} from '../src/audit/browser';

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Build a minimal signed audit log using Node.js crypto (the "reference" signer). */
function buildSignedLog(
  trace: Record<string, unknown>,
  overrides: Partial<Record<string, unknown>> = {}
) {
  const { publicKey: pubPem, privateKey: privPem } = generateKeyPairSync('ed25519', {
    publicKeyEncoding: { type: 'spki', format: 'pem' },
    privateKeyEncoding: { type: 'pkcs8', format: 'pem' },
  });

  const canonical = stringify({ trace });
  const hash = createHash('sha256').update(canonical).digest('hex');
  const sigBuf = nodeSign(null, Buffer.from(hash), privPem);

  return {
    trace,
    hash,
    signature: sigBuf.toString('hex'),
    algorithm: 'Ed25519+SHA256',
    publicKey: pubPem,
    signer: { provider: 'software' },
    ...overrides,
  };
}

// ─── canonicalStringify ───────────────────────────────────────────────────────

describe('canonicalStringify (browser/canonicalize)', () => {
  it('sorts object keys lexicographically', () => {
    const result = canonicalStringify({ z: 1, a: 2, m: 3 });
    expect(result).toBe('{"a":2,"m":3,"z":1}');
  });

  it('handles nested objects', () => {
    const result = canonicalStringify({ b: { y: 1, x: 2 }, a: true });
    expect(result).toBe('{"a":true,"b":{"x":2,"y":1}}');
  });

  it('preserves array order', () => {
    const result = canonicalStringify([3, 1, 2]);
    expect(result).toBe('[3,1,2]');
  });

  it('serialises NaN as null', () => {
    expect(canonicalStringify(NaN)).toBe('null');
  });

  it('serialises Infinity as null', () => {
    expect(canonicalStringify(Infinity)).toBe('null');
    expect(canonicalStringify(-Infinity)).toBe('null');
  });

  it('produces the same output as fast-json-stable-stringify for valid payloads', () => {
    const payload = {
      z: 'last',
      a: [1, 2, 3],
      m: { nested_b: false, nested_a: 42 },
    };
    expect(canonicalStringify(payload)).toBe(stringify(payload));
  });

  it('is deterministic across multiple calls', () => {
    const payload = { events: ['A', 'B'], input: { x: 1 }, state: {} };
    const first = canonicalStringify(payload);
    const second = canonicalStringify(payload);
    expect(first).toBe(second);
  });
});

// ─── buildAuditHashInput ──────────────────────────────────────────────────────

describe('buildAuditHashInput', () => {
  it('wraps trace under "trace" key without attestation', () => {
    const trace = { input: {}, state: {}, events: [] };
    const result = buildAuditHashInput(trace as Record<string, unknown>);
    const expected = stringify({ trace });
    expect(result).toBe(expected);
  });

  it('includes hardware_attestation when provided', () => {
    const trace = { input: {}, state: {}, events: [] };
    const attest = { token_info: 'MockHSM', key_non_exportable: true, certificates: [] };
    const result = buildAuditHashInput(
      trace as Record<string, unknown>,
      attest as Record<string, unknown>
    );
    const expected = stringify({ trace, hardware_attestation: attest });
    expect(result).toBe(expected);
  });
});

// ─── getProviderCapabilities ──────────────────────────────────────────────────

describe('getProviderCapabilities', () => {
  it('reports sha256 as available (Node 16+ has SubtleCrypto)', async () => {
    const caps = await getProviderCapabilities();
    expect(caps.sha256).toBe(true);
  });

  it('always reports pkcs11 as false', async () => {
    const caps = await getProviderCapabilities();
    expect(caps.pkcs11).toBe(false);
  });

  it('always reports awsKms as false', async () => {
    const caps = await getProviderCapabilities();
    expect(caps.awsKms).toBe(false);
  });

  it('returns a capabilities object with the expected shape', async () => {
    const caps = await getProviderCapabilities();
    expect(typeof caps.ed25519Verify).toBe('boolean');
    expect(typeof caps.sha256).toBe('boolean');
  });
});

// ─── verifyAuditLogBrowser ────────────────────────────────────────────────────

describe('verifyAuditLogBrowser', () => {
  it('verifies a correctly signed audit log', async () => {
    const trace = {
      input: { amount: 100 },
      state: { balance: 500 },
      events: ['TRANSFER'],
      timestamp: '2026-01-01T00:00:00.000Z',
    };
    const log = buildSignedLog(trace as Record<string, unknown>);
    const result = await verifyAuditLogBrowser(log);

    expect(result.hash_valid).toBe(true);

    // Ed25519 verify requires Chrome 113+ / Node 18+; skip signature check
    // on environments where it's not supported, but hash must always pass.
    if (result.unsupported_algorithm) {
      expect(result.unsupported_algorithm).toBe('Ed25519');
      expect(result.detail).toMatch(/Ed25519/);
    } else {
      expect(result.signature_valid).toBe(true);
      expect(result.valid).toBe(true);
    }
  });

  it('detects hash tampering', async () => {
    const trace = {
      input: { amount: 100 },
      state: {},
      events: [],
      timestamp: '2026-01-01T00:00:00.000Z',
    };
    const log = buildSignedLog(trace as Record<string, unknown>);
    // Tamper with the trace
    (log as Record<string, unknown>).trace = {
      ...trace,
      input: { amount: 999 },
    };

    const result = await verifyAuditLogBrowser(log);
    expect(result.hash_valid).toBe(false);
    expect(result.valid).toBe(false);
  });

  it('returns unsupported_algorithm for non-Ed25519 logs', async () => {
    const log = buildSignedLog(
      { input: {}, state: {}, events: [], timestamp: '2026-01-01T00:00:00.000Z' },
      { algorithm: 'PKCS11+SHA256' }
    );
    const result = await verifyAuditLogBrowser(log);

    expect(result.unsupported_algorithm).toBe('PKCS11+SHA256');
    expect(result.valid).toBe(false);
    expect(result.detail).toMatch(/PKCS#11|KMS|Node\.js/i);
  });

  it('returns detail message for missing required fields', async () => {
    const result = await verifyAuditLogBrowser({});
    expect(result.valid).toBe(false);
    expect(result.detail).toMatch(/missing/i);
  });

  it('rejects a log with mismatched hash (without touching signature)', async () => {
    const trace = {
      input: { x: 1 },
      state: {},
      events: [],
      timestamp: '2026-01-01T00:00:00.000Z',
    };
    const log = buildSignedLog(trace as Record<string, unknown>);
    // Replace hash with a wrong value (leave signature intact)
    (log as Record<string, unknown>).hash = 'aaaa' + log.hash.slice(4);

    const result = await verifyAuditLogBrowser(log);
    expect(result.hash_valid).toBe(false);
    expect(result.valid).toBe(false);
  });

  it('excludes native module imports — no pkcs11js import in bundle', () => {
    // Structural check: the browser module file must not import pkcs11js or aws-sdk.
    // We check the TypeScript source file, not the compiled output.
    const fs = require('fs');
    const path = require('path');
    const browserVerifierSrc = fs.readFileSync(
      path.resolve(__dirname, '../src/audit/browser/browserVerifier.ts'),
      'utf8'
    );
    // Must not have an import statement for native/Node-only modules
    expect(browserVerifierSrc).not.toMatch(/^import.*pkcs11/m);
    expect(browserVerifierSrc).not.toMatch(/^import.*aws-sdk/m);
    expect(browserVerifierSrc).not.toMatch(/^import.*['"]crypto['"]/m);
    expect(browserVerifierSrc).not.toMatch(/require\s*\(\s*['"]pkcs11js['"]\s*\)/);
  });
});
