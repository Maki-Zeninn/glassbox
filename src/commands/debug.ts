// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { Command } from 'commander';
import { RPCConfigParser } from '../config/rpc-config';
import { FallbackRPCClient } from '../rpc/fallback-client';
import { getLogger, setLogLevel, LogLevel, LogCategory } from '../utils/logger';
import { ExitCode } from '../exit-codes';

/** A valid Stellar transaction hash is exactly 64 lowercase hex characters. */
const TX_HASH_RE = /^[0-9a-f]{64}$/i;

/**
 * validateTransactionHash returns an error message string when the hash is
 * invalid, or null when it is acceptable.
 */
function validateTransactionHash(hash: string): string | null {
    if (!hash || hash.trim().length === 0) {
        return 'transaction hash is required';
    }
    const trimmed = hash.trim();
    if (!TX_HASH_RE.test(trimmed)) {
        return `invalid transaction hash "${trimmed}" — expected 64 hexadecimal characters (got ${trimmed.length})`;
    }
    return null;
}

/**
 * parsePositiveInt parses a CLI option string as a positive integer and
 * returns a descriptive error when the value is missing, non-numeric, or
 * non-positive.
 */
function parsePositiveInt(name: string, raw: string, defaultValue: number): { value: number; error: string | null } {
    if (raw === undefined || raw === null || raw === '') {
        return { value: defaultValue, error: null };
    }
    const n = Number(raw);
    if (!Number.isInteger(n) || isNaN(n)) {
        return { value: defaultValue, error: `--${name} must be an integer, got "${raw}"` };
    }
    if (n <= 0) {
        return { value: defaultValue, error: `--${name} must be a positive integer, got ${n}` };
    }
    return { value: n, error: null };
}

