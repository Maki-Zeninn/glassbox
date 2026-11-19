// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Source-location utilities for VS Code trace navigation.
 *
 * Handles the conversions needed to turn raw frame data (which may carry
 * Unix or Windows paths, 1-based or 0-based offsets, workspace-relative or
 * absolute paths, and generated/external source markers) into the arguments
 * expected by VS Code's vscode.window.showTextDocument / vscode.Uri APIs.
 */

export interface SourceLocation {
    /** A VS Code-compatible URI string (file:///... or a scheme URI). */
    uri: string;
    /** 0-based line number ready to pass to vscode.Position. */
    line: number;
    /** 0-based column number ready to pass to vscode.Position. */
    column: number;
}

export interface TraceFrame {
    /** Raw file path as emitted by the Glassbox CLI. May be absolute, relative,
     *  a Windows path, or a file:// URI. */
    file?: string;
    /** Line number in the source file.  Assumed 1-based unless lineBase is 0. */
    line?: number;
    /** Column number in the source file.  Assumed 1-based unless columnBase is 0. */
    column?: number;
    /** True when the frame originates from a generated (e.g. transpiled) source. */
    generated?: boolean;
    /** True when the frame originates from a path outside the workspace. */
    external?: boolean;
}

/**
 * Convert a raw file path or URI to a VS Code file:// URI string.
 *
 * Rules:
 *  - Already-scheme URIs (file://, Glassbox-state://, …) are returned as-is.
 *  - Windows backslashes are normalised to forward slashes.
 *  - Absolute paths (Unix `/` or Windows `C:\`) are prefixed with file://.
 *  - Relative paths are resolved against workspaceRoot when provided.
 *  - Relative paths with no workspace root are returned unchanged (caller
 *    decides the fallback policy).
 */
export function toFileUri(rawPath: string, workspaceRoot?: string): string {
    if (!rawPath) return '';

    // Already a URI — pass through unchanged.
    if (/^[a-zA-Z][a-zA-Z\d+\-.]*:\/\//.test(rawPath)) {
        return rawPath;
    }

    // Normalise path separators.
    let p = rawPath.replace(/\\/g, '/');

    // Resolve workspace-relative paths.
    if (!isAbsolutePath(p) && workspaceRoot) {
        const root = workspaceRoot.replace(/\\/g, '/').replace(/\/$/, '');
        p = `${root}/${p}`;
    }

    if (isAbsolutePath(p)) {
        // Windows drive letters: C:/foo → file:///C:/foo
        if (/^[A-Za-z]:\//.test(p)) {
            return `file:///${p}`;
        }
        return `file://${p}`;
    }

    // Still relative: return as-is; caller decides what to show.
    return p;
}

/**
 * Convert a 1-based or 0-based line number to the 0-based value expected by
 * vscode.Position.  Out-of-range or undefined values clamp to 0.
 *
 * @param line     Raw line number from the frame.
 * @param inputBase 1 (default) when the CLI emits 1-based numbers, 0 for 0-based.
 */
export function normalizeLine(line: number | undefined | null, inputBase: 0 | 1 = 1): number {
    if (line == null || !isFinite(line)) return 0;
    return Math.max(0, inputBase === 1 ? line - 1 : line);
}

/**
 * Convert a 1-based or 0-based column number to the 0-based value expected by
 * vscode.Position.  Out-of-range or undefined values clamp to 0.
 */
export function normalizeColumn(col: number | undefined | null, inputBase: 0 | 1 = 1): number {
    if (col == null || !isFinite(col)) return 0;
    return Math.max(0, inputBase === 1 ? col - 1 : col);
}

/**
 * Resolve a trace frame to a SourceLocation ready for VS Code navigation.
 *
 * Returns undefined in the following fallback cases so the caller can decide
 * how to render the frame without throwing:
 *  - frame.file is absent or empty.
 *  - toFileUri cannot produce a usable URI (e.g. unresolvable relative path
 *    with no workspace root).
 *
 * Generated and external frames ARE resolved (the documentation policy is to
 * show them, not hide them).  Callers may inspect frame.generated /
 * frame.external to add decorative indicators.
 */
