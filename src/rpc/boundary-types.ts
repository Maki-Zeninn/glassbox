// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * boundary-types.ts
 *
 * Strict type-level and runtime boundary between raw RPC wire data and the
 * internal normalized models used throughout Glassbox.
 *
 * Design:
 *  - WireXxx types model exactly what the Soroban RPC server sends over the
 *    wire.  Fields are `unknown` or `string | undefined` where the server
 *    omits or unexpectedly changes shapes.
 *  - NormalizedXxx types are what the rest of the codebase consumes.  They
 *    are fully typed and never contain `unknown`.
 *  - isWireXxx type guards validate untrusted data before it is used.
 *  - normalizeXxx functions convert wire → normalized, rejecting malformed
 *    input with a typed BoundaryError.
 *
 * Callers MUST call a normalize function before treating RPC data as typed.
 */

// ─── Error type ───────────────────────────────────────────────────────────────

/**
 * BoundaryError is thrown when untrusted RPC data fails validation.
 * It carries the method name, the failing field, and the raw value so callers
 * can emit actionable diagnostics.
 */
export class BoundaryError extends Error {
    constructor(
        public readonly method: string,
        public readonly field: string,
        public readonly received: unknown,
        message?: string,
    ) {
        super(message ?? `RPC boundary violation in ${method}: field "${field}" failed validation (received: ${JSON.stringify(received)})`);
        this.name = 'BoundaryError';
    }
}

// ─── Protocol version union ───────────────────────────────────────────────────

/** Known Soroban RPC protocol versions. */
export type ProtocolVersion = '21' | '22' | '23' | string;

// ─── Wire model: JSON-RPC envelope ────────────────────────────────────────────

/**
 * WireRPCError is the raw error object as it arrives from the server.
 * code and message may be absent in malformed responses from misbehaving nodes.
 */
export interface WireRPCError {
    code?: unknown;
    message?: unknown;
    data?: unknown;
}

/**
 * WireRPCResponse is the raw JSON-RPC 2.0 envelope with completely untrusted
 * result and error fields.
 */
export interface WireRPCResponse {
    jsonrpc?: unknown;
    id?: unknown;
    result?: unknown;
    error?: unknown;
}

// ─── Wire model: individual method results ────────────────────────────────────

export interface WireHealthResult {
    status?: unknown;
    currentProtocolVersion?: unknown;
    currentLedgerVersion?: unknown;
    buildVersion?: unknown;
    networkPassphrase?: unknown;
}

export interface WireLatestLedgerResult {
    sequence?: unknown;
    hash?: unknown;
    closeTime?: unknown;
    protocolVersion?: unknown;
}

export interface WireTransactionResult {
    hash?: unknown;
    status?: unknown;
    ledger?: unknown;
    createdAt?: unknown;
    feeCharged?: unknown;
    successful?: unknown;
    resultXdr?: unknown;
    operationResults?: unknown;
}

export interface WireSimulateTransactionResult {
    results?: unknown;
    cost?: unknown;
    latestLedger?: unknown;
    latestLedgerCloseTime?: unknown;
    error?: unknown;
}

export interface WireSendTransactionResult {
    hash?: unknown;
    status?: unknown;
    ledger?: unknown;
    createdAt?: unknown;
    feeCharged?: unknown;
}

// ─── Normalized models ────────────────────────────────────────────────────────

/**
 * NormalizedRPCError is the internal representation of a JSON-RPC error.
 * Both fields are guaranteed to be present and typed.
 */
export interface NormalizedRPCError {
    code: number;
    message: string;
    data?: unknown;
}

/**
 * NormalizedHealthResult is what the rest of the application uses after wire
 * data has been validated.
 */
export interface NormalizedHealthResult {
    status: 'healthy' | 'unhealthy';
    protocolVersion?: string;
    ledgerVersion?: number;
    buildVersion?: string;
    networkPassphrase?: string;
}

export interface NormalizedLatestLedgerResult {
    sequence: number;
    hash: string;
    closeTime: number;
    protocolVersion?: string;
}

export interface NormalizedTransactionResult {
    hash: string;
    status: string;
    ledger?: number;
    createdAt?: string;
    feeCharged?: string;
    successful?: boolean;
    resultXdr?: string;
}

