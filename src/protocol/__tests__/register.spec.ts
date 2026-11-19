// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { ProtocolRegistrar, ProtocolRegistrationError, formatRegistrationSummary } from '../register';
import * as fs from 'fs/promises';
import * as os from 'os';

jest.mock('fs/promises');
jest.mock('os', () => ({
    ...jest.requireActual('os'),
    platform: jest.fn(() => process.platform),
    homedir: jest.fn(() => (jest.requireActual('os') as typeof import('os')).homedir()),
}));
jest.mock('child_process', () => ({
    exec: jest.fn(),
}));

jest.mock('util', () => {
    const _fn = jest.fn();
    return {
        ...jest.requireActual('util'),
        promisify: jest.fn(() => _fn),
    };
});

const mockExecAsync = (jest.requireMock('util') as any).promisify.mock.results[0].value as jest.Mock;

describe('ProtocolRegistrar.validateRegistrationPrerequisites', () => {
    beforeEach(() => {
        mockExecAsync.mockReset();
        (os.platform as jest.Mock).mockReturnValue('linux');
        (os.homedir as jest.Mock).mockReturnValue('/home/user');
    });

    it('rejects unsupported platforms before registration', async () => {
        (os.platform as jest.Mock).mockReturnValue('freebsd');
        const registrar = new ProtocolRegistrar('/usr/local/bin/glassbox');

        await expect(registrar.validateRegistrationPrerequisites()).rejects.toMatchObject({
            name: 'ProtocolRegistrationError',
            message: expect.stringContaining('not supported on freebsd'),
        });
    });

    it('rejects missing executable paths', async () => {
        (fs.access as jest.Mock).mockRejectedValue(new Error('ENOENT'));
        const registrar = new ProtocolRegistrar('/missing/glassbox');

        await expect(registrar.validateRegistrationPrerequisites()).rejects.toThrow(
            ProtocolRegistrationError,
        );
        await expect(registrar.validateRegistrationPrerequisites()).rejects.toMatchObject({
            message: expect.stringContaining('Executable not found'),
        });
    });

    it('rejects non-executable binaries on Unix', async () => {
        (fs.access as jest.Mock)
            .mockResolvedValueOnce(undefined)
            .mockRejectedValueOnce(new Error('EACCES'));
        mockExecAsync.mockResolvedValue({ stdout: '/usr/bin/xdg-mime\n', stderr: '' });

        const registrar = new ProtocolRegistrar('/usr/local/bin/glassbox');

        await expect(registrar.validateRegistrationPrerequisites()).rejects.toMatchObject({
            message: expect.stringContaining('not executable'),
            remediation: expect.arrayContaining([expect.stringContaining('chmod +x')]),
        });
    });

    it('requires xdg-mime on Linux', async () => {
        (fs.access as jest.Mock).mockResolvedValue(undefined);
        mockExecAsync.mockRejectedValue(new Error('xdg-mime not found'));

        const registrar = new ProtocolRegistrar('/usr/local/bin/glassbox');

        await expect(registrar.validateRegistrationPrerequisites()).rejects.toMatchObject({
            message: expect.stringContaining('xdg-mime is not installed'),
            remediation: expect.arrayContaining([expect.stringContaining('xdg-utils')]),
        });
    });

    it('passes when Linux prerequisites are satisfied', async () => {
        (fs.access as jest.Mock).mockResolvedValue(undefined);
        mockExecAsync.mockResolvedValue({ stdout: '/usr/bin/xdg-mime\n', stderr: '' });

        const registrar = new ProtocolRegistrar('/usr/local/bin/glassbox');
        await expect(registrar.validateRegistrationPrerequisites()).resolves.toBeUndefined();
    });

    it('passes on macOS when the binary is executable', async () => {
        (os.platform as jest.Mock).mockReturnValue('darwin');
        (fs.access as jest.Mock).mockResolvedValue(undefined);

        const registrar = new ProtocolRegistrar('/usr/local/bin/glassbox');
        await expect(registrar.validateRegistrationPrerequisites()).resolves.toBeUndefined();
    });

    it('rejects invalid Windows binary extensions', async () => {
        (os.platform as jest.Mock).mockReturnValue('win32');
        (fs.access as jest.Mock).mockResolvedValue(undefined);

        const registrar = new ProtocolRegistrar('C:\\Glassbox\\readme.txt');

        await expect(registrar.validateRegistrationPrerequisites()).rejects.toMatchObject({
            message: expect.stringContaining('does not look executable on Windows'),
        });
    });
});

