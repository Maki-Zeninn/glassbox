// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Unit tests for the source-map navigation utilities (issue #858).
 *
 * Coverage:
 *  - toFileUri: Unix absolute, Windows absolute, workspace-relative,
 *               already-URI pass-through, empty/missing paths.
 *  - normalizeLine / normalizeColumn: 1-based to 0-based, boundary values,
 *               undefined/null/NaN/Infinity guards.
 *  - resolveFrameLocation: full frame resolution, generated sources,
 *               external paths, unmapped (no-file) frames.
 *  - toWorkspaceRelative: path stripping, mixed slashes, non-workspace paths.
 *
 * All tests run offline with no VS Code runtime or real filesystem access.
 */

import test from 'node:test';
import assert from 'node:assert/strict';
import {
    toFileUri,
    normalizeLine,
    normalizeColumn,
    resolveFrameLocation,
    toWorkspaceRelative,
    type TraceFrame,
} from './sourceMap';

// ── toFileUri ─────────────────────────────────────────────────────────────────

test('toFileUri: Unix absolute path becomes file:// URI', () => {
    assert.equal(toFileUri('/home/user/project/src/main.go'), 'file:///home/user/project/src/main.go');
});

test('toFileUri: Windows absolute path uses triple-slash scheme', () => {
    assert.equal(toFileUri('C:\\Users\\dev\\project\\main.go'), 'file:///C:/Users/dev/project/main.go');
    assert.equal(toFileUri('D:/work/repo/lib/util.ts'), 'file:///D:/work/repo/lib/util.ts');
});

test('toFileUri: already a file:// URI is returned unchanged', () => {
    const uri = 'file:///home/user/contract.rs';
    assert.equal(toFileUri(uri), uri);
});

test('toFileUri: custom scheme URI is returned unchanged', () => {
    const uri = 'Glassbox-state:before?ledger=42';
    assert.equal(toFileUri(uri), uri);
});

test('toFileUri: workspace-relative path is resolved against workspaceRoot', () => {
    assert.equal(
        toFileUri('src/main.go', '/home/user/project'),
        'file:///home/user/project/src/main.go',
    );
});

test('toFileUri: workspace-relative Windows path is resolved correctly', () => {
    assert.equal(
        toFileUri('src\\util.ts', 'C:\\dev\\repo'),
        'file:///C:/dev/repo/src/util.ts',
    );
});

test('toFileUri: relative path without workspace root is returned as-is', () => {
    assert.equal(toFileUri('src/main.go'), 'src/main.go');
});

test('toFileUri: empty string returns empty string', () => {
    assert.equal(toFileUri(''), '');
});

test('toFileUri: Windows backslashes in absolute path are normalised', () => {
    assert.equal(toFileUri('C:\\foo\\bar\\baz.go'), 'file:///C:/foo/bar/baz.go');
});

test('toFileUri: trailing slash on workspaceRoot is stripped before joining', () => {
    assert.equal(
        toFileUri('lib/x.ts', '/root/'),
        'file:///root/lib/x.ts',
    );
});

// ── normalizeLine ─────────────────────────────────────────────────────────────

test('normalizeLine: 1-based line 1 maps to 0-based 0', () => {
    assert.equal(normalizeLine(1), 0);
});

test('normalizeLine: 1-based line 42 maps to 0-based 41', () => {
    assert.equal(normalizeLine(42), 41);
});

test('normalizeLine: 0-based mode returns the value unchanged', () => {
    assert.equal(normalizeLine(10, 0), 10);
});

test('normalizeLine: undefined returns 0', () => {
    assert.equal(normalizeLine(undefined), 0);
});

test('normalizeLine: null returns 0', () => {
    assert.equal(normalizeLine(null), 0);
});

test('normalizeLine: NaN returns 0', () => {
    assert.equal(normalizeLine(NaN), 0);
});

test('normalizeLine: Infinity returns 0', () => {
    assert.equal(normalizeLine(Infinity), 0);
});

