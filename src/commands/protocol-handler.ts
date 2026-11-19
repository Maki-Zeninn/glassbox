// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { Command } from 'commander';
import { ProtocolHandler } from '../protocol/handler';
import { ProtocolRegistrar, ProtocolRegistrationError } from '../protocol/register';
import { ExitCode } from '../exit-codes';
import * as dotenv from 'dotenv';
import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';

// Load environment variables for security configuration
dotenv.config();

const LOCK_FILE = path.join(os.tmpdir(), 'glassbox-protocol-handler.lock');
const LOCK_STALE_MS = 30_000;

/**
 * Check whether a PID corresponds to a running process.
 * Returns true when the process is live, false when it is gone or the PID
 * is invalid.
 */
function isProcessAlive(pid: number): boolean {
    if (!Number.isInteger(pid) || pid <= 0) {
        return false;
    }
    try {
        // Signal 0 tests existence without sending a real signal.
        process.kill(pid, 0);
        return true;
    } catch {
        return false;
    }
}

function acquireLock(force: boolean, staleLockTimeoutMs = LOCK_STALE_MS): boolean {
    if (force) {
        writeLock();
        return true;
    }

    try {
        const raw = fs.readFileSync(LOCK_FILE, 'utf8').trim();
        const stat = fs.statSync(LOCK_FILE);
        const lockAgeSec = Math.round((Date.now() - stat.mtimeMs) / 1000);

        // If the lock file is recent, check whether the owning process is still alive.
        if (Date.now() - stat.mtimeMs <= staleLockTimeoutMs) {
            const lockedPid = parseInt(raw, 10);
            if (isProcessAlive(lockedPid)) {
                // A live process owns this lock — respect it.
                return false;
            }
            // The PID is gone — this is a stale lock from a crashed handler.
            console.warn(
                `[WARN] Stale lock detected: PID ${lockedPid} is no longer running ` +
                `(lock age: ${lockAgeSec}s). Reclaiming.`
            );
        } else {
            console.warn(
                `[WARN] Lock file exceeded timeout (age: ${lockAgeSec}s, ` +
                `timeout: ${Math.round(staleLockTimeoutMs / 1000)}s). Reclaiming.`
            );
        }

        writeLock();
        return true;
    } catch (err: unknown) {
        // Lock file absent or unreadable — safe to create.
        if ((err as NodeJS.ErrnoException).code !== 'ENOENT') {
            // Unexpected read error — log it but proceed.
            console.warn('[WARN] Could not read lock file:', err);
        }
        writeLock();
        return true;
    }
}

function writeLock(): void {
    fs.writeFileSync(LOCK_FILE, String(process.pid), { flag: 'w' });
}

function releaseLock(): void {
    try {
        fs.unlinkSync(LOCK_FILE);
    } catch {
        // Lock already removed — no action needed.
    }
}

function formatRegistrationSummary(diag: { status: string }): string {
    switch (diag.status) {
        case 'ok': return 'OK';
        case 'degraded': return 'DEGRADED';
        case 'unsupported': return 'UNSUPPORTED';
        default: return diag.status.toUpperCase();
    }
}

function printProtocolRegistrationFailure(action: string, error: unknown): void {
    if (error instanceof ProtocolRegistrationError) {
        console.error(`[FAIL] ${action} failed: ${error.message}`);
        if (error.remediation.length > 0) {
            console.error('Fix:');
            for (const step of error.remediation) {
                console.error(`  - ${step}`);
            }
        }
        return;
    }

    if (error instanceof Error) {
        console.error(`[FAIL] ${action} failed: ${error.message}`);
        return;
    }

    console.error(`[FAIL] ${action} failed: An unknown error occurred`);
}

/**
 * registerProtocolCommands adds protocol-related commands to the Glassbox CLI.
 * These include the internal handler called by the OS and user-facing 
 * registration commands.
 */
