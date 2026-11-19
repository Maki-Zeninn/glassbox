// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import {
  runPreflight,
  assertPreflightPassed,
  formatPreflightResult,
  PREFLIGHT_SCHEMA_VERSION,
  type NetworkProbe,
  type AccountProbe,
  type ArtifactProbe,
  type GasProbe,
  type PreflightOptions,
} from '../preflight';
import type { DeploymentManifest } from '../types';

// ─── Fixtures ─────────────────────────────────────────────────────────────────

const VALID_MANIFEST: DeploymentManifest = {
  version: '1',
  network: 'testnet',
  contracts: [
    { name: 'token', wasm: 'token.wasm' },
    { name: 'pool', wasm: 'pool.wasm', dependencies: ['token'] },
  ],
};

const INVALID_MANIFEST: DeploymentManifest = {
  version: '',
  network: '',
  contracts: [],
};

// ─── Fake probes ──────────────────────────────────────────────────────────────

function okNetwork(version = '20'): NetworkProbe {
  return { fetchProtocolVersion: async () => version };
}

function failNetwork(msg = 'connection refused'): NetworkProbe {
  return {
    fetchProtocolVersion: async () => {
      throw new Error(msg);
    },
  };
}

function okAccount(exists = true, funded = true): AccountProbe {
  return {
    checkAccount: async () => ({ exists, funded }),
  };
}

function okArtifact(hash = 'deadbeef'): ArtifactProbe {
  return {
    verifyArtifact: async () => true,
    hashArtifact: async () => hash,
  };
}

function missingArtifact(): ArtifactProbe {
  return {
    verifyArtifact: async () => false,
    hashArtifact: async () => { throw new Error('should not be called'); },
  };
}

function okGas(fee = 500): GasProbe {
  return { simulateFee: async () => fee };
}

function highGas(fee = 9999): GasProbe {
  return { simulateFee: async () => fee };
}

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('runPreflight — schema and shape', () => {
  it('returns schemaVersion and ok fields', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      network: okNetwork(),
    });
    expect(result.schemaVersion).toBe(PREFLIGHT_SCHEMA_VERSION);
    expect(typeof result.ok).toBe('boolean');
    expect(Array.isArray(result.checks)).toBe(true);
  });
});

describe('runPreflight — manifest validation', () => {
  it('passes for a valid manifest', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST });
    const check = result.checks.find(c => c.name === 'manifest_valid')!;
    expect(check.status).toBe('pass');
  });

  it('fails for an invalid manifest', async () => {
    const result = await runPreflight({ manifest: INVALID_MANIFEST });
    const check = result.checks.find(c => c.name === 'manifest_valid')!;
    expect(check.status).toBe('fail');
    expect(result.ok).toBe(false);
  });
});

describe('runPreflight — network check', () => {
  it('passes and records protocol version', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST, network: okNetwork('22') });
    const check = result.checks.find(c => c.name === 'network_reachable')!;
    expect(check.status).toBe('pass');
    expect(result.protocolVersion).toBe('22');
  });

  it('fails when network probe throws', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST, network: failNetwork() });
    const check = result.checks.find(c => c.name === 'network_reachable')!;
    expect(check.status).toBe('fail');
    expect(check.detail).toContain('connection refused');
  });

  it('skips when no network probe configured', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST });
    const check = result.checks.find(c => c.name === 'network_reachable')!;
    expect(check.status).toBe('skip');
  });
});

describe('runPreflight — account check', () => {
  it('passes for an existing funded account', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      deployerAddress: 'GABC',
      account: okAccount(),
    });
    const check = result.checks.find(c => c.name === 'deployer_account')!;
    expect(check.status).toBe('pass');
  });

  it('fails when account does not exist', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      deployerAddress: 'GABC',
      account: okAccount(false, false),
    });
    const check = result.checks.find(c => c.name === 'deployer_account')!;
    expect(check.status).toBe('fail');
    expect(result.ok).toBe(false);
  });

  it('fails when account exists but is not funded', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      deployerAddress: 'GABC',
      account: okAccount(true, false),
    });
    const check = result.checks.find(c => c.name === 'deployer_account')!;
    expect(check.status).toBe('fail');
    expect(check.detail).toContain('not funded');
  });

  it('skips when no deployer address provided', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      account: okAccount(),
    });
    const check = result.checks.find(c => c.name === 'deployer_account')!;
    expect(check.status).toBe('skip');
  });
});

describe('runPreflight — artifact checks', () => {
  it('passes and records artifact hashes', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      artifact: okArtifact('abc123'),
    });
    expect(result.artifactHashes).toEqual({ token: 'abc123', pool: 'abc123' });
    for (const contract of VALID_MANIFEST.contracts) {
      const check = result.checks.find(c => c.name === `artifact_${contract.name}`)!;
      expect(check.status).toBe('pass');
    }
  });

  it('fails when artifact is not accessible', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      artifact: missingArtifact(),
    });
    const check = result.checks.find(c => c.name === 'artifact_token')!;
    expect(check.status).toBe('fail');
    expect(result.ok).toBe(false);
  });

  it('skips all artifact checks when no artifact probe configured', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST });
    const check = result.checks.find(c => c.name === 'artifact_checks')!;
    expect(check.status).toBe('skip');
  });
});

describe('runPreflight — gas checks', () => {
  it('passes when fee is within limit', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      gas: okGas(200),
      maxFeeStroops: 1000,
    });
    for (const contract of VALID_MANIFEST.contracts) {
      const check = result.checks.find(c => c.name === `gas_${contract.name}`)!;
      expect(check.status).toBe('pass');
    }
  });

  it('fails when fee exceeds limit', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      gas: highGas(9999),
      maxFeeStroops: 1000,
    });
    const check = result.checks.find(c => c.name === 'gas_token')!;
    expect(check.status).toBe('fail');
    expect(check.detail).toContain('exceeds limit');
  });
});

describe('runPreflight — all checks pass', () => {
  it('returns ok=true when all required checks pass', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      deployerAddress: 'GABC',
      network: okNetwork(),
      account: okAccount(),
      artifact: okArtifact(),
      gas: okGas(),
      maxFeeStroops: 1000,
    });
    expect(result.ok).toBe(true);
  });

  it('dry-run output is produced', async () => {
    const result = await runPreflight({
      manifest: VALID_MANIFEST,
      network: okNetwork('21'),
      artifact: okArtifact('cafecafe'),
    });
    expect(result.dryRunOutput).toContain('protocol 21');
    expect(result.dryRunOutput).toContain('sha256=cafecafe');
  });

  it('text and JSON agree on pass/fail status', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST, network: failNetwork() });
    const text = formatPreflightResult(result);
    expect(text).toContain('FAIL');
    expect(result.ok).toBe(false);
  });
});

describe('assertPreflightPassed', () => {
  it('does not throw when ok', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST, network: okNetwork() });
    expect(() => assertPreflightPassed(result)).not.toThrow();
  });

  it('throws with blocker details when not ok', async () => {
    const result = await runPreflight({ manifest: INVALID_MANIFEST });
    expect(() => assertPreflightPassed(result)).toThrow('preflight failed');
  });
});

describe('formatPreflightResult', () => {
  it('contains pass/fail header', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST });
    const text = formatPreflightResult(result);
    expect(text).toMatch(/Preflight result: (PASS|FAIL)/);
  });

  it('lists all check names', async () => {
    const result = await runPreflight({ manifest: VALID_MANIFEST });
    for (const check of result.checks) {
      expect(formatPreflightResult(result)).toContain(check.name);
    }
  });
});