describe('ProtocolRegistrar.register', () => {
    beforeEach(() => {
        jest.resetAllMocks();
        mockExecAsync.mockReset();
        (os.platform as jest.Mock).mockReturnValue('linux');
        (os.homedir as jest.Mock).mockReturnValue('/home/user');
    });

    it('validates prerequisites before writing registration artefacts', async () => {
        (fs.access as jest.Mock).mockRejectedValue(new Error('ENOENT'));
        const registrar = new ProtocolRegistrar('/missing/glassbox');

        await expect(registrar.register()).rejects.toThrow(ProtocolRegistrationError);
        expect(fs.mkdir).not.toHaveBeenCalled();
        expect(fs.writeFile).not.toHaveBeenCalled();
    });

    it('wraps shell command failures with remediation guidance', async () => {
        (fs.access as jest.Mock).mockResolvedValue(undefined);
        mockExecAsync
            .mockResolvedValueOnce({ stdout: '/usr/bin/xdg-mime\n', stderr: '' })
            .mockRejectedValueOnce({ stderr: 'permission denied', stdout: '' });

        const registrar = new ProtocolRegistrar('/usr/local/bin/glassbox');

        await expect(registrar.register()).rejects.toMatchObject({
            message: expect.stringContaining('Registration command failed'),
            remediation: expect.arrayContaining([
                'Run "glassbox protocol:status" to inspect the current registration.',
            ]),
        });
    });
});

describe('ProtocolRegistrar.diagnose', () => {
    let registrar: ProtocolRegistrar;

    beforeEach(() => {
        jest.resetAllMocks();
        (os.platform as jest.Mock).mockReturnValue(process.platform);
        (os.homedir as jest.Mock).mockReturnValue(require('os').homedir());
        registrar = new ProtocolRegistrar();
    });

    it('should report unsupported platforms with remediation steps', async () => {
        (os.platform as jest.Mock).mockReturnValue('freebsd');

        const result = await registrar.diagnose();

        expect(result.status).toBe('unsupported');
        expect(result.issues.length).toBeGreaterThan(0);
        expect(result.remediationSteps.length).toBeGreaterThan(0);
        expect(formatRegistrationSummary(result)).toContain('not supported');
    });

    it('should report not registered when protocol is unregistered', async () => {
        jest.spyOn(registrar, 'isRegistered').mockResolvedValue(false);

        const result = await registrar.diagnose();

        expect(result.registered).toBe(false);
        expect(result.status).toBe('not_registered');
        expect(result.cliPath).toBeNull();
        expect(result.issues).toContain('Protocol handler is not registered with the operating system');
        expect(result.remediationSteps.length).toBeGreaterThan(0);
    });

    it('should report unknown path when registered path cannot be resolved', async () => {
        jest.spyOn(registrar, 'isRegistered').mockResolvedValue(true);
        jest.spyOn(registrar, 'getRegisteredPath').mockResolvedValue(null);

        const result = await registrar.diagnose();

        expect(result.registered).toBe(true);
        expect(result.status).toBe('degraded');
        expect(result.cliPath).toBeNull();
        expect(result.issues.length).toBeGreaterThan(0);
    });

    it('should detect missing binary', async () => {
        jest.spyOn(registrar, 'isRegistered').mockResolvedValue(true);
        jest.spyOn(registrar, 'getRegisteredPath').mockResolvedValue('/usr/local/bin/Glassbox');
        (fs.access as jest.Mock).mockRejectedValue(new Error('ENOENT'));

        const result = await registrar.diagnose();

        expect(result.registered).toBe(true);
        expect(result.status).toBe('degraded');
        expect(result.cliPath).toBe('/usr/local/bin/Glassbox');
        expect(result.pathExists).toBe(false);
        expect(result.issues.some((issue) => issue.includes('Binary not found'))).toBe(true);
    });

    it('should detect non-executable binary on Unix', async () => {
        jest.spyOn(registrar, 'isRegistered').mockResolvedValue(true);
        jest.spyOn(registrar, 'getRegisteredPath').mockResolvedValue('/usr/local/bin/Glassbox');
        (os.platform as jest.Mock).mockReturnValue('linux');
        (fs.access as jest.Mock)
            .mockResolvedValueOnce(undefined)
            .mockRejectedValueOnce(new Error('EACCES'));

        const result = await registrar.diagnose();

        expect(result.registered).toBe(true);
        expect(result.status).toBe('degraded');
        expect(result.pathExists).toBe(true);
        expect(result.isExecutable).toBe(false);
        expect(result.remediationSteps.some((step) => step.includes('chmod +x'))).toBe(true);
    });

    it('should check file extension for executability on Windows', async () => {
        jest.spyOn(registrar, 'isRegistered').mockResolvedValue(true);
        jest.spyOn(registrar, 'getRegisteredPath').mockResolvedValue('C:\\Program Files\\Glassbox\\glassbox.exe');
        (os.platform as jest.Mock).mockReturnValue('win32');
        (fs.access as jest.Mock).mockResolvedValue(undefined);

        const result = await registrar.diagnose();

        expect(result.status).toBe('ok');
        expect(result.registered).toBe(true);
        expect(result.pathExists).toBe(true);
        expect(result.isExecutable).toBe(true);
    });

    it('should reject non-executable extension on Windows', async () => {
        jest.spyOn(registrar, 'isRegistered').mockResolvedValue(true);
        jest.spyOn(registrar, 'getRegisteredPath').mockResolvedValue('C:\\Glassbox\\Glassbox.txt');
        (os.platform as jest.Mock).mockReturnValue('win32');
        (fs.access as jest.Mock).mockResolvedValue(undefined);

        const result = await registrar.diagnose();

        expect(result.status).toBe('degraded');
        expect(result.registered).toBe(true);
        expect(result.pathExists).toBe(true);
        expect(result.isExecutable).toBe(false);
    });

    it('should confirm fully healthy registration', async () => {
        jest.spyOn(registrar, 'isRegistered').mockResolvedValue(true);
        jest.spyOn(registrar, 'getRegisteredPath').mockResolvedValue('/usr/local/bin/Glassbox');
        (os.platform as jest.Mock).mockReturnValue('linux');
        (fs.access as jest.Mock).mockResolvedValue(undefined);

        const result = await registrar.diagnose();

        expect(result.status).toBe('ok');
        expect(result.registered).toBe(true);
        expect(result.cliPath).toBe('/usr/local/bin/Glassbox');
        expect(result.pathExists).toBe(true);
        expect(result.isExecutable).toBe(true);
        expect(result.issues).toHaveLength(0);
    });
});

