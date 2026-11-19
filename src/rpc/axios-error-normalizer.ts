// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * axios-error-normalizer.ts
 *
 * Normalizes Axios errors into a stable, typed error union so the rest of the
 * codebase never inspects raw AxiosError internals.
 *
 * Error union:
 *  - NetworkError      – no response (ECONNREFUSED, ETIMEDOUT, etc.)
 *  - HTTPError         – server replied with a 4xx/5xx status
 *  - RPCProtocolError  – server replied 200 but the JSON-RPC payload is invalid
 *  - UnknownRPCError   – anything else
 */

import axios, { AxiosError } from 'axios';

/** Stable error category discriminants. */
export type RPCErrorKind = 'network' | 'http' | 'rpc_protocol' | 'unknown';

/** Base interface shared by all normalized RPC errors. */
export interface NormalizedRPCErrorBase {
    kind: RPCErrorKind;
    /** Human-readable description, safe to display in diagnostics. */
    message: string;
    /** The original error, kept for logging or re-throw if needed. */
    cause: unknown;
}

/** No HTTP response was received (network-level failure). */
export interface NetworkError extends NormalizedRPCErrorBase {
    kind: 'network';
    /** OS-level error code, e.g. 'ECONNREFUSED'. */
    code?: string;
}

/** Server responded with a non-2xx status. */
export interface HTTPError extends NormalizedRPCErrorBase {
    kind: 'http';
    statusCode: number;
    /** Body of the error response, if any. */
    responseBody?: unknown;
}

/** Server responded with 2xx but the JSON-RPC envelope is malformed. */
export interface RPCProtocolError extends NormalizedRPCErrorBase {
    kind: 'rpc_protocol';
    /** Raw response data that failed validation. */
    raw?: unknown;
}

/** Catch-all for unexpected error shapes. */
export interface UnknownRPCError extends NormalizedRPCErrorBase {
    kind: 'unknown';
}

export type NormalizedRPCError =
    | NetworkError
    | HTTPError
    | RPCProtocolError
    | UnknownRPCError;

/**
 * normalizeAxiosError converts any thrown value into a NormalizedRPCError.
 *
 * Usage:
 * ```ts
 * try {
 *   await axiosClient.post(url, data);
 * } catch (err) {
 *   const normalized = normalizeAxiosError(err);
 *   // Switch on normalized.kind for typed handling.
 * }
 * ```
 */
export function normalizeAxiosError(err: unknown): NormalizedRPCError {
    if (!axios.isAxiosError(err)) {
        return {
            kind: 'unknown',
            message: err instanceof Error ? err.message : String(err),
            cause: err,
        };
    }

    const axiosErr = err as AxiosError;

    // No response: network-level failure.
    if (!axiosErr.response) {
        return {
            kind: 'network',
            message: `Network error: ${axiosErr.message}`,
            code: axiosErr.code,
            cause: axiosErr,
        };
    }

    const status = axiosErr.response.status;
    const body = axiosErr.response.data;

    return {
        kind: 'http',
        statusCode: status,
        responseBody: body,
        message: `HTTP ${status}: ${axiosErr.message}`,
        cause: axiosErr,
    };
}

/**
 * isRetryableRPCError returns true for error kinds that are safe to retry.
 * HTTP 429 (rate limit) and 5xx (server errors) are retryable.
 * 4xx (except 429) are not — retrying would not change the outcome.
 */
export function isRetryableRPCError(err: NormalizedRPCError): boolean {
    switch (err.kind) {
        case 'network':
            return true;
        case 'http':
            return err.statusCode === 429 || err.statusCode >= 500;
        case 'rpc_protocol':
            return false;
        case 'unknown':
            return false;
    }
}
