// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { URIParser, SignedURIPayload } from '../uri-parser';

const VALID_TX = 'a1b2c3d4e5f67890123456789abcdef0123456789abcdef0123456789abcdeff';
const SECRET = 'test-signing-secret';
const FUTURE_EXP = Math.floor(Date.now() / 1000) + 3600; // 1 hour from now
const PAST_EXP = Math.floor(Date.now() / 1000) - 60;    // 1 minute ago

function buildPayload(overrides: Partial<SignedURIPayload> = {}): SignedURIPayload {
    return {
        txHash: VALID_TX,
        network: 'testnet',
        exp: FUTURE_EXP,
        iss: 'dashboard.example.com',
        scope: ['debug'],
        ...overrides,
    };
}

describe('URIParser – signed payload', () => {
    let parser: URIParser;

    beforeEach(() => {
        parser = new URIParser();
    });

    describe('generateSignedPayload', () => {
        it('produces a base64url-encoded payload and an HMAC-SHA256 signature', () => {
            const { encoded, signature } = parser.generateSignedPayload(buildPayload(), SECRET);
            expect(encoded).toMatch(/^[A-Za-z0-9_-]+$/); // base64url, no padding
            expect(signature).toMatch(/^[0-9a-f]{64}$/);  // 64-char hex
        });

        it('round-trips through parseSignedPayload', () => {
            const original = buildPayload();
            const { encoded } = parser.generateSignedPayload(original, SECRET);
            const decoded = parser.parseSignedPayload(encoded);
            expect(decoded).toEqual(original);
        });
    });

    describe('parseSignedPayload', () => {
        it('decodes a valid base64url payload', () => {
            const payload = buildPayload();
            const { encoded } = parser.generateSignedPayload(payload, SECRET);
            const result = parser.parseSignedPayload(encoded);
            expect(result.txHash).toBe(payload.txHash);
            expect(result.network).toBe('testnet');
            expect(result.iss).toBe('dashboard.example.com');
            expect(result.scope).toEqual(['debug']);
        });

        it('throws on non-base64url input', () => {
            expect(() => parser.parseSignedPayload('not valid!!!')).toThrow('JSON parsing failed');
        });

        it('throws when txHash is missing', () => {
            const bad = Buffer.from(JSON.stringify({ network: 'testnet', exp: FUTURE_EXP, iss: 'x', scope: [] }))
                .toString('base64').replace(/=/g, '');
            expect(() => parser.parseSignedPayload(bad)).toThrow('missing txHash');
        });

        it('throws when network is invalid', () => {
            const bad = Buffer.from(JSON.stringify({ txHash: VALID_TX, network: 'devnet', exp: FUTURE_EXP, iss: 'x', scope: [] }))
                .toString('base64').replace(/=/g, '');
            expect(() => parser.parseSignedPayload(bad)).toThrow('network must be testnet or mainnet');
        });

        it('throws when exp is missing', () => {
            const bad = Buffer.from(JSON.stringify({ txHash: VALID_TX, network: 'testnet', iss: 'x', scope: [] }))
                .toString('base64').replace(/=/g, '');
            expect(() => parser.parseSignedPayload(bad)).toThrow('exp must be a finite number');
        });

        it('throws when iss is empty', () => {
            const bad = Buffer.from(JSON.stringify({ txHash: VALID_TX, network: 'testnet', exp: FUTURE_EXP, iss: '', scope: [] }))
                .toString('base64').replace(/=/g, '');
            expect(() => parser.parseSignedPayload(bad)).toThrow('iss must be a non-empty string');
        });
    });

    describe('validateSignedPayloadSignature', () => {
        it('returns true for a valid signature', () => {
            const payload = buildPayload();
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            expect(parser.validateSignedPayloadSignature(encoded, signature, SECRET)).toBe(true);
        });

        it('returns false when signature is tampered', () => {
            const payload = buildPayload();
            const { encoded } = parser.generateSignedPayload(payload, SECRET);
            const badSig = 'c0ffee'.repeat(8) + '0000000000000000'; // 64 hex chars, wrong value
            expect(parser.validateSignedPayloadSignature(encoded, badSig, SECRET)).toBe(false);
        });

        it('returns false when the payload is tampered (encoding changed)', () => {
            const payload = buildPayload();
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            const tamperedEncoded = encoded.slice(0, -4) + 'AAAA';
            expect(parser.validateSignedPayloadSignature(tamperedEncoded, signature, SECRET)).toBe(false);
        });

        it('returns false when secret differs', () => {
            const payload = buildPayload();
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            expect(parser.validateSignedPayloadSignature(encoded, signature, 'wrong-secret')).toBe(false);
        });

        it('returns false for a malformed hex signature', () => {
            const payload = buildPayload();
            const { encoded } = parser.generateSignedPayload(payload, SECRET);
            expect(parser.validateSignedPayloadSignature(encoded, 'not-hex', SECRET)).toBe(false);
        });
    });

    describe('URI parse includes signed payload', () => {
        function buildSignedURI(payloadOverrides: Partial<SignedURIPayload> = {}): string {
            const payload = buildPayload(payloadOverrides);
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            return `glassbox://debug/${VALID_TX}?network=testnet&signed-payload=${encoded}&signature=${signature}`;
        }

        it('populates signedPayload and signedPayloadEncoded on the ParsedURI', () => {
            const uri = buildSignedURI();
            const result = parser.parse(uri);
            expect(result.signedPayload).toBeDefined();
            expect(result.signedPayloadEncoded).toBeDefined();
            expect(result.signedPayload!.txHash).toBe(VALID_TX);
        });

        it('leaves signedPayload undefined for unsigned URIs', () => {
            const uri = `glassbox://debug/${VALID_TX}?network=testnet`;
            const result = parser.parse(uri);
            expect(result.signedPayload).toBeUndefined();
            expect(result.signedPayloadEncoded).toBeUndefined();
        });

        it('throws when the signed-payload JSON is structurally invalid', () => {
            const uri = `glassbox://debug/${VALID_TX}?network=testnet&signed-payload=AAAA`;
            expect(() => parser.parse(uri)).toThrow();
        });
    });

    describe('ProtocolHandler signed-payload security checks', () => {
        const { ProtocolHandler } = require('../handler');

        it('accepts a valid signed URI with correct signature and future expiry', async () => {
            const payload = buildPayload();
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            const uri = `glassbox://debug/${VALID_TX}?network=testnet&signed-payload=${encoded}&signature=${signature}`;
            const handler = new ProtocolHandler({ secret: SECRET });
            const parsed = parser.parse(uri);
            // securityChecks is private; exercise via handle() with a stubbed session
            await expect((handler as any).securityChecks(parsed)).resolves.toBeUndefined();
        });

        it('rejects an expired signed payload', async () => {
            const payload = buildPayload({ exp: PAST_EXP });
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            const uri = `glassbox://debug/${VALID_TX}?network=testnet&signed-payload=${encoded}&signature=${signature}`;
            const handler = new ProtocolHandler({ secret: SECRET });
            const parsed = parser.parse(uri);
            await expect((handler as any).securityChecks(parsed)).rejects.toThrow('expired');
        });

        it('rejects a tampered payload (wrong txHash in payload vs URI)', async () => {
            const differentTx = 'b'.repeat(64);
            const payload = buildPayload({ txHash: differentTx });
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            // URI uses VALID_TX, but payload has differentTx
            const uri = `glassbox://debug/${VALID_TX}?network=testnet&signed-payload=${encoded}&signature=${signature}`;
            const handler = new ProtocolHandler({ secret: SECRET });
            const parsed = parser.parse(uri);
            // Signature verifies against the encoded payload, but parameter mismatch should fail
            await expect((handler as any).securityChecks(parsed)).rejects.toThrow('do not match URI');
        });

        it('rejects an unauthorized issuer', async () => {
            const payload = buildPayload({ iss: 'evil.com' });
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            const uri = `glassbox://debug/${VALID_TX}?network=testnet&signed-payload=${encoded}&signature=${signature}`;
            const handler = new ProtocolHandler({ secret: SECRET, allowedIssuers: ['trusted.example.com'] });
            const parsed = parser.parse(uri);
            await expect((handler as any).securityChecks(parsed)).rejects.toThrow("Unauthorized issuer 'evil.com'");
        });

        it('rejects when scope does not include a required scope', async () => {
            const payload = buildPayload({ scope: ['audit'] });
            const { encoded, signature } = parser.generateSignedPayload(payload, SECRET);
            const uri = `glassbox://debug/${VALID_TX}?network=testnet&signed-payload=${encoded}&signature=${signature}`;
            const handler = new ProtocolHandler({ secret: SECRET, requiredScope: ['debug'] });
            const parsed = parser.parse(uri);
            await expect((handler as any).securityChecks(parsed)).rejects.toThrow('Scope');
        });

        it('rejects when requireSignedPayload is set but no signed-payload is present', async () => {
            const uri = `glassbox://debug/${VALID_TX}?network=testnet`;
            const handler = new ProtocolHandler({ secret: SECRET, requireSignedPayload: true });
            const parsed = parser.parse(uri);
            await expect((handler as any).securityChecks(parsed)).rejects.toThrow('signed payload is required');
        });

        it('allows unsigned URIs when requireSignedPayload is false (backward compatibility)', async () => {
            const uri = `glassbox://debug/${VALID_TX}?network=testnet`;
            const handler = new ProtocolHandler({ secret: SECRET });
            const parsed = parser.parse(uri);
            await expect((handler as any).securityChecks(parsed)).resolves.toBeUndefined();
        });
    });
});
