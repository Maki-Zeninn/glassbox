// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import { xdr } from '@stellar/stellar-sdk';
import {
  compareFootprints,
  normalizeFootprint,
  diffNormalizedFootprints,
  FOOTPRINT_DIFF_SCHEMA_VERSION,
  type FootprintDiffEntry,
} from '../diff';
import type { LedgerKey } from '../../xdr/types';

// ─── Fixtures ─────────────────────────────────────────────────────────────────

function makeKey(hash: string, type = xdr.LedgerEntryType.contractData()): LedgerKey {
  return { type, key: `key-${hash}`, hash };
}

const KEY_A = makeKey('aaa');
const KEY_B = makeKey('bbb');
const KEY_C = makeKey('ccc');
const KEY_D = makeKey('ddd');

// ─── normalizeFootprint ───────────────────────────────────────────────────────

describe('normalizeFootprint', () => {
  it('builds O(1) lookup maps', () => {
    const n = normalizeFootprint({ readOnly: [KEY_A], readWrite: [KEY_B] });
    expect(n.readOnly.has('aaa')).toBe(true);
    expect(n.readWrite.has('bbb')).toBe(true);
  });

  it('deduplicates within readOnly', () => {
    const n = normalizeFootprint({ readOnly: [KEY_A, KEY_A], readWrite: [] });
    expect(n.readOnly.size).toBe(1);
  });

  it('readWrite takes precedence over readOnly for the same hash', () => {
    const n = normalizeFootprint({ readOnly: [KEY_A], readWrite: [KEY_A] });
    expect(n.readOnly.has('aaa')).toBe(false);
    expect(n.readWrite.has('aaa')).toBe(true);
  });

  it('handles empty footprint', () => {
    const n = normalizeFootprint({ readOnly: [], readWrite: [] });
    expect(n.readOnly.size).toBe(0);
    expect(n.readWrite.size).toBe(0);
  });
});

// ─── compareFootprints — determinism ─────────────────────────────────────────

describe('compareFootprints — determinism', () => {
  it('identical footprints produce all unchanged entries', () => {
    const fp = { readOnly: [KEY_A, KEY_B], readWrite: [KEY_C] };
    const result = compareFootprints({ before: fp, after: fp });
    expect(result.summary.unchanged).toBe(3);
    expect(result.summary.added).toBe(0);
    expect(result.summary.removed).toBe(0);
    expect(result.hasRisk).toBe(false);
  });

  it('entries are sorted by hash ascending (canonical order)', () => {
    const fp1 = { readOnly: [KEY_C, KEY_A], readWrite: [KEY_B] };
    const fp2 = { readOnly: [KEY_A, KEY_B], readWrite: [KEY_C] };
    const result = compareFootprints({ before: fp1, after: fp2 });
    const hashes = result.entries.map(e => e.hash);
    expect(hashes).toEqual([...hashes].sort());
  });

  it('produces the same result regardless of input order', () => {
    const fp1 = { readOnly: [KEY_A, KEY_B], readWrite: [] };
    const fp2 = { readOnly: [KEY_B, KEY_A], readWrite: [] };
    const r1 = compareFootprints({ before: fp1, after: fp1 });
    const r2 = compareFootprints({ before: fp2, after: fp2 });
    expect(r1.entries.map(e => e.hash)).toEqual(r2.entries.map(e => e.hash));
  });
});

// ─── compareFootprints — additive ────────────────────────────────────────────

describe('compareFootprints — additive', () => {
  it('classifies new read-only entry as added/low-risk', () => {
    const before = { readOnly: [KEY_A], readWrite: [] };
    const after = { readOnly: [KEY_A, KEY_B], readWrite: [] };
    const result = compareFootprints({ before, after });
    const added = result.entries.find(e => e.hash === 'bbb')!;
    expect(added.kind).toBe('added');
    expect(added.accessAfter).toBe('readOnly');
    expect(added.risk).toBe('low');
    expect(result.summary.added).toBe(1);
    expect(result.hasRisk).toBe(false);
  });

  it('classifies new read-write entry as added/high-risk', () => {
    const before = { readOnly: [], readWrite: [] };
    const after = { readOnly: [], readWrite: [KEY_A] };
    const result = compareFootprints({ before, after });
    const added = result.entries.find(e => e.hash === 'aaa')!;
    expect(added.kind).toBe('added');
    expect(added.risk).toBe('high');
    expect(result.summary.highRisk).toBe(1);
    expect(result.hasRisk).toBe(true);
  });
});

// ─── compareFootprints — removals ────────────────────────────────────────────

describe('compareFootprints — removals', () => {
  it('classifies removed entry as high-risk', () => {
    const before = { readOnly: [KEY_A, KEY_B], readWrite: [] };
    const after = { readOnly: [KEY_A], readWrite: [] };
    const result = compareFootprints({ before, after });
    const removed = result.entries.find(e => e.hash === 'bbb')!;
    expect(removed.kind).toBe('removed');
    expect(removed.risk).toBe('high');
    expect(result.hasRisk).toBe(true);
  });
});