describe('ProtocolRegistrar.register', () => {
    let registrar: ProtocolRegistrar;

    beforeEach(() => {
        jest.resetAllMocks();
        (os.platform as jest.Mock).mockReturnValue(process.platform);
        (os.homedir as jest.Mock).mockReturnValue(require('os').homedir());
    });

    it('should throw if CLI path is not absolute', async () => {
        registrar = new ProtocolRegistrar('relative/path');
        await expect(registrar.register()).rejects.toThrow("Registration failed: CLI path must be absolute, got 'relative/path'.");
    });

    it('should throw if CLI executable is not found', async () => {
        registrar = new ProtocolRegistrar('/usr/local/bin/nonexistent');
        (fs.access as jest.Mock).mockRejectedValue(new Error('ENOENT'));
        await expect(registrar.register()).rejects.toThrow("Registration failed: CLI executable not found at '/usr/local/bin/nonexistent'.");
    });

    it('should throw if Windows executable has invalid extension', async () => {
        (os.platform as jest.Mock).mockReturnValue('win32');
        registrar = new ProtocolRegistrar('C:\\invalid.txt');
        (fs.access as jest.Mock).mockResolvedValue(undefined);
        await expect(registrar.register()).rejects.toThrow("Registration failed: Invalid executable extension on Windows for 'C:\\invalid.txt'.");
    });

    it('should throw if Unix executable is not executable', async () => {
        (os.platform as jest.Mock).mockReturnValue('linux');
        registrar = new ProtocolRegistrar('/usr/local/bin/script.sh');
        (fs.access as jest.Mock).mockResolvedValueOnce(undefined); // First access check for existence
        (fs.access as jest.Mock).mockRejectedValueOnce(new Error('EACCES')); // Second for executability
        await expect(registrar.register()).rejects.toThrow("Registration failed: CLI file is not executable at '/usr/local/bin/script.sh'.");
    });
    
    it('should throw with clear message if OS registration fails', async () => {
        (os.platform as jest.Mock).mockReturnValue('linux');
        registrar = new ProtocolRegistrar('/usr/local/bin/glassbox');
        (fs.access as jest.Mock).mockResolvedValue(undefined);
        
        // Mock the internal registerLinux method to fail
        jest.spyOn(registrar as any, 'registerLinux').mockRejectedValue(new Error('Permission denied'));
        
        await expect(registrar.register()).rejects.toThrow("Protocol registration failed on linux: Permission denied");
    });
});
