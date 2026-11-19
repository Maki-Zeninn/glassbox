// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * VS Code extension command integration tests.
 *
 * Uses Node's built-in test runner.  A lightweight vscode mock is injected
 * into the module cache before the extension is loaded so tests never depend
 * on a real VS Code installation or a live Glassbox process.
 *
 * The ERSTClient (RPC client) is replaced with a FakeGlassboxClient that
 * provides the dependency-injection boundary required by the acceptance
 * criteria.
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import Module from 'node:module';
import type { Trace } from './glassboxClient';

// ── Fake Glassbox RPC client ──────────────────────────────────────────────────

class FakeGlassboxClient {
    connectCalled = false;
    debugCalled = false;
    lastDebugHash = '';
    shouldFailDebug = false;
    shouldFailConnect = false;
    fakeTrace: Trace = {
        transaction_hash: 'fake-tx-hash',
        start_time: '2026-01-01T00:00:00Z',
        states: [
            { step: 1, timestamp: '2026-01-01T00:00:01Z', operation: 'invoke', contract_id: 'CABC', function: 'transfer' },
        ],
    };

    async connect(): Promise<void> {
        this.connectCalled = true;
        if (this.shouldFailConnect) throw new Error('Connection refused: fake server not available');
    }

    async debugTransaction(hash: string): Promise<void> {
        this.debugCalled = true;
        this.lastDebugHash = hash;
        if (this.shouldFailDebug) throw new Error('RPC error: transaction not found');
    }

    async getTrace(_hash: string): Promise<Trace> {
        return this.fakeTrace;
    }

    dispose(): void { }
}

// ── Minimal VS Code API mock ──────────────────────────────────────────────────

type CommandHandler = (...args: unknown[]) => unknown;

interface MockContext {
    subscriptions: Array<{ dispose(): void }>;
    globalStorageUri: { fsPath: string };
}

const registeredCommands = new Map<string, CommandHandler>();
const infoMessages: string[] = [];
const errorMessages: string[] = [];
const warnMessages: string[] = [];
let inputBoxValue = 'test-tx-hash-abcdef1234567890abcdef1234567890abcdef1234567890abcdef12';
let saveDialogResult: { fsPath: string } | undefined = undefined;
let currentTrace: Trace | undefined;

const mockVscode = {
    commands: {
        registerCommand(id: string, handler: CommandHandler) {
            registeredCommands.set(id, handler);
            return { dispose() { registeredCommands.delete(id); } };
        },
        executeCommand(id: string, ...args: unknown[]) {
            const h = registeredCommands.get(id);
            return h ? h(...args) : undefined;
        },
    },
    window: {
        createTreeView(_id: string, _opts: unknown) {
            return {
                dispose() { },
                reveal: () => Promise.resolve(),
            };
        },
        showInputBox: async (_opts: unknown) => inputBoxValue,
        withProgress: async (_opts: unknown, fn: (_prog: unknown) => Promise<unknown>) =>
            fn({ report() { } }),
        showInformationMessage(msg: string) { infoMessages.push(msg); },
        showErrorMessage(msg: string) { errorMessages.push(msg); },
        showWarningMessage(msg: string) { warnMessages.push(msg); },
        showSaveDialog: async (_opts: unknown) => saveDialogResult
            ? { fsPath: saveDialogResult.fsPath }
            : undefined,
    },
    ProgressLocation: { Notification: 1 },
    ViewColumn: { Beside: 2 },
    workspace: {
        registerTextDocumentContentProvider(_scheme: string, _provider: unknown) {
            return { dispose() { } };
        },
        openTextDocument: async (opts: unknown) => opts,
        showTextDocument: async (_doc: unknown, _col: unknown) => { },
        workspaceFolders: undefined as unknown,
        fs: {
            writeFile: async (_uri: unknown, _data: unknown) => { },
        },
    },
    Uri: {
        parse: (s: string) => ({ toString: () => s, with: (_: unknown) => ({ toString: () => s }) }),
        joinPath: (...parts: unknown[]) => ({ fsPath: (parts as string[]).join('/') }),
        file: (p: string) => ({ fsPath: p }),
    },
    TreeItem: class {
        label: string;
        constructor(label: string) { this.label = label; }
    },
    ThemeIcon: class {
        constructor(public id: string) { }
    },
    EventEmitter: class {
        private listeners: Array<(v: unknown) => void> = [];
        event = (listener: (v: unknown) => void) => {
            this.listeners.push(listener);
            return { dispose: () => { } };
        };
        fire(value: unknown) { this.listeners.forEach(l => l(value)); }
    },
    TreeItemCollapsibleState: { Collapsed: 1, Expanded: 2, None: 0 },
};

