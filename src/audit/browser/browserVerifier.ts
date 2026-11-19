// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * browserVerifier.ts — Browser-safe audit log verification.
 *
 * Uses the Web Crypto API (SubtleCrypto) exclusively. No Node.js built-ins
 * (no 'crypto', no 'buffer') are imported. This file is safe to include in
 * any modern browser bundle (Chrome 37+, Firefox 34+, Safari 11+, Edge 12+).
 *
 * Supported algorithms:
 *   - Ed25519 (EdDSA) via SubtleCrypto.verify with algorithm "Ed25519"
 *     (Chrome 113+, Firefox 130+; older browsers will report
 *     AlgorithmNotSupported — the API surface reports this explicitly).
 *
 * Unsupported (Node-only):
 *   - PKCS#11 HSM signing (requires native pkcs11js module)
 *   - AWS KMS signing (requires @aws-sdk/client-kms)
 *
 * These are declared via ProviderCapabilities so callers can branch
 * gracefully rather than receiving an opaque error.
 */

import { canonicalStringify, buildAuditHashInput } from './canonicalize';

// ─── Capability reporting ─────────────────────────────────────────────────────

/**
 * Describes which signing/verification algorithms are available in the
 * current runtime.
 */
export interface ProviderCapabilities {
  /**
   * Whether Ed25519 signature verification is supported.
   * true in Chrome 113+, Firefox 130+; false in older browsers.
   */
  ed25519Verify: boolean;

  /**
   * PKCS#11 HSM requires native Node.js modules and is never available
   * in a browser context.
   */
  pkcs11: false;

  /**
   * AWS KMS requires @aws-sdk/client-kms (Node.js) and is never available
   * in a browser context.
   */
  awsKms: false;

  /**
   * SHA-256 hashing is universally available via SubtleCrypto.
   */
  sha256: boolean;
}

/**
 * Probes the current runtime for cryptographic capabilities.
 * Call this once at startup and cache the result.
 *
 * @example
 * const caps = await getProviderCapabilities();
 * if (!caps.ed25519Verify) {
 *   console.warn('Ed25519 not available; upgrade to Chrome 113+ or Firefox 130+');
 * }
 */
export async function getProviderCapabilities(): Promise<ProviderCapabilities> {
  const subtle = getSubtleCrypto();

  let ed25519Verify = false;
  let sha256 = false;

  if (subtle) {
    // Probe SHA-256
    try {
      await subtle.digest('SHA-256', new Uint8Array(1).buffer as ArrayBuffer);
      sha256 = true;
    } catch {
      sha256 = false;
    }

    // Probe Ed25519 — attempt to import a dummy key
    try {
      // Minimal valid SPKI for Ed25519 (32-byte all-zero key)
      const dummySpki = new Uint8Array([
        0x30, 0x2a, // SEQUENCE (42 bytes)
        0x30, 0x05, // SEQUENCE (5 bytes) — AlgorithmIdentifier
        0x06, 0x03, 0x2b, 0x65, 0x70, // OID 1.3.101.112 = Ed25519
        0x03, 0x21, // BIT STRING (33 bytes)
        0x00, // no unused bits
        // 32 bytes of key material (all zero — invalid but parseable)
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
      ]);
      await subtle.importKey(
        'spki',
        dummySpki,
        { name: 'Ed25519' },
        false,
        ['verify']
      );
      ed25519Verify = true;
    } catch {
      ed25519Verify = false;
    }
  }

  return {
    ed25519Verify,
    pkcs11: false,
    awsKms: false,
    sha256,
  };
}

// ─── Result types ─────────────────────────────────────────────────────────────

export interface BrowserVerificationResult {
  /** Overall pass/fail */
  valid: boolean;
  /** Hash (SHA-256 of canonical form) matched the embedded hash */
  hash_valid: boolean;
  /** Signature verified against the embedded public key */
  signature_valid: boolean;
  /**
   * If the algorithm is unsupported in this browser, this is set and
   * valid/signature_valid will both be false.
   */
  unsupported_algorithm?: string;
  /** Human-readable detail about what was checked */
  detail: string;
}

// ─── Verification ─────────────────────────────────────────────────────────────

/**
 * Verifies a signed audit log in the browser using the Web Crypto API.
 *
 * Only Ed25519+SHA256 is supported. PKCS#11 and KMS audit logs cannot be
 * verified in the browser; this function returns an explicit
 * `unsupported_algorithm` field in that case.
 *
 * @param auditLog - The full signed audit JSON object (parsed, not a string)
 * @returns BrowserVerificationResult with granular pass/fail information
 */
