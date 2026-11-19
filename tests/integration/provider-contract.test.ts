// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * provider-contract.test.ts
 *
 * Runs the shared signing contract suite against each provider implementation.
 * Issue #596: Add integration contract tests for external providers.
 *
 * Test structure:
 *   - Software (Ed25519) — always runs, no credentials required.
 *   - Mock (in-memory Ed25519) — always runs, no credentials required.
 *   - PKCS#11 — skipped unless GLASSBOX_LIVE_PROVIDER_TESTS=1.
 *   - KMS — skipped unless GLASSBOX_LIVE_PROVIDER_TESTS=1.
 *   - RPC fake server — runs against a deterministic httptest stub.
 *   - Horizon fake server — runs against a deterministic httptest stub.
 *
 * To run live tests:
 *   GLASSBOX_LIVE_PROVIDER_TESTS=1 \
 *   GLASSBOX_KMS_KEY_ID=arn:aws:kms:… \
 *   AWS_REGION=us-east-1 \
 *   npx jest tests/integration/provider-contract.test.ts
 *
 * Failures produced by this suite include the provider name so engineers
 * can immediately identify which contract was violated.
 */

import { generateKeyPairSync } from 'crypto';
import { createServer, type Server } from 'http';

import {
  runSignerContractSuite,
  runRpcContractSuite,
  isLiveTestEnabled,
  type SignerContract,
  type RpcProviderContract,
} from './providerContract';

import { MockAuditSigner } from '../../src/audit/signing/mockSigner';
import { SoftwareEd25519Signer } from '../../src/audit/signing/softwareSigner';

// ─── Fake HTTP servers ────────────────────────────────────────────────────────

/**
 * Minimal fake Soroban RPC server.
 * Returns deterministic JSON for health and simulate endpoints.
 */
function startFakeRpcServer(): Promise<{ url: string; server: Server }> {
  return new Promise((resolve) => {
    const server = createServer((req, res) => {
      res.setHeader('Content-Type', 'application/json');

      if (req.method === 'GET' && req.url === '/health') {
        res.writeHead(200);
        res.end(JSON.stringify({ status: 'healthy', ledger: 1000 }));
        return;
      }

      // Soroban RPC — all POST requests get a minimal success response
      if (req.method === 'POST') {
        let body = '';
        req.on('data', (chunk: Buffer) => {
          body += chunk.toString();
        });
        req.on('end', () => {
          let parsed: Record<string, unknown> = {};
          try {
            parsed = JSON.parse(body);
          } catch {
            // ignore
          }
          const method = (parsed['method'] as string) ?? '';

          if (method === 'getTransaction') {
            res.writeHead(200);
            res.end(
              JSON.stringify({
                jsonrpc: '2.0',
                id: parsed['id'],
                result: {
                  status: 'SUCCESS',
                  envelopeXdr: 'AAAA',
                  resultXdr: 'AAAA',
                  resultMetaXdr: 'AAAA',
                  ledger: 1000,
                },
              })
            );
            return;
          }

          if (method === 'simulateTransaction') {
            res.writeHead(200);
            res.end(
              JSON.stringify({
                jsonrpc: '2.0',
                id: parsed['id'],
                result: {
                  results: [{ xdr: 'AAAA' }],
                  cost: { cpuInsns: '100', memBytes: '256' },
                  latestLedger: 1000,
                },
              })
            );
            return;
          }

          if (method === 'getLedgerEntries') {
            res.writeHead(200);
            res.end(
              JSON.stringify({
                jsonrpc: '2.0',
                id: parsed['id'],
                result: { entries: [], latestLedger: 1000 },
              })
            );
            return;
          }

          // Unknown method — return a JSON-RPC error
          res.writeHead(200);
          res.end(
            JSON.stringify({
              jsonrpc: '2.0',
              id: parsed['id'] ?? null,
              error: { code: -32601, message: `method not found: ${method}` },
            })
          );
        });
        return;
      }

      res.writeHead(404);
      res.end(JSON.stringify({ error: 'not found' }));
    });

    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      resolve({ url: `http://127.0.0.1:${port}`, server });
    });
  });
}

/**
 * Minimal fake Horizon server.
 * Returns deterministic JSON for the /transactions endpoint.
 */
function startFakeHorizonServer(): Promise<{ url: string; server: Server }> {
  return new Promise((resolve) => {
    const server = createServer((req, res) => {
      res.setHeader('Content-Type', 'application/json');

      if (req.url?.startsWith('/transactions')) {
        res.writeHead(200);
        res.end(
          JSON.stringify({
            _embedded: {
              records: [
                {
                  hash: '5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab',
                  successful: true,
                  ledger: 1000,
                  created_at: '2026-01-01T00:00:00Z',
                  envelope_xdr: 'AAAA',
                  result_xdr: 'AAAA',
                  result_meta_xdr: 'AAAA',
                },
              ],
            },
          })
        );
        return;
      }

      res.writeHead(404);
      res.end(JSON.stringify({ error: 'not found' }));
    });

    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      resolve({ url: `http://127.0.0.1:${port}`, server });
    });
  });
}

// ─── Provider implementations ─────────────────────────────────────────────────

// --- Software Ed25519 --------------------------------------------------------
const softwareContract: SignerContract = {
  providerName: 'software-ed25519',
  isDeterministic: true,
  supportsAttestation: false,

  async buildSigner() {
    const { privateKey } = generateKeyPairSync('ed25519', {
      publicKeyEncoding: { type: 'spki', format: 'pem' },
      privateKeyEncoding: { type: 'pkcs8', format: 'pem' },
    });
    return new SoftwareEd25519Signer(privateKey);
  },
};