test('normalizeLine: negative 1-based value clamps to 0', () => {
    assert.equal(normalizeLine(-5), 0);
});

test('normalizeLine: line 0 in 1-based mode clamps to 0', () => {
    assert.equal(normalizeLine(0), 0);
});

// ── normalizeColumn ───────────────────────────────────────────────────────────

test('normalizeColumn: 1-based column 1 maps to 0', () => {
    assert.equal(normalizeColumn(1), 0);
});

test('normalizeColumn: 1-based column 10 maps to 9', () => {
    assert.equal(normalizeColumn(10), 9);
});

test('normalizeColumn: 0-based mode is a pass-through', () => {
    assert.equal(normalizeColumn(5, 0), 5);
});

test('normalizeColumn: undefined returns 0', () => {
    assert.equal(normalizeColumn(undefined), 0);
});

test('normalizeColumn: negative clamps to 0', () => {
    assert.equal(normalizeColumn(-1), 0);
});

// ── resolveFrameLocation ──────────────────────────────────────────────────────

test('resolveFrameLocation: standard mapped frame resolves to correct URI and 0-based offsets', () => {
    const frame: TraceFrame = { file: '/project/src/lib.rs', line: 10, column: 5 };
    const loc = resolveFrameLocation(frame);
    assert.ok(loc, 'expected a location');
    assert.equal(loc.uri, 'file:///project/src/lib.rs');
    assert.equal(loc.line, 9);
    assert.equal(loc.column, 4);
});

test('resolveFrameLocation: workspace-relative path resolved with root', () => {
    const frame: TraceFrame = { file: 'src/contract.rs', line: 1, column: 1 };
    const loc = resolveFrameLocation(frame, '/home/dev/project');
    assert.ok(loc);
    assert.equal(loc.uri, 'file:///home/dev/project/src/contract.rs');
    assert.equal(loc.line, 0);
    assert.equal(loc.column, 0);
});

test('resolveFrameLocation: generated source frame is resolved (not hidden)', () => {
    const frame: TraceFrame = { file: '/build/out/contract.js', line: 3, generated: true };
    const loc = resolveFrameLocation(frame);
    assert.ok(loc, 'generated frame should still produce a location');
    assert.equal(loc.uri, 'file:///build/out/contract.js');
});

test('resolveFrameLocation: external source frame is resolved (not hidden)', () => {
    const frame: TraceFrame = { file: '/usr/lib/stellar/sdk.go', line: 200, external: true };
    const loc = resolveFrameLocation(frame);
    assert.ok(loc, 'external frame should still produce a location');
});

test('resolveFrameLocation: frame with no file returns undefined (no throw)', () => {
    const frame: TraceFrame = { line: 5, column: 1 };
    assert.equal(resolveFrameLocation(frame), undefined);
});

test('resolveFrameLocation: empty file returns undefined (no throw)', () => {
    const frame: TraceFrame = { file: '', line: 1 };
    assert.equal(resolveFrameLocation(frame), undefined);
});

test('resolveFrameLocation: missing line and column default to 0', () => {
    const frame: TraceFrame = { file: '/src/main.go' };
    const loc = resolveFrameLocation(frame);
    assert.ok(loc);
    assert.equal(loc.line, 0);
    assert.equal(loc.column, 0);
});

test('resolveFrameLocation: Windows path frame navigates to correct URI', () => {
    const frame: TraceFrame = { file: 'C:\\Users\\dev\\project\\src\\lib.ts', line: 7, column: 3 };
    const loc = resolveFrameLocation(frame);
    assert.ok(loc);
    assert.equal(loc.uri, 'file:///C:/Users/dev/project/src/lib.ts');
    assert.equal(loc.line, 6);
    assert.equal(loc.column, 2);
});

