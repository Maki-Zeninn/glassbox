// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import * as vscode from 'vscode';
import { ERSTClient } from './erstClient';
import { TraceTreeDataProvider, TraceItem } from './traceTreeView';
import { buildTraceTreeExport, renderStandaloneHtml } from './traceExport';
import {
    resolveFrameLocation,
    classifyPath,
    originLabelText,
    type TraceFrame,
} from './sourceMap';

export function activate(context: vscode.ExtensionContext) {
    const client = new ERSTClient('127.0.0.1', 8080);
    let treeView: vscode.TreeView<vscode.TreeItem> | undefined;
    let traceDataProvider: TraceTreeDataProvider;

    // Register TreeView with provider (pass treeView to provider for auto-reveal)
    traceDataProvider = new TraceTreeDataProvider();
    treeView = vscode.window.createTreeView('glassbox-traces', { treeDataProvider: traceDataProvider });
    // Patch: set treeView reference in provider for auto-reveal
    (traceDataProvider as any).treeView = treeView;

    // Register TextDocumentContentProvider for states
    const stateProvider = new class implements vscode.TextDocumentContentProvider {
        provideTextDocumentContent(uri: vscode.Uri): string {
            // Decode content from query
            return uri.query;
        }
    };
    context.subscriptions.push(vscode.workspace.registerTextDocumentContentProvider('Glassbox-state', stateProvider));

    // Register command: glassbox.triggerDebug
    let triggerDebugDisposable = vscode.commands.registerCommand('glassbox.triggerDebug', async () => {
        const hash = await vscode.window.showInputBox({
            prompt: 'Enter Transaction Hash to Debug',
            placeHolder: 'e.g., sample-tx-hash-1234'
        });

        if (hash) {
            try {
                await vscode.window.withProgress({
                    location: vscode.ProgressLocation.Notification,
                    title: "GLASSBOX: Debugging Transaction...",
                    cancellable: false
                }, async (progress: vscode.Progress<{ message?: string; increment?: number }>) => {
                    await client.connect();
                    await client.debugTransaction(hash);
                    const trace = await client.getTrace(hash);
                    traceDataProvider.refresh(trace);
                });
                vscode.window.showInformationMessage(`Trace loaded for ${hash}`);
            } catch (err: any) {
                vscode.window.showErrorMessage(`Glassbox Error: ${err.message}`);
            }
        }
    });

    // Handle selecting a trace item
    let selectTraceStepDisposable = vscode.commands.registerCommand('glassbox.selectTraceStep', (item: TraceItem) => {
        const stepJson = JSON.stringify(item.step, null, 2);

        vscode.workspace.openTextDocument({
            content: stepJson,
            language: 'json'
        }).then((doc: vscode.TextDocument) => {
            vscode.window.showTextDocument(doc, vscode.ViewColumn.Beside);
        });
    });

    let setSearchQueryDisposable = vscode.commands.registerCommand('glassbox.setTraceSearchQuery', async () => {
        const value = await vscode.window.showInputBox({
            prompt: 'Set trace search query for export matching',
            placeHolder: 'e.g., transfer or contract-id prefix',
            value: traceDataProvider.getSearchQuery()
        });

        if (value !== undefined) {
            traceDataProvider.setSearchQuery(value);
            const label = value.trim() === '' ? '(cleared)' : `"${value}"`;
            vscode.window.showInformationMessage(`Trace search query updated: ${label}`);
        }
    });

    let exportTraceTreeDisposable = vscode.commands.registerCommand('glassbox.exportTraceTree', async () => {
        const trace = traceDataProvider.getCurrentTrace();
        if (!trace) {
            vscode.window.showWarningMessage('Load a trace first, then export.');
            return;
        }

        const defaultBase = `${trace.transaction_hash || 'trace'}-trace-tree.html`;
        const defaultDir =
            vscode.workspace.workspaceFolders?.[0]?.uri ?? context.globalStorageUri;
        const defaultUri = vscode.Uri.joinPath(defaultDir, defaultBase);
        const htmlTarget = await vscode.window.showSaveDialog({
            title: 'Export trace tree as standalone HTML',
            defaultUri,
            filters: { HTML: ['html'] }
        });

        if (!htmlTarget) {
            return;
        }

        const payload = buildTraceTreeExport(trace, traceDataProvider.getSearchQuery());
        const html = renderStandaloneHtml(payload);
        const json = JSON.stringify(payload, null, 2);
        const jsonPath = htmlTarget.fsPath.replace(/\.html?$/i, '.json');
        const jsonTarget = vscode.Uri.file(jsonPath);

        await vscode.workspace.fs.writeFile(htmlTarget, Buffer.from(html, 'utf8'));
        await vscode.workspace.fs.writeFile(jsonTarget, Buffer.from(json, 'utf8'));

        vscode.window.showInformationMessage(
            `Trace tree exported: ${htmlTarget.fsPath} and ${jsonTarget.fsPath}`
        );
    });

    // Handle showing XDR
    let showXdrDisposable = vscode.commands.registerCommand('glassbox.showXdr', (xdr: string) => {
        vscode.workspace.openTextDocument({
            content: xdr,
            language: 'text'
        }).then((doc: vscode.TextDocument) => {
            vscode.window.showTextDocument(doc, vscode.ViewColumn.Beside);
        });
    });

    // Handle showing state diff
    let showStateDiffDisposable = vscode.commands.registerCommand('glassbox.showStateDiff', (before: string, after: string) => {
        const baseUri = vscode.Uri.parse('Glassbox-state:state');
        const beforeUri = baseUri.with({ path: 'before', query: before });
        const afterUri = baseUri.with({ path: 'after', query: after });

        vscode.commands.executeCommand('vscode.diff', beforeUri, afterUri, 'State Diff (Before vs After)');
    });

    // Navigation: next/prev step commands
    let nextStepDisposable = vscode.commands.registerCommand('Glassbox.nextTraceStep', async () => {
        const trace = traceDataProvider.getCurrentTrace();
        if (!trace) return;
        const idx = traceDataProvider.getCurrentStepIndex();
        if (idx < trace.states.length - 1) {
            await vscode.commands.executeCommand('glassbox.openTraceStep', { stepIndex: idx + 1 });
        }
    });
    let prevStepDisposable = vscode.commands.registerCommand('Glassbox.prevTraceStep', async () => {
        const trace = traceDataProvider.getCurrentTrace();
        if (!trace) return;
        const idx = traceDataProvider.getCurrentStepIndex();
        if (idx > 0) {
            await vscode.commands.executeCommand('glassbox.openTraceStep', { stepIndex: idx - 1 });
        }
    });

    // Register command: glassbox.openSourceLocation
    // Opens a source file at the given location. Used by trace step click-through
    // and glassbox://trace/ deep links.  When the file cannot be resolved a
    // graceful fallback message is shown instead of throwing.
    let openSourceLocationDisposable = vscode.commands.registerCommand(
        'glassbox.openSourceLocation',
        async (args: { file?: string; line?: number; column?: number; stepJson?: string } | undefined) => {
            const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

            if (!args?.file) {
                if (args?.stepJson) {
                    // Fallback: no file path available — show raw step JSON.
                    const doc = await vscode.workspace.openTextDocument({
                        content: args.stepJson,
                        language: 'json',
                    });
                    await vscode.window.showTextDocument(doc, vscode.ViewColumn.Beside);
                    vscode.window.showInformationMessage(
                        'Glassbox: no source location available for this trace step — showing step JSON instead.'
                    );
                }
                return;
            }

            const frame: TraceFrame = {
                file: args.file,
                line: args.line,
                column: args.column,
            };

            const origin = classifyPath(args.file, workspaceRoot);
            const originTag = originLabelText(origin);
            const loc = resolveFrameLocation(frame, workspaceRoot);

            if (!loc) {
                const msg = originTag
                    ? `Glassbox: source not found (${originTag} ${args.file}) — showing step JSON instead`
                    : `Glassbox: source not found for ${args.file} — showing step JSON instead`;
                vscode.window.showInformationMessage(msg);
                if (args.stepJson) {
                    const doc = await vscode.workspace.openTextDocument({
                        content: args.stepJson,
                        language: 'json',
                    });
                    await vscode.window.showTextDocument(doc, vscode.ViewColumn.Beside);
                }
                return;
            }

            // Warn for generated / external paths but still navigate.
            if (origin === 'generated') {
                vscode.window.showInformationMessage(
                    `Glassbox: navigating to generated source — ${originTag} ${args.file}`
                );
            } else if (origin === 'external') {
                vscode.window.showInformationMessage(
                    `Glassbox: navigating to external dependency — ${originTag} ${args.file}`
                );
            }

            try {
                const docUri = vscode.Uri.parse(loc.uri);
                const pos = new vscode.Position(loc.line, loc.column);
                const doc = await vscode.workspace.openTextDocument(docUri);
                await vscode.window.showTextDocument(doc, {
                    selection: new vscode.Range(pos, pos),
                    viewColumn: vscode.ViewColumn.Beside,
                    preserveFocus: false,
                });
            } catch {
                // File exists on disk but could not be opened (e.g. binary WASM).
                const msg = originTag
                    ? `Glassbox: cannot open ${originTag} file ${args.file}`
                    : `Glassbox: cannot open ${args.file}`;
                vscode.window.showWarningMessage(msg);
                if (args.stepJson) {
                    const doc = await vscode.workspace.openTextDocument({
                        content: args.stepJson,
                        language: 'json',
                    });
                    await vscode.window.showTextDocument(doc, vscode.ViewColumn.Beside);
                }
            }
        }
    );

    // Register command: glassbox.openTraceStep
    // Jumps to a step by index in the current trace and navigates to its
    // source location when available.  Used by next/prev navigation and by
    // glassbox://trace/ deep links.
    let openTraceStepDisposable = vscode.commands.registerCommand(
        'glassbox.openTraceStep',
        async (args: { stepIndex: number } | undefined) => {
            const trace = traceDataProvider.getCurrentTrace();
            if (!trace) {
                vscode.window.showWarningMessage(
                    'Glassbox: no trace loaded. Use "Glassbox: Debug Transaction" to load one first.'
                );
                return;
            }

            const idx = args?.stepIndex ?? traceDataProvider.getCurrentStepIndex();
            if (idx < 0 || idx >= trace.states.length) {
                vscode.window.showWarningMessage(
                    `Glassbox: step index ${idx} is out of range for this trace (${trace.states.length} steps).`
                );
                return;
            }

            // Advance the provider to this step (highlights it in the tree view).
            traceDataProvider.setCurrentStepIndex(idx);

            const step = trace.states[idx];
            const stepJson = JSON.stringify(step, null, 2);
            const sourceRef = (step as any).source_ref as
                | { file?: string; line?: number; column?: number }
                | undefined;

            await vscode.commands.executeCommand('glassbox.openSourceLocation', {
                file: sourceRef?.file,
                line: sourceRef?.line,
                column: sourceRef?.column,
                stepJson,
            });
        }
    );

    context.subscriptions.push(
        triggerDebugDisposable,
        selectTraceStepDisposable,
        setSearchQueryDisposable,
        exportTraceTreeDisposable,
        treeView,
        showXdrDisposable,
        showStateDiffDisposable,
        nextStepDisposable,
        prevStepDisposable,
        openSourceLocationDisposable,
        openTraceStepDisposable,
        client
    );
}

export function deactivate() { }