// ─── compareFootprints — access-mode changes ──────────────────────────────────

describe('compareFootprints — access-mode changes', () => {
  it('read-only → read-write is medium risk', () => {
    const before = { readOnly: [KEY_A], readWrite: [] };
    const after = { readOnly: [], readWrite: [KEY_A] };
    const result = compareFootprints({ before, after });
    const entry = result.entries.find(e => e.hash === 'aaa')!;
    expect(entry.kind).toBe('access-mode-changed');
    expect(entry.accessBefore).toBe('readOnly');
    expect(entry.accessAfter).toBe('readWrite');
    expect(entry.risk).toBe('medium');
    expect(result.summary.accessModeChanged).toBe(1);
    expect(result.hasRisk).toBe(true);
  });

  it('read-write → read-only is low risk', () => {
    const before = { readOnly: [], readWrite: [KEY_A] };
    const after = { readOnly: [KEY_A], readWrite: [] };
    const result = compareFootprints({ before, after });
    const entry = result.entries.find(e => e.hash === 'aaa')!;
    expect(entry.kind).toBe('access-mode-changed');
    expect(entry.risk).toBe('low');
    expect(result.hasRisk).toBe(false);
  });
});

// ─── compareFootprints — conflicting / mixed ──────────────────────────────────

describe('compareFootprints — mixed scenarios', () => {
  it('handles all change types in one diff', () => {
    const before = {
      readOnly: [KEY_A, KEY_B],
      readWrite: [KEY_C],
    };
    const after = {
      readOnly: [KEY_A, KEY_C],
      readWrite: [KEY_D],
    };
    const result = compareFootprints({ before, after });

    const entryA = result.entries.find(e => e.hash === 'aaa')!;
    const entryB = result.entries.find(e => e.hash === 'bbb')!;
    const entryC = result.entries.find(e => e.hash === 'ccc')!;
    const entryD = result.entries.find(e => e.hash === 'ddd')!;

    expect(entryA.kind).toBe('unchanged');
    expect(entryB.kind).toBe('removed');
    expect(entryC.kind).toBe('access-mode-changed');
    expect(entryD.kind).toBe('added');
  });
});

// ─── compareFootprints — gas integration ─────────────────────────────────────

describe('compareFootprints — gas metadata', () => {
  it('passes through gas estimations unchanged', () => {
    const fp = { readOnly: [KEY_A], readWrite: [] };
    const gas = {
      cpuCost: 1000,
      memoryCost: 2000,
      cpuLimit: 5000,
      memoryLimit: 10000,
      cpuUsagePercent: 20,
      memoryUsagePercent: 20,
      operationsCount: 5,
      estimatedFeeLowerBound: 100,
      estimatedFeeUpperBound: 115,
    };
    const result = compareFootprints({ before: fp, after: fp, gasBefore: gas, gasAfter: gas });
    expect(result.gasBefore).toBe(gas);
    expect(result.gasAfter).toBe(gas);
  });
});

// ─── compareFootprints — schema version ──────────────────────────────────────

describe('compareFootprints — schema version', () => {
  it('includes the schema version in the result', () => {
    const fp = { readOnly: [], readWrite: [] };
    const result = compareFootprints({ before: fp, after: fp });
    expect(result.schemaVersion).toBe(FOOTPRINT_DIFF_SCHEMA_VERSION);
  });
});

// ─── compareFootprints — empty footprints ────────────────────────────────────

describe('compareFootprints — empty footprints', () => {
  it('empty → empty produces no entries', () => {
    const fp = { readOnly: [], readWrite: [] };
    const result = compareFootprints({ before: fp, after: fp });
    expect(result.entries).toHaveLength(0);
    expect(result.hasRisk).toBe(false);
  });

  it('non-empty → empty marks all entries as removed', () => {
    const before = { readOnly: [KEY_A, KEY_B], readWrite: [KEY_C] };
    const after = { readOnly: [], readWrite: [] };
    const result = compareFootprints({ before, after });
    expect(result.summary.removed).toBe(3);
    expect(result.summary.highRisk).toBe(3);
  });
});

// ─── Large-scale determinism ──────────────────────────────────────────────────

describe('compareFootprints — large scale', () => {
  it('handles 1000 keys deterministically', () => {
    const keys = Array.from({ length: 1000 }, (_, i) =>
      makeKey(i.toString(16).padStart(4, '0'))
    );
    const before = { readOnly: keys.slice(0, 500), readWrite: keys.slice(500) };
    const after = { readOnly: keys.slice(0, 500), readWrite: keys.slice(500) };
    const r1 = compareFootprints({ before, after });
    const r2 = compareFootprints({ before: { readOnly: [...keys.slice(0, 500)].reverse(), readWrite: [...keys.slice(500)].reverse() }, after });
    expect(r1.entries.map(e => e.hash)).toEqual(r2.entries.map(e => e.hash));
    expect(r1.summary.unchanged).toBe(1000);
  });
});
