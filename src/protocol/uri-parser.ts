// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import * as crypto from 'crypto';
import { URL } from 'url';

/**
 * Signed URI payload carried in the `signed-payload` query parameter.
 * The payload is base64url-encoded JSON; its HMAC-SHA256 signature is in
 * the `signature` query parameter.
 *
 * Fields:
 *   txHash    - must match the URI path
 *   network   - must match the `network` query parameter
 *   exp       - Unix timestamp (seconds) after which this link is invalid
 *   iss       - Issuer identifier (e.g. dashboard hostname)
 *   scope     - Allowed operations (e.g. ["debug"])
 *   operation - Optional operation index, mirrors the URI param
 */
export interface SignedURIPayload {
    txHash: string;
    network: 'testnet' | 'mainnet';
    exp: number;
    iss: string;
    scope: string[];
    operation?: number;
}

export interface ParsedURI {
    transactionHash: string;
    network: 'testnet' | 'mainnet';
    operation?: number;
    source?: string;
    signature?: string;
    raw: string;
    mockLedgerManifest?: string;
    mockLedgerEntries?: string[];
    protocolVersion?: number;
    /** Decoded signed payload, present when `signed-payload` query param exists. */
    signedPayload?: SignedURIPayload;
    /** Raw base64url-encoded payload string, needed to re-verify the signature. */
    signedPayloadEncoded?: string;
}

/**
 * URIParser handles parsing, validation, and signature verification
 * for the glassbox:// protocol.
 */
export class URIParser {
    private readonly protocol = 'glassbox';
    private readonly allowedNetworks = ['testnet', 'mainnet'];

    /**
     * Parse and validate GLASSBOX Protocol URI
     */
    parse(uriString: string): ParsedURI {
        // Basic validation
        if (!uriString || typeof uriString !== 'string') {
            throw new Error('Invalid URI: must be a non-empty string');
        }

        // Remove any leading/trailing whitespace
        uriString = uriString.trim();

        // Check protocol
        if (!uriString.startsWith(`${this.protocol}://`)) {
            throw new Error(`Invalid protocol: expected ${this.protocol}://`);
        }

        try {
            // Parse URI
            const url = new URL(uriString);

            // Extract host (debug) and path (transaction hash)
            // URI format: glassbox://debug/<transaction_hash>
            if (url.host !== 'debug') {
                throw new Error('Invalid URI host: expected debug');
            }

            const pathParts = url.pathname.split('/').filter(Boolean);

            if (pathParts.length === 0) {
                throw new Error('Missing transaction hash in URI');
            }

            const transactionHash = decodeURIComponent(pathParts[0]);

            // Validate transaction hash format (64 hex characters)
            if (!/^[a-f0-9]{64}$/i.test(transactionHash)) {
                throw new Error('Invalid transaction hash format');
            }

            // Extract query parameters
            const params = url.searchParams;

            // Network (required)
            const network = params.get('network');
            if (!network) {
                throw new Error('Missing required parameter: network');
            }

            if (!this.allowedNetworks.includes(network)) {
                throw new Error(`Invalid network: must be one of ${this.allowedNetworks.join(', ')}`);
            }

            // Operation (optional)
            const operation = params.get('operation');
            const operationIndex = operation ? parseInt(operation, 10) : undefined;

            if (operationIndex !== undefined && (isNaN(operationIndex) || operationIndex < 0)) {
                throw new Error('Invalid operation index: must be a non-negative integer');
            }

            // Source (optional)
            const source = params.get('source') || undefined;

            // Signature (optional)
            const signature = params.get('signature') || undefined;

            // Protocol Version (optional)
            const protoVersionStr = params.get('protocol-version');
            let protocolVersion: number | undefined;
            if (protoVersionStr !== null) {
                protocolVersion = parseInt(protoVersionStr, 10);
                if (isNaN(protocolVersion) || protocolVersion <= 0) {
                    throw new Error('Invalid protocol-version: must be a positive integer');
                }
                const allowedProtos = [20, 21, 22];
                if (!allowedProtos.includes(protocolVersion)) {
                    throw new Error(`Invalid protocol-version: unsupported version ${protocolVersion}. Supported: ${allowedProtos.join(', ')}`);
                }
            }

            // Mock Ledger Manifest (optional)
            const mockManifest = params.get('mock-ledger-manifest') || undefined;
            if (mockManifest && mockManifest.includes('\0')) {
                throw new Error('Invalid mock-ledger-manifest: cannot contain null bytes');
            }

            // Mock Ledger Entry (optional, repeatable)
            const mockEntries = params.getAll('mock-ledger-entry');
            const validatedEntries: string[] = [];
            for (const entry of mockEntries) {
                const trimmed = entry.trim();
                if (!trimmed) continue;
                const parts = trimmed.split(':');
                if (parts.length < 2 || !parts[0]) {
                    throw new Error(`Invalid mock-ledger-entry format: expected key:value (got '${trimmed}')`);
                }
                const val = parts.slice(1).join(':');
                if (!val) {
                    throw new Error(`Invalid mock-ledger-entry value: value cannot be empty (got '${trimmed}')`);
                }
                // Validate base64 value
                if (!/^[a-zA-Z0-9+/]*={0,2}$/.test(val) || val.length % 4 !== 0) {
                    throw new Error(`Invalid mock-ledger-entry value: must be valid base64 (got '${trimmed}')`);
                }
                validatedEntries.push(trimmed);
            }

            // Signed payload (optional, tamper-evident extension)
            let signedPayload: SignedURIPayload | undefined;
            let signedPayloadEncoded: string | undefined;
            const rawSignedPayload = params.get('signed-payload');
            if (rawSignedPayload) {
                signedPayloadEncoded = rawSignedPayload;
                signedPayload = this.parseSignedPayload(rawSignedPayload);
            }

            return {
                transactionHash,
                network: network as 'testnet' | 'mainnet',
                operation: operationIndex,
                source,
                signature,
                raw: uriString,
                protocolVersion,
                mockLedgerManifest: mockManifest,
                mockLedgerEntries: validatedEntries.length > 0 ? validatedEntries : undefined,
                signedPayload,
                signedPayloadEncoded,
            };
        } catch (error) {
            if (error instanceof Error) {
                // Re-throw the original error to preserve the message for tests
                throw error;
            }
            throw new Error('Failed to parse URI: Unknown error');
        }
    }