export function registerProtocolCommands(program: Command): void {
    // 1. Internal Protocol Handler (hidden from standard help via description)
    program
        .command('protocol-handler <uri>')
        .description('Internal: Handle GLASSBOX Protocol URI (invoked by OS)')
        .option('--force', 'Bypass the concurrency lock')
        .option(
            '--stale-lock-timeout <ms>',
            'Override the stale-lock reclaim timeout in milliseconds (default: 30000)',
            String(LOCK_STALE_MS),
        )
        .action(async (uri: string, opts: { force?: boolean; staleLockTimeout?: string }) => {
            const staleLockMs = parseInt(opts.staleLockTimeout ?? String(LOCK_STALE_MS), 10);
            const effectiveStaleLockMs = Number.isFinite(staleLockMs) && staleLockMs > 0
                ? staleLockMs
                : LOCK_STALE_MS;

            if (!acquireLock(opts.force === true, effectiveStaleLockMs)) {
                // Read the PID from the lock file to give an actionable message.
                let lockDetail = '';
                try {
                    const pid = fs.readFileSync(LOCK_FILE, 'utf8').trim();
                    lockDetail = ` (lock held by PID ${pid})`;
                } catch { /* ignore */ }
                console.error(
                    `[WARN] Another protocol handler instance is running${lockDetail}. ` +
                    `Use --force to override, or wait for it to finish.`
                );
                process.exit(ExitCode.RESOURCE_ERROR);
            }

            const cleanup = (): void => { releaseLock(); };
            process.on('exit', cleanup);
            process.on('SIGINT', () => { cleanup(); process.exit(ExitCode.SIGINT); });
            process.on('SIGTERM', () => { cleanup(); process.exit(ExitCode.SIGTERM); });

            try {
                const handler = new ProtocolHandler({
                    secret: process.env.GLASSBOX_PROTOCOL_SECRET,
                    trustedOrigins: process.env.GLASSBOX_TRUSTED_ORIGINS?.split(','),
                });

                await handler.handle(uri);
            } catch (error) {
                if (error instanceof Error) {
                    console.error(`[FAIL] Protocol handler error: ${error.message}`);
                } else {
                    console.error('[FAIL] Protocol handler error: An unknown error occurred');
                }
                process.exit(ExitCode.UNKNOWN_ERROR);
            } finally {
                releaseLock();
            }
        });

    // 2. Protocol Registration
    program
        .command('protocol:register')
        .description('Register the glassbox:// protocol handler in the operating system (validates CLI path exists and is executable)')
        .action(async () => {
            try {
                const registrar = new ProtocolRegistrar();

                const isRegistered = await registrar.isRegistered();
                if (isRegistered) {
                    console.log('[WARN]  Protocol handler is already registered.');
                    console.log('To refresh registration, run: glassbox protocol:unregister && glassbox protocol:register');
                    console.log('Tip: run "glassbox protocol:status" to verify the registered binary path.');
                    return;
                }

                await registrar.register();
                console.log(' Successfully registered GLASSBOX Protocol handler');
                console.log('You can now launch Glassbox directly from supported dashboards via glassbox:// links.');
                console.log('Tip: run "glassbox protocol:status" to confirm the registration is working.');
            } catch (error) {
                printProtocolRegistrationFailure('Registration', error);
                process.exit(ExitCode.CONFIGURATION_ERROR);
            }
        });

    // 3. Protocol Unregistration
    program
        .command('protocol:unregister')
        .description('Unregister the glassbox:// protocol handler from the operating system')
        .action(async () => {
            try {
                const registrar = new ProtocolRegistrar();
                await registrar.unregister();
                console.log(' Successfully unregistered GLASSBOX Protocol handler');
            } catch (error) {
                printProtocolRegistrationFailure('Unregistration', error);
                process.exit(ExitCode.CONFIGURATION_ERROR);
            }
        });

    // 4. Registration Status
    program
        .command('protocol:status')
        .description('Check registration status and print a diagnostic summary for glassbox://')
        .action(async () => {
            try {
                const registrar = new ProtocolRegistrar();
                const diag = await registrar.diagnose();

                console.log('GLASSBOX Protocol Handler Status');
                console.log('----------------------------');
                console.log(`Summary:         ${formatRegistrationSummary(diag)}`);
                console.log(`Platform:        ${diag.platform}`);
                console.log(`Registered Path: ${diag.cliPath ?? '(not registered)'}`);
                console.log(`Current Binary:  ${diag.currentCliPath}`);

                if (diag.status === 'unsupported') {
                    console.log('Registration:    UNSUPPORTED PLATFORM');
                    console.log('');
                    for (const issue of diag.issues) {
                        console.log(`[FAIL] ${issue}`);
                    }
                    console.log('');
                    console.log('Fix:');
                    for (const fix of diag.remediationSteps) {
                        console.log(`  - ${fix}`);
                    }
                    process.exit(ExitCode.CONFIGURATION_ERROR);
                }

                if (!diag.registered) {
                    console.log('Registration:    NOT REGISTERED');
                    console.log('Path Exists:     No');
                    console.log('Executable:      No');
                    console.log('');
                    for (const issue of diag.issues) {
                        console.log(`[FAIL] ${issue}`);
                    }
                    console.log('');
                    console.log('Fix:');
                    for (const fix of diag.remediationSteps) {
                        console.log(`  - ${fix}`);
                    }
                    process.exit(ExitCode.CONFIGURATION_ERROR);
                }

                console.log(`Registration:    ${diag.status === 'ok' ? 'REGISTERED' : 'REGISTERED (DEGRADED)'}`);
                console.log(`Path Exists:     ${diag.pathExists ? 'Yes' : 'No'}`);
                console.log(`Executable:      ${!diag.pathExists ? 'No' : diag.isExecutable ? 'Yes' : 'No'}`);

                if (diag.status === 'ok') {
                    console.log('[OK] Registered CLI is usable.');
                    return;
                }

                console.log('');
                for (const issue of diag.issues) {
                    console.log(`[FAIL] ${issue}`);
                }
                console.log('');
                console.log('Fix:');
                for (const fix of diag.remediationSteps) {
                    console.log(`  - ${fix}`);
                }
                process.exit(ExitCode.CONFIGURATION_ERROR);
            } catch (error) {
                if (error instanceof Error) {
                    console.error(`[FAIL] Status check failed: ${error.message}`);
                } else {
                    console.error('[FAIL] Status check failed: An unknown error occurred');
                }
                process.exit(ExitCode.UNKNOWN_ERROR);
            }
        });
}
