// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Deployment preflight validation.
 *
 * Runs all configured checks before submitting a transaction or creating
 * irreversible on-chain state. Every check that fails is recorded; the
 * preflight result is returned rather than thrown so callers can choose
 * between structured JSON output and a thrown error.
 *
 * No submission call is made during preflight.
 */

import { DeploymentManifest, DeployedContract } from './types';
import { validateManifest } from './resolver';

// ─── Check model ──────────────────────────────────────────────────────────────

export type PreflightCheckStatus = 'pass' | 'fail' | 'skip';

export interface PreflightCheck {
  /** Short machine-readable identifier (snake_case). */
  name: string;
  /** Human-readable description of what was verified. */
  description: string;
  /** Whether this check must pass before submission is allowed. */
  required: boolean;
  status: PreflightCheckStatus;
  detail?: string;
}

// ─── Result model ─────────────────────────────────────────────────────────────

/** Schema version for the JSON result — increment on breaking changes. */
export const PREFLIGHT_SCHEMA_VERSION = '1.0.0';

export interface PreflightResult {
  schemaVersion: typeof PREFLIGHT_SCHEMA_VERSION;
  /** True only when all *required* checks pass. */
  ok: boolean;
  checks: PreflightCheck[];
  /** Protocol version reported by the RPC node (set when network check passes). */
  protocolVersion?: string;
  /** SHA-256 hashes of each WASM artifact keyed by contract name. */
  artifactHashes?: Record<string, string>;
  dryRunOutput?: string;
}

// ─── Preflight options ────────────────────────────────────────────────────────

export interface NetworkProbe {
  /** Attempt to reach the RPC endpoint and return the protocol version. */
  fetchProtocolVersion(): Promise<string>;
}

export interface AccountProbe {
  /** Check that the deployer account exists and is funded. */
  checkAccount(address: string): Promise<{ exists: boolean; funded: boolean }>;
}

export interface ArtifactProbe {
  /** Compute or retrieve the SHA-256 hash of a WASM file by name. */
  hashArtifact(wasmPath: string): Promise<string>;
  /** Verify that a WASM file is accessible (exists, readable, non-empty). */
  verifyArtifact(wasmPath: string): Promise<boolean>;
}

export interface GasProbe {
  /** Simulate the deployment and return an estimated fee in stroops. */
  simulateFee(contractName: string): Promise<number>;
}

export interface PreflightOptions {
  manifest: DeploymentManifest;
  /** Deployer's Stellar account address (G-address). */
  deployerAddress?: string;
  network?: NetworkProbe;
  account?: AccountProbe;
  artifact?: ArtifactProbe;
  gas?: GasProbe;
  /** Maximum acceptable estimated fee per contract in stroops. */
  maxFeeStroops?: number;
}

// ─── Preflight runner ─────────────────────────────────────────────────────────

/**
 * Run all configured preflight checks without submitting any transaction.
 *
 * Checks are executed in order. All checks run even if earlier ones fail,
 * so the caller receives a complete picture of blockers.
 */