export interface NormalizedSimulateResult {
    results?: unknown[];
    cost?: { cpuInstructions: string; memoryBytes: string };
    latestLedger?: number;
    latestLedgerCloseTime?: number;
    simulationError?: string;
}

export interface NormalizedSendTransactionResult {
    hash: string;
    status: 'pending' | 'success' | 'failed' | string;
    ledger?: number;
    createdAt?: string;
    feeCharged?: string;
}

// ─── Type guards ──────────────────────────────────────────────────────────────

/** Returns true if v is a non-null object. */
function isObject(v: unknown): v is Record<string, unknown> {
    return typeof v === 'object' && v !== null;
}

/**
 * isWireRPCResponse returns true if v has the minimal shape of a JSON-RPC 2.0
 * response (has jsonrpc field).  It does NOT require result or error to be
 * present because some malformed nodes omit both.
 */
export function isWireRPCResponse(v: unknown): v is WireRPCResponse {
    return isObject(v) && 'jsonrpc' in v;
}

/**
 * isWireRPCError returns true if v looks like a JSON-RPC error object with at
 * least a numeric code and a string message.
 */
export function isWireRPCError(v: unknown): v is WireRPCError {
    if (!isObject(v)) return false;
    const { code, message } = v as WireRPCError;
    return typeof code === 'number' && typeof message === 'string';
}

// ─── Normalizers ──────────────────────────────────────────────────────────────

const MAX_STRING_BYTES = 256 * 1024; // 256 KiB — guard against oversized values

function requireString(method: string, field: string, v: unknown): string {
    if (typeof v !== 'string') {
        throw new BoundaryError(method, field, v, `expected string for ${field}`);
    }
    if (v.length > MAX_STRING_BYTES) {
        throw new BoundaryError(method, field, `[${v.length} chars]`, `field ${field} exceeds max length (${MAX_STRING_BYTES} chars)`);
    }
    return v;
}

function requireNumber(method: string, field: string, v: unknown): number {
    if (typeof v === 'number' && Number.isFinite(v)) return v;
    const parsed = typeof v === 'string' ? parseInt(v, 10) : NaN;
    if (!Number.isFinite(parsed)) {
        throw new BoundaryError(method, field, v, `expected number for ${field}`);
    }
    return parsed;
}

function optionalString(method: string, field: string, v: unknown): string | undefined {
    if (v === undefined || v === null) return undefined;
    return requireString(method, field, v);
}

function optionalNumber(method: string, field: string, v: unknown): number | undefined {
    if (v === undefined || v === null) return undefined;
    return requireNumber(method, field, v);
}

/**
 * normalizeRPCResponse validates the JSON-RPC 2.0 envelope and returns the
 * result payload.  Throws BoundaryError if the envelope is malformed.
 */
export function normalizeRPCResponse(method: string, wire: unknown): unknown {
    if (!isObject(wire)) {
        throw new BoundaryError(method, 'root', wire, 'RPC response must be an object');
    }

    const resp = wire as WireRPCResponse;

    if (resp.jsonrpc !== '2.0') {
        throw new BoundaryError(method, 'jsonrpc', resp.jsonrpc, 'expected "2.0"');
    }

    if ('error' in resp && resp.error !== undefined && resp.error !== null) {
        const err = resp.error as Record<string, unknown>;
        const code = typeof err.code === 'number' ? err.code : -1;
        const message = typeof err.message === 'string' ? err.message : String(err.message ?? 'unknown RPC error');
        const normalized: NormalizedRPCError = { code, message, data: err.data };
        throw Object.assign(new Error(`RPC error ${code}: ${message}`), {
            name: 'RPCResponseError',
            rpcError: normalized,
        });
    }

    if (!('result' in resp)) {
        throw new BoundaryError(method, 'result', undefined, 'response has neither result nor error');
    }

    return resp.result;
}

/**
 * normalizeHealthResult validates and normalises a getHealth wire result.
 */
