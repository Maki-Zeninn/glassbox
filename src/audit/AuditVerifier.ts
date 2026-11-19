// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { verify, createHash, X509Certificate } from 'crypto';
import { canonicalizeJSON } from './canonical-json';
import type { HardwareAttestation } from './signing/types';

export interface TrustPolicy {
  /** Configured trust store / root CA certificates (PEM strings, fingerprints, or issuer/subject strings) */
  trustRoots?: string[];
  /** Allowed issuer Common Names, Orgs, or Subject identifiers */
  allowedIssuers?: string[];
  /** Check certificate validity periods (notBefore / notAfter) */
  checkValidity?: boolean;
  /** Custom reference time for validity evaluation (defaults to log timestamp or current Date) */
  currentTime?: string | Date | number;
  /** Revoked certificate serial numbers or fingerprints */
  revokedCertificates?: string[];
  /** Revocation policy mode ('none' | 'strict') */
  revocationPolicy?: 'none' | 'strict';
  /** Require hardware key to be non-exportable (default: false) */
  requireNonExportable?: boolean;
}

export interface PolicyVerification {
  /** Overall policy evaluation pass/fail */
  valid: boolean;
  /** Trust root check passed */
  trust_root_valid: boolean;
  /** Issuer allowlist check passed */
  issuer_allowed: boolean;
  /** Validity window check passed */
  validity_valid: boolean;
  /** Revocation check passed */
  revocation_valid: boolean;
  /** Specific issues/reasons for policy evaluation failure */
  issues: string[];
  /** Identified untrusted issuers, if any */
  untrusted_issuers: string[];
}

export interface VerificationResult {
  /** Overall pass/fail */
  valid: boolean;
  /** Hash integrity check passed */
  hash_valid: boolean;
  /** Signature verification passed */
  signature_valid: boolean;
  /** Hardware attestation verification result (if present) */
  attestation?: AttestationVerification;
  /** Trust policy evaluation result (if present) */
  policy?: PolicyVerification;
}

export interface AttestationVerification {
  /** Whether the attestation chain was present */
  present: boolean;
  /** Whether the certificate chain validates (each cert signed by the next) */
  chain_valid: boolean;
  /** Whether the signing key is marked as non-exportable */
  key_non_exportable: boolean;
  /** Token identification string */
  token_info: string;
  /** Number of certificates in the chain */
  chain_length: number;
  /** Any issues encountered */
  issues: string[];
}

type SignatureEntry = {
  signature: string;
  publicKey: string;
};

/**
 * Verifies a signed audit log, including hardware attestation and trust policy if present.
 *
 * @param auditLog  The full signed audit JSON object
 * @param publicKeyPEM  Optional external public key to verify against
 * @param trustPolicy  Optional trust policy for issuer, root, expiration, and revocation checks
 * @returns boolean for backward compatibility (true = valid)
 */
export const verifyAuditLog = (
  auditLog: any,
  publicKeyPEM?: string,
  trustPolicy?: TrustPolicy
): boolean => {
  const result = verifyAuditLogDetailed(auditLog, publicKeyPEM, trustPolicy);
  return result.valid;
};

/**
 * Detailed verification that returns granular results, including
 * hardware attestation chain validation and trust policy evaluation.
 */
export const verifyAuditLogDetailed = (
  auditLog: any,
  publicKeyPEM?: string,
  trustPolicy?: TrustPolicy
): VerificationResult => {
  const { trace, hash, signature, hardware_attestation, signatures } = auditLog;

  // 1. Re-calculate the deterministic string
  // Must match the hashing logic in AuditLogger:
  // if attestation is present, include it in the hash input.
  const hashInput = hardware_attestation
    ? { trace, hardware_attestation }
    : { trace };
  const canonicalString = canonicalizeJSON(hashInput);

  // 2. Re-calculate the hash
  const recalculatedHash = createHash('sha256').update(canonicalString).digest('hex');

  const hashValid = recalculatedHash === hash;
  if (!hashValid) {
    const attResult = hardware_attestation
      ? buildAttestationResult(hardware_attestation, ['skipped: hash mismatch'])
      : undefined;
    const policyResult = trustPolicy && Object.keys(trustPolicy).length > 0
      ? evaluateTrustPolicy(auditLog, trustPolicy)
      : undefined;
    return {
      valid: false,
      hash_valid: false,
      signature_valid: false,
      attestation: attResult,
      policy: policyResult,
    };
  }

  // 3. Verify signature(s)
  let signatureValid = false;
  if (Array.isArray(signatures) && signatures.length > 0) {
    const entries: SignatureEntry[] = signatures;
    if (publicKeyPEM) {
      const match = entries.find((entry) => entry.publicKey === publicKeyPEM);
      if (!match) {
        signatureValid = false;
      } else {
        signatureValid = verify(
          null,
          Buffer.from(hash),
          publicKeyPEM,
          Buffer.from(match.signature, 'hex')
        );
      }
    } else {
      signatureValid = entries.every((entry) => {
        if (!entry.publicKey || !entry.signature) return false;
        return verify(
          null,
          Buffer.from(hash),
          entry.publicKey,
          Buffer.from(entry.signature, 'hex')
        );
      });
    }
  } else {
    const keyToUse = publicKeyPEM ?? auditLog.publicKey;
    if (!keyToUse) {
      const policyResult = trustPolicy && Object.keys(trustPolicy).length > 0
        ? evaluateTrustPolicy(auditLog, trustPolicy)
        : undefined;
      return {
        valid: false,
        hash_valid: true,
        signature_valid: false,
        policy: policyResult,
      };
    }

    try {
      signatureValid = verify(
        null,
        Buffer.from(hash),
        keyToUse,
        Buffer.from(signature, 'hex')
      );
    } catch {
      signatureValid = false;
    }
  }

  // 4. Verify attestation chain if present (cryptographic verification)
  let attestationResult: AttestationVerification | undefined;
  if (hardware_attestation) {
    attestationResult = verifyAttestationChain(hardware_attestation);
  }

  const attestationOk = !attestationResult || attestationResult.chain_valid;

  // 5. Evaluate Trust Policy if configured
  let policyResult: PolicyVerification | undefined;
  if (trustPolicy && Object.keys(trustPolicy).length > 0) {
    policyResult = evaluateTrustPolicy(auditLog, trustPolicy);
  }

  const policyOk = !policyResult || policyResult.valid;

  return {
    valid: hashValid && signatureValid && attestationOk && policyOk,
    hash_valid: hashValid,
    signature_valid: signatureValid,
    attestation: attestationResult,
    policy: policyResult,
  };
};