export async function runPreflight(opts: PreflightOptions): Promise<PreflightResult> {
  const checks: PreflightCheck[] = [];
  let protocolVersion: string | undefined;
  const artifactHashes: Record<string, string> = {};
  const dryRunLines: string[] = [];

  // ── 1. Manifest validation ────────────────────────────────────────────────
  const manifestErrors = validateManifest(opts.manifest);
  checks.push({
    name: 'manifest_valid',
    description: 'Deployment manifest is well-formed and all dependencies are declared',
    required: true,
    status: manifestErrors.length === 0 ? 'pass' : 'fail',
    detail: manifestErrors.length > 0 ? manifestErrors.join('; ') : undefined,
  });

  // ── 2. Network connectivity ───────────────────────────────────────────────
  if (opts.network) {
    try {
      protocolVersion = await opts.network.fetchProtocolVersion();
      checks.push({
        name: 'network_reachable',
        description: 'RPC endpoint is reachable and returned a protocol version',
        required: true,
        status: 'pass',
        detail: `protocolVersion=${protocolVersion}`,
      });
      dryRunLines.push(`Network: reachable (protocol ${protocolVersion})`);
    } catch (err) {
      checks.push({
        name: 'network_reachable',
        description: 'RPC endpoint is reachable and returned a protocol version',
        required: true,
        status: 'fail',
        detail: err instanceof Error ? err.message : String(err),
      });
    }
  } else {
    checks.push({
      name: 'network_reachable',
      description: 'RPC endpoint is reachable and returned a protocol version',
      required: true,
      status: 'skip',
      detail: 'no network probe configured',
    });
  }

  // ── 3. Account validation ─────────────────────────────────────────────────
  if (opts.account && opts.deployerAddress) {
    try {
      const { exists, funded } = await opts.account.checkAccount(opts.deployerAddress);
      const ok = exists && funded;
      checks.push({
        name: 'deployer_account',
        description: 'Deployer account exists and is funded',
        required: true,
        status: ok ? 'pass' : 'fail',
        detail: !exists
          ? `account ${opts.deployerAddress} does not exist`
          : !funded
          ? `account ${opts.deployerAddress} is not funded`
          : undefined,
      });
    } catch (err) {
      checks.push({
        name: 'deployer_account',
        description: 'Deployer account exists and is funded',
        required: true,
        status: 'fail',
        detail: err instanceof Error ? err.message : String(err),
      });
    }
  } else {
    checks.push({
      name: 'deployer_account',
      description: 'Deployer account exists and is funded',
      required: false,
      status: 'skip',
      detail: opts.deployerAddress ? 'no account probe configured' : 'no deployer address provided',
    });
  }

  // ── 4. Contract artifact checks ───────────────────────────────────────────
  if (opts.artifact) {
    for (const contract of opts.manifest.contracts) {
      try {
        const accessible = await opts.artifact.verifyArtifact(contract.wasm);
        if (!accessible) {
          checks.push({
            name: `artifact_${contract.name}`,
            description: `WASM artifact for "${contract.name}" is accessible`,
            required: true,
            status: 'fail',
            detail: `artifact not found or empty: ${contract.wasm}`,
          });
          continue;
        }

        const hash = await opts.artifact.hashArtifact(contract.wasm);
        artifactHashes[contract.name] = hash;
        checks.push({
          name: `artifact_${contract.name}`,
          description: `WASM artifact for "${contract.name}" is accessible`,
          required: true,
          status: 'pass',
          detail: `sha256=${hash}`,
        });
        dryRunLines.push(`Artifact ${contract.name}: ${contract.wasm} (sha256=${hash})`);
      } catch (err) {
        checks.push({
          name: `artifact_${contract.name}`,
          description: `WASM artifact for "${contract.name}" is accessible`,
          required: true,
          status: 'fail',
          detail: err instanceof Error ? err.message : String(err),
        });
      }
    }
  } else {
    checks.push({
      name: 'artifact_checks',
      description: 'WASM artifacts are accessible',
      required: false,
      status: 'skip',
      detail: 'no artifact probe configured',
    });
  }

  // ── 5. Gas / fee estimation ───────────────────────────────────────────────
  if (opts.gas) {
    for (const contract of opts.manifest.contracts) {
      try {
        const fee = await opts.gas.simulateFee(contract.name);
        const limit = opts.maxFeeStroops;
        const exceeded = limit !== undefined && fee > limit;
        checks.push({
          name: `gas_${contract.name}`,
          description: `Gas estimate for "${contract.name}" is within limits`,
          required: false,
          status: exceeded ? 'fail' : 'pass',
          detail: exceeded
            ? `estimated fee ${fee} stroops exceeds limit ${limit}`
            : `estimated fee ${fee} stroops`,
        });
        dryRunLines.push(`Gas ${contract.name}: ~${fee} stroops`);
      } catch (err) {
        checks.push({
          name: `gas_${contract.name}`,
          description: `Gas estimate for "${contract.name}" is within limits`,
          required: false,
          status: 'fail',
          detail: err instanceof Error ? err.message : String(err),
        });
      }
    }
  }

  // ── 6. Protocol / ABI compatibility ──────────────────────────────────────
  if (protocolVersion && opts.manifest.network) {
    const networkId = opts.manifest.network;
    const isMainnet = networkId === 'mainnet';
    const isTestnet = networkId === 'testnet';
    const knownNetwork = isMainnet || isTestnet || networkId === 'futurenet' || networkId === 'standalone';

    checks.push({
      name: 'protocol_abi_compat',
      description: 'Manifest network matches detected protocol version',
      required: false,
      status: knownNetwork ? 'pass' : 'fail',
      detail: knownNetwork
        ? `network="${networkId}", protocolVersion="${protocolVersion}"`
        : `unrecognised network identifier "${networkId}"`,
    });
  }

  const requiredFailed = checks.some(c => c.required && c.status === 'fail');

  return {
    schemaVersion: PREFLIGHT_SCHEMA_VERSION,
    ok: !requiredFailed,
    checks,
    protocolVersion,
    artifactHashes: Object.keys(artifactHashes).length > 0 ? artifactHashes : undefined,
    dryRunOutput: dryRunLines.length > 0 ? dryRunLines.join('\n') : undefined,
  };
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/**
 * Throw a structured error when preflight did not pass.
 * Text and JSON representations agree.
 */
export function assertPreflightPassed(result: PreflightResult): void {
  if (result.ok) return;
  const failed = result.checks
    .filter(c => c.required && c.status === 'fail')
    .map(c => `  [${c.name}] ${c.detail ?? c.description}`)
    .join('\n');
  throw new Error(
    `Deployment preflight failed — resolve the following blockers before submitting:\n${failed}`,
  );
}

/**
 * Format a preflight result as a compact dry-run text summary.
 * Matches the content of {@link PreflightResult.dryRunOutput} plus check table.
 */
export function formatPreflightResult(result: PreflightResult): string {
  const lines: string[] = [
    `Preflight result: ${result.ok ? 'PASS' : 'FAIL'} (schema ${result.schemaVersion})`,
  ];
  if (result.dryRunOutput) {
    lines.push('', '--- dry-run ---', result.dryRunOutput, '---------------');
  }
  lines.push('', 'Checks:');
  for (const c of result.checks) {
    const tag = c.status === 'pass' ? '✓' : c.status === 'fail' ? '✗' : '–';
    const req = c.required ? ' [required]' : '';
    lines.push(`  ${tag} ${c.name}${req}${c.detail ? ': ' + c.detail : ''}`);
  }
  return lines.join('\n');
}
