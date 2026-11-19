// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Footprint diff and risk reporting.
 *
 * Compares two Soroban transaction footprints and classifies each entry
 * as added, removed, access-mode-changed, or unchanged. Comparisons are
 * deterministic (canonical ordering by hash). Unknown entry types are
 * handled explicitly rather than silently ignored.
 */

import type { LedgerKey } from '../xdr/types';
import type { GasEstimation } from '../gas';

// ─── Schema version ───────────────────────────────────────────────────────────

export const FOOTPRINT_DIFF_SCHEMA_VERSION = '1.0.0';

// ─── Entry types ──────────────────────────────────────────────────────────────

export type FootprintChangeKind =
  | 'added'
  | 'removed'
  | 'access-mode-changed'
  | 'unchanged'
  | 'unknown';

/**
 * Risk classification for a footprint change.
 *
 * - `high`   — may affect authorization or replayability (e.g. new write or removal)
 * - `medium` — may affect cost (e.g. read→write promotion)
 * - `low`    — informational only (new read, unchanged)
 * - `none`   — no change
 */
export type FootprintRisk = 'high' | 'medium' | 'low' | 'none';

export interface FootprintDiffEntry {
  /** Canonical hash of the ledger key (hex). */
  hash: string;
  /** Human-readable key type name (e.g. "contractData"). */
  keyType: string;
  kind: FootprintChangeKind;
  risk: FootprintRisk;
  /** Access mode in the *before* footprint, or undefined if not present. */
  accessBefore?: 'readOnly' | 'readWrite';
  /** Access mode in the *after* footprint, or undefined if not present. */
  accessAfter?: 'readOnly' | 'readWrite';
  /** Additional context about the risk finding. */
  riskDetail?: string;
}

// ─── Diff result ──────────────────────────────────────────────────────────────

export interface FootprintDiffSummary {
  added: number;
  removed: number;
  accessModeChanged: number;
  unchanged: number;
  unknown: number;
  highRisk: number;
  mediumRisk: number;
}

export interface FootprintDiffResult {
  schemaVersion: typeof FOOTPRINT_DIFF_SCHEMA_VERSION;
  entries: FootprintDiffEntry[];
  summary: FootprintDiffSummary;
  /** Gas estimation for the *before* footprint, if provided. */
  gasBefore?: GasEstimation;
  /** Gas estimation for the *after* footprint, if provided. */
  gasAfter?: GasEstimation;
  /** True when any entry has risk ≥ medium. */
  hasRisk: boolean;
}

// ─── Normalised footprint ─────────────────────────────────────────────────────

export interface NormalizedFootprint {
  readOnly: Map<string, LedgerKey>;
  readWrite: Map<string, LedgerKey>;
}

/**
 * Convert a raw footprint (arrays of LedgerKey) to a normalised form with
 * O(1) hash lookups. Duplicate hashes are deduplicated (first-seen wins).
 */
export function normalizeFootprint(footprint: {
  readOnly: LedgerKey[];
  readWrite: LedgerKey[];
}): NormalizedFootprint {
  const readOnly = new Map<string, LedgerKey>();
  const readWrite = new Map<string, LedgerKey>();

  for (const key of footprint.readOnly) {
    if (key.hash && !readOnly.has(key.hash) && !readWrite.has(key.hash)) {
      readOnly.set(key.hash, key);
    }
  }

  for (const key of footprint.readWrite) {
    if (key.hash) {
      readOnly.delete(key.hash);
      if (!readWrite.has(key.hash)) {
        readWrite.set(key.hash, key);
      }
    }
  }

  return { readOnly, readWrite };
}

// ─── Risk classification ──────────────────────────────────────────────────────

function classifyRisk(kind: FootprintChangeKind, entry: Partial<FootprintDiffEntry>): {
  risk: FootprintRisk;
  riskDetail?: string;
} {
  switch (kind) {
    case 'added':
      if (entry.accessAfter === 'readWrite') {
        return {
          risk: 'high',
          riskDetail: 'New write-access entry may affect authorization or replayability',
        };
      }
      return { risk: 'low', riskDetail: 'New read-only entry increases ledger footprint' };

    case 'removed':
      return {
        risk: 'high',
        riskDetail: 'Removed entry may affect contract state availability or authorization',
      };

    case 'access-mode-changed':
      if (entry.accessBefore === 'readOnly' && entry.accessAfter === 'readWrite') {
        return {
          risk: 'medium',
          riskDetail: 'Promotion from read-only to read-write increases cost and write amplification',
        };
      }
      if (entry.accessBefore === 'readWrite' && entry.accessAfter === 'readOnly') {
        return {
          risk: 'low',
          riskDetail: 'Demotion from read-write to read-only reduces cost',
        };
      }
      return { risk: 'low' };

    case 'unchanged':
      return { risk: 'none' };

    case 'unknown':
    default:
      return { risk: 'low', riskDetail: 'Entry type not recognised; manual review recommended' };
  }
}

