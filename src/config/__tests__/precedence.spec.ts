// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import {
  resolveConfig,
  flattenResolvedConfig,
  maskSensitiveValue,
  ENV_VARS,
  type RawConfigInput,
} from '../precedence';

const BASE_CLI: RawConfigInput = {
  source: 'cli',
  urls: 'https://cli.rpc.example',
  timeout: 5000,
  retries: 2,
  retryDelay: 500,
  circuitBreakerThreshold: 3,
  circuitBreakerTimeout: 30000,
  maxRedirects: 3,
};

const originalEnv = process.env;

beforeEach(() => {
  process.env = { ...originalEnv };
  // Clear all glassbox env vars before each test
  for (const key of Object.values(ENV_VARS)) {
    delete process.env[key];
  }
});

afterAll(() => {
  process.env = originalEnv;
});

describe('resolveConfig — precedence matrix', () => {
  it('cli beats env beats file beats default', () => {
    process.env[ENV_VARS.timeout] = '9999';
    const { config } = resolveConfig([
      { source: 'cli', urls: 'https://cli.rpc.example', timeout: 1111 },
      { source: 'file', timeout: 2222 },
    ]);
    expect(config.timeout.value).toBe(1111);
    expect(config.timeout.source).toBe('cli');
  });

  it('env beats file when no cli value present', () => {
    process.env[ENV_VARS.timeout] = '7777';
    const { config } = resolveConfig([
      { source: 'cli', urls: 'https://cli.rpc.example' },
      { source: 'file', timeout: 2222 },
    ]);
    expect(config.timeout.value).toBe(7777);
    expect(config.timeout.source).toBe('env');
  });

  it('file beats default when no cli/env value present', () => {
    const { config } = resolveConfig([
      { source: 'cli', urls: 'https://cli.rpc.example' },
      { source: 'file', timeout: 4444 },
    ]);
    expect(config.timeout.value).toBe(4444);
    expect(config.timeout.source).toBe('file');
  });

  it('falls back to defaults when only urls provided', () => {
    const { config } = resolveConfig([{ source: 'cli', urls: 'https://rpc.example' }]);
    expect(config.timeout.value).toBe(30000);
    expect(config.timeout.source).toBe('default');
    expect(config.retries.value).toBe(3);
    expect(config.retryDelay.value).toBe(1000);
  });
});

describe('resolveConfig — URLs', () => {
  it('accepts comma-separated URL string', () => {
    const { config } = resolveConfig([
      { source: 'cli', urls: 'https://a.example,https://b.example' },
    ]);
    expect(config.urls.value).toEqual(['https://a.example', 'https://b.example']);
  });

  it('accepts array of URLs', () => {
    const { config } = resolveConfig([
      { source: 'cli', urls: ['https://a.example', 'https://b.example'] },
    ]);
    expect(config.urls.value).toEqual(['https://a.example', 'https://b.example']);
  });

  it('reads URL from env when no cli url given', () => {
    process.env[ENV_VARS.urls] = 'https://env.rpc.example';
    const { config } = resolveConfig([{ source: 'cli' }]);
    expect(config.urls.value).toEqual(['https://env.rpc.example']);
    expect(config.urls.source).toBe('env');
  });

  it('throws when no urls from any source', () => {
    expect(() => resolveConfig([{ source: 'cli' }])).toThrow('"urls"');
  });
});

describe('resolveConfig — conflict diagnostics', () => {
  it('records conflict when cli and env both provide timeout', () => {
    process.env[ENV_VARS.timeout] = '8888';
    const { conflicts } = resolveConfig([
      { source: 'cli', urls: 'https://rpc.example', timeout: 1234 },
    ]);
    const conflict = conflicts.find(c => c.field === 'timeout');
    expect(conflict).toBeDefined();
    expect(conflict!.winner).toBe('cli');
    expect(conflict!.losers.map(l => l.source)).toContain('env');
  });

  it('records no conflict when only one source provides a value', () => {
    const { conflicts } = resolveConfig([
      { source: 'cli', urls: 'https://rpc.example', timeout: 5000 },
    ]);
    const timeoutConflict = conflicts.find(c => c.field === 'timeout');
    expect(timeoutConflict).toBeUndefined();
  });
});