export function registerDebugCommand(program: Command): void {
    program
        .command('debug <transaction>')
        .description('Debug a Stellar transaction with RPC fallback support')
        .option(
            '--rpc <urls>',
            'Comma-separated list of RPC URLs (e.g., https://rpc1.com,https://rpc2.com)',
        )
        .option('--timeout <ms>', 'Request timeout in milliseconds (must be > 0)', '30000')
        .option('--retries <n>', 'Number of retries per endpoint (must be > 0)', '3')
        .option('--verbose', 'Enable verbose output with detailed execution steps')
        .action(async (transaction: string, options) => {
            const startTime = Date.now();

            // ── Input validation ─────────────────────────────────────────────
            const hashErr = validateTransactionHash(transaction);
            if (hashErr !== null) {
                console.error(`[FAIL] Validation error: ${hashErr}`);
                console.error('       Provide a 64-character hex transaction hash, e.g.:');
                console.error('         glassbox debug 5c0a1234...ef7890ab');
                process.exit(ExitCode.VALIDATION_ERROR);
            }

            const { value: timeoutMs, error: timeoutErr } = parsePositiveInt('timeout', options.timeout, 30000);
            if (timeoutErr !== null) {
                console.error(`[FAIL] Validation error: ${timeoutErr}`);
                process.exit(ExitCode.VALIDATION_ERROR);
            }

            const { value: retriesCount, error: retriesErr } = parsePositiveInt('retries', options.retries, 3);
            if (retriesErr !== null) {
                console.error(`[FAIL] Validation error: ${retriesErr}`);
                process.exit(ExitCode.VALIDATION_ERROR);
            }
            // ─────────────────────────────────────────────────────────────────

            // Set log level based on verbose flag
            if (options.verbose) {
                setLogLevel(LogLevel.VERBOSE);
            } else {
                setLogLevel(LogLevel.STANDARD);
            }

            const logger = getLogger();

            try {
                // Load RPC configuration
                const config = RPCConfigParser.loadConfig({
                    rpc: options.rpc,
                    timeout: timeoutMs,
                    retries: retriesCount,
                });

                // Initialize RPC client with fallback
                const rpcClient = new FallbackRPCClient(config);

                // Standard output
                logger.info(`\n[SEARCH] Debugging transaction: ${transaction.trim()}\n`);

                // Verbose: Show configuration
                logger.verbose(LogCategory.INFO, 'Configuration');
                logger.verboseIndent(LogCategory.INFO, `RPC URL: ${options.rpc || 'Default'}`);
                logger.verboseIndent(LogCategory.INFO, `Transaction hash: ${transaction.trim()}`);
                logger.verboseIndent(LogCategory.INFO, `Timeout: ${timeoutMs}ms`);
                logger.verboseIndent(LogCategory.INFO, `Retries: ${retriesCount}`);
                logger.verboseIndent(LogCategory.INFO, `Verbose mode: enabled\n`);

                // Make RPC request
                logger.verbose(LogCategory.RPC, 'Initiating transaction fetch...');
                const txData = await rpcClient.request('/transactions/' + transaction.trim(), { method: 'GET' });

                // Verbose: Data parsing
                logger.verbose(LogCategory.DATA, 'Parsing transaction response...');
                logger.verboseIndent(LogCategory.DATA, `Ledger: ${txData.ledger ?? 'N/A'}`);
                logger.verboseIndent(LogCategory.DATA, `Source: ${txData.source_account ?? 'N/A'}`);

                logger.success('Transaction fetched successfully');
                logger.info(`Transaction data: ${JSON.stringify(txData, null, 2)}`);

                // Success
                logger.success('Debug complete');

                // Performance metrics (verbose)
                const totalDuration = Date.now() - startTime;
                const memUsage = process.memoryUsage();

                logger.verbose(LogCategory.PERF, 'Performance metrics');
                logger.verboseIndent(LogCategory.PERF, `Total execution time: ${totalDuration}ms`);
                logger.verboseIndent(LogCategory.PERF, `Memory usage: ${logger.formatBytes(memUsage.heapUsed)}`);
                logger.verboseIndent(LogCategory.PERF, `Peak memory: ${logger.formatBytes(memUsage.heapTotal)}`);

            } catch (error) {
                const totalDuration = Date.now() - startTime;
                let exitCode: number = ExitCode.UNKNOWN_ERROR;
                if (error instanceof Error) {
                    logger.error('Debug failed', error);
                    // Provide actionable guidance for common failure modes.
                    if (error.message.includes('not found') || error.message.includes('404')) {
                        exitCode = ExitCode.NETWORK_ERROR;
                        console.error('       The transaction was not found on the network.');
                        console.error('       Check the hash and ensure you are querying the correct network.');
                    } else if (error.message.includes('timeout') || error.message.includes('ETIMEDOUT')) {
                        exitCode = ExitCode.NETWORK_ERROR;
                        console.error(`       The request timed out after ${timeoutMs}ms.`);
                        console.error('       Try increasing --timeout or verify the RPC endpoint is reachable.');
                    } else if (error.message.includes('ECONNREFUSED') || error.message.includes('ENOTFOUND')) {
                        exitCode = ExitCode.NETWORK_ERROR;
                        console.error('       Could not connect to the RPC endpoint.');
                        console.error('       Check the --rpc URL and your network connectivity.');
                    }
                } else {
                    logger.error('Debug failed: An unknown error occurred');
                }

                logger.verbose(LogCategory.PERF, `Failed after ${totalDuration}ms`);
                process.exit(exitCode);
            }
        });

    // Add health check command
    program
        .command('rpc:health')
        .description('Check health of all configured RPC endpoints')
        .option('--rpc <urls>', 'Comma-separated list of RPC URLs')
        .action(async (options) => {
            try {
                const config = RPCConfigParser.loadConfig({ rpc: options.rpc });
                const rpcClient = new FallbackRPCClient(config);

                await rpcClient.performHealthChecks();

                const status = rpcClient.getHealthStatus();

                console.log('\n[STATS] RPC Endpoint Status:\n');
                status.forEach((ep, idx) => {
                    const statusIcon = ep.healthy ? '' : '[FAIL]';
                    const circuit = ep.circuitOpen ? ' [CIRCUIT OPEN]' : '';
                    const successRate =
                        ep.metrics.totalRequests > 0
                            ? ((ep.metrics.totalSuccess / ep.metrics.totalRequests) * 100).toFixed(1)
                            : '0.0';

                    console.log(`  [${idx + 1}] ${statusIcon} ${ep.url}${circuit}`);
                    console.log(`      Success Rate: ${successRate}% (${ep.metrics.totalSuccess}/${ep.metrics.totalRequests})`);
                    console.log(`      Avg Duration: ${ep.metrics.averageDuration}ms`);
                    console.log(`      Failures: ${ep.failureCount}`);
                });
            } catch (error) {
                if (error instanceof Error) {
                    console.error('[FAIL] Health check failed:', error.message);
                } else {
                    console.error('[FAIL] Health check failed: An unknown error occurred');
                }
                process.exit(ExitCode.UNKNOWN_ERROR);
            }
        });
}