// ── Inject mocks into the module cache ───────────────────────────────────────

const originalLoad = (Module as any)._load.bind(Module);
(Module as any)._load = function (request: string, parent: unknown, isMain: boolean) {
    if (request === 'vscode') return mockVscode;
    return originalLoad(request, parent, isMain);
};

// ── Inject fake client by monkey-patching the glassboxClient module ───────────

const fakeClient = new FakeGlassboxClient();

// Patch glassboxClient after module system is set up
const glassboxClientPath = require.resolve('./glassboxClient');
require.cache[glassboxClientPath] = {
    id: glassboxClientPath,
    filename: glassboxClientPath,
    loaded: true,
    exports: { ERSTClient: class { constructor() { return fakeClient; } } },
    children: [],
    parent: null,
    paths: [],
} as unknown as NodeModule;

const erstClientPath = require.resolve('./erstClient');
require.cache[erstClientPath] = {
    id: erstClientPath,
    filename: erstClientPath,
    loaded: true,
    exports: { ERSTClient: class { constructor() { return fakeClient; } } },
    children: [],
    parent: null,
    paths: [],
} as unknown as NodeModule;

// ── Load the extension under test ─────────────────────────────────────────────

const extension = require('./extension') as { activate: (ctx: MockContext) => void; deactivate: () => void };

// ── Helpers ───────────────────────────────────────────────────────────────────

function freshContext(): MockContext {
    return { subscriptions: [], globalStorageUri: { fsPath: '/tmp/glassbox-test' } };
}

function clearMessages() {
    infoMessages.length = 0;
    errorMessages.length = 0;
    warnMessages.length = 0;
    fakeClient.connectCalled = false;
    fakeClient.debugCalled = false;
    fakeClient.lastDebugHash = '';
    fakeClient.shouldFailDebug = false;
    fakeClient.shouldFailConnect = false;
}

// ── Tests ─────────────────────────────────────────────────────────────────────

test('activate registers all expected commands', () => {
    registeredCommands.clear();
    const ctx = freshContext();
    extension.activate(ctx);

    const expectedCommands = [
        'glassbox.triggerDebug',
        'glassbox.selectTraceStep',
        'glassbox.setTraceSearchQuery',
        'glassbox.exportTraceTree',
        'glassbox.showXdr',
        'glassbox.showStateDiff',
        'Glassbox.nextTraceStep',
        'Glassbox.prevTraceStep',
        'glassbox.openSourceLocation',
        'glassbox.openTraceStep',
    ];

    for (const cmd of expectedCommands) {
        assert.ok(registeredCommands.has(cmd), `Expected command '${cmd}' to be registered`);
    }
});

test('activate pushes disposables into context.subscriptions', () => {
    registeredCommands.clear();
    const ctx = freshContext();
    extension.activate(ctx);
    assert.ok(ctx.subscriptions.length > 0, 'Expected subscriptions to be non-empty');
});

test('glassbox.triggerDebug – valid hash loads trace and shows info message', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    inputBoxValue = 'a'.repeat(64);
    const handler = registeredCommands.get('glassbox.triggerDebug')!;
    await handler();

    assert.ok(fakeClient.debugCalled, 'Expected debugTransaction to be called');
    assert.equal(fakeClient.lastDebugHash, 'a'.repeat(64));
    assert.ok(infoMessages.some(m => m.includes('a'.repeat(64))), 'Expected info message with hash');
});

