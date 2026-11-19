// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { ExitCode } from '../exit-codes';

describe('ExitCode registry', () => {
    it('SUCCESS is 0', () => {
        expect(ExitCode.SUCCESS).toBe(0);
    });

    it('UNKNOWN_ERROR is 1 (POSIX catch-all)', () => {
        expect(ExitCode.UNKNOWN_ERROR).toBe(1);
    });

    it('VALIDATION_ERROR is 2 (POSIX misuse convention)', () => {
        expect(ExitCode.VALIDATION_ERROR).toBe(2);
    });

    it('CONFIGURATION_ERROR is 3', () => {
        expect(ExitCode.CONFIGURATION_ERROR).toBe(3);
    });

    it('NETWORK_ERROR is 4', () => {
        expect(ExitCode.NETWORK_ERROR).toBe(4);
    });

    it('SECURITY_ERROR is 5', () => {
        expect(ExitCode.SECURITY_ERROR).toBe(5);
    });

    it('RESOURCE_ERROR is 6', () => {
        expect(ExitCode.RESOURCE_ERROR).toBe(6);
    });

    it('SIGINT is 130 (128 + SIGINT signal number)', () => {
        expect(ExitCode.SIGINT).toBe(130);
    });

    it('SIGTERM is 143 (128 + SIGTERM signal number)', () => {
        expect(ExitCode.SIGTERM).toBe(143);
    });

    it('all numeric values are unique', () => {
        const values = Object.values(ExitCode) as number[];
        const unique = new Set(values);
        expect(unique.size).toBe(values.length);
    });

    it('all values are non-negative integers', () => {
        for (const v of Object.values(ExitCode)) {
            expect(Number.isInteger(v)).toBe(true);
            expect(v).toBeGreaterThanOrEqual(0);
        }
    });

    it('equivalent failure categories differ from SUCCESS and from each other', () => {
        const errorCodes = [
            ExitCode.UNKNOWN_ERROR,
            ExitCode.VALIDATION_ERROR,
            ExitCode.CONFIGURATION_ERROR,
            ExitCode.NETWORK_ERROR,
            ExitCode.SECURITY_ERROR,
            ExitCode.RESOURCE_ERROR,
        ];
        for (const code of errorCodes) {
            expect(code).not.toBe(ExitCode.SUCCESS);
        }
    });
});