describe('resolveConfig — invalid / malformed values', () => {
  it('skips non-integer string and falls back to next source', () => {
    const { config, invalidFields } = resolveConfig([
      { source: 'cli', urls: 'https://rpc.example', timeout: 'not-a-number' as any },
      { source: 'file', timeout: 2000 },
    ]);
    expect(invalidFields.some(f => f.field === 'timeout')).toBe(true);
    expect(config.timeout.value).toBe(2000);
    expect(config.timeout.source).toBe('file');
  });

  it('falls back to default when all user-provided timeout values are invalid', () => {
    const { config, invalidFields } = resolveConfig([
      { source: 'cli', urls: 'https://rpc.example', timeout: 'bad' as any },
      { source: 'file', timeout: 'also-bad' as any },
    ]);
    // Both bad values are recorded as invalid.
    expect(invalidFields.filter(f => f.field === 'timeout').length).toBeGreaterThanOrEqual(2);
    // Default (30000) is used as the fallback.
    expect(config.timeout.value).toBe(30000);
    expect(config.timeout.source).toBe('default');
  });

  it('records empty-string URL as invalid and falls back', () => {
    process.env[ENV_VARS.urls] = 'https://env.rpc.example';
    const { config, invalidFields } = resolveConfig([
      { source: 'cli', urls: '' },
    ]);
    expect(invalidFields.some(f => f.field === 'urls')).toBe(true);
    expect(config.urls.source).toBe('env');
  });
});

describe('resolveConfig — table-driven source matrix', () => {
  const cases: Array<{
    label: string;
    inputs: RawConfigInput[];
    env: Partial<Record<keyof typeof ENV_VARS, string>>;
    expectedSource: Record<string, string>;
  }> = [
    {
      label: 'cli only',
      inputs: [BASE_CLI],
      env: {},
      expectedSource: {
        timeout: 'cli',
        retries: 'cli',
        urls: 'cli',
      },
    },
    {
      label: 'env only',
      inputs: [{ source: 'cli', urls: 'https://rpc.example' }],
      env: { timeout: '12000', retries: '7' },
      expectedSource: { timeout: 'env', retries: 'env' },
    },
    {
      label: 'file only',
      inputs: [
        { source: 'cli', urls: 'https://rpc.example' },
        { source: 'file', timeout: 11000, retries: 6 },
      ],
      env: {},
      expectedSource: { timeout: 'file', retries: 'file' },
    },
    {
      label: 'all sources — cli wins',
      inputs: [
        { source: 'cli', urls: 'https://rpc.example', timeout: 1000 },
        { source: 'file', timeout: 2000 },
      ],
      env: { timeout: '3000' },
      expectedSource: { timeout: 'cli' },
    },
  ];

  it.each(cases)('$label', ({ inputs, env, expectedSource }) => {
    for (const [k, v] of Object.entries(env)) {
      process.env[ENV_VARS[k as keyof typeof ENV_VARS]] = v;
    }
    const { config } = resolveConfig(inputs);
    for (const [field, src] of Object.entries(expectedSource)) {
      expect((config as any)[field].source).toBe(src);
    }
  });
});

describe('flattenResolvedConfig', () => {
  it('returns plain values without source metadata', () => {
    const { config } = resolveConfig([BASE_CLI]);
    const flat = flattenResolvedConfig(config);
    expect(flat.timeout).toBe(5000);
    expect(flat.retries).toBe(2);
    expect(flat.urls).toEqual(['https://cli.rpc.example']);
    expect((flat as any).timeout?.source).toBeUndefined();
  });
});

describe('maskSensitiveValue', () => {
  it('masks known sensitive fields', () => {
    expect(maskSensitiveValue('softwarePrivateKeyPem', 'secret')).toBe('[REDACTED]');
    expect(maskSensitiveValue('kmsKeyId', 'arn:aws:kms:...')).toBe('[REDACTED]');
    expect(maskSensitiveValue('apiKey', 'abc123')).toBe('[REDACTED]');
  });

  it('does not mask non-sensitive fields', () => {
    expect(maskSensitiveValue('timeout', 30000)).toBe('30000');
    expect(maskSensitiveValue('urls', 'https://rpc.example')).toBe('https://rpc.example');
  });
});
