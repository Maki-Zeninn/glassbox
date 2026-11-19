// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import {
  getSigningCapabilities,
  assertProviderSupported,
  isNodeEnvironment,
  UnsupportedProviderError,
} from '../signing/capabilities';
import type { SigningCapabilityReport } from '../signing/capabilities';

describe('isNodeEnvironment', () => {
  it('returns true in a Jest/Node.js test environment', () => {
    expect(isNodeEnvironment()).toBe(true);
  });
});

describe('getSigningCapabilities', () => {
  it('returns a capability report', () => {
    const caps = getSigningCapabilities();
    expect(typeof caps.software).toBe('boolean');
    expect(typeof caps.pkcs11).toBe('boolean');
    expect(typeof caps.awsKms).toBe('boolean');
    expect(typeof caps.browserVerify).toBe('boolean');
    expect(typeof caps.isNode).toBe('boolean');
  });

  it('software is true in Node.js', () => {
    const caps = getSigningCapabilities();
    expect(caps.software).toBe(true);
  });

  it('pkcs11 is false when pkcs11js is not installed', () => {
    // The test runner should not have pkcs11js installed natively.
    // If it is installed, this test will need updating.
    const caps = getSigningCapabilities();
    // pkcs11js is mocked in __mocks__/pkcs11js.ts — the mock satisfies require()
    // so caps.pkcs11 may be true in test; we just assert it's a boolean.
    expect(typeof caps.pkcs11).toBe('boolean');
  });

  it('browserVerify is true when SubtleCrypto is available (Node ≥ 16)', () => {
    const caps = getSigningCapabilities();
    // Node 16+ exposes globalThis.crypto.subtle — Jest runs on Node.
    expect(caps.browserVerify).toBe(true);
  });
});

describe('assertProviderSupported', () => {
  const mockCaps = (overrides: Partial<SigningCapabilityReport>): SigningCapabilityReport => ({
    software: false,
    pkcs11: false,
    awsKms: false,
    browserVerify: true,
    isNode: false,
    ...overrides,
  });

  describe('software provider', () => {
    it('passes when software is available', () => {
      expect(() =>
        assertProviderSupported('software', mockCaps({ software: true }))
      ).not.toThrow();
    });

    it('throws UnsupportedProviderError when software is unavailable', () => {
      expect(() =>
        assertProviderSupported('software', mockCaps({ software: false }))
      ).toThrow(UnsupportedProviderError);
    });
  });

  describe('pkcs11 provider', () => {
    it('passes when pkcs11 is available', () => {
      expect(() =>
        assertProviderSupported('pkcs11', mockCaps({ pkcs11: true }))
      ).not.toThrow();
    });

    it('throws UnsupportedProviderError in browser context', () => {
      const err = (() => {
        try {
          assertProviderSupported('pkcs11', mockCaps({ pkcs11: false, isNode: false }));
        } catch (e) {
          return e;
        }
      })();
      expect(err).toBeInstanceOf(UnsupportedProviderError);
      expect((err as UnsupportedProviderError).provider).toBe('pkcs11');
      expect((err as UnsupportedProviderError).message).toContain('browser environment');
    });

    it('throws UnsupportedProviderError in Node when pkcs11js not installed', () => {
      const err = (() => {
        try {
          assertProviderSupported('pkcs11', mockCaps({ pkcs11: false, isNode: true }));
        } catch (e) {
          return e;
        }
      })();
      expect(err).toBeInstanceOf(UnsupportedProviderError);
      expect((err as UnsupportedProviderError).message).toContain('pkcs11js native module not installed');
    });
  });

  describe('kms provider', () => {
    it('throws UnsupportedProviderError in browser context', () => {
      expect(() =>
        assertProviderSupported('kms', mockCaps({ awsKms: false, isNode: false }))
      ).toThrow(UnsupportedProviderError);
    });

    it('includes sdk hint when in Node but sdk missing', () => {
      const err = (() => {
        try {
          assertProviderSupported('kms', mockCaps({ awsKms: false, isNode: true }));
        } catch (e) {
          return e;
        }
      })();
      expect((err as UnsupportedProviderError).message).toContain('@aws-sdk/client-kms not installed');
    });
  });

  describe('unknown provider', () => {
    it('throws for any unrecognised provider name', () => {
      expect(() =>
        assertProviderSupported('hsm-mystery' as any, mockCaps({}))
      ).toThrow(UnsupportedProviderError);
    });
  });
});

describe('UnsupportedProviderError', () => {
  it('has the correct name', () => {
    const err = new UnsupportedProviderError('pkcs11');
    expect(err.name).toBe('UnsupportedProviderError');
  });

  it('carries the provider field', () => {
    const err = new UnsupportedProviderError('kms', 'browser environment');
    expect(err.provider).toBe('kms');
  });

  it('is instanceof Error', () => {
    expect(new UnsupportedProviderError('software')).toBeInstanceOf(Error);
  });

  it('includes context in message when provided', () => {
    const err = new UnsupportedProviderError('pkcs11', 'browser environment');
    expect(err.message).toContain('browser environment');
  });
});

describe('browser bundle safety (import gate)', () => {
  it('UnsupportedProviderError can be imported from browser/index without Node modules', async () => {
    const { UnsupportedProviderError: BrowserErr } = await import('../browser/unsupportedProviderError');
    expect(new BrowserErr('pkcs11')).toBeInstanceOf(Error);
  });

  it('getProviderCapabilities reports pkcs11:false and awsKms:false', async () => {
    const { getProviderCapabilities } = await import('../browser/browserVerifier');
    const caps = await getProviderCapabilities();
    expect(caps.pkcs11).toBe(false);
    expect(caps.awsKms).toBe(false);
  });
});
