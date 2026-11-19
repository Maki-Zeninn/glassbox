// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import axios from 'axios';
import MockAdapter from 'axios-mock-adapter';
import {
    BoundaryError,
    normalizeRPCResponse,
    normalizeHealthResult,
    normalizeLatestLedgerResult,
    normalizeTransactionResult,
    normalizeSimulateTransactionResult,
    normalizeSendTransactionResult,
    isWireRPCResponse,
    isWireRPCError,
} from '../boundary-types';
import {
    normalizeAxiosError,
    isRetryableRPCError,
} from '../axios-error-normalizer';

// ─── Helpers ──────────────────────────────────────────────────────────────────

function ok<T>(result: T, method: string): { jsonrpc: '2.0'; id: 1; result: T } {
    return { jsonrpc: '2.0', id: 1, result };
}

function rpcError(code: number, message: string) {
    return { jsonrpc: '2.0', id: 1, error: { code, message } };
}

// ─── isWireRPCResponse type guard ─────────────────────────────────────────────

describe('isWireRPCResponse', () => {
    it('returns true for a valid JSON-RPC envelope', () => {
        expect(isWireRPCResponse({ jsonrpc: '2.0', id: 1, result: {} })).toBe(true);
    });

    it('returns false for non-objects', () => {
        expect(isWireRPCResponse(null)).toBe(false);
        expect(isWireRPCResponse('string')).toBe(false);
        expect(isWireRPCResponse(42)).toBe(false);
    });

    it('returns false when jsonrpc key is absent', () => {
        expect(isWireRPCResponse({ id: 1, result: {} })).toBe(false);
    });
});

// ─── isWireRPCError type guard ────────────────────────────────────────────────

describe('isWireRPCError', () => {
    it('returns true for a valid error object', () => {
        expect(isWireRPCError({ code: -32600, message: 'Invalid Request' })).toBe(true);
    });

    it('returns false when code is a string', () => {
        expect(isWireRPCError({ code: '-32600', message: 'err' })).toBe(false);
    });

    it('returns false when message is missing', () => {
        expect(isWireRPCError({ code: -32600 })).toBe(false);
    });
});

// ─── normalizeRPCResponse ─────────────────────────────────────────────────────

describe('normalizeRPCResponse', () => {
    it('extracts result from a valid envelope', () => {
        const wire = ok({ status: 'healthy' }, 'getHealth');
        expect(normalizeRPCResponse('getHealth', wire)).toEqual({ status: 'healthy' });
    });

    it('throws BoundaryError for non-object input', () => {
        expect(() => normalizeRPCResponse('getHealth', null)).toThrow(BoundaryError);
        expect(() => normalizeRPCResponse('getHealth', 'foo')).toThrow(BoundaryError);
    });

    it('throws BoundaryError when jsonrpc !== "2.0"', () => {
        const wire = { jsonrpc: '1.0', id: 1, result: {} };
        expect(() => normalizeRPCResponse('getHealth', wire)).toThrow(BoundaryError);
    });

    it('throws RPCResponseError when error field is present', () => {
        const wire = rpcError(-32601, 'Method not found');
        expect(() => normalizeRPCResponse('getHealth', wire)).toThrow(/RPC error -32601/);
    });

    it('throws BoundaryError when neither result nor error is present', () => {
        const wire = { jsonrpc: '2.0', id: 1 };
        expect(() => normalizeRPCResponse('getHealth', wire)).toThrow(BoundaryError);
    });

    it('accepts result: null as a valid (but unusual) response', () => {
        const wire = { jsonrpc: '2.0', id: 1, result: null };
        expect(normalizeRPCResponse('getHealth', wire)).toBeNull();
    });

    it('ignores unknown additional fields in the envelope (forward-compat)', () => {
        const wire = { jsonrpc: '2.0', id: 1, result: { status: 'healthy' }, futureField: 'ignored' };
        expect(() => normalizeRPCResponse('getHealth', wire)).not.toThrow();
    });
});

// ─── normalizeHealthResult ────────────────────────────────────────────────────

describe('normalizeHealthResult', () => {
    it('normalizes a healthy response', () => {
        const wire = ok({ status: 'healthy', currentProtocolVersion: '22', currentLedgerVersion: 48 }, 'getHealth');
        const result = normalizeHealthResult(wire);
        expect(result.status).toBe('healthy');
        expect(result.protocolVersion).toBe('22');
        expect(result.ledgerVersion).toBe(48);
    });

    it('normalizes an unhealthy response', () => {
        const wire = ok({ status: 'unhealthy' }, 'getHealth');
        expect(normalizeHealthResult(wire).status).toBe('unhealthy');
    });

    it('throws BoundaryError when status is missing', () => {
        const wire = ok({}, 'getHealth');
        expect(() => normalizeHealthResult(wire)).toThrow(BoundaryError);
    });

    it('throws BoundaryError for an invalid status value', () => {
        const wire = ok({ status: 'degraded' }, 'getHealth');
        expect(() => normalizeHealthResult(wire)).toThrow(BoundaryError);
    });

    it('accepts responses with extra unknown fields', () => {
        const wire = ok({ status: 'healthy', unknownFutureField: 'v2_feature' }, 'getHealth');
        const result = normalizeHealthResult(wire);
        expect(result.status).toBe('healthy');
    });
});