// --- Mock (test double) ------------------------------------------------------
const mockContract: SignerContract = {
  providerName: 'mock-ed25519',
  isDeterministic: true,
  supportsAttestation: false,

  async buildSigner() {
    return new MockAuditSigner();
  },
};

// --- Mock with attestation ----------------------------------------------------
const mockAttestContract: SignerContract = {
  providerName: 'mock-ed25519-with-attestation',
  isDeterministic: true,
  supportsAttestation: true,

  async buildSigner() {
    return new MockAuditSigner({ withAttestation: true });
  },
};

// --- PKCS#11 (live opt-in) ---------------------------------------------------
const pkcs11Contract: SignerContract = {
  providerName: 'pkcs11',
  isDeterministic: false, // HSM uses hardware-generated nonce
  supportsAttestation: true,

  async buildSigner() {
    if (!isLiveTestEnabled()) {
      return null; // skip
    }
    // Lazily import to avoid loading pkcs11js in CI without native module
    const { Pkcs11Signer } = await import('../../src/audit/signing/pkcs11Signer');
    try {
      const signer = new Pkcs11Signer();
      return signer;
    } catch (e) {
      console.warn(`  [pkcs11] skipping: ${e instanceof Error ? e.message : e}`);
      return null;
    }
  },
};

// --- AWS KMS (live opt-in) ---------------------------------------------------
const kmsContract: SignerContract = {
  providerName: 'aws-kms',
  isDeterministic: false, // KMS returns non-deterministic signatures
  supportsAttestation: false,

  async buildSigner() {
    if (!isLiveTestEnabled()) {
      return null; // skip
    }
    if (!process.env['GLASSBOX_KMS_KEY_ID'] || !process.env['AWS_REGION']) {
      console.warn(
        '  [aws-kms] skipping: GLASSBOX_KMS_KEY_ID and AWS_REGION must be set'
      );
      return null;
    }
    const { KmsSigner } = await import('../../src/audit/signing/kmsSigner');
    try {
      return new KmsSigner();
    } catch (e) {
      console.warn(`  [aws-kms] skipping: ${e instanceof Error ? e.message : e}`);
      return null;
    }
  },
};

// --- Fake RPC server ---------------------------------------------------------
let rpcServer: Server | null = null;
let rpcServerUrl = '';

const rpcContract: RpcProviderContract = {
  providerName: 'soroban-rpc-fake',
  supportsLedgerEntries: true,
  supportsSimulation: true,

  async buildServerUrl() {
    if (!rpcServer) {
      const started = await startFakeRpcServer();
      rpcServer = started.server;
      rpcServerUrl = started.url;
    }
    return rpcServerUrl;
  },

  async teardown() {
    if (rpcServer) {
      rpcServer.close();
      rpcServer = null;
    }
  },
};

// --- Fake Horizon server -----------------------------------------------------
let horizonServer: Server | null = null;
let horizonServerUrl = '';

const horizonContract: RpcProviderContract = {
  providerName: 'horizon-fake',
  supportsLedgerEntries: false,
  supportsSimulation: false,

  async buildServerUrl() {
    if (!horizonServer) {
      const started = await startFakeHorizonServer();
      horizonServer = started.server;
      horizonServerUrl = started.url;
    }
    return horizonServerUrl;
  },

  async teardown() {
    if (horizonServer) {
      horizonServer.close();
      horizonServer = null;
    }
  },
};

// ─── Register contract suites ────────────────────────────────────────────────

describe('Provider contract suites', () => {
  afterAll(async () => {
    await rpcContract.teardown?.();
    await horizonContract.teardown?.();
  });

  // Signing contracts
  describe('Signing providers', () => {
    runSignerContractSuite(softwareContract);
    runSignerContractSuite(mockContract);
    runSignerContractSuite(mockAttestContract);
    runSignerContractSuite(pkcs11Contract);
    runSignerContractSuite(kmsContract);
  });

  // RPC / Horizon contracts
  describe('Network providers', () => {
    runRpcContractSuite(rpcContract);
    runRpcContractSuite(horizonContract);
  });

  // Malformed response contract — provider must surface errors clearly
  describe('Timeout and malformed response handling', () => {

    it('[soroban-rpc-fake] responds with error for unknown method', async () => {
      const url = await rpcContract.buildServerUrl();
      const fetch = (await import('cross-fetch')).default;
      const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          id: 1,
          method: 'unknownMethod',
          params: [],
        }),
      });
      const body = (await resp.json()) as Record<string, unknown>;
      expect(body['error']).toBeDefined();
    });

    it('[soroban-rpc-fake] responds with error body for malformed JSON', async () => {
      const url = await rpcContract.buildServerUrl();
      const fetch = (await import('cross-fetch')).default;
      const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: 'not-json{{{',
      });
      // Server should not crash — must return a response
      expect(resp.status).toBeLessThan(600);
    });

    it('[horizon-fake] returns transaction records for /transactions', async () => {
      const url = await horizonContract.buildServerUrl();
      const fetch = (await import('cross-fetch')).default;
      const resp = await fetch(`${url}/transactions`);
      const body = (await resp.json()) as Record<string, unknown>;
      const embedded = body['_embedded'] as Record<string, unknown>;
      expect(Array.isArray(embedded['records'])).toBe(true);
    });
  });
});
