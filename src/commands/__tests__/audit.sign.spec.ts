// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import { Command } from 'commander';
import { registerAuditCommands } from '../audit';
import { createAuditSigner } from '../../audit/signing/factory';
import { AuditLogger } from '../../audit/AuditLogger';
import { ExitCode } from '../../exit-codes';

jest.mock('../../audit/signing/factory', () => ({
  createAuditSigner: jest.fn(),
}));

jest.mock('../../audit/AuditLogger', () => ({
  AuditLogger: jest.fn().mockImplementation(() => ({
    generateLog: jest.fn().mockResolvedValue({ ok: true }),
  })),
}));

const VALID_PAYLOAD = "{\"input\":{},\"state\":{},\"events\":[],\"timestamp\":\"2026-01-01T00:00:00.000Z\"}";
const RICH_PAYLOAD = "{\"input\":{\"op\":\"transfer\"},\"state\":{\"balance\":100},\"events\":[\"INIT\"],\"timestamp\":\"2026-01-01T00:00:00.000Z\"}";

describe('audit:sign --dry-run', () => {
  let program: Command;
  beforeEach(() => { program = new Command(); registerAuditCommands(program); jest.clearAllMocks(); });

  test('validates payload and connectivity without invoking signing logger', async () => {
    (createAuditSigner as jest.Mock).mockReturnValue({ sign: jest.fn(), public_key: jest.fn().mockResolvedValue('pem'), attestation_chain: jest.fn().mockResolvedValue(undefined) });
    const stdoutSpy = jest.spyOn(process.stdout, 'write').mockImplementation(() => true);
    await program.parseAsync(['node','test','audit:sign','--payload',VALID_PAYLOAD,'--hsm-provider','pkcs11','--dry-run']);
    expect(createAuditSigner).toHaveBeenCalledTimes(1);
    expect(AuditLogger).not.toHaveBeenCalled();
    expect(stdoutSpy).toHaveBeenCalledWith(expect.stringContaining('"dry_run": true'));
    expect(stdoutSpy).toHaveBeenCalledWith(expect.stringContaining('"signer_provider": "pkcs11"'));
    stdoutSpy.mockRestore();
  });

  test('returns failure for invalid payload json', async () => {
    const consoleErrorSpy = jest.spyOn(console, 'error').mockImplementation();
    const processExitSpy = jest.spyOn(process, 'exit').mockImplementation((() => undefined) as any);
    await program.parseAsync(['node','test','audit:sign','--payload','{not-json}','--dry-run']);
    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('[FAIL] audit signing failed'));
    expect(processExitSpy).toHaveBeenCalledWith(ExitCode.VALIDATION_ERROR);
    consoleErrorSpy.mockRestore(); processExitSpy.mockRestore();
  });

  test('still performs normal signing flow when dry-run is not set', async () => {
    (createAuditSigner as jest.Mock).mockReturnValue({ sign: jest.fn(), public_key: jest.fn().mockResolvedValue('pem'), attestation_chain: jest.fn().mockResolvedValue(undefined) });
    const stdoutSpy = jest.spyOn(process.stdout, 'write').mockImplementation(() => true);
    await program.parseAsync(['node','test','audit:sign','--payload',VALID_PAYLOAD]);
    expect(AuditLogger).toHaveBeenCalledTimes(1);
    stdoutSpy.mockRestore();
  });
});

describe('audit:sign improved validation', () => {
  let program: Command;
  beforeEach(() => { program = new Command(); registerAuditCommands(program); jest.clearAllMocks(); });

  test('actionable error when payload is not valid JSON', async () => {
    const consoleErrorSpy = jest.spyOn(console, 'error').mockImplementation();
    const processExitSpy = jest.spyOn(process, 'exit').mockImplementation((() => undefined) as any);
    await program.parseAsync(['node','test','audit:sign','--payload','not-json-at-all']);
    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('not valid JSON'));
    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('--payload'));
    expect(processExitSpy).toHaveBeenCalledWith(ExitCode.VALIDATION_ERROR);
    consoleErrorSpy.mockRestore(); processExitSpy.mockRestore();
  });

  test('schema validation errors show remediation hint', async () => {
    (createAuditSigner as jest.Mock).mockReturnValue({ sign: jest.fn(), public_key: jest.fn().mockResolvedValue('pem'), attestation_chain: jest.fn().mockResolvedValue(undefined) });
    (AuditLogger as jest.Mock).mockImplementationOnce(() => ({ generateLog: jest.fn().mockRejectedValue(new Error('Audit payload schema validation failed')) }));
    const consoleErrorSpy = jest.spyOn(console, 'error').mockImplementation();
    const processExitSpy = jest.spyOn(process, 'exit').mockImplementation((() => undefined) as any);
    await program.parseAsync(['node','test','audit:sign','--payload',VALID_PAYLOAD]);
    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('[FAIL]'));
    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('Fix the payload'));
    expect(processExitSpy).toHaveBeenCalledWith(ExitCode.VALIDATION_ERROR);
    consoleErrorSpy.mockRestore(); processExitSpy.mockRestore();
  });

  test('private key error shows GLASSBOX_AUDIT_PRIVATE_KEY_PEM guidance', async () => {
    (createAuditSigner as jest.Mock).mockReturnValue({ sign: jest.fn(), public_key: jest.fn().mockResolvedValue('pem'), attestation_chain: jest.fn().mockResolvedValue(undefined) });
    (AuditLogger as jest.Mock).mockImplementationOnce(() => ({ generateLog: jest.fn().mockRejectedValue(new Error('private key not configured: GLASSBOX_AUDIT_PRIVATE_KEY_PEM')) }));
    const consoleErrorSpy = jest.spyOn(console, 'error').mockImplementation();
    const processExitSpy = jest.spyOn(process, 'exit').mockImplementation((() => undefined) as any);
    await program.parseAsync(['node','test','audit:sign','--payload',VALID_PAYLOAD]);
    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('GLASSBOX_AUDIT_PRIVATE_KEY_PEM'));
    expect(processExitSpy).toHaveBeenCalledWith(ExitCode.CONFIGURATION_ERROR);
    consoleErrorSpy.mockRestore(); processExitSpy.mockRestore();
  });

  test('dry-run outputs valid canonical hash', async () => {
    (createAuditSigner as jest.Mock).mockReturnValue({ sign: jest.fn(), public_key: jest.fn().mockResolvedValue('pem'), attestation_chain: jest.fn().mockResolvedValue(undefined) });
    const stdoutSpy = jest.spyOn(process.stdout, 'write').mockImplementation(() => true);
    await program.parseAsync(['node','test','audit:sign','--payload',RICH_PAYLOAD,'--dry-run']);
    const output = stdoutSpy.mock.calls[0][0] as string;
    const parsed = JSON.parse(output);
    expect(parsed.dry_run).toBe(true);
    expect(parsed.canonical_hash).toMatch(/^[0-9a-f]{64}$/);
    stdoutSpy.mockRestore();
  });

  test('empty payload string gives actionable error', async () => {
    const consoleErrorSpy = jest.spyOn(console, 'error').mockImplementation();
    const processExitSpy = jest.spyOn(process, 'exit').mockImplementation((() => undefined) as any);
    await program.parseAsync(['node','test','audit:sign','--payload','']);
    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('not valid JSON'));
    expect(processExitSpy).toHaveBeenCalledWith(ExitCode.VALIDATION_ERROR);
    consoleErrorSpy.mockRestore(); processExitSpy.mockRestore();
  });
});