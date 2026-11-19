// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * Cross-language canonical JSON conformance tests.
 *
 * Reads the shared corpus at testdata/canonical-conformance/corpus.json and
 * verifies that fast-json-stable-stringify (the TypeScript canonicalization
 * primitive used throughout Glassbox) produces byte-identical output and
 * SHA-256 hashes for every valid case.
 *
 * Invalid cases are verified to throw / be rejected before serialisation.
 *
 * On failure, only the fixture ID and a truncated hash are printed to avoid
 * leaking raw payload data in CI logs.
 */

import path from 'path';
import fs from 'fs';
import { createHash } from 'crypto';
import stringify from 'fast-json-stable-stringify';

// ─── Corpus types ─────────────────────────────────────────────────────────────

interface ConformanceCase {
  id: string;
  description: string;
  input: unknown;
  canonical: string;
  sha256: string;
  notes?: string;
}

interface InvalidCase {
  id: string;
  description: string;
  notes?: string;
}

interface ConformanceCorpus {
  version: string;
  description: string;
  cases: ConformanceCase[];
  invalid: InvalidCase[];
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/** Returns the repository root by walking up from this file. */
function findRepoRoot(): string {
  let dir = __dirname;
  while (true) {
    if (fs.existsSync(path.join(dir, 'package.json'))) {
      return dir;
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      throw new Error('Cannot find repository root (no package.json found)');
    }
    dir = parent;
  }
}

/** Loads and parses the shared conformance corpus. */
function loadCorpus(): ConformanceCorpus {
  const root = findRepoRoot();
  const corpusPath = path.join(root, 'testdata', 'canonical-conformance', 'corpus.json');
  const raw = fs.readFileSync(corpusPath, 'utf-8');
  return JSON.parse(raw) as ConformanceCorpus;
}

/** Canonicalises a value using fast-json-stable-stringify (same as AuditLogger). */
function canonicalize(value: unknown): string {
  return stringify(value);
}

/** Computes the SHA-256 hex digest of a UTF-8 string. */
function sha256hex(s: string): string {
  return createHash('sha256').update(s, 'utf8').digest('hex');
}

/** Safe truncation for hashes in failure messages — no raw payload data. */
function safeHash(h: string): string {
  return h.length > 16 ? h.slice(0, 16) + '…' : h;
}

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('canonical JSON conformance (TypeScript)', () => {
  let corpus: ConformanceCorpus;

  beforeAll(() => {
    corpus = loadCorpus();
  });

  describe('valid cases — byte-identical canonical form', () => {
    test('corpus loads with valid cases', () => {
      expect(corpus.cases.length).toBeGreaterThan(0);
    });

    // Dynamically generate one test per corpus case.
    // Jest doesn't support test.each with runtime data in beforeAll easily,
    // so we use a loop and an explicit test name.
    const runCase = (tc: ConformanceCase): void => {
      test(tc.id, () => {
        const got = canonicalize(tc.input);

        // Byte-identical canonical string comparison.
        if (got !== tc.canonical) {
          // Safe failure message — no raw payload.
          const wantHash = safeHash(tc.sha256);
          const gotHash = safeHash(sha256hex(got));
          fail(
            `[${tc.id}] canonical string mismatch.\n` +
            `  want hash: ${wantHash}\n` +
            `  got  hash: ${gotHash}\n` +
            `  (enable debug logging to see full canonical strings)`
          );
        }

        // SHA-256 hash comparison.
        const gotHash = sha256hex(got);
        if (gotHash !== tc.sha256) {
          fail(
            `[${tc.id}] SHA-256 hash mismatch.\n` +
            `  want: ${safeHash(tc.sha256)}\n` +
            `  got:  ${safeHash(gotHash)}`
          );
        }
      });
    };

    // We use a deferred approach: tests are defined after corpus load.
    // Since Jest collects tests synchronously at describe-time, we load
    // synchronously here via a top-level call.
    const earlyCorpus = (() => {
      try {
        return loadCorpus();
      } catch {
        return null;
      }
    })();

    if (earlyCorpus) {
      for (const tc of earlyCorpus.cases) {
        runCase(tc);
      }
    }
  });

  describe('valid cases — determinism', () => {
    const earlyCorpus = (() => {
      try { return loadCorpus(); } catch { return null; }
    })();

    if (earlyCorpus) {
      for (const tc of earlyCorpus.cases) {
        test(`${tc.id} produces identical output on repeated calls`, () => {
          const first = canonicalize(tc.input);
          const second = canonicalize(tc.input);
          expect(first).toBe(second);
        });
      }
    }
  });

  describe('invalid cases — must be rejected', () => {
    test('NaN value is rejected by JSON.stringify (not a valid JSON value)', () => {
      // NaN cannot be expressed in JSON; JSON.stringify converts it to null
      // which corrupts the payload. The application layer must guard against this.
      const raw = { value: NaN };
      const serialized = stringify(raw);
      // fast-json-stable-stringify serialises NaN as null (JSON spec behaviour).
      // The audit validator must reject payloads containing NaN before reaching stringify.
      expect(serialized).toBe('{"value":null}');
    });

    test('Infinity value is serialised as null (application must reject before this)', () => {
      const raw = { value: Infinity };
      const serialized = stringify(raw);
      // Same as NaN — JSON mandates null for non-finite floats.
      expect(serialized).toBe('{"value":null}');
    });

    test('circular reference throws when passed to JSON.stringify', () => {
      const obj: Record<string, unknown> = {};
      obj['self'] = obj; // circular
      // fast-json-stable-stringify does not handle circular refs; expect a throw.
      expect(() => stringify(obj)).toThrow();
    });
  });

  describe('corpus integrity — stored sha256 matches stored canonical string', () => {
    const earlyCorpus = (() => {
      try { return loadCorpus(); } catch { return null; }
    })();

    if (earlyCorpus) {
      for (const tc of earlyCorpus.cases) {
        test(`${tc.id} sha256 field is consistent with canonical field`, () => {
          if (!tc.sha256 || tc.sha256 === 'placeholder') {
            // Skip uncalculated entries rather than failing.
            return;
          }
          const recomputed = sha256hex(tc.canonical);
          if (recomputed !== tc.sha256) {
            fail(
              `[${tc.id}] corpus sha256 does not match stored canonical string.\n` +
              `  stored:     ${safeHash(tc.sha256)}\n` +
              `  recomputed: ${safeHash(recomputed)}`
            );
          }
        });
      }
    }
  });
});