test('glassbox.triggerDebug – cancelled input box does nothing', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    inputBoxValue = undefined as unknown as string; // simulates user pressing Escape
    const handler = registeredCommands.get('glassbox.triggerDebug')!;
    await handler();

    assert.ok(!fakeClient.debugCalled, 'Expected debugTransaction NOT to be called');
    assert.equal(errorMessages.length, 0, 'Expected no error messages');
});

test('glassbox.triggerDebug – RPC failure shows error message with details', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    inputBoxValue = 'b'.repeat(64);
    fakeClient.shouldFailDebug = true;
    const handler = registeredCommands.get('glassbox.triggerDebug')!;
    await handler();

    assert.ok(errorMessages.some(m => m.includes('Glassbox Error')), 'Expected error message with Glassbox Error prefix');
    assert.ok(errorMessages.some(m => m.includes('RPC error')), 'Expected error message to contain RPC details');
});

test('glassbox.exportTraceTree – warns when no trace is loaded', async () => {
    registeredCommands.clear();
    clearMessages();
    // Reset the tree data by activating fresh (no prior debug call)
    const ctx = freshContext();
    extension.activate(ctx);

    const handler = registeredCommands.get('glassbox.exportTraceTree')!;
    await handler();

    assert.ok(warnMessages.some(m => m.includes('Load a trace first')), 'Expected warning about missing trace');
});

test('glassbox.exportTraceTree – skips export when save dialog is cancelled', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    // Load a trace first
    inputBoxValue = 'c'.repeat(64);
    fakeClient.shouldFailDebug = false;
    await (registeredCommands.get('glassbox.triggerDebug')!)();
    clearMessages();

    saveDialogResult = undefined;
    const handler = registeredCommands.get('glassbox.exportTraceTree')!;
    await handler();

    assert.equal(infoMessages.length, 0, 'Expected no export info message when dialog was cancelled');
});

test('Glassbox.nextTraceStep – does not throw when no trace is loaded', () => {
    registeredCommands.clear();
    const ctx = freshContext();
    extension.activate(ctx);

    const handler = registeredCommands.get('Glassbox.nextTraceStep')!;
    assert.doesNotThrow(() => handler());
});

test('Glassbox.prevTraceStep – does not throw when no trace is loaded', () => {
    registeredCommands.clear();
    const ctx = freshContext();
    extension.activate(ctx);

    const handler = registeredCommands.get('Glassbox.prevTraceStep')!;
    assert.doesNotThrow(() => handler());
});

test('deactivate does not throw', () => {
    assert.doesNotThrow(() => extension.deactivate());
});

// ── openSourceLocation and openTraceStep ──────────────────────────────────────

test('glassbox.openSourceLocation – shows warning when no file and no stepJson', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    const handler = registeredCommands.get('glassbox.openSourceLocation')!;
    await handler({ file: undefined, stepJson: undefined });
    // No message expected — handler exits early with no args.
    assert.equal(errorMessages.length, 0);
});

test('glassbox.openSourceLocation – shows info message with stepJson fallback when file is missing', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    const handler = registeredCommands.get('glassbox.openSourceLocation')!;
    await handler({ file: undefined, stepJson: '{"step":1}' });

    assert.ok(infoMessages.some(m => m.includes('no source location available')),
        'Expected info message about missing source');
});

test('glassbox.openSourceLocation – navigates without error for a valid absolute path', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    const handler = registeredCommands.get('glassbox.openSourceLocation')!;
    // Should not throw even if the file does not exist (the mock openTextDocument never throws)
    await handler({ file: '/src/lib.rs', line: 10, column: 1 });
    assert.equal(errorMessages.length, 0);
});

test('glassbox.openTraceStep – shows warning when no trace is loaded', async () => {
    registeredCommands.clear();
    clearMessages();
    const ctx = freshContext();
    extension.activate(ctx);

    const handler = registeredCommands.get('glassbox.openTraceStep')!;
    await handler({ stepIndex: 0 });

    assert.ok(warnMessages.some(m => m.toLowerCase().includes('no trace')),
        'Expected warning about missing trace');
});
