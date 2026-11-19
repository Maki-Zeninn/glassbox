// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import { KmsSigner } from '../src/audit/signing/kmsSigner';

describe('KMS Signer integration', () => {
  const testKeyId = 'arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-123456789012';

  beforeEach(() => {
    process.env.AWS_REGION = 'us-east-1';
    process.env.GLASSBOX_KMS_KEY_ID = testKeyId;
  });

  afterEach(() => {
    delete process.env.AWS_REGION;
    delete process.env.GLASSBOX_KMS_KEY_ID;
    delete process.env.GLASSBOX_KMS_SIGNING_ALGORITHM;
  });

  test('constructor validates required key ID', () => {
    delete process.env.GLASSBOX_KMS_KEY_ID;
    expect(() => new KmsSigner()).toThrow('GLASSBOX_KMS_KEY_ID is required');
  });

  test('constructor validates required region', () => {
    delete process.env.AWS_REGION;
    expect(() => new KmsSigner()).toThrow('AWS_REGION is required');
  });

  test('constructor accepts explicit options', () => {
    const signer = new KmsSigner({
      keyId: 'alias/test-key',
      region: 'eu-west-1',
    });
    expect(signer).toBeDefined();
  });

  test('uses configured region or defaults', () => {
    process.env.AWS_REGION = 'eu-west-1';
    const signer = new KmsSigner();
    expect(signer).toBeDefined();
  });
});