export function resolveFrameLocation(
    frame: TraceFrame,
    workspaceRoot?: string,
): SourceLocation | undefined {
    if (!frame.file) return undefined;

    const uri = toFileUri(frame.file, workspaceRoot);
    if (!uri) return undefined;

    return {
        uri,
        line: normalizeLine(frame.line),
        column: normalizeColumn(frame.column),
    };
}

/**
 * Return the path of file relative to workspaceRoot, or the original string
 * when file is not under the workspace.
 *
 * Handles mixed slash styles on Windows.
 */
export function toWorkspaceRelative(file: string, workspaceRoot: string): string {
    const normalizedFile = file.replace(/\\/g, '/');
    const normalizedRoot = workspaceRoot.replace(/\\/g, '/').replace(/\/$/, '');
    if (normalizedFile.startsWith(normalizedRoot + '/')) {
        return normalizedFile.slice(normalizedRoot.length + 1);
    }
    return file;
}

/**
 * Labels that describe the origin of a source frame.  The values are surfaced
 * as decorative annotations in the VS Code extension and in CLI trace output.
 *
 * Keep in sync with the Go OriginClass constants in
 * `internal/sourcemap/origin.go`.
 */
export type OriginLabel = 'user' | 'generated' | 'external' | 'unknown';

/**
 * Derive a human-readable origin label for a file path so the editor can
 * annotate generated and external frames without hiding them.
 *
 * Classification rules (applied in priority order):
 *  1. Path contains `target/wasm32-unknown-unknown` or ends with `.wasm`
 *     → generated (Rust build output)
 *  2. Path contains a Cargo registry or `.cargo/` prefix
 *     → external (vendored/dependency code)
 *  3. Path contains `target/` (any build directory)
 *     → generated
 *  4. Path is outside the workspace root (when supplied)
 *     → external
 *  5. Otherwise → user
 */
export function classifyPath(
    rawPath: string,
    workspaceRoot?: string,
): OriginLabel {
    if (!rawPath) return 'unknown';

    const p = rawPath.replace(/\\/g, '/');

    // Rust WASM build outputs
    if (
        p.includes('target/wasm32-unknown-unknown') ||
        p.includes('target/wasm32') ||
        p.endsWith('.wasm')
    ) {
        return 'generated';
    }

    // Cargo registry / external crate sources
    if (
        p.includes('/.cargo/registry') ||
        p.includes('/.cargo/git') ||
        p.includes('/registry/src/') ||
        p.includes('.cargo/registry')
    ) {
        return 'external';
    }

    // Generic build directories
    if (p.includes('/target/') || p.startsWith('target/')) {
        return 'generated';
    }

    // Outside workspace
    if (workspaceRoot) {
        const root = workspaceRoot.replace(/\\/g, '/').replace(/\/$/, '');
        if (!p.startsWith(root + '/') && p !== root) {
            // Only label absolute paths that are definitely outside workspace;
            // relative paths are assumed to be workspace-relative.
            if (isAbsolutePath(p)) {
                return 'external';
            }
        }
    }

    return 'user';
}

/**
 * Return the decorative label string for an OriginLabel, matching the
 * formatting used in CLI trace output.
 *
 * Examples:
 *   originLabelText('generated') → '[generated]'
 *   originLabelText('user')      → ''   (no decoration for user source)
 */
export function originLabelText(origin: OriginLabel): string {
    switch (origin) {
        case 'generated': return '[generated]';
        case 'external':  return '[external]';
        case 'unknown':   return '[unknown origin]';
        default:          return '';
    }
}

// ── Internal helpers ──────────────────────────────────────────────────────────

function isAbsolutePath(p: string): boolean {
    return p.startsWith('/') || /^[A-Za-z]:\//.test(p);
}
