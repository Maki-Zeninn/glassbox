// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

export interface DebugSessionConfig {
    transactionHash: string;
    network: string;
    operation?: number;
    protocolVersion?: number;
    mockLedgerManifest?: string;
    mockLedgerEntries?: string[];
}

/**
 * DebugSession manages a single interactive debugging session for a Stellar
 * transaction opened via the glassbox:// protocol or CLI.
 */
export class DebugSession {
    private config: DebugSessionConfig;

    constructor(config: DebugSessionConfig) {
        this.config = config;
    }

    async start(): Promise<void> {
        const { transactionHash, network, operation } = this.config;
        const opLabel = operation !== undefined ? ` (operation ${operation})` : '';
        console.log(`[DEBUG] Session started: ${transactionHash} on ${network}${opLabel}`);
    }
}