// ─── normalizeLatestLedgerResult ──────────────────────────────────────────────

describe('normalizeLatestLedgerResult', () => {
    it('normalizes a complete response', () => {
        const wire = ok({ sequence: 1234, hash: 'abc', closeTime: 1700000000 }, 'getLatestLedger');
        const result = normalizeLatestLedgerResult(wire);
        expect(result.sequence).toBe(1234);
        expect(result.hash).toBe('abc');
        expect(result.closeTime).toBe(1700000000);
    });

    it('throws BoundaryError when sequence is missing', () => {
        const wire = ok({ hash: 'abc', closeTime: 1700000000 }, 'getLatestLedger');
        expect(() => normalizeLatestLedgerResult(wire)).toThrow(BoundaryError);
    });

    it('throws BoundaryError when hash is missing', () => {
        const wire = ok({ sequence: 1, closeTime: 1700000000 }, 'getLatestLedger');
        expect(() => normalizeLatestLedgerResult(wire)).toThrow(BoundaryError);
    });

    it('throws BoundaryError when sequence is not a number', () => {
        const wire = ok({ sequence: 'not-a-number', hash: 'abc', closeTime: 1 }, 'getLatestLedger');
        expect(() => normalizeLatestLedgerResult(wire)).toThrow(BoundaryError);
    });
});

// ─── normalizeTransactionResult ───────────────────────────────────────────────

describe('normalizeTransactionResult', () => {
    it('normalizes a success response', () => {
        const wire = ok({ hash: 'abc123', status: 'SUCCESS', ledger: 100, successful: true }, 'getTransaction');
        const result = normalizeTransactionResult(wire);
        expect(result.hash).toBe('abc123');
        expect(result.status).toBe('SUCCESS');
        expect(result.ledger).toBe(100);
        expect(result.successful).toBe(true);
    });

    it('throws BoundaryError when hash is missing', () => {
        const wire = ok({ status: 'SUCCESS' }, 'getTransaction');
        expect(() => normalizeTransactionResult(wire)).toThrow(BoundaryError);
    });

    it('throws BoundaryError when status is missing', () => {
        const wire = ok({ hash: 'abc123' }, 'getTransaction');
        expect(() => normalizeTransactionResult(wire)).toThrow(BoundaryError);
    });

    it('handles an RPC error response', () => {
        const wire = rpcError(-32600, 'Transaction not found');
        expect(() => normalizeTransactionResult(wire)).toThrow(/RPC error/);
    });

    it('accepts response with extra unknown fields', () => {
        const wire = ok({ hash: 'abc', status: 'SUCCESS', unknownField: 42 }, 'getTransaction');
        const result = normalizeTransactionResult(wire);
        expect(result.hash).toBe('abc');
    });

    it('rejects oversized hash strings', () => {
        const oversized = 'a'.repeat(300 * 1024);
        const wire = ok({ hash: oversized, status: 'SUCCESS' }, 'getTransaction');
        expect(() => normalizeTransactionResult(wire)).toThrow(BoundaryError);
    });
});

// ─── normalizeSimulateTransactionResult ───────────────────────────────────────

describe('normalizeSimulateTransactionResult', () => {
    it('normalizes a full response', () => {
        const wire = ok({
            results: [{ footprint: 'abc' }],
            cost: { cpuInstructions: '1000', memoryBytes: '2048' },
            latestLedger: 200,
        }, 'simulateTransaction');
        const result = normalizeSimulateTransactionResult(wire);
        expect(result.results).toHaveLength(1);
        expect(result.cost?.cpuInstructions).toBe('1000');
        expect(result.latestLedger).toBe(200);
    });

    it('accepts response with no results array (optional field)', () => {
        const wire = ok({ latestLedger: 200 }, 'simulateTransaction');
        const result = normalizeSimulateTransactionResult(wire);
        expect(result.results).toBeUndefined();
    });

    it('propagates simulation error string', () => {
        const wire = ok({ error: 'HostError: Wasm trap', latestLedger: 200 }, 'simulateTransaction');
        const result = normalizeSimulateTransactionResult(wire);
        expect(result.simulationError).toBe('HostError: Wasm trap');
    });
});