interface CertInfo {
  pem?: string;
  issuer: string;
  subject: string;
  serialNumber: string;
  validFrom?: Date;
  validTo?: Date;
}

function extractCertInfos(auditLog: any): CertInfo[] {
  const list: CertInfo[] = [];
  const rawCerts = auditLog.hardware_attestation?.certificates || auditLog.provenance?.certificate_chain || [];
  for (const c of rawCerts) {
    if (typeof c === 'string') {
      const info: CertInfo = { pem: c, issuer: '', subject: '', serialNumber: '' };
      try {
        const x509 = new X509Certificate(c);
        info.issuer = x509.issuer;
        info.subject = x509.subject;
        info.serialNumber = x509.serialNumber;
        if (x509.validFrom) info.validFrom = new Date(x509.validFrom);
        if (x509.validTo) info.validTo = new Date(x509.validTo);
      } catch {}
      list.push(info);
    } else if (c && typeof c === 'object') {
      const info: CertInfo = {
        pem: c.pem,
        issuer: c.issuer || '',
        subject: c.subject || '',
        serialNumber: c.serial || c.serialNumber || '',
        validFrom: c.validFrom ? new Date(c.validFrom) : undefined,
        validTo: c.validTo ? new Date(c.validTo) : undefined,
      };
      if (c.pem) {
        try {
          const x509 = new X509Certificate(c.pem);
          if (x509.issuer) info.issuer = x509.issuer;
          if (x509.subject) info.subject = x509.subject;
          if (x509.serialNumber) info.serialNumber = x509.serialNumber;
          if (x509.validFrom) info.validFrom = new Date(x509.validFrom);
          if (x509.validTo) info.validTo = new Date(x509.validTo);
        } catch {}
      }
      list.push(info);
    }
  }
  return list;
}

/**
 * Evaluates configured trust policy against audit log certificates/attestation.
 * Explicitly separates policy evaluation outcomes from cryptographic validity.
 */