// ─── Key type name ────────────────────────────────────────────────────────────

function keyTypeName(key: LedgerKey): string {
  try {
    const name: unknown = (key.type as any)?.name ?? (key.type as any)?.toString?.();
    return typeof name === 'string' ? name : 'unknown';
  } catch {
    return 'unknown';
  }
}

// ─── Core diff ────────────────────────────────────────────────────────────────

/**
 * Compare two normalised footprints and produce a deterministic diff.
 * Entries in the result are sorted canonically by hash (ascending).
 */
export function diffNormalizedFootprints(
  before: NormalizedFootprint,
  after: NormalizedFootprint,
): FootprintDiffEntry[] {
  const entries = new Map<string, FootprintDiffEntry>();

  const allHashes = new Set<string>([
    ...before.readOnly.keys(),
    ...before.readWrite.keys(),
    ...after.readOnly.keys(),
    ...after.readWrite.keys(),
  ]);

  for (const hash of allHashes) {
    const inBeforeRO = before.readOnly.has(hash);
    const inBeforeRW = before.readWrite.has(hash);
    const inAfterRO = after.readOnly.has(hash);
    const inAfterRW = after.readWrite.has(hash);

    const wasPresent = inBeforeRO || inBeforeRW;
    const isPresent = inAfterRO || inAfterRW;

    const key = (
      before.readOnly.get(hash) ??
      before.readWrite.get(hash) ??
      after.readOnly.get(hash) ??
      after.readWrite.get(hash)
    )!;

    let kind: FootprintChangeKind;
    let accessBefore: FootprintDiffEntry['accessBefore'];
    let accessAfter: FootprintDiffEntry['accessAfter'];

    if (!wasPresent && isPresent) {
      kind = 'added';
      accessAfter = inAfterRO ? 'readOnly' : 'readWrite';
    } else if (wasPresent && !isPresent) {
      kind = 'removed';
      accessBefore = inBeforeRO ? 'readOnly' : 'readWrite';
    } else if (wasPresent && isPresent) {
      const beforeMode: FootprintDiffEntry['accessBefore'] = inBeforeRO ? 'readOnly' : 'readWrite';
      const afterMode: FootprintDiffEntry['accessAfter'] = inAfterRO ? 'readOnly' : 'readWrite';
      if (beforeMode !== afterMode) {
        kind = 'access-mode-changed';
        accessBefore = beforeMode;
        accessAfter = afterMode;
      } else {
        kind = 'unchanged';
        accessBefore = beforeMode;
        accessAfter = afterMode;
      }
    } else {
      kind = 'unknown';
    }

    const partialEntry: Partial<FootprintDiffEntry> = { accessBefore, accessAfter };
    const { risk, riskDetail } = classifyRisk(kind, partialEntry);

    entries.set(hash, {
      hash,
      keyType: keyTypeName(key),
      kind,
      risk,
      accessBefore,
      accessAfter,
      riskDetail,
    });
  }

  // Canonical sort by hash for determinism.
  return Array.from(entries.values()).sort((a, b) => a.hash.localeCompare(b.hash));
}

// ─── Public API ───────────────────────────────────────────────────────────────

export interface CompareFootprintsOptions {
  before: { readOnly: LedgerKey[]; readWrite: LedgerKey[] };
  after: { readOnly: LedgerKey[]; readWrite: LedgerKey[] };
  gasBefore?: GasEstimation;
  gasAfter?: GasEstimation;
}

/**
 * Compare two raw footprints and return a schema-versioned diff result.
 *
 * This is the primary public entry point. Normalisation, comparison, and
 * risk classification all happen here.
 *
 * @example
 * const diff = compareFootprints({ before: txMeta1.footprint, after: txMeta2.footprint });
 * if (diff.hasRisk) {
 *   console.warn('Footprint change has risk findings');
 * }
 */
export function compareFootprints(opts: CompareFootprintsOptions): FootprintDiffResult {
  const before = normalizeFootprint(opts.before);
  const after = normalizeFootprint(opts.after);
  const entries = diffNormalizedFootprints(before, after);

  const summary: FootprintDiffSummary = {
    added: 0,
    removed: 0,
    accessModeChanged: 0,
    unchanged: 0,
    unknown: 0,
    highRisk: 0,
    mediumRisk: 0,
  };

  for (const e of entries) {
    switch (e.kind) {
      case 'added': summary.added++; break;
      case 'removed': summary.removed++; break;
      case 'access-mode-changed': summary.accessModeChanged++; break;
      case 'unchanged': summary.unchanged++; break;
      default: summary.unknown++; break;
    }
    if (e.risk === 'high') summary.highRisk++;
    if (e.risk === 'medium') summary.mediumRisk++;
  }

  return {
    schemaVersion: FOOTPRINT_DIFF_SCHEMA_VERSION,
    entries,
    summary,
    gasBefore: opts.gasBefore,
    gasAfter: opts.gasAfter,
    hasRisk: summary.highRisk > 0 || summary.mediumRisk > 0,
  };
}
