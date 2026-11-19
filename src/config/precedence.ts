// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Configuration precedence resolution for glassbox.
 *
 * Source priority (highest → lowest):
 *   1. CLI flags   — explicit per-invocation values
 *   2. Environment — process.env variables
 *   3. File        — values from a config file (e.g. glassbox.toml)
 *   4. Defaults    — hard-coded safe defaults
 *
 * Sensitive fields (e.g. API keys, private key PEM) are masked in
 * diagnostic output and must never appear in logs.
 */

// ─── Source tags ──────────────────────────────────────────────────────────────

export type ConfigSource = 'cli' | 'env' | 'file' | 'default';

export const SOURCE_PRIORITY: Record<ConfigSource, number> = {
  cli: 4,
  env: 3,
  file: 2,
  default: 1,
};

// ─── Typed precedence matrix ──────────────────────────────────────────────────

/**
 * One resolved field in the configuration.
 * Carries provenance so consumers can diagnose conflicts.
 */
export interface ResolvedField<T> {
  value: T;
  source: ConfigSource;
}

/**
 * Fully-resolved RPC configuration with per-field provenance.
 */
export interface ResolvedRPCConfig {
  urls: ResolvedField<string[]>;
  timeout: ResolvedField<number>;
  retries: ResolvedField<number>;
  retryDelay: ResolvedField<number>;
  circuitBreakerThreshold: ResolvedField<number>;
  circuitBreakerTimeout: ResolvedField<number>;
  maxRedirects: ResolvedField<number>;
}

// ─── Conflict diagnostics ─────────────────────────────────────────────────────

export interface ConfigConflict {
  field: string;
  winner: ConfigSource;
  losers: Array<{ source: ConfigSource; rawValue: string }>;
  resolvedValue: unknown;
}

export interface PrecedenceResolutionResult {
  config: ResolvedRPCConfig;
  conflicts: ConfigConflict[];
  invalidFields: Array<{ field: string; rawValue: string; reason: string }>;
}

// ─── Sensitive field masking ──────────────────────────────────────────────────

const SENSITIVE_FIELDS = new Set<string>([
  'softwarePrivateKeyPem',
  'kmsKeyId',
  'authToken',
  'apiKey',
]);

export function maskSensitiveValue(field: string, value: unknown): string {
  if (SENSITIVE_FIELDS.has(field)) {
    return '[REDACTED]';
  }
  return String(value);
}

// ─── Raw config input per source ─────────────────────────────────────────────

export interface RawConfigInput {
  source: ConfigSource;
  urls?: string | string[];
  timeout?: string | number;
  retries?: string | number;
  retryDelay?: string | number;
  circuitBreakerThreshold?: string | number;
  circuitBreakerTimeout?: string | number;
  maxRedirects?: string | number;
}

// ─── Defaults ─────────────────────────────────────────────────────────────────

const DEFAULTS: Omit<RawConfigInput, 'source'> = {
  timeout: 30000,
  retries: 3,
  retryDelay: 1000,
  circuitBreakerThreshold: 5,
  circuitBreakerTimeout: 60000,
  maxRedirects: 5,
};

// ─── Env-variable names (documented) ─────────────────────────────────────────

export const ENV_VARS = {
  urls: 'STELLAR_RPC_URLS',
  timeout: 'GLASSBOX_TIMEOUT',
  retries: 'GLASSBOX_RETRIES',
  retryDelay: 'GLASSBOX_RETRY_DELAY',
  circuitBreakerThreshold: 'GLASSBOX_CB_THRESHOLD',
  circuitBreakerTimeout: 'GLASSBOX_CB_TIMEOUT',
  maxRedirects: 'GLASSBOX_MAX_REDIRECTS',
} as const;

// ─── Core resolver ────────────────────────────────────────────────────────────

function pickNumeric(
  field: string,
  inputs: Array<{ source: ConfigSource; rawValue: string | number | undefined }>,
  invalidFields: PrecedenceResolutionResult['invalidFields'],
  conflicts: ConfigConflict[],
): ResolvedField<number> {
  const valid: Array<{ source: ConfigSource; value: number }> = [];

  for (const { source, rawValue } of inputs) {
    if (rawValue === undefined || rawValue === '' || rawValue === null) continue;
    const n = Number(rawValue);
    if (!Number.isFinite(n) || !Number.isInteger(n)) {
      invalidFields.push({ field, rawValue: String(rawValue), reason: 'not a finite integer' });
      continue;
    }
    valid.push({ source, value: n });
  }

  if (valid.length === 0) {
    throw new Error(`[config] field "${field}" has no valid value from any source`);
  }

  valid.sort((a, b) => SOURCE_PRIORITY[b.source] - SOURCE_PRIORITY[a.source]);

  // Only report a conflict when 2+ *non-default* sources provide a value.
  const nonDefault = valid.filter(v => v.source !== 'default');
  if (nonDefault.length > 1) {
    conflicts.push({
      field,
      winner: valid[0].source,
      losers: valid.slice(1).filter(v => v.source !== 'default').map(v => ({ source: v.source, rawValue: String(v.value) })),
      resolvedValue: valid[0].value,
    });
  }

  return { value: valid[0].value, source: valid[0].source };
}

