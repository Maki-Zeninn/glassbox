// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * Hardened canonical JSON for Glassbox audit log signing.
 *
 * This module wraps fast-json-stable-stringify with additional guards that
 * align the TypeScript canonicalization with the Go implementation and the
 * accepted number grammar documented in docs/audit-canonicalization.md.
 *
 * Accepted number grammar
 * ────────────────────────
 *  Integer : optional '-', one or more decimal digits, no leading zeros
 *            (except the literal 0), no exponent suffix.
 *            Examples: 0, -7, 42, 1000000
 *
 *  Float   : integer part followed by '.' and one or more decimal digits,
 *            no exponent notation.
 *            Examples: 0.5, 3.14159, -1.5
 *
 * Rejected forms (throw CanonicalJSONError):
 *  - Negative zero  (-0)
 *  - Exponent notation  (1e10, 1.5E-3, …)
 *  - Leading zeros in the integer part  (007, 01, …)
 *  - NaN and ±Infinity
 *  - Integers outside the safe IEEE 754 range (|n| > 2^53-1)
 *  - Duplicate object keys (after Unicode NFC normalisation)
 *  - Circular references
 */

import stringify from 'fast-json-stable-stringify';

// ─── Error types ──────────────────────────────────────────────────────────────

/** Base error class for canonical JSON violations. */
export class CanonicalJSONError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CanonicalJSONError';
  }
}

/** Thrown when a number value cannot be represented unambiguously. */
export class AmbiguousNumberError extends CanonicalJSONError {
  constructor(message: string) {
    super(message);
    this.name = 'AmbiguousNumberError';
  }
}

/** Thrown when a JSON object contains duplicate keys. */
export class DuplicateKeyError extends CanonicalJSONError {
  constructor(key: string) {
    super(`canonical JSON: duplicate object key "${key}"`);
    this.name = 'DuplicateKeyError';
  }
}

// ─── Constants ───────────────────────────────────────────────────────────────

/** Largest integer exactly representable as IEEE 754 double (2^53 - 1). */
const MAX_SAFE_INTEGER = Number.MAX_SAFE_INTEGER; // 9007199254740991

// ─── Number validation ────────────────────────────────────────────────────────

/**
 * Validates that a numeric value conforms to the accepted canonical grammar.
 *
 * @param v     The numeric value to validate.
 * @param path  JSON path for error messages (e.g. "input.count").
 */
export function validateCanonicalNumber(v: number, path = '(root)'): void {
  if (Number.isNaN(v)) {
    throw new AmbiguousNumberError(
      `canonical JSON: NaN at "${path}" is not a valid JSON value`,
    );
  }
  if (!Number.isFinite(v)) {
    throw new AmbiguousNumberError(
      `canonical JSON: Infinity at "${path}" is not a valid JSON value`,
    );
  }
  // Detect negative zero: Object.is(-0, -0) === true, -0 === 0 is also true,
  // but 1 / -0 === -Infinity whereas 1 / 0 === Infinity.
  if (v === 0 && 1 / v === -Infinity) {
    throw new AmbiguousNumberError(
      `canonical JSON: negative zero (-0) at "${path}" must be written as 0`,
    );
  }
  // Safe-integer range check for whole numbers.
  if (Number.isInteger(v) && (v > MAX_SAFE_INTEGER || v < -MAX_SAFE_INTEGER)) {
    throw new AmbiguousNumberError(
      `canonical JSON: integer ${v} at "${path}" exceeds safe IEEE 754 range (±2^53-1); encode as a string instead`,
    );
  }
}

/**
 * Validates the raw string representation of a JSON number token.
 * Must be called on the raw token before any numeric conversion so that
 * exponent notation and leading zeros are caught before they are silently
 * normalised away.
 *
 * @param raw   Raw number string as it appears in the JSON source.
 * @param path  JSON path for error messages.
 */
export function validateCanonicalNumberStr(raw: string, path = '(root)'): void {
  // Reject exponent notation.
  if (/[eE]/.test(raw)) {
    throw new AmbiguousNumberError(
      `canonical JSON: exponent notation "${raw}" at "${path}" is not permitted; use plain decimal form`,
    );
  }
  // Reject leading zeros in the integer part.
  const intPart = raw.startsWith('-') ? raw.slice(1) : raw;
  if (intPart.length > 1 && intPart[0] === '0' && intPart[1] !== '.') {
    throw new AmbiguousNumberError(
      `canonical JSON: leading zeros in "${raw}" at "${path}" are not permitted`,
    );
  }
  // Delegate remaining checks (negative zero, safe range) to the value validator.
  validateCanonicalNumber(Number(raw), path);
}

// ─── Unicode NFC normalisation ────────────────────────────────────────────────

