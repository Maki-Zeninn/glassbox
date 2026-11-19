// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * Typed streaming API for Glassbox trace events.
 *
 * TraceStream converts a static Trace (or a live AsyncIterable<TraceEvent>)
 * into an ordered sequence of typed events that consumers can process via:
 *   • async iterator  (for await...of): natural pull-based backpressure
 *   • subscribe()     callback adapter: sequential dispatch with cancellation
 *
 * Memory is bounded for slow consumers because events are produced lazily
 * (pull) and subscribe() awaits each callback before requesting the next.
 */

/** Shape compatible with the Trace type from glassboxClient.ts. */
export interface StreamableTraceStep {
    step: number;
    timestamp: string;
    operation: string;
    contract_id?: string;
    function?: string;
    arguments?: unknown[];
    return_value?: unknown;
    error?: string;
    host_state?: unknown;
    memory?: unknown;
    cpu_delta?: number;
    memory_delta?: number;
}

export interface StreamableTrace {
    transaction_hash: string;
    states: StreamableTraceStep[];
}

// ── Discriminated union of all trace event kinds ─────────────────────────────

export interface TraceStepEvent {
    kind: 'step';
    step: number;
    timestamp: string;
    operation: string;
    contract_id?: string;
    function?: string;
    arguments?: unknown[];
    return_value?: unknown;
    cpu_delta?: number;
    memory_delta?: number;
}

export interface TraceHostCallEvent {
    kind: 'hostCall';
    step: number;
    host_state: unknown;
    memory?: unknown;
}

export interface TraceWarningEvent {
    kind: 'warning';
    step: number;
    message: string;
}

export interface TraceCompletionEvent {
    kind: 'completion';
    transaction_hash: string;
    total_steps: number;
}

export interface TraceFailureEvent {
    kind: 'failure';
    step?: number;
    error: string;
}

export type TraceEvent =
    | TraceStepEvent
    | TraceHostCallEvent
    | TraceWarningEvent
    | TraceCompletionEvent
    | TraceFailureEvent;

// ─────────────────────────────────────────────────────────────────────────────

export interface TraceStreamOptions {
    /** AbortSignal to cancel iteration mid-stream. */
    signal?: AbortSignal;
}

/**
 * TraceStream wraps a static Trace or an AsyncIterable<TraceEvent> source and
 * exposes it as an async iterable plus a callback adapter.
 *
 * @example Async iterator (pull, natural backpressure)
 *   const stream = new TraceStream(trace);
 *   for await (const event of stream) { ... }
 *
 * @example Callback adapter (subscribe, sequential)
 *   const cancel = stream.subscribe(async event => { await process(event); });
 *   // call cancel() to stop early
 */
export class TraceStream implements AsyncIterable<TraceEvent> {
    private readonly source: StreamableTrace | AsyncIterable<TraceEvent>;
    private readonly signal?: AbortSignal;

    constructor(
        source: StreamableTrace | AsyncIterable<TraceEvent>,
        options: TraceStreamOptions = {},
    ) {
        this.source = source;
        this.signal = options.signal;
    }

    [Symbol.asyncIterator](): AsyncGenerator<TraceEvent> {
        return this.generate();
    }

    private async *generate(): AsyncGenerator<TraceEvent> {
        const { signal } = this;

        if (isAsyncIterable(this.source)) {
            for await (const event of this.source) {
                if (signal?.aborted) return;
                yield event;
            }
            return;
        }

        const trace = this.source;
        for (const rawStep of trace.states) {
            if (signal?.aborted) return;

            // Always yield the core step event
            yield {
                kind: 'step',
                step: rawStep.step,
                timestamp: rawStep.timestamp,
                operation: rawStep.operation,
                contract_id: rawStep.contract_id,
                function: rawStep.function,
                arguments: rawStep.arguments,
                return_value: rawStep.return_value,
                cpu_delta: rawStep.cpu_delta,
                memory_delta: rawStep.memory_delta,
            } satisfies TraceStepEvent;

            if (signal?.aborted) return;

            // Yield a hostCall event when the step includes host state
            if (rawStep.host_state !== undefined) {
                yield {
                    kind: 'hostCall',
                    step: rawStep.step,
                    host_state: rawStep.host_state,
                    memory: rawStep.memory,
                } satisfies TraceHostCallEvent;
            }

            if (signal?.aborted) return;

            // Yield a warning event when the step records an error
            if (rawStep.error) {
                yield {
                    kind: 'warning',
                    step: rawStep.step,
                    message: rawStep.error,
                } satisfies TraceWarningEvent;
            }
        }

        if (!signal?.aborted) {
            yield {
                kind: 'completion',
                transaction_hash: trace.transaction_hash,
                total_steps: trace.states.length,
            } satisfies TraceCompletionEvent;
        }
    }

    /**
     * Callback adapter: drives the async iterator sequentially, awaiting
     * the handler before requesting the next event.  This ensures memory
     * remains bounded for slow consumers.
     *
     * Returns a cancel function that stops iteration after the current event.
     */
    subscribe(handler: (event: TraceEvent) => void | Promise<void>): () => void {
        let cancelled = false;
        const cancel = (): void => { cancelled = true; };

        (async () => {
            for await (const event of this) {
                if (cancelled) break;
                try {
                    await handler(event);
                } catch {
                    // Malformed or throwing handlers do not crash the stream —
                    // they are skipped so subsequent events still arrive.
                }
                if (cancelled) break;
            }
        })();

        return cancel;
    }
}

function isAsyncIterable(v: unknown): v is AsyncIterable<TraceEvent> {
    return typeof v === 'object' && v !== null && Symbol.asyncIterator in v;
}