    /**
     * Validate URI signature using HMAC-SHA256
     */
    validateSignature(parsed: ParsedURI, secret: string): boolean {
        if (!parsed.signature) {
            return false;
        }

        // Reconstruct the data that was signed
        // We use a structured string format for consistency
        const data = `${parsed.transactionHash}:${parsed.network}:${parsed.operation || ''}:${parsed.source || ''}`;

        // Compute HMAC
        const expectedSignature = crypto
            .createHmac('sha256', secret)
            .update(data)
            .digest('hex');

        // Constant-time comparison to prevent timing attacks
        try {
            return crypto.timingSafeEqual(
                Buffer.from(parsed.signature, 'hex'),
                Buffer.from(expectedSignature, 'hex'),
            );
        } catch {
            return false;
        }
    }

    /**
     * Generate signature for URI (primarily for testing and internal use)
     */
    generateSignature(parsed: Omit<ParsedURI, 'signature' | 'raw'>, secret: string): string {
        const data = `${parsed.transactionHash}:${parsed.network}:${parsed.operation || ''}:${parsed.source || ''}`;

        return crypto
            .createHmac('sha256', secret)
            .update(data)
            .digest('hex');
    }

    /**
     * Decode and parse a base64url-encoded SignedURIPayload.
     * Throws if the encoding or JSON structure is invalid.
     */
    parseSignedPayload(encoded: string): SignedURIPayload {
        let json: string;
        try {
            const padded = encoded + '='.repeat((4 - encoded.length % 4) % 4);
            json = Buffer.from(padded.replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString('utf8');
        } catch {
            throw new Error('Invalid signed-payload: base64url decoding failed');
        }

        let payload: unknown;
        try {
            payload = JSON.parse(json);
        } catch {
            throw new Error('Invalid signed-payload: JSON parsing failed');
        }

        if (typeof payload !== 'object' || payload === null) {
            throw new Error('Invalid signed-payload: must be a JSON object');
        }
        const p = payload as Record<string, unknown>;

        if (typeof p.txHash !== 'string') throw new Error('Invalid signed-payload: missing txHash');
        if (p.network !== 'testnet' && p.network !== 'mainnet') {
            throw new Error('Invalid signed-payload: network must be testnet or mainnet');
        }
        if (typeof p.exp !== 'number' || !Number.isFinite(p.exp)) {
            throw new Error('Invalid signed-payload: exp must be a finite number');
        }
        if (typeof p.iss !== 'string' || p.iss.length === 0) {
            throw new Error('Invalid signed-payload: iss must be a non-empty string');
        }
        if (!Array.isArray(p.scope) || !p.scope.every((s: unknown) => typeof s === 'string')) {
            throw new Error('Invalid signed-payload: scope must be an array of strings');
        }
        if (p.operation !== undefined && (typeof p.operation !== 'number' || p.operation < 0)) {
            throw new Error('Invalid signed-payload: operation must be a non-negative number');
        }

        return {
            txHash: p.txHash as string,
            network: p.network as 'testnet' | 'mainnet',
            exp: p.exp as number,
            iss: p.iss as string,
            scope: p.scope as string[],
            operation: p.operation as number | undefined,
        };
    }

    /**
     * Verify the HMAC-SHA256 signature over the raw base64url-encoded payload.
     * The signature must be a lowercase hex string.
     */
    validateSignedPayloadSignature(encodedPayload: string, signature: string, secret: string): boolean {
        const expected = crypto
            .createHmac('sha256', secret)
            .update(encodedPayload)
            .digest('hex');
        try {
            return crypto.timingSafeEqual(
                Buffer.from(signature, 'hex'),
                Buffer.from(expected, 'hex'),
            );
        } catch {
            return false;
        }
    }

    /**
     * Build a signed URI payload, base64url-encode it, and return both the
     * encoded string and the HMAC-SHA256 signature.
     */
    generateSignedPayload(
        params: SignedURIPayload,
        secret: string,
    ): { encoded: string; signature: string } {
        const json = JSON.stringify(params);
        const encoded = Buffer.from(json)
            .toString('base64')
            .replace(/\+/g, '-')
            .replace(/\//g, '_')
            .replace(/=/g, '');
        const signature = crypto
            .createHmac('sha256', secret)
            .update(encoded)
            .digest('hex');
        return { encoded, signature };
    }

    /**
     * Sanitize URI to prevent command injection and other malicious inputs
     */
    sanitize(uriString: string): string {
        // Remove any potentially dangerous characters
        // Keep only alphanumeric and common URI characters
        return uriString
            .replace(/[^\w:/?&=.-]/g, '')
            .substring(0, 500); // Enforce a reasonable maximum length
    }
}