export async function verifyAuditLogBrowser(
  auditLog: Record<string, unknown>
): Promise<BrowserVerificationResult> {
  const subtle = getSubtleCrypto();
  if (!subtle) {
    return {
      valid: false,
      hash_valid: false,
      signature_valid: false,
      unsupported_algorithm: 'SubtleCrypto not available',
      detail: 'Web Crypto API is not available in this environment',
    };
  }

  const {
    trace,
    hash,
    signature,
    publicKey,
    algorithm,
    hardware_attestation,
    signatures,
  } = auditLog as Record<string, unknown>;

  // 1. Validate required fields
  if (!trace || typeof hash !== 'string' || typeof publicKey !== 'string') {
    return {
      valid: false,
      hash_valid: false,
      signature_valid: false,
      detail: 'audit log is missing required fields (trace, hash, or publicKey)',
    };
  }

  // 2. Reject unsupported algorithms explicitly
  const algoStr = typeof algorithm === 'string' ? algorithm : 'Ed25519+SHA256';
  if (!algoStr.includes('Ed25519')) {
    return {
      valid: false,
      hash_valid: false,
      signature_valid: false,
      unsupported_algorithm: algoStr,
      detail:
        `Algorithm "${algoStr}" is not supported in the browser bundle. ` +
        'Only Ed25519+SHA256 is available. PKCS#11 and KMS logs require the Node.js verifier.',
    };
  }

  // 3. Recompute hash
  const hashInput = buildAuditHashInput(
    trace as Record<string, unknown>,
    hardware_attestation as Record<string, unknown> | undefined
  );
  const encoder = new TextEncoder();
  const hashBytes = await subtle.digest('SHA-256', encoder.encode(hashInput).buffer as ArrayBuffer);
  const recalculatedHash = bufToHex(new Uint8Array(hashBytes));

  const hashValid = recalculatedHash === hash;
  if (!hashValid) {
    return {
      valid: false,
      hash_valid: false,
      signature_valid: false,
      detail: `hash mismatch: expected ${hash}, got ${recalculatedHash}`,
    };
  }

  // 4. Import the public key (SPKI PEM → CryptoKey)
  let cryptoKey: CryptoKey;
  try {
    const spkiDer = pemToDer(publicKey as string);
    cryptoKey = await subtle.importKey(
      'spki',
      spkiDer,
      { name: 'Ed25519' },
      false,
      ['verify']
    );
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    // Check whether the algorithm itself is unsupported
    if (
      msg.includes('not supported') ||
      msg.includes('unsupported') ||
      msg.includes('Algorithm')
    ) {
      return {
        valid: false,
        hash_valid: true,
        signature_valid: false,
        unsupported_algorithm: 'Ed25519',
        detail:
          'Ed25519 is not supported in this browser (requires Chrome 113+ or Firefox 130+). ' +
          'Use the Node.js verifier instead.',
      };
    }
    return {
      valid: false,
      hash_valid: true,
      signature_valid: false,
      detail: `public key import failed: ${msg}`,
    };
  }

  // 5. Verify signature(s)
  // The signed data is the UTF-8 encoding of the hex hash string,
  // matching the Node.js signer: Buffer.from(traceHash) where traceHash is hex.
  const signedData = encoder.encode(hash as string).buffer as ArrayBuffer;
  let signatureValid = false;

  if (Array.isArray(signatures) && (signatures as unknown[]).length > 0) {
    // Multi-signature log: all signatures must verify
    let allValid = true;
    for (const entry of signatures as Array<{
      signature: string;
      publicKey: string;
    }>) {
      if (!entry.signature || !entry.publicKey) {
        allValid = false;
        break;
      }
      try {
        const entrySig = hexToBuf(entry.signature);
        const entryKey = await subtle.importKey(
          'spki',
          pemToDer(entry.publicKey),
          { name: 'Ed25519' },
          false,
          ['verify']
        );
        const ok = await subtle.verify('Ed25519', entryKey, entrySig, signedData);
        if (!ok) {
          allValid = false;
          break;
        }
      } catch {
        allValid = false;
        break;
      }
    }
    signatureValid = allValid;
  } else if (typeof signature === 'string') {
    try {
      const sigBytes = hexToBuf(signature as string);
      signatureValid = await subtle.verify('Ed25519', cryptoKey, sigBytes, signedData);
    } catch {
      signatureValid = false;
    }
  }

  return {
    valid: hashValid && signatureValid,
    hash_valid: hashValid,
    signature_valid: signatureValid,
    detail: hashValid && signatureValid ? 'verification passed' : 'signature verification failed',
  };
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/** Resolves SubtleCrypto from the global, working in both browser and Node ≥ 16. */
function getSubtleCrypto(): SubtleCrypto | null {
  if (typeof globalThis !== 'undefined' && globalThis.crypto?.subtle) {
    return globalThis.crypto.subtle;
  }
  // Node.js 16+: globalThis.crypto is available but may not be set in older versions
  if (typeof crypto !== 'undefined' && (crypto as Crypto).subtle) {
    return (crypto as Crypto).subtle;
  }
  return null;
}

/** Converts a hex string to an ArrayBuffer. */
function hexToBuf(hex: string): ArrayBuffer {
  if (hex.length % 2 !== 0) {
    throw new Error(`invalid hex string length: ${hex.length}`);
  }
  const buf = new Uint8Array(hex.length / 2);
  for (let i = 0; i < hex.length; i += 2) {
    buf[i / 2] = parseInt(hex.substring(i, i + 2), 16);
  }
  return buf.buffer as ArrayBuffer;
}

/** Converts an ArrayBuffer / Uint8Array to a lowercase hex string. */
function bufToHex(buf: Uint8Array): string {
  return Array.from(buf)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/**
 * Strips PEM armor and decodes the base64 body into a DER ArrayBuffer.
 * Works without the Node.js Buffer global.
 */
function pemToDer(pem: string): ArrayBuffer {
  const lines = pem
    .split('\n')
    .filter((l) => !l.startsWith('-----'))
    .join('');
  const binary = atob(lines);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer as ArrayBuffer;
}

// Re-export canonicalize utilities for consumers
export { canonicalStringify, buildAuditHashInput } from './canonicalize';