// ─── normalizeSendTransactionResult ──────────────────────────────────────────

describe('normalizeSendTransactionResult', () => {
    it('normalizes a pending response', () => {
        const wire = ok({ hash: 'txhash', status: 'pending' }, 'sendTransaction');
        const result = normalizeSendTransactionResult(wire);
        expect(result.hash).toBe('txhash');
        expect(result.status).toBe('pending');
    });

    it('throws BoundaryError when hash is missing', () => {
        const wire = ok({ status: 'pending' }, 'sendTransaction');
        expect(() => normalizeSendTransactionResult(wire)).toThrow(BoundaryError);
    });

    it('throws BoundaryError when status is missing', () => {
        const wire = ok({ hash: 'txhash' }, 'sendTransaction');
        expect(() => normalizeSendTransactionResult(wire)).toThrow(BoundaryError);
    });
});

// ─── normalizeAxiosError ──────────────────────────────────────────────────────

describe('normalizeAxiosError', () => {
    let mock: MockAdapter;

    beforeEach(() => { mock = new MockAdapter(axios as any); });
    afterEach(() => { mock.restore(); });

    it('returns NetworkError for ECONNREFUSED', async () => {
        mock.onPost('http://test/rpc').networkError();
        try {
            await axios.post('http://test/rpc', {});
        } catch (err) {
            const normalized = normalizeAxiosError(err);
            expect(normalized.kind).toBe('network');
        }
    });

    it('returns HTTPError for 500 responses', async () => {
        mock.onPost('http://test/rpc').reply(500, { message: 'Internal Server Error' });
        try {
            await axios.post('http://test/rpc', {});
        } catch (err) {
            const normalized = normalizeAxiosError(err);
            expect(normalized.kind).toBe('http');
            if (normalized.kind === 'http') {
                expect(normalized.statusCode).toBe(500);
            }
        }
    });

    it('returns HTTPError for 404 responses', async () => {
        mock.onPost('http://test/rpc').reply(404);
        try {
            await axios.post('http://test/rpc', {});
        } catch (err) {
            const normalized = normalizeAxiosError(err);
            expect(normalized.kind).toBe('http');
        }
    });

    it('returns UnknownRPCError for plain Error objects', () => {
        const normalized = normalizeAxiosError(new Error('plain error'));
        expect(normalized.kind).toBe('unknown');
        expect(normalized.message).toContain('plain error');
    });

    it('returns UnknownRPCError for non-Error values', () => {
        const normalized = normalizeAxiosError('a string error');
        expect(normalized.kind).toBe('unknown');
    });
});

// ─── isRetryableRPCError ──────────────────────────────────────────────────────

describe('isRetryableRPCError', () => {
    it('network errors are retryable', () => {
        const err = { kind: 'network' as const, message: '', cause: null };
        expect(isRetryableRPCError(err)).toBe(true);
    });

    it('HTTP 429 is retryable', () => {
        const err = { kind: 'http' as const, statusCode: 429, message: '', cause: null };
        expect(isRetryableRPCError(err)).toBe(true);
    });

    it('HTTP 500+ is retryable', () => {
        const err = { kind: 'http' as const, statusCode: 503, message: '', cause: null };
        expect(isRetryableRPCError(err)).toBe(true);
    });

    it('HTTP 400 is not retryable', () => {
        const err = { kind: 'http' as const, statusCode: 400, message: '', cause: null };
        expect(isRetryableRPCError(err)).toBe(false);
    });

    it('HTTP 404 is not retryable', () => {
        const err = { kind: 'http' as const, statusCode: 404, message: '', cause: null };
        expect(isRetryableRPCError(err)).toBe(false);
    });

    it('rpc_protocol errors are not retryable', () => {
        const err = { kind: 'rpc_protocol' as const, message: '', cause: null };
        expect(isRetryableRPCError(err)).toBe(false);
    });

    it('unknown errors are not retryable', () => {
        const err = { kind: 'unknown' as const, message: '', cause: null };
        expect(isRetryableRPCError(err)).toBe(false);
    });
});

// ─── BoundaryError ────────────────────────────────────────────────────────────

describe('BoundaryError', () => {
    it('includes method, field, and received in message', () => {
        const err = new BoundaryError('getHealth', 'result.status', 'degraded');
        expect(err.message).toContain('getHealth');
        expect(err.message).toContain('result.status');
        expect(err.message).toContain('degraded');
        expect(err.name).toBe('BoundaryError');
    });

    it('accepts a custom message override', () => {
        const err = new BoundaryError('getHealth', 'status', null, 'custom message');
        expect(err.message).toBe('custom message');
    });
});
