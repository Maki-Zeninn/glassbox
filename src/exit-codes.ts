// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * Cross-language exit-code registry for Glassbox CLI.
 *
 * All TypeScript command wrappers and shell scripts must use these codes so
 * that callers can rely on a stable, documented contract.
 *
 * Go binary equivalents are documented in docs/exit-codes.md.
 *
 * Codes 128–143 are reserved for fatal signal termination per POSIX.
 */
export const ExitCode = {
    /** Command completed successfully. */
    SUCCESS: 0,

    /** Unexpected or unclassified runtime error. */
    UNKNOWN_ERROR: 1,

    /** Invalid user input: bad arguments, malformed payload, wrong format. */
    VALIDATION_ERROR: 2,

    /** Configuration or provider error: missing key, unsupported platform, registration failure. */
    CONFIGURATION_ERROR: 3,

    /** Network or RPC error: connection refused, timeout, endpoint unavailable. */
    NETWORK_ERROR: 4,

    /** Security or authorization error: invalid signature, unauthorized origin, rate limit exceeded. */
    SECURITY_ERROR: 5,

    /** Resource or concurrency error: lock acquisition failed, resource busy. */
    RESOURCE_ERROR: 6,

    /** Process received SIGINT (Ctrl-C). */
    SIGINT: 130,

    /** Process received SIGTERM. */
    SIGTERM: 143,
} as const;

export type ExitCode = (typeof ExitCode)[keyof typeof ExitCode];