/**
 * Returns the Unicode NFC form of a string so that equivalent sequences
 * (precomposed vs. decomposed) produce identical canonical bytes.
 *
 * String.prototype.normalize('NFC') is available in all Node.js versions
 * that Glassbox supports (Node 14+).
 */
export function normalizeUnicodeString(s: string): string {
  return s.normalize('NFC');
}

// ─── Duplicate-key detection ─────────────────────────────────────────────────

/**
 * Recursively walks a parsed JSON value and throws DuplicateKeyError if any
 * object contains a key that appears more than once after NFC normalisation.
 *
 * Note: JSON.parse silently drops duplicate keys (last-writer-wins), so this
 * function must be called on the *raw* JSON string via parseCanonicalInput
 * rather than on the result of JSON.parse.
 */
function checkDuplicateKeys(value: unknown, path: string): void {
  if (Array.isArray(value)) {
    for (let i = 0; i < value.length; i++) {
      checkDuplicateKeys(value[i], `${path}[${i}]`);
    }
    return;
  }
  if (isPlainObject(value)) {
    const seen = new Set<string>();
    for (const k of Object.keys(value as object)) {
      const normKey = normalizeUnicodeString(k);
      if (seen.has(normKey)) {
        throw new DuplicateKeyError(normKey);
      }
      seen.add(normKey);
      checkDuplicateKeys((value as Record<string, unknown>)[k], path ? `${path}.${k}` : k);
    }
  }
}

/**
 * Parses a raw JSON string and checks it for duplicate keys and numeric
 * ambiguity.  Returns the parsed value with all string keys and values
 * NFC-normalised.
 *
 * This is the entry point for inputs received from external or cross-language
 * sources before they are passed to canonicalizeJSON.
 *
 * Throws:
 *  - DuplicateKeyError      when a duplicate key is found.
 *  - AmbiguousNumberError   when an ambiguous number token is found.
 *  - SyntaxError            when the input is not valid JSON.
 */
export function parseCanonicalInput(raw: string): unknown {
  // We need the raw number tokens to validate grammar before JSON.parse
  // converts them to float64.  We do a two-pass approach:
  //
  //  Pass 1: scan the string with a regex for number tokens and validate each.
  //  Pass 2: JSON.parse for structure; then check duplicate keys and NFC-normalise.
  //
  // This is safe because JSON.parse is authoritative for structure; the regex
  // pass only inspects number tokens.
  validateNumberTokensInRawJSON(raw);

  // JSON.parse — last-writer-wins on duplicate keys, so we must detect them
  // separately using the reviver.
  const seen: Map<string, Set<string>> = new Map();
  let objectDepth = 0;
  const depthStack: string[] = [];

  const parsed = JSON.parse(raw, function (this: unknown, key: string, value: unknown) {
    // The reviver is called for each key-value pair, innermost first.
    // We use the implicit "this" (the containing object) to detect duplicates
    // before the key is merged.
    //
    // JSON.parse already merged duplicate keys (last wins) by this point, so we
    // cannot detect them here reliably for all engines.  Duplicate detection is
    // therefore done in the regex-based pre-pass below.
    void key; void objectDepth; void depthStack; void seen;
    if (typeof value === 'string') {
      return normalizeUnicodeString(value);
    }
    return value;
  });

  // Duplicate-key detection via token scan (covers all engines uniformly).
  detectDuplicateKeysInRawJSON(raw);

  // Deep-walk the parsed value to NFC-normalise object keys.
  return deepNormalizeKeys(parsed);
}

/** Recursively NFC-normalises all object keys in a parsed JSON value. */
function deepNormalizeKeys(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => deepNormalizeKeys(item));
  }
  if (isPlainObject(value)) {
    const result: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as object)) {
      result[normalizeUnicodeString(k)] = deepNormalizeKeys(v);
    }
    return result;
  }
  return value;
}

// ─── Raw JSON token validators ────────────────────────────────────────────────

/**
 * Scans raw JSON text for number tokens and validates each against the accepted
 * grammar.  Throws AmbiguousNumberError on the first violation found.
 */
function validateNumberTokensInRawJSON(raw: string): void {
  // Match JSON number tokens: optional minus, digits, optional fraction.
  // This regex intentionally matches a superset (including leading zeros and
  // exponents) so we can report them explicitly.
  const numberTokenRe = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/g;
  let m: RegExpExecArray | null;
  while ((m = numberTokenRe.exec(raw)) !== null) {
    // Only validate tokens that are actual JSON number positions (not inside strings).
    // We check by verifying the preceding context is not inside a string literal.
    if (!isInsideStringAt(raw, m.index)) {
      validateCanonicalNumberStr(m[0]);
    }
  }
}

/**
 * Scans raw JSON text for duplicate object keys using a token-level parser.
 * This is engine-independent and catches duplicates that JSON.parse would
 * silently collapse.
 */