function pickUrls(
  inputs: Array<{ source: ConfigSource; rawValue: string | string[] | undefined }>,
  invalidFields: PrecedenceResolutionResult['invalidFields'],
  conflicts: ConfigConflict[],
): ResolvedField<string[]> {
  const valid: Array<{ source: ConfigSource; value: string[] }> = [];

  for (const { source, rawValue } of inputs) {
    if (rawValue === undefined || rawValue === null) continue;

    // Treat explicitly-provided empty values as invalid (record them).
    const isEmpty = rawValue === '' || (Array.isArray(rawValue) && rawValue.length === 0);
    if (isEmpty) {
      invalidFields.push({ field: 'urls', rawValue: String(rawValue), reason: 'empty value provided' });
      continue;
    }

    const arr = Array.isArray(rawValue)
      ? rawValue
      : String(rawValue).split(',').map(u => u.trim()).filter(Boolean);

    if (arr.length === 0) {
      invalidFields.push({ field: 'urls', rawValue: String(rawValue), reason: 'empty after splitting' });
      continue;
    }
    valid.push({ source, value: arr });
  }

  if (valid.length === 0) {
    throw new Error('[config] field "urls" has no valid value from any source');
  }

  valid.sort((a, b) => SOURCE_PRIORITY[b.source] - SOURCE_PRIORITY[a.source]);

  // Only report a conflict when 2+ non-default sources provide a value.
  const nonDefaultUrls = valid.filter(v => v.source !== 'default');
  if (nonDefaultUrls.length > 1) {
    conflicts.push({
      field: 'urls',
      winner: valid[0].source,
      losers: valid.slice(1).filter(v => v.source !== 'default').map(v => ({ source: v.source, rawValue: v.value.join(',') })),
      resolvedValue: valid[0].value,
    });
  }

  return { value: valid[0].value, source: valid[0].source };
}

/**
 * Resolve configuration from multiple sources according to the precedence matrix.
 *
 * Inputs are ordered from *highest* to *lowest* priority (cli first).
 * The function collects environment variables automatically and merges them
 * at their correct priority slot.
 *
 * @param inputs - Ordered array of raw config inputs (cli, file …).
 *                 The env source is added automatically from `process.env`.
 */
export function resolveConfig(inputs: RawConfigInput[]): PrecedenceResolutionResult {
  const conflicts: ConfigConflict[] = [];
  const invalidFields: PrecedenceResolutionResult['invalidFields'] = [];

  const env = typeof process !== 'undefined' ? process.env : {};
  const envInput: RawConfigInput = {
    source: 'env',
    urls: env[ENV_VARS.urls],
    timeout: env[ENV_VARS.timeout],
    retries: env[ENV_VARS.retries],
    retryDelay: env[ENV_VARS.retryDelay],
    circuitBreakerThreshold: env[ENV_VARS.circuitBreakerThreshold],
    circuitBreakerTimeout: env[ENV_VARS.circuitBreakerTimeout],
    maxRedirects: env[ENV_VARS.maxRedirects],
  };

  const defaultInput: RawConfigInput = { source: 'default', ...DEFAULTS };

  const all: RawConfigInput[] = [...inputs, envInput, defaultInput];

  const numericField = (
    field: keyof Omit<RawConfigInput, 'source' | 'urls'>,
  ): ResolvedField<number> =>
    pickNumeric(
      field,
      all.map(i => ({ source: i.source, rawValue: i[field] })),
      invalidFields,
      conflicts,
    );

  const urls = pickUrls(
    all.map(i => ({ source: i.source, rawValue: i.urls })),
    invalidFields,
    conflicts,
  );

  const config: ResolvedRPCConfig = {
    urls,
    timeout: numericField('timeout'),
    retries: numericField('retries'),
    retryDelay: numericField('retryDelay'),
    circuitBreakerThreshold: numericField('circuitBreakerThreshold'),
    circuitBreakerTimeout: numericField('circuitBreakerTimeout'),
    maxRedirects: numericField('maxRedirects'),
  };

  return { config, conflicts, invalidFields };
}

/**
 * Flatten a {@link ResolvedRPCConfig} to a plain object for runtime use.
 */
export function flattenResolvedConfig(
  resolved: ResolvedRPCConfig,
): {
  urls: string[];
  timeout: number;
  retries: number;
  retryDelay: number;
  circuitBreakerThreshold: number;
  circuitBreakerTimeout: number;
  maxRedirects: number;
} {
  return {
    urls: resolved.urls.value,
    timeout: resolved.timeout.value,
    retries: resolved.retries.value,
    retryDelay: resolved.retryDelay.value,
    circuitBreakerThreshold: resolved.circuitBreakerThreshold.value,
    circuitBreakerTimeout: resolved.circuitBreakerTimeout.value,
    maxRedirects: resolved.maxRedirects.value,
  };
}