test('resolveFrameLocation: relative path without workspace root returns a location with the relative URI', () => {
    const frame: TraceFrame = { file: 'src/contract.wasm', line: 1 };
    const loc = resolveFrameLocation(frame);
    // When no workspace root is available, toFileUri returns the relative path.
    // resolveFrameLocation should still return a location rather than undefined.
    assert.ok(loc);
    assert.equal(loc.uri, 'src/contract.wasm');
});

// ── toWorkspaceRelative ───────────────────────────────────────────────────────

test('toWorkspaceRelative: absolute path under root is stripped to relative', () => {
    assert.equal(
        toWorkspaceRelative('/home/user/project/src/main.go', '/home/user/project'),
        'src/main.go',
    );
});

test('toWorkspaceRelative: Windows path under root is normalised and stripped', () => {
    assert.equal(
        toWorkspaceRelative('C:\\dev\\repo\\lib\\util.ts', 'C:\\dev\\repo'),
        'lib/util.ts',
    );
});

test('toWorkspaceRelative: path not under root is returned unchanged', () => {
    const path = '/usr/lib/external/dep.go';
    assert.equal(toWorkspaceRelative(path, '/home/user/project'), path);
});

test('toWorkspaceRelative: trailing slash on root is handled', () => {
    assert.equal(
        toWorkspaceRelative('/root/project/main.go', '/root/project/'),
        'main.go',
    );
});

test('toWorkspaceRelative: exact root path (not a child) is returned unchanged', () => {
    assert.equal(
        toWorkspaceRelative('/root/project', '/root/project'),
        '/root/project',
    );
});

// ── classifyPath ──────────────────────────────────────────────────────────────

import { classifyPath, originLabelText } from './sourceMap';

test('classifyPath: Rust WASM build output is generated', () => {
    assert.equal(
        classifyPath('/project/target/wasm32-unknown-unknown/release/my_contract.wasm'),
        'generated',
    );
});

test('classifyPath: any .wasm extension is generated', () => {
    assert.equal(classifyPath('contract.wasm'), 'generated');
});

test('classifyPath: generic target/ directory is generated', () => {
    assert.equal(classifyPath('/project/target/debug/build/foo.rs'), 'generated');
});

test('classifyPath: Cargo registry path is external', () => {
    assert.equal(
        classifyPath('/home/user/.cargo/registry/src/github.com-1ecc6299db9ec823/serde-1.0.0/src/lib.rs'),
        'external',
    );
});

test('classifyPath: Cargo git checkout is external', () => {
    assert.equal(
        classifyPath('/home/user/.cargo/git/checkouts/soroban-sdk/src/lib.rs'),
        'external',
    );
});

test('classifyPath: user source file under workspace is user', () => {
    assert.equal(
        classifyPath('/project/src/lib.rs', '/project'),
        'user',
    );
});

test('classifyPath: absolute path outside workspace without cargo markers is external', () => {
    assert.equal(
        classifyPath('/usr/lib/stellar/sdk.go', '/home/user/project'),
        'external',
    );
});

test('classifyPath: relative path is treated as user (workspace-relative)', () => {
    assert.equal(classifyPath('src/lib.rs', '/project'), 'user');
});

test('classifyPath: empty path returns unknown', () => {
    assert.equal(classifyPath(''), 'unknown');
});

test('classifyPath: Windows WASM build path is generated', () => {
    assert.equal(
        classifyPath('C:\\Users\\dev\\project\\target\\wasm32-unknown-unknown\\release\\c.wasm'),
        'generated',
    );
});

// ── originLabelText ───────────────────────────────────────────────────────────

test('originLabelText: user source has no decoration', () => {
    assert.equal(originLabelText('user'), '');
});

test('originLabelText: generated source has [generated] label', () => {
    assert.equal(originLabelText('generated'), '[generated]');
});

test('originLabelText: external source has [external] label', () => {
    assert.equal(originLabelText('external'), '[external]');
});

test('originLabelText: unknown origin has [unknown origin] label', () => {
    assert.equal(originLabelText('unknown'), '[unknown origin]');
});