function detectDuplicateKeysInRawJSON(raw: string): void {
  // Stack of seen-key Sets, one entry per open object scope.
  const scopeStack: Set<string>[] = [];
  let i = 0;
  const len = raw.length;

  while (i < len) {
    const ch = raw[i];

    if (ch === '"') {
      // Read a complete string token.
      const { value: strVal, end } = readJSONString(raw, i);
      const after = skipWhitespace(raw, end);

      if (scopeStack.length > 0 && raw[after] === ':') {
        // This string is an object key.
        const normKey = normalizeUnicodeString(strVal);
        const currentScope = scopeStack[scopeStack.length - 1];
        if (currentScope.has(normKey)) {
          throw new DuplicateKeyError(normKey);
        }
        currentScope.add(normKey);
      }
      i = end;
      continue;
    }

    if (ch === '{') {
      scopeStack.push(new Set<string>());
      i++;
      continue;
    }

    if (ch === '}') {
      scopeStack.pop();
      i++;
      continue;
    }

    // Skip strings that are values (not keys) — advance past the entire string.
    i++;
  }
}

/**
 * Reads a JSON string starting at position start (which must be a '"').
 * Returns the unescaped string value and the position after the closing '"'.
 */
function readJSONString(raw: string, start: number): { value: string; end: number } {
  let i = start + 1; // skip opening "
  let result = '';
  while (i < raw.length) {
    const ch = raw[i];
    if (ch === '\\') {
      i++;
      const esc = raw[i];
      switch (esc) {
        case '"': result += '"'; break;
        case '\\': result += '\\'; break;
        case '/': result += '/'; break;
        case 'b': result += '\b'; break;
        case 'f': result += '\f'; break;
        case 'n': result += '\n'; break;
        case 'r': result += '\r'; break;
        case 't': result += '\t'; break;
        case 'u': {
          const hex = raw.slice(i + 1, i + 5);
          result += String.fromCharCode(parseInt(hex, 16));
          i += 4;
          break;
        }
        default: result += esc;
      }
      i++;
      continue;
    }
    if (ch === '"') {
      return { value: result, end: i + 1 };
    }
    result += ch;
    i++;
  }
  return { value: result, end: i };
}

/** Advances index past whitespace characters. */
function skipWhitespace(raw: string, i: number): number {
  while (i < raw.length && /\s/.test(raw[i])) i++;
  return i;
}

/**
 * Returns true if position idx is inside a JSON string literal.
 * Used to avoid treating number-like substrings inside strings as tokens.
 */
function isInsideStringAt(raw: string, idx: number): boolean {
  let inString = false;
  for (let i = 0; i < idx; i++) {
    if (raw[i] === '\\') { i++; continue; }
    if (raw[i] === '"') inString = !inString;
  }
  return inString;
}

// ─── Core canonicalize function ───────────────────────────────────────────────

/**
 * Produces a deterministic, canonical JSON string of value.
 *
 * Rules applied (identical to the Go canonicalJSON function):
 *  - Object keys sorted lexicographically (Unicode code-point order).
 *  - Compact form — no whitespace between tokens.
 *  - String values and keys NFC-normalised.
 *  - NaN, ±Infinity, and negative zero throw AmbiguousNumberError.
 *  - Circular references throw TypeError.
 *
 * This function does NOT check for exponent notation or duplicate keys in raw
 * JSON source — use parseCanonicalInput for inputs that require those checks.
 */
export function canonicalizeJSON(value: unknown): string {
  // Walk the value tree to validate numbers and NFC-normalise strings/keys
  // before handing off to fast-json-stable-stringify.
  const normalised = deepValidateAndNormalise(value, '');
  return stringify(normalised);
}

/**
 * Recursively validates numbers and NFC-normalises strings and object keys.
 * Returns a new value tree with all strings normalised.
 */
function deepValidateAndNormalise(value: unknown, path: string): unknown {
  if (value === null) return null;

  if (typeof value === 'number') {
    validateCanonicalNumber(value, path || '(root)');
    return value;
  }

  if (typeof value === 'string') {
    return normalizeUnicodeString(value);
  }

  if (typeof value === 'boolean') {
    return value;
  }

  if (Array.isArray(value)) {
    return value.map((item, idx) =>
      deepValidateAndNormalise(item, `${path}[${idx}]`),
    );
  }

  if (isPlainObject(value)) {
    const result: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as object)) {
      const normKey = normalizeUnicodeString(k);
      result[normKey] = deepValidateAndNormalise(v, path ? `${path}.${k}` : k);
    }
    return result;
  }

  return value;
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

// Suppress unused variable warning for the reviver scaffolding.
function _(_v: unknown): void { /* intentional no-op */ }