export function evaluateTrustPolicy(auditLog: any, policy: TrustPolicy): PolicyVerification {
  const certs = extractCertInfos(auditLog);
  const issues: string[] = [];
  const untrustedIssuers: string[] = [];

  let trustRootValid = true;
  let issuerAllowed = true;
  let validityValid = true;
  let revocationValid = true;

  // 1. Trust Store / Roots Check
  if (policy.trustRoots && policy.trustRoots.length > 0) {
    if (certs.length === 0) {
      trustRootValid = false;
      issues.push('No certificates present to verify against configured trust roots');
    } else {
      let rootFound = false;
      for (const cert of certs) {
        for (const root of policy.trustRoots) {
          const trimmedRoot = root.trim();
          if (
            (cert.pem && cert.pem.trim() === trimmedRoot) ||
            (cert.issuer && cert.issuer.includes(trimmedRoot)) ||
            (cert.subject && cert.subject.includes(trimmedRoot)) ||
            (cert.serialNumber && cert.serialNumber === trimmedRoot)
          ) {
            rootFound = true;
            break;
          }
          try {
            if (cert.pem) {
              const rootX509 = new X509Certificate(root);
              const certX509 = new X509Certificate(cert.pem);
              if (rootX509.checkIssued(certX509) || rootX509.fingerprint === certX509.fingerprint) {
                rootFound = true;
                break;
              }
            }
          } catch {}
        }
        if (rootFound) break;
      }
      if (!rootFound) {
        trustRootValid = false;
        issues.push('Chain root is not in configured trust roots');
      }
    }
  }

  // 2. Issuer Allowlist Check
  if (policy.allowedIssuers && policy.allowedIssuers.length > 0) {
    if (certs.length === 0) {
      issuerAllowed = false;
      issues.push('No certificates present to evaluate issuer allowlist');
    } else {
      for (const cert of certs) {
        const issuerName = cert.issuer;
        if (!issuerName) {
          issuerAllowed = false;
          issues.push('Certificate missing issuer information');
          continue;
        }
        const isAllowed = policy.allowedIssuers.some((allowed) => {
          const trimmed = allowed.trim();
          return issuerName === trimmed || issuerName.includes(trimmed);
        });
        if (!isAllowed) {
          issuerAllowed = false;
          if (!untrustedIssuers.includes(issuerName)) {
            untrustedIssuers.push(issuerName);
          }
          issues.push(`Untrusted issuer '${issuerName}' is not in allowed issuers list`);
        }
      }
    }
  }

  // 3. Validity Window Check
  if (policy.checkValidity) {
    const refTime = policy.currentTime
      ? new Date(policy.currentTime)
      : (auditLog.timestamp ? new Date(auditLog.timestamp) : new Date());

    if (certs.length === 0) {
      validityValid = false;
      issues.push('No certificates present to check validity window');
    } else {
      for (const cert of certs) {
        if (cert.validFrom && refTime < cert.validFrom) {
          validityValid = false;
          issues.push(`Certificate '${cert.subject || cert.serialNumber}' is not yet valid (validFrom: ${cert.validFrom.toISOString()})`);
        }
        if (cert.validTo && refTime > cert.validTo) {
          validityValid = false;
          issues.push(`Certificate '${cert.subject || cert.serialNumber}' has expired (validTo: ${cert.validTo.toISOString()})`);
        }
      }
    }
  }

  // 4. Revocation Policy Check
  if (policy.revokedCertificates && policy.revokedCertificates.length > 0) {
    for (const cert of certs) {
      const serialLower = (cert.serialNumber || '').toLowerCase();
      const isRevoked = policy.revokedCertificates.some((rev) => {
        const revLower = rev.trim().toLowerCase();
        return revLower === serialLower || (cert.pem && cert.pem.toLowerCase().includes(revLower));
      });
      if (isRevoked) {
        revocationValid = false;
        issues.push(`Certificate '${cert.subject || cert.serialNumber}' is revoked (serial: ${cert.serialNumber})`);
      }
    }
  }

  // 5. Require Non-Exportable Key
  if (policy.requireNonExportable && auditLog.hardware_attestation && !auditLog.hardware_attestation.key_non_exportable) {
    issues.push('Private key is not marked as non-exportable on hardware token');
  }

  const valid = trustRootValid && issuerAllowed && validityValid && revocationValid && issues.length === 0;

  return {
    valid,
    trust_root_valid: trustRootValid,
    issuer_allowed: issuerAllowed,
    validity_valid: validityValid,
    revocation_valid: revocationValid,
    issues,
    untrusted_issuers: untrustedIssuers,
  };
}

function verifyAttestationChain(attestation: HardwareAttestation): AttestationVerification {
  const issues: string[] = [];
  const certs = attestation.certificates;

  if (!certs || certs.length === 0) {
    return {
      present: false,
      chain_valid: false,
      key_non_exportable: attestation.key_non_exportable,
      token_info: attestation.token_info,
      chain_length: 0,
      issues: ['no certificates in attestation chain'],
    };
  }

  // Validate each certificate can be parsed
  const parsed: X509Certificate[] = [];
  for (let i = 0; i < certs.length; i++) {
    try {
      parsed.push(new X509Certificate(certs[i].pem));
    } catch (e) {
      issues.push(`certificate[${i}]: failed to parse: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // Validate chain: each cert should be issued by the next
  let chainValid = parsed.length === certs.length;
  for (let i = 0; i < parsed.length - 1; i++) {
    try {
      const issuedBy = parsed[i].checkIssued(parsed[i + 1]);
      if (!issuedBy) {
        issues.push(`certificate[${i}] is not issued by certificate[${i + 1}]`);
        chainValid = false;
      }
    } catch (e) {
      issues.push(`chain validation error at index ${i}: ${e instanceof Error ? e.message : String(e)}`);
      chainValid = false;
    }
  }

  // Warn if key is exportable (this defeats the purpose of HSM attestation)
  if (!attestation.key_non_exportable) {
    issues.push('private key is not marked as non-exportable on the token');
  }

  return {
    present: true,
    chain_valid: chainValid,
    key_non_exportable: attestation.key_non_exportable,
    token_info: attestation.token_info,
    chain_length: certs.length,
    issues,
  };
}

function buildAttestationResult(
  attestation: HardwareAttestation,
  extraIssues: string[]
): AttestationVerification {
  return {
    present: true,
    chain_valid: false,
    key_non_exportable: attestation.key_non_exportable,
    token_info: attestation.token_info,
    chain_length: attestation.certificates?.length ?? 0,
    issues: extraIssues,
  };
}