export function normalizeHealthResult(wire: unknown): NormalizedHealthResult {
    const method = 'getHealth';
    const result = normalizeRPCResponse(method, wire) as WireHealthResult;

    if (!isObject(result)) {
        throw new BoundaryError(method, 'result', result, 'result must be an object');
    }

    const status = result.status;
    if (status !== 'healthy' && status !== 'unhealthy') {
        throw new BoundaryError(method, 'result.status', status, 'expected "healthy" or "unhealthy"');
    }

    return {
        status,
        protocolVersion: optionalString(method, 'currentProtocolVersion', result.currentProtocolVersion),
        ledgerVersion: optionalNumber(method, 'currentLedgerVersion', result.currentLedgerVersion),
        buildVersion: optionalString(method, 'buildVersion', result.buildVersion),
        networkPassphrase: optionalString(method, 'networkPassphrase', result.networkPassphrase),
    };
}

/**
 * normalizeLatestLedgerResult validates and normalises a getLatestLedger wire result.
 */
export function normalizeLatestLedgerResult(wire: unknown): NormalizedLatestLedgerResult {
    const method = 'getLatestLedger';
    const result = normalizeRPCResponse(method, wire) as WireLatestLedgerResult;

    if (!isObject(result)) {
        throw new BoundaryError(method, 'result', result, 'result must be an object');
    }

    return {
        sequence: requireNumber(method, 'result.sequence', result.sequence),
        hash: requireString(method, 'result.hash', result.hash),
        closeTime: requireNumber(method, 'result.closeTime', result.closeTime),
        protocolVersion: optionalString(method, 'result.protocolVersion', result.protocolVersion),
    };
}

/**
 * normalizeTransactionResult validates and normalises a getTransaction wire result.
 */
export function normalizeTransactionResult(wire: unknown): NormalizedTransactionResult {
    const method = 'getTransaction';
    const result = normalizeRPCResponse(method, wire) as WireTransactionResult;

    if (!isObject(result)) {
        throw new BoundaryError(method, 'result', result, 'result must be an object');
    }

    return {
        hash: requireString(method, 'result.hash', result.hash),
        status: requireString(method, 'result.status', result.status),
        ledger: optionalNumber(method, 'result.ledger', result.ledger),
        createdAt: optionalString(method, 'result.createdAt', result.createdAt),
        feeCharged: optionalString(method, 'result.feeCharged', result.feeCharged),
        successful: typeof result.successful === 'boolean' ? result.successful : undefined,
        resultXdr: optionalString(method, 'result.resultXdr', result.resultXdr),
    };
}

/**
 * normalizeSimulateTransactionResult validates and normalises a
 * simulateTransaction wire result.
 */
export function normalizeSimulateTransactionResult(wire: unknown): NormalizedSimulateResult {
    const method = 'simulateTransaction';
    const result = normalizeRPCResponse(method, wire) as WireSimulateTransactionResult;

    if (!isObject(result)) {
        throw new BoundaryError(method, 'result', result, 'result must be an object');
    }

    const out: NormalizedSimulateResult = {};

    if (Array.isArray(result.results)) {
        out.results = result.results;
    }

    if (isObject(result.cost)) {
        const cost = result.cost as Record<string, unknown>;
        out.cost = {
            cpuInstructions: requireString(method, 'result.cost.cpuInstructions', cost.cpuInstructions),
            memoryBytes: requireString(method, 'result.cost.memoryBytes', cost.memoryBytes),
        };
    }

    out.latestLedger = optionalNumber(method, 'result.latestLedger', result.latestLedger);
    out.latestLedgerCloseTime = optionalNumber(method, 'result.latestLedgerCloseTime', result.latestLedgerCloseTime);
    out.simulationError = optionalString(method, 'result.error', result.error);

    return out;
}

/**
 * normalizeSendTransactionResult validates and normalises a sendTransaction
 * wire result.
 */
export function normalizeSendTransactionResult(wire: unknown): NormalizedSendTransactionResult {
    const method = 'sendTransaction';
    const result = normalizeRPCResponse(method, wire) as WireSendTransactionResult;

    if (!isObject(result)) {
        throw new BoundaryError(method, 'result', result, 'result must be an object');
    }

    return {
        hash: requireString(method, 'result.hash', result.hash),
        status: requireString(method, 'result.status', result.status),
        ledger: optionalNumber(method, 'result.ledger', result.ledger),
        createdAt: optionalString(method, 'result.createdAt', result.createdAt),
        feeCharged: optionalString(method, 'result.feeCharged', result.feeCharged),
    };
}
